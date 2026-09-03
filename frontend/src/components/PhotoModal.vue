<template>
  <div class="pm-mask" @click.self="$emit('close')">
    <div class="pm-card">
      <!-- 照片区 -->
      <div class="pm-photo">
        <button v-if="hasPrev" class="pm-nav prev" title="上一张（←）" @click.stop="nav(-1)">‹</button>
        <img v-if="!noThumb" :src="`/api/thumb?id=${p.id}`" @error="noThumb = true" @click="$emit('close')" />
        <div v-else class="pm-noimg">缓存命中，无压缩图预览</div>
        <button v-if="hasNext" class="pm-nav next" title="下一张（→）" @click.stop="nav(1)">›</button>
        <span class="pm-badge" :class="badgeClass">{{ badgeText }}</span>
        <button class="btn plain small pm-close" @click="$emit('close')">✕</button>
      </div>

      <!-- 信息区 -->
      <div class="pm-body">
        <!-- 文件行 -->
        <div class="pm-file">
          <div class="pm-name" :title="p.src_path">{{ p.filename }}</div>
          <div class="pm-meta">
            <span class="tag">{{ disp.model || '—' }}</span>
            <span v-if="disp.duration_ms" class="pm-meta-item">{{ disp.duration_ms }}ms</span>
            <span v-if="disp.source === 'cache'" class="pm-meta-item">缓存复用</span>
            <span v-if="p.clamped && !viewed" class="pm-meta-item warn">分数裁剪</span>
            <span v-if="viewed" class="pm-meta-item hist-hint">查看历史 {{ viewed.created_at }}</span>
          </div>
        </div>

        <!-- 总分 + 四维得分条 -->
        <div class="pm-score-wrap" v-if="disp.dims">
          <div class="pm-score-main">
            <div class="pm-score-num">{{ disp.score.toFixed(1) }}</div>
            <div class="pm-score-sub">{{ viewed ? '历史总分' : '总分' }}<span>满分 10</span></div>
          </div>
          <div class="pm-dims">
            <div class="pm-dim" v-for="d in dimRows" :key="d.key">
              <span class="pm-dim-name">{{ d.name }}</span>
              <div class="pm-dim-track"><div class="pm-dim-fill" :style="{ width: (d.value * 10) + '%', background: d.color }"></div></div>
              <span class="pm-dim-val">{{ d.value.toFixed(1) }}</span>
              <span class="pm-dim-w">{{ d.weightPct }}%</span>
            </div>
          </div>
        </div>
        <div v-else class="pm-nodims">
          <span class="error-text">{{ viewed ? '该历史记录未包含维度分' : '评分解析失败，暂无维度分' }}</span>
          <span v-if="!viewed && p.error" class="pm-err-detail">{{ p.error }}</span>
        </div>

        <!-- 反馈区 -->
        <div class="pm-reasons">
          <div class="pm-reason" v-if="disp.strength">
            <span class="pm-ico good">✓</span>
            <span>{{ disp.strength }}</span>
          </div>
          <div class="pm-reason" v-if="disp.weakness">
            <span class="pm-ico bad">!</span>
            <span>{{ disp.weakness }}</span>
          </div>
          <div class="pm-tags" v-if="disp.tags && disp.tags.length">
            <span v-for="t in disp.tags" :key="t" class="tag">{{ t }}</span>
          </div>
        </div>

        <!-- 评分历史（多模型 / 多次重评）：点击切换查看该次详情 -->
        <div class="pm-history" v-if="history.length">
          <div class="pm-h-head">
            <b>评分历史</b>
            <span class="tag">{{ history.length }} 次</span>
            <span class="pm-h-models">
              <span v-for="m in historyModels" :key="m" class="tag">{{ m }}</span>
            </span>
            <span class="pm-h-tip">点击查看该次详情</span>
            <button v-if="viewed" class="btn plain small" @click="viewIdx = -1">返回最新</button>
          </div>
          <div class="pm-h-row" v-for="(h, i) in history" :key="i" :class="{ on: viewIdx === i, current: viewIdx < 0 && i === 0 }"
            :title="viewIdx === i ? '再次点击返回最新评分' : '点击查看这次评分的详情'" @click="selectHistory(i)">
            <span class="pm-h-score">{{ h.score.toFixed(1) }}</span>
            <span class="tag">{{ h.model || '—' }}</span>
            <span class="pm-h-src" :class="{ cached: h.source === 'cache' }">{{ h.source === 'cache' ? '缓存' : 'API' }}</span>
            <span v-if="viewIdx < 0 && i === 0" class="pm-h-latest">最新</span>
            <span class="pm-h-dims" v-if="h.dims">
              技 {{ h.dims.technique.toFixed(1) }} · 构 {{ h.dims.composition.toFixed(1) }} · 内 {{ h.dims.content.toFixed(1) }} · 色 {{ h.dims.color.toFixed(1) }}
            </span>
            <span class="pm-h-time">{{ h.created_at }}</span>
          </div>
        </div>

        <!-- 复检进行中：进度态 -->
        <div v-if="rescoring" class="pm-rescore">
          <div class="pm-rescore-head">
            <span class="pm-spinner"></span>
            <b>复检重评中…</b>
            <span class="pm-rescore-sub">{{ rescorePhase }}</span>
          </div>
          <div class="progress-track"><div class="progress-fill pm-rescore-fill" :style="{ width: rescorePct + '%' }"></div></div>
          <div v-if="rescoreResult" class="pm-rescore-result" :class="rescoreResult.ok ? 'ok' : 'err'">
            <template v-if="rescoreResult.ok">
              ✓ 复检成功：新总分 <b>{{ rescoreResult.score.toFixed(1) }}</b>
              <span v-if="rescoreResult.dims">（技术 {{ rescoreResult.dims.technique.toFixed(1) }} / 构图 {{ rescoreResult.dims.composition.toFixed(1) }} / 内容 {{ rescoreResult.dims.content.toFixed(1) }} / 色彩 {{ rescoreResult.dims.color.toFixed(1) }}）</span>
            </template>
            <template v-else>
              ✗ 复检失败：{{ rescoreResult.error }}
              <div class="pm-err-detail" v-if="rescoreResult.detail">{{ rescoreResult.detail }}</div>
              <div class="pm-rescore-hint">可关闭弹窗后再次点击「↻ 复检重评」重试</div>
            </template>
          </div>
        </div>

        <!-- 操作区 -->
        <div v-else class="pm-actions">
          <button class="btn small" :disabled="sharing" @click="copyShareCard">
            {{ sharing ? '生成中…' : copyHint }}
          </button>
          <a v-if="shareUrl" class="btn plain small" :href="shareUrl"
            :download="`SnapRank_${(p.filename || 'photo').replace(/\.[^.]+$/, '')}.png`">下载卡片</a>
          <span class="pm-flex"></span>
          <button v-if="canRescore" class="btn plain small" :disabled="busy"
            title="重新调用 AI 评分（忽略缓存）" @click="startRescore">↻ 复检重评</button>
          <button class="btn plain small" @click="$emit('close')">关闭</button>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, computed, watch, onMounted, onBeforeUnmount } from 'vue'
