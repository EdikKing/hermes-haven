<script setup>
import { ShieldCheck, MessageCircleQuestion, LoaderCircle, RefreshCw } from 'lucide-vue-next'
defineProps({ controls: { type: Array, default: () => [] }, syncing: Boolean })
defineEmits(['answer', 'respond', 'refresh'])
</script>
<template>
  <section v-if="controls.length" class="run-controls" aria-label="Hermes 待处理请求" aria-live="polite">
    <article v-for="p in controls" :key="p.key" class="control-card" :aria-busy="p.state.sending">
      <header><ShieldCheck v-if="p.kind==='approval'" :size="18"/><MessageCircleQuestion v-else :size="18"/><strong>{{ p.kind==='approval'?'需要你的审批':'Hermes 想再确认一下' }}</strong><span v-if="p.state.sending"><LoaderCircle class="spin" :size="14"/> 正在提交</span></header>
      <template v-if="p.kind==='approval'">
        <p v-if="p.description" class="control-description">{{ p.description }}</p>
        <pre v-if="p.command" class="control-command">{{ p.command }}</pre>
        <details v-if="p.edit"><summary>查看文件修改内容</summary><pre class="control-command">{{ typeof p.edit==='string'?p.edit:JSON.stringify(p.edit,null,2) }}</pre></details>
        <p class="control-hint">仅处理这一次请求，后续操作仍由 Hermes 的审批规则决定。</p>
      </template>
      <template v-else>
        <p class="control-question">{{ p.question || '请补充信息以继续当前运行。' }}</p>
        <div v-if="p.choices.length" class="control-choices">
          <label v-for="(choice,index) in p.choices" :key="index"><input :type="p.multi_select?'checkbox':'radio'" :name="p.key" :checked="p.multi_select?p.state.answer.split('\n').includes(choice):p.state.answer===choice" :disabled="p.state.sending||p.state.unknown||syncing" @change="$emit('answer',p,p.multi_select?(p.state.answer.split('\n').filter(c=>c&&c!==choice).concat($event.target.checked?[choice]:[]).join('\n')):choice)"><span>{{ choice }}</span></label>
        </div>
        <label class="control-answer-label">{{ p.choices.length?'或填写你的回答':'你的回答' }}<textarea :value="p.state.answer" :disabled="p.state.sending||p.state.unknown||syncing" maxlength="16384" rows="2" placeholder="补充后继续同一次运行…" @input="$emit('answer',p,$event.target.value)"></textarea></label>
      </template>
      <p v-if="p.timeout_s" class="control-hint">Hermes 设置了等待时限，超时后以最新运行状态为准。</p>
      <p v-if="p.state.error" class="control-error" role="status">{{ p.state.error }}</p>
      <div class="control-actions">
        <template v-if="!p.state.unknown">
          <button v-if="p.kind==='approval'&&p.choices.includes('deny')" class="control-deny" :disabled="p.state.sending||syncing" @click="$emit('respond',p,'deny')">拒绝</button>
          <button v-if="p.kind==='approval'&&p.choices.includes('once')" class="control-primary" :disabled="p.state.sending||syncing" @click="$emit('respond',p,'once')">允许一次</button>
          <button v-if="p.kind==='clarify'" class="control-primary" :disabled="p.state.sending||syncing||!p.state.answer.trim()" @click="$emit('respond',p,p.state.answer)">提交回答并继续</button>
        </template>
        <button class="control-refresh" :disabled="p.state.sending||syncing" @click="$emit('refresh')"><RefreshCw :size="13"/>{{ syncing?'正在核对…':'核对状态' }}</button>
      </div>
    </article>
  </section>
</template>
