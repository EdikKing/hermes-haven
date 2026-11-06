import test from 'node:test'
import assert from 'node:assert/strict'
import { createRenderer } from 'vue'
import { useHaven } from '../src/useHaven.js'
import { pendingControls, controlKey } from '../src/controls.js'
import { createWorkspace } from '../src/workspace.js'
import { applyRunEvent, emptyRun } from '../src/events.js'

function memoryStorage() { const data = new Map(); return { getItem: k => data.get(k) || null, setItem: (k,v) => data.set(k,v), removeItem: k => data.delete(k) } }
async function until(predicate, ms = 2000) { const end = Date.now()+ms; while (!predicate()) { assert.ok(Date.now()<end, '异步状态未完成'); await new Promise(r=>setTimeout(r,5)) } }
const approval = { kind: 'approval', prompt_id: '审批:1', execution_generation: 3, command: 'printf HAVEN', description: '<script>不可执行</script>', choices: ['once','deny','always','session'] }
const clarification = { kind: 'clarify', prompt_id: '提问:2', execution_generation: 3, question: '需要哪种报告？', choices: ['BLUE','GREEN'], multi_select: true }

function backend(prompts = [approval]) {
  const s = { prompts, posts: [], status: 'running', user: 'edik', lose: false, reject: false, delay: null, streams: [] }
  s.fetch = async (input, options = {}) => {
    const path = new URL(input,'http://haven.test').pathname
    const reply = (data, status=200) => new Response(JSON.stringify(data),{status})
    if (path==='/api/me') return reply({username:s.user})
    if (path==='/api/health') return reply({})
    if (path==='/api/sessions') return reply({data:[{id:'one',owned:true},{id:'two',owned:true}]})
    if (path.endsWith('/messages')) return reply({data:[]})
    if (path.endsWith('/status')) {
      if (s.offlineStatus) throw new TypeError('状态读取断网')
      return reply(path.includes('/one/') ? {run_id:'run-one',status:s.status,pending_controls:s.prompts} : {status:'idle'})
    }
    if (path.endsWith('/approval') || path.endsWith('/clarify')) {
      s.posts.push({path, body:JSON.parse(options.body)})
      if (s.expire) return reply({code:'login_required',error:'登录已过期'},401)
      if (s.delay) await s.delay
      if (s.lose) throw new TypeError('响应丢失')
      s.prompts=[]
      return reply(s.reject?{error:'approval_not_pending'}:{status:s.already?'already_resolved':'resolved'},s.reject?409:200)
    }
    throw new Error('测试禁止调用其他接口：'+path)
  }
  return s
}

async function mount(s, storage=memoryStorage()) {
  const names=['window','fetch','localStorage','EventSource'], saved=names.map(k=>Object.getOwnPropertyDescriptor(globalThis,k))
  const globals={window:new EventTarget(),fetch:s.fetch,localStorage:storage,EventSource:class{constructor(){s.streams.push(this)}close(){this.closed=true}}}
  names.forEach(k=>Object.defineProperty(globalThis,k,{configurable:true,writable:true,value:globals[k]}))
  let h
  const renderer=createRenderer({createComment:()=>({}),insert(){},remove(){},parentNode:()=>null,nextSibling:()=>null,createElement:()=>({}),createText:()=>({}),patchProp(){},setText(){},setElementText(){}})
  const app=renderer.createApp({setup(){h=useHaven();return()=>null}});app.mount({})
  const dispose=()=>{app.unmount();names.forEach((k,i)=>{if(saved[i])Object.defineProperty(globalThis,k,saved[i]);else delete globalThis[k]})}
  try { await until(()=>h.sessions.value.length===2);if(!h.selected.value)await h.choose(h.sessions.value[0]);await until(()=>!h.loading.value&&!h.controlsSyncing.value);return {h,storage,dispose} }
  catch(e){dispose();throw e}
}

test('只投影完整原生身份；终态、伪造类型、缺少代次及重复请求不产生可操作卡片',()=>{
  const data={run_id:'r',status:'running',pending_controls:[approval,approval,{...clarification,execution_generation:undefined},{...approval,kind:'sudo'}]}
  assert.equal(pendingControls(data).length,1)
  assert.equal(pendingControls({...data,status:'completed'}).length,0)
  const view=emptyRun();assert.equal(applyRunEvent(view,{event:'clarify.request',seq:1}).controlsChanged,true);assert.deepEqual(view.controls,[])
})

test('审批双击只发一次精确原生回答；拒绝和允许一次均不创建新运行',async()=>{
  for(const choice of ['deny','once']){
    const s=backend(),c=await mount(s)
    try{let release;s.delay=new Promise(r=>release=r);const p=c.h.controls.value[0]
      const first=c.h.respondControl(p,choice);await c.h.respondControl(p,choice);assert.equal(s.posts.length,1)
      release();await first
      assert.deepEqual(s.posts[0].body,{run_id:'run-one',request_id:'审批:1',execution_generation:3,choice})
      assert.equal(c.h.controls.value.length,0)
    }finally{c.dispose()}
  }
})

