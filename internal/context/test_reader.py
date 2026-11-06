"""验证上下文统计的数据边界与只读调用，不连接实际 Hermes。"""
import sys
import types
import unittest
from unittest.mock import patch
import reader


class Database:
    def __init__(self, messages, model='alpha'):
        self.messages = messages
        self.session = {'model': model, 'system_prompt': '提示', 'input_tokens': 900000000, 'output_tokens': 900000000, 'model_config': {}}

    def resolve_resume_session_id(self, sid):
        return sid

    def get_session(self, sid):
        return self.session if sid == 'one' else None

    def get_messages_as_conversation(self, sid, **kwargs):
        assert kwargs == {'include_ancestors': True}
        return self.messages


class ContextTest(unittest.TestCase):
    def setUp(self):
        self.anchor = None
        self.window = 1050000
        metadata = types.ModuleType('agent.model_metadata')
        metadata.estimate_messages_tokens_rough = lambda rows: sum(row.get('count', 10) for row in rows)
        metadata.get_cached_context_length = lambda *args: 272000
        metadata._config_override_context_length = lambda *args: None
        metadata._is_codex_route = lambda provider, *args: provider == 'openai-codex'
        metadata._skip_persistent_context_cache = lambda *args: False
        catalog = types.ModuleType('agent.models_dev')
        catalog.get_model_info = lambda *args, **kwargs: types.SimpleNamespace(context_window=self.window)
        usage = types.ModuleType('agent.usage_anchor')
        usage.persisted_anchor_tokens = lambda *args: self.anchor
        config = types.ModuleType('hermes_cli.config')
        config.get_compatible_custom_providers = lambda *args: []
        modules = {'agent': types.ModuleType('agent'), 'hermes_cli': types.ModuleType('hermes_cli'), 'agent.model_metadata': metadata, 'agent.models_dev': catalog, 'agent.usage_anchor': usage, 'hermes_cli.config': config}
        self.patcher = patch.dict(sys.modules, modules)
        self.patcher.start(); self.addCleanup(self.patcher.stop)
        self.config = {'model': {'default': 'alpha', 'provider': 'openai-codex'}}

    def test_anchor_ignores_cumulative_session_usage_and_stale_codex_cache(self):
        self.anchor = 34800
        db = Database([{'role': 'user'}, {'role': 'assistant'}])
        db.session['model_config'] = {'_usage_anchor': {'base_count': 1}}
        data = reader.calculate(db, 'one', {}, self.config)['body']
        self.assertEqual(data['used_tokens'], 34800)
        self.assertEqual(data['max_tokens'], 1050000)
        self.assertEqual(data['remaining_tokens'], 1015200)
        self.assertEqual(data['source'], 'usage_anchor')

    def test_stale_anchor_counts_active_messages_and_system_prompt_only(self):
        db = Database([{'role': 'session_meta', 'count': 999999}, {'role': 'user', 'count': 1200}, {'role': 'assistant', 'count': 300}])
        data = reader.calculate(db, 'one', {}, self.config)['body']
        self.assertEqual(data['used_tokens'], 1510)
        self.assertEqual(data['source'], 'messages')
        self.assertTrue(data['estimated'])
        self.assertNotIn('system_prompt', data)

    def test_new_empty_session_and_unknown_capacity_keep_unknown_values(self):
        self.window = None
        data = reader.calculate(Database([]), 'one', {}, self.config)['body']
        self.assertIsNone(data['used_tokens'])
        self.assertIsNone(data['max_tokens'])
        self.assertIsNone(data['remaining_tokens'])
        self.assertEqual(reader.calculate(Database([]), 'missing', {}, self.config)['code'], 404)

    def test_selected_model_and_exceeded_capacity(self):
        self.window = 1000; self.anchor = 34800
        data = reader.calculate(Database([{'role': 'user'}]), 'one', {'model': 'beta', 'provider': 'openai-codex'}, self.config)['body']
        self.assertEqual(data['model'], 'beta')
        self.assertEqual(data['remaining_tokens'], 0)
        self.assertTrue(data['estimated'])


if __name__ == '__main__':
    unittest.main()
