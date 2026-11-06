package main

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// 仅在明确启用的隔离验收中连接真实 Hermes，所有写入都使用本次新建的会话。
func TestLiveHermesPhaseOne(t *testing.T) {
	if os.Getenv("HAVEN_LIVE_HERMES") != "true" {
		t.Skip("未启用真实 Hermes 验收")
	}
	t.Setenv("HAVEN_DATA_DIR", t.TempDir())
	s, err := newServer()
	if err != nil {
		t.Fatal(err)
	}
	create := func() string {
		w := authorized(s, "POST", "/api/sessions", `{}`)
		if w.Code != 201 {
			t.Fatalf("创建隔离会话失败: %d", w.Code)
		}
		var result struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &result)
		if !strings.HasPrefix(result.ID, "haven_") {
			t.Fatal("会话编号不符合隔离要求")
		}
		t.Log("新增验收会话:", result.ID)
		return result.ID
	}
	send := func(id, text string, images []string, request string) string {
		body, _ := json.Marshal(map[string]any{"text": text, "images": images, "request_id": request, "created_at": time.Now().UnixMilli()})
		w := authorized(s, "POST", "/api/sessions/"+id+"/runs", string(body))
		if w.Code != 202 {
			t.Fatalf("提交验收消息失败: %d", w.Code)
		}
		var result struct {
			RunID string `json:"run_id"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &result)
		return result.RunID
	}
	wait := func(id string) string {
		deadline := time.Now().Add(90 * time.Second)
		for time.Now().Before(deadline) {
			w := authorized(s, "GET", "/api/sessions/"+id+"/status", "")
			if w.Code != 200 {
				t.Fatalf("读取运行状态失败: %d", w.Code)
			}
			var result struct {
				Status string `json:"status"`
			}
			_ = json.Unmarshal(w.Body.Bytes(), &result)
			if result.Status == "completed" || result.Status == "cancelled" || result.Status == "interrupted" {
				return result.Status
			}
			if result.Status == "failed" || result.Status == "unknown" {
				t.Fatalf("真实运行未成功: %s", result.Status)
			}
			time.Sleep(300 * time.Millisecond)
		}
		t.Fatal("等待真实运行超时")
		return ""
	}
	id := create()
	run := send(id, "这是 Hermes Haven 独立验收会话。不要调用工具，不修改任何文件，仅回复 HAVEN_PHASE1_OK。", nil, "live_chat_"+randomID(8))
	if wait(id) != "completed" {
		t.Fatal("基础聊天未完成")
	}
	restarted, e := newServer()
	if e != nil {
		t.Fatal(e)
	}
	s = restarted
	w := authorized(s, "GET", "/api/sessions/"+id+"/status", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), run) {
		t.Fatal("后端重启后未恢复原运行")
	}
	request := "live_tool_" + randomID(8)
	run = send(id, "请调用 terminal 执行只读命令 printf HAVEN_TOOL_OK，然后回复验收通过。不要执行其他命令，不要修改文件。", nil, request)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Second)
	r := httptest.NewRequest("GET", "http://haven.test/api/sessions/"+id+"/events?run_id="+run+"&last_seq=-1", nil).WithContext(ctx)
	s.mu.Lock()
	s.logins["live-stream"] = loginSession{ExpiresAt: time.Now().Add(time.Hour)}
	s.mu.Unlock()
	r.AddCookie(&http.Cookie{Name: "haven_session", Value: "live-stream"})
	events := &terminalEventRecorder{ResponseRecorder: httptest.NewRecorder(), cancel: cancel}
	s.api(events, r)
	cancel()
	if events.Code != 200 || !strings.Contains(events.Body.String(), "tool.started") || !strings.Contains(events.Body.String(), "tool.completed") {
		t.Fatal("真实工具事件未完整返回")
	}
	if wait(id) != "completed" {
		t.Fatal("工具运行未完成")
	}
	w = authorized(s, "GET", "/api/sessions/"+id+"/messages", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "HAVEN_TOOL_OK") {
		t.Fatal("工具结果未保存到新会话")
	}
	var before struct {
		Data []any `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &before)
	again := send(id, "请调用 terminal 执行只读命令 printf HAVEN_TOOL_OK，然后回复验收通过。不要执行其他命令，不要修改文件。", nil, request)
	if again != run {
		t.Fatal("重复请求不应生成新运行")
	}
	w = authorized(s, "GET", "/api/sessions/"+id+"/messages", "")
	var after struct {
		Data []any `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &after)
	if len(before.Data) != len(after.Data) {
		t.Fatal("重放已完成请求不应新增消息")
	}

	imageID := create()
	var raw bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 12, 12))
	for y := 0; y < 12; y++ {
		for x := 0; x < 12; x++ {
			img.Set(x, y, color.RGBA{R: 255, A: 255})
		}
	}
	_ = png.Encode(&raw, img)
	var form bytes.Buffer
	writer := multipart.NewWriter(&form)
	part, _ := writer.CreateFormFile("file", "haven-test-red.png")
	_, _ = part.Write(raw.Bytes())
	_ = writer.Close()
	r = httptest.NewRequest("POST", "http://haven.test/api/uploads", &form)
	r.Header.Set("Origin", "http://haven.test")
	r.Header.Set("Content-Type", writer.FormDataContentType())
	r.AddCookie(&http.Cookie{Name: "haven_session", Value: "test-session"})
	w = httptest.NewRecorder()
	s.api(w, r)
	if w.Code != 201 {
		t.Fatal("真实上传路径失败")
	}
	var uploaded struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &uploaded)
	w = authorized(s, "GET", "/api/uploads/"+uploaded.ID, "")
	if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), raw.Bytes()) {
		t.Fatal("刷新后的附件读取失败")
	}
	send(imageID, "描述这张验收图片的主要颜色，仅用一个颜色词作答，不要调用工具。", []string{uploaded.ID}, "live_image_"+randomID(8))
	if wait(imageID) != "completed" {
		t.Fatal("图片运行未完成")
	}
	w = authorized(s, "GET", "/api/sessions/"+imageID+"/messages", "")
	if w.Code != 200 || (!strings.Contains(w.Body.String(), "红") && !strings.Contains(strings.ToLower(w.Body.String()), "red")) {
		t.Fatal("图片识别未返回预期颜色")
	}

	stopID := create()
	send(stopID, "请调用 terminal 执行 sleep 15，然后回复 STOP_TEST。不要修改任何文件。", nil, "live_stop_"+randomID(8))
	time.Sleep(500 * time.Millisecond)
	w = authorized(s, "POST", "/api/sessions/"+stopID+"/stop", `{}`)
	if w.Code != 200 && w.Code != 202 {
		t.Fatalf("停止请求失败: %d", w.Code)
	}
	if result := wait(stopID); result != "cancelled" && result != "interrupted" {
		t.Fatalf("停止状态未确认: %s", result)
	}
	t.Log("真实聊天、多轮工具、事件、重启恢复、幂等、图片、附件刷新及停止验收通过")
}

type terminalEventRecorder struct {
	*httptest.ResponseRecorder
	cancel context.CancelFunc
}

func (w *terminalEventRecorder) Write(b []byte) (int, error) {
	n, err := w.ResponseRecorder.Write(b)
	if bytes.Contains(w.Body.Bytes(), []byte("run.completed")) || bytes.Contains(w.Body.Bytes(), []byte("run.failed")) {
		w.cancel()
	}
	return n, err
}

func TestLiveReadOnlyHistory(t *testing.T) {
	if os.Getenv("HAVEN_LIVE_HISTORY") != "true" {
		t.Skip("未启用真实只读历史验收")
	}
	t.Setenv("HAVEN_DATA_DIR", t.TempDir())
	s, err := newServer()
	if err != nil {
		t.Fatal(err)
	}
	if s.historyDB == "" {
		t.Fatal("必须配置只读数据库")
	}
	for _, id := range strings.Split(os.Getenv("HAVEN_LIVE_HISTORY_SESSIONS"), ",") {
		if !validID(id) {
			t.Fatal("历史会话编号无效")
		}
		body, code, err := s.fetchMessages(context.Background(), id, "0")
		if err != nil || code != 200 {
			t.Fatalf("只读读取失败: %d %v", code, err)
		}
		rows, _ := body["data"].([]any)
		t.Logf("只读会话 %s，返回 %d 条消息", id, len(rows))
	}
}
