export function messageText(message) {
  if (Array.isArray(message.content)) return message.content.filter(x => x.type === 'text').map(x => x.text).join('\n')
  return typeof message.content === 'string' ? message.content : ''
}

export function imageBlocks(message) {
  return Array.isArray(message.content) ? message.content.filter(x => x.type === 'image' && x.src) : []
}

export function calls(message) {
  if (Array.isArray(message.tool_calls)) return message.tool_calls.filter(call => call && typeof call === 'object')
  try {
    const parsed = JSON.parse(message.tool_calls)
    return Array.isArray(parsed) ? parsed.filter(call => call && typeof call === 'object') : []
  } catch { return [] }
}

function callArgs(call) {
  const value = call.function?.arguments ?? call.arguments ?? ''
  try { return JSON.stringify(typeof value === 'string' ? JSON.parse(value) : value, null, 2) }
  catch { return String(value) }
}

export function groupMessages(messages, live = {}) {
  const turns = []
  let current = null, toolBlock = null, callsByID = new Map()

  function resetTools() { toolBlock = null; callsByID = new Map() }
  function assistant(key, timestamp) {
    if (!current || current.role !== 'assistant') {
      current = { key, role: 'assistant', timestamp, blocks: [] }
      turns.push(current)
      resetTools()
    }
    return current
  }
  function tools(key, timestamp) {
    const turn = assistant(key, timestamp)
    if (!toolBlock) {
      toolBlock = { key: 'tools-' + key, type: 'tools', tools: [] }
      turn.blocks.push(toolBlock)
    }
    return toolBlock.tools
  }

  messages.forEach((message, index) => {
    const key = 'message-' + (message.id ?? index)
    if (message.role === 'user') {
      current = { key, role: 'user', timestamp: message.timestamp, blocks: [{ key, type: 'content', message }] }
      turns.push(current)
      resetTools()
    } else if (message.role === 'assistant') {
      if (messageText(message).trim() || imageBlocks(message).length || message.attachments?.length || message.attachment_notice) {
        assistant(key, message.timestamp).blocks.push({ key, type: 'content', message })
        resetTools()
      }
      calls(message).forEach((call, callIndex) => {
        const rows = tools(key, message.timestamp)
        const tool = { key: key + '-call-' + callIndex, id: call.id, name: call.function?.name || call.name || '工具', args: callArgs(call), results: [] }
        rows.push(tool)
        if (call.id) callsByID.set(call.id, tool)
      })
    } else if (message.role === 'tool') {
      const rows = tools(key, message.timestamp)
      // 只按调用编号配对，避免同名并行工具或分页缺失记录被错误合并。
      let tool = message.tool_call_id && callsByID.get(message.tool_call_id)
      if (!tool) {
        tool = { key, id: message.tool_call_id, name: message.tool_name || '工具结果', args: '', results: [] }
        rows.push(tool)
      }
      tool.results.push(message)
    } else {
      current = null
      resetTools()
    }
  })

  if (live.busy || live.text || live.tools?.length) {
    const turn = assistant('live')
    turn.live = true
    if (live.text) turn.blocks.push({ key: 'live-text', type: 'content', message: { content: live.text } })
    if (live.tools?.length) turn.blocks.push({ key: 'live-tools', type: 'tools', live: true, tools: live.tools })
    else if (live.busy && !live.text) turn.blocks.push({ key: 'thinking', type: 'thinking' })
  }
  return turns
}
