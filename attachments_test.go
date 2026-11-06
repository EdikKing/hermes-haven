package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func uploadTestFile(t *testing.T, s *server, name string, reader io.Reader) (*httptest.ResponseRecorder, attachment) {
	t.Helper()
	s.mu.Lock()
	s.logins["test-session"] = loginSession{Username: "test", ExpiresAt: time.Now().Add(time.Hour)}
	s.mu.Unlock()
	pipe, writer := io.Pipe()
	multi := multipart.NewWriter(writer)
	done := make(chan struct{})
	go func() {
		defer close(done)
		part, err := multi.CreateFormFile("file", name)
		if err == nil {
			_, err = io.Copy(part, reader)
		}
		if err == nil {
			err = multi.Close()
		}
		writer.CloseWithError(err)
	}()
	r := httptest.NewRequest("POST", "http://haven.test/api/uploads", pipe)
	r.Header.Set("Content-Type", multi.FormDataContentType())
	r.Header.Set("Origin", "http://haven.test")
	r.AddCookie(&http.Cookie{Name: "haven_session", Value: "test-session"})
	w := httptest.NewRecorder()
	s.api(w, r)
	pipe.Close()
	<-done
	var a attachment
	if w.Code == 201 {
		if err := json.Unmarshal(w.Body.Bytes(), &a); err != nil {
			t.Fatal(err)
		}
		if a.ID == "" {
			t.Fatal("附件编号缺失")
		}
	}
	return w, a
}

func attachmentRequest(text, request string, images, files []string) string {
	raw, _ := json.Marshal(map[string]any{"text": text, "request_id": request, "images": images, "files": files})
	return string(raw)
}

