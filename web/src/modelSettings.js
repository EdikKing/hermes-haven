export const reasoningLevels = [
  ['', '默认强度'], ['none', '关闭思考'], ['minimal', '最低'], ['low', '低'],
  ['medium', '中'], ['high', '高'], ['xhigh', '很高'], ['max', '最高'], ['ultra', '极致'],
]

export function normalizeSettings(value) {
  return {
    model: typeof value?.model === 'string' ? value.model : '',
    provider: typeof value?.model === 'string' && value.model && typeof value?.provider === 'string' ? value.provider : '',
    reasoning_effort: reasoningLevels.some(([level]) => level === value?.reasoning_effort) ? value.reasoning_effort : '',
  }
}

export function modelKey(provider, model) { return model ? JSON.stringify([provider || '', model]) : '' }

export function modelCapability(catalog, settings) {
  const provider = settings.model ? settings.provider : catalog.provider
  const model = settings.model || catalog.model
  return catalog.providers?.find(row => row.slug === provider)?.capabilities?.[model] || {}
}
