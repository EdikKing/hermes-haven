package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func TestControlIdentityValidationAndLeastPrivilege(t *testing.T) {
	posts := 0
	s, _ := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" && r.URL.Path == "/v1/runs/run-one" {
			jsonOut(w, 200, map[string]any{"run_id": "run-one", "status": "waiting_for_approval", "pending_controls": []any{
				map[string]any{"kind": "approval", "prompt_id": "approval:一", "execution_generation": 3, "choices": []string{"once", "deny", "session", "always"}},
				map[string]any{"kind": "clarify", "prompt_id": "clarify:二", "execution_generation": 3},
			}})
			return
		}
		if r.Method == "POST" && (r.URL.Path == "/v1/runs/run-one/approval" || r.URL.Path == "/v1/runs/run-one/clarify") {
			posts++
			var b map[string]any
			if json.NewDecoder(r.Body).Decode(&b) != nil || len(b) != 3 || b["run_id"] != nil {
				t.Errorf("上游必须只有精确身份与回答：%v", b)
			}
			jsonOut(w, 200, map[string]any{"status": "resolved"})
			return
		}
		t.Errorf("禁止访问控制以外的上游接口：%s %s", r.Method, r.URL.Path)
	})
	if err := s.setOwned("one", ownedSession{RunID: "run-one"}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, session, kind, body string
		code                      int
	}{
		{"允许一次", "one", "approval", `{"run_id":"run-one","request_id":"approval:一","execution_generation":3,"choice":"once"}`, 200},
		{"拒绝", "one", "approval", `{"run_id":"run-one","request_id":"approval:一","execution_generation":3,"choice":"deny"}`, 200},
		{"补充回答", "one", "clarify", `{"run_id":"run-one","request_id":"clarify:二","execution_generation":3,"answer":"中文回答\nBLUE"}`, 200},
		{"其他会话", "two", "approval", `{"run_id":"run-one","request_id":"approval:一","execution_generation":3,"choice":"once"}`, 403},
		{"永久审批", "one", "approval", `{"run_id":"run-one","request_id":"approval:一","execution_generation":3,"choice":"always"}`, 400},
		{"会话审批", "one", "approval", `{"run_id":"run-one","request_id":"approval:一","execution_generation":3,"choice":"session"}`, 400},
		{"批量审批", "one", "approval", `{"run_id":"run-one","request_id":"approval:一","execution_generation":3,"choice":"once","all":true}`, 400},
		{"过期代次", "one", "approval", `{"run_id":"run-one","request_id":"approval:一","execution_generation":2,"choice":"once"}`, 409},
		{"其他请求", "one", "approval", `{"run_id":"run-one","request_id":"not-this-prompt","execution_generation":3,"choice":"once"}`, 409},
		{"错误类型", "one", "clarify", `{"run_id":"run-one","request_id":"approval:一","execution_generation":3,"answer":"BLUE"}`, 409},
		{"空回答", "one", "clarify", `{"run_id":"run-one","request_id":"clarify:二","execution_generation":3,"answer":"  "}`, 400},
		{"布尔代次", "one", "approval", `{"run_id":"run-one","request_id":"approval:一","execution_generation":true,"choice":"once"}`, 400},
		{"路径注入", "one", "approval", `{"run_id":"../runs/other","request_id":"approval:一","execution_generation":3,"choice":"once"}`, 400},
		{"多个JSON", "one", "clarify", `{"run_id":"run-one","request_id":"clarify:二","execution_generation":3,"answer":"BLUE"}{}`, 400},
		{"回答超限", "one", "clarify", `{"run_id":"run-one","request_id":"clarify:二","execution_generation":3,"answer":"` + strings.Repeat("中", 16385) + `"}`, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := authorized(s, "POST", "/api/sessions/"+tc.session+"/"+tc.kind, tc.body)
			if res.Code != tc.code {
				t.Fatalf("status=%d, body=%s", res.Code, res.Body)
			}
		})
	}
	if posts != 3 {
		t.Fatalf("预期只提交三个有效回答，实际 %d", posts)
	}
	for _, kind := range []string{"approval", "clarify"} {
		res := httptest.NewRecorder()
		s.api(res, httptest.NewRequest("POST", "/api/sessions/one/"+kind, strings.NewReader(`{}`)))
		if res.Code != 401 {
			t.Fatal("未登录控制接口必须拒绝", res.Code)
		}
		r := httptest.NewRequest("POST", "http://haven.test/api/sessions/one/"+kind, strings.NewReader(`{}`))
		r.AddCookie(&http.Cookie{Name: "haven_session", Value: "test-session"})
		r.Header.Set("Origin", "http://other.test")
		res = httptest.NewRecorder()
		s.api(res, r)
		if res.Code != 403 {
			t.Fatal("跨来源控制接口必须拒绝", res.Code)
		}
	}
}

