package main

import (
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

func attemptLogin(s *server) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", "http://haven.test/api/login", strings.NewReader(`{"username":"edik","password":" test-only-password "}`))
	r.Header.Set("Origin", "http://haven.test")
	w := httptest.NewRecorder()
	s.api(w, r)
	return w
}

func TestHermesAccountLoginVerifiesIdentityAndKeepsSecretsServerSide(t *testing.T) {
	expires := time.Now().Add(2 * time.Hour).Unix()
	var calls []string
	s, _ := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		if r.Header.Get("Authorization") != "" {
			t.Error("不应向 Dashboard 发送 API 密钥")
		}
		switch r.URL.Path {
		case "/auth/password-login":
			var body map[string]string
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if body["username"] != "edik" || body["password"] != " test-only-password " || body["provider"] != "basic" {
				t.Error("未原样转发 Hermes 登录凭据")
			}
			http.SetCookie(w, &http.Cookie{Name: "hermes_session_at", Value: "upstream-only-token", Path: "/"})
			fmt.Fprint(w, `{"ok":true}`)
		case "/api/auth/me":
			cookie, err := r.Cookie("hermes_session_at")
			if err != nil || cookie.Value != "upstream-only-token" {
				t.Error("必须使用 Hermes 刚签发的凭据确认身份")
			}
			fmt.Fprintf(w, `{"user_id":"edik","display_name":"Edik","provider":"basic","expires_at":%d}`, expires)
		default:
			t.Errorf("登录不应访问会话或历史接口: %s", r.URL.Path)
		}
	})
	w := attemptLogin(s)
	if w.Code != 200 || len(calls) != 2 {
		t.Fatalf("登录失败: %d %s，调用 %v", w.Code, w.Body.String(), calls)
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != "haven_session" || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode || cookies[0].MaxAge > 7200 {
		t.Fatalf("登录 Cookie 不符合要求: %d 个", len(cookies))
	}
	if strings.Contains(w.Body.String()+w.Header().Get("Set-Cookie"), "upstream-only-token") || strings.Contains(w.Body.String(), "password") {
		t.Fatal("不应向浏览器泄露上游令牌或密码")
	}
	for _, method := range []string{"GET", "POST"} {
		path := "/api/me"
		if method == "POST" {
			path = "/api/logout"
		}
		r := httptest.NewRequest(method, "http://haven.test"+path, nil)
		r.Header.Set("Origin", "http://haven.test")
		r.AddCookie(cookies[0])
		w = httptest.NewRecorder()
		s.api(w, r)
		if w.Code != 200 {
			t.Fatalf("身份查询/退出失败: %d", w.Code)
		}
		if method == "GET" && !strings.Contains(w.Body.String(), `"username":"edik"`) {
			t.Fatal("应返回 Hermes 确认的用户身份")
		}
	}
	if len(s.logins) != 0 {
		t.Fatal("退出后 Haven 登录应失效")
	}
	if _, err := os.Stat(filepath.Join(s.dataDir, "state.json")); !os.IsNotExist(err) {
		t.Fatal("登录不应创建或改写会话状态文件")
	}
}

func TestHermesLoginFailuresDoNotGrantAccess(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		want   int
	}{
		{"错误凭据", 401, `{"detail":"sensitive upstream error"}`, 401},
		{"频率限制", 429, `{}`, 429},
		{"服务不可用", 503, `{}`, 502},
		{"登录方式未启用", 404, `{}`, 503},
		{"无有效认证结果", 200, `{"ok":false}`, 502},
		{"成功响应缺少凭据", 200, `{"ok":true}`, 502},
		{"禁止跟随重定向", 307, `{}`, 502},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			s, _ := testServer(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.Header().Set("Location", "/redirect-target")
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			})
			w := attemptLogin(s)
			if w.Code != tc.want || len(s.logins) != 0 || len(w.Result().Cookies()) != 0 || calls != 1 || strings.Contains(w.Body.String(), "sensitive") {
				t.Fatalf("错误处理异常: %d %s，调用 %d 次", w.Code, w.Body.String(), calls)
			}
		})
	}
}

func TestHermesUnverifiedOrExpiredIdentityIsRejected(t *testing.T) {
	for _, tc := range []struct {
		name, identity string
		want           int
	}{
		{"缺少身份", `{}`, 502},
		{"登录方式不符", `{"user_id":"edik","provider":"other","expires_at":4102444800}`, 502},
		{"身份过期", `{"user_id":"edik","provider":"basic","expires_at":1}`, 401},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := testServer(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/auth/password-login" {
					http.SetCookie(w, &http.Cookie{Name: "hermes_session_at", Value: "upstream-only-token"})
					fmt.Fprint(w, `{"ok":true}`)
					return
				}
				fmt.Fprint(w, tc.identity)
			})
			w := attemptLogin(s)
			if w.Code != tc.want || len(s.logins) != 0 {
				t.Fatalf("无效身份不应登录: %d %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestHermesLoginRateLimitAndExpiredHavenSession(t *testing.T) {
	calls := 0
	s, _ := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(401)
	})
	for i := 0; i < 8; i++ {
		if w := attemptLogin(s); w.Code != 401 {
			t.Fatalf("意外登录响应: %d", w.Code)
		}
	}
	if w := attemptLogin(s); w.Code != 429 || calls != 8 {
		t.Fatal("超限请求不应继续调用 Hermes")
	}
	s.logins["expired"] = loginSession{Username: "edik", ExpiresAt: time.Now().Add(-time.Minute)}
	r := httptest.NewRequest("GET", "http://haven.test/api/me", nil)
	r.AddCookie(&http.Cookie{Name: "haven_session", Value: "expired"})
	w := httptest.NewRecorder()
	s.api(w, r)
	if w.Code != 401 || len(s.logins) != 0 {
		t.Fatal("过期的 Haven 登录应失效")
	}
}
