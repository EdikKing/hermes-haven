<script setup>
import { computed, ref } from 'vue'
import { Brain, ChevronDown, Cpu } from 'lucide-vue-next'
import { modelCapability, normalizeSettings, reasoningLevels } from './modelSettings.js'
import ModelPicker from './ModelPicker.vue'
const props = defineProps({ modelValue: { type: Object, required: true }, catalog: { type: Object, required: true }, prefix: { type: String, required: true }, disabled: Boolean, loading: Boolean, compact: Boolean })
const emit = defineEmits(['update:modelValue'])
const pickerOpen = ref(false)
const modelLabel = computed(() => props.modelValue.model || props.catalog.model || '默认模型')
const capability = computed(() => modelCapability(props.catalog, props.modelValue))
const supportsReasoning = computed(() => capability.value.reasoning !== false)
const levels = computed(() => reasoningLevels.filter(([level]) => level !== 'none' || capability.value.can_disable_reasoning !== false))
function changeModel({ model, provider }) {
  const settings = normalizeSettings({ ...props.modelValue, model, provider })
  const caps = modelCapability(props.catalog, settings)
  if (caps.reasoning === false || (settings.reasoning_effort === 'none' && caps.can_disable_reasoning === false)) settings.reasoning_effort = ''
  emit('update:modelValue', settings); pickerOpen.value = false
}
</script>

<template>
  <div class="model-controls" :class="{compact}">
    <div class="model-field model-choice">
      <label v-if="!compact" :for="prefix+'-model'">模型</label>
      <button :id="prefix+'-model'" type="button" class="model-picker-trigger" :disabled="disabled||loading" aria-label="选择模型" aria-haspopup="dialog" :aria-expanded="pickerOpen" :title="modelLabel+(modelValue.provider?' · '+modelValue.provider:' · 跟随默认')" @click="pickerOpen=true"><Cpu :size="compact?17:16"/><span>{{ loading?'正在加载模型…':modelLabel }}</span><ChevronDown :size="13"/></button>
    </div>
    <div class="model-field effort-choice">
      <label v-if="!compact" :for="prefix+'-effort'">思考强度</label>
      <div class="model-select"><Brain v-if="compact" class="effort-icon" :size="17"/><select :id="prefix+'-effort'" aria-label="思考强度" :value="supportsReasoning?modelValue.reasoning_effort:''" :disabled="disabled||!supportsReasoning" @change="$emit('update:modelValue',{...modelValue,reasoning_effort:$event.target.value})">
        <template v-if="supportsReasoning"><option v-for="[value,label] in levels" :key="value" :value="value">{{ compact&&value===''?'默认':label }}</option></template><option v-else value="">{{ compact?'不支持':'此模型不支持思考' }}</option>
      </select><ChevronDown class="select-chevron" :size="13"/></div>
    </div>
  </div>
  <ModelPicker :open="pickerOpen" :model-value="modelValue" :catalog="catalog" :loading="loading" :disabled="disabled" @select="changeModel" @close="pickerOpen=false"/>
</template>
