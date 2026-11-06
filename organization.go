package main

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"unicode"
)

type sessionGroup struct {
	Name string `json:"name"`
}

type sessionOrganization struct {
	GroupID  string `json:"group_id"`
	Archived bool   `json:"archived"`
}

func (s *server) initOrganization() {
	if s.st.Groups == nil {
		s.st.Groups = map[string]sessionGroup{}
	}
	if s.st.Organization == nil {
		s.st.Organization = map[string]sessionOrganization{}
	}
	if s.st.Deletions == nil {
		s.st.Deletions = map[string]string{}
	}
}

func (s *server) groupRowsLocked() []map[string]string {
	rows := make([]map[string]string, 0, len(s.st.Groups))
	for id, group := range s.st.Groups {
		rows = append(rows, map[string]string{"id": id, "name": group.Name})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i]["name"] == rows[j]["name"] {
			return rows[i]["id"] < rows[j]["id"]
		}
		return rows[i]["name"] < rows[j]["name"]
	})
	return rows
}

func decodeOrganizationBody(w http.ResponseWriter, r *http.Request, value any) bool {
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	d.DisallowUnknownFields()
	if d.Decode(value) != nil || d.Decode(new(any)) != io.EOF {
		fail(w, 400, "请求参数无效")
		return false
	}
	return true
}

