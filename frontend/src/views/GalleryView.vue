<template>
  <div class="grid">
    <div class="card">
      <div class="row" style="justify-content: space-between">
        <div class="row">
          <label class="field">
            分数段
            <select v-model="band" @change="applyFilters">
              <option value="">全部</option>
              <option value="high">高分（精选档）</option>
              <option value="mid">中分（良好档）</option>
              <option value="low">低分（一般及以下）</option>
            </select>
          </label>
          <label class="field">
            搜索文件名
            <input v-model="search" style="width: 160px" @input="applyFilters" placeholder="留空=全部" />
          </label>
          <label class="field">
            排序
            <select v-model="sortBy" @change="applyFilters" style="width: 110px">
              <option value="score-desc">分数 ↓</option>
              <option value="score-asc">分数 ↑</option>
              <option value="name-asc">文件名 ↑</option>
              <option value="name-desc">文件名 ↓</option>
            </select>
          </label>
          <label class="field row" style="flex-direction: row; align-items: center; gap: 4px" title="按分数区间筛选（只作用于已评分照片；两个都填同一值即精确匹配）">
            分数
            <input v-model.number="scoreMin" type="number" min="0" max="10" step="0.1" style="width: 56px; text-align: center" placeholder="低" @change="applyFilters" />
            ~
            <input v-model.number="scoreMax" type="number" min="0" max="10" step="0.1" style="width: 56px; text-align: center" placeholder="高" @change="applyFilters" />
          </label>
          <label class="field row" style="flex-direction: row; align-items: center; gap: 6px">
            <input v-model="onlySelected" type="checkbox" @change="applyFilters" /> 只看已选（{{ selected.size }}）
          </label>
          <label class="field row" style="flex-direction: row; align-items: center; gap: 6px"
            title="筛选已评分但没有「不足/说明」文字的照片，可全选后批量重评补全">
            <input v-model="noReason" type="checkbox" @change="applyFilters" /> 无说明（{{ noReasonCount }}）
          </label>
          <span class="muted">共 {{ items.length }} 张（跨全部批次按源文件去重）</span>
        </div>
        <div class="row">
          <button class="btn plain small" @click="selectAll">{{ selected.size ? '取消全选' : '全选当前筛选' }}</button>
          <span class="field row" style="flex-direction: row; align-items: center; gap: 4px">
            <button class="btn plain small" style="padding: 4px 10px" title="阈值减 0.5" @click="bumpThreshold(-0.5)">−</button>
            <input v-model.number="lowThreshold" type="number" min="0" max="10" step="0.5" style="width: 60px; text-align: center"
              @change="clampThreshold" />
            <button class="btn plain small" style="padding: 4px 10px" title="阈值加 0.5" @click="bumpThreshold(0.5)">＋</button>
            <button class="btn plain small" :disabled="!lowCount" title="勾选所有低于阈值的照片，页面只展示这些"
              @click="selectLow">选择 &lt; {{ lowThreshold }} 分（{{ lowCount }}）</button>
          </span>
          <button class="btn small" :disabled="!rescorableCount || rescoring" title="忽略缓存，用当前所选模型重新调用 AI 评分"
            @click="rescoreSelected">↻ 重评所选（{{ rescorableCount }}）</button>
          <button class="btn small" :disabled="rescoring" title="选择新模型批量重评：当前筛选/无说明/已选"
            @click="openRescoreAll">↻ 一键重评…</button>
          <button class="btn small" :disabled="!scoreArchiveCount" title="把当前筛选的照片按分值放入归档目录下的分值文件夹：6.0-6.5 / 6.6-7.0 / 7.1-7.5，7.6 起每 0.1 分一个文件夹"
            @click="scoreArchiveOpen = true">📁 分值归档（{{ scoreArchiveCount }}）</button>
          <button class="btn danger" :disabled="!selected.size || deleting" @click="confirmOpen = true">
            🗑️ 删除所选（{{ selected.size }}）
          </button>
          <button class="btn plain small" :disabled="!selected.size" @click="removeRecords">
            📋 仅移除记录（{{ selected.size }}）
          </button>
        </div>
      </div>
      <div class="row" style="margin-top: 8px">
        <label class="field row" style="flex-direction: row; align-items: center">
          <input v-model="deleteRaw" type="checkbox" /> 同时删除同名 RAW/HEIC 伴生文件（.raf/.cr2/.nef…）
        </label>
        <label class="field row" style="flex-direction: row; align-items: center"
          title="开启后删除=移入源图目录下的 SnapRank_回收站（随后自动打开，可移回反悔或手动清空）；关闭则直接删除源文件">
          <input v-model="recycleDelete" type="checkbox" @change="saveRecycleOpt" /> 删除时移入回收站
        </label>
        <span class="muted">已选 {{ selected.size }} 张 · {{ recycleDelete ? '将移入源图目录下的回收站，可反悔' : '将删除源文件，不可恢复' }}</span>
        <div v-if="rescoring" class="row" style="flex: 1; min-width: 220px; align-items: center">
          <div class="progress-track" style="flex: 1">
            <div class="progress-fill" :style="{ width: rescorePct + '%' }"></div>
          </div>
          <span class="muted" style="flex: none">
            重评中 {{ rescoreDone }}/{{ rescoreTotal }}<span v-if="rescoreFailed" class="error-text">（失败 {{ rescoreFailed }}）</span>
          </span>
        </div>
      </div>
      <div v-if="delResult" class="muted" style="margin-top: 6px">
        <template v-if="delResult.recycled">
          ♻️ 已把 {{ delResult.recycled }} 张源文件移入回收站<template v-if="delResult.raws">、{{ delResult.raws }} 个 RAW/伴生文件</template>
          <button class="btn plain small" @click="openRecycle">📂 打开回收站</button>
          · 记录已保留：把文件移回原目录即可恢复展示
        </template>
        <template v-else-if="delResult.removed_records">
          📋 已移除 {{ delResult.removed_records }} 条记录（源文件未动）
        </template>
        <template v-else>
          ✅ 已删除 {{ delResult.deleted }} 张源文件、{{ delResult.raws }} 个 RAW/伴生文件（释放
          {{ delResult.freed_mb.toFixed(1) }} MB）· 数据库与 API 记录已保留
        </template>
        <span v-if="delResult.missing" class="error-text">（{{ delResult.missing }} 张源文件本已不存在）</span>
        <div v-for="(e, i) in delResult.errors || []" :key="i" class="error-text">{{ e }}</div>
      </div>
    </div>

    <div class="card" v-if="filtered.length">
      <!-- 分页渲染：全量列表只做筛选/勾选/弹窗导航，DOM 只挂当前页（图片多时不卡） -->
      <div class="photo-grid">
        <div v-for="p in paged" :key="p.id" class="photo-card" :class="{ selected: selected.has(p.id) }"
          @click="preview = p" title="点击查看大图；勾选左上角选择框进行批量操作">
          <div class="thumb-wrap">
            <input type="checkbox" class="sel-box" :checked="selected.has(p.id)"
              @click.stop="toggleSelect(p)" />
            <img v-if="!p._noThumb" :src="`/api/thumb?id=${p.id}&v=${(p.fingerprint || '').slice(0, 12)}`" loading="lazy" @error="thumbFail(p)" />
            <div v-else class="no-thumb">无预览</div>
            <span class="score-badge" :class="badgeClass(p)">{{ badgeText(p) }}</span>
            <button class="fav-btn" :class="{ on: p.favorite }" :title="p.favorite ? '取消喜欢' : '标记喜欢：低分筛选会跳过这张图'"
              @click.stop="toggleFav(p)">{{ p.favorite ? '❤' : '♡' }}</button>
          </div>
          <div class="p-name" :title="p.src_path">{{ p.filename }}</div>
          <div class="p-meta muted">
            <span v-if="p.raw_siblings && p.raw_siblings.length" class="tag raw-tag">含 RAW</span>
            <span>{{ (p.size / 1048576).toFixed(1) }} MB</span>
          </div>
        </div>
      </div>
      <!-- 分页器（底部）：←/→ 翻页、Home/End 首尾页、PageUp/PageDown 同向 -->
      <div class="row" style="justify-content: space-between; align-items: center; margin-top: 12px">
        <span class="muted">筛选结果 {{ filtered.length }} 张 · 第 {{ page }} / {{ totalPages }} 页</span>
        <span class="row" style="gap: 6px; align-items: center">
          <label class="row" style="gap: 4px; align-items: center; font-size: 12px" title="每页显示的卡片数">
            每页
            <select v-model.number="pageSize" @change="onPageSizeChange" style="width: 64px">
              <option :value="50">50</option>
              <option :value="100">100</option>
              <option :value="200">200</option>
              <option :value="500">500</option>
            </select>
            条
          </label>
          <button class="btn plain small" :disabled="page <= 1" title="首页（Home）" @click="goPage(1)">« 首页</button>
          <button class="btn plain small" :disabled="page <= 1" title="上一页（← / PageUp）" @click="goPage(page - 1)">‹ 上一页</button>
          <button class="btn plain small" :disabled="page >= totalPages" title="下一页（→ / PageDown）" @click="goPage(page + 1)">下一页 ›</button>
          <button class="btn plain small" :disabled="page >= totalPages" title="尾页（End）" @click="goPage(totalPages)">尾页 »</button>
        </span>
      </div>
    </div>
    <div class="card" v-else><span class="muted">没有照片：先到「运行」页完成一次评分，或调整筛选</span></div>

    <PhotoModal v-if="preview" :photo="preview" :busy="false" :list="filtered"
      @close="preview = null" @navigate="onNavigate" @deleted="onDeleted" 
      @rescored="load" />

    <!-- 分值归档弹窗：分桶预览 + 复制/移动 -->
    <div v-if="scoreArchiveOpen" class="cf-mask" @click.self="scoreArchiveOpen = false">
      <div class="cf-card" style="border-color: var(--accent)">
        <h3 class="title">📁 分值归档</h3>
        <div class="muted" style="margin: 6px 0 8px">
          把当前筛选的 <b>{{ scoreArchiveCount }}</b> 张已评分照片按分值放入下列目录的分值文件夹
          （默认放在照片所在盘，避免塞满 C 盘）：
        </div>
        <div class="row" style="gap: 6px; align-items: center; margin-bottom: 8px">
          <input style="flex: 1" v-model="scoreArchiveDest" :placeholder="defaultArchiveDest" @change="saveArchiveDest" />
          <button class="btn plain small" @click="pickArchiveDest">选择目录</button>
        </div>
        <div class="bucket-list">
          <div v-for="bk in scoreBuckets" :key="bk[0]" class="row" style="justify-content: space-between; padding: 2px 4px">
            <span>📁 {{ bk[0] }}</span><span class="muted">{{ bk[1] }} 张</span>
          </div>
          <div v-if="!scoreBuckets.length" class="muted">当前筛选没有已评分的照片</div>
        </div>
        <div class="muted" style="font-size: 12px; margin-top: 6px">
          分桶规则：6.0-6.5、6.6-7.0、7.1-7.5 各一个文件夹；7.6 起每 0.1 分一个文件夹（7.6、7.7…10.0）。
          已归档过的照片自动跳过；同名冲突自动加序号；同名 RAW/HEIC 伴生文件（.raf/.cr2/.nef…）跟随主图一起移动/复制。
        </div>
        <div class="row" style="justify-content: flex-end; gap: 8px; margin-top: 12px">
          <select v-model="scoreArchiveMode" style="width: 130px" title="移动=剪切源文件；复制=保留源文件">
            <option value="move">移动（剪切）</option>
            <option value="copy">复制（保留源）</option>
          </select>
          <button class="btn plain" @click="scoreArchiveOpen = false">取消</button>
          <button class="btn" :disabled="scoreArchiving || !scoreArchiveCount" @click="doScoreArchive">
            {{ scoreArchiving ? '归档中…' : '开始归档' }}
          </button>
        </div>
      </div>
    </div>

    <!-- 一键重评弹窗：选范围 + 新模型 -->
    <div v-if="rescoreAllOpen" class="cf-mask" @click.self="rescoreAllOpen = false">
      <div class="cf-card" style="border-color: var(--accent)">
        <h3 class="title">↻ 一键重新评分</h3>
        <div class="row" style="flex-direction: column; align-items: stretch; gap: 10px; margin-top: 6px">
          <label class="field">
            重评范围
            <select v-model="rescoreScope">
              <option value="filtered">当前筛选结果（{{ scopeCount('filtered') }} 张）</option>
              <option value="noReason">无说明的照片（{{ scopeCount('noReason') }} 张）</option>
              <option value="selected">已勾选（{{ scopeCount('selected') }} 张）</option>
            </select>
          </label>
          <label class="field">
            评分模型（可换新模型，历史保留每次记录可对比）
            <select v-model="rescoreModel">
              <option v-for="m in state.models.vision" :key="m" :value="m">{{ m }}</option>
              <option v-if="state.currentModel && !state.models.vision.includes(state.currentModel)" :value="state.currentModel">{{ state.currentModel }}（当前）</option>
            </select>
          </label>
          <div class="muted">忽略缓存强制重新调用 AI（产生 API 费用）；换新模型可对比不同 AI 的评分差异</div>
        </div>
        <div class="row" style="justify-content: flex-end; margin-top: 12px">
          <button class="btn plain" @click="rescoreAllOpen = false">取消</button>
          <button class="btn" :disabled="rescoring || !scopeCount(rescoreScope)" @click="doRescoreAll">
            开始重评（{{ scopeCount(rescoreScope) }} 张）
          </button>
        </div>
      </div>
    </div>

    <!-- 删除二次预览确认 -->
    <div v-if="confirmOpen" class="cf-mask" @click.self="confirmOpen = false">
      <div class="cf-card">
        <h3 class="title" :style="recycleDelete ? '' : 'color: var(--danger)'">
          {{ recycleDelete ? '♻️ 移入回收站（第 2 步 / 共 2 步）' : '⚠️ 删除确认（第 2 步 / 共 2 步）' }}
        </h3>
        <div class="muted" style="margin-bottom: 8px">
          <template v-if="recycleDelete">
            将把以下 <b>{{ selected.size }}</b> 张照片的源文件移入源图目录下的回收站{{ deleteRaw ? '（含同名 RAW/HEIC 伴生文件）' : '' }}，
            可移回原目录反悔，或到回收站自行清空：
          </template>
          <template v-else>
            将永久删除以下 <b>{{ selected.size }}</b> 张照片的源文件{{ deleteRaw ? '，并联动删除同名 RAW/HEIC 伴生文件' : '' }}（不可恢复）：
          </template>
        </div>
        <div class="cf-list">
          <div v-for="p in selectedItems.slice(0, 200)" :key="p.id" class="cf-row">
            <span class="cf-name">{{ p.filename }}<span v-if="!p.present" class="muted">（源文件已不存在，将跳过）</span></span>
            <span class="cf-score">{{ p.status === 'scored' ? p.score.toFixed(1) + ' 分' : badgeText(p) }}</span>
            <span class="cf-path" :title="p.src_path">{{ p.src_path }}</span>
          </div>
          <div v-if="selectedItems.length > 200" class="muted" style="padding: 6px">
            …共 {{ selectedItems.length }} 张，仅列出前 200 张
          </div>
        </div>
        <div class="row" style="justify-content: flex-end; margin-top: 12px">
          <button class="btn plain" @click="confirmOpen = false">取消</button>
          <button class="btn" :class="recycleDelete ? '' : 'danger'" :disabled="deleting" @click="doDelete">
            {{ deleting ? '处理中…' : (recycleDelete ? '移入回收站' : '确认永久删除') }}
          </button>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, computed, watch, onMounted, onBeforeUnmount } from 'vue'