func TestAttachmentFormatsNamesDownloadAndPreview(t *testing.T) {
	s, _ := testServer(t, func(http.ResponseWriter, *http.Request) { t.Error("附件存取不应调用 Hermes") })
	for _, tc := range []struct {
		name, preview string
		data          []byte
	}{
		{"同名 中文.txt", "text", []byte("你好，文件。\nsecond line")},
		{"同名 中文.txt", "text", []byte("不同内容")},
		{"任意.bin", "none", []byte{0, 1, 2, 3, 255}},
		{"页面.html", "text", []byte(`<script>window.hacked=true</script>`)},
		{"矢量.svg", "text", []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`)},
		{"文本.txt", "text", []byte{0xff, 0xfe, 0x60, 0x4f, 0x7d, 0x59}},
		{"报告.pdf", "pdf", []byte("%PDF-1.4\nfixture")},
		{"表格.xlsx", "none", []byte{'P', 'K', 3, 4, 0, 0, 0, 0}},
	} {
		t.Run(tc.name+tc.preview, func(t *testing.T) {
			w, a := uploadTestFile(t, s, tc.name, bytes.NewReader(tc.data))
			if w.Code != 201 || a.PreviewKind != tc.preview || a.Size != int64(len(tc.data)) {
				t.Fatalf("上传结果无效: %d %s", w.Code, w.Body.String())
			}
			w = authorized(s, "GET", "/api/uploads/"+a.ID+"/download", "")
			_, params, err := mime.ParseMediaType(w.Header().Get("Content-Disposition"))
			if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), tc.data) || err != nil || params["filename"] != tc.name {
				t.Fatalf("下载内容或名称改变: %d %s", w.Code, params)
			}
			w = authorized(s, "GET", "/api/uploads/"+a.ID+"/preview", "")
			if tc.preview == "none" {
				if w.Code != 415 {
					t.Fatalf("二进制不应预览: %d", w.Code)
				}
				return
			}
			if w.Code != 200 {
				t.Fatalf("预览失败: %d %s", w.Code, w.Body.String())
			}
			if tc.preview == "text" && strings.Contains(w.Header().Get("Content-Type"), "text/html") {
				t.Fatal("活动内容不能以 HTML 返回")
			}
			if tc.preview == "pdf" && w.Header().Get("Content-Type") != "application/pdf" {
				t.Fatal("PDF 类型错误")
			}
		})
	}
	entries, _ := os.ReadDir(filepath.Join(s.dataDir, "files"))
	if len(entries) != 8 {
		t.Fatal("同名文件发生覆盖")
	}
	for _, route := range []string{"/api/uploads/anything", "/api/uploads/anything/download", "/api/uploads/anything/preview"} {
		w := httptest.NewRecorder()
		s.api(w, httptest.NewRequest("GET", "http://haven.test"+route, nil))
		if w.Code != 401 {
			t.Fatal("附件路由必须登录")
		}
	}
}

type zeroReader struct{}

func (zeroReader) Read(b []byte) (int, error) { clear(b); return len(b), nil }

type brokenReader struct{ delivered bool }

func (b *brokenReader) Read(p []byte) (int, error) {
	if b.delivered {
		return 0, io.ErrUnexpectedEOF
	}
	b.delivered = true
	return copy(p, "incomplete"), nil
}

func TestAttachmentUploadLimitsAndFailureAtomicity(t *testing.T) {
	s, _ := testServer(t, func(http.ResponseWriter, *http.Request) { t.Error("上传不应调用 Hermes") })
	w, a := uploadTestFile(t, s, "exact.bin", io.LimitReader(zeroReader{}, maxFileBytes))
	if w.Code != 201 || a.Size != maxFileBytes {
		t.Fatalf("50 MiB 边界应接受: %d", w.Code)
	}
	w, _ = uploadTestFile(t, s, "over.bin", io.LimitReader(zeroReader{}, maxFileBytes+1))
	if w.Code != 413 {
		t.Fatalf("超限上传应拒绝: %d", w.Code)
	}
	png := []byte{0x89, 'P', 'N', 'G', 13, 10, 26, 10}
	w, _ = uploadTestFile(t, s, "large.png", io.MultiReader(bytes.NewReader(png), io.LimitReader(zeroReader{}, (5<<20))))
	if w.Code != 413 {
		t.Fatalf("图片应保留 5 MiB 限制: %d", w.Code)
	}
	w, _ = uploadTestFile(t, s, "interrupted.txt", &brokenReader{})
	if w.Code == 201 {
		t.Fatal("中断上传不能登记成功")
	}
	entries, _ := os.ReadDir(filepath.Join(s.dataDir, "files"))
	if len(entries) != 1 {
		t.Fatal("失败上传留下可见记录")
	}
	s.dataDir = t.TempDir()
	if err := os.WriteFile(filepath.Join(s.dataDir, "files"), []byte("blocked"), 0600); err != nil {
		t.Fatal(err)
	}
	w, _ = uploadTestFile(t, s, "fail.txt", strings.NewReader("fail"))
	if w.Code == 201 {
		t.Fatal("落盘失败不能登记成功")
	}
}

func TestAttachmentSendLimitsAndContentIntegrity(t *testing.T) {
	var posts atomic.Int32
	s, _ := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		posts.Add(1)
		w.WriteHeader(202)
		fmt.Fprint(w, `{"run_id":"limit_run"}`)
	})
	_, a := uploadTestFile(t, s, "first.bin", io.LimitReader(zeroReader{}, maxFileBytes))
	_, b := uploadTestFile(t, s, "second.bin", io.LimitReader(zeroReader{}, maxFileBytes))
	_, c := uploadTestFile(t, s, "third.txt", strings.NewReader("x"))
	for _, files := range [][]string{{a.ID, b.ID, c.ID}, {c.ID, c.ID}, {c.ID, c.ID, c.ID, c.ID, c.ID}, {"../../state"}} {
		w := authorized(s, "POST", "/api/sessions/limits/runs", attachmentRequest("", randomID(8), nil, files))
		if w.Code != 400 || posts.Load() != 0 {
			t.Fatalf("无效附件未拦截: %d", w.Code)
		}
	}
	w := authorized(s, "POST", "/api/sessions/limits/runs", attachmentRequest("", "total_exact", nil, []string{a.ID, b.ID}))
	if w.Code != 202 || posts.Load() != 1 {
		t.Fatalf("100 MiB 边界失败: %d %s", w.Code, w.Body.String())
	}
	meta, _ := s.attachment(c.ID)
	if err := os.WriteFile(filepath.Join(s.dataDir, "files", c.ID, meta.StorageName), []byte("y"), 0600); err != nil {
		t.Fatal(err)
	}
	w = authorized(s, "GET", "/api/uploads/"+c.ID+"/download", "")
	if w.Code != 409 {
		t.Fatal("内容改变后不能提供原附件")
	}
	w = authorized(s, "POST", "/api/sessions/limits/runs", attachmentRequest("", "tampered", nil, []string{c.ID}))
	if w.Code != 400 || posts.Load() != 1 {
		t.Fatal("内容改变后不能作为原附件发送")
	}
}

func TestAttachmentFrozenRetryAssociationAndArtifacts(t *testing.T) {
	var input string
	var rows []any
	var status atomic.Int32
	var posts atomic.Int32
	s, _ := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "POST" && r.URL.Path == "/v1/runs":
			var body struct {
				Input string `json:"input"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if posts.Add(1) > 1 && input != body.Input {
				t.Error("未知结果重试改变了上游输入")
			}
			input = body.Input
			if posts.Load() == 1 {
				w.WriteHeader(503)
				fmt.Fprint(w, `{}`)
			} else {
				w.WriteHeader(202)
				fmt.Fprint(w, `{"run_id":"artifact_run","status":"completed"}`)
			}
		case r.URL.Path == "/v1/capabilities":
			fmt.Fprint(w, `{"features":{"runs_idempotency":{"supported":true,"durable":true,"retention_seconds":86400}}}`)
		case r.URL.Path == "/v1/runs/artifact_run":
			status.Add(1)
			if status.Load() == 1 {
				fmt.Fprint(w, `{"status":"completed"}`)
			} else {
				w.WriteHeader(404)
				fmt.Fprint(w, `{}`)
			}
		case strings.HasSuffix(r.URL.Path, "/messages"):
			_ = json.NewEncoder(w).Encode(map[string]any{"data": rows})
		default:
			t.Errorf("不允许的上游操作: %s %s", r.Method, r.URL.Path)
		}
	})
	_, a := uploadTestFile(t, s, "source 中文.txt", strings.NewReader("上传原文"))
	body := attachmentRequest("请读文件并生成报告", "artifact_request", nil, []string{a.ID})
	w := authorized(s, "POST", "/api/sessions/files/runs", body)
	if w.Code != 502 {
		t.Fatal(w.Body.String())
	}
	restarted, err := newServer()
	if err != nil {
		t.Fatal(err)
	}
	w = authorized(restarted, "POST", "/api/sessions/files/runs", body)
	if w.Code != 202 || posts.Load() != 2 {
		t.Fatal(w.Body.String())
	}
	rec, _ := restarted.submissions.get("files", "artifact_request")
	path := filepath.Join(rec.OutputDir, "报告 中文.csv")
	if err := os.WriteFile(path, []byte("name,value\n验收,42\n"), 0600); err != nil {
		t.Fatal(err)
	}
	makeRows := func(blocks bool) []any {
		var content any = input
		if blocks {
			content = []any{map[string]any{"type": "text", "text": input}}
		}
		return []any{map[string]any{"id": 1, "role": "user", "content": content}, map[string]any{"id": 2, "role": "assistant", "content": "[报告](sandbox:" + url.PathEscape(path) + ")"}}
	}
	rows = makeRows(true)
	w = authorized(restarted, "GET", "/api/sessions/files/messages", "")
	if w.Code != 200 || strings.Contains(w.Body.String(), "Haven context") || strings.Contains(w.Body.String(), filepath.ToSlash(s.dataDir)) || strings.Contains(w.Body.String(), "storage_name") {
		t.Fatalf("关联或路径隐藏失败: %d %s", w.Code, w.Body.String())
	}
	var response struct {
		Data []struct {
			Attachments []attachment `json:"attachments"`
		} `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &response)
	if len(response.Data) != 2 || len(response.Data[0].Attachments) != 1 || response.Data[0].Attachments[0].ID != a.ID || len(response.Data[1].Attachments) != 1 {
		t.Fatal("附件没有绑定到对应消息")
	}
	generated := response.Data[1].Attachments[0]
	w = authorized(restarted, "GET", "/api/uploads/"+generated.ID+"/download", "")
	if w.Code != 200 || w.Body.String() != "name,value\n验收,42\n" {
		t.Fatal("生成文件下载失败")
	}
	if err := os.WriteFile(path, []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	rows = makeRows(false)
	w = authorized(restarted, "GET", "/api/sessions/files/messages", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), generated.ID) || status.Load() != 1 {
		t.Fatal("登记文件必须在上游运行过期后仍可读取")
	}
	w = authorized(restarted, "GET", "/api/uploads/"+generated.ID+"/download", "")
	if w.Body.String() != "name,value\n验收,42\n" {
		t.Fatal("输出文件变化不应改变已发布副本")
	}
	w = authorized(restarted, "POST", "/api/sessions/files/runs", body)
	if w.Code != 202 || posts.Load() != 2 {
		t.Fatal("受理后的重放不得重新执行")
	}
	rows = makeRows(false)
	w = authorized(restarted, "GET", "/api/sessions/another/messages", "")
	if strings.Contains(w.Body.String(), generated.ID) {
		t.Fatal("生成文件不得关联到另一个会话")
	}
}

func TestLegacyPendingRequestKeepsOriginalWireProtocol(t *testing.T) {
	var posts atomic.Int32
	s, _ := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/capabilities" {
			fmt.Fprint(w, `{"features":{"runs_idempotency":{"supported":true,"durable":true,"retention_seconds":86400}}}`)
			return
		}
		var body struct {
			Input string `json:"input"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Input != "旧协议内容" {
			t.Error("旧请求被追加了新协议说明")
		}
		posts.Add(1)
		w.WriteHeader(202)
		fmt.Fprint(w, `{"run_id":"legacy_existing"}`)
	})
	raw, _ := json.Marshal(map[string]any{"session_id": "legacy", "input": "旧协议内容"})
	rec := submission{SessionID: "legacy", RequestID: "legacy_request", Fingerprint: digest(string(raw)), Scope: digest(s.hermesURL + "\n" + s.apiKey), State: "unknown", CreatedAt: time.Now().UTC(), Text: "旧协议内容"}
	if err := s.submissions.save(rec); err != nil {
		t.Fatal(err)
	}
	restarted, err := newServer()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		w := authorized(restarted, "POST", "/api/sessions/legacy/runs", attachmentRequest("旧协议内容", "legacy_request", nil, nil))
		if w.Code != 202 || posts.Load() != 1 {
			t.Fatal("旧请求未保持幂等兼容")
		}
	}
}

