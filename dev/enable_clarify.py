"""仅为默认 API 入口添加 clarify；验证其他配置和有效工具均保持不变。"""
import argparse
import copy
import hashlib
import json
import os
from pathlib import Path
import tempfile

import yaml
from hermes_cli.tools_config import _get_platform_tools


parser = argparse.ArgumentParser()
parser.add_argument('--apply', action='store_true')
parser.add_argument('--backup', required=True)
args = parser.parse_args()
path = Path('/root/.hermes/config.yaml')
if path.is_symlink():
    raise RuntimeError('配置是符号链接，停止自动更新')
original = path.read_bytes()
text = original.decode('utf-8')
config = yaml.safe_load(text)
before = _get_platform_tools(config, 'api_server')
if 'clarify' in before:
    print(json.dumps({'already_enabled': True}))
    raise SystemExit(0)
candidate = copy.deepcopy(config)
candidate.setdefault('platform_toolsets', {})['api_server'] = sorted(before | {'clarify'})
after = _get_platform_tools(candidate, 'api_server')
assert after - before == {'clarify'} and not before - after, '有效工具集出现非预期差异'
node = yaml.compose(text)
platform_node = next((v for k, v in node.value if k.value == 'platform_toolsets'), None)
encoded = json.dumps(candidate['platform_toolsets']['api_server'], ensure_ascii=False)
if platform_node is None:
    changed = text.rstrip() + '\n\nplatform_toolsets:\n  api_server: ' + encoded + '\n'
else:
    if not isinstance(platform_node, yaml.MappingNode):
        raise RuntimeError('platform_toolsets 结构不是映射，停止自动更新')
    existing = next((v for k, v in platform_node.value if k.value == 'api_server'), None)
    if existing is not None:
        changed = text[:existing.start_mark.index] + encoded + text[existing.end_mark.index:]
    else:
        index = platform_node.end_mark.index
        indent = platform_node.value[0][0].start_mark.column if platform_node.value else 2
        changed = text[:index] + ' ' * indent + 'api_server: ' + encoded + '\n' + text[index:]
assert yaml.safe_load(changed) == candidate, '配置文本与预期差异不一致'
print(json.dumps({'only_added_tool': 'clarify', 'previous_tools': len(before), 'next_tools': len(after), 'config_before_sha256': hashlib.sha256(original).hexdigest(), 'apply': args.apply}), flush=True)
if not args.apply:
    raise SystemExit(0)
backup = Path(args.backup)
backup.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
descriptor = os.open(backup, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
with os.fdopen(descriptor, 'wb') as output:
    output.write(original); output.flush(); os.fsync(output.fileno())
if path.read_bytes() != original:
    raise RuntimeError('配置已被其他任务修改，停止覆盖')
descriptor, temporary = tempfile.mkstemp(prefix='.haven-clarify-', dir=path.parent)
try:
    with os.fdopen(descriptor, 'wb') as output:
        output.write(changed.encode('utf-8')); output.flush(); os.fsync(output.fileno())
    os.chmod(temporary, path.stat().st_mode & 0o777)
    os.replace(temporary, path)
finally:
    if os.path.exists(temporary):
        os.unlink(temporary)
assert yaml.safe_load(path.read_text()) == candidate
print(json.dumps({'enabled': True, 'backup': str(backup), 'config_after_sha256': hashlib.sha256(path.read_bytes()).hexdigest()}))
