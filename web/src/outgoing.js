function messageKey(message) {
  return message.id == null ? JSON.stringify([message.role, message.timestamp, message.content]) : String(message.id)
}

export function createOutgoingMessage(request, files, history) {
  const content = []
  if (request.text) content.push({ type: 'text', text: request.text })
  for (const id of request.images || []) {
    const image = files.find(file => file.id === id)
    content.push({ type: 'image', src: '/api/uploads/' + id, name: image?.name || '图片' })
  }
  return {
    requestID: request.request_id,
    before: history.slice(-100).map(messageKey),
    message: {
      id: 'outgoing-' + request.request_id,
      role: 'user',
      timestamp: Math.floor(request.created_at / 1000),
      content,
      attachments: files.filter(file => (request.files || []).includes(file.id)),
    },
  }
}

function boundary(history, outgoing) {
  if (!outgoing.before.length) return -1
  const known = new Set(outgoing.before)
  return history.findLastIndex(message => known.has(messageKey(message)))
}

export function reconcileOutgoing(history, outgoing) {
  if (!outgoing) return null
  const index = boundary(history, outgoing)
  // 按发送前的历史边界识别新记录，上一轮相同文本不能顶替本次消息。
  if (outgoing.before.length && index < 0) return outgoing
  return history.slice(index + 1).some(message => message.role === 'user') ? null : outgoing
}

export function withOutgoingMessage(history, outgoing) {
  if (!reconcileOutgoing(history, outgoing)) return history
  const index = boundary(history, outgoing)
  const position = index + 1
  return [...history.slice(0, position), outgoing.message, ...history.slice(position)]
}
