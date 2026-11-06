import test from 'node:test'
import assert from 'node:assert/strict'
import { createRenderer } from 'vue'
import { useHaven } from '../src/useHaven.js'
import { createWorkspace } from '../src/workspace.js'

function memoryStorage() {
  const data = new Map()
  return { get length() { return data.size }, key: i => [...data.keys()][i], getItem: key => data.get(key) || null, setItem: (key, value) => data.set(key, value), removeItem: key => data.delete(key) }
}
async function until(predicate) {
  const deadline = Date.now() + 2500
  while (!predicate()) { assert.ok(Date.now() < deadline, '异步状态没有按预期完成'); await new Promise(resolve => setTimeout(resolve, 5)) }
}
function backend() {
  const server = {
    rows: [{ id: 'one', owned: true, title: '项目讨论', group_id: '', archived: false }, { id: 'old', owned: false, source: 'cli', title: '历史讨论', group_id: '', archived: false }],
    groups: [], calls: [], deleteFailure: false, deletionGate: null, pages: null,
    async fetch(input, init = {}) {
      const url = new URL(input, 'http://haven.test'), path = url.pathname, method = init.method || 'GET'
      server.calls.push(method + ' ' + path + url.search)
      const reply = (data, status = 200) => new Response(JSON.stringify(data), { status })
      if (path === '/api/me') return reply({ username: 'preview' })
      if (path === '/api/health') return reply({})
      if (path === '/api/models') return reply({ providers: [] })
      if (path === '/api/sessions') {
        const offset = Number(url.searchParams.get('offset') || 0)
        const page = server.pages?.[offset] || { data: server.rows, has_more: false }
        return reply({ ...page, offset, limit: 200, groups: server.groups })
      }
      if (path === '/api/groups' && method === 'POST') {
        const group = { id: 'project', name: JSON.parse(init.body).name }; server.groups.push(group)
        return reply({ ...group, groups: server.groups }, 201)
      }
      if (path === '/api/groups/project' && method === 'DELETE') {
        server.groups = []; for (const row of server.rows) if (row.group_id === 'project') row.group_id = ''
        return reply({ groups: [] })
      }
      const match = path.match(/^\/api\/sessions\/([^/]+)(?:\/(.*))?$/)
      if (match) {
        const id = match[1], action = match[2], row = server.rows.find(s => s.id === id)
        if (action === 'messages') return reply({ data: [{ id: id + '-message', role: 'user', content: '保存的消息' }], organization: { group_id: row?.group_id || '', archived: row?.archived || false } })
        if (action === 'settings') return reply({})
        if (action === 'context') return reply({})
        if (action === 'status') return reply({ status: 'idle' })
        if (action === 'organization') { Object.assign(row, JSON.parse(init.body)); return reply({ group_id: row.group_id, archived: row.archived }) }
        if (!action && method === 'DELETE') {
          assert.deepEqual(JSON.parse(init.body), { confirmed: true })
          if (server.deletionGate) await server.deletionGate
          if (server.deleteFailure) return reply({ error: '删除结果待核对，请重试' }, 503)
          server.rows = server.rows.filter(s => s.id !== id); return reply({ deleted: true })
        }
      }
      throw new Error('未处理的测试请求：' + method + ' ' + path)
    },
  }
  return server
}
async function mount(server, storage = memoryStorage()) {
  const names = ['window', 'fetch', 'localStorage', 'EventSource'], saved = names.map(k => Object.getOwnPropertyDescriptor(globalThis, k))
  const globals = { window: new EventTarget(), fetch: server.fetch, localStorage: storage, EventSource: class { close() {} } }
  for (const name of names) Object.defineProperty(globalThis, name, { configurable: true, writable: true, value: globals[name] })
  let h
  const renderer = createRenderer({ createComment: () => ({}), insert() {}, remove() {}, parentNode: () => null, nextSibling: () => null, createElement: () => ({}), createText: () => ({}), patchProp() {}, setText() {}, setElementText() {} })
  const app = renderer.createApp({ setup() { h = useHaven(); return () => null } }); app.mount({})
  const dispose = () => { app.unmount(); names.forEach((k, i) => { if (saved[i]) Object.defineProperty(globalThis, k, saved[i]); else delete globalThis[k] }) }
  try { await until(() => h.logged.value && h.sessions.value.length && !h.sessionsLoading.value); return { h, storage, dispose } } catch (e) { dispose(); throw e }
}

