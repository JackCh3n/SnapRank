<template>
  <div class="grid">
    <!-- 分布统计 -->
    <div class="card">
      <div class="head">
        <h3 class="title">批次分布</h3>
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
      <h3 class="title">评分复核 <span class="muted">（可在下拉中手动调档）</span></h3>
      <div class="photo-grid">
        <div v-for="p in photos" :key="p.id" class="photo-card">
          <div class="thumb-wrap">
            <img :src="`/api/thumb?id=${p.id}`" loading="lazy" @error="thumbFail(p)" />
            <span class="score-badge" :class="scoreClass(p.score)">{{ p.status === 'parse_fail' ? '复检' : p.score.toFixed(1) }}</span>
          </div>
          <div class="p-name" :title="p.src_path">{{ p.filename }}</div>
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
          <select class="bucket-sel" :value="p.override_bucket || autoBucket(p)" @change="setBucket(p, $event.target.value)">
            <option v-for="b in bucketNames" :key="b" :value="b">{{ b }}</option>
          </select>
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
  </div>
</template>

<script setup>
import { ref, computed, onMounted } from 'vue'
import { state, api, toast } from '../store.js'

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

const totalScored = computed(() => {
  const s = summary.value
  if (!s || !s.status) return 0
  return (s.status.scored || 0) + (s.status.parse_fail || 0)
})

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

async function loadPhotos() {
  try {
    const r = await api('/api/photos?page=1&page_size=200')
    photos.value = (r.items || []).filter((p) => p.status === 'scored' || p.status === 'parse_fail')
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
    const r = await api('/api/recalculate', { method: 'POST', body: '{}' })
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
    archiveResult.value = await api('/api/archive', { method: 'POST', body: JSON.stringify({ mode: mode.value }) })
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
  state.summary = r.summary
  state.config = r.config
  if (r.config) {
    w.value = { ...r.config.weights }
    archiveRoot.value = r.config.paths.archive_root
  }
  await loadPhotos()
})
</script>

<style scoped>
.grid { display: flex; flex-direction: column; gap: 14px; max-width: 1080px; }
.head { display: flex; align-items: baseline; gap: 14px; flex-wrap: wrap; }
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
.bucket-sel { margin: 6px 8px 8px; }
</style>
