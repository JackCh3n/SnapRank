import { reactive } from 'vue'

// 全局响应式状态
// 终态集合（与 RunView 的 FINAL 一致）：这些状态计入「已完成」计数
const FINAL_STATUS = new Set(['scored', 'parse_fail', 'failed', 'bad_image', 'unsupported'])

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
  selModel: '',                // 运行页选中的主模型（跨页面保留）
  compareModels: [],           // 多模型对比：除主模型外的对比模型（当前平台）
  compareCross: [],            // 跨平台对比：[{preset, model}] 引用预设平台的模型
  formatPreset: '',            // 运行页格式筛选（跨页面保留）
  importDir: '',               // 当前导入任务目录（贴入/拖入统一归类，后续追加）
  forcedFiles: new Set(),      // 强制评分标记：`session|文件名`（重评/复检入队的图片，忽略缓存重调）
  retryFiles: new Set(),       // 自动重试标记：`session|文件名`（失败后重新排队的图片）
  scoreFeed: [],               // 最近评完的 10 条（跨任务滚动，仅作展示）
  tally: { done: 0, fail: 0 }, // 本页面会话累计：已评完 / 失败张数
  lastDone: null,              // 最近一次完成事件 {stopped, session}：停止后剩余张数展示用
  toast: null,
})

const FINAL = new Set(['scored', 'parse_fail', 'failed', 'bad_image', 'unsupported'])

// 标记一批图片为强制评分（重评提交方调用；评分成功后自动摘除标记）
export function markForced(sessionId, filenames) {
  for (const f of filenames || []) state.forcedFiles.add(sessionId + '|' + f)
}

// 复检重评：与「开始评分」行为一致——先把运行页选中的模型应用为默认，再提交重评。
// 否则配置默认可能是已失效的旧模型（如平台已下架），重评会一直 404 重试。
export async function rescorePhotos(body) {
  if (state.selModel && state.currentModel && state.selModel !== state.currentModel) {
    await api('/api/model', { method: 'POST', body: JSON.stringify({ id: state.selModel }) })
    state.currentModel = state.selModel
  }
  return api('/api/rescore', { method: 'POST', body: JSON.stringify(body) })
}

// 获取（或初始化）某任务的进度容器。
// lastStatus/compN/finalN 是单调统计：feed 有 400 条上限会丢旧事件，
// 成功/剩余等数字不能从 feed 重数（大批次会出现「350/507 已完成但剩余 157」的假象）
export function taskOf(sid) {
  if (!sid) return null
  if (!state.tasks[sid]) {
    state.tasks[sid] = { progress: [], total: 0, stage: '', startedAt: 0, finished: false,
      lastStatus: {}, compN: 0, finalN: 0 }
  }
  return state.tasks[sid]
}

let toastTimer = null
export function toast(msg, isErr = false) {
  state.toast = { msg, err: isErr }
  clearTimeout(toastTimer)
  toastTimer = setTimeout(() => (state.toast = null), 3500)
}

// ---------- Windows 系统通知（浏览器 Web Notification = 系统 toast） ----------
// 夜间挂机场景页面保持打开，任务完成时弹系统级通知（Windows 通知中心可见）。
// 权限在开始评分时请求一次（用户在场，避免冷弹权限框）。
export function requestNotifyPermission() {
  try {
    if ('Notification' in window && Notification.permission === 'default') {
      Notification.requestPermission()
    }
  } catch { /* 不支持则静默 */ }
}

function notifyDesktop(title, body) {
  try {
    if (!('Notification' in window) || Notification.permission !== 'granted') return
    const n = new Notification(title, { body, tag: 'snaprank-task', silent: false })
    setTimeout(() => n.close(), 10000)
  } catch { /* 通知失败不影响主流程 */ }
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
    // 单调统计（feed 上限不影响）：按文件名记最新状态 + 压缩/完成计数
    t.lastStatus[d.file] = d.status
    if (d.status === 'compressed') t.compN++
    if (FINAL_STATUS.has(d.status)) t.finalN++
    // 全局累计与「最近评完」滚动列表（运行页只展示待评分 + 最近 10 条）
    if (d.status === 'scored') state.tally.done++
    else if (d.status === 'failed' || d.status === 'parse_fail') state.tally.fail++
    if (FINAL_STATUS.has(d.status)) {
      state.scoreFeed.push({ file: d.file, status: d.status, score: d.score, error: d.error, cached: d.cached, session_id: d.session_id, photo_id: d.photo_id })
      if (state.scoreFeed.length > 10) state.scoreFeed.splice(0, state.scoreFeed.length - 10)
    }
    // 评分成功：摘除强制/重试标记
    if (d.status === 'scored') {
      const key = d.session_id + '|' + d.file
      state.forcedFiles.delete(key)
      state.retryFiles.delete(key)
    }
  })
  es.addEventListener('night', (e) => {
    const d = JSON.parse(e.data)
    const t = taskOf(d.session_id)
    // 把上一轮失败的图片标记为重试（本轮将排队尾重新评分）
    if (t) {
      for (const p of t.progress) {
        if (p.status === 'failed' || p.status === 'parse_fail') state.retryFiles.add(d.session_id + '|' + p.file)
      }
    }
    toast(`🔁 自动重试第 ${d.round} 轮：剩余 ${d.remaining} 张，${d.delay_sec}s 后开始`)
    notifyDesktop('SnapRank 自动重试', `第 ${d.round} 轮：剩余 ${d.remaining} 张，${d.delay_sec}s 后开始`)
  })
  es.addEventListener('done', (e) => {
    const d = JSON.parse(e.data)
    const t = taskOf(d.session_id)
    if (t) { t.finished = true; t.stopped = !!d.stopped }
    state.lastDone = { stopped: !!d.stopped, session: d.session_id }
    // 不在此处 refreshState：done 与下一个任务的 queue 事件之间有一个引擎复位间隙，
    // 拉取到的 running=false 会覆盖随后到达的实时队列事件（推/拉竞态），
    // 导致下一个任务明明在跑前端却显示「本轮已评完」。队列状态只信 SSE 事件流。
    if (d.stopped) {
      toast('已停止：未评完的图片下次开始时自动继续')
      notifyDesktop('SnapRank 评分已停止', '未评完的图片下次继续')
    } else if (d.night) {
      const ok = d.failed > 0
      const msg = ok ? `仍有 ${d.failed} 张持续失败（已停止重试）` : '全部评分成功'
      toast(`🔁 自动重试结束：${msg}`)
      notifyDesktop(ok ? 'SnapRank 重试结束（有失败）' : 'SnapRank 评分全部完成', msg)
    } else {
      notifyDesktop('SnapRank 评分完成', '本轮全部评完')
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
