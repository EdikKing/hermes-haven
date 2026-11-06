import test from 'node:test'
import assert from 'node:assert/strict'
import { createRenderer, nextTick } from 'vue'
import { useHaven } from '../src/useHaven.js'

const flush = () => new Promise(resolve => setImmediate(resolve))
const deferred = () => { let resolve, reject; const promise = new Promise((yes, no) => { resolve = yes; reject = no }); return { promise, resolve, reject } }

async function until(predicate) {
  const deadline = Date.now() + 2000
  while (!predicate()) { assert.ok(Date.now() < deadline, '历史加载未按预期完成'); await flush() }
}

async function mount({ count = 310, height = 40 } = {}) {
  const history = Object.fromEntries(['one', 'two'].map((id, index) => [id, Array.from({ length: count }, (_, i) => ({ id: index * 10000 + i + 1, role: i % 2 ? 'assistant' : 'user', content: `消息 ${i + 1}` }))]))
  const server = { requests: [], intercept: null, active: 0, maxActive: 0 }
  const reply = (data, status = 200) => new Response(JSON.stringify(data), { status, headers: { 'Content-Type': 'application/json' } })
  const globals = {
    window: new EventTarget(), localStorage: { getItem: () => null, setItem() {}, removeItem() {} },
    async fetch(input) {
      const url = new URL(input, 'http://haven.test'), path = url.pathname
      if (path === '/api/me') return reply({ username: 'preview' })
      if (path === '/api/health' || path === '/api/logout') return reply({})
      if (path === '/api/models') return reply({ providers: [] })
      if (path === '/api/sessions') return reply({ data: ['one', 'two'].map(id => ({ id, title: id, owned: true })) })
      if (path.endsWith('/settings') || path.endsWith('/context')) return reply({})
      if (path.endsWith('/status')) return reply({ status: 'idle' })
      const match = path.match(/^\/api\/sessions\/([^/]+)\/messages$/)
      assert.ok(match, '测试未处理的接口：' + path)
      const id = match[1], offset = Number(url.searchParams.get('offset')), end = Math.max(0, history[id].length - offset)
      const request = { id, offset }
      server.requests.push(request); server.active++; server.maxActive = Math.max(server.maxActive, server.active)
      try {
        const result = await server.intercept?.(request)
        return result || reply({ data: history[id].slice(Math.max(0, end - 100), end) })
      } finally { server.active-- }
    },
  }
  const names = Object.keys(globals), saved = names.map(name => Object.getOwnPropertyDescriptor(globalThis, name))
  names.forEach(name => Object.defineProperty(globalThis, name, { configurable: true, writable: true, value: globals[name] }))
  let haven, top = 0
  const renderer = createRenderer({
    createComment: () => ({}), insert() {}, remove() {}, parentNode: () => null, nextSibling: () => null,
    createElement: () => ({}), createText: () => ({}), patchProp() {}, setText() {}, setElementText() {},
  })
  const app = renderer.createApp({ setup() { haven = useHaven(); return () => null } })
  app.mount({})
  const element = {
    clientHeight: 800,
    get scrollHeight() { return 200 + haven.messages.value.length * height },
    get scrollTop() { return top },
    set scrollTop(value) { top = Math.max(0, Math.min(value, this.scrollHeight - this.clientHeight)) },
  }
  haven.scroller.value = element
  const dispose = () => {
    app.unmount()
    names.forEach((name, i) => { if (saved[i]) Object.defineProperty(globalThis, name, saved[i]); else delete globalThis[name] })
  }
  try {
    await until(() => haven.sessions.value.length === 2)
    await haven.choose(haven.sessions.value[0]); await nextTick()
    return { haven, server, element, dispose, reply }
  } catch (error) { dispose(); throw error }
}

test('接近顶部时提前加载，同一页只请求一次，并保留请求完成时的阅读位置', async () => {
  const client = await mount(), { haven: h, server, element } = client
  const gate = deferred()
  try {
    assert.equal(element.scrollTop, 3400)
    element.scrollTop = 601; await h.onHistoryScroll()
    assert.deepEqual(server.requests.map(r => r.offset), [0])
    server.intercept = () => gate.promise
    element.scrollTop = 600
    const loading = h.onHistoryScroll()
    for (let i = 0; i < 20; i++) h.onHistoryScroll()
    assert.equal(h.olderLoading.value, true)
    assert.deepEqual(server.requests.map(r => r.offset), [0, 100])
    element.scrollTop = 450
    gate.resolve(); await loading; await flush()
    assert.equal(h.messages.value.length, 200)
    assert.equal(element.scrollTop, 4450)
    assert.equal(h.olderLoading.value, false)
    assert.equal(server.maxActive, 1)
  } finally { gate.resolve(); client.dispose() }
})