import { state, toast, api } from '../store.js'

const props = defineProps({
  photo: { type: Object, required: true },
  busy: { type: Boolean, default: false },
  list: { type: Array, default: () => [] }, // 所属批次照片列表（用于上一张/下一张）
})
const emit = defineEmits(['close', 'rescore', 'navigate'])

const p = computed(() => props.photo)
const sharing = ref(false)
const copyHint = ref('📋 复制分享卡片')
const shareUrl = ref('')
const noThumb = ref(false)

// 不支持格式与解码失败无法通过重试解决，其余状态（含已评分）均可重评
const canRescore = computed(() => !['unsupported', 'bad_image'].includes(p.value.status))
const badgeText = computed(() => {
  const s = p.value.status
  if (s === 'parse_fail') return '待复检'
  if (s === 'failed') return '调用失败'
  return p.value.score.toFixed(1) + ' 分'
})
const badgeClass = computed(() => {
  const s = p.value.status
  if (s === 'parse_fail' || s === 'failed') return 'bad'
  if (p.value.score >= 9) return 'best'
  if (p.value.score >= 7) return 'good'
  if (p.value.score >= 5) return 'mid'
  return 'low'
})

const hasPrev = computed(() => props.list.length > 1)
const hasNext = computed(() => props.list.length > 1)

