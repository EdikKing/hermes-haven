package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestContextUsageRequiresLoginAndReportsUnknownWithoutHistory(t *testing.T) {
	s, _ := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("缺少只读历史时不应回退读取上游消息")
	})
	w := httptest.NewRecorder()
	s.api(w, httptest.NewRequest("GET", "/api/sessions/one/context", nil))
	if w.Code != 401 {
		t.Fatal("上下文统计必须登录")
	}
	w = authorized(s, "GET", "/api/sessions/one/context", "")
	var data map[string]any
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &data) != nil || data["used_tokens"] != nil || data["max_tokens"] != nil || data["remaining_tokens"] != nil {
		t.Fatalf("未知用量不能伪报为零：%d %s", w.Code, w.Body.String())
	}
}

func TestLiveContextUsageReadOnly(t *testing.T) {
	if os.Getenv("HAVEN_LIVE_CONTEXT") != "true" {
		t.Skip("未启用真实只读上下文验收")
	}
	id := os.Getenv("HAVEN_LIVE_CONTEXT_SESSION")
	if !validID(id) {
		t.Fatal("必须指定本次读取的会话编号")
	}
	t.Setenv("HAVEN_DATA_DIR", t.TempDir())
	t.Setenv("HERMES_API_KEY", "unused-read-only-context-test")
	s, err := newServer()
	if err != nil {
		t.Fatal(err)
	}
	w := authorized(s, "GET", "/api/sessions/"+id+"/context", "")
	var data struct {
		Used      *int64 `json:"used_tokens"`
		Maximum   *int64 `json:"max_tokens"`
		Remaining *int64 `json:"remaining_tokens"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &data) != nil || data.Used == nil || data.Maximum == nil || data.Remaining == nil || *data.Remaining != max(0, *data.Maximum-*data.Used) {
		t.Fatalf("实际上下文统计失败：%d %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "base_url") || strings.Contains(w.Body.String(), "system_prompt") || strings.Contains(w.Body.String(), "api_key") {
		t.Fatal("上下文接口泄露非计数内容")
	}
	t.Logf("只读上下文：已用 %d，总容量 %d，剩余 %d", *data.Used, *data.Maximum, *data.Remaining)
	w = authorized(s, "GET", "/api/sessions/__missing_context_session__/context", "")
	if w.Code != 404 {
		t.Fatalf("不存在的会话应返回 404：%d %s", w.Code, w.Body.String())
	}
}
