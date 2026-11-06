<script setup>
import { computed, ref } from 'vue'
import { Anchor, ArrowUp, Check, ChevronDown, ChevronRight, CircleAlert, Archive, Clock3, File, Folder, LoaderCircle, LockKeyhole, LogOut, Menu, MessageSquare, MoreHorizontal, PanelLeftClose, Pencil, Plus, Search, Square, Sparkles, UserRound, X } from 'lucide-vue-next'
import { useHaven } from './useHaven.js'
import { groupMessages, imageBlocks, messageText } from './messages.js'
import ToolGroup from './ToolGroup.vue'
import AttachmentCards from './AttachmentCards.vue'
import AttachmentPreview from './AttachmentPreview.vue'
import ModelSettings from './ModelSettings.vue'
import NewSessionDialog from './NewSessionDialog.vue'
import ContextMeter from './ContextMeter.vue'
import RunControls from './RunControls.vue'
import SessionOrganizer from './SessionOrganizer.vue'
import { formatSize } from './attachments.js'
const haven = useHaven()
const { logged, username, password, identity, loginError, signingIn, filter, section, selected, messages, loading, sidebar, editing, titleDraft, hasOlder, scroller, sessionList, onSessionsScroll, picker, health, sessionsLoading, hasMoreSessions, searching, olderLoading, globalNotice, text, files, pending, tools, liveText, sending, uploading, isBusy, isUncertain, composerLocked, mine, notice, filtered, selectedTitle, statusLabel, sourceName, sessionTitle, timeLabel, login, logout, checkHealth, loadMoreSessions, choose, createSession, startRename, rename, attach, pasteImages, removeImage, send, retryPending, acknowledgePending, refreshStatus, stop, render } = haven
const { olderError, onHistoryScroll } = haven
const { sessionsError } = haven
const { uploadQueue, previewFile, openPreview, loadPreviewText, dropFiles } = haven
const { contextUsage, contextLoading, contextError } = haven
const { modelCatalog, modelsLoading, modelsError, loadModels, currentSettings, settingsLoading, settingsSaving, settingsBlocked, settingsError, saveSettings, loadSettings, newSessionOpen, newSessionSettings, newSessionError, creatingSession, openNewSession } = haven
const { controls, controlsSyncing, answerControl, respondControl, refreshControls } = haven
const { groups, groupFilter, selectedGroup, sessionAction, groupDialog, organizationBusy, organizationError, actionBlocked, openSessionActions, openGroupDialog, organizeSession, saveGroup, deleteGroup, deleteSession } = haven
const activeGroup = computed(() => groups.value.find(g => g.id === groupFilter.value))
const turns = computed(() => groupMessages(messages.value, { text: liveText.value, tools: tools.value, busy: isBusy.value }))
const sidebarMinWidth = 272
const sidebarMaxWidth = 480
const sidebarWidth = ref(sidebarMinWidth)
const sidebarResizing = ref(false)
const sidebarResizeStart = ref(null)
function maxSidebarWidth() {
  return Math.max(sidebarMinWidth, Math.min(sidebarMaxWidth, Math.floor(window.innerWidth * 0.48)))
}
function startSidebarResize(event) {
  if (event.button !== 0) return
  sidebarResizeStart.value = { pointerId: event.pointerId, x: event.clientX, width: sidebarWidth.value }
  sidebarResizing.value = true
  event.currentTarget.setPointerCapture(event.pointerId)
}
function moveSidebarResize(event) {
  const start = sidebarResizeStart.value
  if (!start || start.pointerId !== event.pointerId) return
  sidebarWidth.value = Math.min(maxSidebarWidth(), Math.max(sidebarMinWidth, start.width + event.clientX - start.x))
}
function endSidebarResize(event) {
  if (sidebarResizeStart.value?.pointerId !== event.pointerId) return
  sidebarResizeStart.value = null
  sidebarResizing.value = false
}
function resizeSidebarWithKeyboard(event) {
  const step = 16
  if (event.key === 'ArrowLeft') sidebarWidth.value = Math.max(sidebarMinWidth, sidebarWidth.value - step)
  else if (event.key === 'ArrowRight') sidebarWidth.value = Math.min(maxSidebarWidth(), sidebarWidth.value + step)
  else if (event.key === 'Home') sidebarWidth.value = sidebarMinWidth
  else if (event.key === 'End') sidebarWidth.value = maxSidebarWidth()
  else return
  event.preventDefault()
}
</script>
<template>
  <div v-if="logged === null" class="splash"><LoaderCircle class="spin" :size="22" /></div>
  <div v-else-if="!logged" class="login-page">
    <div class="login-card"><div class="brand-mark large"><Anchor :size="24" /></div><div class="eyebrow">YOUR SPACE TO THINK</div><h1>Hermes Haven</h1><p>使用你的 Hermes Dashboard 账号登录</p><form @submit.prevent="login"><div class="login-field"><label for="username">Hermes 用户名</label><div class="password-wrap"><UserRound :size="17"/><input id="username" v-model="username" name="username" autocomplete="username" placeholder="输入 Hermes 用户名" :disabled="signingIn" required autofocus></div></div><div class="login-field"><label for="password">密码</label><div class="password-wrap"><LockKeyhole :size="17"/><input id="password" v-model="password" name="password" type="password" autocomplete="current-password" placeholder="输入 Hermes 密码" :disabled="signingIn" required></div></div><div v-if="loginError" class="form-error" role="alert">{{ loginError }}</div><button class="login-button" :disabled="signingIn || !username.trim() || !password">{{ signingIn ? '正在验证…' : '进入 Haven' }} <ArrowUp class="arrow-diagonal" :size="17"/></button></form><div class="login-foot"><span class="dot"></span> YOUR PERSONAL HERMES WORKSPACE</div></div>
  </div>
  <div v-else class="app-shell" :class="{'sidebar-resizing':sidebarResizing}">
    <div v-if="sidebar" class="mobile-backdrop" @click="sidebar=false"></div>
    <aside class="sidebar" :class="{open:sidebar}" :style="{'--sidebar-width':`${sidebarWidth}px`}">
      <div class="sidebar-brand"><div class="brand-mark"><Anchor :size="18" /></div><div><strong>Hermes Haven</strong><small>YOUR AI WORKSPACE</small></div><button class="icon-button mobile-only side-close" title="收起侧栏" @click="sidebar=false"><PanelLeftClose :size="18"/></button></div>
      <button class="new-chat" @click="openNewSession"><Plus :size="17"/> <span>新建会话</span></button>
      <div class="search-box"><Search :size="14"/><input v-model="filter" placeholder="搜索会话" aria-label="搜索会话"></div>
      <div class="project-heading"><span><Folder :size="14"/> 项目分组</span><button class="icon-button" aria-label="新建分组" title="新建分组" @click="openGroupDialog()"><Plus :size="16"/></button></div>
      <div class="project-filter"><select v-model="groupFilter" aria-label="按项目分组筛选"><option value="">全部分组</option><option value="ungrouped">未分组</option><option v-for="g in groups" :key="g.id" :value="g.id">{{ g.name }}</option></select><button v-if="activeGroup" class="icon-button" aria-label="编辑当前分组" title="编辑当前分组" @click="openGroupDialog(activeGroup)"><Pencil :size="15"/></button></div>
      <div class="nav-tabs"><button :class="{active:section==='all'}" @click="section='all'">全部</button><button :class="{active:section==='mine'}" @click="section='mine'">我的会话</button><button :class="{active:section==='history'}" @click="section='history'">历史记录</button><button :class="{active:section==='archive'}" @click="section='archive'"><Archive :size="12"/> 归档</button></div>
      <div class="session-heading"><span>{{ section==='archive'?'已归档':activeGroup?.name||'最近会话' }}</span><span>{{ filtered.length }}{{ hasMoreSessions?'+':'' }}</span></div>
      <div ref="sessionList" class="session-list" @scroll.passive="onSessionsScroll"><div v-for="s in filtered" :key="s.id" class="session-item" :class="{active:selected?.id===s.id}"><button class="session-row" :class="{active:selected?.id===s.id}" @click="choose(s)"><MessageSquare :size="15"/><span class="session-name">{{ sessionTitle(s) }}<small v-if="s.deletion_pending">删除待完成</small></span><span class="session-time">{{ timeLabel(s.last_active || s.started_at) }}</span></button><button class="session-more icon-button" :aria-label="'管理会话：'+sessionTitle(s)" title="分组、归档或删除" @click="openSessionActions(s)"><MoreHorizontal :size="16"/></button></div><div v-if="searching||sessionsLoading" class="empty-list" role="status"><LoaderCircle :size="14" class="spin"/> 正在加载更多会话…</div><div v-else-if="sessionsError" class="empty-list" role="status">{{ sessionsError }} <button class="session-retry" @click="loadMoreSessions">重试</button></div><div v-else-if="!filtered.length" class="empty-list">{{ filter?'没有找到相关会话':section==='archive'?'暂无归档会话':groupFilter?'此分组暂无会话':'还没有会话，开始聊聊吧' }}</div></div>
      <div class="sidebar-footer"><div class="connection"><span class="status-dot" :class="health"></span><span>{{ health==='online'?'Hermes 已连接':health==='connecting'?'正在连接':'Hermes 未连接' }}</span><button class="icon-button" title="刷新连接" @click="checkHealth"><Sparkles :size="15"/></button></div><button class="account" @click="logout"><span class="avatar small">H</span><span><strong>{{ identity.display_name || identity.username || '个人空间' }}</strong><small>点击安全退出</small></span><LogOut :size="16"/></button></div>
      <div class="sidebar-resizer" role="separator" aria-orientation="vertical" aria-label="调整侧栏宽度" :aria-valuemin="sidebarMinWidth" :aria-valuemax="maxSidebarWidth()" :aria-valuenow="Math.round(sidebarWidth)" tabindex="0" @pointerdown.prevent="startSidebarResize" @pointermove="moveSidebarResize" @pointerup="endSidebarResize" @pointercancel="endSidebarResize" @lostpointercapture="endSidebarResize" @keydown="resizeSidebarWithKeyboard"></div>
    </aside>
    <main class="main"><header class="topbar"><button class="icon-button mobile-only" title="打开侧栏" @click="sidebar=true"><Menu :size="20"/></button><div class="breadcrumb"><span>工作空间</span><ChevronRight :size="14"/><strong>{{ selected?selectedTitle:'对话' }}</strong><span v-if="selected&&!mine" class="readonly-pill">{{ sourceName(selected.source) }}</span></div><div class="top-actions"><button v-if="selected&&mine" class="icon-button" title="重命名" @click="startRename"><Pencil :size="16"/></button><button v-if="selected" class="icon-button" aria-label="管理当前会话" title="分组、归档或删除" @click="openSessionActions()"><MoreHorizontal :size="18"/></button><div class="top-profile"><span class="avatar tiny">H</span><span>我的空间</span><ChevronDown :size="14"/></div></div></header>
      <div v-if="globalNotice&&!selected" class="global-notice notice" role="status"><CircleAlert :size="16"/><span>{{ globalNotice }}</span><button class="icon-button" @click="globalNotice=''"><X :size="15"/></button></div>
      <div ref="scroller" class="conversation" :class="{'is-empty':!selected}" @scroll.passive="onHistoryScroll">
        <div v-if="!selected" class="welcome"><div class="welcome-emblem"><Sparkles :size="27"/></div><div class="eyebrow">A LITTLE SPACE FOR BIG IDEAS</div><h1>你好，今天想聊什么？</h1><p>想法、问题，或是正在进行的事。<br>在这里，和你的 Hermes 慢慢聊。</p><button class="welcome-button" @click="openNewSession"><Plus :size="17"/> 开始新会话</button><div class="welcome-hint"><span></span> YOUR THOUGHTS, IN A SAFE HARBOR <span></span></div></div>
        <div v-else class="thread"><div class="thread-intro"><div class="thread-kicker"><span class="mini-mark"><Anchor :size="12"/></span> {{ mine?'HERMES HAVEN':'历史记录' }} <span class="intro-divider">/</span> {{ sourceName(selected.source) }}</div><div v-if="editing" class="rename"><input v-model="titleDraft" maxlength="80" @keydown.enter="rename" @keydown.esc="editing=false"><button class="icon-button" @click="rename"><Check :size="16"/></button><button class="icon-button" @click="editing=false"><X :size="16"/></button></div><h2 v-else>{{ selectedTitle }}</h2><div v-if="selectedGroup||selected.archived" class="thread-labels"><span v-if="selectedGroup"><Folder :size="12"/>{{ selectedGroup }}</span><span v-if="selected.archived"><Archive :size="12"/>已归档</span></div><p>{{ mine?'一个属于你的思考空间':'这条会话来自其他入口，可尝试继续对话。' }}</p></div>
          <div v-if="hasOlder&&!loading" class="older" role="status"><LoaderCircle v-if="olderLoading" class="spin" :size="14"/><Clock3 v-else :size="14"/> {{ olderLoading?'正在加载更早的消息…':olderError?'加载暂未完成，稍后自动重试':'向上滚动，自动加载更早的消息' }}</div>
          <div v-if="loading" class="thread-loading"><LoaderCircle class="spin" :size="18"/> 正在加载会话…</div>
          <div v-for="turn in turns" :key="turn.key" class="message" :class="turn.role">
            <div class="message-avatar" :class="turn.role">{{ turn.role==='user'?'你':'H' }}</div>
            <div class="message-body">
              <div class="message-meta"><strong>{{ turn.role==='user'?'你':'Hermes' }}</strong><span v-if="turn.timestamp">{{ timeLabel(turn.timestamp) }}</span><span v-if="turn.live" class="live-label"><span class="status-dot" :class="isBusy?'online':''"></span> {{ statusLabel }}</span></div>
              <div v-for="block in turn.blocks" :key="block.key" class="message-block">
                <template v-if="block.type==='content'">
                  <div v-if="messageText(block.message)" class="prose" v-html="render(messageText(block.message))"></div>
                  <div v-if="imageBlocks(block.message).length" class="message-images"><img v-for="img in imageBlocks(block.message)" :key="img.src" :src="img.src" :alt="img.name||'图片'"></div>
                  <AttachmentCards :files="block.message.attachments" @preview="openPreview"/>
                  <p v-if="block.message.attachment_notice" class="attachment-notice" role="status">{{ block.message.attachment_notice }}</p>
                </template>
                <ToolGroup v-else-if="block.type==='tools'" :tools="block.tools" :live="block.live"/>
                <div v-else-if="block.type==='thinking'" class="thinking"><span></span><span></span><span></span></div>
              </div>
            </div>
          </div>
          <div v-if="!loading&&!messages.length&&!isBusy" class="empty-thread"><Sparkles :size="19"/><span>一段新的对话，从这里开始。</span></div>
          <RunControls :controls="controls" :syncing="controlsSyncing" @answer="answerControl" @respond="respondControl" @refresh="refreshControls"/>
        </div>
      </div>
      <div v-if="selected&&(selected.archived||selected.deletion_pending)" class="archived-banner"><Archive :size="17"/><span>{{ selected.deletion_pending?'删除尚未完成，请重试彻底删除。':'此会话已归档，恢复后可以继续对话。' }}</span><button @click="openSessionActions()">{{ selected.deletion_pending?'继续删除':'管理与恢复' }}</button></div>
      <div v-if="selected&&!selected.archived&&!selected.deletion_pending" class="composer-area">
        <div v-if="notice" class="notice" role="status"><CircleAlert :size="16"/><span>{{ notice }}</span><button class="icon-button" @click="notice=''"><X :size="15"/></button></div>
        <div v-if="pending||isUncertain" class="pending-panel"><div><strong>发送结果待核对</strong><p v-if="pending">{{ pending.text?.slice(0,100)||'附件消息' }}{{ (pending.images?.length||0)+(pending.files?.length||0)?' · '+((pending.images?.length||0)+(pending.files?.length||0))+' 个附件':'' }}</p><p v-else>原运行状态尚不确定，当前不会自动重新执行。</p></div><div class="pending-actions"><button @click="refreshStatus" :disabled="sending">核对结果</button><button v-if="pending" @click="retryPending" :disabled="sending">{{ sending?'正在核对…':'安全重试原请求' }}</button><button v-if="pending" @click="acknowledgePending" :disabled="sending">已核对，解除等待</button></div></div>
        <div class="composer" @dragover.prevent @drop.prevent="dropFiles">
          <ContextMeter :usage="contextUsage" :loading="contextLoading" :error="contextError"/>
          <div v-if="files.length||uploadQueue.length" class="attachments">
            <div v-for="(f,i) in files" :key="f.id" class="attachment">
              <button class="draft-attachment" :disabled="f.preview_kind==='none'" :title="f.name" @click="openPreview(f)"><img v-if="f.kind==='image'" :src="f.preview" :alt="f.name"><File v-else :size="22"/><span>{{ f.name }}<small>{{ formatSize(f.size) }}</small></span></button>
              <button @click="removeImage(i)" :disabled="composerLocked" :aria-label="'移除 '+f.name"><X :size="13"/></button>
            </div>
            <div v-for="job in uploadQueue" :key="job.id" class="attachment upload-job" role="status"><LoaderCircle class="spin" :size="15"/><span>{{ job.name }}<small>{{ job.state==='uploading'?'正在上传…':'等待上传…' }}</small></span></div>
          </div>
          <textarea v-model="text" rows="3" placeholder="输入消息…（Enter 发送，Shift+Enter 换行）" :disabled="composerLocked" @paste="pasteImages" @keydown.enter.exact.prevent="send"></textarea>
          <div class="composer-bottom"><div class="composer-toolbar"><input ref="picker" type="file" multiple hidden @change="attach"><button class="attach-button" :disabled="composerLocked||Boolean(uploading)" aria-label="添加附件" title="最多 4 个附件，单文件 50 MiB；图片单张 5 MiB" @click="picker.click()"><LoaderCircle v-if="uploading" :size="17" class="spin"/><Plus v-else :size="20"/></button><ModelSettings prefix="composer" compact :model-value="currentSettings" :catalog="modelCatalog" :loading="modelsLoading" :disabled="settingsLoading||settingsSaving||settingsBlocked" @update:model-value="saveSettings"/></div><button v-if="isBusy" class="send-button stop" title="停止" @click="stop"><Square :size="15" fill="currentColor"/></button><button v-else class="send-button" :disabled="composerLocked||settingsBlocked||Boolean(uploading)||(!text.trim()&&!files.length)" title="发送" @click="send"><LoaderCircle v-if="sending" :size="17" class="spin"/><ArrowUp v-else :size="19"/></button></div>
        </div>
        <div v-if="settingsLoading||settingsSaving" class="settings-status composer-saving" role="status">{{ settingsLoading?'正在读取设置…':'正在保存设置…' }}</div>
        <div v-if="settingsError" class="model-error" role="alert">{{ settingsError }} <button @click="loadSettings()">重新读取</button></div>
        <div v-if="modelsError" class="model-error" role="status">{{ modelsError }} <button :disabled="modelsLoading" @click="loadModels">重新加载模型</button></div>
        <div class="composer-disclaimer">由 Hermes 提供支持 <span>·</span> 草稿与附件按会话保留</div>
      </div>
    </main>
  </div>
  <SessionOrganizer :session="sessionAction" :group="groupDialog" :groups="groups" :title="sessionAction?sessionTitle(sessionAction):''" :busy="organizationBusy" :error="organizationError" :blocked="actionBlocked" @close="sessionAction=null;groupDialog=null" @organize="organizeSession" @save-group="saveGroup" @delete-group="deleteGroup" @delete-session="deleteSession"/>
  <AttachmentPreview :file="previewFile" :load-text="loadPreviewText" :render="render" @close="previewFile=null"/>
  <NewSessionDialog :open="newSessionOpen" v-model:settings="newSessionSettings" :catalog="modelCatalog" :loading="modelsLoading" :creating="creatingSession" :error="newSessionError" :model-error="modelsError" @close="newSessionOpen=false" @create="createSession" @retry="loadModels"/>
</template>
