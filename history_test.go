package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestDecodedImageBlocksDoNotExposePaths(t *testing.T) {
	for _, representation := range []string{"JSON字符串", "已解码数组"} {
		t.Run(representation, func(t *testing.T) {
			root := t.TempDir()
			imagePath := filepath.Join(root, "old.png")
			png := []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0, 0, 0, 0}
			if err := os.WriteFile(imagePath, png, 0600); err != nil {
				t.Fatal(err)
			}
			blocks := []map[string]any{{"type": "text", "text": "保留旧消息文字"}, {"type": "image", "path": imagePath, "url": "file:///private/old/image.png"}}
			var content any = blocks
			if representation == "JSON字符串" {
				raw, err := json.Marshal(blocks)
				if err != nil {
					t.Fatal(err)
				}
				content = string(raw)
			}
			var calls atomic.Int32
			s, _ := testServer(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Method != http.MethodGet || r.URL.Path != "/api/sessions/old/messages" {
					t.Errorf("旧图片只能读取消息: %s %s", r.Method, r.URL.Path)
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"id": 1, "content": content}}})
			})
			s.imageRoots = []string{root}
			w := authorized(s, "GET", "/api/sessions/old/messages", "")
			var data struct {
				Data []struct {
					Content []map[string]any `json:"content"`
				} `json:"data"`
			}
			if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &data) != nil || len(data.Data) != 1 || len(data.Data[0].Content) != 2 {
				t.Fatalf("旧图片消息读取失败: %d %s", w.Code, w.Body.String())
			}
			if data.Data[0].Content[0]["text"] != "保留旧消息文字" {
				t.Fatal("旧消息文字应保留")
			}
			image := data.Data[0].Content[1]
			for _, field := range []string{"path", "url"} {
				if _, exists := image[field]; exists {
					t.Fatalf("旧图片不得返回本地路径字段: %s", field)
				}
			}
			src, _ := image["src"].(string)
			if !strings.Contains(src, "ref=") || strings.Contains(w.Body.String(), "file://") {
				t.Fatal("旧图片必须使用受控引用")
			}
			w = authorized(s, "GET", src, "")
			if w.Code != http.StatusOK || !bytes.Equal(w.Body.Bytes(), png) || calls.Load() != 1 {
				t.Fatalf("受控引用应直接读取原图片: %d，上游请求 %d 次", w.Code, calls.Load())
			}
			w = authorized(s, "GET", "/api/sessions/old/images/1/1", "")
			if w.Code != http.StatusOK || !bytes.Equal(w.Body.Bytes(), png) || calls.Load() != 2 {
				t.Fatalf("旧链接应兼容内容块原始位置: %d，上游请求 %d 次", w.Code, calls.Load())
			}
		})
	}
}