func TestNewImageCardsRemoveOnlyDuplicateNativePresentation(t *testing.T) {
	s, _ := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(202)
		fmt.Fprint(w, `{"run_id":"image_card_run"}`)
	})
	_, pic := uploadTestFile(t, s, "tiny.png", bytes.NewReader([]byte{0x89, 'P', 'N', 'G', 13, 10, 26, 10, 0, 0, 0, 0}))
	w := authorized(s, "POST", "/api/sessions/image_cards/runs", attachmentRequest("原始文字", "image_card_request", []string{pic.ID}, nil))
	if w.Code != 202 {
		t.Fatal(w.Body.String())
	}
	rec, _ := s.submissions.get("image_cards", "image_card_request")
	makeRows := func() []any {
		return []any{map[string]any{"id": 1, "role": "user", "content": []any{map[string]any{"type": "text", "text": "原始文字\n\n" + rec.ContextNote + "\n\n[Image attached at: /native/tiny.png]"}, map[string]any{"type": "image", "path": "/native/tiny.png"}}}}
	}
	rows := makeRows()
	s.decorateAttachments(context.Background(), "image_cards", rows)
	message := rows[0].(map[string]any)
	if message["content"] != "原始文字" || len(message["attachments"].([]any)) != 1 {
		t.Fatal("新图片应只有原始正文和附件卡片，没有额外图片段落")
	}
	meta, _ := s.attachment(pic.ID)
	if err := os.Remove(filepath.Join(s.dataDir, "files", pic.ID, meta.StorageName)); err != nil {
		t.Fatal(err)
	}
	rows = makeRows()
	s.decorateAttachments(context.Background(), "image_cards", rows)
	if !strings.Contains(messageTextForTest(rows[0].(map[string]any)["content"]), "[Image attached at:") {
		t.Fatal("附件缺失时应保留原生历史图片通道")
	}
	legacy := []any{map[string]any{"id": 2, "role": "user", "content": "旧图片\n\n[Image attached at: /native/old.png]"}}
	s.decorateAttachments(context.Background(), "legacy_image", legacy)
	if legacy[0].(map[string]any)["content"] != "旧图片\n\n[Image attached at: /native/old.png]" {
		t.Fatal("不应改写旧图片展示内容")
	}
}

