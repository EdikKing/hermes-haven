<script setup>
import { computed } from 'vue'
import { Check, ChevronDown, CircleAlert, LoaderCircle, Wrench } from 'lucide-vue-next'
import { imageBlocks, messageText } from './messages.js'

const props = defineProps({ tools: { type: Array, required: true }, live: Boolean })
const failures = computed(() => props.tools.filter(tool => tool.state === 'error').length)
const running = computed(() => props.tools.filter(tool => tool.state === 'running').length)
</script>

<template>
  <details class="tool-group" :class="{'has-errors':failures}" :open="live || failures > 0">
    <summary class="tool-group-summary">
      <Wrench :size="14"/>
      <span class="tool-group-title">工具调用</span>
      <small>{{ tools.length }} 项<span v-if="running"> · {{ running }} 项执行中</span><span v-if="failures"> · {{ failures }} 项失败</span></small>
      <ChevronDown class="tool-chevron" :size="14"/>
    </summary>
    <div class="tool-group-items">
      <details v-for="tool in tools" :key="tool.key || tool.id" class="tool-row" :class="{'tool-error':tool.state==='error'}" :open="tool.state==='error'">
        <summary>
          <LoaderCircle v-if="tool.state==='running'" class="spin" :size="13"/>
          <Check v-else-if="tool.state==='done'" :size="13"/>
          <CircleAlert v-else-if="tool.state==='error'" :size="13"/>
          <Wrench v-else :size="13"/>
          <span class="tool-name" :title="tool.name">{{ tool.name }}</span>
          <small v-if="live">{{ tool.state==='running'?'执行中':tool.state==='error'?'失败':'已完成' }}{{ tool.duration!=null?' · '+tool.duration+' 秒':'' }}</small>
          <small v-else>{{ tool.results.length ? '已返回' : '调用' }}</small>
          <ChevronDown class="tool-chevron" :size="13"/>
        </summary>
        <div class="tool-details">
          <template v-if="tool.args"><div class="tool-detail-label">调用参数</div><pre>{{ tool.args }}</pre></template>
          <template v-for="(result,index) in tool.results" :key="result.id ?? index">
            <div class="tool-detail-label">执行结果</div>
            <pre>{{ messageText(result) }}</pre>
            <div v-if="imageBlocks(result).length" class="message-images"><img v-for="img in imageBlocks(result)" :key="img.src" :src="img.src" :alt="img.name||'图片'"></div>
          </template>
          <div v-if="live && tool.preview && tool.preview !== tool.result && tool.preview !== tool.error" class="tool-preview">{{ tool.preview }}</div>
          <pre v-if="tool.error || tool.result">{{ tool.error || tool.result }}</pre>
          <div v-if="live && tool.result" class="tool-preview">实时结果摘要，完整记录以保存的历史为准</div>
        </div>
      </details>
    </div>
  </details>
</template>
