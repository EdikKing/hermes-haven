package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func createTestGroup(t *testing.T, s *server, name string) string {
	t.Helper()
	w := authorized(s, "POST", "/api/groups", fmt.Sprintf(`{"name":%q}`, name))
	if w.Code != 201 {
		t.Fatalf("创建分组失败: %d %s", w.Code, w.Body.String())
	}
	var body struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(w.Body.Bytes(), &body) != nil || body.ID == "" {
		t.Fatal("分组编号缺失")
	}
	return body.ID
}

func TestGroupArchivePersistsWithoutChangingHistoryOwnership(t *testing.T) {
	s, _ := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Errorf("整理会话不应写入 Hermes: %s", r.Method)
		}
		if r.URL.Path == "/api/sessions" {
			fmt.Fprint(w, `{"data":[{"id":"old_cli","title":"历史","source":"cli"},{"id":"mine"}],"has_more":false}`)
		} else {
			fmt.Fprint(w, `{"session":{"id":"old_cli"}}`)
		}
	})
	if err := s.setOwned("mine", ownedSession{Title: "我的"}); err != nil {
		t.Fatal(err)
	}
	id := createTestGroup(t, s, "项目甲")
	w := authorized(s, "PUT", "/api/sessions/old_cli/organization", fmt.Sprintf(`{"group_id":%q,"archived":true}`, id))
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if _, owned := s.owned("old_cli"); owned {
		t.Fatal("历史会话被错误登记为自有会话")
	}
	restarted, err := newServer()
	if err != nil {
		t.Fatal(err)
	}
	w = authorized(restarted, "GET", "/api/sessions", "")
	var result struct {
		Data []struct {
			ID       string
			Owned    bool
			Archived bool
			GroupID  string `json:"group_id"`
		}
		Groups []sessionGroup
	}
	if json.Unmarshal(w.Body.Bytes(), &result) != nil || len(result.Data) != 2 || result.Data[0].Owned || !result.Data[0].Archived || result.Data[0].GroupID != id || !result.Data[1].Owned || len(result.Groups) != 1 {
		t.Fatalf("重启后整理状态不正确: %s", w.Body.String())
	}
	w = authorized(restarted, "POST", "/api/sessions/old_cli/runs", `{"text":"归档后不能发送","request_id":"new-request"}`)
	if w.Code != 409 {
		t.Fatalf("归档会话仍可发送: %d", w.Code)
	}
	w = authorized(restarted, "PUT", "/api/groups/"+id, `{"name":"项目乙"}`)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	w = authorized(restarted, "DELETE", "/api/groups/"+id, "")
	if w.Code != 200 || restarted.st.Organization["old_cli"].GroupID != "" || !restarted.st.Organization["old_cli"].Archived {
		t.Fatal("删除分组改变了会话或归档状态")
	}
	w = authorized(restarted, "PUT", "/api/sessions/old_cli/organization", `{"archived":false,"group_id":""}`)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"group_id":""`) || restarted.st.Organization["old_cli"].Archived {
		t.Fatal("会话未恢复或未分组状态缺失")
	}
}

func TestOrganizationValidationAndFailedSaveRollBack(t *testing.T) {
	s, _ := testServer(t, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{}`) })
	id := createTestGroup(t, s, "项目")
	for _, body := range []string{`{"name":""}`, `{"name":"\n"}`, `{"name":"项目","extra":true}`} {
		if w := authorized(s, "POST", "/api/groups", body); w.Code != 400 {
			t.Fatalf("应拒绝无效分组: %s", body)
		}
	}
	if w := authorized(s, "POST", "/api/groups", `{"name":"项目"}`); w.Code != 409 {
		t.Fatal("同名分组未拒绝")
	}
	if w := authorized(s, "PUT", "/api/sessions/old/organization", `{"group_id":"missing"}`); w.Code != 404 {
		t.Fatal("不存在的分组未拒绝")
	}
	if w := authorized(s, "PUT", "/api/sessions/old/organization", `{"archived":"yes"}`); w.Code != 400 {
		t.Fatal("无效归档标记未拒绝")
	}
	s.st.Organization["old"] = sessionOrganization{GroupID: id, Archived: true}
	s.dataDir = filepath.Join(s.dataDir, "missing")
	if w := authorized(s, "DELETE", "/api/groups/"+id, ""); w.Code != 500 {
		t.Fatal("分组存储失败伪报成功")
	}
	if s.st.Groups[id].Name != "项目" || s.st.Organization["old"].GroupID != id {
		t.Fatal("失败后未恢复分组关联")
	}
	if w := authorized(s, "PUT", "/api/sessions/old/organization", `{"archived":false}`); w.Code != 500 || !s.st.Organization["old"].Archived {
		t.Fatal("归档保存失败未回滚")
	}
}