func TestControlConcurrentSettlementAndRestart(t *testing.T) {
	var mu sync.Mutex
	resolved := 0
	s, _ := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Method == "GET" {
			pending := []any{}
			if resolved == 0 {
				pending = append(pending, map[string]any{"kind": "approval", "prompt_id": "p", "execution_generation": 1, "choices": []string{"once", "deny"}})
			}
			jsonOut(w, 200, map[string]any{"run_id": "r", "status": "waiting_for_approval", "pending_controls": pending})
		} else if resolved == 0 {
			resolved++
			jsonOut(w, 200, map[string]any{"status": "resolved"})
		} else {
			jsonOut(w, 409, map[string]any{"error": "approval_not_pending"})
		}
	})
	if err := s.setOwned("s", ownedSession{RunID: "r"}); err != nil {
		t.Fatal(err)
	}
	// 进程重启后从 Haven 自有登记恢复，审批请求本身仍以 Hermes 的实时快照为准。
	restarted, err := newServer()
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := authorized(restarted, "POST", "/api/sessions/s/approval", `{"run_id":"r","request_id":"p","execution_generation":1,"choice":"once"}`)
			if w.Code != 200 && w.Code != 409 {
				t.Errorf("status=%d %s", w.Code, w.Body)
			}
		}()
	}
	wg.Wait()
	if resolved != 1 {
		t.Fatal("重复审批不能重复解除工具等待", resolved)
	}
}

func TestControlUnknownResultDoesNotRetry(t *testing.T) {
	var posts atomic.Int32
	s, _ := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			jsonOut(w, 200, map[string]any{"run_id": "r", "status": "running", "pending_controls": []any{map[string]any{"kind": "clarify", "prompt_id": "p", "execution_generation": 1}}})
			return
		}
		posts.Add(1)
		// 原生端已收到回答，但连接在响应前中断。
		connection, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		connection.Close()
	})
	s.setOwned("s", ownedSession{RunID: "r"})
	w := authorized(s, "POST", "/api/sessions/s/clarify", `{"run_id":"r","request_id":"p","execution_generation":1,"answer":"BLUE"}`)
	if w.Code != 502 || !strings.Contains(w.Body.String(), "control_result_unknown") || posts.Load() != 1 {
		t.Fatalf("status=%d posts=%d body=%s", w.Code, posts.Load(), w.Body)
	}
}

func TestControlMalformedSuccessRemainsUnknown(t *testing.T) {
	for _, raw := range []string{`{"status":"unexpected"}`, `not-json`, `{}`} {
		t.Run(raw, func(t *testing.T) {
			s, _ := testServer(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "GET" {
					jsonOut(w, 200, map[string]any{"run_id": "r", "status": "running", "pending_controls": []any{map[string]any{"kind": "clarify", "prompt_id": "p", "execution_generation": 1}}})
					return
				}
				w.Write([]byte(raw))
			})
			if err := s.setOwned("s", ownedSession{RunID: "r"}); err != nil {
				t.Fatal(err)
			}
			w := authorized(s, "POST", "/api/sessions/s/clarify", `{"run_id":"r","request_id":"p","execution_generation":1,"answer":"中文"}`)
			if w.Code != 502 || !strings.Contains(w.Body.String(), "control_result_unknown") {
				t.Fatalf("不能将不完整结果显示为成功：%d %s", w.Code, w.Body)
			}
		})
	}
}