func TestAttachmentPreviewEncodingTruncationAndPDFRange(t *testing.T) {
	s, _ := testServer(t, func(http.ResponseWriter, *http.Request) { t.Fatal("预览不能访问 Hermes") })
	_, a := uploadTestFile(t, s, "large.txt", strings.NewReader(strings.Repeat("你", maxTextPreview/3+10)))
	w := authorized(s, "GET", "/api/uploads/"+a.ID+"/preview", "")
	var preview struct {
		Text      string
		Truncated bool
	}
	_ = json.Unmarshal(w.Body.Bytes(), &preview)
	if w.Code != 200 || !preview.Truncated || len(preview.Text) > maxTextPreview || strings.ContainsRune(preview.Text, '\ufffd') {
		t.Fatal("UTF-8 截断处理错误")
	}
	if _, _, err := decodePreview([]byte{0xff, 0xfe, 1}, false); err == nil {
		t.Fatal("损坏 UTF-16 应明确拒绝")
	}
	if _, _, err := decodePreview([]byte{0xff, 0xfe, 0x00, 0xd8}, false); err == nil {
		t.Fatal("未配对的 UTF-16 代理项应明确拒绝")
	}
	_, pdf := uploadTestFile(t, s, "range.pdf", strings.NewReader("%PDF-1.4\nfixture"))
	r := httptest.NewRequest("GET", "http://haven.test/api/uploads/"+pdf.ID+"/preview", nil)
	r.Header.Set("Range", "bytes=0-3")
	r.AddCookie(&http.Cookie{Name: "haven_session", Value: "test-session"})
	w = httptest.NewRecorder()
	w.Header().Set("X-Frame-Options", "DENY")
	s.api(w, r)
	if w.Code != 206 || w.Body.String() != "%PDF" {
		t.Fatal("PDF 范围读取失败")
	}
	if w.Header().Get("X-Frame-Options") != "SAMEORIGIN" {
		t.Fatal("PDF 必须允许本站阅读器嵌入")
	}
}

