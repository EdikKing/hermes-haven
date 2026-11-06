const valid = value => typeof value === 'number' && Number.isFinite(value) && value >= 0 && value < 1e12
export function formatTokens(value) {
  if (!valid(value)) return '—'
  if (value < 1000) return String(Math.round(value))
  const unit = value >= 1e6 ? 1e6 : 1000
  const number = (value / unit).toFixed(1)
  return unit === 1e6 ? number + 'M' : number.replace(/\.0$/, '') + 'k'
}
export function contextMeter(value) {
  const used = valid(value?.used_tokens) ? Math.round(value.used_tokens) : null
  const maximum = valid(value?.max_tokens) && value.max_tokens > 0 ? Math.round(value.max_tokens) : null
  const remaining = used !== null && maximum !== null ? Math.max(0, maximum - used) : null
  const percent = used !== null && maximum !== null ? Math.min(100, used / maximum * 100) : null
  return { used, maximum, remaining, percent, tone: used !== null && maximum !== null && used >= maximum ? 'full' : percent >= 85 ? 'warning' : 'normal' }
}