import { state, taskOf, api, toast, refreshState, markForced, rescorePhotos } from '../store.js'
import PhotoModal from '../components/PhotoModal.vue'

const items = ref([])
const filtered = ref([])
const selected = ref(new Set())
const preview = ref(null)
const band = ref('')
const search = ref('')
const onlySelected = ref(false) // 只看已选：界面仅展示勾选的照片
const deleting = ref(false)
const deleteRaw = ref(true)
const delResult = ref(null)
const lowThreshold = ref(5)
const noReason = ref(false) // 无说明筛选：已评分但没有「不足」文字的照片
const sortBy = ref('score-desc') // 排序：分数/文件名双向
const scoreMin = ref('') // 分数区间筛选（空=不限）
const scoreMax = ref('')
const recycleDelete = ref(true) // 删除→回收站（配置持久化，见 saveRecycleOpt）
const page = ref(1)
// 每页条数（localStorage 持久化；改完回第 1 页）
const pageSize = ref(Math.min(500, Math.max(50, +(localStorage.getItem('sr_gallery_pagesize') || 100) || 100)))
function onPageSizeChange() {
  localStorage.setItem('sr_gallery_pagesize', String(pageSize.value))
  page.value = 1
}

const totalPages = computed(() => Math.max(1, Math.ceil(filtered.value.length / pageSize.value)))
const paged = computed(() => filtered.value.slice((page.value - 1) * pageSize.value, page.value * pageSize.value))
function goPage(p) {
  page.value = Math.min(Math.max(1, p), totalPages.value)
  scrollToTop()
}
function scrollToTop() { window.scrollTo({ top: 0 }) }
// 阈值步进（±0.5）并夹在 0~10
function bumpThreshold(d) {
  lowThreshold.value = Math.min(10, Math.max(0, +((lowThreshold.value + d).toFixed(1))))
}
function clampThreshold() {
  if (!Number.isFinite(lowThreshold.value)) lowThreshold.value = 5
  lowThreshold.value = Math.min(10, Math.max(0, +lowThreshold.value.toFixed(1)))
}