test('继续上翻会逐页加载，最后不足一页后停止，消息顺序和编号不重复', async () => {
  const client = await mount({ count: 210 }), { haven: h, server, element } = client
  try {
    element.scrollTop = 500; await h.onHistoryScroll(); await new Promise(resolve => setTimeout(resolve, 5))
    element.scrollTop = 500; await h.onHistoryScroll(); await flush()
    assert.deepEqual(server.requests.map(r => r.offset), [0, 100, 200])
    assert.deepEqual(h.messages.value.map(m => m.id), Array.from({ length: 210 }, (_, i) => i + 1))
    assert.equal(h.hasOlder.value, false)
    element.scrollTop = 0; await h.onHistoryScroll()
    assert.equal(server.requests.length, 3)
  } finally { client.dispose() }
})

test('内容不足一屏时自动连续补页，不需要滚动事件，且请求始终串行', async () => {
  const client = await mount({ count: 240, height: 1 }), { haven: h, server } = client
  try {
    await until(() => !h.hasOlder.value && !h.olderLoading.value)
    assert.equal(h.messages.value.length, 240)
    assert.deepEqual(server.requests.map(r => r.offset), [0, 100, 200])
    assert.equal(server.maxActive, 1)
  } finally { client.dispose() }
})

test('切换会话后丢弃旧页响应和错误，不影响新会话正在加载的分页', async () => {
  const client = await mount(), { haven: h, server, element } = client
  const first = deferred(), second = deferred()
  try {
    server.intercept = ({ id, offset }) => offset ? (id === 'one' ? first.promise : second.promise) : null
    element.scrollTop = 500; const oldPage = h.onHistoryScroll()
    await h.choose(h.sessions.value[1]); await nextTick()
    assert.equal(h.olderLoading.value, false)
    element.scrollTop = 500; const newPage = h.onHistoryScroll()
    first.reject(new Error('旧会话网络中断')); await oldPage
    assert.equal(h.olderLoading.value, true)
    assert.equal(h.olderError.value, '')
    second.resolve(); await newPage
    assert.equal(h.messages.value.length, 200)
    assert.ok(h.messages.value.every(m => m.id > 10000))
  } finally { first.resolve(); second.resolve(); client.dispose() }
})

test('刷新最新历史后忽略旧的分页结果，再次预加载使用刷新后的页码', async () => {
  const client = await mount(), { haven: h, server, element } = client
  const gate = deferred()
  try {
    server.intercept = ({ offset }) => offset ? gate.promise : null
    element.scrollTop = 500; const oldPage = h.onHistoryScroll()
    element.scrollTop = 2000; await h.refreshStatus(); await nextTick()
    gate.resolve(); await oldPage; await flush()
    assert.equal(h.messages.value.length, 100)
    assert.equal(element.scrollTop, 2000)
    server.intercept = null
    element.scrollTop = 500; await h.onHistoryScroll()
    assert.deepEqual(server.requests.map(r => r.offset), [0, 100, 0, 100])
    assert.equal(h.messages.value.length, 200)
  } finally { gate.resolve(); client.dispose() }
})

test('失败后自动退避重试，重复滚动不绕过等待，离开顶部后停止重试', async t => {
  t.mock.timers.enable({ apis: ['setTimeout'] })
  const client = await mount(), { haven: h, server, element, reply } = client
  try {
    let failures = 2
    server.intercept = () => failures-- > 0 ? reply({ error: '历史暂不可用' }, 503) : null
    element.scrollTop = 500; await h.onHistoryScroll()
    assert.equal(h.olderError.value, '历史暂不可用')
    for (let i = 0; i < 10; i++) h.onHistoryScroll()
    t.mock.timers.tick(2999); await flush()
    assert.equal(server.requests.length, 2)
    t.mock.timers.tick(1); await flush()
    assert.equal(server.requests.length, 3)
    t.mock.timers.tick(5999); await flush()
    assert.equal(server.requests.length, 3)
    element.scrollTop = 2000; t.mock.timers.tick(1); await flush()
    assert.equal(server.requests.length, 3)
    element.scrollTop = 500; await h.onHistoryScroll()
    assert.equal(h.olderError.value, '')
    assert.equal(h.messages.value.length, 200)
    assert.deepEqual(server.requests.map(r => r.offset), [0, 100, 100, 100])
  } finally { client.dispose(); t.mock.timers.reset() }
})

test('退出登录后不再重试，已在途的响应不会恢复消息列表', async t => {
  t.mock.timers.enable({ apis: ['setTimeout'] })
  const client = await mount(), { haven: h, server, element } = client
  const gate = deferred()
  try {
    server.intercept = () => gate.promise
    element.scrollTop = 500; const loading = h.onHistoryScroll()
    await h.logout(); gate.resolve(); await loading
    t.mock.timers.tick(60000); await flush()
    assert.equal(h.logged.value, false)
    assert.equal(h.messages.value.length, 0)
    assert.equal(h.olderLoading.value, false)
    assert.equal(server.requests.length, 2)
  } finally { gate.resolve(); client.dispose(); t.mock.timers.reset() }
})
