package main

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// 真实验收只向本次新建会话发送，附件与生成文件保留在独立目录中。
func TestLiveHermesPhaseTwo(t *testing.T) {
	if os.Getenv("HAVEN_LIVE_HERMES") != "true" {
		t.Skip("未启用真实 Hermes 验收")
	}
	dir := os.Getenv("HAVEN_LIVE_DATA_DIR")
	if dir == "" || !filepath.IsAbs(dir) {
		t.Fatal("真实附件验收必须指定独立的绝对数据目录")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HAVEN_DATA_DIR", dir)
	s, err := newServer()
	if err != nil {
		t.Fatal(err)
	}
	w := authorized(s, "POST", "/api/sessions", `{}`)
	var created struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &created)
	if w.Code != 201 || !strings.HasPrefix(created.ID, "haven_") {
		t.Fatalf("创建隔离会话失败: %d", w.Code)
	}
	t.Log("新增验收会话:", created.ID)
	upload := func(name string, raw []byte) attachment {
		t.Helper()
		w, a := uploadTestFile(t, s, name, bytes.NewReader(raw))
		if w.Code != 201 {
			t.Fatalf("真实上传失败: %d", w.Code)
		}
		w = authorized(s, "GET", "/api/uploads/"+a.ID+"/download", "")
		if w.Code != 200 || sha256.Sum256(w.Body.Bytes()) != sha256.Sum256(raw) {
			t.Fatal("下载内容摘要不一致")
		}
		return a
	}
	txt := upload("校验文本.txt", []byte("文本校验词：HAVEN_TXT_EVIDENCE_836"))
	var document bytes.Buffer
	archive := zip.NewWriter(&document)
	for name, content := range map[string]string{
		"[Content_Types].xml": `<?xml version="1.0"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/></Types>`,
		"_rels/.rels":         `<?xml version="1.0"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>`,
		"word/document.xml":   `<?xml version="1.0"?><w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>Word 校验词：HAVEN_DOCX_EVIDENCE_491</w:t></w:r></w:p></w:body></w:document>`,
	} {
		part, err := archive.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = part.Write([]byte(content))
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	docx := upload("校验文档.docx", document.Bytes())
	bin := upload("不执行的任意附件.bin", []byte{0, 1, 2, 3, 255, 254, 0})
	var pixels bytes.Buffer
	picture := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			picture.SetRGBA(x, y, color.RGBA{R: 128, G: 90, B: 200, A: 255})
		}
	}
	if err := png.Encode(&pixels, picture); err != nil {
		t.Fatal(err)
	}
	pic := upload("混合附件.png", pixels.Bytes())
	message := "这是 Hermes Haven 新建隔离验收会话。请用 read_file 分别读取附上的 TXT 和 DOCX，回答两份文档内各自的校验词。BIN 只用于存取测试，不要运行或读取它。不修改、删除任何已有文件、会话、配置或历史。"
	body := attachmentRequest(message, "phase2_read_"+randomID(8), []string{pic.ID}, []string{txt.ID, docx.ID, bin.ID})
	send := func(body string) {
		t.Helper()
		w := authorized(s, "POST", "/api/sessions/"+created.ID+"/runs", body)
		if w.Code != 202 {
			t.Fatalf("真实发送失败: %d %s", w.Code, w.Body.String())
		}
		deadline := time.Now().Add(3 * time.Minute)
		for time.Now().Before(deadline) {
			w = authorized(s, "GET", "/api/sessions/"+created.ID+"/status", "")
			var state struct {
				Status string `json:"status"`
			}
			_ = json.Unmarshal(w.Body.Bytes(), &state)
			if state.Status == "completed" {
				return
			}
			if state.Status == "failed" || state.Status == "cancelled" || state.Status == "unknown" || state.Status == "waiting_for_approval" {
				t.Fatalf("真实运行未完成: %s", state.Status)
			}
			time.Sleep(400 * time.Millisecond)
		}
		t.Fatal("真实附件验收超时")
	}
	history := func() []map[string]any {
		t.Helper()
		w := authorized(s, "GET", "/api/sessions/"+created.ID+"/messages", "")
		var result struct {
			Data []map[string]any `json:"data"`
		}
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil {
			t.Fatal("读取真实历史失败")
		}
		return result.Data
	}
	send(body)
	rows := history()
	var assistantText strings.Builder
	toolCount, associated := 0, false
	for _, row := range rows {
		if row["role"] == "assistant" {
			assistantText.WriteString(messageTextForTest(row["content"]))
		}
		if row["role"] == "tool" {
			toolCount++
		}
		if row["role"] == "user" {
			if items, ok := row["attachments"].([]any); ok && len(items) == 4 && messageTextForTest(row["content"]) == message {
				associated = true
			}
		}
	}
	if !strings.Contains(assistantText.String(), "HAVEN_TXT_EVIDENCE_836") || !strings.Contains(assistantText.String(), "HAVEN_DOCX_EVIDENCE_491") || toolCount < 2 || !associated {
		t.Fatalf("文档内容或附件关联未验证，工具结果 %d，关联 %t", toolCount, associated)
	}
	before := len(rows)
	restarted, err := newServer()
	if err != nil {
		t.Fatal(err)
	}
	s = restarted
	w = authorized(s, "POST", "/api/sessions/"+created.ID+"/runs", body)
	if w.Code != 202 || len(history()) != before {
		t.Fatal("重启后重放新增了消息")
	}
	generate := "请只在本次输入末尾指定的文件输出目录中新建 haven-acceptance.csv，使用 write_file 写入准确内容 name,value 换行 haven,42 换行。不要修改、删除其它任何文件或历史。完成后在最终回复中用指定的 sandbox 文件链接给出该 CSV。"
	send(attachmentRequest(generate, "phase2_export_"+randomID(8), nil, nil))
	rows = history()
	found := false
	for _, row := range rows {
		if row["role"] != "assistant" {
			continue
		}
		for _, item := range anySlice(row["attachments"]) {
			a, ok := item.(map[string]any)
			if !ok || a["name"] != "haven-acceptance.csv" {
				continue
			}
			w = authorized(s, "GET", "/api/uploads/"+a["id"].(string)+"/download", "")
			if w.Code != 200 || strings.ReplaceAll(w.Body.String(), "\r\n", "\n") != "name,value\nhaven,42\n" {
				t.Fatal("生成文件内容不符")
			}
			found = true
		}
	}
	if !found {
		t.Fatal("没有登记真实生成文件")
	}
	t.Log("真实文本、DOCX、任意二进制、工具、附件关联、重启重放与 CSV 生成下载通过")
}

func anySlice(value any) []any { rows, _ := value.([]any); return rows }
func messageTextForTest(content any) string {
	if text, ok := content.(string); ok {
		return text
	}
	var text strings.Builder
	if blocks, ok := contentBlocks(content); ok {
		for _, block := range blocks {
			if block["type"] == "text" {
				text.WriteString(fmtString(block["text"]))
			}
		}
	}
	return text.String()
}
func fmtString(value any) string { text, _ := value.(string); return text }
