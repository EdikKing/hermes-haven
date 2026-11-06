"""用于本机故障与界面验收的模拟服务，绝不连接真实 Hermes。"""
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import os
import threading
import time
import uuid
from urllib.parse import parse_qs, urlsplit

lock = threading.RLock()
sessions = [
    {"id": "preview_tools", "source": "cli", "title": "工具调用紧凑预览", "last_active": 1793956307},
    {"id": "preview_cli", "source": "cli", "title": "周末出行计划", "last_active": 1793926000},
    {"id": "preview_weixin", "source": "weixin", "title": "整理阅读清单", "last_active": 1793816000},
] + [{"id": f"preview_old_{i}", "source": "cli", "title": f"历史测试 {i}", "last_active": 1700000000 - i} for i in range(2400)] + [
    {"id": "preview_ancient", "source": "weixin", "title": "最早历史分页验收", "last_active": 1600000000}
]
messages = {"preview_cli": [{"id": i + 1, "role": "user" if i % 2 == 0 else "assistant", "content": f"长历史消息 {i + 1}", "timestamp": 1793926000 + i} for i in range(3105)]}
messages["preview_tools"] = [{"id": 1, "role": "user", "content": "帮我整理这篇笔记，检查内容后给出摘要。", "timestamp": 1793956307}]
for index, names in enumerate((("read_file", "terminal"), ("tool_call", "terminal"), ("write_file",), ("mcp__ekko_studio_interaction__ekko_studio_update_plan",))):
    calls = [{"id": f"preview_call_{index}_{number}", "function": {"name": name, "arguments": json.dumps({"path": "notes/example.md"})}} for number, name in enumerate(names)]
    messages["preview_tools"].append({"id": len(messages["preview_tools"]) + 1, "role": "assistant", "content": "", "tool_calls": calls, "timestamp": 1793956308 + index})
    for call in reversed(calls):
        messages["preview_tools"].append({"id": len(messages["preview_tools"]) + 1, "role": "tool", "tool_call_id": call["id"], "tool_name": call["function"]["name"], "content": "执行成功，笔记内容已核对。", "timestamp": 1793956308 + index})
messages["preview_tools"].append({"id": len(messages["preview_tools"]) + 1, "role": "assistant", "content": "笔记已整理完成。\n\n### 内容摘要\n\n工具调用、文件检查和整理结果现在集中展示，展开上方工具区即可查看每一步的参数与结果。", "timestamp": 1793956312})
if os.environ.get("MOCK_IMAGE_PATH"):
    messages["preview_cli"][2]["content"] = "[Image attached at: " + os.environ["MOCK_IMAGE_PATH"] + "]"
runs, reservations = {}, {}
control = {"mode": "normal", "delay": 5}


def finish(run):
    with lock:
        if run["status"] in ("completed", "failed", "cancelled") or time.time() < run["finish_at"]:
            return
        failed = run["mode"] == "tool_error"
        run["status"] = "failed" if failed else "completed"
        messages[run["session_id"]].append({"id": len(messages[run["session_id"]]) + 1, "role": "assistant", "content": "文件不存在，工具执行失败。" if failed else "你好，验收回复已经保存。"})