test('项目分组跨来源筛选，归档内容刷新可读，恢复及删除分组保留消息', async () => {
  const server = backend(), storage = memoryStorage()
  let c = await mount(server, storage), h = c.h
  try {
    await h.choose(h.sessions.value[1]); h.openGroupDialog(); await h.saveGroup('同一个项目')
    assert.equal(h.filtered.value.length, 0)
    h.openSessionActions(h.selected.value); await h.organizeSession({ group_id: 'project' })
    assert.deepEqual(h.filtered.value.map(s => s.id), ['old']); assert.equal(h.selected.value.owned, false)
    h.openSessionActions(); await h.organizeSession({ archived: true })
    assert.equal(h.filtered.value.length, 0); h.section.value = 'archive'; assert.equal(h.filtered.value.length, 1)
    assert.equal(h.composerLocked.value, true); assert.equal(h.messages.value[0].content, '保存的消息')
    c.dispose(); c = await mount(server, storage); h = c.h
    await until(() => h.selected.value?.id === 'old' && !h.loading.value)
    assert.equal(h.section.value, 'archive'); assert.equal(h.selectedGroup.value, '同一个项目'); assert.equal(h.selected.value.archived, true)
    h.openSessionActions(); await h.organizeSession({ archived: false }); h.section.value = 'all'
    assert.equal(h.composerLocked.value, false); assert.equal(h.filtered.value.length, 1)
    h.openSessionActions(); await h.organizeSession({ group_id: '' })
    assert.equal(h.selected.value.group_id, ''); assert.equal(h.filtered.value.length, 0)
    h.openGroupDialog(h.groups.value[0]); await h.deleteGroup()
    assert.equal(h.groups.value.length, 0); assert.equal(h.filtered.value.length, 2); assert.equal(h.messages.value[0].content, '保存的消息')
  } finally { c.dispose() }
})

test('彻底删除失败不隐藏会话，重试成功清除草稿、运行和补充回答缓存', async () => {
  const server = backend(), c = await mount(server), h = c.h
  try {
    await h.choose(h.sessions.value[0]); h.text.value = '删除会话的草稿'
    const workspace = createWorkspace(c.storage, 'preview'); workspace.saveControl('one', 'answer', { answer: '隐私回答', unknown: false })
    server.deleteFailure = true; h.openSessionActions(); await h.deleteSession()
    assert.equal(h.sessions.value.length, 2); assert.equal(h.selected.value.id, 'one'); assert.match(h.organizationError.value, /重试/)
    assert.equal(h.selected.value.deletion_pending, true); assert.equal(h.composerLocked.value, true)
    server.deleteFailure = false; await h.deleteSession()
    assert.equal(h.sessions.value.length, 1); assert.equal(h.selected.value, null); assert.equal(h.sessionAction.value, null)
    assert.equal(workspace.draft('one').text, ''); assert.equal(workspace.checkpoint('one'), null); assert.equal(workspace.control('one', 'answer').answer, '')
    assert.equal(workspace.preferences().selected, null)
  } finally { c.dispose() }
})

test('删除响应在切换会话后返回，不清空正在阅读的其他会话', async () => {
  const server = backend(), c = await mount(server), h = c.h
  let release
  server.deletionGate = new Promise(resolve => { release = resolve })
  try {
    await h.choose(h.sessions.value[0]); h.openSessionActions(); const deleting = h.deleteSession()
    await h.choose(h.sessions.value[1]); h.text.value = '另一个会话的草稿'; release(); await deleting
    assert.equal(h.selected.value.id, 'old'); assert.equal(h.text.value, '另一个会话的草稿'); assert.equal(h.messages.value[0].content, '保存的消息')
  } finally { release(); c.dispose() }
})

test('归档和分组查找继续跨过全部为已删除记录的空分页', async () => {
  const server = backend()
  server.pages = { 0: { data: [server.rows[0]], has_more: true }, 200: { data: [], has_more: true }, 400: { data: [{ ...server.rows[1], archived: true }], has_more: false } }
  const c = await mount(server), h = c.h
  try {
    h.section.value = 'archive'
    await until(() => h.filtered.value.length === 1 && !h.searching.value)
    assert.equal(h.filtered.value[0].id, 'old'); assert.ok(server.calls.includes('GET /api/sessions?offset=400')); assert.equal(h.hasMoreSessions.value, false)
  } finally { c.dispose() }
})

test('另一个标签页完成删除后清空当前会话，旧缓存不能在离开页面时重新写回', async () => {
  const server = backend(), c = await mount(server), h = c.h
  try {
    await h.choose(h.sessions.value[0]); h.text.value = '待清理的旧草稿'
    const workspace = createWorkspace(c.storage, 'preview')
    workspace.removeSession('one')
    const event = new Event('storage'); event.key = workspace.prefix + 'deleted:one'; event.newValue = 'true'; window.dispatchEvent(event)
    assert.equal(h.selected.value, null); assert.equal(h.sessions.value.some(s => s.id === 'one'), false)
    workspace.saveDraft('one', { text: '旧标签页迟到的草稿', files: [], pending: null })
    assert.equal(workspace.draft('one').text, ''); assert.equal(c.storage.getItem(workspace.prefix + 'draft:one'), null)
  } finally { c.dispose() }
})
