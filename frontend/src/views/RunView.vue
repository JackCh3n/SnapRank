<template>
  <div class="grid">
    <div class="card">
      <h3 class="title">① 选择照片目录</h3>
      <div class="row">
        <label class="field grow">
          源图目录（递归扫描，支持 jpg/png/webp/gif/bmp/tiff）
          <input v-model="state.dir" placeholder="例如 D:\\Photos\\2026-08" @keyup.enter="doScan" />
        </label>
        <button class="btn plain" :disabled="state.running" @click="doScan">扫描</button>
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
        <button class="btn" :disabled="state.running || !state.dir" @click="doStart(true)">▶ 抽样试跑 10 张</button>
        <button class="btn" :disabled="state.running || !state.dir" @click="doStart(false)">▶ 开始评分</button>
        <button class="btn danger" :disabled="!state.running" @click="doStop">■ 停止</button>
      </div>
      <div class="muted" style="margin-top: 8px">
        当前模型：<b>{{ state.currentModel || '未设置' }}</b>
        <span v-if="state.selModel && state.selModel !== state.currentModel" class="tag">已选择 {{ state.selModel }}，下次开始时生效</span>
        · 不同模型的分数不可横向比较
      </div>
    </div>

    <div class="card" v-if="state.running || state.progress.length">
      <h3 class="title">③ 进度 {{ doneCount }}/{{ totalStr }}
        <span class="eta" v-if="etaText">{{ etaText }}</span>
      </h3>
      <div class="progress-track"><div class="progress-fill" :style="{ width: pct + '%' }"></div></div>
      <div class="muted" style="margin: 6px 0 10px">
        {{ stageLabel }}<span v-if="elapsedText"> · 已用时 {{ elapsedText }}</span>
      </div>
      <div class="feed">
        <div v-for="(p, i) in state.progress" :key="i" class="feed-item">
          <span class="mono">{{ p.status === 'scored' ? '✅' : p.status === 'parse_fail' ? '⚠️' : '❌' }}</span>
          <span class="fname" :title="p.file">{{ p.file }}</span>
          <span v-if="p.status === 'scored'" class="score">{{ p.score.toFixed(1) }} 分</span>
          <span v-else-if="p.status === 'parse_fail'" class="error-text">解析失败（待复检）</span>
          <span v-else class="error-text" :title="p.error">{{ statusLabel(p.status) }}</span>
          <span v-if="p.cached" class="tag">缓存</span>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, computed, onMounted, watch } from 'vue'
import { state, api, toast, refreshState, refreshModels } from '../store.js'

const loadingModels = ref(false)
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

const doneCount = computed(() => state.progress.length)
const totalStr = computed(() => (state.stage && state.stage.total) || state.progress.length || '?')
const pct = computed(() => {
  const t = (state.stage && state.stage.total) || 0
  return t ? Math.min(100, (state.progress.length / t) * 100) : 0
})

const stageLabel = computed(() => {
  const st = state.stage && state.stage.stage
  return { compress: '阶段：压缩图片（全部完成后开始评分）', score: '阶段：AI 评分' }[st] || ''
})

// 已用时（本批次开始时刻由进度首条事件近似）
const elapsedText = computed(() => {
  if (!state.running || !state.progress.length) return ''
  const first = state.progress[state.progress.length - 1]
  // progress 无时间戳，用组件内记录的 startedAt
  if (!batchStartedAt) return ''
  const sec = Math.round((Date.now() - batchStartedAt) / 1000)
  return sec >= 60 ? `${Math.floor(sec / 60)}分${sec % 60}秒` : `${sec}秒`
})

let batchStartedAt = 0
let etaTimer = null
const etaText = ref('')

function fmtEta(sec) {
  if (sec <= 0) return ''
  if (sec < 60) return `约 ${sec} 秒`
  return `约 ${Math.floor(sec / 60)} 分 ${Math.round(sec % 60)} 秒`
}

// 监听进度变化计算速率（滑动窗口：最近 8 个完成的间隔均值）
function tickEta() {
  if (!state.running || !state.stage || !state.progress.length) {
    etaText.value = ''
    return
  }
  const total = state.stage.total || 0
  const done = state.progress.length
  if (!total || done === 0) { etaText.value = ''; return }
  if (done >= total) { etaText.value = '即将完成…'; return }
  const el = (Date.now() - batchStartedAt) / 1000
  const rate = el / done // 秒/张（含并发摊销）
  etaText.value = '剩余 ' + fmtEta(Math.round((total - done) * rate))
}

watch(() => state.progress.length, (n, o) => {
  if (n === 1 && o === 0) batchStartedAt = Date.now()
})
watch(() => state.running, (r) => {
  if (r) { batchStartedAt = Date.now() }
  if (etaTimer) clearInterval(etaTimer)
  if (r) etaTimer = setInterval(tickEta, 1000)
  else { etaText.value = '' }
})

function statusLabel(s) {
  return { failed: '调用失败', bad_image: '图片无法解码', unsupported: '格式不支持', duplicate: '重复跳过' }[s] || s
}

async function doScan() {
  if (!state.dir) return toast('请先输入目录', true)
  try {
    const r = await api('/api/scan', { method: 'POST', body: JSON.stringify({ dir: state.dir }) })
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

async function doStart(sample) {
  try {
    if (state.selModel && state.selModel !== state.currentModel) {
      await api('/api/model', { method: 'POST', body: JSON.stringify({ id: state.selModel }) })
    }
    state.progress = []
    const r = await api('/api/start', {
      method: 'POST',
      body: JSON.stringify({ dir: state.dir, sample_n: sample ? 10 : 0 }),
    })
    state.session = r.session_id
    state.running = true
    toast(`已开始：会话 ${r.session_id}`)
  } catch (e) {
    toast(e.message, true)
  }
}

async function doStop() {
  try {
    await api('/api/stop', { method: 'POST', body: '{}' })
  } catch (e) {
    toast(e.message, true)
  }
}

onMounted(async () => {
  await refreshState()
  if (!state.selModel) state.selModel = state.currentModel
  if (!state.models.vision.length) loadModels()
})
</script>

<style scoped>
.grid { display: flex; flex-direction: column; gap: 14px; width: 100%; }
.grow { flex: 1; min-width: 260px; }
.feed { display: flex; flex-direction: column; gap: 4px; max-height: 340px; overflow-y: auto; }
.feed-item { display: flex; align-items: center; gap: 8px; font-size: 13px; padding: 3px 4px; border-radius: 6px; }
.feed-item:hover { background: var(--card-2); }
.fname { flex: 1; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.score { color: var(--accent); font-weight: 600; }
.mono { font-size: 13px; }
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