function bandOf(p) {
  if (p.status !== 'scored') return null
  const t = (state.config && state.config.score && state.config.score.thresholds) || [9, 7, 5]
  if (p.score >= t[0]) return 'high'
  if (p.score >= t[1]) return 'mid'
  return 'low'
}
function badgeText(p) {
  if (p.status === 'parse_fail') return '复检'
  if (p.status === 'scored') return p.score.toFixed(1)
  return { failed: '调用失败', bad_image: '无法解码', unsupported: '不支持', duplicate: '重复', pending: '待处理', compressed: '已压缩' }[p.status] || p.status
}
function badgeClass(p) {
  if (p.status === 'scored') {
    const v = p.score
    if (v >= 9) return 'best'
    if (v >= 7) return 'good'
    if (v >= 5) return 'mid'
    return 'low'
  }
  return 'bad'
}
function thumbFail(p) { p._noThumb = true }

async function load() {
  try {
    if (state.config && state.config.gallery) {
      recycleDelete.value = state.config.gallery.recycle_on_delete !== false
    }
    items.value = await api('/api/gallery')
    applyFilters(false)
  } catch (e) {
    toast(e.message, true)
  }
}

// resetPage=false 用于任务完成后的自动刷新：保留用户当前页码（越界才回夹）
function applyFilters(resetPage = true) {
  let list = items.value
  if (band.value) list = list.filter((p) => bandOf(p) === band.value)
  if (search.value) {
    const q = search.value.toLowerCase()
    list = list.filter((p) => p.filename.toLowerCase().includes(q))
  }
  if (noReason.value) list = list.filter((p) => p.status === 'scored' && !p.weakness)
  // 分数区间：只作用于已评分照片（填了任一端即排除未评分）
  if (scoreMin.value !== '' || scoreMax.value !== '') {
    list = list.filter((p) => {
      if (p.status !== 'scored') return false
      if (scoreMin.value !== '' && p.score < +scoreMin.value) return false
      if (scoreMax.value !== '' && p.score > +scoreMax.value) return false
      return true
    })
  }
  if (onlySelected.value) list = list.filter((p) => selected.value.has(p.id))
  // 排序（客户端，作用于全部筛选结果）
  const sorted = [...list]
  switch (sortBy.value) {
    case 'score-asc': sorted.sort((a, b) => a.score - b.score); break
    case 'name-asc': sorted.sort((a, b) => a.filename.localeCompare(b.filename, 'zh')); break
    case 'name-desc': sorted.sort((a, b) => b.filename.localeCompare(a.filename, 'zh')); break
    default: sorted.sort((a, b) => b.score - a.score)
  }
  filtered.value = sorted
  if (resetPage) page.value = 1
  else if (page.value > totalPages.value) page.value = totalPages.value
  // 清掉已不在列表中的选择
  const ids = new Set(filtered.value.map((p) => p.id))
  const next = new Set([...selected.value].filter((id) => ids.has(id)))
  if (next.size !== selected.value.size) selected.value = next
}
// 「只看已选」模式下取消勾选时视图实时跟随（size 不变时不触发，天然防循环）
watch(() => selected.value.size, () => { if (onlySelected.value) applyFilters() })
// 挂在图库页时任务在别处跑完 → 自动重载，否则新照片要切一次页面才出现
watch(() => state.running, (v, old) => { if (old && !v) load() })

