package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// 官方消息 HTTP 接口会回填部分旧记录的显示索引；完整历史改用官方只读 SessionDB。
const readOnlyHistoryScript = `import sys, json, pathlib, contextlib
source, database, session, offset = sys.argv[1:]
sys.path.insert(0, source)
with contextlib.redirect_stdout(sys.stderr):
    from hermes_state import SessionDB
    from agent.compaction_display import _COMPACTION_INTERNAL_FIELDS, project_compaction_message_for_display
    db = SessionDB(pathlib.Path(database), read_only=True)
try:
    db._conn.execute("PRAGMA query_only=ON")
    if not db.get_session(session):
        result = {"code": 404, "body": {"error": "会话不存在"}}
    else:
        resolved = db.resolve_resume_session_id(session)
        rows = db.get_messages(resolved, limit=100, offset=int(offset), latest=True, include_compacted=True, include_ancestors=True)
        keys = ("id", "session_id", "role", "content", "tool_call_id", "tool_calls", "tool_name", "timestamp", "token_count", "finish_reason", "reasoning", "reasoning_content", "display_kind")
        data = []
        for message in rows:
            if message.get("display_kind") == "hidden" and (message.get("display_metadata") or {}).get("notification_category") == "diagnostic":
                projected = {k: message[k] for k in ("id", "session_id", "role", "timestamp", "display_kind") if k in message}
                projected["content"] = ""
            else:
                projected = project_compaction_message_for_display(message)
                if projected is None:
                    projected = {k: v for k, v in message.items() if k not in _COMPACTION_INTERNAL_FIELDS}
                    projected.update(content="", display_kind="hidden")
            data.append({k: projected[k] for k in keys if k in projected})
        result = {"code": 200, "body": {"object": "list", "session_id": resolved, "data": data, "pagination": {"limit": 100, "offset": int(offset), "order": "latest", "returned": len(data)}}}
    print(json.dumps(result, ensure_ascii=False))
finally:
    db.close()
`

func (s *server) prepareHistoryReader() error {
	if s.historyDB == "" {
		return nil
	}
	if st, err := os.Stat(s.historyDB); err != nil || !st.Mode().IsRegular() {
		return errors.New("Hermes 只读历史数据库不可用")
	}
	f, err := os.CreateTemp(s.dataDir, ".history-reader-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0600); err == nil {
		_, err = f.WriteString(readOnlyHistoryScript)
	}
	if err == nil {
		err = f.Sync()
	}
	if e := f.Close(); err == nil {
		err = e
	}
	if err != nil {
		return err
	}
	s.historyScript = filepath.Join(s.dataDir, "history-reader-v1.py")
	return os.Rename(f.Name(), s.historyScript)
}

type boundedOutput struct {
	buffer bytes.Buffer
	limit  int
}

func (b *boundedOutput) Write(p []byte) (int, error) {
	if b.buffer.Len()+len(p) > b.limit {
		return 0, errors.New("历史输出超过限制")
	}
	return b.buffer.Write(p)
}
func (b *boundedOutput) Bytes() []byte { return b.buffer.Bytes() }

func (s *server) readOnlyMessages(ctx context.Context, id, offset string) (map[string]any, int, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, s.historyPython, s.historyScript, s.historySource, s.historyDB, id, offset)
	cmd.Dir = s.historySource
	cmd.Env = append(os.Environ(), "PYTHONDONTWRITEBYTECODE=1", "PYTHONIOENCODING=utf-8", "HERMES_DISABLE_LAZY_INSTALLS=true")
	output := &boundedOutput{limit: 32 << 20}
	diagnostic := &boundedOutput{limit: 16 << 10}
	cmd.Stdout = output
	cmd.Stderr = diagnostic
	if err := cmd.Run(); err != nil {
		return nil, 503, errors.New("Hermes 只读历史读取失败；未回退到可能写入索引的接口")
	}
	var reply struct {
		Code int            `json:"code"`
		Body map[string]any `json:"body"`
	}
	if json.Unmarshal(output.Bytes(), &reply) != nil || reply.Body == nil || (reply.Code != 200 && reply.Code != 404) {
		return nil, 503, errors.New("Hermes 只读历史响应无效")
	}
	return reply.Body, reply.Code, nil
}
