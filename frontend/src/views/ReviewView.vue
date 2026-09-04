<template>
  <div class="grid">
    <!-- 分布统计 -->
    <div class="card">
      <div class="head">
        <h3 class="title">批次分布</h3>
        <select v-model="sessionID" @change="onSessionChange" class="session-sel" title="切换历史会话">
          <option v-for="s in sessions" :key="s.id" :value="s.id">{{ s.name || s.id }}（{{ shortDir(s.source_dir) }}，{{ s.done || 0 }} 张）</option>
        </select>
        <button class="btn plain small" @click="showManager = true">⚙ 管理</button>
        <div class="muted" v-if="summary">
          均分 <b>{{ summary.avg_score.toFixed(1) }}</b> · 最高 <b>{{ summary.max_score.toFixed(1) }}</b> ·
          预估费用 ¥{{ summary.est_cost.toFixed(3) }}
          <span v-if="summary.archived" class="tag">已归档</span>
        </div>
      </div>
      <div v-if="buckets.length" class="bars">
        <div v-for="b in buckets" :key="b.name" class="bar-item">
          <div class="bar-num">{{ b.count }}</div>
          <div class="bar-track">
            <div class="bar-fill" :style="{ height: barH(b.count), background: b.color }"></div>
          </div>
          <div class="bar-name">{{ b.name }}</div>
        </div>
      </div>
      <div v-else class="muted">暂无评分结果：请先到「运行」页完成一次评分</div>
      <div class="row" style="margin-top: 10px" v-if="failTotal > 0">
        <button class="btn plain" :disabled="state.running" @click="rescoreAllFailed">
          ↻ 一键重试全部失败（{{ failTotal }} 张{{ failDetail }}）
        </button>
        <span class="muted">重新调用 AI 评分（忽略缓存），成功后自动回到对应分数档</span>
      </div>
    </div>

    <!-- 权重与本地重算 -->
    <div class="card">
      <h3 class="title">评分权重（修改后本地重算，0 API 成本）</h3>
      <div class="row">
        <label class="field">技术质量 <input type="number" step="0.05" min="0" max="1" v-model="w.technique" /></label>
        <label class="field">构图 <input type="number" step="0.05" min="0" max="1" v-model="w.composition" /></label>
        <label class="field">内容情感 <input type="number" step="0.05" min="0" max="1" v-model="w.content" /></label>
        <label class="field">色彩 <input type="number" step="0.05" min="0" max="1" v-model="w.color" /></label>
        <button class="btn plain" :disabled="recalcing" @click="saveAndRecalc">保存并重算</button>
        <span class="muted">权重和无需恰好为 1，后台自动归一</span>
      </div>
    </div>

    <!-- 照片网格 -->
    <div class="card" v-if="photos.length">
      <h3 class="title">评分复核 <span class="muted">（可在下拉中手动调档）</span>
        <select v-model="scope" @change="loadPhotos" class="scope-sel">
          <option value="scored">已评分照片</option>
          <option value="all">全部照片（含失败/待处理）</option>
        </select>
      </h3>
      <div class="photo-grid">
        <div v-for="p in photos" :key="p.id" class="photo-card">
          <div class="thumb-wrap clickable" :class="{ 'parse-fail': p.status === 'parse_fail' }"
            @click="preview = p" title="点击查看大图与评分详情">
            <img v-if="!p._noThumb" :src="`/api/thumb?id=${p.id}`" loading="lazy" @error="thumbFail(p)" />
            <div v-else class="no-thumb">缓存命中<br />无压缩图</div>
            <span class="score-badge" :class="badgeClass(p)">{{ badgeText(p) }}</span>
          </div>
          <div class="p-name" :title="p.src_path">{{ p.filename }}</div>
          <div class="p-dims muted" v-if="p.error && p.status !== 'scored' && p.status !== 'parse_fail'">
            <span class="error-text">{{ p.error }}</span>
          </div>
          <div class="p-dims muted" v-if="p.dims">
            技{{ p.dims.technique.toFixed(1) }} 构{{ p.dims.composition.toFixed(1) }}
            容{{ p.dims.content.toFixed(1) }} 色{{ p.dims.color.toFixed(1) }}
          </div>
          <div class="p-tags" v-if="p.tags && p.tags.length">
            <span v-for="t in p.tags" :key="t" class="tag">{{ t }}</span>
          </div>
          <div class="p-reason muted" v-if="p.strength" :title="`${p.strength}\n不足：${p.weakness}`">
            {{ p.strength }}
          </div>
          <div class="card-actions">
            <select class="bucket-sel" :value="p.override_bucket || autoBucket(p)" @change="setBucket(p, $event.target.value)">
              <option v-for="b in bucketNames" :key="b" :value="b">{{ b }}</option>
            </select>
            <button class="btn plain small" :disabled="state.running" title="重新调用 AI 评分（忽略缓存）"
              @click="rescoreOne(p)">↻ 复检</button>
          </div>
        </div>
      </div>
    </div>

    <!-- 归档确认（两阶段） -->
    <div class="card" v-if="summary && totalScored > 0">
      <h3 class="title">确认归档 <span class="muted">（默认复制，移动需显式选择）</span></h3>
      <div class="row">
        <label class="field">
          归档方式
          <select v-model="mode">
            <option value="copy">复制（源图不动，推荐）</option>
            <option value="move">移动（源图移入归档目录）</option>
          </select>
        </label>
        <label class="field">
          归档根目录
          <input style="width: 320px" v-model="archiveRoot" />
        </label>
        <button class="btn" :disabled="archiving || state.running" @click="doArchive">执行归档</button>
        <button class="btn plain" v-if="summary.archive_dir" @click="openFolder">打开归档目录</button>
      </div>
      <div v-if="archiveResult" class="muted" style="margin-top: 8px">
        ✅ 放置 {{ archiveResult.placed }} · 跳过 {{ archiveResult.skipped }} · 失败 {{ archiveResult.failed }} →
        {{ archiveResult.dir }}
        <a v-if="archiveResult.report_csv" href="/api/report" download>下载 report.csv</a>
        <div v-for="(e, i) in archiveResult.errors || []" :key="i" class="error-text">{{ e }}</div>
      </div>
      <div class="muted" style="margin-top: 6px">
        说明：解析失败的照片不会移动，仅记录在报告「29_待复检」；解码失败/不支持的文件留在原目录。
      </div>
    </div>

    <!-- 统一照片详情弹窗 -->
    <PhotoModal v-if="preview" :photo="preview" :busy="state.running" :list="photos"
      @close="closePreview" @navigate="onNavigate" @deleted="onPhotoDeleted" />

    <!-- 批次管理弹窗 -->
    <SessionManager v-if="showManager" :sessions="sessions" :current="sessionID"
      @close="showManager = false" @changed="onSessionsChanged" />
  </div>
