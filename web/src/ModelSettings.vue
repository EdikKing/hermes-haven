<script setup>
import { computed } from 'vue'
import { ChevronDown } from 'lucide-vue-next'
import { modelKey, modelCapability, normalizeSettings, reasoningLevels } from './modelSettings.js'
const props = defineProps({ modelValue: { type: Object, required: true }, catalog: { type: Object, required: true }, prefix: { type: String, required: true }, disabled: Boolean, loading: Boolean, compact: Boolean })
const emit = defineEmits(['update:modelValue'])
const selectedKey = computed(() => modelKey(props.modelValue.provider, props.modelValue.model))
const knownModel = computed(() => !selectedKey.value || props.catalog.providers?.some(provider => provider.models.some(model => modelKey(provider.slug, model) === selectedKey.value)))
const capability = computed(() => modelCapability(props.catalog, props.modelValue))
const supportsReasoning = computed(() => capability.value.reasoning !== false)
const levels = computed(() => reasoningLevels.filter(([level]) => level !== 'none' || capability.value.can_disable_reasoning !== false))
function changeModel(event) {
  const [provider, model] = event.target.value ? JSON.parse(event.target.value) : ['', '']
  const settings = normalizeSettings({ ...props.modelValue, model, provider })
  const caps = modelCapability(props.catalog, settings)
  if (caps.reasoning === false || (settings.reasoning_effort === 'none' && caps.can_disable_reasoning === false)) settings.reasoning_effort = ''
  emit('update:modelValue', settings)
}
</script>

<template>
  <div class="model-controls" :class="{compact}">
    <div class="model-field model-choice">
      <label :for="prefix+'-model'">模型</label>
      <div class="model-select"><select :id="prefix+'-model'" :value="selectedKey" :disabled="disabled||loading" @change="changeModel">
        <option value="">{{ loading?'正在加载模型…':catalog.model?'默认 · '+catalog.model:'跟随 Hermes 默认模型' }}</option>
        <option v-if="!knownModel" :value="selectedKey">{{ modelValue.model }}{{ modelValue.provider?' · '+modelValue.provider:'' }}</option>
        <optgroup v-for="provider in catalog.providers" :key="provider.slug" :label="provider.name||provider.slug"><option v-for="model in provider.models" :key="model" :value="modelKey(provider.slug,model)">{{ model }}</option></optgroup>
      </select><ChevronDown :size="13"/></div>
    </div>
    <div class="model-field effort-choice">
      <label :for="prefix+'-effort'">思考强度</label>
      <div class="model-select"><select :id="prefix+'-effort'" :value="supportsReasoning?modelValue.reasoning_effort:''" :disabled="disabled||!supportsReasoning" @change="$emit('update:modelValue',{...modelValue,reasoning_effort:$event.target.value})">
        <template v-if="supportsReasoning"><option v-for="[value,label] in levels" :key="value" :value="value">{{ label }}</option></template>
        <option v-else value="">此模型不支持</option>
      </select><ChevronDown :size="13"/></div>
    </div>
  </div>
</template>
