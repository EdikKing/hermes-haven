export function controlKey(runID, prompt) {
  return JSON.stringify([runID, prompt.kind, prompt.prompt_id, prompt.execution_generation])
}

// 提问事件只触发核对；可操作的卡片始终来自当前运行的原生快照。
export function pendingControls(data) {
  if (!['running', 'started', 'waiting_for_approval'].includes(data.status) || !data.run_id || !Array.isArray(data.pending_controls)) return []
  const seen = new Set()
  return data.pending_controls.filter(p => {
    if (!p || !['approval', 'clarify'].includes(p.kind) || typeof p.prompt_id !== 'string' || !p.prompt_id || !Number.isSafeInteger(p.execution_generation) || p.execution_generation < 1) return false
    const key = controlKey(data.run_id, p)
    if (seen.has(key)) return false
    seen.add(key); return true
  }).map(p => ({
    ...p, key: controlKey(data.run_id, p), run_id: data.run_id,
    question: typeof p.question === 'string' ? p.question : '',
    command: typeof p.command === 'string' ? p.command : '',
    description: typeof p.description === 'string' ? p.description : '',
    choices: Array.isArray(p.choices) ? p.choices.filter(c => typeof c === 'string') : [],
  }))
}
