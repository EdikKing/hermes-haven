<script setup>
import { computed } from 'vue'
import { LoaderCircle } from 'lucide-vue-next'
import { contextMeter, formatTokens } from './contextUsage.js'
const props = defineProps({ usage: Object, loading: Boolean, error: String })
const meter = computed(() => contextMeter(props.usage))
const detail = computed(() => {
  const { used, maximum, remaining } = meter.value
  if (used === null) return props.error || '首轮回复后显示上下文用量；数值供参考。'
  return `已用 ${used.toLocaleString('zh-CN')} tokens${maximum===null?'':`，总容量 ${maximum.toLocaleString('zh-CN')}，剩余 ${remaining.toLocaleString('zh-CN')}`}。数值供参考。${props.error?' 暂未取得最新数据。':''}`
})
</script>

<template>
  <div class="context-meter" :class="meter.tone" :title="detail" aria-label="上下文用量" role="status">
    <span class="context-numbers"><span>{{ formatTokens(meter.used) }}</span><span class="context-slash">/</span><span>{{ meter.maximum===null?'容量未知':formatTokens(meter.maximum) }}</span><span class="context-separator">·</span><strong>{{ meter.remaining===null?'剩余量待统计':'剩余 '+formatTokens(meter.remaining) }}</strong></span>
    <div v-if="meter.percent!==null" class="context-track" role="progressbar" aria-label="上下文已用比例" :aria-valuenow="Math.round(meter.percent)" aria-valuemin="0" aria-valuemax="100"><span :style="{width:meter.percent+'%'}"></span></div>
    <LoaderCircle v-if="loading" :size="12" class="spin context-loading"/>
  </div>
</template>
