package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const sampleSubmission = `{"text":"执行一次测试","images":[],"request_id":"recovery_request_123"}`

func TestAcceptedRequestSurvivesRestartWithoutExecutingAgain(t *testing.T) {
	var posts atomic.Int32
	s, _ := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/v1/runs" {
			t.Errorf("意外上游操作: %s %s", r.Method, r.URL.Path)
		}
		posts.Add(1)
		w.WriteHeader(202)
		fmt.Fprint(w, `{"run_id":"run_persisted","status":"started"}`)
	})
	if w := authorized(s, "POST", "/api/sessions/new_test/runs", sampleSubmission); w.Code != 202 {
		t.Fatal(w.Body.String())
	}
	restarted, err := newServer()
	if err != nil {
		t.Fatal(err)
	}
	w := authorized(restarted, "POST", "/api/sessions/new_test/runs", sampleSubmission)
	if w.Code != 202 || posts.Load() != 1 {
		t.Fatalf("重启后不应再次执行: %d，调用 %d 次", w.Code, posts.Load())
	}
	w = authorized(restarted, "GET", "/api/sessions/new_test/requests/recovery_request_123", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"run_id":"run_persisted"`) {
		t.Fatal(w.Body.String())
	}
	w = authorized(restarted, "POST", "/api/sessions/new_test/runs", strings.Replace(sampleSubmission, "执行一次测试", "不同消息", 1))
	if w.Code != 409 || posts.Load() != 1 {
		t.Fatal("相同请求编号不得用于不同消息")
	}
}

func TestUnknownSubmissionIsReadOnlyUntilExplicitSafeRetry(t *testing.T) {
	var posts atomic.Int32
	var keys []string
	s, _ := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" && r.URL.Path == "/v1/capabilities" {
			fmt.Fprint(w, `{"features":{"runs_idempotency":{"supported":true,"durable":true,"retention_seconds":86400}}}`)
			return
		}
		if r.Method != "POST" || r.URL.Path != "/v1/runs" {
			t.Errorf("意外操作 %s %s", r.Method, r.URL.Path)
			return
		}
		keys = append(keys, r.Header.Get("Idempotency-Key"))
		if posts.Add(1) == 1 {
			w.WriteHeader(503)
			fmt.Fprint(w, `{"error":"响应丢失，但假定运行已受理"}`)
			return
		}
		w.WriteHeader(202)
		fmt.Fprint(w, `{"run_id":"run_existing","status":"running"}`)
	})
	w := authorized(s, "POST", "/api/sessions/new_test/runs", sampleSubmission)
	if w.Code != 502 {
		t.Fatal(w.Body.String())
	}
	restarted, err := newServer()
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/sessions/new_test/status", "/api/sessions/new_test/requests/recovery_request_123"} {
		w = authorized(restarted, "GET", path, "")
		if w.Code != 200 {
			t.Fatal(w.Body.String())
		}
	}
	if posts.Load() != 1 {
		t.Fatal("只读核对不得重新提交")
	}
	w = authorized(restarted, "POST", "/api/sessions/new_test/runs", strings.Replace(sampleSubmission, "recovery_request_123", "another_request", 1))
	if w.Code != 409 || posts.Load() != 1 {
		t.Fatal("有未知结果时不得接受另一请求")
	}
	w = authorized(restarted, "POST", "/api/sessions/new_test/runs", sampleSubmission)
	if w.Code != 202 || posts.Load() != 2 || keys[0] != keys[1] {
		t.Fatalf("重试必须沿用同一上游请求编号: %d %v", w.Code, keys)
	}
}

func TestUnsafeRetryCannotSubmitOrChangeOldHistory(t *testing.T) {
	for _, mode := range []string{"expired", "memory_only", "scope_changed"} {
		t.Run(mode, func(t *testing.T) {
			var posts atomic.Int32
			s, _ := testServer(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "GET" {
					fmt.Fprintf(w, `{"features":{"runs_idempotency":{"supported":true,"durable":%t,"retention_seconds":86400}}}`, mode != "memory_only")
					return
				}
				posts.Add(1)
				w.WriteHeader(503)
				fmt.Fprint(w, `{}`)
			})
			_ = authorized(s, "POST", "/api/sessions/old_cli/runs", sampleSubmission)
			rec, _ := s.submissions.get("old_cli", "recovery_request_123")
			if mode == "expired" {
				rec.CreatedAt = time.Now().Add(-25 * time.Hour)
				if err := s.submissions.save(rec); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "scope_changed" {
				s.apiKey = "different-api-key"
			}
			w := authorized(s, "POST", "/api/sessions/old_cli/runs", sampleSubmission)
			if w.Code != 409 || posts.Load() != 1 {
				t.Fatalf("不安全重试不得提交: %d，调用 %d 次", w.Code, posts.Load())
			}
			w = authorized(s, "POST", "/api/sessions/old_cli/requests/recovery_request_123/acknowledge", `{"confirmed":true}`)
			if w.Code != 200 || posts.Load() != 1 {
				t.Fatal("本地确认不应调用 Hermes")
			}
			w = authorized(s, "POST", "/api/sessions/old_cli/runs", sampleSubmission)
			if w.Code != 409 || posts.Load() != 1 {
				t.Fatal("已关闭的旧请求不得重放")
			}
		})
	}
}

func TestConcurrentReplayAndDifferentSessionKeys(t *testing.T) {
	var posts atomic.Int32
	var keys sync.Map
	s, _ := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		posts.Add(1)
		keys.Store(r.Header.Get("Idempotency-Key"), true)
		time.Sleep(5 * time.Millisecond)
		w.WriteHeader(202)
		fmt.Fprint(w, `{"run_id":"run_once","status":"started"}`)
	})
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := authorized(s, "POST", "/api/sessions/new_one/runs", sampleSubmission)
			if w.Code != 202 {
				t.Errorf("并发重放失败 %d", w.Code)
			}
		}()
	}
	wg.Wait()
	if posts.Load() != 1 {
		t.Fatalf("并发相同请求重复执行 %d 次", posts.Load())
	}
	w := authorized(s, "POST", "/api/sessions/new_two/runs", sampleSubmission)
	count := 0
	keys.Range(func(_, _ any) bool { count++; return true })
	if w.Code != 202 || count != 2 {
		t.Fatal("不同会话的请求编号必须有独立作用域")
	}
}

func TestCannotSubmitBeforeDurableRecordIsSaved(t *testing.T) {
	var calls atomic.Int32
	s, _ := testServer(t, func(w http.ResponseWriter, r *http.Request) { calls.Add(1) })
	s.submissions.dir = filepath.Join(t.TempDir(), "missing", "directory")
	w := authorized(s, "POST", "/api/sessions/new_test/runs", sampleSubmission)
	if w.Code != 503 || calls.Load() != 0 {
		t.Fatal("发送记录未落盘时必须阻止提交")
	}
}

func TestStaleBrowserRequestCannotBecomeANewExecution(t *testing.T) {
	s, _ := testServer(t, func(w http.ResponseWriter, r *http.Request) { t.Fatal("过期的浏览器请求不得访问 Hermes") })
	body := fmt.Sprintf(`{"text":"过期草稿","request_id":"old_request","created_at":%d}`, time.Now().Add(-25*time.Hour).UnixMilli())
	w := authorized(s, "POST", "/api/sessions/new_test/runs", body)
	if w.Code != 409 {
		t.Fatal(w.Body.String())
	}
}

func TestCorruptSubmissionRecordFailsClosed(t *testing.T) {
	s, _ := testServer(t, func(http.ResponseWriter, *http.Request) {})
	if err := os.WriteFile(filepath.Join(s.dataDir, "requests", "bad.json"), []byte(`{`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := newServer(); err == nil {
		t.Fatal("损坏的恢复记录不得静默忽略")
	}
}

func TestSSEStopsWhenHavenLoginExpires(t *testing.T) {
	s, _ := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Last-Event-ID") != "7" {
			t.Error("未转发事件恢复序号")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"seq\":8}\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	})
	if err := s.setOwned("new_test", ownedSession{RunID: "run_stream"}); err != nil {
		t.Fatal(err)
	}
	s.logins["short"] = loginSession{ExpiresAt: time.Now().Add(150 * time.Millisecond)}
	r := httptest.NewRequest("GET", "http://haven.test/api/sessions/new_test/events?run_id=run_stream&last_seq=7", nil)
	r.AddCookie(&http.Cookie{Name: "haven_session", Value: "short"})
	w := httptest.NewRecorder()
	started := time.Now()
	s.api(w, r)
	if w.Code != 200 || time.Since(started) > 2*time.Second {
		t.Fatal("登录到期后实时连接应关闭")
	}
}

func TestEncryptedImageReferenceAvoidsScanningLongHistory(t *testing.T) {
	root := t.TempDir()
	imagePath := filepath.Join(root, "old.png")
	png := []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0, 0, 0, 0}
	_ = os.WriteFile(imagePath, png, 0600)
	var calls atomic.Int32
	s, _ := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Query().Get("offset") != "5000" {
			t.Errorf("不应扫描其他页面: %s", r.URL.RawQuery)
		}
		content, _ := json.Marshal([]map[string]any{{"type": "image", "path": imagePath}})
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"id": 5001, "content": string(content)}}})
	})
	s.imageRoots = []string{root}
	w := authorized(s, "GET", "/api/sessions/old/messages?offset=5000", "")
	var data struct {
		Data []struct {
			Content []map[string]any `json:"content"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &data); err != nil {
		t.Fatal(err)
	}
	src, _ := data.Data[0].Content[0]["src"].(string)
	if strings.Contains(src, imagePath) || !strings.Contains(src, "ref=") {
		t.Fatal("图片路径应加密绑定到消息")
	}
	w = authorized(s, "GET", src, "")
	if w.Code != 200 || calls.Load() != 1 {
		t.Fatalf("图片不应再次扫描历史: %d %d", w.Code, calls.Load())
	}
	w = authorized(s, "GET", strings.Replace(src, "/sessions/old/", "/sessions/another/", 1), "")
	if w.Code != 403 {
		t.Fatal("图片引用不得跨会话使用")
	}
}

