package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestModelCatalogOnlyReturnsPickerMetadata(t *testing.T) {
	s, _ := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RequestURI() != "/api/model/options?include_unconfigured=false" || r.Method != "GET" {
			t.Errorf("意外模型请求：%s %s", r.Method, r.URL)
		}
		fmt.Fprint(w, `{"model":"alpha","provider":"custom:local","api_key":"secret-top","providers":[{"slug":"custom:local","name":"本地模型","models":["alpha","beta"],"authenticated":true,"api_url":"secret-host","api_key":"secret-key","capabilities":{"alpha":{"reasoning":true,"can_disable_reasoning":false,"secret":"hidden"}}},{"slug":"offline","models":["gamma"],"authenticated":false}]}`)
	})
	w := httptest.NewRecorder()
	s.api(w, httptest.NewRequest("GET", "/api/models", nil))
	if w.Code != 401 {
		t.Fatal("模型目录必须要求登录")
	}
	w = authorized(s, "GET", "/api/models", "")
	if w.Code != 200 || strings.Contains(w.Body.String(), "secret") || strings.Contains(w.Body.String(), "offline") || strings.Contains(w.Body.String(), "hidden") || !strings.Contains(w.Body.String(), `"reasoning":true`) {
		t.Fatalf("模型目录过滤错误：%d %s", w.Code, w.Body.String())
	}
}

func TestSessionModelSettingsCreatePersistAndIsolate(t *testing.T) {
	var created map[string]any
	s, _ := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/api/sessions" {
			t.Fatalf("保存选择不应修改上游历史：%s %s", r.Method, r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&created)
		w.WriteHeader(201)
		fmt.Fprint(w, `{}`)
	})
	w := authorized(s, "POST", "/api/sessions", `{"settings":{"model":"alpha","provider":"custom:local","reasoning_effort":"high"}}`)
	if w.Code != 201 || created["model"] != "alpha" || created["provider"] != "custom:local" || created["model_options"].(map[string]any)["reasoning"].(map[string]any)["effort"] != "high" {
		t.Fatalf("新会话未使用所选模型：%d %s %v", w.Code, w.Body.String(), created)
	}
	var result struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &result)
	w = authorized(s, "PUT", "/api/sessions/"+result.ID+"/settings", `{"model":"beta","provider":"custom:local","reasoning_effort":"none"}`)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	w = authorized(s, "PUT", "/api/sessions/old_cli/settings", `{"model":"old-choice","reasoning_effort":"low"}`)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if own, _ := s.owned("old_cli"); !own.External {
		t.Fatal("为历史会话保存选择不能改变会话归属")
	}
	restarted, err := newServer()
	if err != nil {
		t.Fatal(err)
	}
	if settings := restarted.savedSettings(result.ID); settings.Model != "beta" || settings.ReasoningEffort != "none" {
		t.Fatalf("重启未恢复选择：%+v", settings)
	}
	if restarted.savedSettings("old_cli").Model != "old-choice" || restarted.savedSettings("other").Model != "" {
		t.Fatal("会话设置没有隔离")
	}
	for _, body := range []string{`{"model":"alpha","reasoning_effort":"invalid"}`, `{"provider":"missing-model"}`, `{"model":"bad\nmodel"}`} {
		w = authorized(s, "PUT", "/api/sessions/"+result.ID+"/settings", body)
		if w.Code != 400 || s.savedSettings(result.ID).Model != "beta" {
			t.Fatalf("无效选择改变了会话：%d %s", w.Code, w.Body.String())
		}
	}
}

