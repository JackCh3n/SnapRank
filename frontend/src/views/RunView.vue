<template>
  <div class="grid">
    <div class="card">
      <h3 class="title">① 选择照片目录</h3>
      <div class="row">
        <label class="field grow">
          源图目录（递归扫描，支持 jpg/png/webp/gif/bmp/tiff；多个目录用 ; 分隔）
          <input v-model="state.dir" placeholder="例如 D:\\Photos\\2026-08;D:\\Photos\\旅行" @keyup.enter="doScan" />
        </label>
        <button class="btn plain" :disabled="picking" :title="picking ? '请在弹出的窗口中选择目录' : '弹出系统目录选择框（支持 Ctrl/Shift 多选）'"
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
        <button class="btn plain" :disabled="scanning" @click="doScan">
          {{ scanning ? '扫描中…（计算指纹）' : '扫描' }}
        </button>
      </div>
      <div v-if="formatPreset && state.scan" class="muted" style="margin-top: 4px">
        已按「{{ formatLabel }}」筛选，其他格式（含 RAF/HEIC 等相机 RAW）不会进入评分
      </div>
      <div v-if="dirHistory.length" class="dir-history">
        <span class="muted">最近：</span>
        <span class="chip-x" title="清空全部目录历史" style="cursor:pointer;border:none;background:none"
          @click.stop="clearDirHistory">🗑️</span>
        <span v-for="d in dirHistory" :key="d" class="dir-chip" @click="useDir(d)" :title="d">
          <span class="chip-text">{{ shortDir(d) }}</span>
          <span class="chip-x" @click.stop="removeDir(d)" title="删除该标签">×</span>
        </span>
      </div>
      <div v-if="state.scan" class="muted" style="margin-top: 8px">
        共 {{ state.scan.count }} 张（批内去重后 {{ state.scan.live }} 张需评分<template v-if="state.scan.dirs > 1">，{{ state.scan.dirs }} 个目录</template>），预估费用
        <b>¥{{ state.scan.est_cost.toFixed(3) }}</b>
        <span v-if="state.scan.dupCount > 0">（重复 {{ state.scan.dupCount }} 张跳过）</span>
      </div>
      <div v-if="importedPreviews.length" class="import-preview">
        <div class="muted" style="margin-bottom: 4px">
          📥 本次导入任务（{{ importedPreviews.length }} 张，已归类为一个任务；再次贴入/拖入会自动追加）：
        </div>
        <div class="pv-grid">
          <div v-for="(pv, i) in importedPreviews" :key="i" class="pv-item" :title="pv.name">
            <img :src="pv.url" />
          </div>
          <div v-if="previewMore > 0" class="pv-item pv-more">+{{ previewMore }}</div>
        </div>
      </div>
      <div class="muted" style="margin-top: 8px">
        也可以把照片或文件夹<b>直接拖入本页面</b>，或对截图/照片 <b>Ctrl+V 粘贴</b>——
        会自动归类到独立导入目录并扫描，成为一个新任务。
      </div>
      <div v-if="importing" class="tag" style="margin-top: 6px">📥 正在导入 {{ importDone }}/{{ importTotal }} 个文件…</div>
      <div v-if="dragOver" class="drop-hint">松开鼠标，导入拖入的照片/文件夹</div>
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
            <option v-for="m in modelOptions" :key="m" :value="m">{{ m }}</option>
            <option v-if="!modelOptions.includes(state.selModel) && state.selModel" :value="state.selModel">{{ state.selModel }}（当前）</option>
          </select>
        </label>
        <label class="field row" style="flex-direction: row; align-items: center; gap: 6px"
          title="关闭后显示平台全部模型（识别规则只是猜测，部分视觉模型可能被误过滤）">
          <input v-model="showAllModels" type="checkbox" /> 全部
        </label>
        <input v-model="manualModel" style="width: 170px" placeholder="或手动输入模型 ID（无清单接口的平台）"
          title="部分平台（如天翼）没有模型清单接口，直接输入模型 ID 回车即选中" @keyup.enter="applyManualModel" />
        <button class="btn plain" :disabled="loadingModels" @click="loadModels">
          {{ loadingModels ? '拉取中…' : '刷新模型列表' }}
        </button>
        <button class="btn" :disabled="!state.dir || starting" @click="doStart(true)">
          {{ starting ? '创建中…' : '▶ 抽样试跑 10 张' }}</button>
        <button class="btn" :disabled="!state.dir || starting" @click="doStart(false)">
          {{ starting ? '创建中…' : '▶ 开始评分' }}</button>
        <button class="btn danger" :disabled="!state.queue.current" @click="doStop">■ 停止当前任务</button>
      </div>
      <div class="row" style="margin-top: 10px; align-items: center">
        <label class="field row" style="flex-direction: row; align-items: center; gap: 6px">
          <input v-model="nightMode" type="checkbox" />
          <span>🔁 失败自动重试</span>
        </label>
        <label class="field row" style="flex-direction: row; align-items: center; gap: 6px"
          title="勾选后可加选多个模型，同一张图并行评分对比（主模型结果入正记录，其余进评分历史）">
          <input v-model="compareMode" type="checkbox" />
          <span>⚖️ 多模型对比</span>
        </label>
        <span class="muted">失败自动重试：排到队尾重试（间隔 15s）直到全部成功；多模型对比：至少 2 个模型同时评同一张</span>
      </div>
      <div v-if="compareMode" class="row" style="margin-top: 8px; align-items: flex-start">
        <label class="field grow">
          对比模型（当前平台，Ctrl/Shift 多选）
          <select v-model="state.compareModels" multiple size="5" title="按住 Ctrl 点选多个">
            <option v-for="m in state.models.vision.filter(x => x !== state.selModel)" :key="m" :value="m">{{ m }}</option>
          </select>
        </label>
        <div class="field" style="min-width: 360px">
          跨平台模型（其它平台的预设 + 模型，并行评同一张）
          <div class="row" style="gap: 6px">
            <select v-model="crossPreset" style="flex: 1" @change="loadPresetModels" title="平台预设（设置页可添加）">
              <option value="">选择平台…</option>
              <option v-for="p in presets" :key="p.name" :value="p.name">{{ p.name }}</option>
            </select>
            <select v-model="crossModel" style="flex: 1" :disabled="!crossPresetModels.length">
              <option value="">{{ crossPresetLoading ? "拉取中…" : "选择模型…" }}</option>
              <option v-for="m in crossPresetModels" :key="m" :value="m">{{ m }}</option>
            </select>
            <button class="btn plain small" :disabled="!crossPreset || !(crossModel || crossManual.trim())" @click="addCrossModel">＋ 添加</button>
          </div>
          <div class="row" style="gap: 6px; margin-top: 4px">
            <input v-model="crossManual" style="flex: 1" placeholder="或手动输入模型 ID（部分平台无模型清单接口，如天翼）"
              @keyup.enter="addCrossModel" />
            <span v-if="crossNoList" class="muted" style="flex: none">该平台无清单接口，直接输入模型 ID</span>
          </div>
          <div v-if="state.compareCross.length" class="row" style="flex-wrap: wrap; gap: 6px; margin-top: 6px">
            <span v-for="(c, i) in state.compareCross" :key="c.preset + '/' + c.model" class="tag"
              style="cursor: pointer" :title="c.preset + ' 平台的 ' + c.model + '（点击移除）'" @click="removeCrossModel(i)">
              🌐 {{ c.preset }} / {{ c.model }} ×
            </span>
          </div>
        </div>
      </div>
      <div v-if="compareMode" class="muted" style="margin-top: 6px">
        主模型（上方下拉）的结果作为照片的正式分数；每个模型各自写入评分历史，照片弹窗可切换对比。
        当前共 {{ state.compareModels.length + state.compareCross.length }} 个对比模型（同平台 {{ state.compareModels.length }} + 跨平台 {{ state.compareCross.length }}）。
      </div>
      <div class="muted" style="margin-top: 8px">
        当前模型：<b>{{ state.currentModel || '未设置' }}</b>
        <span v-if="state.selModel && state.selModel !== state.currentModel" class="tag">已选择 {{ state.selModel }}，下次开始时生效</span>
        · 不同模型的分数不可横向比较
        <span v-if="queueCount > 0" class="tag" style="margin-left: 6px">📋 队列中还有 {{ queueCount }} 个任务</span>
      </div>
    </div>

    <div class="card" v-if="hasQueue">
      <h3 class="title">③ 评分进度</h3>

      <div class="row" style="justify-content: space-between; align-items: center; margin: 2px 0 6px">
        <div>
          <b>⏳ 待评分 {{ waitingCount }}</b> 张 · <b>✅ 已评完 {{ state.tally.done }}</b> 张
          <span v-if="state.tally.fail" class="error-text">· ❌ 失败{{ autoRetry ? '待重试' : '' }} {{ state.tally.fail }}</span>
        </div>
        <span class="row" style="gap: 8px; align-items: center">
          <span class="eta" v-if="etaText">{{ etaText }}</span>
          <span v-if="state.queue.paused" class="tag" style="background: #3a2c12; color: #ffa300">⏸ 已暂停</span>
          <span class="row" style="gap: 3px; align-items: center" title="评分并发：运行中可调，1 秒内生效（调小则多余进程跑完当前张退出）">
            <button class="btn plain small" style="padding: 2px 8px" :disabled="scoreConc <= 1" @click="bumpConc(-1)">−</button>
            <b>{{ scoreConc }}</b>
            <button class="btn plain small" style="padding: 2px 8px" :disabled="scoreConc >= 30" @click="bumpConc(1)">＋</button>
            并发
          </span>
          <template v-if="state.running">
            <button v-if="!state.queue.paused" class="btn plain small" title="正在评分的这张完成后挂起" @click="pauseTask">⏸ 暂停</button>
            <button v-else class="btn small" @click="resumeTask">▶ 继续</button>
            <button class="btn danger small" @click="doStop">■ 停止</button>
          </template>
          <!-- 空闲但还有待评分 = 被停止/夜间安全阀中止，不能显示「已评完」 -->
          <span v-else-if="waitingCount > 0" class="tag" style="background: #3a2c12; color: #ffa300">■ 已停止 · 剩 {{ waitingCount }} 张待评分（下次开始时继续）</span>
          <span v-else-if="state.tally.done || state.tally.fail" class="tag">✓ 本轮已评完</span>
        </span>
      </div>
      <div class="progress-track"><div class="progress-fill" :style="{ width: pct + '%' }"></div></div>
      <div class="muted" style="margin: 6px 0 10px">
        <span v-if="elapsedText">已用时 {{ elapsedText }}</span>
      </div>

      <!-- 排队中的目录：当前评分完成后自动开始 -->
      <div v-for="q in queuedDirs" :key="q.sid" class="row" style="align-items: center; gap: 8px; margin-bottom: 4px">
        <span class="tag">⏳ 排队</span>
        <span class="muted" style="max-width: 280px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap" :title="q.dir">{{ q.label }}</span>
        <span class="muted">{{ q.waiting ? `${q.waiting} 张待评分，` : '' }}等待当前评分完成后自动开始</span>
        <button class="btn plain small danger-text" @click="removeQueued(q.sid)">🗑 移除</button>
      </div>

      <div v-if="state.scoreFeed.length">
        <div class="muted" style="margin: 6px 0 4px">最近评完（{{ state.scoreFeed.length }} 条）：</div>
        <div class="feed">
          <div v-for="(p, i) in state.scoreFeed" :key="i" class="feed-item clickable"
            :title="p.photo_id ? '点击查看大图与评分详情' : ''" @click="openFeedPhoto(p)">
            <span class="mono">{{ iconOf(p.status) }}</span>
            <span class="fname" :title="p.file">{{ p.file }}</span>
            <span v-if="p.status === 'scored'" class="score">{{ p.score.toFixed(1) }} 分</span>
            <span v-else-if="p.status === 'parse_fail'" class="error-text">解析失败{{ autoRetry ? '（将重新排队）' : '（待复检）' }}</span>
            <span v-else-if="p.status === 'failed'" class="error-text">调用失败{{ autoRetry ? '（将重新排队）' : '' }}</span>
            <span v-else class="error-text" :title="p.error">{{ statusLabel(p.status) }}</span>
            <span v-if="p.cached" class="tag">缓存</span>
            <span v-if="state.forcedFiles.has(p.session_id + '|' + p.file)" class="tag force-tag">强制</span>
            <span v-if="state.retryFiles.has(p.session_id + '|' + p.file)" class="tag retry-tag">重试</span>
          </div>
        </div>
      </div>
    </div>
    <PhotoModal v-if="preview" :photo="preview" :busy="state.running" :list="feedPhotoList"
      @close="preview = null" @navigate="onFeedNavigate" @deleted="onFeedDeleted" @rescored="onFeedRescored" />
  </div>
