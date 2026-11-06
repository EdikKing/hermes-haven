"""只读统计有效上下文，只向 Haven 返回计数和模型标识。"""
import contextlib
import json
import pathlib
import sys


def positive(value):
    return int(value) if isinstance(value, (int, float)) and not isinstance(value, bool) and 0 < value < 10**12 else None


def model_target(config, session, settings):
    configured = config.get('model') or {}
    configured = configured if isinstance(configured, dict) else {'default': configured}
    stored = session.get('model_config') or {}
    if isinstance(stored, str):
        try:
            stored = json.loads(stored)
        except (ValueError, TypeError):
            stored = {}
    stored = stored if isinstance(stored, dict) else {}
    lock = stored.get('browser_model_lock') or {}
    runtime = stored.get('gateway_runtime') or {}
    model = settings.get('model') or (lock.get('model') if lock.get('confirmed') else '') or configured.get('default') or configured.get('name') or session.get('model') or ''
    provider = settings.get('provider') or (lock.get('provider') if not settings.get('model') and lock.get('confirmed') else '') or configured.get('provider') or ''
    base_url = configured.get('base_url', '') if provider == configured.get('provider') else ''
    if model == session.get('model') and provider == runtime.get('provider'):
        base_url = runtime.get('base_url') or base_url
    return str(model), str(provider), str(base_url), stored


def capacity(config, model, provider, base_url):
    from agent.models_dev import get_model_info
    from agent.model_metadata import get_cached_context_length, _config_override_context_length, _is_codex_route, _skip_persistent_context_cache
    from hermes_cli.config import get_compatible_custom_providers
    custom = get_compatible_custom_providers(config)
    for row in custom:
        if row.get('name') == provider.removeprefix('custom:'):
            base_url = row.get('base_url') or base_url
            break
    explicit = _config_override_context_length(model, base_url, provider, custom)
    if positive(explicit):
        return positive(explicit), 'config'
    # 缓存按模型和端点匹配，目录查询明确禁止联网与更新缓存。
    cached = get_cached_context_length(model, base_url) if base_url and not _is_codex_route(provider, base_url, custom) and not _skip_persistent_context_cache(base_url, provider) else None
    if positive(cached):
        return positive(cached), 'cache'
    info = get_model_info(provider, model, allow_network=False, config=config)
    return positive(getattr(info, 'context_window', None)), 'catalog'


def calculate(db, session_id, settings, config):
    resolved = db.resolve_resume_session_id(session_id)
    session = db.get_session(resolved)
    if not session:
        return {'code': 404, 'body': {'error': '会话不存在'}}
    model, provider, base_url, stored = model_target(config, session, settings)
    maximum, capacity_source = None, ''
    with contextlib.suppress(Exception):
        maximum, capacity_source = capacity(config, model, provider, base_url)
    from agent.model_metadata import estimate_messages_tokens_rough
    # 不读取已压缩的展示历史；仅统计 Hermes 恢复时使用的有效消息。
    rows = db.get_messages_as_conversation(resolved, include_ancestors=True)
    messages = [row for row in rows if row.get('role') in ('system', 'user', 'assistant', 'tool', 'function')]
    used, source, estimated = None, 'unavailable', False
    if messages:
        with contextlib.suppress(Exception):
            from agent.usage_anchor import persisted_anchor_tokens
            used = persisted_anchor_tokens(db, resolved, messages)
        if used is not None:
            anchor = stored.get('_usage_anchor') or {}
            extra = messages[int(anchor.get('base_count') or 0):]
            if extra and extra[0].get('role') == 'assistant':
                extra = extra[1:]
            source, estimated = 'usage_anchor', bool(extra)
        else:
            counted = list(messages)
            prompt = session.get('system_prompt')
            if prompt and not any(row.get('role') == 'system' for row in counted):
                counted.insert(0, {'role': 'system', 'content': prompt})
            used = max(0, int(estimate_messages_tokens_rough(counted)))
            source, estimated = 'messages', True
    if model and session.get('model') and model != session.get('model'):
        estimated = True
    used = int(used) if isinstance(used, (int, float)) and not isinstance(used, bool) and 0 <= used < 10**12 else None
    remaining = max(0, maximum - used) if maximum is not None and used is not None else None
    return {'code': 200, 'body': {
        'session_id': resolved, 'model': model, 'provider': provider,
        'used_tokens': used, 'max_tokens': maximum, 'remaining_tokens': remaining,
        'source': source, 'capacity_source': capacity_source, 'estimated': estimated,
    }}


def main():
    source, database, session_id, raw_settings = sys.argv[1:]
    sys.dont_write_bytecode = True
    sys.path.insert(0, source)
    with contextlib.redirect_stdout(sys.stderr):
        from hermes_state import SessionDB
        from hermes_cli.config import load_config
        db = SessionDB(pathlib.Path(database), read_only=True)
        try:
            db._conn.execute('PRAGMA query_only=ON')
            db._conn.execute('BEGIN')
            reply = calculate(db, session_id, json.loads(raw_settings), load_config())
        finally:
            db.close()
    print(json.dumps(reply, ensure_ascii=False))


if __name__ == '__main__':
    main()