func TestUnknownAttachmentMutationBlocksExecutionButAcceptedReplayDoesNotNeedSource(t *testing.T) {
	var posts atomic.Int32
	s, _ := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			fmt.Fprint(w, `{"features":{"runs_idempotency":{"supported":true,"durable":true,"retention_seconds":86400}}}`)
			return
		}
		if posts.Add(1) == 1 {
			w.WriteHeader(503)
			fmt.Fprint(w, `{}`)
			return
		}
		w.WriteHeader(202)
		fmt.Fprint(w, `{"run_id":"frozen_attachment_run"}`)
	})
	_, a := uploadTestFile(t, s, "source.txt", strings.NewReader("original"))
	body := attachmentRequest("读取附件", "frozen_attachment", nil, []string{a.ID})
	if w := authorized(s, "POST", "/api/sessions/frozen/runs", body); w.Code != 502 {
		t.Fatal(w.Body.String())
	}
	meta, _ := s.attachment(a.ID)
	path := filepath.Join(s.dataDir, "files", a.ID, meta.StorageName)
	if err := os.WriteFile(path, []byte("modified"), 0600); err != nil {
		t.Fatal(err)
	}
	w := authorized(s, "POST", "/api/sessions/frozen/runs", body)
	if w.Code != 409 || posts.Load() != 1 || !strings.Contains(w.Body.String(), "retry_unsafe") {
		t.Fatal("未知结果且附件改变时不能重新执行")
	}
	if err := os.WriteFile(path, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	w = authorized(s, "POST", "/api/sessions/frozen/runs", body)
	if w.Code != 202 || posts.Load() != 2 {
		t.Fatal("附件恢复后仍应沿用原请求核对")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	w = authorized(s, "POST", "/api/sessions/frozen/runs", body)
	if w.Code != 202 || posts.Load() != 2 {
		t.Fatal("已受理的记录重放不需要重新读取或执行附件")
	}
}

func TestArtifactPathsCannotEscapeOutputDirectory(t *testing.T) {
	s, _ := testServer(t, func(http.ResponseWriter, *http.Request) { t.Fatal("文件边界测试不应调用 Hermes") })
	output := filepath.Join(s.dataDir, "outputs", "only-this-run")
	if err := os.MkdirAll(output, 0700); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(s.dataDir, "secret.txt")
	_ = os.WriteFile(outside, []byte("private"), 0600)
	rec := submission{SessionID: "current", RequestID: "current_request", OutputDir: output}
	if _, err := s.captureArtifact(rec, "message", outside); err == nil {
		t.Fatal("不能读取输出目录以外的文件")
	}
	if _, err := s.captureArtifact(rec, "message", output); err == nil {
		t.Fatal("不能将目录作为文件")
	}
	link := filepath.Join(output, "escape.txt")
	if err := os.Symlink(outside, link); err != nil {
		t.Skip("本机不允许创建符号链接；Linux race 测试另行验证")
	}
	if _, err := s.captureArtifact(rec, "message", link); err == nil {
		t.Fatal("符号链接逃逸未被拒绝")
	}
}