func TestPermanentDeletePurgesHistoryAndLocalDataPreservingSharedFiles(t *testing.T) {
	var deletes atomic.Int32
	s, _ := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "DELETE" && r.URL.Path == "/api/sessions/old" {
			deletes.Add(1)
			fmt.Fprint(w, `{"deleted":true}`)
			return
		}
		if r.Method == "GET" && r.URL.Path == "/api/sessions" {
			fmt.Fprint(w, `{"data":[{"id":"old"},{"id":"other"}],"has_more":false}`)
			return
		}
		t.Errorf("意外上游访问: %s %s", r.Method, r.URL.Path)
	})
	private, err := s.storeAttachment(strings.NewReader("仅属于删除会话"), "private.txt", false)
	if err != nil {
		t.Fatal(err)
	}
	shared, err := s.storeAttachment(strings.NewReader("共用附件"), "shared.txt", false)
	if err != nil {
		t.Fatal(err)
	}
	generated, err := s.storeAttachment(strings.NewReader("生成文件"), "generated.txt", false)
	if err != nil {
		t.Fatal(err)
	}
	for _, rec := range []submission{
		{SessionID: "old", RequestID: "one", State: "acknowledged", CreatedAt: time.Now(), Text: "应清理的正文", Files: []string{private.ID, shared.ID}},
		{SessionID: "other", RequestID: "two", State: "acknowledged", CreatedAt: time.Now(), Files: []string{shared.ID}},
	} {
		if err = s.submissions.save(rec); err != nil {
			t.Fatal(err)
		}
	}
	id := createTestGroup(t, s, "保留分组")
	s.st.Organization["old"] = sessionOrganization{GroupID: id, Archived: true}
	if err = s.setOwned("old", ownedSession{Title: "应清理的标题", External: true}); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(s.dataDir, "outputs", submissionKey("old", "one"))
	if err = os.MkdirAll(output, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(output, "output.txt"), []byte("输出"), 0600); err != nil {
		t.Fatal(err)
	}
	links := filepath.Join(s.dataDir, "file-links")
	if err = os.MkdirAll(links, 0700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(links, digest("link")+".json")
	if err = writeAtomicJSON(link, map[string]string{"ID": generated.ID, "Session": "old"}); err != nil {
		t.Fatal(err)
	}
	w := authorized(s, "DELETE", "/api/sessions/old", `{"confirmed":true}`)
	if w.Code != 200 {
		t.Fatalf("删除失败: %d %s", w.Code, w.Body.String())
	}
	for _, path := range []string{output, link, filepath.Join(s.dataDir, "files", private.ID), filepath.Join(s.dataDir, "files", generated.ID), filepath.Join(s.dataDir, "requests", submissionKey("old", "one")+".json")} {
		if _, err = os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("删除后仍有会话数据: %s", path)
		}
	}
	if _, err = s.attachment(shared.ID); err != nil {
		t.Fatal("误删了共用附件")
	}
	if _, ok := s.submissions.get("other", "two"); !ok {
		t.Fatal("误删了其他会话的发送记录")
	}
	restarted, err := newServer()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := restarted.st.Sessions["old"]; ok {
		t.Fatal("仍保留会话标题和归属")
	}
	if _, ok := restarted.st.Organization["old"]; ok || restarted.st.Groups[id].Name != "保留分组" {
		t.Fatal("会话整理数据未清理或误删分组")
	}
	for _, path := range []string{"/api/sessions/old/messages", "/api/sessions/old/status", "/api/sessions/old/settings"} {
		if w = authorized(restarted, "GET", path, ""); w.Code != 404 {
			t.Fatalf("过期页面仍可读取删除会话: %s", path)
		}
	}
	if w = authorized(restarted, "POST", "/api/sessions/old/runs", `{"text":"过期页面","request_id":"late"}`); w.Code != 404 {
		t.Fatal("过期页面仍可继续发送")
	}
	w = authorized(restarted, "GET", "/api/sessions", "")
	if strings.Contains(w.Body.String(), `"id":"old"`) {
		t.Fatal("上游迟到记录重新出现")
	}
	w = authorized(restarted, "DELETE", "/api/sessions/old", `{"confirmed":true}`)
	if w.Code != 200 || deletes.Load() != 1 {
		t.Fatal("重复删除未保持幂等")
	}
}

