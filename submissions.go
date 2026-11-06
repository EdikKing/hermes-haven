package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"
)

type submission struct {
	Version           int             `json:"version,omitempty"`
	SessionID         string          `json:"session_id"`
	RequestID         string          `json:"request_id"`
	Fingerprint       string          `json:"fingerprint"`
	Scope             string          `json:"scope"`
	State             string          `json:"state"`
	CreatedAt         time.Time       `json:"created_at"`
	RunID             string          `json:"run_id,omitempty"`
	Text              string          `json:"text,omitempty"`
	Images            []string        `json:"images,omitempty"`
	Files             []string        `json:"files,omitempty"`
	ClientFingerprint string          `json:"client_fingerprint,omitempty"`
	FrozenInput       json.RawMessage `json:"frozen_input,omitempty"`
	ContextNote       string          `json:"context_note,omitempty"`
	OutputDir         string          `json:"output_dir,omitempty"`
	Code              int             `json:"code,omitempty"`
	Reply             json.RawMessage `json:"reply,omitempty"`
}

type submissionStore struct {
	mu      sync.RWMutex
	dir     string
	records map[string]submission
}

func digest(value string) string                   { h := sha256.Sum256([]byte(value)); return hex.EncodeToString(h[:]) }
func submissionKey(session, request string) string { return digest(session + "\n" + request) }

func openSubmissions(dir string) (*submissionStore, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	s := &submissionStore{dir: dir, records: map[string]submission{}}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, err
		}
		var rec submission
		if json.Unmarshal(b, &rec) != nil || !validID(rec.SessionID) || !validID(rec.RequestID) || rec.CreatedAt.IsZero() || entry.Name() != submissionKey(rec.SessionID, rec.RequestID)+".json" {
			return nil, errors.New("发送记录损坏，已停止启动以避免重复提交")
		}
		if rec.State != "unknown" && rec.State != "accepted" && rec.State != "rejected" && rec.State != "acknowledged" {
			return nil, errors.New("发送记录状态无效")
		}
		if rec.Version != 0 && rec.Version != 2 {
			return nil, errors.New("发送记录版本不受支持")
		}
		if rec.Version == 2 {
			if !json.Valid(rec.FrozenInput) || rec.ContextNote == "" || rec.OutputDir == "" || rec.ClientFingerprint != clientFingerprint(rec.Text, rec.Images, rec.Files) {
				return nil, errors.New("附件发送记录损坏，已停止启动")
			}
			raw, _ := json.Marshal(map[string]any{"session_id": rec.SessionID, "input": rec.FrozenInput})
			if digest(string(raw)) != rec.Fingerprint {
				return nil, errors.New("冻结的发送内容校验失败")
			}
		}
		if rec.State == "accepted" && (!validID(rec.RunID) || rec.Code != 202 || !json.Valid(rec.Reply)) {
			return nil, errors.New("运行关联记录无效")
		}
		if rec.State == "rejected" && (rec.Code < 400 || rec.Code >= 500 || !json.Valid(rec.Reply)) {
			return nil, errors.New("拒绝记录无效")
		}
		s.records[submissionKey(rec.SessionID, rec.RequestID)] = rec
	}
	return s, nil
}

func (s *submissionStore) save(rec submission) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(s.dir, ".request-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(b)
	}
	if err == nil {
		err = f.Sync()
	}
	if e := f.Close(); err == nil {
		err = e
	}
	if err != nil {
		return err
	}
	key := submissionKey(rec.SessionID, rec.RequestID)
	if err = os.Rename(f.Name(), filepath.Join(s.dir, key+".json")); err != nil {
		return err
	}
	// Linux 部署时同步目录项，保证首次提交前的记录在掉电后仍可找回。
	if runtime.GOOS != "windows" {
		dir, e := os.Open(s.dir)
		if e != nil {
			return e
		}
		e = dir.Sync()
		_ = dir.Close()
		if e != nil {
			return e
		}
	}
	s.records[key] = rec
	return nil
}
func (s *submissionStore) get(session, request string) (submission, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.records[submissionKey(session, request)]
	return r, ok
}
func (s *submissionStore) latest(session string, pendingOnly bool) (submission, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var found submission
	ok := false
	for _, r := range s.records {
		if r.SessionID == session && (!pendingOnly || r.State == "unknown") && (!ok || r.CreatedAt.After(found.CreatedAt)) {
			found = r
			ok = true
		}
	}
	return found, ok
}
func (s *submissionStore) hasRun(session, run string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, r := range s.records {
		if r.SessionID == session && r.RunID == run && r.State == "accepted" {
			return true
		}
	}
	return false
}

