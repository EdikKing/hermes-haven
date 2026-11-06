<script setup>
import { computed } from 'vue'
import { Anchor, ArrowUp, Check, ChevronDown, ChevronRight, CircleAlert, Clock3, ImagePlus, LoaderCircle, LockKeyhole, LogOut, Menu, MessageSquare, MoreHorizontal, PanelLeftClose, Pencil, Plus, Search, Square, Sparkles, UserRound, X } from 'lucide-vue-next'
import { useHaven } from './useHaven.js'
import { groupMessages, imageBlocks, messageText } from './messages.js'
import ToolGroup from './ToolGroup.vue'
const { logged, username, password, identity, loginError, signingIn, filter, section, selected, messages, loading, sidebar, editing, titleDraft, hasOlder, scroller, picker, health, sessionsLoading, hasMoreSessions, searching, olderLoading, globalNotice, text, files, pending, tools, liveText, sending, uploading, isBusy, isUncertain, composerLocked, mine, notice, filtered, selectedTitle, statusLabel, sourceName, sessionTitle, timeLabel, login, logout, checkHealth, loadMoreSessions, choose, moreHistory, createSession, startRename, rename, attach, pasteImages, removeImage, send, retryPending, acknowledgePending, refreshStatus, stop, render } = useHaven()
const turns = computed(() => groupMessages(messages.value, { text: liveText.value, tools: tools.value, busy: isBusy.value }))
</script>
<template>
  <div v-if="logged === null" class="splash"><LoaderCircle class="spin" :size="22" /></div>
  <div v-else-if="!logged" class="login-page">
    <div class="login-card"><div class="brand-mark large"><Anchor :size="24" /></div><div class="eyebrow">YOUR SPACE TO THINK</div><h1>Hermes Haven</h1><p>使用你的 Hermes Dashboard 账号登录</p><form @submit.prevent="login"><div class="login-field"><label for="username">Hermes 用户名</label><div class="password-wrap"><UserRound :size="17"/><input id="username" v-model="username" name="username" autocomplete="username" placeholder="输入 Hermes 用户名" :disabled="signingIn" required autofocus></div></div><div class="login-field"><label for="password">密码</label><div class="password-wrap"><LockKeyhole :size="17"/><input id="password" v-model="password" name="password" type="password" autocomplete="current-password" placeholder="输入 Hermes 密码" :disabled="signingIn" required></div></div><div v-if="loginError" class="form-error" role="alert">{{ loginError }}</div><button class="login-button" :disabled="signingIn || !username.trim() || !password">{{ signingIn ? '正在验证…' : '进入 Haven' }} <ArrowUp class="arrow-diagonal" :size="17"/></button></form><div class="login-foot"><span class="dot"></span> YOUR PERSONAL HERMES WORKSPACE</div></div>
  </div>
  <div v-else class="app-shell">
    <div v-if="sidebar" class="mobile-backdrop" @click="sidebar=false"></div>
    <aside class="sidebar" :class="{open:sidebar}">
      <div class="sidebar-brand"><div class="brand-mark"><Anchor :size="18" /></div><div><strong>Hermes Haven</strong><small>YOUR AI WORKSPACE</small></div><button class="icon-button mobile-only side-close" title="收起侧栏" @click="sidebar=false"><PanelLeftClose :size="18"/></button></div>
      <button class="new-chat" @click="createSession"><Plus :size="17"/> <span>新建会话</span><kbd>＋</kbd></button>
      <div class="search-box"><Search :size="16"/><input v-model="filter" placeholder="搜索会话" aria-label="搜索会话"><span>⌕</span></div>
      <div class="nav-tabs"><button :class="{active:section==='mine'}" @click="section='mine'">我的会话</button><button :class="{active:section==='history'}" @click="section='history'">历史记录</button></div>
      <div class="session-heading"><span>{{ section==='mine'?'最近会话':'其他来源' }}</span><MoreHorizontal :size="16"/></div>
      <div class="session-list"><button v-for="s in filtered" :key="s.id" class="session-row" :class="{active:selected?.id===s.id}" @click="choose(s)"><MessageSquare :size="15"/><span class="session-name">{{ sessionTitle(s) }}</span><span class="session-time">{{ timeLabel(s.last_active || s.started_at) }}</span></button><div v-if="searching" class="empty-list"><LoaderCircle :size="14" class="spin"/> 正在搜索更早的会话…</div><div v-else-if="!filtered.length" class="empty-list">{{ filter ? '没有找到相关会话' : section==='mine' ? '还没有会话，开始聊聊吧' : '暂无其他历史记录' }}</div><button v-if="hasMoreSessions" class="load-sessions" :disabled="sessionsLoading||searching" @click="loadMoreSessions">{{ sessionsLoading?'正在加载…':'加载更多会话' }}</button></div>
      <div class="sidebar-footer"><div class="connection"><span class="status-dot" :class="health"></span><span>{{ health==='online'?'Hermes 已连接':health==='connecting'?'正在连接':'Hermes 未连接' }}</span><button class="icon-button" title="刷新连接" @click="checkHealth"><Sparkles :size="15"/></button></div><button class="account" @click="logout"><span class="avatar small">H</span><span><strong>{{ identity.display_name || identity.username || '个人空间' }}</strong><small>点击安全退出</small></span><LogOut :size="16"/></button></div>
    </aside>
    <main class="main"><header class="topbar"><button class="icon-button mobile-only" title="打开侧栏" @click="sidebar=true"><Menu :size="20"/></button><div class="breadcrumb"><span>工作空间</span><ChevronRight :size="14"/><strong>{{ selected?selectedTitle:'对话' }}</strong><span v-if="selected&&!mine" class="readonly-pill">{{ sourceName(selected.source) }}</span></div><div class="top-actions"><button v-if="selected&&mine" class="icon-button" title="重命名" @click="startRename"><Pencil :size="16"/></button><div class="top-profile"><span class="avatar tiny">H</span><span>我的空间</span><ChevronDown :size="14"/></div></div></header>
      <div v-if="globalNotice&&!selected" class="global-notice notice" role="status"><CircleAlert :size="16"/><span>{{ globalNotice }}</span><button class="icon-button" @click="globalNotice=''"><X :size="15"/></button></div>
      <div ref="scroller" class="conversation" :class="{'is-empty':!selected}">
        <div v-if="!selected" class="welcome"><div class="welcome-emblem"><Sparkles :size="27"/></div><div class="eyebrow">A LITTLE SPACE FOR BIG IDEAS</div><h1>你好，今天想聊什么？</h1><p>想法、问题，或是正在进行的事。<br>在这里，和你的 Hermes 慢慢聊。</p><button class="welcome-button" @click="createSession"><Plus :size="17"/> 开始新会话</button><div class="welcome-hint"><span></span> YOUR THOUGHTS, IN A SAFE HARBOR <span></span></div></div>
        <div v-else class="thread"><div class="thread-intro"><div class="thread-kicker"><span class="mini-mark"><Anchor :size="12"/></span> {{ mine?'HERMES HAVEN':'历史记录' }} <span class="intro-divider">/</span> {{ sourceName(selected.source) }}</div><div v-if="editing" class="rename"><input v-model="titleDraft" maxlength="80" @keydown.enter="rename" @keydown.esc="editing=false"><button class="icon-button" @click="rename"><Check :size="16"/></button><button class="icon-button" @click="editing=false"><X :size="16"/></button></div><h2 v-else>{{ selectedTitle }}</h2><p>{{ mine?'一个属于你的思考空间':'这条会话来自其他入口，可尝试继续对话。' }}</p></div>
          <button v-if="hasOlder" class="older" :disabled="olderLoading||loading" @click="moreHistory"><Clock3 :size="14"/> {{ olderLoading?'正在加载…':'加载更早的消息' }}</button>
          <div v-if="loading" class="thread-loading"><LoaderCircle class="spin" :size="18"/> 正在加载会话…</div>
          <div v-for="turn in turns" :key="turn.key" class="message" :class="turn.role">
            <div class="message-avatar" :class="turn.role">{{ turn.role==='user'?'你':'H' }}</div>
            <div class="message-body">
              <div class="message-meta"><strong>{{ turn.role==='user'?'你':'Hermes' }}</strong><span v-if="turn.timestamp">{{ timeLabel(turn.timestamp) }}</span><span v-if="turn.live" class="live-label"><span class="status-dot" :class="isBusy?'online':''"></span> {{ statusLabel }}</span></div>
              <div v-for="block in turn.blocks" :key="block.key" class="message-block">
                <template v-if="block.type==='content'">
                  <div v-if="messageText(block.message)" class="prose" v-html="render(messageText(block.message))"></div>
                  <div v-if="imageBlocks(block.message).length" class="message-images"><img v-for="img in imageBlocks(block.message)" :key="img.src" :src="img.src" :alt="img.name||'图片'"></div>
                </template>
                <ToolGroup v-else-if="block.type==='tools'" :tools="block.tools" :live="block.live"/>
                <div v-else-if="block.type==='thinking'" class="thinking"><span></span><span></span><span></span></div>
              </div>
            </div>
          </div>
          <div v-if="!loading&&!messages.length&&!isBusy" class="empty-thread"><Sparkles :size="19"/><span>一段新的对话，从这里开始。</span></div>
        </div>
      </div>
      <div v-if="selected" class="composer-area"><div v-if="notice" class="notice" role="status"><CircleAlert :size="16"/><span>{{ notice }}</span><button class="icon-button" @click="notice=''"><X :size="15"/></button></div><div v-if="pending||isUncertain" class="pending-panel"><div><strong>发送结果待核对</strong><p v-if="pending">{{ pending.text?.slice(0,100)||'图片消息' }}{{ pending.images?.length?' · '+pending.images.length+' 张图片':'' }}</p><p v-else>原运行状态尚不确定，当前不会自动重新执行。</p></div><div class="pending-actions"><button @click="refreshStatus" :disabled="sending">核对结果</button><button v-if="pending" @click="retryPending" :disabled="sending">{{ sending?'正在核对…':'安全重试原请求' }}</button><button v-if="pending" @click="acknowledgePending" :disabled="sending">已核对，解除等待</button></div></div><div class="composer"><div v-if="files.length" class="attachments"><div v-for="(f,i) in files" :key="f.id" class="attachment"><img :src="f.preview" :alt="f.name"><span>{{ f.name }}</span><button @click="removeImage(i)" :disabled="composerLocked" title="移除"><X :size="13"/></button></div></div><textarea v-model="text" rows="2" placeholder="给 Hermes 发消息…" :disabled="composerLocked" @paste="pasteImages" @keydown.enter.exact.prevent="send"></textarea><div class="composer-bottom"><div><input ref="picker" type="file" accept="image/png,image/jpeg,image/gif,image/webp" multiple hidden @change="attach"><button class="attach-button" :disabled="composerLocked||Boolean(uploading)" @click="picker.click()"><ImagePlus :size="17"/> <span>{{ uploading?'正在上传…':'添加图片' }}</span></button><span class="composer-meta">ENTER 发送 · SHIFT + ENTER 换行</span></div><button v-if="isBusy" class="send-button stop" title="停止" @click="stop"><Square :size="15" fill="currentColor"/></button><button v-else class="send-button" :disabled="composerLocked||Boolean(uploading)||(!text.trim()&&!files.length)" title="发送" @click="send"><LoaderCircle v-if="sending" :size="17" class="spin"/><ArrowUp v-else :size="19"/></button></div></div><div class="composer-disclaimer">由 Hermes 提供支持 <span>·</span> 草稿按会话保存在当前浏览器</div></div>
    </main>
  </div>
</template>