const lowCount = computed(() => items.value.filter((p) => p.status === 'scored' && p.score < lowThreshold.value && !p.favorite).length)
const noReasonCount = computed(() => items.value.filter((p) => p.status === 'scored' && !p.weakness).length)
const selectedItems = computed(() => items.value.filter((p) => selected.value.has(p.id)))
const confirmOpen = ref(false)

// ---------- 分值归档：当前筛选按分值分桶放入归档目录 ----------
const scoreArchiveOpen = ref(false)
const scoreArchiveMode = ref('move')
const scoreArchiving = ref(false)
// 归档目标目录：默认按第一批照片的源目录推导到同级 SnapRank（同一块盘，不塞 C 盘），可改并记住
const scoreArchiveDest = ref(localStorage.getItem('sr_score_archive_dest') || '')
const defaultArchiveDest = computed(() => {
  const p = filtered.value.find((x) => x.status === 'scored') || filtered.value[0]
  const src = p && p.src_path ? String(p.src_path).split('\\').join('/') : ''
  const dir = src.slice(0, src.lastIndexOf('/'))
  const parent = dir.slice(0, dir.lastIndexOf('/'))
  return parent ? parent + '/SnapRank' : '选择归档目标目录'
})
function saveArchiveDest() {
  localStorage.setItem('sr_score_archive_dest', scoreArchiveDest.value.trim())
}
async function pickArchiveDest() {
  try {
    const r = await api('/api/pick-dir', { method: 'POST', body: '{}' })
    if (r.dir) { scoreArchiveDest.value = r.dir; saveArchiveDest() }
  } catch (e) {
    toast(e.message, true)
  }
}
const scoreArchiveCount = computed(() => filtered.value.filter((p) => p.status === 'scored').length)
// 分桶口径与后端 ScoreBucket 一致：<6.0 一桶；6.0-6.5 / 6.6-7.0 / 7.1-7.5；7.6 起每 0.1 一桶
function scoreBucketOf(s) {
  if (s < 6.0) return '6.0以下'
  if (s <= 6.5) return '6.0-6.5'
  if (s <= 7.0) return '6.6-7.0'
  if (s <= 7.5) return '7.1-7.5'
  return Number(s).toFixed(1)
}
const scoreBuckets = computed(() => {
  const m = {}
  for (const p of filtered.value) {
    if (p.status !== 'scored') continue
    const b = scoreBucketOf(p.score)
    m[b] = (m[b] || 0) + 1
  }
  const key = (s) => (s.endsWith('以下') ? -1 : parseFloat(s) || 0)
  return Object.entries(m).sort((a, b) => key(a[0]) - key(b[0]))
})
async function doScoreArchive() {
  const ids = filtered.value.filter((p) => p.status === 'scored').map((p) => p.id)
  if (!ids.length) return
  scoreArchiving.value = true
  try {
    const dest = scoreArchiveDest.value.trim() || defaultArchiveDest.value
    const r = await api('/api/gallery/score-archive', { method: 'POST', body: JSON.stringify({ ids, mode: scoreArchiveMode.value, dest_dir: dest }) })
    saveArchiveDest()
    toast(`分值归档完成：${r.placed} 张${r.raws ? ` + RAW ${r.raws} 个` : ''}${r.skipped ? `（${r.skipped} 张已归档过跳过）` : ''} → ${r.dir}`)
    scoreArchiveOpen.value = false
    load()
  } catch (e) {
    toast(e.message, true)
  }
  scoreArchiving.value = false
}

