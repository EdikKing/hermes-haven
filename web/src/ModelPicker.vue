<script setup>
import { computed, nextTick, ref, watch } from 'vue'
import { Check, ChevronRight, Search, X } from 'lucide-vue-next'
import { modelKey } from './modelSettings.js'
const props = defineProps({ open: Boolean, modelValue: Object, catalog: { type: Object, required: true }, loading: Boolean, disabled: Boolean })
const emit = defineEmits(['close', 'select'])
const query = ref(''), providerID = ref(''), panel = ref(null), search = ref(null)
let previousFocus = null
const providers = computed(() => (props.catalog.providers || []).map(provider => {
  const term = query.value.trim().toLowerCase(), match = (provider.name + ' ' + provider.slug).toLowerCase().includes(term)
  return { ...provider, models: [...new Set(provider.models || [])].filter(model => !term || match || model.toLowerCase().includes(term)) }
}).filter(provider => provider.models.length))
const active = computed(() => providers.value.find(provider => provider.slug === providerID.value) || providers.value[0])
watch(providers, rows => { if (!rows.some(provider => provider.slug === providerID.value)) providerID.value = rows[0]?.slug || '' })
watch(() => props.open, async open => {
  if (!open) { previousFocus?.focus?.(); return }
  previousFocus = document.activeElement; query.value = ''; providerID.value = props.modelValue?.provider || props.catalog.provider || props.catalog.providers?.[0]?.slug || ''
  await nextTick(); search.value?.focus()
})
function select(provider, model) { if (!props.disabled) emit('select', { provider, model }) }
function keydown(event) {
  event.stopPropagation()
  if (event.key === 'Escape') { event.preventDefault(); emit('close'); return }
  if (event.key !== 'Tab') return
  const items = panel.value.querySelectorAll('input,button:not(:disabled)'), first = items[0], last = items[items.length-1]
  if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last?.focus() }
  else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first?.focus() }
}
function providerKey(event, index) {
  if (!['ArrowUp', 'ArrowDown', 'ArrowRight'].includes(event.key)) return
  event.preventDefault()
  if (event.key === 'ArrowRight') { panel.value.querySelector('.picker-model-row')?.focus(); return }
  const next = (index + (event.key === 'ArrowDown' ? 1 : -1) + providers.value.length) % providers.value.length
  providerID.value = providers.value[next].slug
  panel.value.querySelectorAll('.picker-provider-row')[next]?.focus()
}
</script>

<template>
  <Teleport to="body"><div v-if="open" class="model-picker-backdrop" @click.self="$emit('close')" @keydown="keydown">
    <section ref="panel" class="model-picker" role="dialog" aria-modal="true" aria-label="选择模型">
      <header class="picker-search"><Search :size="19"/><input ref="search" v-model="query" aria-label="搜索模型" placeholder="搜索模型或服务商…"><button type="button" class="icon-button" aria-label="关闭模型选择" @click="$emit('close')"><X :size="20"/></button></header>
      <div class="picker-columns">
        <div class="picker-providers" role="tablist" aria-label="服务商" aria-orientation="vertical"><div class="picker-column-title">服务商</div><button v-for="(provider,index) in providers" :key="provider.slug" type="button" class="picker-provider-row" :class="{active:active?.slug===provider.slug}" role="tab" :aria-selected="active?.slug===provider.slug" @click="providerID=provider.slug" @keydown="providerKey($event,index)"><span>{{ provider.name||provider.slug }}</span><small>{{ provider.models.length }}</small><ChevronRight :size="14"/></button></div>
        <div class="picker-models" role="tabpanel" :aria-label="active?.name||'模型'"><div class="picker-column-title">{{ active?.name||'模型' }}</div><div v-if="loading" class="picker-empty">正在加载模型…</div><div v-else-if="!active" class="picker-empty">{{ query?'没有匹配的模型':'暂无可选模型' }}</div><template v-else><button v-for="model in active.models" :key="model" type="button" class="picker-model-row" :class="{selected:modelKey(active.slug,model)===modelKey(modelValue?.provider,modelValue?.model)}" :disabled="disabled" :aria-pressed="modelKey(active.slug,model)===modelKey(modelValue?.provider,modelValue?.model)" @click="select(active.slug,model)"><span>{{ model }}</span><Check v-if="modelKey(active.slug,model)===modelKey(modelValue?.provider,modelValue?.model)" :size="18"/></button></template></div>
      </div>
      <footer><span>模型选择按会话保存</span><button type="button" :disabled="disabled" @click="select('','')">使用默认模型<Check v-if="!modelValue?.model" :size="14"/></button></footer>
    </section>
  </div></Teleport>
</template>