class Handler(BaseHTTPRequestHandler):
    def answer(self, status, data):
        raw = json.dumps(data, ensure_ascii=False).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(raw)))
        self.end_headers()
        self.wfile.write(raw)

    def do_GET(self):
        parsed = urlsplit(self.path)
        path, query = parsed.path, parse_qs(parsed.query)
        if path == "/api/auth/providers": return self.answer(200, {"providers": [{"name": "basic", "supports_password": True}]})
        if path == "/api/auth/me":
            if "hermes_session_at=preview-only-token" not in self.headers.get("Cookie", ""): return self.answer(401, {"detail": "Unauthorized"})
            return self.answer(200, {"user_id": "preview", "display_name": "本机预览", "provider": "basic", "expires_at": int(time.time()) + 3600})
        if path == "/v1/capabilities": return self.answer(200, {"features": {"run_submission": True, "runs_idempotency": {"supported": True, "durable": True, "retention_seconds": 86400}}})
        if path == "/api/sessions":
            offset, limit = int(query.get("offset", [0])[0]), int(query.get("limit", [200])[0])
            return self.answer(200, {"data": sessions[offset:offset+limit], "offset": offset, "limit": limit, "has_more": offset + limit < len(sessions)})
        if path.startswith("/api/sessions/") and path.endswith("/messages"):
            sid, offset, limit = path.split("/")[3], int(query.get("offset", [0])[0]), int(query.get("limit", [100])[0])
            rows = messages.get(sid, [])
            end = max(0, len(rows) - offset)
            return self.answer(200, {"data": rows[max(0, end-limit):end], "pagination": {"returned": min(limit, end)}})
        if path.startswith("/v1/runs/"):
            run_id = path.split("/")[3]
            run = runs.get(run_id)
            if not run: return self.answer(404, {"error": "run_not_found"})
            finish(run)
            if path.endswith("/events"):
                self.send_response(200); self.send_header("Content-Type", "text/event-stream"); self.end_headers()
                events = [{"event": "message.delta", "delta": "正在核对验收内容。", "seq": 0}, {"event": "tool.started", "tool": "read_file", "tool_call_id": "mock_tool", "preview": "读取验收文件", "seq": 1}]
                last = int(self.headers.get("Last-Event-ID", "-1"))
                try:
                    for event in events:
                        if event["seq"] > last:
                            self.wfile.write(("data: " + json.dumps(event, ensure_ascii=False) + "\n\n").encode()); self.wfile.flush()
                    while run["status"] not in ("completed", "failed", "cancelled"):
                        finish(run); time.sleep(0.1)
                    failed = run["status"] == "failed"
                    events = [{"event": "tool.completed", "tool": "read_file", "tool_call_id": "mock_tool", "preview": "文件不存在" if failed else "验收文件读取成功", "error": failed, "duration": 1.25, "seq": 2}, {"event": "run." + run["status"], "seq": 3}]
                    for event in events:
                        if event["seq"] > last:
                            self.wfile.write(("data: " + json.dumps(event, ensure_ascii=False) + "\n\n").encode()); self.wfile.flush()
                except (BrokenPipeError, ConnectionResetError): pass
                return
            return self.answer(200, {"run_id": run_id, "status": run["status"]})
        return self.answer(404, {"error": "not_found"})

    def do_POST(self):
        path = urlsplit(self.path).path
        data = json.loads(self.rfile.read(int(self.headers.get("Content-Length", 0))) or b"{}")
        if path == "/__test/control":
            with lock: control.update(data)
            return self.answer(200, {"ok": True})
        if path == "/auth/password-login":
            if data.get("provider") != "basic" or data.get("username") != "preview" or data.get("password") != "local-preview-password": return self.answer(401, {"detail": "Invalid credentials"})
            raw = b'{"ok":true}'
            self.send_response(200); self.send_header("Content-Type", "application/json"); self.send_header("Content-Length", str(len(raw))); self.send_header("Set-Cookie", "hermes_session_at=preview-only-token; Path=/; HttpOnly"); self.end_headers(); self.wfile.write(raw)
            return
        if path == "/api/sessions":
            sid = data["id"]
            with lock: sessions.insert(0, {"id": sid, "source": "api_server", "title": "新会话"}); messages[sid] = []
            return self.answer(201, {"session": {"id": sid}})
        if path == "/v1/runs":
            sid, key = data["session_id"], self.headers.get("Idempotency-Key")
            if not sid.startswith("haven_"): return self.answer(409, {"error": {"code": "permission_denied"}})
            with lock:
                if key in reservations:
                    run_id = reservations[key]
                    return self.answer(202, {"run_id": run_id, "status": runs[run_id]["status"]})
                if any(r["session_id"] == sid and r["status"] == "running" for r in runs.values()): return self.answer(409, {"error": {"message": "该会话已有运行"}})
                run_id = "run_" + uuid.uuid4().hex
                mode = control["mode"]
                runs[run_id] = {"session_id": sid, "status": "running", "mode": mode, "finish_at": time.time() + float(control["delay"])}
                reservations[key] = run_id
                content = data["input"] if isinstance(data["input"], str) else "图片验收消息"
                messages[sid].append({"id": len(messages[sid])+1, "role": "user", "content": content})
                control["mode"] = "normal"
            if mode == "unknown": return self.answer(503, {"error": "故障注入：已受理但响应丢失"})
            return self.answer(202, {"run_id": run_id, "status": "running"})
        if path.startswith("/v1/runs/") and path.endswith("/stop"):
            run_id = path.split("/")[3]
            with lock: runs[run_id]["status"] = "cancelled"
            return self.answer(200, {"run_id": run_id, "status": "stopping"})
        return self.answer(404, {"error": "not_found"})


if __name__ == "__main__":
    print("模拟 Hermes：http://127.0.0.1:18642", flush=True)
    ThreadingHTTPServer(("127.0.0.1", 18642), Handler).serve_forever()