func TestLegacyImageLinkCanReadBeyondThreeThousandMessages(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "image.png")
	_ = os.WriteFile(path, []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0, 0, 0, 0}, 0600)
	pages := 0
	s, _ := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		pages++
		offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
		rows := []any{}
		if offset == 3100 {
			rows = append(rows, map[string]any{"id": 3101, "content": "[Image attached at: " + path + "]"})
		} else {
			for i := 0; i < 100; i++ {
				rows = append(rows, map[string]any{"id": offset + i + 1, "content": "旧消息"})
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": rows})
	})
	s.imageRoots = []string{root}
	w := authorized(s, "GET", "/api/sessions/old/images/3101/0", "")
	if w.Code != 200 || pages != 32 {
		t.Fatalf("旧链接应可读取更早图片: %d %d", w.Code, pages)
	}
}

func TestSessionPaginationHasNoTwoThousandLimit(t *testing.T) {
	s, _ := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("offset") != "2200" {
			t.Error("分页位置未传递")
		}
		fmt.Fprint(w, `{"data":[{"id":"older_session"}],"offset":2200,"limit":200,"has_more":false}`)
	})
	w := authorized(s, "GET", "/api/sessions?offset=2200", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "older_session") {
		t.Fatal(w.Body.String())
	}
	w = authorized(s, "GET", "/api/sessions?offset=-1", "")
	if w.Code != 400 {
		t.Fatal("负数分页应被拒绝")
	}
}
