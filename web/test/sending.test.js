import test from 'node:test'
import assert from 'node:assert/strict'
import { createRenderer } from 'vue'
import { useHaven } from '../src/useHaven.js'
import { groupMessages, imageBlocks, messageText } from '../src/messages.js'
import { createOutgoingMessage, reconcileOutgoing, withOutgoingMessage } from '../src/outgoing.js'

function memoryStorage() {
  const data = new Map()
  return { getItem: key => data.get(key) || null, setItem: (key, value) => data.set(key, value), removeItem: key => data.delete(key) }
}

async function until(predicate) {
  const deadline = Date.now() + 2000
  while (!predicate()) {
    assert.ok(Date.now() < deadline, '异步状态没有按预期完成')
    await new Promise(resolve => setTimeout(resolve, 5))
  }
}

function backend(options = {}) {
  let release
  const gate = new Promise(resolve => { release = resolve })
  const server = {
    streams: [], submissions: 0, run: false, status: 'running', request: null,
    history: {
      one: [{ id: 1, role: 'user', content: '再解释一下' }, { id: 2, role: 'assistant', content: '上一轮的回复' }],
      two: [{ id: 10, role: 'user', content: '另一个会话' }],
    },
    release,
    async fetch(input, init = {}) {
      const url = new URL(input, 'http://haven.test'), path = url.pathname
      const reply = (value, status = 200) => new Response(JSON.stringify(value), { status, headers: { 'Content-Type': 'application/json' } })
      if (path === '/api/me') return reply({ username: 'preview' })
      if (path === '/api/health') return reply({})
      if (path === '/api/sessions') return reply({ data: ['one', 'two'].map(id => ({ id, owned: true, title: id })), has_more: false })
      const history = path.match(/^\/api\/sessions\/(one|two)\/messages$/)
      if (history) return reply({ data: server.history[history[1]] })
      if (path === '/api/sessions/one/runs') {
        server.submissions++; server.request = JSON.parse(init.body)
        if (options.delayAcceptance) await gate
        if (options.rejected) return reply({ error: '该会话拒绝继续运行' }, 409)
        server.run = true
        if (options.responseLost) throw new TypeError('故障注入：已受理但响应丢失')
        return reply({ run_id: 'run-one', status: 'running' }, 202)
      }
      if (/^\/api\/sessions\/one\/requests\//.test(path)) return reply(server.run ? { state: 'accepted', run_id: 'run-one' } : { state: 'unknown' })
      if (path.endsWith('/status')) return reply(path.includes('/one/') && server.run ? { run_id: 'run-one', status: server.status } : { status: 'idle' })
      throw new Error('测试未处理的接口：' + path)
    },
    complete() {
      server.status = 'completed'
      server.history.one.push({ id: 3, role: 'user', content: server.request.text || [{ type: 'image', src: '/api/images/history-image', name: '截图.png' }] }, { id: 4, role: 'assistant', content: '本轮完成' })
      const stream = server.streams.findLast(item => !item.closed)
      assert.ok(stream)
      stream.onmessage({ data: JSON.stringify({ event: 'run.completed', seq: 1 }) })
    },
  }
  return server
}

async function mount(server, storage = memoryStorage()) {
  const names = ['window', 'fetch', 'localStorage', 'EventSource']
  const saved = names.map(name => Object.getOwnPropertyDescriptor(globalThis, name))
  const globals = {
    window: new EventTarget(), localStorage: storage, fetch: server.fetch,
    EventSource: class {
      constructor() { server.streams.push(this) }
      close() { this.closed = true }
    },
  }
  for (const name of names) Object.defineProperty(globalThis, name, { configurable: true, writable: true, value: globals[name] })
  let haven
  const renderer = createRenderer({
    createComment: () => ({}), insert() {}, remove() {}, parentNode: () => null, nextSibling: () => null,
    createElement: () => ({}), createText: () => ({}), patchProp() {}, setText() {}, setElementText() {},
  })
  const app = renderer.createApp({ setup() { haven = useHaven(); return () => null } })
  app.mount({})
  const dispose = () => {
    app.unmount()
    names.forEach((name, index) => { if (saved[index]) Object.defineProperty(globalThis, name, saved[index]); else delete globalThis[name] })
  }
  try {
    await until(() => haven.sessions.value.length === 2)
    if (!haven.selected.value) await haven.choose(haven.sessions.value[0])
    await until(() => !haven.loading.value)
    return { haven, storage, dispose }
  } catch (error) { dispose(); throw error }
}

test('发送后立即显示；历史延迟期间保持可见，相同文字的旧消息不顶替新消息，回填后无重复', async () => {
  const server = backend({ delayAcceptance: true }), client = await mount(server), h = client.haven
  try {
    h.text.value = '再解释一下'
    const submitted = h.send()
    assert.equal(messageText(h.messages.value.at(-1)), '再解释一下')
    assert.match(h.messages.value.at(-1).id, /^outgoing-/)
    assert.equal(server.submissions, 1)
    server.release(); await submitted
    assert.equal(h.isBusy.value, true)
    assert.equal(h.text.value, '')
    assert.equal(h.pending.value, null)
    assert.deepEqual(groupMessages(h.messages.value, { busy: true }).map(turn => turn.role), ['user', 'assistant', 'user', 'assistant'])
    assert.match(h.messages.value.at(-1).id, /^outgoing-/)
    await h.refreshStatus()
    assert.match(h.messages.value.at(-1).id, /^outgoing-/)
    server.complete()
    await until(() => h.messages.value.at(-1)?.id === 4)
    assert.deepEqual(h.messages.value.filter(message => message.role === 'user').map(message => message.id), [1, 3])
    assert.equal(JSON.parse(client.storage.getItem('hermes-haven:v2:preview:outgoing:one')), null)
  } finally { client.dispose() }
})

test('思考期间刷新或切换会话仍显示已发消息，恢复过程不会重新提交请求', async () => {
  const server = backend(), storage = memoryStorage()
  let client = await mount(server, storage)
  try {
    client.haven.text.value = '刷新后也应保留'
    await client.haven.send()
    client.dispose(); client = await mount(server, storage)
    assert.equal(client.haven.isBusy.value, true)
    assert.equal(messageText(client.haven.messages.value.at(-1)), '刷新后也应保留')
    await client.haven.choose(client.haven.sessions.value[1])
    assert.deepEqual(client.haven.messages.value.map(messageText), ['另一个会话'])
    await client.haven.choose(client.haven.sessions.value[0])
    assert.equal(messageText(client.haven.messages.value.at(-1)), '刷新后也应保留')
    assert.equal(server.submissions, 1)
    server.complete()
    await until(() => client.haven.messages.value.at(-1)?.id === 4)
    assert.equal(client.haven.messages.value.filter(message => messageText(message) === '刷新后也应保留').length, 1)
  } finally { client.dispose() }
})

test('明确拒绝时恢复原草稿，移除本地发送气泡并显示失败原因', async () => {
  const server = backend({ rejected: true }), client = await mount(server), h = client.haven
  try {
    h.text.value = '没有被受理的消息'; await h.send()
    assert.equal(h.text.value, '没有被受理的消息')
    assert.equal(h.pending.value, null)
    assert.match(h.notice.value, /拒绝继续运行/)
    assert.deepEqual(h.messages.value.map(message => message.id), [1, 2])
  } finally { client.dispose() }
})

test('受理响应丢失时保留气泡与待核对状态，刷新只核对原请求而不重发', async () => {
  const server = backend({ responseLost: true }), storage = memoryStorage()
  let client = await mount(server, storage)
  try {
    client.haven.text.value = '响应丢失也要保留'; await client.haven.send()
    assert.equal(client.haven.status.value, 'submission_unknown')
    assert.ok(client.haven.pending.value)
    assert.equal(messageText(client.haven.messages.value.at(-1)), '响应丢失也要保留')
    client.dispose(); client = await mount(server, storage)
    assert.equal(client.haven.isBusy.value, true)
    assert.equal(client.haven.pending.value, null)
    assert.equal(messageText(client.haven.messages.value.at(-1)), '响应丢失也要保留')
    assert.equal(server.submissions, 1)
  } finally { client.dispose() }
})

test('只有图片的消息在运行期间也立即显示，正式记录回填后替换本地预览', async () => {
  const server = backend(), client = await mount(server), h = client.haven
  try {
    const id = 'a'.repeat(40)
    h.files.value.push({ id, name: '截图.png', mime: 'image/png', preview: '/api/uploads/' + id })
    await h.send()
    assert.deepEqual(imageBlocks(h.messages.value.at(-1)), [{ type: 'image', src: '/api/uploads/' + id, name: '截图.png' }])
    server.complete()
    await until(() => h.messages.value.at(-1)?.id === 4)
    assert.equal(h.messages.value.filter(message => imageBlocks(message).length).length, 1)
  } finally { client.dispose() }
})

test('大量工具记录挤出发送前的历史边界时，用户消息仍排在本轮助手记录前，加载旧页后可正常去重', () => {
  const before = [{ id: 1, role: 'assistant', content: '前一轮' }]
  const outgoing = createOutgoingMessage({ request_id: 'long-run', text: '长任务', created_at: Date.now() }, [], before)
  const recent = Array.from({ length: 100 }, (_, index) => ({ id: index + 3, role: 'assistant', content: '执行步骤' }))
  assert.equal(withOutgoingMessage(recent, outgoing)[0], outgoing.message)
  const full = [...before, { id: 2, role: 'user', content: '长任务' }, ...recent]
  assert.equal(reconcileOutgoing(full, outgoing), null)
  assert.equal(withOutgoingMessage(full, outgoing), full)
})
