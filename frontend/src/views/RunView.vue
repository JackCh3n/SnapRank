<template>
  <div class="grid">
    <div class="card">
      <h3 class="title">① 选择照片目录</h3>
      <div class="row">
        <label class="field grow">
          源图目录（递归扫描，支持 jpg/png/webp/gif/bmp/tiff）
          <input v-model="dir" placeholder="例如 D:\\Photos\\2026-08" @keyup.enter="doScan" />
        </label>
        <button class="btn plain" :disabled="state.running" @click="doScan">扫描</button>
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
          <select v-model="model">
            <option v-for="m in state.models.vision" :key="m" :value="m">{{ m }}</option>
            <option v-if="!state.models.vision.includes(model) && model" :value="model">{{ model }}（当前）</option>
          </select>
        </label>
        <button class="btn plain" :disabled="loadingModels" @click="loadModels">
          {{ loadingModels ? '拉取中…' : '刷新模型列表' }}
        </button>
        <button class="btn" :disabled="state.running || !dir" @click="doStart(true)">▶ 抽样试跑 10 张</button>
        <button class="btn" :disabled="state.running || !dir" @click="doStart(false)">▶ 开始评分</button>
        <button class="btn danger" :disabled="!state.running" @click="doStop">■ 停止</button>
      </div>
      <div class="muted" style="margin-top: 8px">
        当前模型：<b>{{ state.currentModel || '未设置' }}</b>
        <span v-if="model && model !== state.currentModel" class="tag">已选择 {{ model }}，下次开始时生效</span>
        · 不同模型的分数不可横向比较
      </div>
    </div>

    <div class="card" v-if="state.running || state.progress.length">
      <h3 class="title">③ 进度 {{ doneCount }}/{{ totalStr }}</h3>
      <div class="progress-track"><div class="progress-fill" :style="{ width: pct + '%' }"></div></div>
      <div class="muted" style="margin: 6px 0 10px">
        {{ state.stage ? `阶段：${state.stage.stage === 'score' ? '压缩+评分' : state.stage.stage}` : '' }}
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
import { ref, computed, onMounted } from 'vue'
import { state, api, toast, refreshState, refreshModels } from '../store.js'

const dir = ref('')
const model = ref('')
const loadingModels = ref(false)

const doneCount = computed(() => state.progress.length)
const totalStr = computed(() => (state.stage && state.stage.total) || state.progress.length || '?')
const pct = computed(() => {
  const t = (state.stage && state.stage.total) || 0
  return t ? Math.min(100, (state.progress.length / t) * 100) : 0
})

function statusLabel(s) {
  return { failed: '调用失败', bad_image: '图片无法解码', unsupported: '格式不支持', duplicate: '重复跳过' }[s] || s
}

async function doScan() {
  if (!dir.value) return toast('请先输入目录', true)
  try {
    const r = await api('/api/scan', { method: 'POST', body: JSON.stringify({ dir: dir.value }) })
    const live = r.items.filter((i) => !i.dup).length
    const dupCount = r.items.length - live
    state.scan = { count: r.items.length, live, est_cost: r.est_cost, dupCount }
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
  else if (!m.vision.includes(model.value)) model.value = m.vision[0]
}

async function doStart(sample) {
  try {
    if (model.value && model.value !== state.currentModel) {
      await api('/api/model', { method: 'POST', body: JSON.stringify({ id: model.value }) })
    }
    state.progress = []
    const r = await api('/api/start', {
      method: 'POST',
      body: JSON.stringify({ dir: dir.value, sample_n: sample ? 10 : 0 }),
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
  dir.value = state.config && state.config.paths ? '' : ''
  model.value = state.currentModel
  if (!state.models.vision.length) loadModels()
})
</script>

<style scoped>
.grid { display: flex; flex-direction: column; gap: 14px; max-width: 1080px; }
.grow { flex: 1; min-width: 260px; }
.feed { display: flex; flex-direction: column; gap: 4px; max-height: 340px; overflow-y: auto; }
.feed-item { display: flex; align-items: center; gap: 8px; font-size: 13px; padding: 3px 4px; border-radius: 6px; }
.feed-item:hover { background: var(--card-2); }
.fname { flex: 1; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.score { color: var(--accent); font-weight: 600; }
.mono { font-size: 13px; }
</style>