func TestModelSettingsFrozenAcrossUnknownRetryAndRestart(t *testing.T) {
	var payloads []string
	s, _ := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/capabilities" {
			fmt.Fprint(w, `{"features":{"runs_idempotency":{"supported":true,"durable":true,"retention_seconds":86400}}}`)
			return
		}
		if r.Method != "POST" || r.URL.Path != "/v1/runs" {
			t.Fatalf("意外调用：%s %s", r.Method, r.URL.Path)
		}
		var payload map[string]any
		_ = json.NewDecoder(r.Body).Decode(&payload)
		raw, _ := json.Marshal(payload)
		payloads = append(payloads, string(raw))
		if len(payloads) == 1 {
			w.WriteHeader(503)
			fmt.Fprint(w, `{"error":"响应丢失"}`)
			return
		}
		w.WriteHeader(202)
		fmt.Fprint(w, `{"run_id":"model_run","status":"started"}`)
	})
	body := `{"text":"使用固定选择","request_id":"model_request","settings":{"model":"alpha","provider":"custom:local","reasoning_effort":"high"}}`
	w := authorized(s, "POST", "/api/sessions/model_session/runs", body)
	if w.Code != 502 || !strings.Contains(payloads[0], `"model":"alpha"`) || !strings.Contains(payloads[0], `"effort":"high"`) {
		t.Fatalf("首次提交未冻结模型：%d %s", w.Code, w.Body.String())
	}
	w = authorized(s, "PUT", "/api/sessions/model_session/settings", `{"model":"beta","reasoning_effort":"low"}`)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	w = authorized(s, "GET", "/api/sessions/model_session/status", "")
	if !strings.Contains(w.Body.String(), `"model":"alpha"`) {
		t.Fatal("待核对请求应显示原来的模型选择")
	}
	restarted, err := newServer()
	if err != nil {
		t.Fatal(err)
	}
	w = authorized(restarted, "POST", "/api/sessions/model_session/runs", strings.Replace(body, `"high"`, `"low"`, 1))
	if w.Code != 409 || len(payloads) != 1 {
		t.Fatal("同一请求编号不能更换模型设置")
	}
	w = authorized(restarted, "POST", "/api/sessions/model_session/runs", body)
	if w.Code != 202 || len(payloads) != 2 || payloads[0] != payloads[1] {
		t.Fatalf("重试改变了原请求：%d %s %v", w.Code, w.Body.String(), payloads)
	}
	w = authorized(restarted, "POST", "/api/sessions/model_session/runs", body)
	if w.Code != 202 || len(payloads) != 2 {
		t.Fatal("已受理重放不应再次执行")
	}
}

type gatedModelTitleBody struct {
	reader           io.Reader
	waiting, release chan struct{}
	once             sync.Once
}

func (b *gatedModelTitleBody) Read(p []byte) (int, error) {
	b.once.Do(func() { close(b.waiting); <-b.release })
	return b.reader.Read(p)
}

func TestRenamingPreservesConcurrentModelSettings(t *testing.T) {
	s, _ := testServer(t, func(w http.ResponseWriter, r *http.Request) { t.Error("本地设置不应调用 Hermes") })
	if err := s.setOwned("rename_model", ownedSession{Title: "原标题", Settings: &modelSettings{Model: "alpha"}}); err != nil {
		t.Fatal(err)
	}
	_ = authorized(s, "GET", "/api/sessions/rename_model/settings", "")
	body := &gatedModelTitleBody{reader: strings.NewReader(`{"title":"新标题"}`), waiting: make(chan struct{}), release: make(chan struct{})}
	var unblock sync.Once
	defer unblock.Do(func() { close(body.release) })
	r := httptest.NewRequest("POST", "http://haven.test/api/sessions/rename_model/title", body)
	r.Header.Set("Origin", "http://haven.test")
	r.AddCookie(&http.Cookie{Name: "haven_session", Value: "test-session"})
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { w := httptest.NewRecorder(); s.api(w, r); done <- w }()
	select {
	case <-body.waiting:
	case <-time.After(2 * time.Second):
		t.Fatal("重命名没有开始读取请求")
	}
	w := authorized(s, "PUT", "/api/sessions/rename_model/settings", `{"model":"beta","reasoning_effort":"high"}`)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	unblock.Do(func() { close(body.release) })
	select {
	case w = <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("重命名没有完成")
	}
	own, _ := s.owned("rename_model")
	if w.Code != 200 || own.Title != "新标题" || own.Settings.Model != "beta" || own.Settings.ReasoningEffort != "high" {
		t.Fatalf("重命名丢失并发保存的选择：%d %+v", w.Code, own)
	}
}
