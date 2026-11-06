import { ref, reactive, computed, nextTick, onMounted, onUnmounted, watch } from 'vue'
import MarkdownIt from 'markdown-it'
import DOMPurify from 'dompurify'
import { createWorkspace, emptyDraft, samePayload, mergeMessages, errorText } from './workspace.js'
import { activeStatuses, uncertainStatuses, emptyRun, applyRunEvent } from './events.js'
import { createOutgoingMessage, reconcileOutgoing, withOutgoingMessage } from './outgoing.js'
import { normalizeAttachment, attachmentPayload, validateUploads } from './attachments.js'
import { normalizeSettings } from './modelSettings.js'
import { pendingControls } from './controls.js'

export function useHaven() {
  const md = new MarkdownIt({ html: false, linkify: true, breaks: true })
  const linkOpen = md.renderer.rules.link_open || ((tokens, idx, options, env, self) => self.renderToken(tokens, idx, options))
  md.renderer.rules.link_open = (tokens, idx, options, env, self) => { tokens[idx].attrSet('target', '_blank'); tokens[idx].attrSet('rel', 'noopener noreferrer'); return linkOpen(tokens, idx, options, env, self) }
  const render = value => DOMPurify.sanitize(md.render(typeof value === 'string' ? value : ''), { ADD_ATTR: ['target'] })

  const logged = ref(null), username = ref(''), password = ref(''), identity = ref({}), loginError = ref(''), signingIn = ref(false)
  const sessions = ref([]), filter = ref(''), section = ref('mine'), selected = ref(null), messages = ref([]), loading = ref(false)
  const groups = ref([]), groupFilter = ref(''), sessionAction = ref(null), groupDialog = ref(null), organizationBusy = ref(false), organizationError = ref('')
  const removedSessions = new Set()
  const sidebar = ref(false), editing = ref(false), titleDraft = ref(''), hasOlder = ref(false), scroller = ref(null), sessionList = ref(null), picker = ref(null)
  const health = ref('connecting'), healthChecking = ref(false), healthError = ref(''), sessionsLoading = ref(false), sessionsError = ref(''), searching = ref(false), olderLoading = ref(false), globalNotice = ref('')
  const sessionPageSize = 200, sessionLimit = ref(sessionPageSize)
  let moreSessionPages = false
  const olderError = ref('')
  const drafts = reactive({}), runs = reactive({}), sendingIDs = reactive(new Set()), uploadingIDs = reactive({})
  const previewFile = ref(null)
  const outgoing = reactive({})
  const modelCatalog = ref({ providers: [], model: '', provider: '' }), modelsLoading = ref(false), modelsError = ref('')
  const newSessionOpen = ref(false), newSessionSettings = ref(normalizeSettings()), creatingSession = ref(false), newSessionError = ref('')
  const newSessionDefaults = ref(normalizeSettings())
  const contextData = reactive({}), contextVersions = reactive({}), contextErrors = reactive({}), contextLoadingIDs = reactive(new Set())
  let contextTimer = null
  const sessionModels = reactive({}), settingsReady = reactive({}), settingsErrors = reactive({}), settingsSavingIDs = reactive(new Set()), settingsLoadingIDs = reactive(new Set())
  const controlDrafts = reactive({})
  let workspace = null, stream = null, pollTimer = null, searchTimer = null, checkpointTimer = null, statusTimer = null, healthTimer = null
  let selectionEpoch = 0, listEpoch = 0, messageOffset = 0, sessionOffset = 0, searchEpoch = 0, healthRequestID = 0
  let modelsEpoch = 0
  let historyEpoch = 0, historyTimer = null, historyFailures = 0, historyRefreshing = false

  function isRemoved(id) { return removedSessions.has(id) || workspace?.deleted(id) }
  function view(id) { return runs[id] || (runs[id] = emptyRun()) }
  function draft(id) { return drafts[id] || (drafts[id] = workspace?.draft(id) || emptyDraft()) }
  function persist(id) { if (workspace && drafts[id]) workspace.saveDraft(id, drafts[id]) }
  function outgoingFor(id) { if (!(id in outgoing)) outgoing[id] = workspace?.outgoing(id) || null; return outgoing[id] }
  function saveOutgoing(id, value) { outgoing[id] = value; workspace?.saveOutgoing(id, value) }
  function clearOutgoing(id, requestID) { if (outgoingFor(id)?.requestID === requestID) saveOutgoing(id, null) }
  const visibleMessages = computed(() => selected.value ? withOutgoingMessage(messages.value, outgoingFor(selected.value.id)) : [])
  const currentView = computed(() => selected.value ? view(selected.value.id) : emptyRun())
  function controlState(id, p) {
    const key = id + ':' + p.key
    if (!controlDrafts[key]) {
      const saved = workspace?.control(id, p.key) || { answer: '', unknown: false }
      controlDrafts[key] = { ...saved, sending: false, error: saved.unknown ? '上次提交结果尚未确认，请先核对状态；不会自动重发。' : '' }
    }
    return controlDrafts[key]
  }
  const controls = computed(() => selected.value ? currentView.value.controls.map(p => ({ ...p, state: controlState(selected.value.id, p) })) : [])
  const controlsSyncing = computed(() => currentView.value.controlsSyncing || !currentView.value.controlsVerified || currentView.value.status === 'stopping' || health.value !== 'online')
  const text = computed({ get: () => selected.value ? draft(selected.value.id).text : '', set: value => { if (selected.value) { draft(selected.value.id).text = value; persist(selected.value.id) } } })
  const files = computed(() => selected.value ? draft(selected.value.id).files : [])
  const pending = computed(() => selected.value ? draft(selected.value.id).pending : null)
  const status = computed(() => currentView.value.status)
  const tools = computed(() => currentView.value.tools)
  const liveText = computed(() => currentView.value.liveText)
  const sending = computed(() => selected.value && sendingIDs.has(selected.value.id))
  const uploadQueue = computed(() => selected.value ? uploadingIDs[selected.value.id] || [] : [])
  const uploading = computed(() => uploadQueue.value.length)
  const isBusy = computed(() => activeStatuses.has(status.value))
  const isUncertain = computed(() => uncertainStatuses.has(status.value))
  const composerLocked = computed(() => selected.value?.archived || selected.value?.deletion_pending || isBusy.value || isUncertain.value || sending.value || Boolean(pending.value))
  const currentSettings = computed(() => normalizeSettings(selected.value ? sessionModels[selected.value.id] : null))
  const settingsSaving = computed(() => Boolean(selected.value && settingsSavingIDs.has(selected.value.id)))
  const settingsLoading = computed(() => Boolean(selected.value && settingsLoadingIDs.has(selected.value.id)))
  const settingsBlocked = computed(() => Boolean(selected.value && (!settingsReady[selected.value.id] || settingsLoading.value || settingsSaving.value)))
  const settingsError = computed(() => selected.value ? settingsErrors[selected.value.id] || '' : '')
  const contextUsage = computed(() => selected.value ? contextData[selected.value.id] || null : null)
  const contextLoading = computed(() => Boolean(selected.value && contextLoadingIDs.has(selected.value.id)))
  const contextError = computed(() => selected.value ? contextErrors[selected.value.id] || '' : '')
  const mine = computed(() => selected.value?.owned === true)
  const notice = computed({ get: () => currentView.value.notice || globalNotice.value, set: value => { if (selected.value) { globalNotice.value = ''; view(selected.value.id).notice = value } else globalNotice.value = value } })
  const matchingSessions = computed(() => sessions.value.filter(s =>
    (section.value === 'archive' ? s.archived : !s.archived) &&
    (section.value !== 'mine' || s.owned) && (section.value !== 'history' || !s.owned) &&
    (!groupFilter.value || (groupFilter.value === 'ungrouped' ? !s.group_id : s.group_id === groupFilter.value)) &&
    sessionTitle(s).toLowerCase().includes(filter.value.trim().toLowerCase())))
  const filtered = computed(() => matchingSessions.value.slice(0, sessionLimit.value))
  const hasMoreSessions = computed(() => matchingSessions.value.length > sessionLimit.value)
  const selectedGroup = computed(() => groups.value.find(g => g.id === selected.value?.group_id)?.name || '')
  const actionBlocked = computed(() => {
    const row = sessionAction.value
    if (!row || row.deletion_pending) return ''
    if (sendingIDs.has(row.id) || activeStatuses.has(view(row.id).status) || uncertainStatuses.has(view(row.id).status) || draft(row.id).pending) return '会话正在运行或发送结果待核对，请先停止并核对状态。'
    return uploadingIDs[row.id]?.length ? '附件正在上传，请等待上传结束后再删除。' : ''
  })
  const selectedTitle = computed(() => selected.value ? sessionTitle(selected.value) : '一个安静的地方，和 Hermes 一起思考')
  const statusLabel = computed(() => controls.value.length ? (controls.value.some(p => p.kind === 'approval') ? '等待审批' : '等待补充回答') : ({ queued: '正在排队', started: '正在开始', running: '正在思考', waiting_for_approval: '等待审批', stopping: '正在停止', completed: '已完成', failed: '运行失败', cancelled: '已停止', interrupted: '运行中断', unknown: '运行结果待核对', submission_unknown: '发送结果待核对' }[status.value] || '本次运行'))
  const sourceName = source => ({ api_server: 'API', cli: 'CLI', oneshot: 'CLI', weixin: '微信', feishu: '飞书', telegram: 'Telegram', tui: 'TUI', cron: '定时任务' }[source] || source || 'Hermes')
  function sessionTitle(s) { return s.haven_title || s.title || s.preview || '未命名会话' }
  function timeLabel(t) { if (!t) return ''; const d = new Date(Number(t) * 1000); if (isNaN(d)) return ''; return d.toDateString() === new Date().toDateString() ? d.toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit' }) : d.toLocaleDateString('zh-CN', { month: 'short', day: 'numeric' }) }

  function closeStream() {
    if (stream) { stream.close(); stream = null }
    if (pollTimer) { clearTimeout(pollTimer); pollTimer = null }
    if (checkpointTimer) { clearTimeout(checkpointTimer); checkpointTimer = null }
    if (statusTimer) { clearTimeout(statusTimer); statusTimer = null }
  }
  function savePreferences() {
    workspace?.savePreferences({ section: section.value, groupFilter: groupFilter.value, newSessionSettings: newSessionDefaults.value, selected: selected.value ? { id: selected.value.id, title: selectedTitle.value, source: selected.value.source, owned: selected.value.owned, archived: selected.value.archived, group_id: selected.value.group_id, deletion_pending: selected.value.deletion_pending } : null })
  }
  function saveCheckpoint(id) { workspace?.saveCheckpoint(id, view(id)) }
  function expireLogin() {
    invalidateSessionList()
    resetHistoryLoading()
    stopContextRefresh()
    previewFile.value = null
    newSessionOpen.value = false
    sessionAction.value = null; groupDialog.value = null
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
    resetHistoryLoading()
    clearHealthTimer()
    stopContextRefresh(); closeStream(); selectionEpoch++; invalidateSessionList(); logged.value = false; selected.value = null; messages.value = []; password.value = ''; previewFile.value = null; newSessionOpen.value = false; sessionAction.value = null; groupDialog.value = null
  }
  async function initialize() {
    clearHealthTimer()
    resetHistoryLoading()
    stopContextRefresh()
    closeStream(); selectionEpoch++; listEpoch++; searchEpoch++
    for (const key of Object.keys(drafts)) delete drafts[key]
    for (const key of Object.keys(runs)) delete runs[key]
    for (const key of Object.keys(controlDrafts)) delete controlDrafts[key]
    for (const key of Object.keys(outgoing)) delete outgoing[key]
    for (const key of Object.keys(uploadingIDs)) delete uploadingIDs[key]
    for (const rows of [sessionModels, settingsReady, settingsErrors]) for (const key of Object.keys(rows)) delete rows[key]
    settingsSavingIDs.clear(); settingsLoadingIDs.clear(); newSessionOpen.value = false
    for (const rows of [contextData, contextVersions, contextErrors]) for (const key of Object.keys(rows)) delete rows[key]
    contextLoadingIDs.clear()
    modelsEpoch++; modelsLoading.value = false; modelCatalog.value = { providers: [], model: '', provider: '' }; modelsError.value = ''
    previewFile.value = null
    selected.value = null; messages.value = []; filter.value = ''; sessions.value = []; globalNotice.value = ''
    groups.value = []; groupFilter.value = ''; sessionAction.value = null; groupDialog.value = null; organizationError.value = ''; organizationBusy.value = false; removedSessions.clear()
    workspace = createWorkspace(localStorage, identity.value.username, message => { globalNotice.value = message })
    const prefs = workspace.preferences(); section.value = ['all', 'mine', 'history', 'archive'].includes(prefs.section) ? prefs.section : 'all'
    newSessionDefaults.value = normalizeSettings(prefs.newSessionSettings); newSessionSettings.value = normalizeSettings(newSessionDefaults.value)
    loadModels()
    await Promise.all([loadSessions(), checkHealth()])
    if (!logged.value) return
    groupFilter.value = prefs.groupFilter === 'ungrouped' || groups.value.some(g => g.id === prefs.groupFilter) ? prefs.groupFilter : ''
    resumeSessionScan()
    if (prefs.selected?.id && !isRemoved(prefs.selected.id) && /^[A-Za-z0-9_-]{1,160}$/.test(prefs.selected.id)) {
      const restored = sessions.value.find(s => s.id === prefs.selected.id) || prefs.selected
      await choose(restored)
    }
  }
  function clearHealthTimer() { if (healthTimer) { clearTimeout(healthTimer); healthTimer = null } }
  function scheduleHealthCheck() {
    if (!logged.value || healthTimer) return
    healthTimer = setTimeout(() => { healthTimer = null; if (logged.value) checkHealth() }, 15000)
  }
  async function checkHealth() {
    const requestID = ++healthRequestID
    clearHealthTimer()
    healthChecking.value = true; healthError.value = ''
    try {
      await api('/api/health')
      if (requestID === healthRequestID) health.value = 'online'
    } catch (e) {
      if (requestID === healthRequestID) { health.value = 'offline'; healthError.value = e.message || '连接检查失败'; scheduleHealthCheck() }
    } finally {
      if (requestID === healthRequestID) healthChecking.value = false
    }
  }
  function stopContextRefresh() { clearTimeout(contextTimer); contextTimer = null }
  function scheduleContextRefresh() {
    stopContextRefresh()
    if (!logged.value || !selected.value) return
    contextTimer = setTimeout(async () => { contextTimer = null; if (logged.value && selected.value) { await loadContext(selected.value.id); scheduleContextRefresh() } }, isBusy.value ? 15000 : 60000)
  }
  async function loadContext(id = selected.value?.id, force = false) {
    if (!id || !logged.value || (contextLoadingIDs.has(id) && !force)) return
    const currentWorkspace = workspace, version = (contextVersions[id] || 0) + 1
    contextVersions[id] = version; contextLoadingIDs.add(id)
    try {
      const data = await api(`/api/sessions/${id}/context`)
      if (workspace === currentWorkspace && logged.value && contextVersions[id] === version) { contextData[id] = data; contextErrors[id] = '' }
    } catch (e) { if (workspace === currentWorkspace && logged.value && contextVersions[id] === version) contextErrors[id] = e.message }
    finally { if (workspace === currentWorkspace && contextVersions[id] === version) contextLoadingIDs.delete(id) }
  }
  async function loadModels() {
    if (modelsLoading.value) return
    const currentWorkspace = workspace, epoch = ++modelsEpoch
    modelsLoading.value = true; modelsError.value = ''
    try { const data = await api('/api/models'); if (workspace === currentWorkspace && logged.value) modelCatalog.value = { providers: [], ...data } }
    catch (e) { if (workspace === currentWorkspace && logged.value) modelsError.value = e.message }
    finally { if (epoch === modelsEpoch) modelsLoading.value = false }
  }
  async function loadSettings(id = selected.value?.id) {
    if (!id || settingsLoadingIDs.has(id) || settingsSavingIDs.has(id)) return
    const currentWorkspace = workspace
    settingsLoadingIDs.add(id); settingsErrors[id] = ''
    try {
      const value = await api(`/api/sessions/${id}/settings`)
      if (workspace === currentWorkspace && logged.value && !removedSessions.has(id)) { sessionModels[id] = normalizeSettings(value); settingsReady[id] = true; loadContext(id, true) }
    } catch (e) { if (workspace === currentWorkspace && logged.value) { settingsReady[id] = false; settingsErrors[id] = '无法读取会话设置：' + e.message } }
    finally { if (workspace === currentWorkspace) settingsLoadingIDs.delete(id) }
  }
  async function saveSettings(value) {
    const id = selected.value?.id
    if (!id || !settingsReady[id] || settingsLoadingIDs.has(id) || settingsSavingIDs.has(id)) return
    const previous = currentSettings.value, next = normalizeSettings(value), currentWorkspace = workspace
    sessionModels[id] = next; settingsSavingIDs.add(id); settingsErrors[id] = ''
    try {
      const saved = await api(`/api/sessions/${id}/settings`, { method: 'PUT', body: JSON.stringify(next) })
      if (workspace !== currentWorkspace || !logged.value || removedSessions.has(id)) return
      sessionModels[id] = normalizeSettings(saved)
      loadContext(id, true)
      newSessionDefaults.value = normalizeSettings(saved)
      if (!newSessionOpen.value) newSessionSettings.value = normalizeSettings(saved)
      savePreferences()
    } catch (e) { if (workspace === currentWorkspace && logged.value && !removedSessions.has(id)) { sessionModels[id] = previous; settingsErrors[id] = '设置未保存：' + e.message } }
    finally { if (workspace === currentWorkspace) settingsSavingIDs.delete(id) }
  }
  async function loadSessions(more = false) {
    if (sessionsLoading.value && more) return
    const epoch = more ? listEpoch : ++listEpoch
    if (!more) { clearTimeout(searchTimer); searchEpoch++; searching.value = false; sessionOffset = 0; moreSessionPages = false; sessionLimit.value = sessionPageSize }
    sessionsLoading.value = true; sessionsError.value = ''
    let loaded = false
    try {
      const data = await api('/api/sessions?offset=' + (more ? sessionOffset : 0))
      if (epoch !== listEpoch || !logged.value) return
      const rows = data.data || [], known = new Map((more ? sessions.value : []).map(s => [s.id, s]))
      for (const row of rows) { if (!isRemoved(row.id)) known.set(row.id, row) }
      sessions.value = [...known.values()]
      if (Array.isArray(data.groups)) groups.value = data.groups
      moreSessionPages = data.has_more === true
      sessionOffset = Number(data.offset || 0) + Number(data.limit || sessionPageSize)
      loaded = true
      if (selected.value) { const updated = known.get(selected.value.id); if (updated) selected.value = updated }
    } catch (e) { if (epoch === listEpoch && logged.value) sessionsError.value = e.message }
    finally {
      if (epoch === listEpoch) {
        sessionsLoading.value = false
        if (!more && loaded) scanSessions()
      }
    }
  }
  async function loadMoreSessions() {
    if (sessionsLoading.value || searching.value || !logged.value) return
    if (sessionsError.value) {
      if (sessionOffset === 0) await loadSessions()
      else await scanSessions()
      return
    }
    if (!hasMoreSessions.value) return
    sessionLimit.value += sessionPageSize
    await scanSessions()
  }
  function onSessionsScroll(event) {
    const list = event.currentTarget
    if (!hasMoreSessions.value || sessionsLoading.value || searching.value || sessionsError.value) return
    if (list.scrollHeight - list.scrollTop - list.clientHeight <= 120) loadMoreSessions()
  }
  async function scanSessions() {
    clearTimeout(searchTimer); const epoch = ++searchEpoch
    searching.value = logged.value && moreSessionPages && !hasMoreSessions.value
    try {
      // 先找到当前筛选下的下一条会话，再显示加载入口；空页和其他来源的记录继续跳过。
      while (epoch === searchEpoch && logged.value && moreSessionPages && !hasMoreSessions.value) {
        if (sessionsLoading.value) { await new Promise(resolve => setTimeout(resolve, 30)); continue }
        const before = sessionOffset; await loadSessions(true); if (before === sessionOffset) break
      }
    } finally { if (epoch === searchEpoch) searching.value = false }
  }
  watch([filter, section, groupFilter], () => {
    clearTimeout(searchTimer); searchEpoch++; sessionLimit.value = sessionPageSize
    if (sessionList.value) sessionList.value.scrollTop = 0
    if (hasMoreSessions.value) sessionsError.value = ''
    searching.value = logged.value && moreSessionPages && !hasMoreSessions.value && !sessionsError.value
    if (searching.value) searchTimer = setTimeout(scanSessions, 350)
  })
  watch(() => matchingSessions.value.length, (count, previous) => {
    if (count < previous && !sessionsLoading.value && !searching.value && !sessionsError.value) scanSessions()
  })
  watch([section, groupFilter], savePreferences)

  function clearHistoryTimer() { clearTimeout(historyTimer); historyTimer = null }
  function resetHistoryLoading() {
    clearHistoryTimer(); historyEpoch++; historyFailures = 0; historyRefreshing = false
    olderLoading.value = false; olderError.value = ''
  }
  function onHistoryScroll() {
    if (!logged.value || !selected.value || loading.value || historyRefreshing || olderLoading.value || historyTimer !== null || !hasOlder.value) return
    if (scroller.value && scroller.value.scrollTop <= 600) return moreHistory()
  }
  function scrollBottom() { nextTick(() => { if (scroller.value) { scroller.value.scrollTop = scroller.value.scrollHeight; onHistoryScroll() } }) }
  async function choose(session) {
    if (isRemoved(session.id)) return
    resetHistoryLoading()
    stopContextRefresh()
    if (selected.value) saveCheckpoint(selected.value.id)
    closeStream(); const epoch = ++selectionEpoch
    selected.value = session; draft(session.id); outgoingFor(session.id); editing.value = false; sidebar.value = false; messages.value = []; messageOffset = 0; hasOlder.value = false; loading.value = true
    const checkpoint = workspace?.checkpoint(session.id)
    if (checkpoint?.runID && !runs[session.id]) Object.assign(view(session.id), checkpoint, { connected: false, retries: 0 })
    savePreferences()
    try { await Promise.all([loadMessages(false, epoch), loadSettings(session.id)]); if (epoch === selectionEpoch) await recover(session.id) }
    catch (e) { if (epoch === selectionEpoch) { view(session.id).notice = e.message; if (e.status === 404) { selected.value = null; savePreferences() } } }
    finally { if (epoch === selectionEpoch) { loading.value = false; scrollBottom(); scheduleContextRefresh() } }
  }
  async function loadMessages(older, epoch = selectionEpoch) {
    if (!selected.value) return
    if (!older) { resetHistoryLoading(); historyRefreshing = true }
    const revision = historyEpoch
    const id = selected.value.id, next = older ? messageOffset + 100 : 0
    try {
      const data = await api(`/api/sessions/${encodeURIComponent(id)}/messages?offset=${next}`)
      // 刷新历史或切换会话后，旧分页响应不能覆盖新列表及其滚动位置。
      if (epoch !== selectionEpoch || revision !== historyEpoch || selected.value?.id !== id || !logged.value) return
      if (data.organization) { Object.assign(selected.value, data.organization); const row = sessions.value.find(s => s.id === id); if (row) Object.assign(row, data.organization); savePreferences() }
      const rows = data.data || []
      if (data.history_notice) view(id).notice = data.history_notice
      const element = scroller.value, oldHeight = element?.scrollHeight || 0, oldTop = element?.scrollTop || 0
      messages.value = older ? mergeMessages(rows, messages.value) : rows
      if (outgoingFor(id) && !reconcileOutgoing(messages.value, outgoingFor(id))) saveOutgoing(id, null)
      messageOffset = next; hasOlder.value = rows.length === 100
      if (older) await nextTick(() => { if (element && scroller.value === element && revision === historyEpoch && epoch === selectionEpoch) element.scrollTop = oldTop + element.scrollHeight - oldHeight })
    } finally {
      if (!older && revision === historyEpoch) { historyRefreshing = false; nextTick(onHistoryScroll) }
    }
  }
  async function moreHistory() {
    const revision = historyEpoch
    olderLoading.value = true; olderError.value = ''
    let retryDelay = 0
    try { await loadMessages(true); if (revision === historyEpoch) historyFailures = 0 }
    catch (e) {
      if (revision === historyEpoch) { olderError.value = e.message; retryDelay = Math.min(3000 * 2 ** historyFailures++, 30000) }
    } finally {
      if (revision === historyEpoch) {
        olderLoading.value = false
        // 逐页串行补足顶部缓冲区；失败后退避重试，离开顶部后停止预加载。
        if (logged.value && hasOlder.value) historyTimer = setTimeout(() => { historyTimer = null; onHistoryScroll() }, retryDelay)
      }
    }
  }
  function openNewSession() { newSessionSettings.value = normalizeSettings(newSessionDefaults.value); newSessionError.value = ''; newSessionOpen.value = true; sidebar.value = false; if (!modelCatalog.value.providers.length) loadModels() }
  async function createSession() {
    if (creatingSession.value) return
    creatingSession.value = true; newSessionError.value = ''
    try {
      const settings = normalizeSettings(newSessionSettings.value)
      const data = await api('/api/sessions', { method: 'POST', body: JSON.stringify({ settings }) })
      const session = { id: data.id, owned: true, haven_title: data.title, source: 'api_server', settings: data.settings }
      newSessionDefaults.value = settings; newSessionOpen.value = false; savePreferences()
      sessions.value.unshift(session); section.value = 'mine'; groupFilter.value = ''; await choose(session)
    } catch (e) { newSessionError.value = e.message }
    finally { creatingSession.value = false }
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

  function openSessionActions(session = selected.value) { sessionAction.value = session; groupDialog.value = null; organizationError.value = '' }
  function openGroupDialog(group = null) { groupDialog.value = group ? { ...group } : { id: '', name: '' }; sessionAction.value = null; organizationError.value = '' }
  function invalidateSessionList() { clearTimeout(searchTimer); listEpoch++; searchEpoch++; sessionsLoading.value = false; searching.value = false }
  function resumeSessionScan() { if (!sessionsLoading.value && !searching.value && !sessionsError.value) scanSessions() }
  async function organizeSession(patch) {
    if (!sessionAction.value || organizationBusy.value) return
    const id = sessionAction.value.id, currentWorkspace = workspace
    organizationBusy.value = true; organizationError.value = ''; invalidateSessionList()
    try {
      const meta = await api(`/api/sessions/${id}/organization`, { method: 'PUT', body: JSON.stringify(patch) })
      if (workspace !== currentWorkspace || !logged.value) return
      for (const row of [sessions.value.find(s => s.id === id), selected.value?.id === id ? selected.value : null, sessionAction.value?.id === id ? sessionAction.value : null]) if (row) Object.assign(row, meta)
      savePreferences(); sessionAction.value = null
    } catch (e) { if (workspace === currentWorkspace && logged.value) organizationError.value = e.message }
    finally { if (workspace === currentWorkspace) { organizationBusy.value = false; resumeSessionScan() } }
  }
  async function saveGroup(name) {
    if (!groupDialog.value || organizationBusy.value) return
    const id = groupDialog.value.id, currentWorkspace = workspace
    organizationBusy.value = true; organizationError.value = ''; invalidateSessionList()
    try {
      const result = await api('/api/groups' + (id ? '/' + id : ''), { method: id ? 'PUT' : 'POST', body: JSON.stringify({ name }) })
      if (workspace !== currentWorkspace || !logged.value) return
      groups.value = result.groups; groupDialog.value = null
      if (!id) { groupFilter.value = result.id; section.value = 'all' }
    } catch (e) { if (workspace === currentWorkspace && logged.value) organizationError.value = e.message }
    finally { if (workspace === currentWorkspace) { organizationBusy.value = false; resumeSessionScan() } }
  }
  async function deleteGroup() {
    if (!groupDialog.value?.id || organizationBusy.value) return
    const id = groupDialog.value.id, currentWorkspace = workspace
    organizationBusy.value = true; organizationError.value = ''; invalidateSessionList()
    try {
      const result = await api('/api/groups/' + id, { method: 'DELETE' })
      if (workspace !== currentWorkspace || !logged.value) return
      groups.value = result.groups
      for (const row of [...sessions.value, selected.value]) if (row?.group_id === id) row.group_id = ''
      if (groupFilter.value === id) groupFilter.value = ''
      savePreferences(); groupDialog.value = null
    } catch (e) { if (workspace === currentWorkspace && logged.value) organizationError.value = e.message }
    finally { if (workspace === currentWorkspace) { organizationBusy.value = false; resumeSessionScan() } }
  }
  function forgetSession(id) {
    removedSessions.add(id)
    sessions.value = sessions.value.filter(s => s.id !== id)
    if (selected.value?.id === id) {
      resetHistoryLoading(); stopContextRefresh(); closeStream(); selectionEpoch++; selected.value = null; messages.value = []; loading.value = false; editing.value = false; previewFile.value = null
    }
    for (const rows of [drafts, runs, outgoing, uploadingIDs, sessionModels, settingsReady, settingsErrors, contextData, contextVersions, contextErrors]) delete rows[id]
    for (const key of Object.keys(controlDrafts)) if (key.startsWith(id + ':')) delete controlDrafts[key]
    workspace?.removeSession(id); savePreferences(); if (sessionAction.value?.id === id) sessionAction.value = null
  }
  async function deleteSession() {
    if (!sessionAction.value || organizationBusy.value) return
    const id = sessionAction.value.id, currentWorkspace = workspace
    organizationBusy.value = true; organizationError.value = ''; invalidateSessionList()
    try {
      await api(`/api/sessions/${id}`, { method: 'DELETE', body: JSON.stringify({ confirmed: true }) })
      if (workspace !== currentWorkspace || !logged.value) return
      forgetSession(id)
      // 删除会使上游分页位置前移，重新加载首屏以免下一页漏掉会话。
      await loadSessions(); resumeSessionScan()
    } catch (e) {
      if (workspace === currentWorkspace && logged.value) {
        organizationError.value = e.message
        const pending = typeof e.data?.deletion_pending === 'boolean' ? e.data.deletion_pending : Boolean(e.network || e.status >= 500)
        for (const row of [sessions.value.find(s => s.id === id), selected.value?.id === id ? selected.value : null]) if (row) row.deletion_pending = pending
        savePreferences()
      }
    } finally { if (workspace === currentWorkspace) { organizationBusy.value = false; resumeSessionScan() } }
  }

  async function uploadFiles(chosen) {
    if (!selected.value || composerLocked.value) return
    const id = selected.value.id, row = draft(id), currentWorkspace = workspace
    const issue = validateUploads(row.files, chosen, uploadingIDs[id] || [])
    if (issue) { view(id).notice = issue; return }
    const jobs = chosen.map(file => ({ id: randomToken(), name: file.name, size: file.size, type: file.type, state: 'queued' }))
    uploadingIDs[id] = [...(uploadingIDs[id] || []), ...jobs]
    for (const [index, file] of chosen.entries()) {
      const job = jobs[index]
      try {
        if (workspace !== currentWorkspace || !logged.value) continue
        const active = uploadingIDs[id]?.find(item => item.id === job.id); if (active) active.state = 'uploading'
        const form = new FormData(); form.append('file', file)
        const data = await api('/api/uploads', { method: 'POST', body: form })
        if (workspace !== currentWorkspace || !logged.value) continue
        const attachment = normalizeAttachment(data)
        if (!attachment) throw new Error('服务器没有返回有效的附件编号')
        row.files.push(attachment); persist(id)
      } catch (e) { if (workspace === currentWorkspace) view(id).notice = file.name + '：' + e.message }
      finally { if (workspace === currentWorkspace) uploadingIDs[id] = (uploadingIDs[id] || []).filter(item => item.id !== job.id) }
    }
  }
  async function attach(event) { const chosen = Array.from(event.target.files || []); event.target.value = ''; await uploadFiles(chosen) }
  function dropFiles(event) { const chosen = Array.from(event.dataTransfer?.files || []); if (chosen.length) uploadFiles(chosen) }
  function pasteImages(event) { const chosen = Array.from(event.clipboardData?.items || []).filter(x => x.kind === 'file' && x.type.startsWith('image/')).map(x => x.getAsFile()).filter(Boolean); if (chosen.length) { event.preventDefault(); uploadFiles(chosen) } }
  function removeImage(index) { if (!selected.value || composerLocked.value) return; draft(selected.value.id).files.splice(index, 1); persist(selected.value.id) }
  function openPreview(file) { previewFile.value = normalizeAttachment(file) }
  const loadPreviewText = id => api('/api/uploads/' + id + '/preview')

  function accepted(id, request, runID, nextStatus) {
    const row = draft(id), current = view(id)
    current.recoveryVersion = (current.recoveryVersion || 0) + 1
    if (row.pending?.request_id === request.request_id) {
      if (samePayload(request, row)) { row.text = ''; row.files = [] }
      row.pending = null; persist(id)
    }
    if (current.runID !== runID) Object.assign(current, emptyRun(), { runID })
    current.status = nextStatus || 'started'; saveCheckpoint(id)
    if (selected.value?.id === id) scheduleContextRefresh()
  }
  async function send() {
    if (!selected.value || composerLocked.value || settingsBlocked.value || uploading.value || (!text.value.trim() && !files.value.length)) return
    const id = selected.value.id, row = draft(id), existing = workspace?.draft(id).pending
    if (existing) { row.pending = existing; persist(id); await recover(id); return }
    row.pending = { text: row.text, ...attachmentPayload(row.files), settings: currentSettings.value, request_id: randomToken(), created_at: Date.now() }; persist(id)
    saveOutgoing(id, createOutgoingMessage(row.pending, row.files, messages.value)); scrollBottom()
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
      if (e.data?.code === 'submission_pending') { clearOutgoing(id, request.request_id); row.pending = null; persist(id); await recover(id) }
      else if (!e.status || e.status >= 500 || ['retry_unsafe', 'request_conflict'].includes(e.data?.code)) { view(id).status = 'submission_unknown'; view(id).notice = e.message; saveCheckpoint(id) }
      else if (e.status === 401 && e.data?.code === 'login_required') { view(id).status = 'submission_unknown' }
      else { clearOutgoing(id, request.request_id); row.pending = null; persist(id); view(id).status = 'idle'; view(id).notice = e.message }
    } finally { sendingIDs.delete(id) }
  }
  async function checkPending(id) {
    const row = draft(id), request = row.pending
    if (!request) return
    try {
      const data = await api(`/api/sessions/${id}/requests/${request.request_id}`)
      if (data.state === 'accepted') accepted(id, request, data.run_id, 'started')
      else if (data.state === 'rejected') { clearOutgoing(id, request.request_id); row.pending = null; persist(id); view(id).status = 'idle'; view(id).notice = errorText(data.response?.error) || 'Hermes 已拒绝本次请求，原草稿已保留。' }
      else if (data.state === 'acknowledged') { clearOutgoing(id, request.request_id); row.pending = null; persist(id); view(id).status = 'idle'; view(id).notice = '原请求已核对并解除等待，未重新执行。' }
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
      clearOutgoing(id, request.request_id); row.pending = null; persist(id); view(id).status = 'idle'; view(id).notice = '已解除本地等待，原草稿保留。没有停止或重新执行 Hermes 运行。'
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
    if (stream?.havenRunID === runID && stream?.havenSessionID === id) return
    closeStream(); const current = view(id)
    const es = new EventSource(`/api/sessions/${encodeURIComponent(id)}/events?run_id=${encodeURIComponent(runID)}&last_seq=${current.lastSeq}`)
    stream = es
    es.havenRunID = runID; es.havenSessionID = id
    es.onopen = () => { if (stream !== es) return; current.connected = true; current.retries = 0; health.value = 'online'; clearConnectionNotice(current) }
    es.onmessage = async event => {
      if (stream !== es || selected.value?.id !== id) return
      let data; try { data = JSON.parse(event.data) } catch { return }
      const result = applyRunEvent(current, data)
      if (result.duplicate) return
      if (result.controlsChanged) await recover(id)
      clearTimeout(checkpointTimer); checkpointTimer = setTimeout(() => saveCheckpoint(id), 150)
      if (result.terminal) {
        current.recoveryVersion = (current.recoveryVersion || 0) + 1
        closeStream(); current.connected = false; saveCheckpoint(id)
        try { await loadMessages(false); loadContext(id, true); if (current.runID === runID) { current.liveText = ''; current.tools = current.tools.filter(t => t.state === 'error'); saveCheckpoint(id) }; await loadSessions() }
        catch (e) { current.notice = '运行已结束，历史暂未恢复：' + e.message; scheduleRecover(id) }
      }
      scrollBottom()
    }
    es.onerror = () => { if (stream !== es) return; saveCheckpoint(id); closeStream(); current.connected = false; checkHealth(); scheduleRecover(id) }
  }
  async function recover(id = selected.value?.id) {
    if (!id || !logged.value || removedSessions.has(id)) return
    const current = view(id)
    const accountWorkspace = workspace
    const recoveryVersion = (current.recoveryVersion || 0) + 1; current.recoveryVersion = recoveryVersion
    current.controlsSyncing = true
    try {
      if (draft(id).pending) await checkPending(id)
      const data = await api(`/api/sessions/${id}/status`)
      if (!logged.value || accountWorkspace !== workspace || removedSessions.has(id) || recoveryVersion !== current.recoveryVersion) return
      clearConnectionNotice(current)
      if (data.status === 'submission_unknown') {
        if (!draft(id).pending) { draft(id).pending = { text: data.text || '', images: data.images || [], files: data.files || [], settings: data.settings, request_id: data.request_id, created_at: data.created_at ? Date.parse(data.created_at) : 1 }; persist(id) }
        current.status = 'submission_unknown'; current.notice = '此会话有待确认的发送，已恢复原请求。请先核对结果。'; scheduleRecover(id); return
      }
      if (draft(id).pending) { scheduleRecover(id); return }
      if (data.run_id && current.runID !== data.run_id) Object.assign(current, emptyRun(), { runID: data.run_id, recoveryVersion })
      current.status = data.status || 'idle'
      const previousControls = current.controls.map(p => p.key).join('|')
      current.controls = pendingControls(data); current.controlsVerified = true
      if (current.controls.length && previousControls !== current.controls.map(p => p.key).join('|') && selected.value?.id === id) scrollBottom()
      if (current.status === 'waiting_for_approval' && !Array.isArray(data.pending_controls)) current.notice = '当前 Hermes 未提供可核对的审批编号，请在原入口处理或停止运行。'
      if (data.error) current.notice = errorText(data.error)
      if (uncertainStatuses.has(current.status)) current.notice = 'Hermes 无法确认该运行是否完成，请核对历史；当前不会自动重新执行。'
      if (activeStatuses.has(current.status) && data.run_id && selected.value?.id === id) listen(id, data.run_id)
      else if (selected.value?.id === id && current.status !== 'idle') { await loadMessages(false); if (recoveryVersion !== current.recoveryVersion) return; current.liveText = ''; current.tools = current.tools.filter(t => t.state === 'error'); saveCheckpoint(id) }
      if (uncertainStatuses.has(current.status)) scheduleRecover(id)
      else current.retries = 0
      if (data.run_id) health.value = 'online'
    } catch (e) {
      if (!logged.value || accountWorkspace !== workspace || removedSessions.has(id) || recoveryVersion !== current.recoveryVersion) return
      current.controlsVerified = false
      if (e.status === 404) { current.controls = []; current.status = 'unavailable'; current.notice = '实时运行记录已不可用，请以保存的会话历史为准。'; return }
      current.notice = '暂时无法核对运行状态：' + e.message; health.value = 'offline'; scheduleHealthCheck(); scheduleRecover(id)
    } finally {
      if (accountWorkspace === workspace && recoveryVersion === current.recoveryVersion) {
        current.controlsSyncing = false
        if (logged.value && selected.value?.id === id && activeStatuses.has(current.status)) {
          clearTimeout(statusTimer); statusTimer = setTimeout(() => recover(id), 3000)
        }
      }
    }
  }
  function answerControl(p, answer) {
    if (!selected.value) return
    const id = selected.value.id, state = controlState(id, p)
    if (state.sending || state.unknown) return
    state.answer = answer; workspace?.saveControl(id, p.key, state)
  }
  async function respondControl(p, value) {
    if (!selected.value || controlsSyncing.value) return
    const id = selected.value.id, current = view(id), state = controlState(id, p), accountWorkspace = workspace
    if (state.sending || state.unknown || !current.controls.some(row => row.key === p.key)) return
    if (p.kind === 'approval' ? !['once', 'deny'].includes(value) || !p.choices.includes(value) : !value.trim() || [...value].length > 16384) return
    state.sending = true; state.unknown = true; state.error = ''
    // 先保存不确定标记，页面关闭或响应丢失后也不会自动再次回答。
    accountWorkspace?.saveControl(id, p.key, state)
    const body = { run_id: p.run_id, request_id: p.prompt_id, execution_generation: p.execution_generation, [p.kind === 'approval' ? 'choice' : 'answer']: value }
    try {
      const result = await api(`/api/sessions/${id}/${p.kind}`, { method: 'POST', body: JSON.stringify(body) })
      if (accountWorkspace !== workspace || !logged.value) return
      state.unknown = false; state.answer = ''; accountWorkspace?.saveControl(id, p.key, state)
      current.controls = current.controls.filter(row => row.key !== p.key)
      current.notice = result.status === 'already_resolved' ? '该请求已由其他入口处理，正在核对最新状态。' : p.kind === 'clarify' ? '补充回答已提交，Hermes 将继续原运行。' : value === 'deny' ? '已拒绝这次请求。后续结果以 Hermes 返回为准。' : '已允许这次请求，Hermes 将继续原运行。'
    } catch (e) {
      if (accountWorkspace !== workspace) return
      state.unknown = e.network || !e.status || e.status >= 500 || e.data?.code === 'control_result_unknown'
      state.error = state.unknown ? '提交结果尚未确认，请先核对状态；不会自动重发。' : e.message
      accountWorkspace?.saveControl(id, p.key, state)
      if (e.status === 409) current.notice = '该请求已处理或已过期，正在核对最新状态。'
    } finally {
      state.sending = false
      if (logged.value && accountWorkspace === workspace) await recover(id)
    }
  }
  async function refreshControls() {
    const id = selected.value?.id, accountWorkspace = workspace
    if (!id) return
    await recover(id)
    if (!logged.value || workspace !== accountWorkspace || !view(id).controlsVerified) return
    for (const p of view(id).controls) {
      const state = controlState(id, p)
      if (state.unknown && !state.sending) {
        state.unknown = false; state.error = '已核对：Hermes 仍在等待此请求，可重新选择后提交。'
        accountWorkspace?.saveControl(id, p.key, state)
      }
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
    scheduleHealthCheck()
    if (logged.value && selected.value) { const id = selected.value.id; saveCheckpoint(id); closeStream(); view(id).notice = '网络已断开，草稿和发送记录已保留。恢复连接后会重新核对运行。'; scheduleRecover(id) }
  }
  function storageChanged(event) {
    if (workspace && event.key?.startsWith(workspace.prefix + 'deleted:') && event.newValue === 'true') { forgetSession(event.key.slice((workspace.prefix + 'deleted:').length)); return }
    if (workspace && selected.value && event.key?.startsWith(workspace.prefix + 'control:' + selected.value.id + ':')) {
      const id = selected.value.id
      for (const p of view(id).controls) {
        const state = controlState(id, p)
        if (!state.sending) {
          Object.assign(state, workspace.control(id, p.key))
          if (state.unknown) state.error = '其他页面可能正在处理此请求，请先核对状态；不会自动重发。'
        }
      }
      recover(id)
    }
    if (workspace && selected.value && event.key === workspace.prefix + 'outgoing:' + selected.value.id) {
      const id = selected.value.id, incoming = workspace.outgoing(id)
      if (incoming) outgoing[id] = incoming
      recover(id)
    }
    if (workspace && selected.value && event.key === workspace.prefix + 'draft:' + selected.value.id) {
      const id = selected.value.id
      try { const incoming = JSON.parse(event.newValue); if (incoming?.pending && !draft(id).pending) draft(id).pending = incoming.pending } catch {}
      recover(id)
    }
  }
  function beforeUnload() { if (selected.value) saveCheckpoint(selected.value.id); savePreferences() }

  onMounted(async () => {
    window.addEventListener('online', online); window.addEventListener('offline', offline); window.addEventListener('storage', storageChanged); window.addEventListener('pagehide', beforeUnload)
    try { identity.value = await api('/api/me'); logged.value = true; await initialize() } catch (e) { logged.value = false; if (e.network) loginError.value = '无法连接服务器，请恢复网络后重试。' }
  })
  onUnmounted(() => { invalidateSessionList(); resetHistoryLoading(); stopContextRefresh(); beforeUnload(); closeStream(); window.removeEventListener('online', online); window.removeEventListener('offline', offline); window.removeEventListener('storage', storageChanged); window.removeEventListener('pagehide', beforeUnload) })

  return { groups, groupFilter, selectedGroup, actionBlocked, sessionAction, groupDialog, organizationBusy, organizationError, openSessionActions, openGroupDialog, organizeSession, saveGroup, deleteGroup, deleteSession, olderError, onHistoryScroll, contextUsage, contextLoading, contextError, loadContext, modelCatalog, modelsLoading, modelsError, loadModels, currentSettings, settingsLoading, settingsSaving, settingsBlocked, settingsError, saveSettings, loadSettings, newSessionOpen, newSessionSettings, newSessionError, creatingSession, openNewSession, controls, controlsSyncing, answerControl, respondControl, refreshControls, logged, username, password, identity, loginError, signingIn, sessions, filter, section, selected, messages: visibleMessages, loading, sidebar, editing, titleDraft, hasOlder, scroller, sessionList, onSessionsScroll, sessionsLoading, sessionsError, hasMoreSessions, searching, olderLoading, globalNotice, text, files, pending, status, tools, liveText, sending, uploading, uploadQueue, previewFile, openPreview, loadPreviewText, dropFiles, isBusy, isUncertain, composerLocked, mine, notice, filtered, selectedTitle, statusLabel, sourceName, sessionTitle, timeLabel, login, logout, health, healthChecking, healthError, checkHealth, loadMoreSessions, choose, createSession, startRename, rename, attach, pasteImages, removeImage, send, retryPending, acknowledgePending, refreshStatus, stop, render }
}