</template>

<script setup>
import { ref, computed, onMounted, onBeforeUnmount, watch } from 'vue'
import { state, taskOf, api, toast, refreshState, refreshModels, requestNotifyPermission } from '../store.js'
import { confirmDialog } from '../confirm.js'
import PhotoModal from '../components/PhotoModal.vue'

// 最近评完条目 → 点击弹窗查看（与图库同款）
const preview = ref(null)
const feedPhotoList = computed(() => state.scoreFeed.filter((f) => f.photo_id).map((f) => ({ id: f.photo_id })))
async function openFeedPhoto(f) {
  if (!f.photo_id) return
  try {
    preview.value = await api(`/api/photo?id=${f.photo_id}`)
  } catch (e) {
    toast(e.message, true)
  }
}
async function onFeedNavigate(item) {
  if (!item || !item.id) return
  try {
    preview.value = await api(`/api/photo?id=${item.id}`)
  } catch (e) {
    toast(e.message, true)
  }
}
function onFeedDeleted(photo) {
  preview.value = null
  // 从最近评完移除该条（文件已删）
  const i = state.scoreFeed.findIndex((f) => f.photo_id === photo.id)
  if (i >= 0) state.scoreFeed.splice(i, 1)
}
function onFeedRescored() {
  // 重评后刷新当前弹窗照片的分数到 feed 条目（轻量同步）
  if (preview.value && preview.value.id) {
    const f = state.scoreFeed.find((x) => x.photo_id === preview.value.id)
    if (f && typeof preview.value.score === 'number') f.score = preview.value.score
  }
}