func TestStartupRequiresReadOnlyHistoryCapability(t *testing.T) {
	python := historyPythonForTest(t)
	const supported = `import os
class Connection:
    def execute(self, sql):
        assert sql == "PRAGMA query_only=ON"
class SessionDB:
    def __init__(self, path, read_only=False):
        assert read_only is True
        assert os.environ["PYTHONDONTWRITEBYTECODE"] == "1"
        assert os.environ["HERMES_DISABLE_LAZY_INSTALLS"] == "true"
        self._conn = Connection()
    def get_session(self, session): return None
    def close(self): pass
`
	for _, tc := range []struct {
		name    string
		module  string
		wantErr bool
	}{
		{"只读能力可用", supported, false},
		{"接口不支持只读", strings.Replace(supported, "def __init__(self, path, read_only=False):", "def __init__(self, path):", 1), true},
		{"读取能力异常", strings.Replace(supported, "return None", "raise RuntimeError('模拟读取失败')", 1), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _ = testServer(t, func(http.ResponseWriter, *http.Request) {
				t.Error("启动能力检查不得调用消息 HTTP 接口")
			})
			source := t.TempDir()
			if err := os.Mkdir(filepath.Join(source, "agent"), 0700); err != nil {
				t.Fatal(err)
			}
			for name, body := range map[string]string{
				"agent/__init__.py":           "",
				"agent/compaction_display.py": "_COMPACTION_INTERNAL_FIELDS=()\ndef project_compaction_message_for_display(message): return message\n",
				"hermes_state.py":             tc.module,
				"original.db":                 "unchanged-history",
			} {
				if err := os.WriteFile(filepath.Join(source, filepath.FromSlash(name)), []byte(body), 0600); err != nil {
					t.Fatal(err)
				}
			}
			database := filepath.Join(source, "original.db")
			t.Setenv("HERMES_HISTORY_DB", database)
			t.Setenv("HERMES_HISTORY_PYTHON", python)
			t.Setenv("HERMES_SOURCE_DIR", source)
			_, err := newServer()
			if tc.wantErr {
				if err == nil || !strings.Contains(err.Error(), "无法确认 Hermes 只读历史能力") {
					t.Fatalf("无法确认只读能力时应拒绝启动: %v", err)
				}
			} else if err != nil {
				t.Fatalf("只读能力可用时应正常启动: %v", err)
			}
			raw, err := os.ReadFile(database)
			if err != nil || string(raw) != "unchanged-history" {
				t.Fatal("启动检查不得改动原历史文件")
			}
			for _, dir := range []string{source, filepath.Join(source, "agent")} {
				if _, err := os.Stat(filepath.Join(dir, "__pycache__")); !os.IsNotExist(err) {
					t.Fatal("启动检查不得向 Hermes 源码目录写入字节码")
				}
			}
		})
	}
}

func TestConfiguredHistoryReaderUsesReadOnlySessionDB(t *testing.T) {
	python := historyPythonForTest(t)
	s, _ := testServer(t, func(http.ResponseWriter, *http.Request) {
		t.Fatal("已配置只读历史时不得请求消息 HTTP 接口")
	})
	root := t.TempDir()
	_ = os.Mkdir(filepath.Join(root, "agent"), 0700)
	_ = os.WriteFile(filepath.Join(root, "agent", "__init__.py"), nil, 0600)
	_ = os.WriteFile(filepath.Join(root, "agent", "compaction_display.py"), []byte("_COMPACTION_INTERNAL_FIELDS=()\ndef project_compaction_message_for_display(message): return message\n"), 0600)
	module := `class Connection:
    def execute(self, sql):
        assert sql == "PRAGMA query_only=ON"
class SessionDB:
    def __init__(self, path, read_only=False):
        assert read_only is True
        self._conn=Connection()
    def get_session(self, session): return {"id":session}
    def resolve_resume_session_id(self, session): return session
    def get_messages(self, session, **kwargs):
        assert kwargs["include_compacted"] and kwargs["include_ancestors"]
        assert kwargs["latest"] and kwargs["offset"] == 5000
        return [{"id":5001,"role":"user","content":"旧历史完整展示"}]
    def close(self): pass
`
	_ = os.WriteFile(filepath.Join(root, "hermes_state.py"), []byte(module), 0600)
	s.historyDB = filepath.Join(root, "original.db")
	_ = os.WriteFile(s.historyDB, []byte("unchanged-history"), 0600)
	s.historyPython = python
	s.historySource = root
	if err := s.prepareHistoryReader(); err != nil {
		t.Fatal(err)
	}
	w := authorized(s, "GET", "/api/sessions/legacy/messages?offset=5000", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "旧历史完整展示") {
		t.Fatalf("只读历史读取失败: %d %s", w.Code, w.Body.String())
	}
	raw, _ := os.ReadFile(s.historyDB)
	if string(raw) != "unchanged-history" {
		t.Fatal("原历史文件不得被改动")
	}
}

func historyPythonForTest(t *testing.T) string {
	t.Helper()
	python, err := exec.LookPath("python")
	if err != nil {
		python, err = exec.LookPath("python3")
	}
	if err != nil {
		t.Skip("未安装 Python")
	}
	return python
}