const dimRows = computed(() => {
  if (!disp.value.dims) return []
  const w = (state.config && state.config.weights) || { technique: 0.4, composition: 0.3, content: 0.2, color: 0.1 }
  const sum = w.technique + w.composition + w.content + w.color || 1
  return [
    { key: 'technique', name: '技术质量', value: disp.value.dims.technique, weightPct: Math.round(w.technique / sum * 100), color: '#07c160' },
    { key: 'composition', name: '构图', value: disp.value.dims.composition, weightPct: Math.round(w.composition / sum * 100), color: '#10aeff' },
    { key: 'content', name: '内容情感', value: disp.value.dims.content, weightPct: Math.round(w.content / sum * 100), color: '#ffa300' },
    { key: 'color', name: '色彩', value: disp.value.dims.color, weightPct: Math.round(w.color / sum * 100), color: '#af52de' },
  ]
})

// 上一张/下一张（在 list 中循环）
function nav(dir) {
  if (!props.list || props.list.length < 2) return
  const i = props.list.findIndex((x) => x.id === p.value.id)
  if (i < 0) return
  const ni = (i + dir + props.list.length) % props.list.length
  emit('navigate', props.list[ni])
}

// ESC 关闭；←/→ 切换
function onKey(e) {
  if (e.key === 'Escape') emit('close')
  if (e.key === 'ArrowLeft') nav(-1)
  if (e.key === 'ArrowRight') nav(1)
}
onMounted(() => {
  window.addEventListener('keydown', onKey)
  loadHistory() // 首次打开弹窗加载评分历史
})
onBeforeUnmount(() => window.removeEventListener('keydown', onKey))

// ---------- 评分历史 ----------
const history = ref([])
const viewIdx = ref(-1) // -1 = 当前（最新）；>=0 = 正在查看的第 i 条历史
const historyModels = computed(() => [...new Set(history.value.map((h) => h.model).filter(Boolean))])
const viewed = computed(() => (viewIdx.value >= 0 ? history.value[viewIdx.value] || null : null))
// 弹窗主体展示的数据：查看历史时用该条记录，否则用照片当前数据
const disp = computed(() => {
  const h = viewed.value
  if (!h) return p.value
  return { ...p.value, score: h.score, dims: h.dims, tags: h.tags || [], strength: h.strength, weakness: h.weakness, model: h.model, source: h.source, duration_ms: 0 }
})
function selectHistory(i) {
  viewIdx.value = viewIdx.value === i ? -1 : i
}
async function loadHistory() {
  if (!p.value || !p.value.id) { history.value = []; viewIdx.value = -1; return }
  try {
    history.value = await api(`/api/photo/scores?id=${p.value.id}`)
  } catch {
    history.value = []
  }
  if (viewIdx.value >= history.value.length) viewIdx.value = -1
}

// 重置分享状态（换照片时）
// 注意：不能加 immediate——回调同步执行会碰到下方复检进度状态的 TDZ
watch(() => p.value && p.value.id, () => {
  shareUrl.value = ''
  copyHint.value = '📋 复制分享卡片'
  noThumb.value = false
  viewIdx.value = -1 // 换照片回到查看最新
  resetRescore()
  loadHistory()
})

// ---------- 复检进度 ----------
const rescoring = ref(false)
const rescorePhase = ref('')
const rescorePct = ref(8)
const rescoreResult = ref(null)   // {ok, score, dims} | {ok:false, error, detail}
let rescoreEs = null

function resetRescore() {
  rescoring.value = false
  rescorePhase.value = ''
  rescorePct.value = 8
  rescoreResult.value = null
  if (rescoreEs) { rescoreEs.close(); rescoreEs = null }
}

