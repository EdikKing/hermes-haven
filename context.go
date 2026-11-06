package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"time"
)

//go:embed internal/context/reader.py
var contextReader string

func (s *server) contextUsage(w http.ResponseWriter, r *http.Request, id string) {
	if s.historyDB == "" {
		jsonOut(w, 200, map[string]any{"used_tokens": nil, "max_tokens": nil, "remaining_tokens": nil, "source": "unavailable"})
		return
	}
	settings, _ := json.Marshal(s.savedSettings(id))
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, s.historyPython, "-c", contextReader, s.historySource, s.historyDB, id, string(settings))
	cmd.Dir = s.historySource
	cmd.Env = append(os.Environ(), "PYTHONDONTWRITEBYTECODE=1", "PYTHONIOENCODING=utf-8", "HERMES_DISABLE_LAZY_INSTALLS=true")
	output, diagnostic := &boundedOutput{limit: 64 << 10}, &boundedOutput{limit: 16 << 10}
	cmd.Stdout, cmd.Stderr = output, diagnostic
	if cmd.Run() != nil {
		fail(w, 503, "上下文用量暂不可用")
		return
	}
	var reply struct {
		Code int            `json:"code"`
		Body map[string]any `json:"body"`
	}
	if json.Unmarshal(output.Bytes(), &reply) != nil || reply.Body == nil || (reply.Code != 200 && reply.Code != 404) {
		fail(w, 503, "上下文统计响应无效")
		return
	}
	jsonOut(w, reply.Code, reply.Body)
}
