<script setup>
import { File, FileImage, Download, Eye } from 'lucide-vue-next'
import { normalizeAttachment, formatSize } from './attachments.js'
import { computed } from 'vue'
const props = defineProps({ files: { type: Array, default: () => [] } })
defineEmits(['preview'])
const rows = computed(() => props.files.map(normalizeAttachment).filter(Boolean))
</script>

<template>
  <div v-if="rows.length" class="file-cards">
    <div v-for="file in rows" :key="file.id" class="file-card">
      <div class="file-card-icon"><FileImage v-if="file.kind==='image'" :size="20"/><File v-else :size="20"/></div>
      <div class="file-card-info"><strong :title="file.name">{{ file.name }}</strong><small>{{ formatSize(file.size) }}<span v-if="file.preview_kind==='none'"> · 暂不支持预览</span></small></div>
      <button v-if="file.preview_kind!=='none'" class="icon-button" :aria-label="'预览 '+file.name" @click="$emit('preview',file)"><Eye :size="16"/></button>
      <a class="icon-button" :href="file.download_url" :download="file.name" :aria-label="'下载 '+file.name"><Download :size="16"/></a>
    </div>
  </div>
</template>
