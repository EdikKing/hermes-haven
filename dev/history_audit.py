"""通过只读 SQLite 连接核对历史；输出仅含编号、摘要和计数。"""
import argparse
import hashlib
import json
import os
import pathlib
import sqlite3


def snapshot():
    result = {}
    for name in ('/root/.hermes/state.db', '/root/.hermes/profiles/feishu-ws/state.db'):
        connection = sqlite3.connect('file:' + name + '?mode=ro', uri=True)
        connection.execute('PRAGMA query_only=ON')
        connection.execute('BEGIN')
        tables = {}
        for table, query in (
            ('messages', 'SELECT * FROM messages ORDER BY id'),
            ('sessions', 'SELECT id,source,title,message_count,tool_call_count,parent_session_id,archived,pinned FROM sessions ORDER BY id'),
        ):
            rows = {}
            for row in connection.execute(query):
                raw = json.dumps(list(row), ensure_ascii=False, separators=(',', ':'), default=lambda value: value.hex()).encode()
                rows[str(row[0])] = hashlib.sha256(raw).hexdigest()
            tables[table] = rows
        assert connection.total_changes == 0
        connection.close()
        result[name] = tables
    return result


parser = argparse.ArgumentParser()
parser.add_argument('operation', choices=('snapshot', 'compare'))
parser.add_argument('baseline')
args = parser.parse_args()
current = snapshot()
if args.operation == 'snapshot':
    # 独占创建基线，不覆盖已有审计证据。
    descriptor = os.open(args.baseline, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(descriptor, 'w', encoding='utf-8') as output:
        json.dump(current, output, ensure_ascii=False, separators=(',', ':'))
    print(json.dumps({name: {table: len(rows) for table, rows in tables.items()} for name, tables in current.items()}))
else:
    baseline = json.loads(pathlib.Path(args.baseline).read_text())
    report, changed = {}, False
    for name, tables in baseline.items():
        report[name] = {}
        for table, rows in tables.items():
            now = current[name][table]
            missing = [key for key in rows if key not in now]
            differences = [key for key in rows if key in now and rows[key] != now[key]]
            report[name][table] = {'baseline': len(rows), 'missing': missing, 'changed': differences, 'new': len(set(now) - set(rows))}
            changed = changed or bool(missing or differences)
    print(json.dumps({'passed': not changed, 'databases': report}))
    raise SystemExit(1 if changed else 0)
