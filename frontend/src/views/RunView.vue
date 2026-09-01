<template>
  <div class="grid">
    <div class="card">
      <h3 class="title">① 选择照片目录</h3>
      <div class="row">
        <label class="field grow">
          源图目录（递归扫描，支持 jpg/png/webp/gif/bmp/tiff）
          <input v-model="state.dir" placeholder="例如 D:\\Photos\\2026-08" @keyup.enter="doScan" />
        </label>
        <button class="btn plain" :disabled="picking" :title="picking ? '请在弹出的窗口中选择目录' : '弹出系统目录选择框'"
          @click="pickDir">{{ picking ? '选择中…' : '📁 选择目录' }}</button>
        <label class="field">
          格式筛选
          <select v-model="formatPreset" style="width: 150px" @change="rescanIfScanned">
            <option value="">全部支持格式</option>
            <option value="jpg">仅 JPG</option>
            <option value="png">仅 PNG</option>
            <option value="webp">仅 WebP</option>
            <option value="web">JPG/PNG/WebP</option>
          </select>
        </label>
        <button class="btn plain" :disabled="state.running" @click="doScan">扫描</button>
      </div>
      <div v-if="formatPreset && state.scan" class="muted" style="margin-top: 4px">
        已按「{{ formatLabel }}」筛选，其他格式（含 RAF/HEIC 等相机 RAW）不会进入评分
      </div>
      <div v-if="dirHistory.length" class="dir-history">
        <span class="muted">最近：</span>
        <span v-for="d in dirHistory" :key="d" class="dir-chip" @click="useDir(d)" :title="d">
          {{ shortDir(d) }}
          <span class="chip-x" @click.stop="removeDir(d)" title="删除该标签">×</span>
        </span>
      </div>
      <div v-if="state.scan" class="muted" style="margin-top: 8px">
        共 {{ state.scan.count }} 张（批内去重后 {{ state.scan.live }} 张需评分），预估费用
        <b>¥{{ state.scan.est_cost.toFixed(3) }}</b>
        <span v-if="state.scan.dupCount > 0">（重复 {{ state.scan.dupCount }} 张跳过）</span>
      </div>
      <div v-if="state.config && state.config.provider.type === 'mock'" class="tag" style="margin-top: 8px">
        当前为 mock 离线模式：不调用平台、不计费，评分为演示数据
      </div>
    </div>

    <div class="card">
      <h3 class="title">② 选择打分模型（批次内锁定，切换后下一次生效）</h3>
      <div class="row">
        <label class="field grow">
          视觉模型（在线拉取）
          <select v-model="state.selModel">
            <option v-for="m in state.models.vision" :key="m" :value="m">{{ m }}</option>
            <option v-if="!state.models.vision.includes(state.selModel) && state.selModel" :value="state.selModel">{{ state.selModel }}（当前）</option>
          </select>
        </label>
        <button class="btn plain" :disabled="loadingModels" @click="loadModels">
          {{ loadingModels ? '拉取中…' : '刷新模型列表' }}
        </button>
        <button class="btn" :disabled="!state.dir" @click="doStart(true)">▶ 抽样试跑 10 张</button>
        <button class="btn" :disabled="!state.dir" @click="doStart(false)">▶ 开始评分</button>
        <button class="btn danger" :disabled="!state.queue.current" @click="doStop">■ 停止当前任务</button>
      </div>
      <div class="muted" style="margin-top: 8px">
        当前模型：<b>{{ state.currentModel || '未设置' }}</b>
        <span v-if="state.selModel && state.selModel !== state.currentModel" class="tag">已选择 {{ state.selModel }}，下次开始时生效</span>
        · 不同模型的分数不可横向比较
        <span v-if="queueCount > 0" class="tag" style="margin-left: 6px">📋 队列中还有 {{ queueCount }} 个任务</span>
      </div>
    </div>

    <div class="card" v-if="taskIds.length">
      <h3 class="title">
        ③ 任务进度
        <select v-model="selectedTask" class="task-sel">
          <option v-for="sid in taskIds" :key="sid" :value="sid">{{ taskLabel(sid) }}</option>
        </select>
      </h3>

      <template v-if="task">
        <template v-if="isQueued">
          <div class="muted">⏳ 排队中，等待前面的任务完成后自动开始…</div>
        </template>
        <template v-else>
          <h3 class="title sub">{{ stageLabel }} {{ doneCount }}/{{ totalStr }}
            <span class="eta" v-if="etaText">{{ etaText }}</span>
          </h3>
          <div class="progress-track"><div class="progress-fill" :style="{ width: pct + '%' }"></div></div>
          <div class="muted" style="margin: 6px 0 10px">
            <span v-if="elapsedText">已用时 {{ elapsedText }}</span>
          </div>
          <div class="feed">
            <div v-for="(p, i) in task.progress" :key="i" class="feed-item">
              <span class="mono">{{ iconOf(p.status) }}</span>
              <span class="fname" :title="p.file">{{ p.file }}</span>
              <span v-if="p.status === 'scored'" class="score">{{ p.score.toFixed(1) }} 分</span>
              <span v-else-if="p.status === 'parse_fail'" class="error-text">解析失败（待复检）</span>
              <span v-else-if="p.status !== 'compressed'" class="error-text" :title="p.error">{{ statusLabel(p.status) }}</span>
              <span v-if="p.cached" class="tag">缓存</span>
            </div>
          </div>
        </template>
      </template>
    </div>
  </div>
