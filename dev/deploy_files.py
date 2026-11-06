"""在空闲检查和备份后仅更新 Haven；不会恢复或写入 Hermes 数据库。"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import tarfile
import tempfile
import time
from urllib.request import Request, urlopen
from urllib.error import HTTPError, URLError


DATA = Path('/var/lib/hermes-haven')
BINARY = Path('/opt/hermes-haven/hermes-haven')
UNIT = Path('/etc/systemd/system/hermes-haven.service')
BASE_URL = 'http://10.1.1.52:18770'


def command(*args):
    return subprocess.run(args, check=True, capture_output=True, text=True).stdout.strip()


def sha(path):
    with Path(path).open('rb') as stream:
        return hashlib.file_digest(stream, 'sha256').hexdigest()


def manifest():
    return {str(path.relative_to(DATA)): sha(path) for path in DATA.rglob('*') if not path.is_symlink() and path.is_file()}


def api_key():
    for line in Path('/root/.hermes/.env').read_text().splitlines():
        if line.strip().startswith('API_SERVER_KEY='):
            return line.strip().split('=', 1)[1].strip().strip('\"\'')
    raise RuntimeError('未找到本机 Hermes API 密钥')


def idle():
    records = [json.loads(path.read_text()) for path in (DATA / 'requests').glob('*.json')]
    unknown = [record['request_id'] for record in records if record['state'] == 'unknown']
    latest = {}
    for record in sorted(records, key=lambda record: record['created_at']):
        if record['state'] == 'accepted':
            latest[record['session_id']] = record['run_id']
    for sid, own in json.loads((DATA / 'state.json').read_text()).get('sessions', {}).items():
        if sid not in latest and own.get('run_id'):
            latest[sid] = own['run_id']
    active = []
    key = api_key()
    for sid, run in latest.items():
        if not re.fullmatch('[A-Za-z0-9_-]{1,160}', run):
            raise RuntimeError('运行编号无效')
        try:
            request = Request('http://127.0.0.1:8642/v1/runs/' + run, headers={'Authorization': 'Bearer ' + key})
            with urlopen(request, timeout=12) as response:
                status = json.load(response).get('status')
        except HTTPError as error:
            if error.code == 404:
                continue
            raise RuntimeError('无法核实 Hermes 运行状态') from None
        if status not in ('completed', 'failed', 'cancelled', 'interrupted', 'rejected'):
            active.append({'session': sid, 'run': run, 'status': status})
    return {'idle': not unknown and not active, 'unknown_requests': unknown, 'active_runs': active, 'checked_runs': len(latest)}


def verify(source):
    result = {}
    with urlopen(BASE_URL + '/', timeout=8) as response:
        body = response.read()
        if response.status != 200:
            raise RuntimeError('首页不可用')
    expected = (source / 'internal/web/dist/index.html').read_bytes()
    if body != expected:
        raise RuntimeError('首页资源不匹配')
    paths = re.findall(r'(?:src|href)="(/assets/[^\"]+)"', body.decode())
    for path in paths:
        with urlopen(BASE_URL + path, timeout=8) as response:
            actual = hashlib.sha256(response.read()).hexdigest()
        wanted = sha(source / 'internal/web/dist' / path.lstrip('/'))
        if actual != wanted:
            raise RuntimeError('静态资源摘要不匹配')
        result[path] = actual
    for path in ('/api/sessions', '/api/uploads/test', '/api/uploads/test/download', '/api/uploads/test/preview', '/api/sessions/test/requests/test'):
        try:
            with urlopen(BASE_URL + path, timeout=8):
                raise RuntimeError('受保护路由未要求登录')
        except HTTPError as error:
            if error.code != 401:
                raise RuntimeError('受保护路由状态异常') from None
    for kind in ('approval', 'clarify'):
        try:
            request = Request(BASE_URL + '/api/sessions/test/' + kind, data=b'{}', headers={'Content-Type': 'application/json'})
            with urlopen(request, timeout=8):
                raise RuntimeError('控制接口未要求登录')
        except HTTPError as error:
            if error.code != 401:
                raise RuntimeError('控制接口登录保护异常') from None
    return result


parser = argparse.ArgumentParser()
parser.add_argument('--apply', action='store_true')
parser.add_argument('--release', default='phase2-files-01a1aece')
parser.add_argument('--candidate', default='/tmp/hermes-haven-phase2-01a1aece')
parser.add_argument('--source', default='/tmp/hermes-haven-phase2-src-01a1aece')
parser.add_argument('--expected-current', default='b3677906344d7e27beb2866d69597b3d7ed28be5bca889e4d60a7cc5e96eca7f')
parser.add_argument('--baseline', default='/tmp/hermes-haven-phase2-before-01a1aece.json')
parser.add_argument('--archive', default='/tmp/hermes-haven-phase2-src-01a1aece.tar.gz')
args = parser.parse_args()
ready = idle()
print(json.dumps(ready), flush=True)
if not ready['idle']:
    raise SystemExit(2)
if not args.apply:
    raise SystemExit(0)
if not re.fullmatch('[a-zA-Z0-9_-]+', args.release):
    raise RuntimeError('发布目录名称无效')
if sha(BINARY) != args.expected_current:
    raise RuntimeError('线上程序已发生变化，停止覆盖')
release = Path('/opt/hermes-haven/releases') / args.release
release.mkdir(mode=0o700)
os.chmod(release, 0o700)
before = manifest()
gateway = command('systemctl', 'show', 'hermes-gateway', '-p', 'MainPID', '--value')
unit_hash = sha(UNIT)
shutil.copy2(BINARY, release / 'hermes-haven.before')
shutil.copy2(UNIT, release / 'hermes-haven.service.before')
os.chmod(release / 'hermes-haven.service.before', 0o600)
with tarfile.open(release / 'haven-data-before.tar.gz', 'w:gz') as archive:
    archive.add(DATA, arcname='hermes-haven')
shutil.copy2(args.baseline, release / 'history-baseline.json')
shutil.copy2(args.candidate, release / 'hermes-haven.after')
shutil.copy2(args.archive, release / 'source.tar.gz')
(release / 'data-before.json').write_text(json.dumps(before, ensure_ascii=False))
if manifest() != before or not idle()['idle']:
    raise RuntimeError('备份期间有新的活动，停止更新')
if sha(BINARY) != args.expected_current:
    raise RuntimeError('备份期间线上程序已变化，停止覆盖')

# 所有校验和备份完成后才停止 Haven，后续回退仅替换程序。
descriptor, temporary = tempfile.mkstemp(prefix='.haven-release-', dir=str(BINARY.parent))
os.close(descriptor)
shutil.copyfile(args.candidate, temporary)
os.chmod(temporary, 0o755)
with open(temporary, 'rb') as stream:
    os.fsync(stream.fileno())
try:
    command('systemctl', 'stop', 'hermes-haven')
    os.replace(temporary, BINARY)
    command('systemctl', 'start', 'hermes-haven')
    for attempt in range(30):
        try:
            assets = verify(Path(args.source))
            break
        except (HTTPError, URLError, OSError):
            if attempt == 29:
                raise
            time.sleep(1)
    if command('systemctl', 'is-active', 'hermes-haven') != 'active':
        raise RuntimeError('服务启动失败')
except Exception:
    shutil.copyfile(release / 'hermes-haven.before', temporary)
    os.chmod(temporary, 0o755)
    os.replace(temporary, BINARY)
    command('systemctl', 'restart', 'hermes-haven')
    (release / 'rollback.json').write_text(json.dumps({'rolled_back_binary_only': True, 'time': time.time()}))
    raise
result = {'deployed': True, 'binary_sha256': sha(BINARY), 'assets': assets, 'haven_data_unchanged': manifest() == before,
          'service_config_unchanged': sha(UNIT) == unit_hash, 'gateway_pid_before': gateway,
          'gateway_pid_after': command('systemctl', 'show', 'hermes-gateway', '-p', 'MainPID', '--value'),
          'enabled': command('systemctl', 'is-enabled', 'hermes-haven'), 'release': str(release)}
(release / 'result.json').write_text(json.dumps(result, ensure_ascii=False, indent=2))
print(json.dumps(result), flush=True)
