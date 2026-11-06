package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"
)

type controlReply struct {
	RunID      string `json:"run_id"`
	RequestID  string `json:"request_id"`
	Generation *int64 `json:"execution_generation"`
	Choice     string `json:"choice,omitempty"`
	Answer     string `json:"answer,omitempty"`
}

// 审批和补充回答只解除原运行的等待，不创建推理、修改历史或扩大授权范围。
func (s *server) respondControl(w http.ResponseWriter, r *http.Request, sessionID, kind string) {
	var b controlReply
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 80<<10))
	d.DisallowUnknownFields()
	if d.Decode(&b) != nil || d.Decode(new(any)) != io.EOF || !validID(b.RunID) ||
		b.RequestID == "" || len(b.RequestID) > 512 || strings.ContainsAny(b.RequestID, "\r\n\x00") ||
		b.Generation == nil || *b.Generation < 1 || !utf8.ValidString(b.Answer) || len([]rune(b.Answer)) > 16384 ||
		(kind == "approval" && (b.Choice != "once" && b.Choice != "deny" || b.Answer != "")) ||
		(kind == "clarify" && (strings.TrimSpace(b.Answer) == "" || b.Choice != "")) {
		fail(w, 400, "审批或补充回答参数无效；审批仅支持允许一次或拒绝")
		return
	}
	own, ok := s.owned(sessionID)
	if (!ok || own.RunID != b.RunID) && !s.submissions.hasRun(sessionID, b.RunID) {
		fail(w, 403, "运行不属于此会话，无法处理请求")
		return
	}
	path := "/v1/runs/" + url.PathEscape(b.RunID)
	res, err := s.upstream(r.Context(), "GET", path, nil, nil)
	if err != nil {
		fail(w, 502, "暂时无法核对待处理请求，尚未发送回答")
		return
	}
	var state struct {
		RunID    string `json:"run_id"`
		Status   string `json:"status"`
		Controls []struct {
			ID         string   `json:"prompt_id"`
			Kind       string   `json:"kind"`
			Generation int64    `json:"execution_generation"`
			Choices    []string `json:"choices"`
		} `json:"pending_controls"`
	}
	err = json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&state)
	res.Body.Close()
	if res.StatusCode != 200 || err != nil || state.RunID != b.RunID {
		jsonOut(w, 409, map[string]any{"error": "运行已不可用或无法核对，请刷新状态", "code": "control_not_pending"})
		return
	}
	matched := false
	if state.Status == "running" || state.Status == "waiting_for_approval" || state.Status == "started" {
		for _, p := range state.Controls {
			if p.ID != b.RequestID || p.Kind != kind || p.Generation != *b.Generation {
				continue
			}
			matched = kind == "clarify"
			for _, choice := range p.Choices {
				if kind == "approval" && choice == b.Choice {
					matched = true
				}
			}
		}
	}
	if !matched {
		jsonOut(w, 409, map[string]any{"error": "该请求已处理、已过期或执行代次已变化，请刷新状态", "code": "control_not_pending"})
		return
	}
	body := map[string]any{"request_id": b.RequestID, "execution_generation": *b.Generation}
	if kind == "approval" {
		body["choice"] = b.Choice
	} else {
		body["answer"] = b.Answer
	}
	// 原生接口再次校验身份与代次；并发处理只会解除同一个等待，绝不消费下一个请求。
	res, err = s.upstream(r.Context(), "POST", path+"/"+kind, body, nil)
	if err != nil {
		jsonOut(w, 502, map[string]any{"error": "处理结果尚未确认，请核对状态；不会自动再次提交", "code": "control_result_unknown"})
		return
	}
	defer res.Body.Close()
	var result map[string]any
	if json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&result) != nil {
		jsonOut(w, 502, map[string]any{"error": "Hermes 返回结果无法确认，请核对状态", "code": "control_result_unknown"})
		return
	}
	if res.StatusCode >= 200 && res.StatusCode < 300 &&
		(res.StatusCode != 200 || result["status"] != "resolved" && result["status"] != "already_resolved") {
		jsonOut(w, 502, map[string]any{"error": "Hermes 未返回可核对的处理结果，请核对状态", "code": "control_result_unknown"})
		return
	}
	if res.StatusCode >= 500 {
		result["code"] = "control_result_unknown"
	}
	jsonOut(w, res.StatusCode, result)
}
