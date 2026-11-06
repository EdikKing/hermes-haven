package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
)

var renderedImageSource = regexp.MustCompile(`!\[生成图片\]\(([^)]+)\)`)

func TestGeneratedImageReferencesShowOriginalWithoutNewRun(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "生成 原图.png")
	png := []byte{0x89, 'P', 'N', 'G', 13, 10, 26, 10, 0, 0, 0, 0}
	if err := os.WriteFile(path, png, 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, content string
		blocks        bool
	}{
		{"截图中的行内引用", "你好，图片生成好了：MEDIA:" + strings.ReplaceAll(path, " ", "%20"), false},
		{"独占一行的旧引用", "MEDIA:" + path, false},
		{"引号中的空格", "图片已生成：MEDIA:\"" + path + "\"", false},
		{"反引号中的引用", "图片已生成：`MEDIA:" + strings.ReplaceAll(path, " ", "%20") + "`", false},
		{"Markdown图片", "![图片](<" + path + ">)", false},
		{"内容块中的图片链接", "[图片](<sandbox:" + path + ">)", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var reads atomic.Int32
			s, _ := testServer(t, func(w http.ResponseWriter, r *http.Request) {
				reads.Add(1)
				if r.Method != "GET" || r.URL.Path != "/api/sessions/generated/messages" {
					t.Errorf("展示原图不应生成新运行: %s %s", r.Method, r.URL.Path)
				}
				var content any = tc.content
				if tc.blocks {
					content = []any{map[string]any{"type": "text", "text": tc.content}}
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"id": 21700, "role": "assistant", "content": content}}})
			})
			s.imageRoots = []string{root}
			w := authorized(s, "GET", "/api/sessions/generated/messages", "")
			var response struct {
				Data []map[string]any `json:"data"`
			}
			if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &response) != nil || len(response.Data) != 1 {
				t.Fatal(w.Body.String())
			}
			text := messageTextForTest(response.Data[0]["content"])
			match := renderedImageSource.FindStringSubmatch(text)
			if len(match) != 2 || strings.Contains(text, "MEDIA:") || strings.Contains(text, root) || strings.Contains(text, "`") || !strings.Contains(text, "查看原图") {
				t.Fatalf("图片必须直接显示并提供原图链接: %s", text)
			}
			w = authorized(s, "GET", match[1], "")
			if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), png) || reads.Load() != 1 {
				t.Fatalf("图片应返回原始字节且无需再读历史: %d，历史读取 %d 次", w.Code, reads.Load())
			}
			w = authorized(s, "GET", strings.Replace(match[1], "/sessions/generated/", "/sessions/another/", 1), "")
			if w.Code != 403 {
				t.Fatal("原图引用不能移用于其他会话")
			}
			w = httptest.NewRecorder()
			s.api(w, httptest.NewRequest("GET", "http://haven.test"+match[1], nil))
			if w.Code != 401 {
				t.Fatal("原图必须登录后才能读取")
			}
		})
	}
}

func TestGeneratedImageReferencesKeepFileBoundaries(t *testing.T) {
	s, _ := testServer(t, func(http.ResponseWriter, *http.Request) { t.Fatal("图片边界检查不应访问上游") })
	allowed := filepath.Join(t.TempDir(), "images")
	if err := os.Mkdir(allowed, 0700); err != nil {
		t.Fatal(err)
	}
	s.imageRoots = []string{allowed}
	outside := filepath.Join(filepath.Dir(allowed), "images-private", "secret.png")
	if s.generatedImageReference("session", "message", 0, outside) != "" {
		t.Fatal("同前缀目录不能取得图片引用")
	}
	for _, name := range []string{"page.svg", "report.pdf", "secret.txt"} {
		if s.generatedImageReference("session", "message", 0, filepath.Join(allowed, name)) != "" {
			t.Fatal("非栅格图片不能作为生成图片展示")
		}
	}
	fake := filepath.Join(allowed, "fake.png")
	if err := os.WriteFile(fake, []byte("private plain text"), 0600); err != nil {
		t.Fatal(err)
	}
	src := s.generatedImageReference("session", "message", 0, fake)
	if w := authorized(s, "GET", src, ""); w.Code != 415 || strings.Contains(w.Body.String(), "private plain text") {
		t.Fatal("图片扩展名不能绕过真实文件类型检查")
	}
	t.Run("符号链接不能逃逸", func(t *testing.T) {
		outside := filepath.Join(filepath.Dir(allowed), "secret.png")
		if err := os.WriteFile(outside, []byte{0x89, 'P', 'N', 'G', 13, 10, 26, 10}, 0600); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(allowed, "link.png")
		if err := os.Symlink(outside, link); err != nil {
			t.Skip("本机不允许创建符号链接，Linux 验证时执行")
		}
		src := s.generatedImageReference("session", "message", 0, link)
		if w := authorized(s, "GET", src, ""); w.Code != 403 {
			t.Fatal("符号链接不能读取允许目录以外的文件")
		}
	})
}

func TestGeneratedOutputImageIsInlineAndKeepsOriginalCopy(t *testing.T) {
	var content string
	s, _ := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Fatal("展示生成图片不应执行写操作")
		}
		switch r.URL.Path {
		case "/v1/runs/image_run":
			fmt.Fprint(w, `{"status":"completed"}`)
		case "/api/sessions/output_image/messages":
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"id": 2, "role": "assistant", "content": content}}})
		default:
			t.Errorf("意外上游请求: %s", r.URL.Path)
		}
	})
	output := filepath.Join(s.dataDir, "outputs", "image_request")
	if err := os.MkdirAll(output, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(output, "original.png")
	png := []byte{0x89, 'P', 'N', 'G', 13, 10, 26, 10, 0, 0, 0, 0}
	if err := os.WriteFile(path, png, 0600); err != nil {
		t.Fatal(err)
	}
	rec := submission{Version: 2, SessionID: "output_image", RequestID: "image_request", RunID: "image_run", State: "accepted", OutputDir: output}
	if err := s.submissions.save(rec); err != nil {
		t.Fatal(err)
	}
	content = "图片已生成：MEDIA:" + path
	w := authorized(s, "GET", "/api/sessions/output_image/messages", "")
	var response struct {
		Data []struct {
			Content     string       `json:"content"`
			Attachments []attachment `json:"attachments"`
		} `json:"data"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &response) != nil || len(response.Data) != 1 || len(response.Data[0].Attachments) != 1 {
		t.Fatal(w.Body.String())
	}
	image := response.Data[0].Attachments[0]
	src := "/api/uploads/" + image.ID + "/preview"
	if image.Kind != "image" || !strings.Contains(response.Data[0].Content, "![生成图片]("+src+")") {
		t.Fatal("已登记图片应直接显示，而非只有下载卡片")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	w = authorized(s, "GET", "/api/sessions/output_image/messages", "")
	if !strings.Contains(w.Body.String(), image.ID) {
		t.Fatal("源文件移除后仍应保留已发布图片")
	}
	w = authorized(s, "GET", src, "")
	if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), png) {
		t.Fatal("图片预览必须使用未压缩的原文件副本")
	}
}
