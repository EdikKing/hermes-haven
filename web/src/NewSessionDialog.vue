<script setup>
import { ref, watch, nextTick, onMounted, onUnmounted } from 'vue'
import { Plus, X, LoaderCircle } from 'lucide-vue-next'
import ModelSettings from './ModelSettings.vue'
const props = defineProps({ open: Boolean, settings: Object, catalog: Object, loading: Boolean, creating: Boolean, error: String, modelError: String })
const emit = defineEmits(['close', 'create', 'retry', 'update:settings'])
const panel = ref(null)
let previousFocus = null
watch(() => props.open, async open => {
  if (!open) { previousFocus?.focus?.(); return }
  previousFocus = document.activeElement
  await nextTick(); (panel.value?.querySelector('select:not(:disabled),button:not(:disabled)') || panel.value)?.focus()
})
function keydown(event) {
  if (!props.open) return
  if (event.key === 'Escape' && !props.creating) { event.preventDefault(); emit('close') }
  if (event.key !== 'Tab' || !panel.value) return
  const items = panel.value.querySelectorAll('button:not(:disabled),select:not(:disabled)'), first = items[0], last = items[items.length-1]
  if (event.shiftKey && (document.activeElement === first || document.activeElement === panel.value)) { event.preventDefault(); last?.focus() }
  else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first?.focus() }
}
onMounted(() => window.addEventListener('keydown', keydown))
onUnmounted(() => window.removeEventListener('keydown', keydown))
</script>

<template>
  <Teleport to="body"><div v-if="open" class="session-dialog-backdrop" @click.self="!creating&&$emit('close')">
    <section ref="panel" class="session-dialog" role="dialog" aria-modal="true" aria-labelledby="new-session-title" tabindex="-1">
      <header><div class="session-dialog-mark"><Plus :size="21"/></div><button class="icon-button" aria-label="关闭新建会话" :disabled="creating" @click="$emit('close')"><X :size="19"/></button></header>
      <h2 id="new-session-title">开始一段新对话</h2><p>选择模型与思考强度，之后也可以在输入框下方修改。</p>
      <form @submit.prevent="$emit('create')">
        <ModelSettings prefix="new-session" :model-value="settings" :catalog="catalog" :loading="loading" :disabled="creating" @update:model-value="$emit('update:settings',$event)"/>
        <div v-if="modelError" class="model-error" role="status">{{ modelError }} <button type="button" :disabled="loading||creating" @click="$emit('retry')">重新加载</button></div>
        <div v-if="error" class="form-error" role="alert">{{ error }}</div>
        <footer><button type="button" class="session-cancel" :disabled="creating" @click="$emit('close')">取消</button><button class="session-create" type="submit" :disabled="creating"><LoaderCircle v-if="creating" :size="15" class="spin"/><Plus v-else :size="15"/>{{ creating?'正在创建…':'创建会话' }}</button></footer>
      </form>
    </section>
  </div></Teleport>
</template>
