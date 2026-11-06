import test from 'node:test'
import assert from 'node:assert/strict'
import { normalizeAttachment, attachmentPayload, validateUploads, maxFileSize, maxTotalSize } from '../src/attachments.js'
import { createWorkspace, samePayload } from '../src/workspace.js'
import { groupMessages } from '../src/messages.js'

const image = { id: 'a'.repeat(40), name: '截图.png', mime: 'image/png' }
const file = { id: 'b'.repeat(40), name: '计划 中文.pdf', mime: 'application/pdf', kind: 'file', size: 123, preview_kind: 'pdf' }

test('旧图片草稿与新普通附件在相同存储版本中恢复，链接只由编号生成', () => {
  const values = new Map(), storage = { getItem: key => values.get(key), setItem: (key, value) => values.set(key, value) }
  const one = createWorkspace(storage, 'edik')
  one.saveDraft('chat', { text: '内容', files: [image, file], pending: { text: '内容', images: [image.id], files: [file.id], request_id: 'fixed' } })
  const restored = createWorkspace(storage, 'edik').draft('chat')
  assert.equal(restored.files[0].kind, 'image')
  assert.equal(restored.files[1].preview_kind, 'pdf')
  assert.equal(restored.files[1].size, 123)
  assert.equal(samePayload(restored.pending, restored), true)
  assert.equal(samePayload(restored.pending, { ...restored, files: [image] }), false)
  assert.equal(normalizeAttachment({ ...file, preview_url: 'javascript:alert(1)' }).preview_url, '/api/uploads/' + file.id + '/preview')
  assert.equal(normalizeAttachment({ ...file, id: '../secret' }), null)
})

test('混合附件发送保持图片原字段，并将普通文件分离到 files', () => {
  assert.deepEqual(attachmentPayload([file, image]), { images: [image.id], files: [file.id] })
  assert.equal(samePayload({ text: '', images: [image.id] }, { text: '', files: [image] }), true)
  assert.equal(normalizeAttachment({ ...file, mime: 'image/svg+xml', kind: undefined }).kind, 'file')
})

test('文件数量、单文件、总量和图片限制包含上传队列', () => {
  assert.equal(validateUploads([], [{ name: '边界.bin', size: maxFileSize }]), '')
  assert.match(validateUploads([], [{ name: '超限.bin', size: maxFileSize + 1 }]), /50 MiB/)
  assert.match(validateUploads([file, file], [file, file], [file]), /4 个/)
  assert.equal(validateUploads([], [{ size: maxFileSize }, { size: maxFileSize }]), '')
  assert.match(validateUploads([{ size: maxTotalSize }], [{ size: 1 }]), /100 MiB/)
  assert.match(validateUploads([], [{ name: '图片.png', type: 'image/png', size: 5 * 1024 * 1024 + 1 }]), /5 MiB/)
  assert.match(validateUploads([{ ...image, size: 4 * 1024 * 1024 }], [{ type: 'image/jpeg', size: 3 * 1024 * 1024 }]), /6 MiB/)
})

test('仅含附件或附件错误的助手消息仍有内容区，不影响工具折叠分组', () => {
  const turns = groupMessages([{ id: 1, role: 'assistant', content: '', attachments: [file] }, { id: 2, role: 'assistant', content: '', attachment_notice: '附件不可用' }])
  assert.equal(turns.length, 1)
  assert.equal(turns[0].blocks.length, 2)
  assert.equal(turns[0].blocks[0].message.attachments[0].id, file.id)
})
