import test from 'node:test'
import assert from 'node:assert/strict'
import { groupMessages, messageText, imageBlocks } from '../src/messages.js'

const call = (id, name = 'terminal', args = '{}') => ({ id, function: { name, arguments: args } })

test('一轮多次工具往返与最终回复共用作者，参数和结果按编号合并且不改写历史', () => {
  const messages = [{ id: 1, role: 'user', content: '整理笔记' }]
  for (let i = 0; i < 4; i++) {
    messages.push({ id: i * 3 + 2, role: 'assistant', timestamp: 123, content: '', tool_calls: [call('read-' + i, 'read_file', '{"path":"note.md"}'), call('shell-' + i)] })
    messages.push({ id: i * 3 + 3, role: 'tool', tool_call_id: 'read-' + i, content: '笔记内容 ' + i })
    messages.push({ id: i * 3 + 4, role: 'tool', tool_call_id: 'shell-' + i, content: '执行成功 ' + i })
  }
  messages.push({ id: 14, role: 'assistant', content: '整理完成' })
  const original = structuredClone(messages), turns = groupMessages(messages)
  assert.deepEqual(turns.map(turn => turn.role), ['user', 'assistant'])
  const blocks = turns[1].blocks
  assert.equal(blocks.length, 2)
  assert.equal(blocks[0].tools.length, 8)
  assert.equal(blocks[0].tools[0].args, '{\n  "path": "note.md"\n}')
  assert.equal(messageText(blocks[0].tools[7].results[0]), '执行成功 3')
  assert.equal(messageText(blocks[1].message), '整理完成')
  assert.deepEqual(messages, original)
})

test('同名并行工具的乱序结果按编号配对，不同用户轮次之间不会串联', () => {
  const turns = groupMessages([
    { id: 1, role: 'assistant', tool_calls: [call('one'), call('two')] },
    { id: 2, role: 'tool', tool_call_id: 'two', content: '第二个结果' },
    { id: 3, role: 'tool', tool_call_id: 'one', content: '第一个结果' },
    { id: 4, role: 'user', content: '继续' },
    { id: 5, role: 'tool', tool_call_id: 'one', content: '另一轮结果' },
  ])
  assert.deepEqual(turns.map(turn => turn.role), ['assistant', 'user', 'assistant'])
  assert.deepEqual(turns[0].blocks[0].tools.map(tool => messageText(tool.results[0])), ['第一个结果', '第二个结果'])
  assert.equal(turns[2].blocks[0].tools[0].args, '')
  assert.equal(messageText(turns[2].blocks[0].tools[0].results[0]), '另一轮结果')
})

test('分页从工具结果中间开始时保留孤立结果，加载前页后可恢复配对', () => {
  const result = { id: 2, role: 'tool', tool_call_id: 'old', tool_name: 'read_file', content: '完整内容' }
  const recent = [result, { id: 3, role: 'assistant', content: '完成' }]
  assert.equal(groupMessages(recent)[0].blocks[0].tools[0].name, 'read_file')
  const turns = groupMessages([{ id: 1, role: 'assistant', tool_calls: JSON.stringify([call('old', 'read_file')]) }, ...recent])
  assert.equal(turns.length, 1)
  assert.equal(turns[0].blocks[0].tools.length, 1)
  assert.equal(turns[0].blocks[0].tools[0].results[0], result)
})

test('正文、图片与工具保持原顺序，无编号结果不按同名工具猜测合并', () => {
  const image = { type: 'image', src: '/api/images/one', name: '截图' }
  const turns = groupMessages([
    { id: 1, role: 'assistant', content: '先检查', tool_calls: [call('one')] },
    { id: 2, role: 'tool', tool_name: 'terminal', content: [{ type: 'text', text: '无编号结果' }, image] },
    { id: 3, role: 'assistant', content: [image] },
    { id: 4, role: 'assistant', tool_calls: [call('two')] },
    { id: 5, role: 'assistant', content: '最后说明' },
  ])
  const blocks = turns[0].blocks
  assert.deepEqual(blocks.map(block => block.type), ['content', 'tools', 'content', 'tools', 'content'])
  assert.equal(blocks[1].tools.length, 2)
  assert.equal(messageText(blocks[1].tools[1].results[0]), '无编号结果')
  assert.deepEqual(imageBlocks(blocks[2].message), [image])
})

test('流式回复接续当前助手轮次，执行中与失败工具的状态和摘要完整保留', () => {
  const tools = [{ id: 'live-call', name: 'read_file', state: 'error', error: '文件不存在', duration: 1.25 }]
  const turns = groupMessages([{ id: 1, role: 'user', content: '检查' }, { id: 2, role: 'assistant', content: '开始检查' }], { busy: true, text: '正在读取', tools })
  assert.equal(turns.length, 2)
  assert.equal(turns[1].live, true)
  assert.equal(messageText(turns[1].blocks[1].message), '正在读取')
  assert.equal(turns[1].blocks[2].tools[0], tools[0])
  assert.equal(turns[1].blocks[2].live, true)
  assert.equal(groupMessages([], { busy: true })[0].blocks[0].type, 'thinking')
})

test('空助手记录和损坏的调用数据不产生空头像，隐藏消息作为轮次边界', () => {
  assert.deepEqual(groupMessages([{ role: 'assistant', content: ' ', tool_calls: '{bad' }, { role: 'assistant', tool_calls: '{"name":"terminal"}' }, { role: 'assistant', tool_calls: '[null,1]' }]), [])
  const turns = groupMessages([{ id: 1, role: 'assistant', content: '前一轮' }, { role: 'system', content: '内部边界' }, { id: 2, role: 'assistant', content: '后一轮' }])
  assert.equal(turns.length, 2)
})