// 慢速爬升的伪进度（真实结果以 SSE 为准）
function startFakeProgress() {
  const timer = setInterval(() => {
    if (!rescoring.value) { clearInterval(timer); return }
    if (rescorePct.value < 90) rescorePct.value += Math.max(1, (92 - rescorePct.value) * 0.06)
  }, 500)
}

async function startRescore() {
  if (rescoring.value) return
  rescoring.value = true
  rescoreResult.value = null
  rescorePhase.value = '提交复检请求…'
  rescorePct.value = 8
  try {
    await api('/api/rescore', {
      method: 'POST',
      body: JSON.stringify({ ids: [p.value.id], force: true }),
    })
  } catch (e) {
    rescoring.value = false
    rescoreResult.value = { ok: false, error: e.message }
    return
  }
  rescorePhase.value = '等待 AI 重新评分…'
  startFakeProgress()
  // SSE 跟踪这张照片的重评进度（progress 事件按文件名匹配）
  rescoreEs = new EventSource('/api/events')
  const name = p.value.filename
  rescoreEs.addEventListener('progress', (e) => {
    try {
      const d = JSON.parse(e.data)
      if (d.file !== name || d.session_id !== d.session_id) return
      if (d.status === 'scored') {
        rescorePct.value = 100
        rescorePhase.value = '完成'
        rescoreResult.value = { ok: true, score: d.score }
        // 拉取完整明细（维度分）
        api(`/api/photo?id=${p.value.id}`).then((np) => {
          if (rescoreResult.value && rescoreResult.value.ok && np.dims) {
            rescoreResult.value.dims = np.dims
            // 弹窗照片数据同步为新结果
            Object.assign(p.value, np)
          }
        }).catch(() => {})
        finishRescore()
      } else if (d.status === 'parse_fail' || d.status === 'failed') {
        rescorePct.value = 100
        rescorePhase.value = '失败'
        rescoreResult.value = { ok: false, error: statusText(d.status), detail: d.error || '' }
        finishRescore()
      }
    } catch { /* 忽略解析错误 */ }
  })
  rescoreEs.addEventListener('error', () => { /* 断线由浏览器重连 */ })
}

function finishRescore() {
  loadHistory() // 重评结果已追加一条历史
  setTimeout(() => {
    if (rescoreEs) { rescoreEs.close(); rescoreEs = null }
    rescoring.value = false
  }, 2500) // 结果停留 2.5s 后回到操作区
}

function statusText(s) {
  return { parse_fail: '评分解析失败（模型未返回有效维度分）', failed: '模型调用失败' }[s] || s
}

onBeforeUnmount(() => { if (rescoreEs) rescoreEs.close() })

// ---------- 分享卡片 ----------
function loadImg(src, timeoutMs = 8000) {
  return new Promise((resolve, reject) => {
    const img = new Image()
    const timer = setTimeout(() => reject(new Error('图片加载超时')), timeoutMs)
    img.onload = () => { clearTimeout(timer); resolve(img) }
    img.onerror = () => { clearTimeout(timer); reject(new Error('图片加载失败')) }
    img.src = src
  })
}
function roundRect(ctx, x, y, w, h, r) {
  ctx.beginPath()
  ctx.moveTo(x + r, y)
  ctx.arcTo(x + w, y, x + w, y + h, r)
  ctx.arcTo(x + w, y + h, x, y + h, r)
  ctx.arcTo(x, y + h, x, y, r)
  ctx.arcTo(x, y, x + w, y, r)
  ctx.closePath()
}
function wrapText(ctx, text, maxWidth) {
  const lines = []
  let line = ''
  for (const ch of text) {
    if (ctx.measureText(line + ch).width > maxWidth) { lines.push(line); line = ch }
    else line += ch
  }
  if (line) lines.push(line)
  return lines
}