const loadingModels = ref(false)
const showAllModels = ref(false) // false=仅视觉过滤后的模型；true=平台全部模型
const manualModel = ref('') // 无清单接口平台（如天翼）手动输入模型 ID
const modelOptions = computed(() => (showAllModels.value ? state.models.all : state.models.vision) || [])
function applyManualModel() {
  const m = manualModel.value.trim()
  if (!m) return
  state.selModel = m
  manualModel.value = ''
  toast(`已选择模型：${m}（下次开始时生效）`)
}
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
// 多目录解析：输入框支持 ; / ；/ 换行分隔，去重保序
function parseDirs() {
  return [...new Set(String(state.dir || '').split(/[;；\n]/).map((s) => s.trim()).filter(Boolean))]
}

async function pickDir() {
  picking.value = true
  try {
    const r = await api('/api/pick-dir', { method: 'POST', body: '{}' })
    const dirs = r.dirs || (r.dir ? [r.dir] : [])
    if (dirs.length) {
      // 多选时追加到已有目录之后（去重），单选直接替换
      if (dirs.length > 1) {
        const merged = [...new Set([...parseDirs(), ...dirs])]
        state.dir = merged.join(';')
      } else {
        state.dir = dirs[0]
      }
      toast(dirs.length > 1 ? `已选择 ${dirs.length} 个目录` : `已选择：${dirs[0]}`)
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
async function clearDirHistory() {
  if (!(await confirmDialog({ title: '清空目录历史', message: '清空全部目录历史？', danger: true, confirmText: '清空' }))) return
  try {
    await api('/api/dir-history/remove', { method: 'POST', body: JSON.stringify({ dir: '__ALL__' }) })
    const r = await api('/api/state')
    state.config = r.config
    toast('目录历史已清空')
  } catch (e) {
    toast(e.message, true)
  }
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

// ---------- 评分队列 ----------
const FINAL = new Set(['scored', 'parse_fail', 'failed', 'bad_image', 'unsupported'])
const queueCount = computed(() => state.queue.queued.length)

// 评分并发快捷调整（运行中生效：池每秒对齐配置；调小则多余 worker 跑完当前张退出）
const scoreConc = computed(() => (state.config && state.config.pipeline && state.config.pipeline.score_concurrency) || 1)
async function bumpConc(d) {
  const v = Math.min(30, Math.max(1, scoreConc.value + d))
  try {
    const cfg = await api('/api/config', {
      method: 'POST',
      body: JSON.stringify({ pipeline: { ...state.config.pipeline, score_concurrency: v } }),
    })
    state.config = cfg
    toast(`评分并发已调整为 ${v}（1 秒内生效）`)
  } catch (e) {
    toast(e.message, true)
  }
}

// ---------- 评分管理：暂停 / 继续 / 移除排队目录 ----------
async function pauseTask() {
  try {
    const r = await api('/api/task/pause', { method: 'POST', body: '{}' })
    toast(r.paused ? '已暂停：正在评分的这张完成后挂起' : '暂停失败（评分可能刚结束）', !r.paused)
  } catch (e) {
    toast(e.message, true)
  }
}
async function resumeTask() {
  try {
    const r = await api('/api/task/resume', { method: 'POST', body: '{}' })
    toast(r.resumed ? '已继续评分' : '恢复失败（未在暂停中）', !r.resumed)
  } catch (e) {
    toast(e.message, true)
  }
}
async function removeQueued(sid) {
  if (!sid) return
  try {
    const r = await api('/api/task/remove', { method: 'POST', body: JSON.stringify({ session: sid }) })
    if (r.removed) {
      toast('已从排队中移除')
      delete state.tasks[sid]
    } else {
      toast('移除失败（可能已开始评分）', true)
    }
  } catch (e) {
    toast(e.message, true)
  }
}

// 当前运行中的任务（ETA/用时/暂停停止都只针对它）
const cur = computed(() => taskOf(state.queue.current))
const autoRetry = computed(() => !!(cur.value && cur.value.autoRetry))

const stage = computed(() => (cur.value && cur.value.stage) || '')
const total = computed(() => (cur.value && cur.value.total) || 0)
// 用单调计数器而非重数 feed（feed 有 400 条上限，大批次会丢旧事件导致「已完成但剩余>0」）
const compressDone = computed(() => (cur.value ? cur.value.compN : 0))
const scoreDone = computed(() => (cur.value ? cur.value.finalN : 0))
const doneCount = computed(() => (stage.value === 'compress' ? compressDone.value : scoreDone.value))

// 队列视图：待评分 = 当前未评完 + 各排队目录的未评完（入队/刷新时登记总数）；
// 空闲时若最近一次是「停止」，被停任务的剩余也计入（标签据此显示已停止而非已评完）
const waitingCount = computed(() => {
  let n = 0
  for (const sid of [state.queue.current, ...state.queue.queued]) {
    const t = state.tasks[sid]
    if (t && t.total) n += Math.max(0, t.total - (t.finalN || 0))
  }
  if (!state.running && state.lastDone && state.lastDone.stopped) {
    const t = state.tasks[state.lastDone.session]
    if (t && t.total) n += Math.max(0, t.total - (t.finalN || 0))
  }
  return n
})
const queuedDirs = computed(() => state.queue.queued.map((sid) => {
  const t = state.tasks[sid] || {}
  const dir = t.dir || ''
  return { sid, dir, label: dir ? shortDirLabel(dir) : '待评分目录', waiting: t.total ? Math.max(0, t.total - (t.finalN || 0)) : 0 }
}))
function shortDirLabel(d) {
  const parts = String(d).split(/[\\/]/).filter(Boolean)
  return parts.slice(-2).join('/')
}
const hasQueue = computed(() =>
  state.running || state.queue.queued.length || state.scoreFeed.length || state.tally.done || state.tally.fail)
const pct = computed(() => {
  const denom = state.tally.done + waitingCount.value
  return denom ? Math.min(100, (state.tally.done / denom) * 100) : 0
})
const elapsedText = computed(() => {
  const t = cur.value
  if (!t || !t.startedAt || !state.running) return ''
  return fmtDur(Math.round((Date.now() - t.startedAt) / 1000))
})

// 时长人性化：秒 → 分秒 → 小时分 → 天小时 → 年天
function fmtDur(sec) {
  if (!Number.isFinite(sec) || sec <= 0) return ''
  if (sec < 60) return `${Math.round(sec)} 秒`
  const m = Math.floor(sec / 60)
  if (m < 60) return `${m} 分 ${Math.round(sec % 60)} 秒`
  const h = Math.floor(m / 60)
  if (h < 24) return `${h} 小时 ${m % 60} 分`
  const d = Math.floor(h / 24)
  if (d < 365) return `${d} 天 ${h % 24} 小时`
  return `${Math.floor(d / 365)} 年 ${Math.round(d % 365)} 天`
}
function fmtEta(sec) {
  const s = fmtDur(sec)
  return s ? `剩余 约 ${s}` : ''
}

// ETA：取当前阶段最近 ETA_WINDOW 条真实事件估速率（滑动窗口，提速/卡顿都能及时反映）。
// 刷新页面后 hydrate 回填的历史项 _ts 为 0（时间未知），必须排除——
// 此前混入样本会把时间跨度算成 Date.now() 级别（几十年），ETA 显示几百万分钟且几乎不降
const ETA_WINDOW = 20
const etaText = ref('')
let etaTimer = null
function tickEta() {
  const t = cur.value
  if (!t || t.finished || !t.total || !t.progress.length) { etaText.value = ''; return }
  const done = stage.value === 'compress' ? compressDone.value : scoreDone.value
  if (done <= 0) { etaText.value = ''; return }
  if (done >= t.total) { etaText.value = '即将完成…'; return }
  const stageStatuses = (stage.value === 'compress'
    ? t.progress.filter((x) => x.status === 'compressed')
    : t.progress.filter((x) => FINAL.has(x.status))
  ).filter((x) => x._ts > 0)
  if (stageStatuses.length < 2) { etaText.value = ''; return }
  const win = stageStatuses.slice(-ETA_WINDOW)
  const rate = ((win[win.length - 1]._ts - win[0]._ts) / 1000) / (win.length - 1)
  if (rate <= 0) { etaText.value = ''; return }
  etaText.value = fmtEta(Math.round((t.total - done) * rate))
}
let etaTimer2 = etaTimer
onMounted(() => { etaTimer2 = setInterval(tickEta, 1000) })
onBeforeUnmount(() => clearInterval(etaTimer2))

function iconOf(status) {
  if (status === 'scored') return '✅'
  if (status === 'parse_fail') return '⚠️'
  if (status === 'compressed') return '🗜️'
  return '❌'
}
function statusLabel(s) {
  return { failed: '调用失败', bad_image: '图片无法解码', unsupported: '格式不支持', duplicate: '重复跳过', compressed: '已压缩' }[s] || s
}

const scanning = ref(false)
const starting = ref(false)
// 夜间评分模式：失败自动重试直到全部成功（选择记忆到 localStorage）
const nightMode = ref(localStorage.getItem('sr_night_mode') === '1')
const compareMode = ref(false) // 多模型对比：主模型 + compareModels（同平台）+ compareCross（跨平台）并行评同一张
const crossPreset = ref('')
const crossModel = ref('')
const crossManual = ref('')
const crossNoList = ref(false) // 平台无 /models 清单接口（如天翼）→ 提示手动输入
const crossPresetModels = ref([])
const crossPresetLoading = ref(false)
const presets = computed(() => (state.config && state.config.presets) || [])

async function loadPresetModels() {
  crossModel.value = ''
  crossPresetModels.value = []
  crossNoList.value = false
  if (!crossPreset.value) return
  crossPresetLoading.value = true
  try {
    const r = await api('/api/preset/models', { method: 'POST', body: JSON.stringify({ name: crossPreset.value }) })
    crossPresetModels.value = r.vision || []
    if (!crossPresetModels.value.length) {
      crossNoList.value = true
      toast('该平台未返回可用模型清单，可直接在下方输入模型 ID')
    }
  } catch (e) {
    // 清单接口不存在/不可达：不阻塞——手动输入模型 ID 即可
    crossNoList.value = true
    toast('该平台无模型清单接口（或拉取失败），可直接输入模型 ID 添加')
  }
  crossPresetLoading.value = false
}
function addCrossModel() {
  const model = crossManual.value.trim() || crossModel.value
  if (!crossPreset.value || !model) return
  const exists = state.compareCross.some((c) => c.preset === crossPreset.value && c.model === model)
  if (!exists) state.compareCross.push({ preset: crossPreset.value, model })
  crossModel.value = ''
  crossManual.value = ''
}
function removeCrossModel(i) {
  state.compareCross.splice(i, 1)
}
watch(nightMode, (v) => localStorage.setItem('sr_night_mode', v ? '1' : '0'))
const importing = ref(false)
const importDone = ref(0)
const importTotal = ref(0)
const dragOver = ref(false)
const importedPreviews = ref([])
const previewMore = computed(() => Math.max(0, importedPreviews.value.length - 30))

async function doScan() {
  const dirs = parseDirs()
  if (!dirs.length) return toast('请先输入目录', true)
  scanning.value = true
  try {
    // 多目录逐个扫描后聚合展示
    let count = 0, live = 0, est = 0
    for (const d of dirs) {
      const r = await api('/api/scan', { method: 'POST', body: JSON.stringify({ dir: d, formats: formatsArg.value }) })
      const items = r.items || []
      count += items.length
      live += items.filter((i) => !i.dup).length
      est += r.est_cost || 0
    }
    state.scan = { count, live, est_cost: est, dupCount: count - live, dirs: dirs.length }
    toast(count
      ? `扫描完成：共 ${count} 张${dirs.length > 1 ? `（${dirs.length} 个目录）` : ''}`
      : '扫描完成：未发现可处理的图片（可能过小或格式不符）')
    refreshState() // 拉取最新目录历史
  } catch (e) {
    state.scan = null
    toast(e.message, true)
  }
  scanning.value = false
}

// ---------- 粘贴/拖入导入 ----------
function sanitizeName(name) {
  return name.replace(/[\\/:*?"<>|]/g, '_')
}

// 收集拖入项（支持文件夹递归）
async function collectEntries(items) {
  const files = []
  const walk = async (entry, path) => {
    if (!entry) return
    if (entry.isFile) {
      const f = await new Promise((res, rej) => entry.file(res, rej))
      f._rel = path + f.name
      files.push(f)
    } else if (entry.isDirectory) {
      const reader = entry.createReader()
      const read = () => new Promise((res) => reader.readEntries(res, () => res([])))
      let batch
      do {
        batch = await read()
        for (const e of batch) await walk(e, path + entry.name + '/')
      } while (batch.length)
    }
  }
  const roots = []
  for (const it of items) {
    const entry = it.webkitGetAsEntry && it.webkitGetAsEntry()
    if (entry) roots.push(entry)
    else {
      const f = it.getAsFile && it.getAsFile()
      if (f) { f._rel = f.name; files.push(f) }
    }
  }
  for (const e of roots) await walk(e, '')
  return files.filter((f) => !f.type || f.type.startsWith('image/') || /\.(jpe?g|png|webp|gif|bmp|tiff?|heic|heif|raf|cr2|cr3|nef|arw|dng)$/i.test(f.name))
}

async function importFiles(files) {
  if (!files.length) return
  importing.value = true
  importTotal.value = files.length
  importDone.value = 0
  try {
    // 上传到导入目录（每个导入任务独立归类）
    const fd = new FormData()
    const paths = []
    for (const f of files) {
      fd.append('files', f, sanitizeName(f.name))
      paths.push(sanitizeName(f._rel || f.name))
      importDone.value++
    }
    fd.append('paths', JSON.stringify(paths))
    if (state.importDir) fd.append('dir', state.importDir) // 追加到当前导入任务
    // 共用导入目录：从服务端获取持久化值（跨重启不丢）
    const impDir = await api('/api/import-dir').then(x => x.dir).catch(() => '')
    if (impDir) fd.append('dir', impDir)
    const r = await fetch('/api/import', { method: 'POST', body: fd }).then((x) => x.json())
    if (r.error) throw new Error(r.error)
    state.importDir = r.dir
    state.dir = r.dir
    // 预览（本地对象 URL，上限 30 张缩略展示）
    for (const f of files) {
      if (importedPreviews.value.length >= 30) break
      importedPreviews.value.push({ url: URL.createObjectURL(f), name: f._rel || f.name })
    }
    toast(`已导入 ${r.count} 张到导入任务，开始扫描`)
    await doScan()
  } catch (e) {
    toast('导入失败：' + e.message, true)
  }
  importing.value = false
}

// 粘贴（Ctrl+V 截图/照片）
function onPaste(e) {
  const files = []
  for (const item of e.clipboardData.items || []) {
    if (item.kind === 'file') {
      const f = item.getAsFile()
      if (f) files.push(f)
    }
  }
  if (files.length) {
    e.preventDefault()
    importFiles(files)
  }
}

// 拖入（照片/文件夹）
function onDragOver(e) {
  if (e.dataTransfer && [...(e.dataTransfer.types || [])].includes('Files')) {
    e.preventDefault()
    dragOver.value = true
  }
}
function onDragLeave() { dragOver.value = false }
async function onDrop(e) {
  dragOver.value = false
  if (!e.dataTransfer || !e.dataTransfer.items) return
  e.preventDefault()
  const files = await collectEntries([...e.dataTransfer.items])
  importFiles(files)
}

onMounted(() => {
  window.addEventListener('paste', onPaste)
  window.addEventListener('dragover', onDragOver)
  window.addEventListener('dragleave', onDragLeave)
  window.addEventListener('drop', onDrop)
})
onBeforeUnmount(() => {
  window.removeEventListener('paste', onPaste)
  window.removeEventListener('dragover', onDragOver)
  window.removeEventListener('dragleave', onDragLeave)
  window.removeEventListener('drop', onDrop)
  for (const pv of importedPreviews.value) URL.revokeObjectURL(pv.url)
})

async function loadModels() {
  loadingModels.value = true
  const m = await refreshModels()
  loadingModels.value = false
  if (m.vision.length === 0) toast('未发现视觉模型，可到设置调整识别规则', true)
  else if (!m.vision.includes(state.selModel)) state.selModel = m.vision[0]
}

// 开始评分：任务创建后立即返回（后台排队执行），可继续添加新任务
async function doStart(sample) {
  requestNotifyPermission() // 任务完成时弹系统通知（首次会请求权限，用户在场）
  // 未选模型时兜底为当前模型，不传空值覆盖有效默认（避免评分卡死）
  if (!state.selModel && state.currentModel) state.selModel = state.currentModel
  if (state.selModel && state.selModel !== state.currentModel) {
    starting.value = true
    try {
      await api('/api/model', { method: 'POST', body: JSON.stringify({ id: state.selModel }) })
      state.currentModel = state.selModel
    } catch (e) {
      toast('切换模型失败：' + e.message, true)
      starting.value = false
      return
    }
    starting.value = false
  }
  starting.value = true
  try {
    // 多目录：逐目录提交，依次进入排队（队列模型天然承接）
    const dirs = parseDirs()
    // 多模型对比：主模型 + 同平台对比 + 跨平台对比（总计至少 2 个才启用）
    let models
    let crossModels
    if (compareMode.value && !sample) {
      models = [state.selModel, ...state.compareModels.filter((m) => m && m !== state.selModel)]
      crossModels = state.compareCross.map((c) => ({ preset: c.preset, model: c.model }))
      if (models.length + crossModels.length < 2) {
        toast('多模型对比至少需要 2 个模型（同平台或跨平台均可）', true)
        starting.value = false
        return
      }
    }
    let resumed = 0, queued = 0, first = null
    for (const d of dirs) {
      const r = await api('/api/start', {
        method: 'POST',
        body: JSON.stringify({ dir: d, sample_n: sample ? 10 : 0, formats: formatsArg.value, night: nightMode.value && !sample, models, cross_models: crossModels }),
      })
      const t = taskOf(r.session_id)
      t.autoRetry = nightMode.value && !sample // feed 里失败行显示「将重新排队」
      t.dir = d // 排队目录名（队列视图展示用）
      // 入队即登记该目录的图片数（待评分计数包含排队目录；失败静默，开始后 stage 会再给）
      api(`/api/photos?session=${r.session_id}&page_size=1`).then((rr) => { if (rr.total) t.total = rr.total }).catch(() => {})
      if (r.resumed) resumed++
      if (state.queue.queued.includes(r.session_id)) queued++
      if (!first) first = r
    }
    state.currentModel = (first && first.model) || state.currentModel
    const nightTag = nightMode.value && !sample ? '🔁 自动重试：' : ''
    if (dirs.length > 1) {
      toast(`${nightTag}已提交 ${dirs.length} 个目录${queued ? `（${queued} 个排队中` : ''}${resumed ? `${queued ? '，' : '（'}${resumed} 个续跑${queued ? '' : '）'}` : ''}）`)
    } else if (first.resumed) {
      toast(`${nightTag}继续上次：剩余 ${first.pending} 张待评分${queued ? '，已排队' : ''}`)
    }
    else if (queued) toast(`${nightTag}已加入排队（前面还有 ${state.queue.queued.indexOf(first.session_id)} 个目录）`)
    else toast(`${nightTag}开始评分（模型 ${first.model}${models && (models.length + (crossModels ? crossModels.length : 0)) > 1 ? ` +${models.length - 1 + (crossModels ? crossModels.length : 0)} 对比` : ''}）`)
    refreshState()
  } catch (e) {
    toast(e.message, true)
  }
  starting.value = false
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
      // 分页拉全（>200 张一页拿不完；旧版后端无 LIMIT 会一次全给，两种都兼容）
      let items = [], total = 0, page = 1
      for (;;) {
        const r = await api(`/api/photos?page=${page}&page_size=200&session=${sid}`)
        total = r.total
        items = items.concat(r.items)
        if (items.length >= total || !r.items.length || page >= 50) break
        page++
      }
      const t = taskOf(sid)
      t.progress = items
        .filter((p) => p.status === 'compressed' || FINAL.has(p.status))
        .map((p) => ({ file: p.filename, score: p.score, status: p.status, error: p.error, cached: p.source === 'cache', _ts: 0 })) // _ts:0=时间未知，ETA 速率计算会排除
      if (t.progress.length > 400) t.progress.splice(0, t.progress.length - 400)
      t.total = total
      if (!t.stage) t.stage = 'score'
      if (items.length) t.dir = items[0].src_path.replace(/[\\/][^\\/]+$/, '') // 排队目录名
      // 单调统计按回填记录播种（feed 仅作展示）
      t.lastStatus = {}
      t.compN = 0
      t.finalN = 0
      for (const p of items) {
        if (p.status !== 'compressed' && !FINAL.has(p.status)) continue
        t.lastStatus[p.filename] = p.status
        if (p.status === 'compressed') t.compN++
        else t.finalN++
        // 全局累计与「最近评完」滚动列表同步播种
        if (p.status === 'scored') state.tally.done++
        else if (p.status === 'failed' || p.status === 'parse_fail') state.tally.fail++
        state.scoreFeed.push({ file: p.filename, status: p.status, score: p.score, error: p.error, cached: p.source === 'cache', session_id: sid })
      }
      if (state.scoreFeed.length > 10) state.scoreFeed.splice(0, state.scoreFeed.length - 10)
    } catch { /* 会话不可读则跳过 */ }
  }
}

onMounted(async () => {
  try {
    await refreshState()
    await hydrateTasks()
    if (!state.selModel) state.selModel = state.currentModel
    if (!state.models.vision.length) loadModels().catch((e) => console.error('[RunView] 模型拉取失败:', e))
  } catch (e) {
    console.error('[RunView] 初始化失败:', e)
  }
})
</script>

<style scoped>
.grid { display: flex; flex-direction: column; gap: 14px; width: 100%; }
.grow { flex: 1; min-width: 260px; }
.task-sel { max-width: 340px; font-size: 13px; padding: 4px 8px; vertical-align: middle; }
.feed-stats { font-size: 12px; }
.force-tag { background: #3a2c12; color: #ffa300; }
.retry-tag { background: #1f2c3a; color: #10aeff; }
.title.sub { margin-top: 6px; font-size: 14px; }
.feed { display: flex; flex-direction: column; gap: 4px; max-height: 340px; overflow-y: auto; }
.feed-item.clickable { cursor: pointer; }
.feed-item.clickable:hover { background: var(--card-2); border-radius: 6px; }
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
  padding: 2px 4px 2px 10px; font-size: 12px; cursor: pointer; max-width: 260px;
}
.chip-text { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; min-width: 0; }
.chip-x { flex: none; }
.import-preview { margin-top: 10px; }
.pv-grid { display: flex; gap: 6px; flex-wrap: wrap; }
.pv-item {
  width: 72px; height: 54px; border-radius: 6px; overflow: hidden;
  background: var(--card-2); display: flex; align-items: center; justify-content: center;
}
.pv-item img { width: 100%; height: 100%; object-fit: cover; }
.pv-more { color: var(--text-2); font-size: 12px; }
.dir-chip:hover { border-color: var(--accent); color: var(--accent); }
.chip-x { color: var(--text-2); font-size: 13px; padding: 0 1px; }
.chip-x:hover { color: var(--danger); }
.drop-hint {
  position: fixed; inset: 0; z-index: 98; pointer-events: none;
  border: 4px dashed var(--accent); background: rgba(7, 193, 96, 0.08);
  display: flex; align-items: center; justify-content: center;
  font-size: 20px; color: var(--accent); font-weight: 700;
}
</style>