func (s *server) canRetry(ctx context.Context, rec submission) bool {
	if rec.Scope != digest(s.hermesURL+"\n"+s.apiKey) || time.Since(rec.CreatedAt) < 0 {
		return false
	}
	res, err := s.upstream(ctx, "GET", "/v1/capabilities", nil, nil)
	if err != nil {
		return false
	}
	defer res.Body.Close()
	var caps struct {
		Features struct {
			Idempotency struct {
				Supported bool `json:"supported"`
				Durable   bool `json:"durable"`
				Retention int  `json:"retention_seconds"`
			} `json:"runs_idempotency"`
		} `json:"features"`
	}
	if res.StatusCode != 200 || json.NewDecoder(io.LimitReader(res.Body, 64<<10)).Decode(&caps) != nil {
		return false
	}
	i := caps.Features.Idempotency
	return i.Supported && i.Durable && i.Retention > 60 && time.Since(rec.CreatedAt) < time.Duration(i.Retention-60)*time.Second
}

func (s *server) submit(w http.ResponseWriter, r *http.Request, id, requestID, text string, images, files []string, createdAt int64) {
	lock, _ := s.submitLocks.LoadOrStore(id, &sync.Mutex{})
	mu := lock.(*sync.Mutex)
	mu.Lock()
	defer mu.Unlock()
	rec, exists := s.submissions.get(id, requestID)
	if exists && rec.State == "acknowledged" {
		jsonOut(w, 409, map[string]any{"error": "此请求已由用户核对并关闭，不能再次执行", "code": "request_acknowledged"})
		return
	}
	var input any
	contextNote, outputDir := "", ""
	if exists && rec.Version == 2 {
		if rec.ClientFingerprint != clientFingerprint(text, images, files) {
			jsonOut(w, 409, map[string]any{"error": "同一请求编号不能用于不同消息或附件", "code": "request_conflict"})
			return
		}
		input = rec.FrozenInput
	} else {
		if exists && len(files) > 0 {
			jsonOut(w, 409, map[string]any{"error": "原请求不包含这些附件", "code": "request_conflict"})
			return
		}
		var err error
		input, contextNote, outputDir, err = s.submissionInput(id, requestID, text, images, files, exists)
		if err != nil {
			fail(w, 400, err.Error())
			return
		}
	}
	payload := map[string]any{"session_id": id, "input": input}
	raw, _ := json.Marshal(payload)
	fingerprint := digest(string(raw))
	if exists && rec.Fingerprint != fingerprint {
		jsonOut(w, 409, map[string]any{"error": "同一请求编号不能用于不同消息", "code": "request_conflict"})
		return
	}
	if exists && rec.State != "unknown" {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(rec.Code)
		_, _ = w.Write(rec.Reply)
		return
	}
	if pending, ok := s.submissions.latest(id, true); ok && pending.RequestID != requestID {
		jsonOut(w, 409, map[string]any{"error": "此会话有结果待核对的发送，请先核对原请求", "code": "submission_pending", "request_id": pending.RequestID})
		return
	}
	if exists && !s.canRetry(r.Context(), rec) {
		jsonOut(w, 409, map[string]any{"error": "原提交结果仍未知，当前不能安全重试。请核对历史和运行状态，暂不再次执行。", "code": "retry_unsafe", "request_id": requestID})
		return
	}
	if exists && rec.Version == 2 {
		// 结果未知时仍可能首次执行；文件内容变化后不能只凭相同路径再次提交。
		for _, fileID := range append(append([]string{}, rec.Images...), rec.Files...) {
			a, err := s.attachment(fileID)
			if err == nil {
				var f *os.File
				f, err = s.openAttachment(a)
				if err == nil {
					err = f.Close()
				}
			}
			if err != nil {
				jsonOut(w, 409, map[string]any{"error": "原附件缺失或内容已变化，不能安全重试，请先核对原会话历史", "code": "retry_unsafe", "request_id": requestID})
				return
			}
		}
	}
	if !exists {
		started := time.Now().UTC()
		if createdAt > 0 {
			clientTime := time.UnixMilli(createdAt)
			if clientTime.After(started.Add(5 * time.Minute)) {
				fail(w, 400, "请求时间无效，请检查设备时间")
				return
			}
			if started.Sub(clientTime) > 24*time.Hour-time.Minute {
				jsonOut(w, 409, map[string]any{"error": "此待确认请求已超过安全重试期限，请先核对历史，不会再次执行", "code": "retry_unsafe"})
				return
			}
			if clientTime.Before(started) {
				started = clientTime
			}
		}
		frozen, _ := json.Marshal(input)
		rec = submission{Version: 2, SessionID: id, RequestID: requestID, Fingerprint: fingerprint, Scope: digest(s.hermesURL + "\n" + s.apiKey), State: "unknown", CreatedAt: started, Text: text, Images: images, Files: files, ClientFingerprint: clientFingerprint(text, images, files), FrozenInput: frozen, ContextNote: contextNote, OutputDir: outputDir}
		if err := s.submissions.save(rec); err != nil {
			fail(w, 503, "无法保存发送记录，本次未提交给 Hermes")
			return
		}
	}
	// 先落盘，再提交；浏览器断开也继续核对本次响应。恢复过程只读，重试必须由用户点击。
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 35*time.Second)
	defer cancel()
	h := http.Header{}
	h.Set("Idempotency-Key", "haven_"+submissionKey(id, requestID))
	res, err := s.upstream(ctx, "POST", "/v1/runs", payload, h)
	unknown := func() {
		jsonOut(w, 502, map[string]any{"error": "提交结果尚未确认，发送记录已保留。请先核对结果，或用原请求安全重试。", "code": "submission_unknown", "request_id": requestID})
	}
	if err != nil {
		unknown()
		return
	}
	defer res.Body.Close()
	var reply map[string]any
	if json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&reply) != nil {
		unknown()
		return
	}
	if res.StatusCode >= 500 {
		unknown()
		return
	}
	if res.StatusCode < 400 && res.StatusCode != 202 {
		unknown()
		return
	}
	if res.StatusCode == 202 {
		run, ok := reply["run_id"].(string)
		if !ok || !validID(run) {
			unknown()
			return
		}
		rec.State = "accepted"
		rec.RunID = run
		if rec.Version == 0 {
			rec.Text = ""
			rec.Images = nil
		}
	} else {
		// 已经未知的请求不能因后续认证或配置变化而被认定为未执行。
		if exists {
			unknown()
			return
		}
		rec.State = "rejected"
		if e, ok := reply["error"].(map[string]any); ok && e["code"] == "permission_denied" {
			reply = map[string]any{"error": "Hermes 拒绝继续此会话：该会话由其他入口管理。", "code": "permission_denied"}
		}
	}
	rec.Code = res.StatusCode
	rec.Reply, _ = json.Marshal(reply)
	if err := s.submissions.save(rec); err != nil {
		unknown()
		return
	}
	if rec.State == "accepted" {
		s.mu.Lock()
		own, ok := s.st.Sessions[id]
		if !ok {
			own.External = true
		}
		own.RunID = rec.RunID
		if own.Title == "新会话" && text != "" {
			rr := []rune(text)
			if len(rr) > 28 {
				rr = rr[:28]
			}
			own.Title = string(rr)
		}
		s.st.Sessions[id] = own
		if err := s.saveLocked(); err != nil {
			reply["warning"] = "运行已登记，标题状态暂未保存"
		}
		s.mu.Unlock()
	}
	jsonOut(w, res.StatusCode, reply)
}

