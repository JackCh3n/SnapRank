import { reactive } from 'vue'

// 全局响应式状态
export const state = reactive({
  page: 'run',                 // run | review | detail | settings
  theme: localStorage.getItem('sr_theme') || 'light',
  version: '',
  config: null,
  currentModel: '',
  running: false,
  session: '',
  summary: null,
  models: { all: [], vision: [] },
  progress: [],               // 最近评分进度（{file, score, status, error}）
  stage: null,                // {stage, total}
  scan: null,                 // {count, est_cost, items}
  dir: '',                    // 运行页输入的源图目录（跨页面保留）
  selModel: '',               // 运行页选中的模型（跨页面保留）
  toast: null,
})

let toastTimer = null
export function toast(msg, isErr = false) {
  state.toast = { msg, err: isErr }
  clearTimeout(toastTimer)
  toastTimer = setTimeout(() => (state.toast = null), 3500)
}

export async function api(path, opts = {}) {
  const r = await fetch(path, {
    headers: { 'Content-Type': 'application/json' },
    ...opts,
  })
  let j = {}
  try { j = await r.json() } catch { /* 空响应 */ }
  if (!r.ok) throw new Error(j.error || `HTTP ${r.status}`)
  return j
}

export function applyState(s) {
  state.version = s.version || state.version
  state.config = s.config
  state.currentModel = s.currentModel || ''
  state.running = s.running
  state.session = s.session || ''
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
    toast('拉取模型失败：' + e.message, true)
    return { all: [], vision: [] }
  }
}

export function setTheme(t) {
  state.theme = t
  localStorage.setItem('sr_theme', t)
  document.documentElement.classList.toggle('dark', t === 'dark')
}

// SSE 实时进度
export function connectSSE() {
  const es = new EventSource('/api/events')
  es.addEventListener('state', (e) => {
    const d = JSON.parse(e.data)
    state.running = d.running
    if (d.session) state.session = d.session
  })
  es.addEventListener('stage', (e) => {
    state.stage = JSON.parse(e.data)
  })
  es.addEventListener('progress', (e) => {
    const p = JSON.parse(e.data)
    state.progress.unshift(p)
    if (state.progress.length > 100) state.progress.pop()
  })
  es.addEventListener('error', (e) => {
    if (e.data) {
      const d = JSON.parse(e.data)
      toast(d.error || '流水线错误', true)
    }
  })
  es.addEventListener('done', async (e) => {
    const d = JSON.parse(e.data)
    state.running = false
    state.stage = null
    await refreshState()
    if (d.stopped) toast('任务已停止')
    else toast(`评分完成（失败 ${d.failed} 张），请到「复核归档」查看`)
  })
  es.onerror = () => { /* 断线时浏览器自动重连 */ }
}