</template>

<script setup>
import { ref, computed, onMounted, onBeforeUnmount, watch } from 'vue'
import { state, taskOf, api, toast, refreshState, refreshModels } from '../store.js'

const loadingModels = ref(false)
const picking = ref(false)
const formatPreset = ref('')

const PRESETS = {
  '': [],
  jpg: ['jpg', 'jpeg'],
  png: ['png'],
  webp: ['webp'],
  web: ['jpg', 'jpeg', 'png', 'webp'],
}
const formatLabel = computed(() => ({ jpg: '仅 JPG', png: '仅 PNG', webp: '仅 WebP', web: 'JPG/PNG/WebP' }[formatPreset.value] || ''))
const formatsArg = computed(() => PRESETS[formatPreset.value] || [])

function rescanIfScanned() {
  if (state.scan) doScan() // 已有扫描结果时切换格式立即重扫
}

// 弹出系统目录选择框（由本机服务端弹出），选完自动扫描
async function pickDir() {
  picking.value = true
  try {
    const r = await api('/api/pick-dir', { method: 'POST', body: '{}' })
    if (r.dir) {
      state.dir = r.dir
      toast(`已选择：${r.dir}`)
      doScan()
    } else {
      toast('已取消选择')
    }
  } catch (e) {
    toast(e.message, true)
  }
  picking.value = false
}
const dirHistory = computed(() => (state.config && state.config.dir_history) || [])

function shortDir(d) {
  const parts = d.split(/[\/]+/).filter(Boolean)
  return parts.length > 2 ? '…/' + parts.slice(-2).join('/') : d
}
function useDir(d) {
  state.dir = d
  doScan()
}
async function removeDir(d) {
  try {
    await api('/api/dir-history/remove', { method: 'POST', body: JSON.stringify({ dir: d }) })
    const r = await api('/api/state')
    state.config = r.config
  } catch (e) {
    toast(e.message, true)
  }
}

// ---------- 多任务 ----------
const FINAL = new Set(['scored', 'parse_fail', 'failed', 'bad_image', 'unsupported'])
const selectedTask = ref('')

// 任务列表：运行中 → 排队 → 其他（含已完成）
const taskIds = computed(() => {
  const ids = []
  const cur = state.queue.current
  if (cur) ids.push(cur)
  for (const q of state.queue.queued) if (!ids.includes(q)) ids.push(q)
  for (const k of Object.keys(state.tasks)) if (!ids.includes(k)) ids.push(k)
  return ids
})
const queueCount = computed(() => state.queue.queued.length)

