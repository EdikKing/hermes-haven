package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testServer(t *testing.T, h http.HandlerFunc) (*server, *httptest.Server) {
	t.Helper()
	up := httptest.NewServer(h)
	t.Cleanup(up.Close)
	t.Setenv("HAVEN_DATA_DIR", t.TempDir())
	t.Setenv("HERMES_URL", up.URL)
	t.Setenv("HERMES_API_KEY", "test-only-key")
	t.Setenv("HERMES_DASHBOARD_URL", up.URL)
	t.Setenv("HERMES_AUTH_PROVIDER", "basic")
	t.Setenv("HERMES_HISTORY_DB", "")
	s, err := newServer()
	if err != nil {
		t.Fatal(err)
	}
	return s, up
}

func authorized(s *server, method, path, body string) *httptest.ResponseRecorder {
	s.mu.Lock()
	s.logins["test-session"] = loginSession{Username: "test", ExpiresAt: time.Now().Add(time.Hour)}
	s.mu.Unlock()
	r := httptest.NewRequest(method, "http://haven.test"+path, strings.NewReader(body))
	r.Header.Set("Origin", "http://haven.test")
	r.AddCookie(&http.Cookie{Name: "haven_session", Value: "test-session"})
	w := httptest.NewRecorder()
	s.api(w, r)
	return w
}

func TestLegacyRejectionIsReportedWithoutDeletingHistory(t *testing.T) {
	var calls []string
	s, _ := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(409)
		fmt.Fprint(w, `{"error":{"code":"permission_denied"}}`)
	})
	w := authorized(s, "POST", "/api/sessions/old_cli/runs", `{"text":"继续","images":[],"request_id":"1234567890abcdef"}`)
	if w.Code != 409 || !strings.Contains(w.Body.String(), "其他入口") {
		t.Fatalf("意外响应: %d %s", w.Code, w.Body.String())
	}
	if len(calls) != 1 || calls[0] != "POST /v1/runs" {
		t.Fatalf("意外上游调用: %v", calls)
	}
	if _, ok := s.owned("old_cli"); ok {
		t.Fatal("拒绝的旧会话不应被登记")
	}
	w = authorized(s, "POST", "/api/sessions/old_cli/title", `{"title":"新标题"}`)
	if w.Code != 403 || len(calls) != 1 {
		t.Fatal("不应修改原有会话标题")
	}
}

func TestNewSessionTitleDoesNotModifyHermesHistory(t *testing.T) {
	var methods []string
	s, _ := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.Method+" "+r.URL.Path)
		if r.URL.Path != "/api/sessions" || r.Method != "POST" {
			t.Errorf("意外调用 %s %s", r.Method, r.URL.Path)
		}
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		if _, ok := b["title"]; ok {
			t.Error("创建时不应向 Hermes 提交可能冲突的标题")
		}
		w.WriteHeader(201)
		fmt.Fprint(w, `{}`)
	})
	w := authorized(s, "POST", "/api/sessions", `{}`)
	if w.Code != 201 {
		t.Fatal(w.Body.String())
	}
	var data struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &data)
	w = authorized(s, "POST", "/api/sessions/"+data.ID+"/title", `{"title":"自定义标题"}`)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if len(methods) != 1 {
		t.Fatalf("标题修改不应调用 Hermes: %v", methods)
	}
}

func TestHistoryImagesCannotEscapeAllowedRoot(t *testing.T) {
	root := t.TempDir()
	allowed := filepath.Join(root, "allowed")
	if err := os.Mkdir(allowed, 0700); err != nil {
		t.Fatal(err)
	}
	png := []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0, 0, 0, 0}
	good := filepath.Join(allowed, "image.png")
	bad := filepath.Join(root, "outside.png")
	_ = os.WriteFile(good, png, 0600)
	_ = os.WriteFile(bad, png, 0600)
	var chosen string
	s, _ := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		data := map[string]any{"data": []any{map[string]any{"id": float64(123), "role": "user", "content": fmt.Sprintf("[Image attached at: %s]", chosen)}}}
		_ = json.NewEncoder(w).Encode(data)
	})
	s.imageRoots = []string{allowed}
	chosen = bad
	w := authorized(s, "GET", "/api/sessions/old/images/123/0", "")
	if w.Code != 403 {
		t.Fatalf("越界图片应被拒绝，收到 %d", w.Code)
	}
	chosen = good
	w = authorized(s, "GET", "/api/sessions/old/images/123/0", "")
	if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), png) {
		t.Fatalf("允许的图片无法读取: %d", w.Code)
	}
}

func TestRejectCrossOriginMutation(t *testing.T) {
	s, _ := testServer(t, func(http.ResponseWriter, *http.Request) { t.Fatal("跨站请求不应访问 Hermes") })
	r := httptest.NewRequest("POST", "http://haven.test/api/login", strings.NewReader(`{"password":"test-only-password"}`))
	r.Header.Set("Origin", "https://elsewhere.test")
	w := httptest.NewRecorder()
	s.api(w, r)
	if w.Code != 403 {
		t.Fatalf("跨站请求应被拒绝: %d", w.Code)
	}
}
