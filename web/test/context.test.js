import test from 'node:test'
import assert from 'node:assert/strict'
import { contextMeter, formatTokens } from '../src/contextUsage.js'

test('上下文显示已用、模型容量和剩余量，不把累计消耗计入上下文', () => {
  const meter = contextMeter({ used_tokens: 34800, max_tokens: 1050000, remaining_tokens: 1, total_tokens: 99000000 })
  assert.equal(meter.remaining, 1015200)
  assert.equal(formatTokens(meter.used), '34.8k')
  assert.equal(formatTokens(meter.maximum), '1.1M')
  assert.equal(formatTokens(meter.remaining), '1.0M')
  assert.ok(meter.percent < 4)
})

test('容量切换、超限和缺少统计不会显示负数或虚假的零用量', () => {
  const full = contextMeter({ used_tokens: 40000, max_tokens: 32000 })
  assert.equal(full.remaining, 0); assert.equal(full.percent, 100); assert.equal(full.tone, 'full')
  assert.equal(contextMeter({ used_tokens: 880, max_tokens: 1000 }).tone, 'warning')
  assert.equal(contextMeter({ max_tokens: 1050000 }).used, null)
  assert.equal(contextMeter({ used_tokens: 500 }).remaining, null)
  assert.equal(contextMeter({ used_tokens: -1, max_tokens: 0 }).percent, null)
  assert.equal(formatTokens(null), '—'); assert.equal(formatTokens(0), '0')
  assert.equal(formatTokens(NaN), '—'); assert.equal(formatTokens(Infinity), '—')
})
