<template>
  <div class="pm-mask" @click.self="$emit('close')">
    <div class="pm-card">
      <!-- 照片区 -->
      <div class="pm-photo">
        <img :src="`/api/thumb?id=${p.id}`" @click="$emit('close')" />
        <span class="pm-badge" :class="badgeClass">{{ badgeText }}</span>
        <button class="btn plain small pm-close" @click="$emit('close')">✕</button>
      </div>

      <!-- 信息区 -->
      <div class="pm-body">
        <!-- 文件行 -->
        <div class="pm-file">
          <div class="pm-name" :title="p.src_path">{{ p.filename }}</div>
          <div class="pm-meta">
            <span class="tag">{{ p.model || '—' }}</span>
            <span v-if="p.duration_ms" class="pm-meta-item">{{ p.duration_ms }}ms</span>
            <span v-if="p.source === 'cache'" class="pm-meta-item">缓存复用</span>
            <span v-if="p.clamped" class="pm-meta-item warn">分数裁剪</span>
          </div>
        </div>

        <!-- 总分 + 四维得分条 -->
        <div class="pm-score-wrap" v-if="p.dims">
          <div class="pm-score-main">
            <div class="pm-score-num">{{ p.score.toFixed(1) }}</div>
            <div class="pm-score-sub">总分<span>满分 10</span></div>
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
          <span class="error-text">评分解析失败，暂无维度分</span>
          <span v-if="p.error" class="pm-err-detail">{{ p.error }}</span>
        </div>

        <!-- 反馈区 -->
        <div class="pm-reasons">
          <div class="pm-reason" v-if="p.strength">
            <span class="pm-ico good">✓</span>
            <span>{{ p.strength }}</span>
          </div>
          <div class="pm-reason" v-if="p.weakness">
            <span class="pm-ico bad">!</span>
            <span>{{ p.weakness }}</span>
          </div>
          <div class="pm-tags" v-if="p.tags && p.tags.length">
            <span v-for="t in p.tags" :key="t" class="tag">{{ t }}</span>
          </div>
        </div>

        <!-- 操作区 -->
        <div class="pm-actions">
          <button class="btn small" :disabled="sharing" @click="copyShareCard">
            {{ sharing ? '生成中…' : copyHint }}
          </button>
          <a v-if="shareUrl" class="btn plain small" :href="shareUrl"
            :download="`SnapRank_${(p.filename || 'photo').replace(/\.[^.]+$/, '')}.png`">下载卡片</a>
          <span class="pm-flex"></span>
          <button v-if="canRescore" class="btn plain small" :disabled="busy"
            title="重新调用 AI 评分（忽略缓存）" @click="$emit('rescore', p)">↻ 复检重评</button>
          <button class="btn plain small" @click="$emit('close')">关闭</button>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, computed, watch, onMounted, onBeforeUnmount } from 'vue'
import { state, toast } from '../store.js'

const props = defineProps({
  photo: { type: Object, required: true },
  busy: { type: Boolean, default: false },
})
const emit = defineEmits(['close', 'rescore'])

const p = computed(() => props.photo)
const sharing = ref(false)
const copyHint = ref('📋 复制分享卡片')
const shareUrl = ref('')

const canRescore = computed(() => ['parse_fail', 'failed'].includes(p.value.status))
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

const dimRows = computed(() => {
  if (!p.value.dims) return []
  const w = (state.config && state.config.weights) || { technique: 0.4, composition: 0.3, content: 0.2, color: 0.1 }
  const sum = w.technique + w.composition + w.content + w.color || 1
  return [
    { key: 'technique', name: '技术质量', value: p.value.dims.technique, weightPct: Math.round(w.technique / sum * 100), color: '#07c160' },
    { key: 'composition', name: '构图', value: p.value.dims.composition, weightPct: Math.round(w.composition / sum * 100), color: '#10aeff' },
    { key: 'content', name: '内容情感', value: p.value.dims.content, weightPct: Math.round(w.content / sum * 100), color: '#ffa300' },
    { key: 'color', name: '色彩', value: p.value.dims.color, weightPct: Math.round(w.color / sum * 100), color: '#af52de' },
  ]
})

// ESC 关闭
function onKey(e) { if (e.key === 'Escape') emit('close') }
onMounted(() => window.addEventListener('keydown', onKey))
onBeforeUnmount(() => window.removeEventListener('keydown', onKey))

// 重置分享状态（换照片时）
watch(() => p.value && p.value.id, () => {
  shareUrl.value = ''
  copyHint.value = '📋 复制分享卡片'
})

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
  if (!p.value) return
  sharing.value = true
  copyHint.value = '生成中…'
  try {
    const canvas = await buildShareCard(p.value)
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
.pm-badge {
  position: absolute; left: 14px; bottom: 14px; padding: 4px 14px;
  border-radius: 999px; color: #fff; font-weight: 700; font-size: 15px; background: #888;
}
.pm-badge.best { background: #07c160; } .pm-badge.good { background: #10aeff; }
.pm-badge.mid { background: #ffa300; } .pm-badge.bad { background: #fa5151; }
.pm-close { position: absolute; right: 10px; top: 10px; }
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
.pm-flex { flex: 1; }
</style>