function toggleSelect(p) {
  const s = new Set(selected.value)
  if (s.has(p.id)) s.delete(p.id)
  else s.add(p.id)
  selected.value = s
}
// 标记/取消喜欢（按指纹持久化，重扫重评后标记仍生效）
async function toggleFav(p) {
  const on = !p.favorite
  try {
    await api('/api/photo/favorite', { method: 'POST', body: JSON.stringify({ id: p.id, on }) })
    p.favorite = on
  } catch (e) {
    toast(e.message, true)
  }
}
function selectAll() {
  if (selected.value.size) { selected.value = new Set(); return }
  selected.value = new Set(filtered.value.map((p) => p.id))
}
function selectLow() {
  // 已标记「喜欢」的照片不参与低分筛选
  selected.value = new Set(items.value.filter((p) => p.status === 'scored' && p.score < lowThreshold.value && !p.favorite).map((p) => p.id))
  if (!selected.value.size) {
    toast('没有低于该分数的照片（已喜欢的会跳过）')
    return
  }
  band.value = '' // 「只看已选」已覆盖筛选视图，清空分数段避免双重过滤混淆
  search.value = ''
  onlySelected.value = true // 页面只展示这批勾选的照片
  applyFilters()
  toast(`已选中 ${selected.value.size} 张低于 ${lowThreshold.value} 分的照片，页面已只展示这些（取消勾选即从视图移除）`)
}
function onNavigate(newPhoto) {
  preview.value = newPhoto
}

