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
            <input v-model="search" style="width: 180px" @input="applyFilters" placeholder="留空=全部" />
          </label>
          <label class="field row" style="flex-direction: row; align-items: center; gap: 6px">
            <input v-model="onlySelected" type="checkbox" @change="applyFilters" /> 只看已选（{{ selected.size }}）
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
        <span class="muted">已选 {{ selected.size }} 张 · 将删除源文件，不可恢复</span>
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
        ✅ 已删除 {{ delResult.deleted }} 张源文件、{{ delResult.raws }} 个 RAW/伴生文件（释放
        {{ delResult.freed_mb.toFixed(1) }} MB）· 数据库与 API 记录已保留
        <span v-if="delResult.missing" class="error-text">（{{ delResult.missing }} 张源文件本已不存在）</span>
        <div v-for="(e, i) in delResult.errors || []" :key="i" class="error-text">{{ e }}</div>
      </div>
    </div>

    <div class="card" v-if="filtered.length">
      <div class="photo-grid">
        <div v-for="p in filtered" :key="p.id" class="photo-card" :class="{ selected: selected.has(p.id) }"
          @click="preview = p" title="点击查看大图；勾选左上角选择框进行批量操作">
          <div class="thumb-wrap">
            <input type="checkbox" class="sel-box" :checked="selected.has(p.id)"
              @click.stop="toggleSelect(p)" />
            <img v-if="!p._noThumb" :src="`/api/thumb?id=${p.id}&v=${(p.fingerprint || '').slice(0, 12)}`" loading="lazy" @error="thumbFail(p)" />
            <div v-else class="no-thumb">无预览</div>
            <span class="score-badge" :class="badgeClass(p)">{{ badgeText(p) }}</span>
          </div>
          <div class="p-name" :title="p.src_path">{{ p.filename }}</div>
          <div class="p-meta muted">
            <span v-if="p.raw_siblings && p.raw_siblings.length" class="tag raw-tag">含 RAW</span>
            <span>{{ (p.size / 1048576).toFixed(1) }} MB</span>
          </div>
        </div>
      </div>
    </div>
    <div class="card" v-else><span class="muted">没有照片：先到「运行」页完成一次评分，或调整筛选</span></div>

    <PhotoModal v-if="preview" :photo="preview" :busy="false" :list="filtered"
      @close="preview = null" @navigate="onNavigate" @deleted="onDeleted" />

    <!-- 删除二次预览确认 -->
    <div v-if="confirmOpen" class="cf-mask" @click.self="confirmOpen = false">
      <div class="cf-card">
        <h3 class="title" style="color: var(--danger)">⚠️ 删除确认（第 2 步 / 共 2 步）</h3>
        <div class="muted" style="margin-bottom: 8px">
          将永久删除以下 <b>{{ selected.size }}</b> 张照片的源文件{{ deleteRaw ? '，并联动删除同名 RAW/HEIC 伴生文件' : '' }}（不可恢复）：
        </div>
        <div class="cf-list">
          <div v-for="p in selectedItems" :key="p.id" class="cf-row">
            <span class="cf-name">{{ p.filename }}<span v-if="!p.present" class="muted">（源文件已不存在，将跳过）</span></span>
            <span class="cf-score">{{ p.status === 'scored' ? p.score.toFixed(1) + ' 分' : badgeText(p) }}</span>
            <span class="cf-path" :title="p.src_path">{{ p.src_path }}</span>
          </div>
        </div>
        <div class="row" style="justify-content: flex-end; margin-top: 12px">
          <button class="btn plain" @click="confirmOpen = false">取消</button>
          <button class="btn danger" :disabled="deleting" @click="doDelete">
            {{ deleting ? '删除中…' : '确认永久删除' }}
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
    items.value = await api('/api/gallery')
    applyFilters()
  } catch (e) {
    toast(e.message, true)
  }
}

function applyFilters() {
  let list = items.value
  if (band.value) list = list.filter((p) => bandOf(p) === band.value)
  if (search.value) {
    const q = search.value.toLowerCase()
    list = list.filter((p) => p.filename.toLowerCase().includes(q))
  }
  if (onlySelected.value) list = list.filter((p) => selected.value.has(p.id))
  filtered.value = list
  // 清掉已不在列表中的选择
  const ids = new Set(filtered.value.map((p) => p.id))
  const next = new Set([...selected.value].filter((id) => ids.has(id)))
  if (next.size !== selected.value.size) selected.value = next
}
// 「只看已选」模式下取消勾选时视图实时跟随（size 不变时不触发，天然防循环）
watch(() => selected.value.size, () => { if (onlySelected.value) applyFilters() })

const lowCount = computed(() => items.value.filter((p) => p.status === 'scored' && p.score < lowThreshold.value).length)
const selectedItems = computed(() => items.value.filter((p) => selected.value.has(p.id)))
const confirmOpen = ref(false)

function toggleSelect(p) {
  const s = new Set(selected.value)
  if (s.has(p.id)) s.delete(p.id)
  else s.add(p.id)
  selected.value = s
}
function selectAll() {
  if (selected.value.size) { selected.value = new Set(); return }
  selected.value = new Set(filtered.value.map((p) => p.id))
}
function selectLow() {
  selected.value = new Set(items.value.filter((p) => p.status === 'scored' && p.score < lowThreshold.value).map((p) => p.id))
  if (!selected.value.size) {
    toast('没有低于该分数的照片')
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

async function rescoreSelected() {
  const targets = items.value.filter((p) => selected.value.has(p.id) && rescorable(p))
  if (!targets.length) {
    toast('所选照片中没有可重评的（源文件已删或格式不支持）')
    return
  }
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
    await api('/api/gallery/delete', {
      method: 'POST',
      body: JSON.stringify({ ids: [...selected.value], delete_raw: false }),
    })
    toast('已移除所选记录（源文件保留或已不存在）')
    selected.value = new Set()
    await load()
  } catch (e) {
    toast(e.message, true)
  }
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
.cf-list { overflow-y: auto; flex: 1; margin: 8px 0; }
.cf-row {
  display: flex; align-items: baseline; gap: 10px; font-size: 13px;
  padding: 5px 6px; border-bottom: 1px solid var(--line);
}
.cf-name { font-weight: 600; flex: none; width: 180px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.cf-score { flex: none; width: 60px; color: var(--text-2); }
.cf-path { flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; color: var(--text-2); font-size: 12px; }
</style>