</template>

<script setup>
import { ref, computed, onMounted } from 'vue'
import { state, api, toast, markForced } from '../store.js'
import PhotoModal from '../components/PhotoModal.vue'
import SessionManager from '../components/SessionManager.vue'

const bucketNames = ['35_精选', '34_良好', '33_一般', '30_待清理', '29_待复检']
const bucketColors = ['#07c160', '#10aeff', '#ffa300', '#888888', '#fa5151']

const w = ref({ technique: 0.4, composition: 0.3, content: 0.2, color: 0.1 })
const mode = ref('copy')
const archiveRoot = ref('')
const photos = ref([])
const summary = computed(() => state.summary)
const recalcing = ref(false)
const archiving = ref(false)
const archiveResult = ref(null)
const preview = ref(null)
const sessionID = ref('')
const sessions = ref([])
const showManager = ref(false)

// 批次管理变更后：刷新列表；当前查看的批次被删则回落到最新
async function onSessionsChanged() {
  await loadSessions()
  if (!sessions.value.find((x) => x.id === sessionID.value)) {
    sessionID.value = sessions.value.length ? sessions.value[0].id : ''
  }
  await Promise.all([loadSummary(), loadPhotos()])
}

function shortDir(d) {
  if (!d) return ''
  const parts = d.split(/[\\/]+/).filter(Boolean)
  return parts.length > 2 ? '…/' + parts.slice(-2).join('/') : d
}

async function onSessionChange() {
  await Promise.all([loadSummary(), loadPhotos()])
}

async function loadSummary() {
  const r = await api(`/api/summary?session=${sessionID.value}`).catch(() => null)
  state.summary = r
}

async function loadSessions() {
  try {
    sessions.value = await api('/api/sessions')
    if (!sessionID.value && sessions.value.length) sessionID.value = sessions.value[0].id
  } catch { /* 无会话 */ }
}

// 弹窗评分明细行：维度分 + 归一化权重
function dimRows(p) {
  const w = (state.config && state.config.weights) || { technique: 0.4, composition: 0.3, content: 0.2, color: 0.1 }
  const sum = w.technique + w.composition + w.content + w.color || 1
  return [
    { key: 'technique', name: '技术质量', value: p.dims.technique, weight: w.technique / sum, weightPct: Math.round(w.technique / sum * 100), color: '#07c160' },
    { key: 'composition', name: '构图', value: p.dims.composition, weight: w.composition / sum, weightPct: Math.round(w.composition / sum * 100), color: '#10aeff' },
    { key: 'content', name: '内容情感', value: p.dims.content, weight: w.content / sum, weightPct: Math.round(w.content / sum * 100), color: '#ffa300' },
    { key: 'color', name: '色彩', value: p.dims.color, weight: w.color / sum, weightPct: Math.round(w.color / sum * 100), color: '#af52de' },
  ]
}