function taskLabel(sid) {
  const t = state.tasks[sid] || {}
  let st = ''
  if (sid === state.queue.current) st = '▶ 运行中'
  else if (state.queue.queued.includes(sid)) st = '⏳ 排队中'
  else if (t.finished) st = '✓ 已完成'
  return (st ? st + ' ' : '') + sid
}

const task = computed(() => taskOf(selectedTask.value))
const isQueued = computed(() => {
  const sid = selectedTask.value
  return !!sid && sid !== state.queue.current && state.queue.queued.includes(sid)
})

const stage = computed(() => (task.value && task.value.stage) || '')
const total = computed(() => (task.value && task.value.total) || 0)
const compressDone = computed(() => (task.value ? task.value.progress.filter((x) => x.status === 'compressed').length : 0))
const scoreDone = computed(() => (task.value ? task.value.progress.filter((x) => FINAL.has(x.status)).length : 0))
const doneCount = computed(() => (stage.value === 'compress' ? compressDone.value : scoreDone.value))
const totalStr = computed(() => total.value || '?')
const pct = computed(() => (total.value ? Math.min(100, (doneCount.value / total.value) * 100) : 0))

const stageLabel = computed(() =>
  ({ compress: '阶段一：压缩图片', score: '阶段二：AI 评分' }[stage.value] || '准备中…'))

const elapsedText = computed(() => {
  const t = task.value
  if (!t || !t.startedAt || t.finished) return ''
  const sec = Math.round((Date.now() - t.startedAt) / 1000)
  return sec >= 60 ? `${Math.floor(sec / 60)}分${sec % 60}秒` : `${sec}秒`
})

function fmtEta(sec) {
  if (sec <= 0) return ''
  if (sec < 60) return `剩余 约 ${sec} 秒`
  return `剩余 约 ${Math.floor(sec / 60)} 分 ${Math.round(sec % 60)} 秒`
}

// ETA：按当前阶段内最近完成事件的速率估算（每秒刷新由 tick 定时器驱动）
const etaText = ref('')
let etaTimer = null
function tickEta() {
  const t = taskOf(selectedTask.value)
  if (!t || t.finished || !t.total || !t.progress.length) { etaText.value = ''; return }
  const done = stage.value === 'compress' ? compressDone.value : scoreDone.value
  if (done <= 0) { etaText.value = ''; return }
  if (done >= t.total) { etaText.value = '即将完成…'; return }
  // 当前阶段内首尾事件的时间跨度（压缩与评分交界处会短暂偏大，可接受）
  const stageStatuses = stage.value === 'compress'
    ? t.progress.filter((x) => x.status === 'compressed')
    : t.progress.filter((x) => FINAL.has(x.status))
  const span = (stageStatuses[stageStatuses.length - 1]._ts - stageStatuses[0]._ts) / 1000
  const rate = span / Math.max(1, stageStatuses.length - 1)
  etaText.value = fmtEta(Math.round((t.total - done) * rate))
}
watch(selectedTask, () => tickEta())
let etaTimer2 = etaTimer
onMounted(() => { etaTimer2 = setInterval(tickEta, 1000) })
onBeforeUnmount(() => clearInterval(etaTimer2))

watch(taskIds, (ids) => {
  if (!ids.includes(selectedTask.value)) selectedTask.value = ids[0] || ''
}, { immediate: true })

function iconOf(status) {
  if (status === 'scored') return '✅'
  if (status === 'parse_fail') return '⚠️'
  if (status === 'compressed') return '🗜️'
  return '❌'
}
function statusLabel(s) {
  return { failed: '调用失败', bad_image: '图片无法解码', unsupported: '格式不支持', duplicate: '重复跳过', compressed: '已压缩' }[s] || s
}

async function doScan() {
  if (!state.dir) return toast('请先输入目录', true)
  try {
    const r = await api('/api/scan', { method: 'POST', body: JSON.stringify({ dir: state.dir, formats: formatsArg.value }) })
    const live = r.items.filter((i) => !i.dup).length
    const dupCount = r.items.length - live
    state.scan = { count: r.items.length, live, est_cost: r.est_cost, dupCount }
    refreshState() // 拉取最新目录历史
  } catch (e) {
    state.scan = null
    toast(e.message, true)
  }
}

