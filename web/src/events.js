import { errorText } from './workspace.js'

export const activeStatuses = new Set(['queued', 'running', 'waiting_for_approval', 'stopping', 'started'])
export const uncertainStatuses = new Set(['submission_unknown', 'unknown', 'owner_lost'])
const terminalEvents = new Set(['run.completed', 'run.failed', 'run.cancelled', 'run.interrupted', 'run.rejected'])

export function emptyRun() { return { runID: '', lastSeq: -1, liveText: '', tools: [], controls: [], controlsSyncing: false, status: 'idle', notice: '', retries: 0, connected: false } }

export function applyRunEvent(view, event) {
  if (Number.isInteger(event.seq)) {
    if (event.seq <= view.lastSeq) return { duplicate: true }
    view.lastSeq = event.seq
  }
  switch (event.event) {
    case 'message.delta': view.liveText += event.delta || ''; view.status = 'running'; break
    case 'run.queued': view.status = 'queued'; break
    case 'run.started': case 'run.running': view.status = 'running'; break
    case 'tool.started':
      if (!event.tool_call_id || !view.tools.some(t => t.id === event.tool_call_id)) {
        view.tools.push({ id: event.tool_call_id || 'event-' + view.lastSeq, name: event.tool || event.tool_name || '工具', preview: event.preview || '', state: 'running', result: '', error: '' })
      }
      break
    case 'tool.progress': {
      const tool = view.tools.findLast(t => event.tool_call_id ? t.id === event.tool_call_id : t.state === 'running')
      if (tool && (event.preview || event.delta)) tool.preview = event.preview || event.delta
      break
    }
    case 'tool.failed': case 'tool.completed': {
      let tool = view.tools.findLast(t => event.tool_call_id ? t.id === event.tool_call_id : t.state === 'running' && (!event.tool || t.name === event.tool))
      if (!tool) { tool = { id: event.tool_call_id || 'event-' + view.lastSeq, name: event.tool || '工具', preview: '' }; view.tools.push(tool) }
      const failed = event.event === 'tool.failed' || Boolean(event.error || event.is_error)
      tool.state = failed ? 'error' : 'done'
      tool.result = event.preview || ''
      tool.error = failed ? (typeof event.error === 'boolean' ? '' : errorText(event.error)) || event.preview || '工具执行失败，未返回详细原因' : ''
      tool.duration = Number.isFinite(event.duration) ? event.duration : null
      if (failed) view.notice = tool.name + '：' + tool.error
      break
    }
    case 'approval.request': case 'clarify.request': case 'approval.responded': case 'approval.settled': case 'clarify.settled': return { controlsChanged: true }
    case 'replay.truncated': view.notice = '部分实时过程已过期，运行结束后会从历史恢复最终消息。'; break
    case 'run.unknown': view.status = 'unknown'; view.notice = 'Hermes 无法确认该运行是否完成，请核对历史；当前不会自动重新执行。'; break
  }
  if (terminalEvents.has(event.event)) {
    view.controls = []
    view.status = event.event.slice(4)
    if (event.error) view.notice = errorText(event.error)
    return { terminal: true }
  }
  return {}
}
