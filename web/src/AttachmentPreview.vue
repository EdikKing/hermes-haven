<script setup>
import { ref, watch, nextTick, onMounted, onUnmounted } from 'vue'
import { X, Download, LoaderCircle } from 'lucide-vue-next'
import { formatSize } from './attachments.js'
const props = defineProps({ file: Object, loadText: { type: Function, required: true } })
const emit = defineEmits(['close'])
const panel = ref(null), text = ref(''), error = ref(''), loading = ref(false), truncated = ref(false), encoding = ref('')
const pdfUnavailable = ref(typeof navigator !== 'undefined' && navigator.pdfViewerEnabled === false)
let epoch = 0, previousFocus = null
watch(() => props.file, async file => {
  const current = ++epoch
  text.value = ''; error.value = ''; truncated.value = false; loading.value = false
  if (!file) { previousFocus?.focus?.(); return }
  previousFocus = document.activeElement
  await nextTick(); panel.value?.focus()
  if (file.preview_kind !== 'text') return
  loading.value = true
  try { const result = await props.loadText(file.id); if (current === epoch) { text.value = result.text; truncated.value = result.truncated; encoding.value = result.encoding } }
  catch (e) { if (current === epoch) error.value = e.message }
  finally { if (current === epoch) loading.value = false }
})
function keydown(event) {
  if (!props.file) return
  if (event.key === 'Escape') { event.preventDefault(); emit('close') }
  if (event.key === 'Tab' && panel.value) {
    const items = panel.value.querySelectorAll('button,a[href],iframe'), first = items[0], last = items[items.length - 1]
    if (event.shiftKey && (document.activeElement === first || document.activeElement === panel.value)) { event.preventDefault(); last?.focus() }
    else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first?.focus() }
  }
}
onMounted(() => window.addEventListener('keydown', keydown))
onUnmounted(() => window.removeEventListener('keydown', keydown))
</script>

<template>
  <Teleport to="body"><div v-if="file" class="preview-backdrop" @click.self="$emit('close')">
    <section ref="panel" class="file-preview" role="dialog" aria-modal="true" aria-label="附件预览" tabindex="-1">
      <header><div><strong>{{ file.name }}</strong><small>{{ formatSize(file.size) }}</small></div><a :href="file.download_url" :download="file.name" class="preview-download"><Download :size="15"/> 下载</a><button class="icon-button" aria-label="关闭预览" @click="$emit('close')"><X :size="20"/></button></header>
      <div v-if="loading" class="preview-status"><LoaderCircle class="spin" :size="18"/> 正在读取预览…</div>
      <div v-else-if="error" class="preview-status" role="alert">{{ error }}</div>
      <template v-else-if="file.preview_kind==='text'"><div class="preview-note">{{ encoding }}<span v-if="truncated"> · 内容已截断，仅显示前 1 MiB；下载可查看完整文件。</span></div><pre class="text-preview">{{ text }}</pre></template>
      <div v-else-if="file.preview_kind==='image'" class="image-preview"><img :src="file.preview_url" :alt="file.name" @error="error='图片暂不可预览，请重新登录或下载查看。'"></div>
      <template v-else-if="file.preview_kind==='pdf'"><div class="preview-note">如浏览器无法显示 PDF，请点击下载查看。</div><div v-if="pdfUnavailable" class="preview-status">当前浏览器未启用原生 PDF 预览，请下载查看。</div><iframe v-else class="pdf-preview" :title="file.name" :src="file.preview_url" referrerpolicy="no-referrer" @error="pdfUnavailable=true"></iframe></template>
      <div v-else class="preview-status">此格式暂不支持预览，请下载查看。</div>
    </section>
  </div></Teleport>
</template>
