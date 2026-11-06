package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// 显式启用后只在本次新建会话中测试真实模型与工具，不进入任何旧会话。
func TestLiveHermesControls(t *testing.T) {
	if os.Getenv("HAVEN_LIVE_CONTROLS") != "true" {
		t.Skip("未启用真实模型审批与提问验收")
	}
	data := os.Getenv("HAVEN_LIVE_DATA_DIR")
	if !filepath.IsAbs(data) {
		t.Fatal("必须指定独立绝对数据目录")
	}
	if err := os.Mkdir(data, 0700); err != nil {
		t.Fatal("必须使用本次新建的独立数据目录：", err)
	}
	t.Setenv("HAVEN_DATA_DIR", data)
	s, err := newServer()
	if err != nil {
		t.Fatal(err)
	}
	w := authorized(s, "POST", "/api/sessions", `{}`)
	var created struct {
		ID string `json:"id"`
	}
	if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &created) != nil || !strings.HasPrefix(created.ID, "haven_") {
		t.Fatalf("创建隔离会话失败：%d %s", w.Code, w.Body)
	}
	sid := created.ID
	t.Log("本次新增验收会话：", sid)
	target := filepath.Join(data, "only-new-approval-fixture")
	if err := os.Mkdir(target, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "fixture.txt"), []byte("Haven controls acceptance"), 0600); err != nil {
		t.Fatal(err)
	}
	target = filepath.Join(target, "fixture.txt")
	t.Cleanup(func() { _ = os.Chmod(target, 0600) })
	cases := []struct{ label, text, kind, value string }{
		{"补充提问", `这是 Haven 网页补充提问接口验收。请现在必须调用 clarify 工具，提出一个问题“验收报告使用哪种语言？”，提供选项“中文”和“英文”，等我通过工具回答后，简短回复“已收到：”加我的回答。不要用普通正文代替 clarify 工具，不要读取其他会话或文件。`, "clarify", "中文"},
		{"拒绝审批", fmt.Sprintf("这是 Haven 审批拒绝验收。仅调用一次 terminal 工具执行命令 chmod 777 '%s'，让 Hermes 的手动审批机制询问我。这个文件是本次新建的独立测试夹具，父目录只允许 root 访问。不要使用其他工具、不要绕过审批，收到拒绝后结束本轮并说明未执行。", target), "approval", "deny"},
		{"允许一次", fmt.Sprintf("这是 Haven 单次审批验收。仅调用一次 terminal 工具执行命令 chmod 777 '%s'，等待 Hermes 的手动审批，然后报告执行结果。目标仅为本次新建的测试文件，父目录只允许 root 访问；不要修改任何其他文件，也不要绕过审批。", target), "approval", "once"},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			body, _ := json.Marshal(map[string]any{"text": tc.text, "request_id": "controls_" + randomID(12), "created_at": time.Now().UnixMilli()})
			w := authorized(s, "POST", "/api/sessions/"+sid+"/runs", string(body))
			var accepted struct {
				RunID string `json:"run_id"`
			}
			if w.Code != 202 || json.Unmarshal(w.Body.Bytes(), &accepted) != nil {
				t.Fatalf("发送失败：%d %s", w.Code, w.Body)
			}
			run := accepted.RunID
			t.Cleanup(func() {
				// 失败清理仅停止本测试持有的确切新运行，保留全部验收历史。
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				res, e := s.upstream(ctx, "GET", "/v1/runs/"+run, nil, nil)
				if e == nil {
					defer res.Body.Close()
					var state map[string]any
					json.NewDecoder(res.Body).Decode(&state)
					if state["status"] == "running" || state["status"] == "waiting_for_approval" {
						stopped, e := s.upstream(ctx, "POST", "/v1/runs/"+run+"/stop", map[string]any{}, nil)
						if e == nil {
							stopped.Body.Close()
						}
					}
				}
			})
			deadline := time.Now().Add(100 * time.Second)
			var prompt map[string]any
			for prompt == nil && time.Now().Before(deadline) {
				status := authorized(s, "GET", "/api/sessions/"+sid+"/status", "")
				var state map[string]any
				json.Unmarshal(status.Body.Bytes(), &state)
				if controls, ok := state["pending_controls"].([]any); ok {
					for _, p := range controls {
						row := p.(map[string]any)
						if row["kind"] == tc.kind {
							prompt = row
							break
						}
					}
				}
				if state["status"] == "completed" || state["status"] == "failed" {
					t.Fatalf("模型未发起预期原生等待：%s", status.Body)
				}
				if prompt == nil {
					time.Sleep(500 * time.Millisecond)
				}
			}
			if prompt == nil {
				t.Fatal("未等到真实工具请求")
			}
			reply := map[string]any{"run_id": run, "request_id": prompt["prompt_id"], "execution_generation": prompt["execution_generation"]}
			if tc.kind == "approval" {
				reply["choice"] = tc.value
			} else {
				reply["answer"] = tc.value
			}
			raw, _ := json.Marshal(reply)
			w = authorized(s, "POST", "/api/sessions/"+sid+"/"+tc.kind, string(raw))
			if w.Code != 200 {
				t.Fatalf("回答失败：%d %s", w.Code, w.Body)
			}
			for {
				status := authorized(s, "GET", "/api/sessions/"+sid+"/status", "")
				var state map[string]any
				json.Unmarshal(status.Body.Bytes(), &state)
				if state["status"] == "completed" {
					break
				}
				if state["status"] == "failed" || time.Now().After(deadline) {
					t.Fatalf("运行未正常完成：%s", status.Body)
				}
				time.Sleep(500 * time.Millisecond)
			}
			duplicate := authorized(s, "POST", "/api/sessions/"+sid+"/"+tc.kind, string(raw))
			if duplicate.Code != 409 {
				t.Fatal("已完成请求应拒绝再次回答", duplicate.Code)
			}
			info, err := os.Stat(target)
			if err != nil {
				t.Fatal(err)
			}
			if tc.value == "deny" && info.Mode().Perm() != 0600 {
				t.Fatal("拒绝后夹具权限应保持原样", info.Mode().Perm())
			}
			if tc.value == "once" && info.Mode().Perm() != 0777 {
				t.Fatal("允许后应由工具处理本次新建夹具", info.Mode().Perm())
			}
			t.Logf("原生 %s 已处理，原运行 %s 完成，重复回答拒绝", tc.kind, run)
		})
		if t.Failed() {
			break
		}
	}
}
