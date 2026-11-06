"""只供本机浏览器验收的审批和补充提问场景，不访问真实 Hermes。"""
import io
import json
import os
from http.server import ThreadingHTTPServer
import time
from urllib.parse import urlsplit

import mock_hermes as base


class Handler(base.Handler):
    def do_GET(self):
        parts = urlsplit(self.path).path.strip('/').split('/')
        if len(parts) == 3 and parts[:2] == ['v1', 'runs']:
            run = base.runs.get(parts[2])
            if run:
                base.finish(run)
                return self.answer(200, {'run_id': parts[2], 'status': run['status'], 'pending_controls': run.get('pending_controls', []) if run['status'] in ('running', 'waiting_for_approval') else []})
        return super().do_GET()

    def do_POST(self):
        path = urlsplit(self.path).path
        parts = path.strip('/').split('/')
        if len(parts) == 4 and parts[:2] == ['v1', 'runs'] and parts[3] in ('approval', 'clarify'):
            body = json.loads(self.rfile.read(int(self.headers['Content-Length'])))
            with base.lock:
                run = base.runs.get(parts[2])
                if not run:
                    return self.answer(404, {'error': 'not_found'})
                p = next((p for p in run.get('pending_controls', []) if p['prompt_id'] == body.get('request_id') and p['execution_generation'] == body.get('execution_generation') and p['kind'] == parts[3]), None)
                if not p:
                    return self.answer(409, {'error': {'message': 'approval_not_pending'}})
                run['pending_controls'].remove(p)
                run['status'] = 'running'
                run['finish_at'] = time.time() + 1
                run['control_answer'] = body
                return self.answer(200, {'run_id': parts[2], 'status': 'resolved'})
        if path == '/v1/runs':
            raw = self.rfile.read(int(self.headers['Content-Length']))
            self.rfile = io.BytesIO(raw)
            text = json.dumps(json.loads(raw).get('input', ''), ensure_ascii=False)
            trigger = 'HAVEN_ASK_' in text
            if trigger:
                base.control.update(mode='normal', delay=3600)
            super().do_POST()
            if trigger:
                run_id = base.reservations[self.headers.get('Idempotency-Key')]
                run = base.runs[run_id]
                kind = 'approval' if 'HAVEN_ASK_APPROVAL' in text else 'clarify'
                p = {'kind': kind, 'prompt_id': 'prompt:' + run_id, 'execution_generation': 3, 'choices': ['once', 'deny', 'session', 'always'] if kind == 'approval' else ['简明摘要', '详细报告'], 'timeout_s': 300}
                p.update({'command': 'printf "Hermes Haven approval test"', 'description': '工具希望执行以下操作，请决定是否允许。'} if kind == 'approval' else {'question': '你希望我按哪种形式整理附件内容？\n也可以直接补充你的要求。', 'multi_select': 'MULTI' in text})
                run['pending_controls'] = [p]
                run['status'] = 'waiting_for_approval' if kind == 'approval' else 'running'
            return
        return super().do_POST()


if __name__ == '__main__':
    port = int(os.environ.get('MOCK_PORT', '18644'))
    print(f'控制交互本机验收：http://127.0.0.1:{port}', flush=True)
    ThreadingHTTPServer(('127.0.0.1', port), Handler).serve_forever()