// 弹窗内删除成功：从列表移除（源文件已删，后端图库不再返回它）
function onDeleted(photo) {
  items.value = items.value.filter((p) => p.id !== photo.id)
  const s = new Set(selected.value)
  s.delete(photo.id)
  selected.value = s
  applyFilters()
  preview.value = null
}

// ---------- 批量重评（跨批次：后端按所属批次分组入队） ----------
const rescoring = ref(false)
const rescoreDone = ref(0)
const rescoreFailed = ref(0)
const rescoreTotal = ref(0)
const rescorePct = computed(() => (rescoreTotal.value ? Math.round((rescoreDone.value / rescoreTotal.value) * 100) : 0))
let rescoreEs = null
let pendingKeys = new Set() // 待完成回报：session_id|filename

const NOK_RESCORE = ['unsupported', 'bad_image']
function rescorable(p) {
  return p.present && !NOK_RESCORE.includes(p.status)
}
const rescorableCount = computed(() => items.value.filter((p) => selected.value.has(p.id) && rescorable(p)).length)

// ---------- 一键重评：范围 + 新模型 ----------
const rescoreAllOpen = ref(false)
const rescoreScope = ref('filtered')
const rescoreModel = ref('')
function rescopeTargets() {
  let list = filtered.value
  if (rescoreScope.value === 'noReason') list = items.value.filter((p) => p.status === 'scored' && !p.weakness)
  else if (rescoreScope.value === 'selected') list = items.value.filter((p) => selected.value.has(p.id))
  return list.filter(rescorable)
}
// 纯函数：按指定范围计数（模板渲染用——绝不能在渲染期间修改响应式状态）
function scopeCount(scope) {
  let list = filtered.value
  if (scope === 'noReason') list = items.value.filter((p) => p.status === 'scored' && !p.weakness)
  else if (scope === 'selected') list = items.value.filter((p) => selected.value.has(p.id))
  return list.filter(rescorable).length
}
async function openRescoreAll() {
  // 模型清单可能未加载（图库页不拉取）：打开时补拉
  if (!state.models.vision.length) {
    try {
      const r = await api('/api/models')
      state.models = r
    } catch { /* 拉取失败仍可用当前模型 */ }
  }
  rescoreModel.value = state.selModel || state.currentModel || (state.models.vision[0] || '')
  rescoreAllOpen.value = true
}
async function doRescoreAll() {
  const targets = rescopeTargets()
  if (!targets.length) {
    toast('所选范围内没有可重评的照片')
    return
  }
  rescoreAllOpen.value = false
  state.selModel = rescoreModel.value // rescorePhotos 会把它应用为默认模型
  await startRescore(targets)
}

