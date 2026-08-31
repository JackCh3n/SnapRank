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
      <div class="row" style="margin-top: 10px" v-if="parseFailCount > 0">
        <button class="btn plain" :disabled="state.running" @click="rescoreAllParseFail">
          ↻ 一键重评全部待复检（{{ parseFailCount }} 张）
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
      <h3 class="title">评分复核 <span class="muted">（可在下拉中手动调档）</span></h3>
      <div class="photo-grid">
        <div v-for="p in photos" :key="p.id" class="photo-card">
          <div class="thumb-wrap clickable" :class="{ 'parse-fail': p.status === 'parse_fail' }"
            @click="preview = p" title="点击查看大图与评分详情">
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
          <div class="card-actions">
            <select class="bucket-sel" :value="p.override_bucket || autoBucket(p)" @change="setBucket(p, $event.target.value)">
              <option v-for="b in bucketNames" :key="b" :value="b">{{ b }}</option>
            </select>
            <button class="btn plain small" :disabled="state.running" title="重新调用 AI 评分（忽略缓存，约 1 次调用费用）"
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

    <!-- 大图 + 评分详情弹窗 -->
    <div v-if="preview" class="lightbox" @click.self="preview = null">
      <div class="lightbox-card">
        <img :src="`/api/thumb?id=${preview.id}`" />
        <button class="btn plain small lb-close" @click="preview = null">✕ 关闭</button>
        <div class="lb-body">
          <div class="lb-head">
            <b class="lb-name">{{ preview.filename }}</b>
            <span class="lb-score" :class="scoreClass(preview.score)">
              {{ preview.status === 'parse_fail' ? '待复检' : preview.score.toFixed(1) + ' 分' }}
            </span>
          </div>

          <div class="lb-dims" v-if="preview.dims">
            <div class="lb-dims-title">评分明细（维度分 × 权重 = 加权总分）</div>
            <div class="dim-row" v-for="d in dimRows(preview)" :key="d.key">
              <span class="dim-name">{{ d.name }}</span>
              <div class="dim-track"><div class="dim-fill" :style="{ width: (d.value * 10) + '%', background: d.color }"></div></div>
              <span class="dim-val">{{ d.value.toFixed(1) }} × {{ d.weightPct }}%</span>
            </div>
            <div class="dim-total">总分 = <b>{{ preview.score.toFixed(1) }}</b>（0–10 分制）</div>
          </div>
          <div v-else class="error-text">评分解析失败，暂无维度分</div>

          <div class="lb-reasons">
            <div class="lb-reason" v-if="preview.strength"><span class="lb-ico good">✓</span>{{ preview.strength }}</div>
            <div class="lb-reason" v-if="preview.weakness"><span class="lb-ico bad">!</span>{{ preview.weakness }}</div>
            <div class="lb-tags" v-if="preview.tags && preview.tags.length">
              <span v-for="t in preview.tags" :key="t" class="tag">{{ t }}</span>
            </div>
            <div class="error-text" v-if="preview.status === 'parse_fail' && preview.error">解析失败：{{ preview.error }}</div>
          </div>

          <div class="lb-actions">
            <button class="btn small" :disabled="sharing" @click="copyShareCard">
              {{ sharing ? '生成中…' : copyHint }}
            </button>
            <a v-if="shareUrl" class="btn plain small" :href="shareUrl" :download="`SnapRank_${preview.filename.replace(/\.[^.]+$/, '')}.png`">下载卡片</a>
            <button v-if="preview.status === 'parse_fail' || preview.status === 'failed'"
              class="btn plain small" :disabled="state.running"
              @click="rescoreOne(preview); preview = null">↻ 复检重评</button>
            <button class="btn plain small" @click="preview = null">关闭</button>
          </div>
        </div>
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
const preview = ref(null)
const sharing = ref(false)
const copyHint = ref('📋 复制分享卡片')
const shareUrl = ref('')