async function loadModels() {
  loadingModels.value = true
  const m = await refreshModels()
  loadingModels.value = false
  if (m.vision.length === 0) toast('未发现视觉模型，可到设置调整识别规则', true)
  else if (!m.vision.includes(state.selModel)) state.selModel = m.vision[0]
}

// 开始评分：任务创建后立即返回（后台排队执行），可继续添加新任务
async function doStart(sample) {
  try {
    if (state.selModel && state.selModel !== state.currentModel) {
      await api('/api/model', { method: 'POST', body: JSON.stringify({ id: state.selModel }) })
    }
    const r = await api('/api/start', {
      method: 'POST',
      body: JSON.stringify({ dir: state.dir, sample_n: sample ? 10 : 0, formats: formatsArg.value }),
    })
    selectedTask.value = r.session_id
    taskOf(r.session_id)
    const queued = state.queue.queued.includes(r.session_id)
    toast(queued ? `任务已排队：${r.session_id}（前方还有 ${state.queue.queued.indexOf(r.session_id)} 个任务）` : `任务已开始：${r.session_id}`)
    refreshState()
  } catch (e) {
    toast(e.message, true)
  }
}

async function doStop() {
  try {
    await api('/api/stop', { method: 'POST', body: '{}' })
    toast('已请求停止当前任务（排队任务不受影响）')
  } catch (e) {
    toast(e.message, true)
  }
}

// 刷新页面后，从数据库恢复运行中/排队任务的已产生进度（事件是瞬态的）
async function hydrateTasks() {
  const ids = [state.queue.current, ...state.queue.queued].filter(Boolean)
  for (const sid of ids) {
    try {
      const r = await api(`/api/photos?page=1&page_size=200&session=${sid}`)
      const t = taskOf(sid)
      t.progress = r.items
        .filter((p) => p.status === 'compressed' || FINAL.has(p.status))
        .map((p) => ({ file: p.filename, score: p.score, status: p.status, error: p.error, cached: p.source === 'cache', _ts: 0 }))
      t.total = r.total
      if (!t.stage) t.stage = 'score'
    } catch { /* 会话不可读则跳过 */ }
  }
}

onMounted(async () => {
  await refreshState()
  await hydrateTasks()
  if (!state.selModel) state.selModel = state.currentModel
  if (!state.models.vision.length) loadModels()
})
</script>

<style scoped>
.grid { display: flex; flex-direction: column; gap: 14px; width: 100%; }
.grow { flex: 1; min-width: 260px; }
.task-sel { max-width: 340px; font-size: 13px; padding: 4px 8px; vertical-align: middle; }
.title.sub { margin-top: 6px; font-size: 14px; }
.feed { display: flex; flex-direction: column; gap: 4px; max-height: 340px; overflow-y: auto; }
.feed-item { display: flex; align-items: center; gap: 8px; font-size: 13px; padding: 3px 4px; border-radius: 6px; }
.feed-item:hover { background: var(--card-2); }
.fname { flex: 1; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.score { color: var(--accent); font-weight: 600; }
.mono { font-size: 13px; }
.eta { color: var(--text-2); font-size: 13px; font-weight: 400; margin-left: 8px; }
.dir-history { display: flex; gap: 6px; flex-wrap: wrap; align-items: center; margin-top: 8px; }
.dir-chip {
  display: inline-flex; align-items: center; gap: 4px;
  background: var(--card-2); border: 1px solid var(--line); border-radius: 999px;
  padding: 2px 10px; font-size: 12px; cursor: pointer; max-width: 260px;
}
.dir-chip:hover { border-color: var(--accent); color: var(--accent); }
.chip-x { color: var(--text-2); font-size: 13px; padding: 0 1px; }
.chip-x:hover { color: var(--danger); }
</style>