async function rescoreSelected() {
  const targets = items.value.filter((p) => selected.value.has(p.id) && rescorable(p))
  if (!targets.length) {
    toast('所选照片中没有可重评的（源文件已删或格式不支持）')
    return
  }
  await startRescore(targets)
}

// 提交重评并监听进度（重评所选 / 一键重评共用）
async function startRescore(targets) {
  rescoring.value = true
  rescoreDone.value = 0
  rescoreFailed.value = 0
  rescoreTotal.value = targets.length
  pendingKeys = new Set(targets.map((p) => `${p.session_id}|${p.filename}`))
  try {
    const r = await rescorePhotos({ ids: targets.map((p) => p.id), force: true })
    markForced(r.session_id, targets.map((p) => p.filename))
  } catch (e) {
    toast(e.message, true)
    stopRescoreWatch()
    rescoring.value = false
    return
  }
  // SSE 按批次+文件名匹配各张照片的重评进度
  rescoreEs = new EventSource('/api/events')
  rescoreEs.addEventListener('progress', (e) => {
    try {
      const d = JSON.parse(e.data)
      const key = `${d.session_id}|${d.file}`
      if (!pendingKeys.has(key)) return
      pendingKeys.delete(key)
      rescoreDone.value++
      if (d.status === 'parse_fail' || d.status === 'failed') rescoreFailed.value++
      if (pendingKeys.size === 0) finishRescore()
    } catch { /* 忽略解析错误 */ }
  })
  rescoreEs.addEventListener('error', () => { /* 断线由浏览器重连 */ })
}

function finishRescore() {
  const ok = rescoreDone.value - rescoreFailed.value
  const failTxt = rescoreFailed.value ? `，${rescoreFailed.value} 张失败可再次重评` : ''
  toast(`重评完成：${ok} 张成功${failTxt}`)
  stopRescoreWatch()
  rescoring.value = false
  load()
}

function stopRescoreWatch() {
  if (rescoreEs) { rescoreEs.close(); rescoreEs = null }
  pendingKeys = new Set()
}

// 仅移除数据库记录（源文件保留或已不存在）
async function removeRecords() {
  try {
    delResult.value = await api('/api/gallery/delete', {
      method: 'POST',
      body: JSON.stringify({ ids: [...selected.value], delete_raw: false, records_only: true }),
    })
    selected.value = new Set()
    await load()
  } catch (e) {
    toast(e.message, true)
  }
}

// 回收站开关持久化到配置（失败则回滚勾选状态）
async function saveRecycleOpt() {
  try {
    const cfg = await api('/api/config', {
      method: 'POST',
      body: JSON.stringify({ ...state.config, gallery: { recycle_on_delete: recycleDelete.value } }),
    })
    state.config = cfg
  } catch (e) {
    toast(e.message, true)
    recycleDelete.value = !recycleDelete.value
  }
}

function openRecycle() {
  const dir = delResult.value && delResult.value.recycle_dir
  if (!dir) return
  api('/api/open', { method: 'POST', body: JSON.stringify({ path: dir }) }).catch((e) => toast(e.message, true))
}

async function doDelete() {
  if (deleting.value) return
  // 过滤掉源文件已不存在的（无可删）
  const targets = filtered.value.filter((p) => selected.value.has(p.id) && p.present)
  if (!targets.length) {
    toast('所选照片的源文件都已不存在（可用“仅移除记录”清理）')
    return
  }
  deleting.value = true
  try {
    delResult.value = await api('/api/gallery/delete', {
      method: 'POST',
      body: JSON.stringify({ ids: targets.map((p) => p.id), delete_raw: deleteRaw.value }),
    })
    selected.value = new Set()
    confirmOpen.value = false
    // 不主动打开文件夹：结果栏留「📂 打开回收站」按钮，由用户自己决定何时查看
    await load()
    refreshState()
  } catch (e) {
    toast(e.message, true)
  }
  deleting.value = false
}