async function buildShareCard(photo) {
  const img = await loadImg(`/api/thumb?id=${photo.id}`)
  const W = 900, pad = 28
  const imgH = Math.round(W * (img.naturalHeight / img.naturalWidth))
  const canvas = document.createElement('canvas')
  canvas.width = W + pad * 2
  canvas.height = pad * 2 + 34 + imgH + 230
  const ctx = canvas.getContext('2d')

  ctx.fillStyle = '#101418'
  ctx.fillRect(0, 0, canvas.width, canvas.height)
  ctx.strokeStyle = '#07c160'
  ctx.lineWidth = 3
  roundRect(ctx, 8, 8, canvas.width - 16, canvas.height - 16, 18)
  ctx.stroke()

  ctx.save()
  roundRect(ctx, pad, pad + 34, W, imgH, 12)
  ctx.clip()
  ctx.drawImage(img, pad, pad + 34, W, imgH)
  ctx.restore()

  ctx.fillStyle = '#07c160'
  ctx.font = 'bold 22px "PingFang SC", "Microsoft YaHei", sans-serif'
  ctx.textBaseline = 'middle'
  ctx.fillText('帧选 SnapRank', pad, pad + 16)
  ctx.fillStyle = 'rgba(255,255,255,0.45)'
  ctx.font = '14px "PingFang SC", "Microsoft YaHei", sans-serif'
  ctx.textAlign = 'right'
  ctx.fillText('AI 摄影评分', pad + W, pad + 16)
  ctx.textAlign = 'left'

  let y = pad + 34 + imgH + 30
  ctx.fillStyle = '#ffffff'
  ctx.font = 'bold 46px "PingFang SC", "Microsoft YaHei", sans-serif'
  ctx.fillText(photo.score.toFixed(1), pad, y + 20)
  ctx.fillStyle = 'rgba(255,255,255,0.5)'
  ctx.font = '15px "PingFang SC", "Microsoft YaHei", sans-serif'
  ctx.fillText('总分 / 10', pad + 86, y + 26)

  const dims = [
    ['技术', photo.dims && photo.dims.technique, '#07c160'],
    ['构图', photo.dims && photo.dims.composition, '#10aeff'],
    ['内容', photo.dims && photo.dims.content, '#ffa300'],
    ['色彩', photo.dims && photo.dims.color, '#af52de'],
  ]
  const barW = 240
  dims.forEach((d, i) => {
    const col = i % 2, row = Math.floor(i / 2)
    const x = pad + 190 + col * 330
    const yy = y + 4 + row * 28
    ctx.fillStyle = 'rgba(255,255,255,0.75)'
    ctx.font = '14px "PingFang SC", "Microsoft YaHei", sans-serif'
    ctx.fillText(d[0], x, yy)
    ctx.fillStyle = 'rgba(255,255,255,0.12)'
    roundRect(ctx, x + 40, yy - 7, barW, 10, 5)
    ctx.fill()
    ctx.fillStyle = d[2]
    roundRect(ctx, x + 40, yy - 7, Math.max(8, barW * ((d[1] || 0) / 10)), 10, 5)
    ctx.fill()
    ctx.fillStyle = 'rgba(255,255,255,0.85)'
    ctx.fillText((d[1] || 0).toFixed(1), x + 40 + barW + 10, yy)
  })

  y += 92
  ctx.font = '15px "PingFang SC", "Microsoft YaHei", sans-serif'
  if (photo.strength) {
    ctx.fillStyle = '#07c160'
    ctx.fillText('✓ ' + wrapText(ctx, photo.strength, W - 20)[0], pad, y)
    y += 24
  }
  if (photo.weakness) {
    ctx.fillStyle = '#e8a23a'
    ctx.fillText('! ' + wrapText(ctx, photo.weakness, W - 20)[0], pad, y)
    y += 24
  }
  if (photo.tags && photo.tags.length) {
    ctx.fillStyle = 'rgba(255,255,255,0.55)'
    ctx.fillText(photo.tags.slice(0, 4).map(t => '#' + t).join('  '), pad, Math.min(y + 4, canvas.height - pad - 14))
  }
  return canvas
}

