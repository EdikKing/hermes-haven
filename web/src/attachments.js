const imageTypes = new Set(['image/png', 'image/jpeg', 'image/gif', 'image/webp'])
export const maxFileSize = 50 * 1024 * 1024
export const maxTotalSize = 100 * 1024 * 1024

export function fileKind(file) { return file.kind || (imageTypes.has(file.mime) ? 'image' : 'file') }
export function normalizeAttachment(file) {
  if (!file || !/^[a-f0-9]{40}$/.test(file.id) || typeof file.name !== 'string') return null
  const kind = fileKind(file)
  return { id: file.id, name: file.name, mime: file.mime, size: Number.isFinite(file.size) ? file.size : null, kind,
    preview_kind: file.preview_kind || (kind === 'image' ? 'image' : 'none'), preview: '/api/uploads/' + file.id,
    preview_url: '/api/uploads/' + file.id + '/preview', download_url: '/api/uploads/' + file.id + '/download' }
}
export function attachmentPayload(files) {
  return { images: files.filter(f => fileKind(f) === 'image').map(f => f.id), files: files.filter(f => fileKind(f) !== 'image').map(f => f.id) }
}
export function validateUploads(existing, selected, queued = []) {
  if (existing.length + selected.length + queued.length > 4) return '每条消息最多添加 4 个附件'
  for (const file of selected) {
    if (file.size > maxFileSize) return file.name + ' 超过 50 MiB'
    if (imageTypes.has(file.type) && file.size > 5 * 1024 * 1024) return file.name + ' 超过图片的 5 MiB 限制'
  }
  if ([...existing, ...selected, ...queued].reduce((sum, f) => sum + (f.size || 0), 0) > maxTotalSize) return '附件总大小不能超过 100 MiB'
  const pictures = [...existing.filter(f => fileKind(f) === 'image'), ...selected.filter(f => imageTypes.has(f.type)), ...queued.filter(f => imageTypes.has(f.type))]
  if (pictures.reduce((sum, f) => sum + (f.size || 0), 0) > 6 * 1024 * 1024) return '图片总大小不能超过 6 MiB'
  return ''
}
export function formatSize(size) {
  if (!Number.isFinite(size)) return ''
  if (size < 1024) return size + ' B'
  if (size < 1024 * 1024) return (size / 1024).toFixed(1) + ' KiB'
  return (size / (1024 * 1024)).toFixed(1) + ' MiB'
}
