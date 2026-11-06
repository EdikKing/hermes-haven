import test from 'node:test'
import assert from 'node:assert/strict'
import { createWorkspace, emptyDraft, samePayload, mergeMessages } from '../src/workspace.js'
import { emptyRun, applyRunEvent } from '../src/events.js'

function memoryStorage() { const data = new Map(); return { getItem: k => data.get(k) || null, setItem: (k, v) => data.set(k, v), removeItem: k => data.delete(k) } }

test('刷新后按账号和会话恢复草稿、附件及待确认请求', () => {
  const storage = memoryStorage(), first = createWorkspace(storage, 'edik')
  const image = { id: 'a'.repeat(40), name: '截图.png', mime: 'image/png', preview: 'blob:temporary' }
  first.saveDraft('one', { text: '会话一', files: [image], pending: { text: '会话一', images: [image.id], request_id: 'same-request', created_at: 123 } })
  first.saveDraft('two', { text: '会话二', files: [], pending: null })
  first.savePreferences({ selected: { id: 'one' }, section: 'mine' })
  const restored = createWorkspace(storage, 'edik')
  assert.equal(restored.draft('one').text, '会话一')
  assert.equal(restored.draft('two').text, '会话二')
  assert.equal(restored.draft('one').files[0].preview, '/api/uploads/' + image.id)
  assert.equal(restored.draft('one').pending.request_id, 'same-request')
  assert.equal(restored.preferences().selected.id, 'one')
  assert.deepEqual(createWorkspace(storage, 'different-user').draft('one'), emptyDraft())
})

test('两个标签页读取同一待确认编号，清除草稿只作用于匹配的消息', () => {
  const storage = memoryStorage(), one = createWorkspace(storage, 'edik'), two = createWorkspace(storage, 'edik')
  const pending = { request_id: 'fixed', text: '原消息', images: [] }
  one.saveDraft('chat', { text: '原消息', files: [], pending })
  assert.equal(two.draft('chat').pending.request_id, 'fixed')
  assert.equal(samePayload(pending, two.draft('chat')), true)
  assert.equal(samePayload(pending, { text: '后来输入的草稿', files: [] }), false)
})

test('事件序号和对应文字一起恢复，重复回放不重复追加', () => {
  const storage = memoryStorage(), workspace = createWorkspace(storage, 'edik'), view = emptyRun()
  view.runID = 'run-one'
  applyRunEvent(view, { event: 'message.delta', seq: 0, delta: '你好' })
  workspace.saveCheckpoint('chat', view)
  const restored = { ...emptyRun(), ...workspace.checkpoint('chat') }
  assert.equal(applyRunEvent(restored, { event: 'message.delta', seq: 0, delta: '你好' }).duplicate, true)
  applyRunEvent(restored, { event: 'message.delta', seq: 1, delta: '，世界' })
  assert.equal(restored.liveText, '你好，世界')
  assert.equal(restored.lastSeq, 1)
})

test('工具失败显示真实摘要和耗时，结构化错误可读', () => {
  const view = emptyRun()
  applyRunEvent(view, { event: 'tool.started', seq: 1, tool: 'read_file', tool_call_id: 'tool-one', preview: '读取文件' })
  applyRunEvent(view, { event: 'tool.completed', seq: 2, tool: 'read_file', tool_call_id: 'tool-one', error: true, duration: 1.25, preview: '文件不存在' })
  assert.equal(view.tools[0].state, 'error')
  assert.equal(view.tools[0].error, '文件不存在')
  assert.equal(view.tools[0].duration, 1.25)
  assert.match(view.notice, /文件不存在/)
  assert.equal(applyRunEvent(view, { event: 'run.failed', seq: 3, error: { message: '模型连接失败' } }).terminal, true)
  assert.equal(view.notice, '模型连接失败')
})

test('长历史分页交叠时去重并保持消息顺序', () => {
  assert.deepEqual(mergeMessages([{ id: 1 }, { id: 2 }], [{ id: 2 }, { id: 3 }]).map(x => x.id), [1, 2, 3])
})

test('存储已满和损坏数据有提示，不能伪报保存成功', () => {
  const errors = [], storage = { getItem: () => '{broken', setItem: () => { throw new Error('quota') } }
  const workspace = createWorkspace(storage, 'edik', e => errors.push(e))
  assert.deepEqual(workspace.draft('chat'), emptyDraft())
  assert.equal(workspace.saveDraft('chat', emptyDraft()), false)
  assert.equal(errors.length, 2)
})