function onKey(e) {
  // 删除确认弹窗打开时：Enter 确认删除，Esc 仅取消确认
  if (confirmOpen.value) {
    if (e.key === 'Enter') {
      e.preventDefault()
      doDelete()
    } else if (e.key === 'Escape') {
      confirmOpen.value = false
    }
    return
  }
  if (e.key === 'a' && (e.ctrlKey || e.metaKey) && state.page === 'gallery') {
    e.preventDefault()
    selectAll()
    return
  }
  if (state.page !== 'gallery') return
  // 键盘翻页：输入控件聚焦（搜索框/选择器）或照片弹窗打开时不响应
  const tag = (e.target && e.target.tagName) || ''
  if (tag === 'INPUT' || tag === 'SELECT' || tag === 'TEXTAREA' || preview.value) return
  const keys = { ArrowLeft: -1, PageUp: -1, ArrowRight: 1, PageDown: 1 }
  if (e.key in keys) {
    if ((keys[e.key] < 0 && page.value > 1) || (keys[e.key] > 0 && page.value < totalPages.value)) {
      e.preventDefault()
      goPage(page.value + keys[e.key])
    }
  } else if (e.key === 'Home' && page.value > 1) {
    e.preventDefault()
    goPage(1)
  } else if (e.key === 'End' && page.value < totalPages.value) {
    e.preventDefault()
    goPage(totalPages.value)
  }
}
onMounted(() => {
  window.addEventListener('keydown', onKey)
  load()
  refreshState()
})
onBeforeUnmount(() => {
  window.removeEventListener('keydown', onKey)
  stopRescoreWatch()
})
</script>

<style scoped>
.grid { display: flex; flex-direction: column; gap: 14px; width: 100%; }
.photo-grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(150px, 1fr)); gap: 12px; }
.photo-card {
  background: var(--card-2); border-radius: 10px; overflow: hidden;
  display: flex; flex-direction: column; cursor: pointer; position: relative;
  outline: 2px solid transparent; transition: outline-color 0.15s;
}
.photo-card.selected { outline: 2px solid var(--danger); }
.thumb-wrap { position: relative; aspect-ratio: 4/3; background: #ddd; }
html.dark .thumb-wrap { background: #333; }
.thumb-wrap img { width: 100%; height: 100%; object-fit: cover; display: block; }
.no-thumb {
  width: 100%; height: 100%; display: flex; align-items: center; justify-content: center;
  color: var(--text-2); font-size: 12px; text-align: center; background: var(--card-2);
}
.sel-box {
  position: absolute; left: 6px; top: 6px; width: 16px; height: 16px; z-index: 2;
  accent-color: var(--danger); cursor: pointer;
}
.fav-btn {
  position: absolute; right: 6px; bottom: 6px; z-index: 2;
  width: 26px; height: 26px; border: none; border-radius: 50%;
  background: rgba(0, 0, 0, 0.45); color: #fff; font-size: 14px; line-height: 1;
  cursor: pointer; display: flex; align-items: center; justify-content: center;
}
.fav-btn:hover { background: rgba(0, 0, 0, 0.65); }
.fav-btn.on { background: rgba(250, 81, 81, 0.88); }
.score-badge {
  position: absolute; right: 6px; top: 6px; padding: 2px 8px; border-radius: 999px;
  color: #fff; font-weight: 700; font-size: 13px; background: #888;
}
.best { background: #07c160; } .good { background: #10aeff; }
.mid { background: #ffa300; } .low { background: #fa5151; } .bad { background: #666; }
.p-name { padding: 6px 8px 0; font-size: 13px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.p-meta { padding: 2px 8px 8px; display: flex; gap: 6px; align-items: center; font-size: 12px; }
.raw-tag { background: #3a2c12; color: #ffa300; }
.photo-card.missing { opacity: 0.45; }
.cf-mask {
  position: fixed; inset: 0; background: rgba(0,0,0,0.72); z-index: 110;
  display: flex; align-items: center; justify-content: center; padding: 24px;
}
.cf-card {
  background: var(--card); border: 2px solid var(--danger); border-radius: 12px;
  width: min(720px, 94vw); max-height: 86vh; display: flex; flex-direction: column;
  padding: 16px 18px; box-shadow: 0 12px 48px rgba(0,0,0,0.4);
}
.bucket-list { max-height: 260px; overflow-y: auto; border: 1px dashed var(--line); border-radius: 8px; padding: 6px 8px; margin: 4px 0; }
.cf-list { overflow-y: auto; flex: 1; margin: 8px 0; }
.cf-row {
  display: flex; align-items: baseline; gap: 10px; font-size: 13px;
  padding: 5px 6px; border-bottom: 1px solid var(--line);
}
.cf-name { font-weight: 600; flex: none; width: 180px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.cf-score { flex: none; width: 60px; color: var(--text-2); }
.cf-path { flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; color: var(--text-2); font-size: 12px; }
</style>