// 绘制带品牌边框的分享卡片（canvas），返回 blob
function loadImg(src, timeoutMs = 8000) {
  return new Promise((resolve, reject) => {
    const img = new Image()
    const timer = setTimeout(() => reject(new Error('图片加载超时')), timeoutMs)
    img.onload = () => { clearTimeout(timer); resolve(img) }
    img.onerror = () => { clearTimeout(timer); reject(new Error('图片加载失败')) }
    img.src = src
  })
}

async function buildShareCard(p) {
  const img = await loadImg(`/api/thumb?id=${p.id}`)

  const W = 900
  const pad = 28            // 品牌边框留白
  const imgH = Math.round(W * (img.naturalHeight / img.naturalWidth))
  const infoH = 250
  const canvas = document.createElement('canvas')
  canvas.width = W + pad * 2
  canvas.height = pad * 2 + imgH + infoH
  const ctx = canvas.getContext('2d')

  // 背景与品牌边框（暗色卡片 + 绿色描边 + 圆角）
  ctx.fillStyle = '#101418'
  ctx.fillRect(0, 0, canvas.width, canvas.height)
  ctx.strokeStyle = '#07c160'
  ctx.lineWidth = 3
  roundRect(ctx, 8, 8, canvas.width - 16, canvas.height - 16, 18)
  ctx.stroke()

  // 照片（圆角裁剪）
  ctx.save()
  roundRect(ctx, pad, pad + 34, W, imgH, 12)
  ctx.clip()
  ctx.drawImage(img, pad, pad + 34, W, imgH)
  ctx.restore()

  // 品牌头：帧选 SnapRank
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
  // 总分（大号）
  ctx.fillStyle = '#ffffff'
  ctx.font = 'bold 46px "PingFang SC", "Microsoft YaHei", sans-serif'
  ctx.fillText(p.score.toFixed(1), pad, y + 20)
  ctx.fillStyle = 'rgba(255,255,255,0.5)'
  ctx.font = '15px "PingFang SC", "Microsoft YaHei", sans-serif'
  ctx.fillText('总分 / 10', pad + 86, y + 26)

  // 四维小条
  const dims = [
    ['技术', p.dims.technique, 0.4, '#07c160'],
    ['构图', p.dims.composition, 0.3, '#10aeff'],
    ['内容', p.dims.content, 0.2, '#ffa300'],
    ['色彩', p.dims.color, 0.1, '#af52de'],
  ]
  const barW = 240
  dims.forEach((d, i) => {
    const col = i % 2, row = Math.floor(i / 2)
    const x = pad + 190 + col * 330
    const yy = y + 4 + row * 28
    ctx.fillStyle = 'rgba(255,255,255,0.75)'
    ctx.font = '14px "PingFang SC", "Microsoft YaHei", sans-serif'
    ctx.fillText(d[0], x, yy)
    // 背景 + 得分条
    ctx.fillStyle = 'rgba(255,255,255,0.12)'
    roundRect(ctx, x + 40, yy - 7, barW, 10, 5)
    ctx.fill()
    ctx.fillStyle = d[3]
    roundRect(ctx, x + 40, yy - 7, Math.max(8, barW * (d[1] / 10)), 10, 5)
    ctx.fill()
    ctx.fillStyle = 'rgba(255,255,255,0.85)'
    ctx.fillText(d[1].toFixed(1), x + 40 + barW + 10, yy)
  })

  // 评语（优点/不足，自动换行截断）
  y += 92
  ctx.font = '15px "PingFang SC", "Microsoft YaHei", sans-serif'
  if (p.strength) {
    ctx.fillStyle = '#07c160'
    ctx.fillText('✓ ' + wrapText(ctx, p.strength, W - 20)[0], pad, y)
    y += 24
  }
  if (p.weakness) {
    ctx.fillStyle = '#e8a23a'
    ctx.fillText('! ' + wrapText(ctx, p.weakness, W - 20)[0], pad, y)
    y += 24
  }
  // 标签
  if (p.tags && p.tags.length) {
    ctx.fillStyle = 'rgba(255,255,255,0.55)'
    ctx.fillText(p.tags.slice(0, 4).map(t => '#' + t).join('  '), pad, Math.min(y + 4, canvas.height - pad - 14))
  }
  return canvas
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

async function copyShareCard() {
  if (!preview.value) return
  sharing.value = true
  copyHint.value = '生成中…'
  try {
    const canvas = await buildShareCard(preview.value)
    shareUrl.value = canvas.toDataURL('image/png')
    // 优先 ClipboardItem 写图片；旧浏览器降级提示用下载
    let copied = false
    if (navigator.clipboard && window.ClipboardItem) {
      const blob = await new Promise(res => canvas.toBlob(res, 'image/png'))
      try {
        await navigator.clipboard.write([new ClipboardItem({ 'image/png': blob })])
        copied = true
      } catch { /* 部分环境不支持图片写入 */ }
    }
    copyHint.value = copied ? '✅ 已复制，可直接粘贴分享' : '已生成，请用“下载卡片”'
  } catch (e) {
    copyHint.value = '生成失败'
    toast(e.message, true)
  }
  sharing.value = false
  setTimeout(() => { copyHint.value = '📋 复制分享卡片' }, 4000)
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

async function rescoreOne(p) {
  try {
    await api('/api/rescore', { method: 'POST', body: JSON.stringify({ ids: [p.id], force: true }) })
    toast(`已提交复检：${p.filename}，完成后自动刷新`)
  } catch (e) {
    toast(e.message, true)
  }
}

async function rescoreAllParseFail() {
  try {
    const r = await api('/api/rescore', { method: 'POST', body: JSON.stringify({ all: true }) })
    toast(`已提交 ${r.count} 张复检重评`)
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
.card-actions { display: flex; gap: 6px; align-items: center; margin: 6px 8px 8px; }
.card-actions .bucket-sel { margin: 0; flex: 1; min-width: 0; }
.parse-fail { outline: 2px solid var(--danger); outline-offset: -2px; border-radius: 10px; }
.thumb-wrap.clickable { cursor: zoom-in; }
.lightbox {
  position: fixed; inset: 0; background: rgba(0,0,0,0.72); z-index: 100;
  display: flex; align-items: center; justify-content: center; padding: 24px;
}
.lightbox-card {
  background: var(--card); border-radius: 14px; max-width: min(880px, 94vw);
  max-height: 92vh; overflow-y: auto; position: relative;
}
.lightbox-card > img { max-width: 100%; max-height: 56vh; object-fit: contain; display: block; }
.lb-close { position: absolute; right: 10px; top: 10px; }
.lb-body { padding: 14px 18px 18px; display: flex; flex-direction: column; gap: 12px; }
.lb-head { display: flex; align-items: center; gap: 12px; }
.lb-name { font-size: 15px; flex: 1; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.lb-score { padding: 3px 12px; border-radius: 999px; color: #fff; font-weight: 700; background: #888; }
.lb-dims { display: flex; flex-direction: column; gap: 6px; }
.lb-dims-title { font-size: 13px; color: var(--text-2); }
.dim-row { display: flex; align-items: center; gap: 10px; }
.dim-name { width: 64px; font-size: 13px; }
.dim-track { flex: 1; height: 10px; background: var(--card-2); border-radius: 999px; overflow: hidden; }
.dim-fill { height: 100%; border-radius: 999px; transition: width 0.3s; }
.dim-val { width: 110px; text-align: right; font-size: 13px; color: var(--text-2); font-variant-numeric: tabular-nums; }
.dim-total { font-size: 13px; color: var(--text-2); text-align: right; }
.lb-reasons { display: flex; flex-direction: column; gap: 6px; }
.lb-reason { display: flex; gap: 8px; font-size: 13px; line-height: 1.5; align-items: baseline; }
.lb-ico { flex: none; width: 18px; height: 18px; border-radius: 50%; display: inline-flex;
  align-items: center; justify-content: center; color: #fff; font-size: 12px; font-weight: 700; }
.lb-ico.good { background: var(--accent); }
.lb-ico.bad { background: var(--warn); }
.lb-tags { display: flex; gap: 6px; flex-wrap: wrap; }
.lb-actions { display: flex; gap: 10px; justify-content: flex-end; }
</style>