const totalScored = computed(() => {
  const s = summary.value
  if (!s || !s.status) return 0
  return (s.status.scored || 0) + (s.status.parse_fail || 0)
})

const parseFailCount = computed(() => {
  const s = summary.value
  return (s && s.status && s.status.parse_fail) || 0
})
const failedCount = computed(() => {
  const s = summary.value
  return (s && s.status && s.status.failed) || 0
})
const failTotal = computed(() => parseFailCount.value + failedCount.value)
const failDetail = computed(() => {
  const p = parseFailCount.value, f = failedCount.value
  if (p && f) return `解析失败 ${p} · 调用失败 ${f}`
  if (p) return '解析失败'
  return '调用失败'
})

function closePreview() {
  preview.value = null
  // 复检可能已改变评分，关闭时刷新汇总
  api('/api/state').then((r) => { state.summary = r.summary }).catch(() => {})
}

function onNavigate(newPhoto) {
  preview.value = newPhoto
}

// 弹窗内删除了源文件：从列表移除并刷新汇总
function onPhotoDeleted(p) {
  photos.value = photos.value.filter((x) => x.id !== p.id)
  preview.value = null
  api('/api/state').then((r) => { state.summary = r.summary }).catch(() => {})
}

async function rescoreOne(p) {
  try {
    const r = await api('/api/rescore', { method: 'POST', body: JSON.stringify({ ids: [p.id], force: true }) })
    markForced(r.session_id, [p.filename])
    toast(`已提交复检：${p.filename}，完成后自动刷新`)
  } catch (e) {
    toast(e.message, true)
  }
}

async function rescoreAllFailed() {
  try {
    const r = await api('/api/rescore', { method: 'POST', body: JSON.stringify({ all: true }) })
    // 全部失败重试：把当前会话清单里失败照片标记为强制
    const failed = photos.value.filter((x) => x.status === 'parse_fail' || x.status === 'failed')
    markForced(r.session_id, failed.map((x) => x.filename))
    toast(`已提交 ${r.count} 张失败重试`)
    setTimeout(() => { onSessionChange() }, 1500) // 重试完成后刷新
  } catch (e) {
    toast(e.message, true)
  }
}

const buckets = computed(() => {
  const s = summary.value
  if (!s || !s.buckets) return []
  return bucketNames
    .map((name, i) => ({ name, count: s.buckets[name] || 0, color: bucketColors[i] }))
    .filter((b) => b.count > 0)
})

function barH(count) {
  const max = Math.max(...buckets.value.map((b) => b.count), 1)
  return Math.max(8, (count / max) * 100) + '%'
}

function scoreClass(v) {
  if (v >= 9) return 's-best'
  if (v >= 7) return 's-good'
  if (v >= 5) return 's-mid'
  return 's-low'
}

function autoBucket(p) {
  if (p.status === 'parse_fail') return '29_待复检'
  const t = (state.config && state.config.score.thresholds) || [9, 7, 5]
  if (p.score >= t[0]) return '35_精选'
  if (p.score >= t[1]) return '34_良好'
  if (p.score >= t[2]) return '33_一般'
  return '30_待清理'
}

function thumbFail(p) { p._noThumb = true }

// 缓存命中但无压缩图的照片（老批次）正常参与列表展示

const scope = ref('scored')

function badgeText(p) {
  if (p.status === 'parse_fail') return '复检'
  if (p.status === 'scored') return p.score.toFixed(1)
  return { failed: '调用失败', bad_image: '无法解码', unsupported: '不支持', duplicate: '重复', pending: '待处理', compressed: '已压缩' }[p.status] || p.status
}
function badgeClass(p) {
  if (p.status === 'scored') return scoreClass(p.score)
  return 'bad'
}

async function loadPhotos() {
  try {
    const r = await api(`/api/photos?page=1&page_size=500&session=${sessionID.value}`)
    let list = r.items || []
    if (scope.value === 'scored') {
      list = list.filter((p) => p.status === 'scored' || p.status === 'parse_fail')
    }
    photos.value = list
  } catch { /* 尚无会话 */ }
}