func TestDeleteRejectsUnconfirmedUnknownAndActiveSessions(t *testing.T) {
	var deletes atomic.Int32
	s, _ := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" && r.URL.Path == "/api/sessions" {
			fmt.Fprint(w, `{"data":[],"has_more":false}`)
			return
		}
		if r.Method == "DELETE" {
			deletes.Add(1)
			fmt.Fprint(w, `{"deleted":true}`)
			return
		}
		fmt.Fprint(w, `{"status":"running"}`)
	})
	if w := authorized(s, "DELETE", "/api/sessions/old", `{}`); w.Code != 400 {
		t.Fatal("未确认便删除")
	}
	if err := s.submissions.save(submission{SessionID: "unknown", RequestID: "request", State: "unknown", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if w := authorized(s, "DELETE", "/api/sessions/unknown", `{"confirmed":true}`); w.Code != 409 {
		t.Fatal("结果待核对会话被删除")
	}
	if err := s.setOwned("active", ownedSession{RunID: "running"}); err != nil {
		t.Fatal(err)
	}
	if w := authorized(s, "DELETE", "/api/sessions/active", `{"confirmed":true}`); w.Code != 409 {
		t.Fatal("活动会话被删除")
	}
	if deletes.Load() != 0 {
		t.Fatal("不满足条件时调用了上游删除")
	}
}

func TestDeleteFailureAndLostResponseCanBeRetriedAfterRestart(t *testing.T) {
	for _, firstCode := range []int{403, 409, 503, 200} {
		t.Run(fmt.Sprint(firstCode), func(t *testing.T) {
			calls := 0
			s, _ := testServer(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "GET" && r.URL.Path == "/api/sessions/old" {
					w.WriteHeader(404)
					fmt.Fprint(w, `{"error":"not_found"}`)
					return
				}
				if r.Method != "DELETE" {
					t.Fatal("不应有其他上游操作")
				}
				calls++
				if calls == 1 {
					w.WriteHeader(firstCode)
					fmt.Fprint(w, `{"deleted":false}`)
				} else {
					w.WriteHeader(404)
					fmt.Fprint(w, `{"error":"not_found"}`)
				}
			})
			if err := s.setOwned("old", ownedSession{Title: "仍需保留"}); err != nil {
				t.Fatal(err)
			}
			w := authorized(s, "DELETE", "/api/sessions/old", `{"confirmed":true}`)
			if w.Code == 200 {
				t.Fatal("删除失败或 deleted=false 时伪报成功")
			}
			if _, ok := s.owned("old"); !ok {
				t.Fatal("失败时提前移除了本地状态")
			}
			if firstCode == 403 || firstCode == 409 {
				if s.st.Deletions["old"] != "" {
					t.Fatal("明确拒绝后会话仍被锁定")
				}
			} else if s.st.Deletions["old"] != "pending" {
				t.Fatal("不确定结果没有保留删除意图")
			}
			restarted, err := newServer()
			if err != nil {
				t.Fatal(err)
			}
			w = authorized(restarted, "DELETE", "/api/sessions/old", `{"confirmed":true}`)
			if w.Code != 200 || restarted.st.Deletions["old"] != "deleted" {
				t.Fatalf("重启后无法重试清理: %d %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestDeletionCannotEscapeManagedDataDirectory(t *testing.T) {
	s, _ := testServer(t, func(http.ResponseWriter, *http.Request) { t.Fatal("不应访问上游") })
	outside := filepath.Join(filepath.Dir(s.dataDir), "outside.txt")
	if err := os.WriteFile(outside, []byte("保留"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.removeManagedPath(filepath.Join("..", "outside.txt")); err == nil {
		t.Fatal("允许了越界清理")
	}
	if raw, err := os.ReadFile(outside); err != nil || string(raw) != "保留" {
		t.Fatal("影响了数据目录外文件")
	}
}

func TestDeleteRouteNotFoundDoesNotHideAnExistingSession(t *testing.T) {
	s, _ := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "DELETE" {
			w.WriteHeader(404)
			fmt.Fprint(w, `{"error":"route_not_found"}`)
			return
		}
		fmt.Fprint(w, `{"session":{"id":"old"}}`)
	})
	if err := s.setOwned("old", ownedSession{Title: "保留"}); err != nil {
		t.Fatal(err)
	}
	w := authorized(s, "DELETE", "/api/sessions/old", `{"confirmed":true}`)
	if w.Code == 200 || s.st.Deletions["old"] != "" {
		t.Fatal("缺失的删除路由被误判为成功删除")
	}
	if _, ok := s.owned("old"); !ok {
		t.Fatal("仍然存在的会话被移除")
	}
}

func TestDeleteLocalCleanupFailureCanResume(t *testing.T) {
	calls := 0
	s, _ := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" && r.URL.Path == "/api/sessions" {
			fmt.Fprint(w, `{"data":[],"has_more":false}`)
			return
		}
		if r.Method == "DELETE" {
			calls++
			if calls == 1 {
				fmt.Fprint(w, `{"deleted":true}`)
				return
			}
		}
		w.WriteHeader(404)
		fmt.Fprint(w, `{"error":"not_found"}`)
	})
	if err := s.submissions.save(submission{SessionID: "old", RequestID: "request", State: "acknowledged", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(s.dataDir, "file-links"), 0700); err != nil {
		t.Fatal(err)
	}
	blocked := filepath.Join(s.dataDir, "file-links", "broken.json")
	if err := os.WriteFile(blocked, []byte("清理失败夹具"), 0600); err != nil {
		t.Fatal(err)
	}
	w := authorized(s, "DELETE", "/api/sessions/old", `{"confirmed":true}`)
	if w.Code != 500 || s.st.Deletions["old"] != "pending" {
		t.Fatalf("本地清理失败却伪报成功: %d %s %s", w.Code, w.Body.String(), s.st.Deletions["old"])
	}
	if _, ok := s.submissions.get("old", "request"); !ok {
		t.Fatal("清理失败后丢失重试关联")
	}
	w = authorized(s, "GET", "/api/sessions", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"deletion_pending":true`) || !strings.Contains(w.Body.String(), `"id":"old"`) {
		t.Fatalf("刷新后没有保留继续删除入口: %d %s", w.Code, w.Body.String())
	}
	if err := os.Remove(blocked); err != nil {
		t.Fatal(err)
	}
	w = authorized(s, "DELETE", "/api/sessions/old", `{"confirmed":true}`)
	if w.Code != 200 || s.st.Deletions["old"] != "deleted" {
		t.Fatal("无法继续本地清理")
	}
	if _, ok := s.submissions.get("old", "request"); ok {
		t.Fatal("本地发送记录仍被保留")
	}
}
