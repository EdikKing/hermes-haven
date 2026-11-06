import { normalizeAttachment, attachmentPayload } from './attachments.js'

export function emptyDraft() { return { text: '', files: [], pending: null } }

export function createWorkspace(storage, username, onError = () => {}) {
  const prefix = 'hermes-haven:v2:' + encodeURIComponent(username) + ':'
  function read(key, fallback) {
    try { const value = storage.getItem(prefix + key); return value ? JSON.parse(value) : fallback }
    catch { onError('浏览器存储不可用，刷新前请保留草稿并核对发送状态。'); return fallback }
  }
  function write(key, value) {
    try { storage.setItem(prefix + key, JSON.stringify(value)); return true }
    catch { onError('草稿暂未保存到浏览器，请勿关闭页面；发送记录仍会由服务器核对。'); return false }
  }
  return {
    prefix,
    preferences: () => read('preferences', {}),
    savePreferences: value => write('preferences', value),
    outgoing(id) {
      const value = read('outgoing:' + id, null)
      return value && typeof value.requestID === 'string' && Array.isArray(value.before) && value.message?.role === 'user' ? value : null
    },
    saveOutgoing: (id, value) => write('outgoing:' + id, value),
    control(id, key) {
      const value = read('control:' + id + ':' + key, {})
      return { answer: typeof value?.answer === 'string' ? value.answer : '', unknown: value?.unknown === true }
    },
    saveControl: (id, key, value) => write('control:' + id + ':' + key, { answer: value.answer, unknown: value.unknown }),
    draft(id) {
      const value = read('draft:' + id, null)
      if (!value || typeof value.text !== 'string' || !Array.isArray(value.files)) return emptyDraft()
      return {
        text: value.text,
        files: value.files.map(normalizeAttachment).filter(Boolean).slice(0, 4),
        pending: value.pending && typeof value.pending.request_id === 'string' ? value.pending : null,
      }
    },
    saveDraft: (id, value) => write('draft:' + id, { text: value.text, files: value.files.map(({ id, name, mime, size, kind, preview_kind }) => ({ id, name, mime, size, kind, preview_kind })), pending: value.pending }),
    checkpoint(id) { return read('run:' + id, null) },
    saveCheckpoint(id, view) {
      // 序号和对应内容一起保存，避免刷新后跳过尚未恢复的文字和工具事件。
      return write('run:' + id, { runID: view.runID, lastSeq: view.lastSeq, liveText: view.liveText, tools: view.tools, status: view.status })
    },
    clearCheckpoint(id) { try { storage.removeItem(prefix + 'run:' + id) } catch {} },
  }
}

export function samePayload(pending, draft) {
  const payload = attachmentPayload(draft.files)
  return pending?.text === draft.text && JSON.stringify(pending?.images || []) === JSON.stringify(payload.images) && JSON.stringify(pending?.files || []) === JSON.stringify(payload.files)
}

export function mergeMessages(older, newer) {
  const seen = new Set()
  return [...older, ...newer].filter(row => {
    if (row.id == null) return true
    const key = String(row.id)
    if (seen.has(key)) return false
    seen.add(key)
    return true
  })
}

export function errorText(value) {
  if (typeof value === 'string') return value
  if (value && typeof value.message === 'string') return value.message
  if (value == null || value === false) return ''
  if (value === true) return '运行失败，未返回详细原因'
  try { return JSON.stringify(value) } catch { return String(value) }
}