async function saveAndRecalc() {
  recalcing.value = true
  try {
    const cfg = state.config
    await api('/api/config', {
      method: 'POST',
      body: JSON.stringify({ weights: {
        technique: +w.value.technique, composition: +w.value.composition,
        content: +w.value.content, color: +w.value.color } }),
    })
    const r = await api('/api/recalculate', { method: 'POST', body: JSON.stringify({ session: sessionID.value }) })
    toast(`已按新权重重算 ${r.recalculated} 张`)
    await loadPhotos()
  } catch (e) {
    toast(e.message, true)
  }
  recalcing.value = false
}

async function setBucket(p, bucket) {
  try {
    await api('/api/photo/bucket', { method: 'POST', body: JSON.stringify({ id: p.id, bucket }) })
    p.override_bucket = bucket
    toast(`已调档：${p.filename} → ${bucket}`)
  } catch (e) {
    toast(e.message, true)
  }
}

async function doArchive() {
  if (mode.value === 'move' && !confirm('移动模式会把这些照片从源目录移入归档目录，确认继续？')) return
  archiving.value = true
  try {
    if (archiveRoot.value && archiveRoot.value !== state.config.paths.archive_root) {
      await api('/api/config', { method: 'POST', body: JSON.stringify({ paths: { ...state.config.paths, archive_root: archiveRoot.value } }) })
    }
    archiveResult.value = await api('/api/archive', { method: 'POST', body: JSON.stringify({ mode: mode.value, session: sessionID.value }) })
    toast('归档完成')
  } catch (e) {
    toast(e.message, true)
  }
  archiving.value = false
}

async function openFolder() {
  const d = archiveResult.value ? archiveResult.value.dir : (summary.value && summary.value.archive_dir)
  if (!d) return
  await api('/api/open', { method: 'POST', body: JSON.stringify({ path: d }) }).catch((e) => toast(e.message, true))
}

onMounted(async () => {
  const r = await api('/api/state')
  state.config = r.config
  if (r.config) {
    w.value = { ...r.config.weights }
    archiveRoot.value = r.config.paths.archive_root
  }
  await loadSessions()
  await Promise.all([loadSummary(), loadPhotos()])
})
</script>

<style scoped>
.grid { display: flex; flex-direction: column; gap: 14px; width: 100%; }
.head { display: flex; align-items: baseline; gap: 14px; flex-wrap: wrap; }
.session-sel { max-width: 320px; font-size: 13px; padding: 5px 8px; }
.bars { display: flex; gap: 26px; align-items: flex-end; padding: 6px 4px; }
.bar-item { display: flex; flex-direction: column; align-items: center; gap: 4px; width: 72px; }
.bar-num { font-weight: 600; }
.bar-track { height: 90px; width: 40px; background: var(--card-2); border-radius: 8px; display: flex; align-items: flex-end; overflow: hidden; }
.bar-fill { width: 100%; border-radius: 8px 8px 0 0; transition: height 0.3s; }
.bar-name { font-size: 12px; color: var(--text-2); }
.photo-grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(150px, 1fr)); gap: 12px; }
.photo-card { background: var(--card-2); border-radius: 10px; overflow: hidden; display: flex; flex-direction: column; }
.thumb-wrap { position: relative; aspect-ratio: 4/3; background: #ddd; }
html.dark .thumb-wrap { background: #333; }
.thumb-wrap img { width: 100%; height: 100%; object-fit: cover; display: block; }
.score-badge {
  position: absolute; right: 6px; top: 6px; padding: 2px 8px; border-radius: 999px;
  color: #fff; font-weight: 700; font-size: 13px; background: #888;
}
.s-best { background: #07c160; } .s-good { background: #10aeff; }
.s-mid { background: #ffa300; } .s-low { background: #fa5151; }
.p-name { padding: 6px 8px 0; font-size: 13px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.p-dims { padding: 2px 8px 0; }
.p-tags { padding: 4px 8px 0; display: flex; gap: 4px; flex-wrap: wrap; }
.p-reason { padding: 4px 8px; font-size: 12px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.card-actions { display: flex; gap: 6px; align-items: center; margin: 6px 8px 8px; }
.card-actions .bucket-sel { margin: 0; flex: 1; min-width: 0; }
.parse-fail { outline: 2px solid var(--danger); outline-offset: -2px; border-radius: 10px; }
.scope-sel { font-size: 13px; padding: 4px 8px; vertical-align: middle; max-width: 240px; }
.no-thumb {
  width: 100%; height: 100%; display: flex; align-items: center; justify-content: center;
  color: var(--text-2); font-size: 12px; text-align: center; background: var(--card-2);
}
.thumb-wrap.clickable { cursor: zoom-in; }

</style>
