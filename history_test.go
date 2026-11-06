package main

import (
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfiguredHistoryReaderUsesReadOnlySessionDB(t *testing.T) {
	python, err := exec.LookPath("python")
	if err != nil {
		python, err = exec.LookPath("python3")
	}
	if err != nil {
		t.Skip("未安装 Python")
	}
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