test('补充回答草稿跨会话与刷新恢复；代次和账号隔离，提交后继续原运行',async()=>{
  const s=backend([clarification]),store=memoryStorage();let c=await mount(s,store)
  c.h.answerControl(c.h.controls.value[0],'BLUE\nGREEN 中文');await c.h.choose(c.h.sessions.value[1]);assert.equal(c.h.controls.value.length,0)
  await c.h.choose(c.h.sessions.value[0]);assert.equal(c.h.controls.value[0].state.answer,'BLUE\nGREEN 中文');c.dispose()
  c=await mount(s,store)
  try {const p=c.h.controls.value[0];assert.equal(p.state.answer,'BLUE\nGREEN 中文');await c.h.respondControl(p,p.state.answer);assert.equal(s.posts[0].body.answer,'BLUE\nGREEN 中文');assert.equal(s.posts.length,1)}finally{c.dispose()}
  const w=createWorkspace(store,'another');assert.equal(w.control('one',controlKey('run-one',clarification)).answer,'')
  assert.equal(createWorkspace(store,'edik').control('one',controlKey('run-one',{...clarification,execution_generation:4})).answer,'')
})

test('响应丢失不自动重发，刷新后仍须核对，确认同一请求仍待回答后才允许手动提交',async()=>{
  const s=backend([clarification]);s.lose=true;const store=memoryStorage();let c=await mount(s,store)
  c.h.answerControl(c.h.controls.value[0],'BLUE');await c.h.respondControl(c.h.controls.value[0],'BLUE')
  assert.equal(s.posts.length,1);assert.equal(c.h.controls.value[0].state.unknown,true)
  await c.h.respondControl(c.h.controls.value[0],'BLUE');assert.equal(s.posts.length,1);c.dispose()
  c=await mount(s,store)
  try {assert.equal(c.h.controls.value[0].state.unknown,true);assert.equal(c.h.controls.value[0].state.answer,'BLUE')
    await c.h.refreshControls();assert.equal(c.h.controls.value[0].state.unknown,false)
    s.lose=false;await c.h.respondControl(c.h.controls.value[0],'BLUE');assert.equal(s.posts.length,2)
  }finally{c.dispose()}
})

test('另一入口已处理或运行结束时移除过期卡片，原先的按钮不能提交到后续问题',async()=>{
  const s=backend(),c=await mount(s)
  try {const old=c.h.controls.value[0];s.prompts=[{...clarification,execution_generation:4}];await c.h.refreshControls()
    await c.h.respondControl(old,'once');assert.equal(s.posts.length,0)
    s.reject=true;await c.h.respondControl(c.h.controls.value[0],'BLUE');assert.equal(c.h.controls.value.length,0)
    s.status='completed';s.prompts=[approval];await c.h.refreshControls();assert.equal(c.h.controls.value.length,0)
  }finally{c.dispose()}
})

test('并发回答返回已处理时，不把其他入口的决定显示为本次允许成功',async()=>{
  const s=backend(),c=await mount(s)
  try{s.already=true;await c.h.respondControl(c.h.controls.value[0],'once');assert.match(c.h.notice.value,/其他入口处理/);assert.doesNotMatch(c.h.notice.value,/已允许/)}finally{c.dispose()}
})

test('原生 SSE 不广播补充提问时，轮询也能发现等待；读取状态不触发回答',async()=>{
  const s=backend([]),c=await mount(s)
  try {s.prompts=[clarification];await until(()=>c.h.controls.value.length===1,4500);assert.equal(s.posts.length,0);assert.equal(s.streams.filter(x=>!x.closed).length,1)}finally{c.dispose()}
})

test('登录到期时保留补充回答，重新登录后核对同一原生请求且不自动提交',async()=>{
  const s=backend([clarification]),store=memoryStorage();let c=await mount(s,store)
  c.h.answerControl(c.h.controls.value[0],'保留这段回答');s.expire=true
  await c.h.respondControl(c.h.controls.value[0],'保留这段回答');assert.equal(c.h.logged.value,false);assert.equal(s.posts.length,1);c.dispose()
  s.expire=false;c=await mount(s,store)
  try {assert.equal(c.h.controls.value[0].state.answer,'保留这段回答');assert.equal(s.posts.length,1)}finally{c.dispose()}
})

test('状态读取断网时禁用旧按钮；其他标签页提交标记和完成状态同步且不自动作答',async()=>{
  const s=backend(),c=await mount(s)
  try {const old=c.h.controls.value[0];s.offlineStatus=true;await c.h.refreshControls();assert.equal(c.h.controlsSyncing.value,true)
    await c.h.respondControl(old,'once');assert.equal(s.posts.length,0)
    s.offlineStatus=false;await c.h.refreshControls();assert.equal(c.h.controlsSyncing.value,false)
    const workspace=createWorkspace(c.storage,'edik');workspace.saveControl('one',old.key,{answer:'',unknown:true})
    const signal=()=>{const event=new Event('storage');Object.defineProperty(event,'key',{value:workspace.prefix+'control:one:'+old.key});window.dispatchEvent(event)}
    signal();await until(()=>c.h.controls.value[0].state.unknown);await c.h.respondControl(old,'once');assert.equal(s.posts.length,0)
    s.prompts=[];signal();await until(()=>c.h.controls.value.length===0);await c.h.respondControl(old,'deny');assert.equal(s.posts.length,0)
  }finally{c.dispose()}
})
