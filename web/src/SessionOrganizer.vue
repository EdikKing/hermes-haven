<script setup>
import { computed, ref, watch, nextTick, onMounted, onUnmounted } from 'vue'
import { Archive, ArchiveRestore, Folder, LoaderCircle, Trash2, X } from 'lucide-vue-next'
const props = defineProps({ session: Object, group: Object, groups: Array, title: String, busy: Boolean, error: String, blocked: String })
const emit = defineEmits(['close', 'organize', 'save-group', 'delete-group', 'delete-session'])
const panel = ref(null), groupID = ref(''), name = ref(''), confirming = ref(false)
const open = computed(() => Boolean(props.session || props.group))
let previousFocus = null
watch(() => [props.session?.id, props.group?.id, open.value], async () => {
  if (!open.value) { previousFocus?.focus?.(); return }
  previousFocus = document.activeElement
  groupID.value = props.session?.group_id || ''; name.value = props.group?.name || ''; confirming.value = Boolean(props.session?.deletion_pending)
  await nextTick(); (panel.value?.querySelector('input,select,button') || panel.value)?.focus()
})
function close() { if (!props.busy) emit('close') }
function keydown(event) {
  if (!open.value) return
  if (event.key === 'Escape') { event.preventDefault(); close() }
  if (event.key !== 'Tab' || !panel.value) return
  const items = panel.value.querySelectorAll('button:not(:disabled),input:not(:disabled),select:not(:disabled)'), first = items[0], last = items[items.length - 1]
  if (event.shiftKey && (document.activeElement === first || document.activeElement === panel.value)) { event.preventDefault(); last?.focus() }
  else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first?.focus() }
}
onMounted(() => window.addEventListener('keydown', keydown))
onUnmounted(() => window.removeEventListener('keydown', keydown))
</script>

<template>
  <Teleport to="body"><div v-if="open" class="session-dialog-backdrop" @click.self="close">
    <section ref="panel" class="session-dialog organizer-dialog" role="dialog" aria-modal="true" aria-labelledby="organizer-title" tabindex="-1" :aria-busy="busy">
      <header><div class="session-dialog-mark"><Folder :size="21"/></div><button class="icon-button" aria-label="关闭会话管理" :disabled="busy" @click="close"><X :size="19"/></button></header>
      <template v-if="session">
        <h2 id="organizer-title">{{ confirming?'彻底删除会话？':'管理会话' }}</h2>
        <p class="organizer-subtitle">{{ title }}</p>
        <template v-if="!confirming">
          <form class="organizer-group-form" @submit.prevent="$emit('organize',{group_id:groupID})">
            <label for="session-group">项目分组</label>
            <div><select id="session-group" v-model="groupID" :disabled="busy"><option value="">未分组</option><option v-for="g in groups" :key="g.id" :value="g.id">{{ g.name }}</option></select><button class="organizer-save" :disabled="busy||groupID===(session.group_id||'')">保存分组</button></div>
          </form>
          <button class="organizer-action" :disabled="busy" @click="$emit('organize',{archived:!session.archived})"><ArchiveRestore v-if="session.archived" :size="18"/><Archive v-else :size="18"/><span><strong>{{ session.archived?'恢复会话':'归档会话' }}</strong><small>{{ session.archived?'移回会话列表，继续对话':'保留完整内容，可在「归档」中查看和恢复' }}</small></span></button>
          <button class="organizer-action danger-text" :disabled="busy" @click="confirming=true"><Trash2 :size="18"/><span><strong>彻底删除</strong><small>删除会话及消息，无法恢复</small></span></button>
        </template>
        <template v-else>
          <div class="delete-explanation"><Trash2 :size="20"/><p>会话和消息将从 Hermes 及 Haven 中永久删除，不能在归档中找回。<br>只想收起会话，可以选择归档。</p></div>
          <p v-if="blocked" class="organizer-blocked" role="status">{{ blocked }}</p>
          <footer><button type="button" :disabled="busy" @click="confirming=false">返回</button><button class="danger-button" :disabled="busy||Boolean(blocked)" @click="$emit('delete-session')"><LoaderCircle v-if="busy" :size="15" class="spin"/>{{ busy?'正在删除…':'确认彻底删除' }}</button></footer>
        </template>
      </template>
      <template v-else>
        <h2 id="organizer-title">{{ confirming?'删除分组？':group.id?'编辑分组':'新建项目分组' }}</h2>
        <template v-if="!confirming">
          <p>将同一项目的会话放在一起，方便查找。</p>
          <form class="organizer-group-form" @submit.prevent="$emit('save-group',name.trim())"><label for="group-name">分组名称</label><input id="group-name" v-model="name" maxlength="40" required :disabled="busy" placeholder="例如：网站改版"><footer><button v-if="group.id" type="button" class="danger-text" :disabled="busy" @click="confirming=true">删除分组</button><button type="button" :disabled="busy" @click="close">取消</button><button class="session-create" :disabled="busy||!name.trim()"><LoaderCircle v-if="busy" :size="15" class="spin"/>{{ busy?'正在保存…':'保存分组' }}</button></footer></form>
        </template>
        <template v-else><p>删除「{{ group.name }}」后，组内会话会移回未分组，归档状态保留。</p><footer><button :disabled="busy" @click="confirming=false">返回</button><button class="danger-button" :disabled="busy" @click="$emit('delete-group')">{{ busy?'正在删除…':'确认删除分组' }}</button></footer></template>
      </template>
      <p v-if="error" class="organizer-error" role="alert">{{ error }}</p>
    </section>
  </div></Teleport>
</template>