func (s *server) requestStatus(w http.ResponseWriter, r *http.Request, id, requestID string) {
	rec, ok := s.submissions.get(id, requestID)
	if !ok {
		jsonOut(w, 404, map[string]any{"error": "未找到发送记录", "code": "request_not_found"})
		return
	}
	v := map[string]any{"state": rec.State, "request_id": rec.RequestID, "run_id": rec.RunID, "code": rec.Code, "created_at": rec.CreatedAt}
	if rec.State == "unknown" {
		v["text"] = rec.Text
		v["images"] = rec.Images
		v["files"] = rec.Files
	}
	if rec.State == "rejected" {
		var reply any
		_ = json.Unmarshal(rec.Reply, &reply)
		v["response"] = reply
	}
	jsonOut(w, 200, v)
}
func (s *server) acknowledgeSubmission(w http.ResponseWriter, r *http.Request, id, requestID string) {
	var body struct {
		Confirmed bool `json:"confirmed"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&body) != nil || !body.Confirmed {
		fail(w, 400, "需要明确确认已核对原请求")
		return
	}
	lock, _ := s.submitLocks.LoadOrStore(id, &sync.Mutex{})
	mu := lock.(*sync.Mutex)
	mu.Lock()
	defer mu.Unlock()
	rec, ok := s.submissions.get(id, requestID)
	if !ok {
		jsonOut(w, 200, map[string]any{"ok": true})
		return
	}
	if rec.State != "unknown" && rec.State != "acknowledged" {
		fail(w, 409, "原请求已有明确结果，请重新核对")
		return
	}
	rec.State = "acknowledged"
	if err := s.submissions.save(rec); err != nil {
		fail(w, 503, "确认状态未保存，请稍后重试")
		return
	}
	jsonOut(w, 200, map[string]any{"ok": true})
}
func (s *server) sessionRun(id string) string {
	s.submissions.mu.RLock()
	var latest submission
	for _, rec := range s.submissions.records {
		if rec.SessionID == id && rec.State == "accepted" && (latest.RunID == "" || rec.CreatedAt.After(latest.CreatedAt)) {
			latest = rec
		}
	}
	s.submissions.mu.RUnlock()
	if latest.RunID != "" {
		return latest.RunID
	}
	own, _ := s.owned(id)
	return own.RunID
}
func (s *server) sessionStatus(w http.ResponseWriter, r *http.Request, id string) {
	if rec, ok := s.submissions.latest(id, true); ok {
		jsonOut(w, 200, map[string]any{"status": "submission_unknown", "request_id": rec.RequestID, "text": rec.Text, "images": rec.Images, "files": rec.Files, "created_at": rec.CreatedAt})
		return
	}
	run := s.sessionRun(id)
	if run == "" {
		jsonOut(w, 200, map[string]any{"status": "idle"})
		return
	}
	s.proxyJSON(w, r, "GET", "/v1/runs/"+url.PathEscape(run), nil)
}
func envQuery(r *http.Request, key, def string) string {
	if v := r.URL.Query().Get(key); v != "" {
		return v
	}
	return def
}