async function copyShareCard() {
  if (!disp.value) return
  sharing.value = true
  copyHint.value = '生成中…'
  try {
    const canvas = await buildShareCard(disp.value)
    shareUrl.value = canvas.toDataURL('image/png')
    let copied = false
    if (navigator.clipboard && window.ClipboardItem) {
      const blob = await new Promise(res => canvas.toBlob(res, 'image/png'))
      try {
        await navigator.clipboard.write([new ClipboardItem({ 'image/png': blob })])
        copied = true
      } catch { /* 环境不支持图片写入 */ }
    }
    copyHint.value = copied ? '✅ 已复制，可直接粘贴分享' : '已生成，请用“下载卡片”'
  } catch (e) {
    copyHint.value = '生成失败'
    toast(e.message, true)
  }
  sharing.value = false
  setTimeout(() => { copyHint.value = '📋 复制分享卡片' }, 4000)
}
</script>

<style scoped>
.pm-mask {
  position: fixed; inset: 0; background: rgba(0, 0, 0, 0.72); z-index: 100;
  display: flex; align-items: center; justify-content: center; padding: 24px;
}
.pm-card {
  background: var(--card); border-radius: 16px; overflow: hidden;
  width: min(880px, 94vw); max-height: 92vh; display: flex; flex-direction: column;
  box-shadow: 0 12px 48px rgba(0, 0, 0, 0.35);
}
.pm-photo { position: relative; background: #000; display: flex; justify-content: center; }
.pm-photo img { max-width: 100%; max-height: 52vh; object-fit: contain; display: block; cursor: zoom-out; }
.pm-noimg {
  min-height: 200px; width: 100%; display: flex; align-items: center; justify-content: center;
  color: var(--text-2); font-size: 14px;
}
.pm-badge {
  position: absolute; left: 14px; bottom: 14px; padding: 4px 14px;
  border-radius: 999px; color: #fff; font-weight: 700; font-size: 15px; background: #888;
}
.pm-badge.best { background: #07c160; } .pm-badge.good { background: #10aeff; }
.pm-badge.mid { background: #ffa300; } .pm-badge.bad { background: #fa5151; }
.pm-close { position: absolute; right: 10px; top: 10px; }
.pm-nav {
  position: absolute; top: 50%; transform: translateY(-50%);
  width: 40px; height: 56px; border: none; cursor: pointer;
  background: rgba(0, 0, 0, 0.45); color: #fff; font-size: 30px;
  line-height: 1; border-radius: 8px; z-index: 2;
}
.pm-nav:hover { background: rgba(0, 0, 0, 0.7); }
.pm-nav.prev { left: 10px; }
.pm-nav.next { right: 10px; }
.pm-body { padding: 16px 20px 18px; display: flex; flex-direction: column; gap: 14px; overflow-y: auto; }

.pm-file { display: flex; align-items: center; gap: 12px; flex-wrap: wrap; }
.pm-name { font-size: 15px; font-weight: 600; flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.pm-meta { display: flex; gap: 10px; align-items: center; }
.pm-meta-item { font-size: 12px; color: var(--text-2); }
.pm-meta-item.warn { color: var(--warn); }

.pm-score-wrap { display: flex; gap: 22px; align-items: center; background: var(--card-2); border-radius: 12px; padding: 14px 16px; }
.pm-score-main { display: flex; flex-direction: column; align-items: center; gap: 2px; }
.pm-score-num { font-size: 38px; font-weight: 800; line-height: 1; color: var(--accent); font-variant-numeric: tabular-nums; }
.pm-score-sub { font-size: 12px; color: var(--text-2); display: flex; flex-direction: column; align-items: center; }
.pm-dims { flex: 1; display: flex; flex-direction: column; gap: 8px; min-width: 0; }
.pm-dim { display: flex; align-items: center; gap: 10px; }
.pm-dim-name { width: 62px; font-size: 13px; flex: none; }
.pm-dim-track { flex: 1; height: 9px; background: rgba(127, 127, 127, 0.18); border-radius: 999px; overflow: hidden; }
.pm-dim-fill { height: 100%; border-radius: 999px; transition: width 0.3s; }
.pm-dim-val { width: 34px; text-align: right; font-size: 13px; font-weight: 600; font-variant-numeric: tabular-nums; }
.pm-dim-w { width: 34px; text-align: right; font-size: 12px; color: var(--text-2); font-variant-numeric: tabular-nums; }

.pm-nodims { background: var(--card-2); border-radius: 12px; padding: 14px 16px; display: flex; flex-direction: column; gap: 6px; }
.pm-err-detail { font-size: 12px; color: var(--text-2); word-break: break-all; }

.pm-reasons { display: flex; flex-direction: column; gap: 8px; }
.pm-reason { display: flex; gap: 10px; font-size: 13px; line-height: 1.6; align-items: flex-start; }
.pm-ico {
  flex: none; width: 20px; height: 20px; border-radius: 50%; margin-top: 1px;
  display: inline-flex; align-items: center; justify-content: center;
  color: #fff; font-size: 12px; font-weight: 700;
}
.pm-ico.good { background: var(--accent); }
.pm-ico.bad { background: var(--warn); }
.pm-tags { display: flex; gap: 6px; flex-wrap: wrap; margin-top: 2px; }

.pm-actions { display: flex; gap: 10px; align-items: center; border-top: 1px solid var(--line); padding-top: 14px; }

.pm-history { border-top: 1px solid var(--line); padding-top: 12px; display: flex; flex-direction: column; gap: 6px; }
.pm-h-head { display: flex; align-items: center; gap: 8px; margin-bottom: 2px; }
.pm-h-models { display: flex; gap: 4px; flex-wrap: wrap; }
.pm-h-tip { color: var(--text-2); font-size: 11px; margin-left: auto; }
.pm-h-row {
  display: flex; align-items: center; gap: 8px; font-size: 12px;
  background: var(--card-2); border-radius: 8px; padding: 6px 10px;
  cursor: pointer; outline: 2px solid transparent; transition: outline-color 0.15s, background 0.15s;
}
.pm-h-row:hover { background: rgba(127, 127, 127, 0.14); }
.pm-h-row.on { outline: 2px solid var(--accent); }
.pm-h-row.current { box-shadow: inset 3px 0 0 var(--accent); }
.pm-h-score { font-weight: 800; font-size: 15px; color: var(--accent); font-variant-numeric: tabular-nums; min-width: 30px; }
.pm-h-src { color: var(--text-2); flex: none; }
.pm-h-src.cached { color: var(--warn); }
.pm-h-latest {
  flex: none; font-size: 10px; font-weight: 700; color: #fff;
  background: var(--accent); border-radius: 4px; padding: 1px 5px;
}
.pm-h-dims { color: var(--text-2); margin-left: auto; font-variant-numeric: tabular-nums; }
.pm-h-time { color: var(--text-2); font-size: 11px; flex: none; }
.pm-meta-item.hist-hint { color: var(--accent); }

.pm-rescore { border-top: 1px solid var(--line); padding-top: 14px; display: flex; flex-direction: column; gap: 10px; }
.pm-rescore-head { display: flex; align-items: center; gap: 10px; }
.pm-rescore-sub { color: var(--text-2); font-size: 13px; }
.pm-spinner {
  width: 16px; height: 16px; border-radius: 50%; flex: none;
  border: 2px solid var(--accent); border-top-color: transparent;
  animation: pm-spin 0.8s linear infinite;
}
@keyframes pm-spin { to { transform: rotate(360deg); } }
.pm-rescore-fill { transition: width 0.5s; }
.pm-rescore-result { font-size: 13px; line-height: 1.6; border-radius: 8px; padding: 10px 12px; }
.pm-rescore-result.ok { background: var(--accent-weak); color: var(--accent); }
.pm-rescore-result.err { background: rgba(250, 81, 81, 0.08); color: var(--danger); }
.pm-rescore-hint { color: var(--text-2); font-size: 12px; margin-top: 4px; }
.pm-flex { flex: 1; }
</style>
