import { reactive } from 'vue'

// 全局响应式状态
export const state = reactive({
  page: 'run',                 // run | review | detail | settings
  theme: localStorage.getItem('sr_theme') || 'light',
  version: '',
  config: null,
  currentModel: '',
  running: false,              // 是否有正在执行的任务
  session: '',                 // 当前运行中的会话
  queue: { current: '', queued: [] }, // 任务队列（current=运行中，queued=排队）
  summary: null,
  models: { all: [], vision: [] },
  tasks: {},                   // sid -> { progress: [], total: 0, stage: '', startedAt: 0, finished: false }
  scan: null,                  // {count, est_cost, items}
  dir: '',                     // 运行页输入的源图目录（跨页面保留）
  selModel: '',                // 运行页选中的模型（跨页面保留）
  formatPreset: '',            // 运行页格式筛选（跨页面保留）
  importDir: '',               // 当前导入任务目录（贴入/拖入统一归类，后续追加）
  toast: null,
})

const FINAL = new Set(['scored', 'parse_fail', 'failed', 'bad_image', 'unsupported'])

// 获取（或初始化）某任务的进度容器
export function taskOf(sid) {
  if (!sid) return null
  if (!state.tasks[sid]) {
    state.tasks[sid] = { progress: [], total: 0, stage: '', startedAt: 0, finished: false }
  }
  return state.tasks[sid]
}

let toastTimer = null
export function toast(msg, isErr = false) {
  state.toast = { msg, err: isErr }
  clearTimeout(toastTimer)
  toastTimer = setTimeout(() => (state.toast = null), 3500)
}

export async function api(path, opts = {}) {
  if (localStorage.getItem('sr_debug') === '1') {
    console.log(`[api] → ${opts.method || 'GET'} ${path}`, opts.body || '')
  }
  const r = await fetch(path, {
    headers: { 'Content-Type': 'application/json' },
    ...opts,
  })
  let j = {}
  try { j = await r.json() } catch { /* 空响应 */ }
  if (!r.ok) {
    let msg = j.error || `HTTP ${r.status}`
    if (r.status === 401 || r.status === 403) {
      msg += ' —— 认证失败：请检查 API Key 是否正确（设置 → 平台接入 → 测试连接）'
    }
    console.error(`[api] ✗ ${r.status} ${path}:`, msg)
    throw new Error(msg)
  }
  if (localStorage.getItem('sr_debug') === '1') {
    console.log(`[api] ✓ ${r.status} ${path}`)
  }
  return j
}

export function applyState(s) {
  state.version = s.version || state.version
  state.config = s.config
  state.currentModel = s.currentModel || ''
  state.running = s.running
  state.session = s.session || ''
  if (s.queue) {
    state.queue = s.queue
    if (s.queue.current) { state.session = s.queue.current; taskOf(s.queue.current) }
    for (const q of s.queue.queued || []) taskOf(q)
  }
  if (s.summary !== undefined) state.summary = s.summary
}

export async function refreshState() {
  applyState(await api('/api/state'))
}

export async function refreshModels() {
  try {
    state.models = await api('/api/models')
    return state.models
  } catch (e) {
    console.error('[models] 拉取失败:', e)
    // 保留已有模型列表（避免切页后下拉清空/页面异常）
    if (!state.models.vision.length) {
      toast('拉取模型失败：' + e.message, true)
    } else {
      toast('拉取模型失败，沿用已有列表（' + e.message + '）', true)
    }
    return state.models
  }
}

export function setTheme(t) {
  state.theme = t
  localStorage.setItem('sr_theme', t)
  document.documentElement.classList.toggle('dark', t === 'dark')
}

// SSE 实时进度（多任务：按 session_id 归档到各自的任务容器）
export function connectSSE() {
  const es = new EventSource('/api/events')
  es.addEventListener('state', (e) => {
    const d = JSON.parse(e.data)
    state.running = d.running
    if (d.session) state.session = d.session
    if (d.queue) {
      state.queue = d.queue
      if (d.queue.current) taskOf(d.queue.current)
    }
  })
  es.addEventListener('queue', (e) => {
    state.queue = JSON.parse(e.data)
    state.running = !!state.queue.current
    if (state.queue.current) state.session = state.queue.current
  })
  es.addEventListener('stage', (e) => {
    const d = JSON.parse(e.data)
    const t = taskOf(d.session_id)
    if (!t) return
    t.total = d.total
    t.stage = d.stage
    if (!t.startedAt) t.startedAt = Date.now()
  })
  es.addEventListener('progress', (e) => {
    const d = JSON.parse(e.data)
    const t = taskOf(d.session_id)
    if (!t) return
    if (!t.startedAt) t.startedAt = Date.now()
    t.progress.push({ ...d, _ts: Date.now() })
    if (t.progress.length > 400) t.progress.splice(0, t.progress.length - 400)
  })
  es.addEventListener('night', (e) => {
    const d = JSON.parse(e.data)
    toast(`🌙 夜间重试第 ${d.round} 轮：剩余 ${d.remaining} 张，${d.delay_sec}s 后开始`)
  })
  es.addEventListener('done', (e) => {
    const d = JSON.parse(e.data)
    const t = taskOf(d.session_id)
    if (t) t.finished = true
    refreshState()
    if (d.stopped) toast(`任务 ${d.session_id} 已停止`)
    else if (d.night) {
      toast(d.failed > 0
        ? `🌙 夜间模式结束：仍有 ${d.failed} 张持续失败（已停止重试）`
        : '🌙 夜间模式结束：全部评分成功')
    }
  })
  es.addEventListener('error', (e) => {
    if (e.data) {
      const d = JSON.parse(e.data)
      toast(d.error || '流水线错误', true)
    }
  })
  es.onerror = () => { /* 断线时浏览器自动重连 */ }
}
