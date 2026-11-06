"""隔离启动已安装的 Hermes，使用确定性的本机模型验证真实工具等待和 Haven 控制链路。"""
import argparse
from concurrent.futures import ThreadPoolExecutor
from http.cookiejar import CookieJar
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import os
from pathlib import Path
import shlex
import socket
import subprocess
import sys
import threading
import time
from urllib.error import HTTPError
from urllib.request import HTTPCookieProcessor, Request, build_opener


def port():
    with socket.socket() as sock:
        sock.bind(('127.0.0.1', 0))
        return sock.getsockname()[1]


class Model(BaseHTTPRequestHandler):
    def log_message(self, *args):
        pass

    def reply(self, body, cookie=False):
        raw = json.dumps(body).encode()
        self.send_response(200)
        self.send_header('Content-Type', 'application/json')
        self.send_header('Content-Length', str(len(raw)))
        if cookie:
            self.send_header('Set-Cookie', 'hermes_session_at=isolated-controls; Path=/; HttpOnly')
        self.end_headers()
        self.wfile.write(raw)

    def do_GET(self):
        if self.path == '/api/auth/providers':
            return self.reply({'providers': [{'name': 'basic', 'supports_password': True}]})
        if self.path == '/api/auth/me':
            return self.reply({'user_id': 'acceptance', 'provider': 'basic', 'expires_at': int(time.time()) + 3600})
        self.send_error(404)

    def do_POST(self):
        body = json.loads(self.rfile.read(int(self.headers['Content-Length'])))
        if self.path == '/auth/password-login':
            assert body['username'] == 'acceptance' and body['password'] == 'isolated-local-fixture'
            return self.reply({'ok': True}, cookie=True)
        messages = body.get('messages', [])
        if messages:
            self.server.requests.append(body)
        last = messages[-1] if messages else {}
        text = json.dumps(last)
        message = {'role': 'assistant', 'content': 'CONTROL_DONE'}
        if last.get('role') != 'tool' and 'HAVEN_ASK_' in text:
            if 'HAVEN_ASK_APPROVAL' in text:
                name, arguments = 'terminal', {'command': 'rm -rf ' + shlex.quote(str(self.server.target))}
            else:
                name, arguments = 'clarify', {'questions': [{'question': '选择报告颜色', 'choices': ['BLUE', 'GREEN']}]}
                if 'HAVEN_ASK_MULTI' in text:
                    arguments['questions'][0]['multi_select'] = True
            message = {'role': 'assistant', 'content': None, 'tool_calls': [{'id': 'call_control', 'type': 'function', 'function': {'name': name, 'arguments': json.dumps(arguments)}}]}
        finish = 'tool_calls' if 'tool_calls' in message else 'stop'
        if body.get('stream'):
            if 'tool_calls' in message:
                message['tool_calls'][0]['index'] = 0
            raw = ('data: ' + json.dumps({'id': 'local-control', 'choices': [{'index': 0, 'delta': message, 'finish_reason': finish}]}) + '\n\ndata: [DONE]\n\n').encode()
            self.send_response(200); self.send_header('Content-Type', 'text/event-stream'); self.end_headers(); self.wfile.write(raw)
        else:
            self.reply({'id': 'local-control', 'choices': [{'index': 0, 'message': message, 'finish_reason': finish}]})


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--binary', required=True)
    parser.add_argument('--root', required=True)
    args = parser.parse_args()
    root = Path(args.root).resolve()
    root.mkdir(mode=0o700)  # 独占创建，绝不复用或清理其他验收数据。
    home, user, data = root / 'hermes', root / 'user', root / 'haven'
    home.mkdir(mode=0o700); user.mkdir(); data.mkdir(mode=0o700)
    installed = Path('/usr/local/lib/hermes-agent')
    sys.path.insert(0, str(installed))
    from tests.gateway.fixtures.local_recovery_probe import child_env, daemon
    peer = ThreadingHTTPServer(('127.0.0.1', 0), Model)
    peer.requests = []
    peer.target = home / 'only-new-fixture-target'
    peer.target.mkdir(); (peer.target / 'test.txt').write_text('isolated')
    threading.Thread(target=peer.serve_forever, daemon=True).start()
    native_port, haven_port = port(), port()
    native, base = f'http://127.0.0.1:{native_port}', f'http://127.0.0.1:{haven_port}'
    model_base = f'http://127.0.0.1:{peer.server_port}'
    config = {'model': {'provider': 'custom', 'default': 'local-wire-stub', 'base_url': model_base + '/v1'},
              'gateway': {'platforms': {'api_server': {'enabled': True, 'port': native_port, 'host': '127.0.0.1'}}, 'multiplex_profiles': False},
              'auxiliary': {'title_generation': {'enabled': False}}, 'terminal': {'cwd': str(home)},
              'platform_toolsets': {'api_server': ['terminal', 'clarify']}, 'approvals': {'mode': 'manual'}}
    (home / 'config.yaml').write_text(json.dumps(config))
    env = {**child_env(), 'HOME': str(user), 'USERPROFILE': str(user), 'HERMES_HOME': str(home), 'PYTHONPATH': str(installed),
           'OPENAI_API_KEY': 'loopback-only', 'OPENAI_BASE_URL': model_base + '/v1', 'API_SERVER_KEY': 'isolated-controls-only',
           'API_SERVER_ENABLED': 'true', 'API_SERVER_PORT': str(native_port), 'PYTHONUNBUFFERED': '1',
           'PYTHONDONTWRITEBYTECODE': '1', 'HERMES_DISABLE_LAZY_INSTALLS': 'true'}
    haven_env = {**os.environ, 'HAVEN_DATA_DIR': str(data), 'HAVEN_LISTEN': f'127.0.0.1:{haven_port}', 'HERMES_URL': native,
                 'HERMES_API_KEY': env['API_SERVER_KEY'], 'HERMES_DASHBOARD_URL': model_base, 'HERMES_AUTH_PROVIDER': 'basic',
                 'HERMES_HISTORY_DB': str(home / 'state.db'), 'HERMES_HISTORY_PYTHON': sys.executable, 'HERMES_SOURCE_DIR': str(installed)}
    client = build_opener(HTTPCookieProcessor(CookieJar()))
    proc, log = None, (root / 'haven.log').open('w')

    def api(path, body=None):
        request = Request(base + path, data=None if body is None else json.dumps(body).encode(), headers={'Content-Type': 'application/json', 'Origin': base})
        try:
            with client.open(request, timeout=20) as response:
                return response.status, json.load(response)
        except HTTPError as error:
            return error.code, json.load(error)

    def start():
        nonlocal proc
        proc = subprocess.Popen([str(Path(args.binary).resolve())], env=haven_env, stdout=log, stderr=log)
        end = time.monotonic() + 20
        while True:
            try:
                if api('/api/me')[0] == 401:
                    break
            except OSError:
                pass
            assert proc.poll() is None and time.monotonic() < end, 'Haven 启动失败'
            time.sleep(.1)
        assert api('/api/login', {'username': 'acceptance', 'password': 'isolated-local-fixture'})[0] == 200

    def wait(sid, predicate):
        end = time.monotonic() + 35
        while True:
            code, state = api('/api/sessions/' + sid + '/status')
            assert code == 200, state
            if predicate(state):
                return state
            assert time.monotonic() < end, state
            time.sleep(.1)

    report = []
    try:
        with daemon(installed, home, env, barrier=False):
            start()
            for label, text, choice, restart in [('拒绝', 'HAVEN_ASK_APPROVAL', 'deny', False), ('允许一次', 'HAVEN_ASK_APPROVAL', 'once', True),
                                                  ('补充回答', 'HAVEN_ASK_CLARIFY', 'BLUE 中文回答', True), ('多选回答', 'HAVEN_ASK_MULTI', 'BLUE\nGREEN', False)]:
                code, session = api('/api/sessions', {})
                assert code == 201, session
                sid = session['id']
                code, accepted = api('/api/sessions/' + sid + '/runs', {'text': text, 'request_id': 'controls_' + str(len(report)), 'created_at': int(time.time() * 1000)})
                assert code == 202, accepted
                state = wait(sid, lambda s: bool(s.get('pending_controls')))
                p = state['pending_controls'][0]
                if restart:
                    proc.terminate(); proc.wait(timeout=10); start()
                    assert wait(sid, lambda s: bool(s.get('pending_controls')))['pending_controls'][0] == p
                body = {'run_id': accepted['run_id'], 'request_id': p['prompt_id'], 'execution_generation': p['execution_generation'], 'choice' if p['kind'] == 'approval' else 'answer': choice}
                path = '/api/sessions/' + sid + '/' + p['kind']
                assert api(path, {**body, 'execution_generation': p['execution_generation'] + 1})[0] == 409
                assert api(path, {**body, 'request_id': 'other-prompt'})[0] == 409
                assert api('/api/sessions/foreign/' + p['kind'], body)[0] == 403
                with ThreadPoolExecutor(max_workers=4) as pool:
                    responses = list(pool.map(lambda _: api(path, body), range(4)))
                assert any(code == 200 for code, _ in responses), responses
                assert all(code in (200, 409) for code, _ in responses), responses
                final = wait(sid, lambda s: s['status'] in ('completed', 'failed'))
                assert final['status'] == 'completed', final
                assert api(path, body)[0] == 409
                if label == '拒绝':
                    assert peer.target.exists(), '拒绝审批不能执行命令'
                elif label == '允许一次':
                    assert not peer.target.exists(), '仅本次新建夹具目录应被工具处理'
                code, history = api('/api/sessions/' + sid + '/messages')
                assert code == 200 and any('CONTROL_DONE' in str(m.get('content')) for m in history['data']), history
                report.append({'case': label, 'session_id': sid, 'run_id': accepted['run_id'], 'kind': p['kind'], 'restart': restart, 'duplicate_statuses': [c for c, _ in responses], 'status': final['status']})
                print(json.dumps(report[-1], ensure_ascii=False), flush=True)
            tools = [m for req in peer.requests for m in req.get('messages', []) if m.get('role') == 'tool']
            assert any('BLUE 中文回答' in str(m['content']) for m in tools), tools
            assert any('BLUE' in str(m['content']) and 'GREEN' in str(m['content']) for m in tools), tools
            result = {'passed': True, 'installed_hermes_runtime': True, 'deterministic_loopback_model': True, 'model_requests': len(peer.requests), 'cases': report}
            (root / 'result.json').write_text(json.dumps(result, ensure_ascii=False, indent=2))
            print(json.dumps(result, ensure_ascii=False), flush=True)
    finally:
        if proc is not None and proc.poll() is None:
            proc.terminate(); proc.wait(timeout=10)
        log.close(); peer.shutdown(); peer.server_close()


if __name__ == '__main__':
    main()
