import { ref, reactive, computed, nextTick, onMounted, onUnmounted, watch } from 'vue'
import MarkdownIt from 'markdown-it'
import DOMPurify from 'dompurify'
import { createWorkspace, emptyDraft, samePayload, mergeMessages, errorText } from './workspace.js'
import { activeStatuses, uncertainStatuses, emptyRun, applyRunEvent } from './events.js'

export function useHaven() {
  const md = new MarkdownIt({ html: false, linkify: true, breaks: true })
  const linkOpen = md.renderer.rules.link_open || ((tokens, idx, options, env, self) => self.renderToken(tokens, idx, options))
  md.renderer.rules.link_open = (tokens, idx, options, env, self) => { tokens[idx].attrSet('target', '_blank'); tokens[idx].attrSet('rel', 'noopener noreferrer'); return linkOpen(tokens, idx, options, env, self) }
  const render = value => DOMPurify.sanitize(md.render(typeof value === 'string' ? value : ''), { ADD_ATTR: ['target'] })

  const logged = ref(null), username = ref(''), password = ref(''), identity = ref({}), loginError = ref(''), signingIn = ref(false)
  const sessions = ref([]), filter = ref(''), section = ref('mine'), selected = ref(null), messages = ref([]), loading = ref(false)
  const sidebar = ref(false), editing = ref(false), titleDraft = ref(''), hasOlder = ref(false), scroller = ref(null), picker = ref(null)
  const health = ref('connecting'), sessionsLoading = ref(false), hasMoreSessions = ref(false), searching = ref(false), olderLoading = ref(false), globalNotice = ref('')
  const drafts = reactive({}), runs = reactive({}), sendingIDs = reactive(new Set()), uploadingIDs = reactive({})
  let workspace = null, stream = null, pollTimer = null, searchTimer = null, checkpointTimer = null
  let selectionEpoch = 0, listEpoch = 0, messageOffset = 0, sessionOffset = 0, searchEpoch = 0

  function view(id) { return runs[id] || (runs[id] = emptyRun()) }
  function draft(id) { return drafts[id] || (drafts[id] = workspace?.draft(id) || emptyDraft()) }
  function persist(id) { if (workspace && drafts[id]) workspace.saveDraft(id, drafts[id]) }
  const currentView = computed(() => selected.value ? view(selected.value.id) : emptyRun())
  const text = computed({ get: () => selected.value ? draft(selected.value.id).text : '', set: value => { if (selected.value) { draft(selected.value.id).text = value; persist(selected.value.id) } } })
  const files = computed(() => selected.value ? draft(selected.value.id).files : [])
  const pending = computed(() => selected.value ? draft(selected.value.id).pending : null)
  const status = computed(() => currentView.value.status)
  const tools = computed(() => currentView.value.tools)
  const liveText = computed(() => currentView.value.liveText)
  const sending = computed(() => selected.value && sendingIDs.has(selected.value.id))
  const uploading = computed(() => selected.value ? uploadingIDs[selected.value.id] || 0 : 0)
  const isBusy = computed(() => activeStatuses.has(status.value))
  const isUncertain = computed(() => uncertainStatuses.has(status.value))
  const composerLocked = computed(() => isBusy.value || isUncertain.value || sending.value || Boolean(pending.value))
  const mine = computed(() => selected.value?.owned === true)
  const notice = computed({ get: () => currentView.value.notice || globalNotice.value, set: value => { if (selected.value) { globalNotice.value = ''; view(selected.value.id).notice = value } else globalNotice.value = value } })
  const filtered = computed(() => sessions.value.filter(s => (section.value === 'mine' ? s.owned : !s.owned) && sessionTitle(s).toLowerCase().includes(filter.value.toLowerCase())))
  const selectedTitle = computed(() => selected.value ? sessionTitle(selected.value) : '一个安静的地方，和 Hermes 一起思考')
  const statusLabel = computed(() => ({ queued: '正在排队', started: '正在开始', running: '正在思考', waiting_for_approval: '等待审批', stopping: '正在停止', completed: '已完成', failed: '运行失败', cancelled: '已停止', interrupted: '运行中断', unknown: '运行结果待核对', submission_unknown: '发送结果待核对' }[status.value] || '本次运行'))
  const sourceName = source => ({ api_server: 'API', cli: 'CLI', oneshot: 'CLI', weixin: '微信', feishu: '飞书', telegram: 'Telegram', tui: 'TUI', cron: '定时任务' }[source] || source || 'Hermes')
  function sessionTitle(s) { return s.haven_title || s.title || s.preview || '未命名会话' }
  function timeLabel(t) { if (!t) return ''; const d = new Date(Number(t) * 1000); if (isNaN(d)) return ''; return d.toDateString() === new Date().toDateString() ? d.toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit' }) : d.toLocaleDateString('zh-CN', { month: 'short', day: 'numeric' }) }

  function closeStream() {
    if (stream) { stream.close(); stream = null }
    if (pollTimer) { clearTimeout(pollTimer); pollTimer = null }
    if (checkpointTimer) { clearTimeout(checkpointTimer); checkpointTimer = null }
  }
  function savePreferences() {
    workspace?.savePreferences({ section: section.value, selected: selected.value ? { id: selected.value.id, title: selectedTitle.value, source: selected.value.source, owned: selected.value.owned } : null })
  }
  function saveCheckpoint(id) { workspace?.saveCheckpoint(id, view(id)) }
  function expireLogin() {
    const wasLogged = logged.value === true
    if (selected.value) saveCheckpoint(selected.value.id)
    savePreferences(); closeStream(); logged.value = false
    username.value = identity.value.username || username.value
    password.value = ''; if (wasLogged) loginError.value = '登录已过期，请重新登录。会话和草稿已保留。'
  }
  async function api(path, options = {}) {
    let res
    try { res = await fetch(path, { credentials: 'same-origin', ...options, headers: { ...(options.body && !(options.body instanceof FormData) ? { 'Content-Type': 'application/json' } : {}), ...(options.headers || {}) } }) }
    catch { const e = new Error('网络连接中断'); e.network = true; throw e }
    let data = {}; try { data = await res.json() } catch {}
    if (!res.ok) {
      if (res.status === 401 && data.code === 'login_required' && path !== '/api/login') expireLogin()
      const e = new Error(typeof data.error === 'string' ? data.error : (data.error?.message || `请求失败 (${res.status})`))
      e.status = res.status; e.data = data; throw e
    }
    return data
  }
  function randomToken() { const a = new Uint8Array(16); crypto.getRandomValues(a); return Array.from(a, b => b.toString(16).padStart(2, '0')).join('') }

  async function login() {
    loginError.value = ''; signingIn.value = true
    try {
      identity.value = await api('/api/login', { method: 'POST', body: JSON.stringify({ username: username.value, password: password.value }) })
      password.value = ''; logged.value = true; await initialize()
    } catch (e) { loginError.value = e.message } finally { signingIn.value = false }
  }
  async function logout() {
    try { await api('/api/logout', { method: 'POST', body: '{}' }) } catch (e) { if (e.status !== 401) { notice.value = '退出未完成，请恢复连接后重试。'; return } }
    savePreferences(); if (selected.value) saveCheckpoint(selected.value.id)
    closeStream(); selectionEpoch++; listEpoch++; logged.value = false; selected.value = null; messages.value = []; password.value = ''
  }
  async function initialize() {
    closeStream(); selectionEpoch++; listEpoch++; searchEpoch++
    for (const key of Object.keys(drafts)) delete drafts[key]
    for (const key of Object.keys(runs)) delete runs[key]
    selected.value = null; messages.value = []; filter.value = ''; sessions.value = []; globalNotice.value = ''
    workspace = createWorkspace(localStorage, identity.value.username, message => { globalNotice.value = message })
    const prefs = workspace.preferences(); section.value = prefs.section === 'history' ? 'history' : 'mine'
    await Promise.all([loadSessions(), checkHealth()])
    if (!logged.value) return
    if (prefs.selected?.id && /^[A-Za-z0-9_-]{1,160}$/.test(prefs.selected.id)) {
      const restored = sessions.value.find(s => s.id === prefs.selected.id) || prefs.selected
      await choose(restored)
    }
  }
  async function checkHealth() { try { await api('/api/health'); health.value = 'online' } catch { health.value = 'offline' } }
  async function loadSessions(more = false) {
    if (sessionsLoading.value && more) return
    const epoch = more ? listEpoch : ++listEpoch
    if (!more) { sessionOffset = 0; hasMoreSessions.value = false }
    sessionsLoading.value = true
    try {
      const data = await api('/api/sessions?offset=' + (more ? sessionOffset : 0))
      if (epoch !== listEpoch || !logged.value) return
      const rows = data.data || [], known = new Map((more ? sessions.value : []).map(s => [s.id, s]))
      let added = 0
      for (const row of rows) { if (!known.has(row.id)) added++; known.set(row.id, row) }
      sessions.value = [...known.values()]
      hasMoreSessions.value = Boolean(data.has_more) && rows.length > 0 && (!more || added > 0)
      sessionOffset = Number(data.offset || 0) + Number(data.limit || 200)
      if (selected.value) { const updated = known.get(selected.value.id); if (updated) selected.value = updated }
    } catch (e) { globalNotice.value = e.message } finally { if (epoch === listEpoch) sessionsLoading.value = false }
  }
  async function loadMoreSessions() { await loadSessions(true) }
  watch(filter, value => {
    clearTimeout(searchTimer); const epoch = ++searchEpoch
    if (!value.trim()) { searching.value = false; return }
    searchTimer = setTimeout(async () => {
      searching.value = true
      try {
        // 标题只在已读取的数据中匹配，继续逐页读取；不调用可能修复或改写旧会话的标题接口。
        while (epoch === searchEpoch && logged.value && hasMoreSessions.value) {
          if (sessionsLoading.value) break
          const before = sessionOffset; await loadMoreSessions(); if (before === sessionOffset) break
        }
      } finally { if (epoch === searchEpoch) searching.value = false }
    }, 350)
  })
  watch(section, savePreferences)

  function scrollBottom() { nextTick(() => { if (scroller.value) scroller.value.scrollTop = scroller.value.scrollHeight }) }
  async function choose(session) {
    if (selected.value) saveCheckpoint(selected.value.id)
    closeStream(); const epoch = ++selectionEpoch
    selected.value = session; draft(session.id); editing.value = false; sidebar.value = false; messages.value = []; messageOffset = 0; hasOlder.value = false; loading.value = true
    const checkpoint = workspace?.checkpoint(session.id)
    if (checkpoint?.runID && !runs[session.id]) Object.assign(view(session.id), checkpoint, { connected: false, retries: 0 })
    savePreferences()
    try { await loadMessages(false, epoch); if (epoch === selectionEpoch) await recover(session.id) }
    catch (e) { if (epoch === selectionEpoch) { view(session.id).notice = e.message; if (e.status === 404) { selected.value = null; savePreferences() } } }
    finally { if (epoch === selectionEpoch) { loading.value = false; scrollBottom() } }
  }
  async function loadMessages(older, epoch = selectionEpoch) {
    if (!selected.value) return
    const id = selected.value.id, next = older ? messageOffset + 100 : 0
    const data = await api(`/api/sessions/${encodeURIComponent(id)}/messages?offset=${next}`)
    if (epoch !== selectionEpoch || selected.value?.id !== id) return
    const rows = data.data || []
    if (data.history_notice) view(id).notice = data.history_notice
    const element = scroller.value, oldHeight = element?.scrollHeight || 0, oldTop = element?.scrollTop || 0
    messages.value = older ? mergeMessages(rows, messages.value) : rows
    messageOffset = next; hasOlder.value = rows.length === 100
    if (older) await nextTick(() => { if (element) element.scrollTop = oldTop + element.scrollHeight - oldHeight })
  }
  async function moreHistory() { if (olderLoading.value) return; olderLoading.value = true; try { await loadMessages(true) } catch (e) { notice.value = e.message } finally { olderLoading.value = false } }
  async function createSession() {
    try {
      const data = await api('/api/sessions', { method: 'POST', body: '{}' })
      const session = { id: data.id, owned: true, haven_title: data.title, source: 'api_server' }
      sessions.value.unshift(session); section.value = 'mine'; await choose(session)
    } catch (e) { notice.value = e.message }
  }
  function startRename() { titleDraft.value = selectedTitle.value; editing.value = true }
  async function rename() {
    if (!selected.value || !titleDraft.value.trim()) return
    const id = selected.value.id, title = titleDraft.value.trim()
    try {
      await api(`/api/sessions/${id}/title`, { method: 'POST', body: JSON.stringify({ title }) })
      const row = sessions.value.find(s => s.id === id); if (row) row.haven_title = title
      if (selected.value?.id === id) { selected.value.haven_title = title; editing.value = false; savePreferences() }
    } catch (e) { view(id).notice = e.message }
  }

  async function uploadImages(chosen) {
    if (!selected.value || composerLocked.value) return
    const id = selected.value.id, row = draft(id)
    if (chosen.length + row.files.length + (uploadingIDs[id] || 0) > 4) { view(id).notice = '一次最多上传 4 张图片'; return }
    uploadingIDs[id] = (uploadingIDs[id] || 0) + chosen.length
    for (const file of chosen) {
      try {
        const form = new FormData(); form.append('file', file)
        const data = await api('/api/uploads', { method: 'POST', body: form })
        row.files.push({ ...data, preview: '/api/uploads/' + data.id }); persist(id)
      } catch (e) { view(id).notice = e.message } finally { uploadingIDs[id]-- }
    }
  }
  async function attach(event) { const chosen = Array.from(event.target.files || []); event.target.value = ''; await uploadImages(chosen) }
  function pasteImages(event) { const chosen = Array.from(event.clipboardData?.items || []).filter(x => x.kind === 'file' && x.type.startsWith('image/')).map(x => x.getAsFile()).filter(Boolean); if (chosen.length) { event.preventDefault(); uploadImages(chosen) } }
  function removeImage(index) { if (!selected.value || composerLocked.value) return; draft(selected.value.id).files.splice(index, 1); persist(selected.value.id) }

  function accepted(id, request, runID, nextStatus) {
    const row = draft(id), current = view(id)
    current.recoveryVersion = (current.recoveryVersion || 0) + 1
    if (row.pending?.request_id === request.request_id) {
      if (samePayload(request, row)) { row.text = ''; row.files = [] }
      row.pending = null; persist(id)
    }
    if (current.runID !== runID) Object.assign(current, emptyRun(), { runID })
    current.status = nextStatus || 'started'; saveCheckpoint(id)
  }
  async function send() {
    if (!selected.value || composerLocked.value || uploading.value || (!text.value.trim() && !files.value.length)) return
    const id = selected.value.id, row = draft(id), existing = workspace?.draft(id).pending
    if (existing) { row.pending = existing; persist(id); await recover(id); return }
    row.pending = { text: row.text, images: row.files.map(f => f.id), request_id: randomToken(), created_at: Date.now() }; persist(id)
    await submitPending(id)
  }
  async function submitPending(id) {
    const row = draft(id), request = row.pending
    if (!request || sendingIDs.has(id)) return
    sendingIDs.add(id); view(id).notice = ''
    try {
      const data = await api(`/api/sessions/${id}/runs`, { method: 'POST', body: JSON.stringify(request) })
      if (!data.run_id) throw new Error('Hermes 没有返回运行编号，请核对发送结果')
      accepted(id, request, data.run_id, data.status)
      if (data.warning) view(id).notice = data.warning
      if (selected.value?.id === id && logged.value) { await recover(id); try { await loadMessages(false); scrollBottom() } catch (e) { view(id).notice = '运行已开始，历史暂未加载：' + e.message } }
      await loadSessions()
    } catch (e) {
      if (e.data?.code === 'submission_pending') { row.pending = null; persist(id); await recover(id) }
      else if (!e.status || e.status >= 500 || ['retry_unsafe', 'request_conflict'].includes(e.data?.code)) { view(id).status = 'submission_unknown'; view(id).notice = e.message; saveCheckpoint(id) }
      else if (e.status === 401 && e.data?.code === 'login_required') { view(id).status = 'submission_unknown' }
      else { row.pending = null; persist(id); view(id).status = 'idle'; view(id).notice = e.message }
    } finally { sendingIDs.delete(id) }
  }
  async function checkPending(id) {
    const row = draft(id), request = row.pending
    if (!request) return
    try {
      const data = await api(`/api/sessions/${id}/requests/${request.request_id}`)
      if (data.state === 'accepted') accepted(id, request, data.run_id, 'started')
      else if (data.state === 'rejected') { row.pending = null; persist(id); view(id).status = 'idle'; view(id).notice = errorText(data.response?.error) || 'Hermes 已拒绝本次请求，原草稿已保留。' }
      else if (data.state === 'acknowledged') { row.pending = null; persist(id); view(id).status = 'idle'; view(id).notice = '原请求已核对并解除等待，未重新执行。' }
      else { view(id).status = 'submission_unknown'; view(id).notice = '发送结果尚未确认。核对不会重新发送消息；安全重试会沿用原请求编号。' }
    } catch (e) { if (e.status !== 404) throw e; view(id).status = 'submission_unknown'; view(id).notice = '服务器尚无本次请求的发送记录，可以点击“安全重试原请求”。' }
  }
  async function retryPending() {
    if (!selected.value) return
    const id = selected.value.id
    try { await checkPending(id); if (draft(id).pending) await submitPending(id); else await recover(id) } catch (e) { view(id).notice = e.message }
  }
  async function acknowledgePending() {
    if (!selected.value || !pending.value || sending.value) return
    if (!window.confirm('请先核对原会话历史和运行状态。此操作只解除 Haven 的等待标记，不会停止或重新发送消息；如原消息已执行，请勿再次提交相同内容。确认已经核对？')) return
    const id = selected.value.id, row = draft(id), request = row.pending
    try {
      await api(`/api/sessions/${id}/requests/${request.request_id}/acknowledge`, { method: 'POST', body: JSON.stringify({ confirmed: true }) })
      row.pending = null; persist(id); view(id).status = 'idle'; view(id).notice = '已解除本地等待，原草稿保留。没有停止或重新执行 Hermes 运行。'
      await recover(id)
    } catch (e) { view(id).notice = e.message }
  }

  function scheduleRecover(id) {
    if (!logged.value || selected.value?.id !== id) return
    if (pollTimer) clearTimeout(pollTimer)
    const current = view(id); current.connected = false
    const delay = Math.min(30000, 1500 * 2 ** Math.min(current.retries++, 5))
    pollTimer = setTimeout(async () => { pollTimer = null; if (logged.value && selected.value?.id === id) { try { await api('/api/me'); await recover(id) } catch (e) { if (logged.value) { current.notice = '连接中断，正在重新连接。草稿和发送记录已保留。'; scheduleRecover(id) } } } }, delay)
  }
  function clearConnectionNotice(current) {
    if (/^(网络已断开|连接中断|暂时无法核对运行状态|运行已结束，历史暂未恢复|运行已开始，历史暂未加载)/.test(current.notice)) current.notice = ''
  }
  function listen(id, runID) {
    if (!logged.value || selected.value?.id !== id) return
    closeStream(); const current = view(id)
    const es = new EventSource(`/api/sessions/${encodeURIComponent(id)}/events?run_id=${encodeURIComponent(runID)}&last_seq=${current.lastSeq}`)
    stream = es
    es.onopen = () => { if (stream !== es) return; current.connected = true; current.retries = 0; health.value = 'online'; clearConnectionNotice(current) }
    es.onmessage = async event => {
      if (stream !== es || selected.value?.id !== id) return
      let data; try { data = JSON.parse(event.data) } catch { return }
      const result = applyRunEvent(current, data)
      if (result.duplicate) return
      clearTimeout(checkpointTimer); checkpointTimer = setTimeout(() => saveCheckpoint(id), 150)
      if (result.terminal) {
        closeStream(); current.connected = false; saveCheckpoint(id)
        try { await loadMessages(false); if (current.runID === runID) { current.liveText = ''; current.tools = current.tools.filter(t => t.state === 'error'); saveCheckpoint(id) }; await loadSessions() }
        catch (e) { current.notice = '运行已结束，历史暂未恢复：' + e.message; scheduleRecover(id) }
      }
      scrollBottom()
    }
    es.onerror = () => { if (stream !== es) return; saveCheckpoint(id); closeStream(); current.connected = false; health.value = 'offline'; scheduleRecover(id) }
  }
  async function recover(id = selected.value?.id) {
    if (!id || !logged.value) return
    const current = view(id)
    try {
      if (draft(id).pending) await checkPending(id)
      const recoveryVersion = (current.recoveryVersion || 0) + 1; current.recoveryVersion = recoveryVersion
      const data = await api(`/api/sessions/${id}/status`)
      if (!logged.value || recoveryVersion !== current.recoveryVersion) return
      clearConnectionNotice(current)
      if (data.status === 'submission_unknown') {
        if (!draft(id).pending) { draft(id).pending = { text: data.text || '', images: data.images || [], request_id: data.request_id, created_at: data.created_at ? Date.parse(data.created_at) : 1 }; persist(id) }
        current.status = 'submission_unknown'; current.notice = '此会话有待确认的发送，已恢复原请求。请先核对结果。'; scheduleRecover(id); return
      }
      if (draft(id).pending) { scheduleRecover(id); return }
      if (data.run_id && current.runID !== data.run_id) Object.assign(current, emptyRun(), { runID: data.run_id })
      current.status = data.status || 'idle'
      if (data.error) current.notice = errorText(data.error)
      if (uncertainStatuses.has(current.status)) current.notice = 'Hermes 无法确认该运行是否完成，请核对历史；当前不会自动重新执行。'
      if (activeStatuses.has(current.status) && data.run_id && selected.value?.id === id) listen(id, data.run_id)
      else if (selected.value?.id === id && current.status !== 'idle') { await loadMessages(false); if (recoveryVersion !== current.recoveryVersion) return; current.liveText = ''; current.tools = current.tools.filter(t => t.state === 'error'); saveCheckpoint(id) }
      if (uncertainStatuses.has(current.status)) scheduleRecover(id)
      else current.retries = 0
      if (data.run_id) health.value = 'online'
    } catch (e) {
      if (!logged.value) return
      if (e.status === 404) { current.status = 'unavailable'; current.notice = '实时运行记录已不可用，请以保存的会话历史为准。'; return }
      current.notice = '暂时无法核对运行状态：' + e.message; health.value = 'offline'; scheduleRecover(id)
    }
  }
  async function refreshStatus() { if (!selected.value) return; try { await loadMessages(false); await recover() } catch (e) { notice.value = e.message } }
  async function stop() {
    if (!selected.value || !isBusy.value) return
    const id = selected.value.id
    try { await api(`/api/sessions/${id}/stop`, { method: 'POST', body: '{}' }); view(id).status = 'stopping'; saveCheckpoint(id); scheduleRecover(id) }
    catch (e) { view(id).notice = e.message }
  }
  function online() { if (logged.value) { checkHealth(); if (selected.value) recover(selected.value.id) } }
  function offline() {
    health.value = 'offline'
    if (logged.value && selected.value) { const id = selected.value.id; saveCheckpoint(id); closeStream(); view(id).notice = '网络已断开，草稿和发送记录已保留。恢复连接后会重新核对运行。'; scheduleRecover(id) }
  }
  function storageChanged(event) {
    if (workspace && selected.value && event.key === workspace.prefix + 'draft:' + selected.value.id) {
      const id = selected.value.id
      try { const incoming = JSON.parse(event.newValue); if (incoming?.pending && !draft(id).pending) draft(id).pending = incoming.pending } catch {}
      recover(id)
    }
  }
  function beforeUnload() { if (selected.value) saveCheckpoint(selected.value.id); savePreferences() }

  function messageText(message) { if (Array.isArray(message.content)) return message.content.filter(x => x.type === 'text').map(x => x.text).join('\n'); return typeof message.content === 'string' ? message.content : '' }
  function imageBlocks(message) { return Array.isArray(message.content) ? message.content.filter(x => x.type === 'image' && x.src) : [] }
  function calls(message) { if (!message.tool_calls) return []; if (Array.isArray(message.tool_calls)) return message.tool_calls; try { return JSON.parse(message.tool_calls) || [] } catch { return [] } }
  function callName(call) { return call.function?.name || call.name || '工具' }
  function callArgs(call) { const value = call.function?.arguments || call.arguments || ''; try { return JSON.stringify(typeof value === 'string' ? JSON.parse(value) : value, null, 2) } catch { return String(value) } }
  function roleLabel(message) { return message.role === 'user' ? '你' : message.role === 'assistant' ? 'Hermes' : message.tool_name || '工具' }

  onMounted(async () => {
    window.addEventListener('online', online); window.addEventListener('offline', offline); window.addEventListener('storage', storageChanged); window.addEventListener('pagehide', beforeUnload)
    try { identity.value = await api('/api/me'); logged.value = true; await initialize() } catch (e) { logged.value = false; if (e.network) loginError.value = '无法连接服务器，请恢复网络后重试。' }
  })
  onUnmounted(() => { beforeUnload(); closeStream(); clearTimeout(searchTimer); window.removeEventListener('online', online); window.removeEventListener('offline', offline); window.removeEventListener('storage', storageChanged); window.removeEventListener('pagehide', beforeUnload) })

  return { logged, username, password, identity, loginError, signingIn, sessions, filter, section, selected, messages, loading, sidebar, editing, titleDraft, hasOlder, scroller, picker, health, sessionsLoading, hasMoreSessions, searching, olderLoading, globalNotice, text, files, pending, status, tools, liveText, sending, uploading, isBusy, isUncertain, composerLocked, mine, notice, filtered, selectedTitle, statusLabel, sourceName, sessionTitle, timeLabel, login, logout, checkHealth, loadMoreSessions, choose, moreHistory, createSession, startRename, rename, attach, pasteImages, removeImage, send, retryPending, acknowledgePending, refreshStatus, stop, messageText, imageBlocks, calls, callName, callArgs, roleLabel, render }
}