func (s *server) groups(w http.ResponseWriter, r *http.Request, id string) {
	var body struct {
		Name string `json:"name"`
	}
	if r.Method == "POST" && id == "" || r.Method == "PUT" && id != "" {
		if !decodeOrganizationBody(w, r, &body) {
			return
		}
		body.Name = strings.TrimSpace(body.Name)
		if body.Name == "" || len([]rune(body.Name)) > 40 || strings.IndexFunc(body.Name, unicode.IsControl) >= 0 {
			fail(w, 400, "分组名称应为 1 至 40 个字符")
			return
		}
	} else if !(r.Method == "GET" && id == "" || r.Method == "DELETE" && id != "") {
		fail(w, 405, "不支持此操作")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if r.Method == "GET" {
		jsonOut(w, 200, map[string]any{"data": s.groupRowsLocked()})
		return
	}
	old, exists := s.st.Groups[id]
	if id != "" && !exists {
		fail(w, 404, "分组不存在")
		return
	}
	if r.Method != "DELETE" {
		for key, group := range s.st.Groups {
			if key != id && strings.EqualFold(group.Name, body.Name) {
				fail(w, 409, "已有同名分组")
				return
			}
		}
	}
	previous := map[string]sessionOrganization{}
	if r.Method == "DELETE" {
		delete(s.st.Groups, id)
		for key, meta := range s.st.Organization {
			if meta.GroupID == id {
				previous[key] = meta
				meta.GroupID = ""
				s.st.Organization[key] = meta
			}
		}
	} else {
		if id == "" {
			id = randomID(16)
		}
		s.st.Groups[id] = sessionGroup{Name: body.Name}
	}
	if s.saveLocked() != nil {
		if exists {
			s.st.Groups[id] = old
		} else {
			delete(s.st.Groups, id)
		}
		for key, meta := range previous {
			s.st.Organization[key] = meta
		}
		fail(w, 500, "分组未保存，请重试")
		return
	}
	code := 200
	if r.Method == "POST" {
		code = 201
	}
	jsonOut(w, code, map[string]any{"id": id, "name": body.Name, "groups": s.groupRowsLocked()})
}

func (s *server) sessionUnavailable(w http.ResponseWriter, id string) bool {
	s.mu.Lock()
	stage := s.st.Deletions[id]
	s.mu.Unlock()
	if stage == "deleted" {
		fail(w, 404, "会话已彻底删除")
		return true
	}
	if stage != "" {
		fail(w, 409, "删除尚未完成，请重试彻底删除")
		return true
	}
	return false
}

// 分组与归档独立于会话归属，整理历史来源不会将其登记为 Haven 创建的会话。
func (s *server) organizeSession(w http.ResponseWriter, r *http.Request, id string) {
	var body struct {
		GroupID  *string `json:"group_id"`
		Archived *bool   `json:"archived"`
	}
	if !decodeOrganizationBody(w, r, &body) {
		return
	}
	if body.GroupID == nil && body.Archived == nil {
		fail(w, 400, "请选择分组或归档操作")
		return
	}
	lock, _ := s.submitLocks.LoadOrStore(id, &sync.Mutex{})
	mu := lock.(*sync.Mutex)
	mu.Lock()
	defer mu.Unlock()
	if s.sessionUnavailable(w, id) {
		return
	}
	res, err := s.upstream(r.Context(), "GET", "/api/sessions/"+url.PathEscape(id), nil, nil)
	if err != nil {
		fail(w, 502, "无法核对会话，请稍后重试")
		return
	}
	res.Body.Close()
	if res.StatusCode != 200 {
		fail(w, res.StatusCode, "会话不存在或暂不可用")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	old, existed := s.st.Organization[id]
	meta := old
	if body.GroupID != nil {
		if _, ok := s.st.Groups[*body.GroupID]; *body.GroupID != "" && !ok {
			fail(w, 404, "分组不存在")
			return
		}
		meta.GroupID = *body.GroupID
	}
	if body.Archived != nil {
		meta.Archived = *body.Archived
	}
	s.st.Organization[id] = meta
	if s.saveLocked() != nil {
		if existed {
			s.st.Organization[id] = old
		} else {
			delete(s.st.Organization, id)
		}
		fail(w, 500, "会话整理未保存，请重试")
		return
	}
	jsonOut(w, 200, meta)
}

func (s *server) markDeletion(id, stage string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	old := s.st.Deletions[id]
	if stage == "" {
		delete(s.st.Deletions, id)
	} else {
		s.st.Deletions[id] = stage
	}
	if err := s.saveLocked(); err != nil {
		if old == "" {
			delete(s.st.Deletions, id)
		} else {
			s.st.Deletions[id] = old
		}
		return err
	}
	return nil
}

func (s *server) deleteSession(w http.ResponseWriter, r *http.Request, id string) {
	var body struct {
		Confirmed bool `json:"confirmed"`
	}
	if !decodeOrganizationBody(w, r, &body) {
		return
	}
	if !body.Confirmed {
		fail(w, 400, "请明确确认彻底删除，此操作无法恢复")
		return
	}
	lock, _ := s.submitLocks.LoadOrStore(id, &sync.Mutex{})
	mu := lock.(*sync.Mutex)
	mu.Lock()
	defer mu.Unlock()
	s.mu.Lock()
	stage := s.st.Deletions[id]
	s.mu.Unlock()
	if stage == "deleted" {
		jsonOut(w, 200, map[string]bool{"deleted": true})
		return
	}
	if stage == "" {
		if _, pending := s.submissions.latest(id, true); pending {
			fail(w, 409, "发送结果尚未确认，请先核对原请求")
			return
		}
		if run := s.sessionRun(id); run != "" {
			res, err := s.upstream(r.Context(), "GET", "/v1/runs/"+url.PathEscape(run), nil, nil)
			if err != nil {
				fail(w, 502, "无法核对运行状态，尚未删除")
				return
			}
			var status struct {
				Status string `json:"status"`
			}
			err = json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&status)
			res.Body.Close()
			if res.StatusCode != 404 && (res.StatusCode != 200 || err != nil) {
				fail(w, 502, "无法核对运行状态，尚未删除")
				return
			}
			if res.StatusCode == 200 && status.Status != "completed" && status.Status != "failed" && status.Status != "cancelled" && status.Status != "interrupted" && status.Status != "rejected" {
				fail(w, 409, "会话仍在运行或状态待核对，请先停止并确认状态")
				return
			}
		}
		// 先持久化删除意图，防止网络中断或重启后重新向原会话发送消息。
		if s.markDeletion(id, "pending") != nil {
			fail(w, 500, "删除状态未保存，尚未删除")
			return
		}
	}
	res, err := s.upstream(r.Context(), "DELETE", "/api/sessions/"+url.PathEscape(id), nil, nil)
	if err != nil {
		fail(w, 502, "删除结果尚未确认，请重试彻底删除")
		return
	}
	defer res.Body.Close()
	var reply struct {
		Deleted *bool `json:"deleted"`
	}
	decodeErr := json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&reply)
	if res.StatusCode != 200 && res.StatusCode != 204 && res.StatusCode != 404 {
		if res.StatusCode >= 400 && res.StatusCode < 500 {
			if s.markDeletion(id, "") != nil {
				fail(w, 500, "删除未完成，状态保存失败，请重试")
				return
			}
		}
		if res.StatusCode == 409 {
			jsonOut(w, 409, map[string]any{"error": "Hermes 中的会话仍在使用，请先结束运行再删除", "deletion_pending": false})
		} else {
			jsonOut(w, 502, map[string]any{"error": "Hermes 未完成删除，请稍后重试", "deletion_pending": res.StatusCode >= 500})
		}
		return
	}
	if res.StatusCode == 200 && (decodeErr != nil || reply.Deleted == nil || !*reply.Deleted) {
		fail(w, 502, "Hermes 尚未确认删除，请重试")
		return
	}
	if res.StatusCode == 404 {
		// 旧版服务也可能因缺少删除路由返回 404，必须再确认会话本身已经不存在。
		check, err := s.upstream(r.Context(), "GET", "/api/sessions/"+url.PathEscape(id), nil, nil)
		if err != nil {
			fail(w, 502, "无法确认会话已删除，请重试")
			return
		}
		check.Body.Close()
		if check.StatusCode != 404 {
			if check.StatusCode == 200 {
				if s.markDeletion(id, "") != nil {
					fail(w, 500, "删除未完成，状态保存失败，请重试")
					return
				}
			}
			jsonOut(w, 502, map[string]any{"error": "Hermes 未确认会话已删除，请检查删除接口后重试", "deletion_pending": check.StatusCode != 200})
			return
		}
	}
	if s.purgeSessionData(id) != nil {
		fail(w, 500, "Hermes 会话已删除，本地清理尚未完成，请重试彻底删除")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	old, owned := s.st.Sessions[id]
	meta, organized := s.st.Organization[id]
	delete(s.st.Sessions, id)
	delete(s.st.Organization, id)
	// 只保留不可恢复的会话编号，阻止过期页面或上游迟到写入使会话重新出现。
	s.st.Deletions[id] = "deleted"
	if s.saveLocked() != nil {
		if owned {
			s.st.Sessions[id] = old
		}
		if organized {
			s.st.Organization[id] = meta
		}
		s.st.Deletions[id] = "pending"
		fail(w, 500, "删除状态保存未完成，请重试彻底删除")
		return
	}
	jsonOut(w, 200, map[string]bool{"deleted": true})
}

// 只移除数据目录内的已知路径，不跟随中间目录的符号链接。
func (s *server) removeManagedPath(relative string) error {
	base, err := filepath.EvalSymlinks(s.dataDir)
	if err != nil {
		return err
	}
	base, err = filepath.Abs(base)
	if err != nil {
		return err
	}
	target := filepath.Join(base, relative)
	rel, err := filepath.Rel(base, target)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || filepath.IsAbs(rel) {
		return errors.New("清理路径越界")
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(target))
	if os.IsNotExist(err) {
		return nil
	}
	same := parent == filepath.Dir(target)
	if runtime.GOOS == "windows" {
		same = strings.EqualFold(parent, filepath.Dir(target))
	}
	if err != nil || !same {
		return errors.New("清理目录不可用")
	}
	return os.RemoveAll(target)
}

func (s *server) purgeSessionData(id string) error {
	s.attachmentUseMu.Lock()
	defer s.attachmentUseMu.Unlock()
	s.artifactMu.Lock()
	defer s.artifactMu.Unlock()
	s.submissions.mu.Lock()
	defer s.submissions.mu.Unlock()
	candidates, shared := map[string]bool{}, map[string]bool{}
	for _, rec := range s.submissions.records {
		for _, file := range append(append([]string{}, rec.Images...), rec.Files...) {
			if rec.SessionID == id {
				candidates[file] = true
			} else {
				shared[file] = true
			}
		}
	}
	links, err := os.ReadDir(filepath.Join(s.dataDir, "file-links"))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if err != nil {
		if _, statErr := os.Lstat(filepath.Join(s.dataDir, "file-links")); statErr == nil || !os.IsNotExist(statErr) {
			return err
		}
	}
	var removedLinks []string
	for _, link := range links {
		if link.IsDir() || filepath.Ext(link.Name()) != ".json" {
			continue
		}
		f, err := openConfined(filepath.Join(s.dataDir, "file-links"), link.Name())
		if err != nil {
			return err
		}
		var binding struct{ ID, Session string }
		err = json.NewDecoder(io.LimitReader(f, 16<<10)).Decode(&binding)
		f.Close()
		if err != nil {
			return err
		}
		if binding.Session == id {
			candidates[binding.ID] = true
			removedLinks = append(removedLinks, link.Name())
		} else {
			shared[binding.ID] = true
		}
	}
	for file := range candidates {
		if shared[file] || !uploadID.MatchString(file) {
			continue
		}
		for _, dir := range []string{"files", "uploads"} {
			if err := s.removeManagedPath(filepath.Join(dir, file)); err != nil {
				return err
			}
		}
	}
	for _, name := range removedLinks {
		if err := s.removeManagedPath(filepath.Join("file-links", name)); err != nil {
			return err
		}
	}
	// 发送记录最后移除，前面清理失败或进程退出时仍可按原关联重试。
	for key, rec := range s.submissions.records {
		if rec.SessionID != id {
			continue
		}
		if err := s.removeManagedPath(filepath.Join("outputs", key)); err != nil {
			return err
		}
		if err := s.removeManagedPath(filepath.Join("requests", key+".json")); err != nil {
			return err
		}
		delete(s.submissions.records, key)
	}
	return nil
}
