<template>
  <div class="grid" v-if="cfg">
    <div class="card">
      <h3 class="title">平台接入</h3>
      <div class="row" v-if="presets.length" style="margin-bottom: 10px">
        <label class="field">
          快速切换预设
          <select :value="activePresetName" @change="applyPreset($event.target.value)" style="width: 200px">
            <option value="" disabled>选择已保存的平台…</option>
            <option v-for="ps in presets" :key="ps.name" :value="ps.name">{{ ps.name }}</option>
          </select>
        </label>
        <button class="btn plain small" :disabled="!activePresetName" @click="deletePreset">🗑️ 删除该预设</button>
        <span class="muted">切换后自动填入对应的 URL 和 Key</span>
      </div>
      <div class="row">
        <label class="field">
          Provider 类型
          <select v-model="cfg.provider.type">
            <option value="tokenrhythm">tokenrhythm（基元律动，OpenAI 兼容）</option>
            <option value="mock">mock（离线演示，0 成本）</option>
          </select>
        </label>
        <label class="field grow">
          Base URL
          <input v-model="cfg.provider.base_url" placeholder="https://api.tokenrhythm.studio/v1" />
        </label>
        <label class="field grow">
          协议
          <select v-model="cfg.provider.protocol" style="width: 170px"
            title="火山 coding plan 等网关两种都支持；Anthropic 官方选 anthropic"
            @change="onProtocolChange">
            <option value="chat">Chat Completions</option>
            <option value="anthropic">Anthropic Messages</option>
          </select>
        </label>
        <label class="field">
          API Key（本地明文展示，可直接编辑）
          <input v-model="cfg.provider.api_key" placeholder="sk-..." />
        </label>
        <button class="btn plain small" title="填入火山 coding plan Anthropic 地址（/api/coding）"
          @click="fillVolcano('anthropic')">火山 Anthropic</button>
        <button class="btn plain small" title="填入火山 coding plan OpenAI 地址（/api/coding/v3）"
          @click="fillVolcano('chat')">火山 OpenAI v3</button>
      </div>
      <div class="row" style="margin-top: 12px">
        <button class="btn plain" @click="testConn">{{ testing ? '测试中…' : '测试连接' }}</button>
        <button class="btn plain small" @click="saveAsPreset">💾 将当前 URL+Key 存为预设</button>
        <input v-model="presetSaveName" placeholder="预设名（如：基元律动主力）" style="width: 200px"
          :disabled="!cfg.provider.base_url" />
        <span v-if="connState" :class="connState.ok ? 'tag' : 'error-text'">
          {{ connState.message }}
          <span v-if="connState.ok && connState.models">（{{ connState.models.length }} 个模型）</span>
        </span>
      </div>
    </div>

    <div class="card">
      <h3 class="title">模型</h3>
      <div class="row">
        <label class="field">
          默认模型
          <input v-model="cfg.model.default" style="width: 220px" />
        </label>
        <label class="field grow">
          视觉模型识别正则（每行一条，用于过滤 /v1/models 清单）
          <textarea v-model="visionPatterns" rows="4"></textarea>
        </label>
      </div>
      <div class="muted" style="margin-top: 6px" v-if="state.models.all.length">
        平台全部模型：{{ state.models.all.join('、') }}
      </div>
    </div>

    <div class="card">
      <h3 class="title">评分参数</h3>
      <div class="row">
        <label class="field">temperature <input type="number" step="0.1" min="0" max="1" v-model="cfg.score.temperature" style="width: 90px" /></label>
        <label class="field">
          思考强度
          <select v-model="cfg.score.reasoning_effort" style="width: 130px">
            <option value="">模型默认</option>
            <option value="low">low（快/省）</option>
            <option value="medium">medium</option>
            <option value="high">high（慢/细）</option>
          </select>
        </label>
        <label class="field">max_tokens <input type="number" v-model="cfg.score.max_tokens" style="width: 90px" /></label>
        <label class="field">超时（秒）<input type="number" v-model="cfg.score.timeout_sec" style="width: 90px" /></label>
        <label class="field">阈值①精选 ≥ <input type="number" step="0.5" v-model="cfg.score.thresholds[0]" style="width: 70px" /></label>
        <label class="field">阈值②良好 ≥ <input type="number" step="0.5" v-model="cfg.score.thresholds[1]" style="width: 70px" /></label>
        <label class="field">阈值③一般 ≥ <input type="number" step="0.5" v-model="cfg.score.thresholds[2]" style="width: 70px" /></label>
        <label class="field row" style="flex-direction: row; align-items: center">
          <input type="checkbox" v-model="cfg.score.reuse_scores" /> 跨会话评分缓存（重复导入不重复计费）
        </label>
      </div>
    </div>

    <div class="card">
      <h3 class="title">流水线与压缩</h3>
      <div class="row">
        <label class="field">评分并发 <input type="number" min="1" max="16" v-model="cfg.pipeline.score_concurrency" style="width: 80px" /></label>
        <label class="field">压缩最长边 <input type="number" step="128" v-model="cfg.pipeline.max_edge" style="width: 90px" /></label>
        <label class="field">MozJPEG 质量 <input type="number" min="40" max="100" v-model="cfg.pipeline.jpeg_quality" style="width: 80px" /></label>
        <label class="field">跳过小于（KB）<input type="number" v-model="cfg.pipeline.min_file_size_kb" style="width: 90px" /></label>
        <label class="field grow">DD鹅 lib 目录 <input v-model="cfg.paths.lib_dir" /></label>
      </div>
    </div>

    <div class="card">
      <h3 class="title">成本护栏</h3>
      <div class="row">
        <label class="field">单批次上限（¥）<input type="number" step="1" v-model="cfg.cost.batch_limit" style="width: 100px" /></label>
        <label class="field">每日上限（¥，0 不限）<input type="number" step="1" v-model="cfg.cost.daily_limit" style="width: 110px" /></label>
        <label class="field grow">模型单价表（JSON：模型 → {input, output} 元/百万 tokens）
          <textarea v-model="pricesJson" rows="4"></textarea>
        </label>
      </div>
    </div>

    <div class="card">
      <h3 class="title">路径</h3>
      <div class="row">
        <label class="field grow">归档输出根目录 <input v-model="cfg.paths.archive_root" /></label>
        <label class="field grow">数据目录（只读）<input :value="cfg.paths.data_dir" readonly /></label>
      </div>
      <div class="muted" style="margin-top: 6px">
        隐私说明：使用平台评分时，压缩图（已剥离 EXIF/GPS）会上传至聚合平台；如需离线体验请切换 mock 模式。
      </div>
    </div>

    <div class="row">
      <button class="btn" :class="{ saved: saved }" :disabled="saving" @click="save()">
        {{ saving ? '保存中…' : saved ? '✅ 已保存' : '保存配置' }}
      </button>
      <span class="muted">保存后立即生效；模型切换在下一批次生效</span>
    </div>

    <div class="card">
      <h3 class="title">数据库管理</h3>
      <div class="row">
        <label class="field">删除天数
          <select v-model="purgeDays" style="width: 130px">
            <option :value="7">7 天前</option>
            <option :value="30">30 天前</option>
            <option :value="90">90 天前</option>
            <option :value="365">1 年前</option>
          </select>
        </label>
        <button class="btn plain" :disabled="purging" @click="purgeDB">{{ purging ? '清理中…' : '🧹 清理记录' }}</button>
        <span class="muted">删除该天数之前的批次、评分明细、评分缓存与 API 用量记录；配置与压缩缓存不受影响</span>
      </div>
      <div v-if="purgeMsg" class="muted" style="margin-top: 6px">{{ purgeMsg }}</div>
    </div>

    <div class="card danger-zone">
      <h3 class="title">危险区</h3>
      <div class="row">
        <button class="btn danger" :disabled="clearing" @click="clearAll">{{ clearing ? '清空中…' : '🗑️ 清空全部数据' }}</button>
        <span class="muted">删除全部会话/评分明细/评分缓存/费用记录与压缩缓存，<b>不影响源图与归档照片</b>；配置保留。操作不可恢复。</span>
      </div>
      <div v-if="clearMsg" class="muted" style="margin-top: 6px">{{ clearMsg }}</div>
    </div>
  </div>
</template>

<script setup>
import { ref, computed, onMounted } from 'vue'
import { state, api, toast } from '../store.js'

const cfg = ref(null)
const visionPatterns = ref('')
const pricesJson = ref('')
const testing = ref(false)
const saving = ref(false)
const connState = ref(null)
const clearing = ref(false)
const clearMsg = ref('')
const presetSaveName = ref('')

// 火山 coding plan 两个协议入口地址
const VOLCANO = {
  anthropic: 'https://ark.cn-beijing.volces.com/api/coding',
  chat: 'https://ark.cn-beijing.volces.com/api/coding/v3',
}

// 切换协议时：若当前 base_url 是火山地址，自动换到对应协议入口
function onProtocolChange() {
  const c = cfg.value
  if (!c || !c.provider.base_url) return
  if (c.provider.base_url.startsWith('https://ark.cn-beijing.volces.com')) {
    c.provider.base_url = VOLCANO[c.provider.protocol || 'chat']
    toast('已自动切换到火山 ' + (c.provider.protocol === 'anthropic' ? 'Anthropic' : 'OpenAI v3') + ' 地址')
  }
}

function fillVolcano(proto) {
  if (!cfg.value) return
  cfg.value.provider.protocol = proto
  cfg.value.provider.base_url = VOLCANO[proto]
  cfg.value.provider.type = 'tokenrhythm'
  toast('已填入火山 coding plan 地址，请确认 Key 后保存')
}

const presets = computed(() => (state.config && state.config.presets) || [])
const activePresetName = computed(() => {
  const c = state.config
  if (!c) return ''
  const hit = presets.value.find((p) => p.base_url === c.provider.base_url)
  return hit ? hit.name : ''
})

async function applyPreset(name) {
  if (!name) return
  try {
    const cfgNew = await api('/api/preset/apply', { method: 'POST', body: JSON.stringify({ name }) })
    state.config = cfgNew
    cfg.value = cfgNew
    connState.value = null
    toast(`已切换到预设「${name}」，URL 和 Key 已生效`)
  } catch (e) {
    toast(e.message, true)
  }
}

async function deletePreset() {
  if (!activePresetName.value) return
  if (!confirm(`删除预设「${activePresetName.value}」？（只删预设本身，不影响当前接入配置）`)) return
  try {
    const cfgNew = await api('/api/preset/delete', { method: 'POST', body: JSON.stringify({ name: activePresetName.value }) })
    state.config = cfgNew
    toast('预设已删除')
  } catch (e) {
    toast(e.message, true)
  }
}

async function saveAsPreset() {
  const c = cfg.value
  const name = (presetSaveName.value.trim() || activePresetName.value)
  if (!name) { toast('请先填写预设名', true); return }
  try {
    await api('/api/preset/upsert', {
      method: 'POST',
      body: JSON.stringify({ name, base_url: c.provider.base_url, api_key: c.provider.api_key || '', protocol: c.provider.protocol || 'chat' }),
    })
    const st = await api('/api/state')
    state.config = st.config
    presetSaveName.value = name
    toast(`预设「${name}」已保存（选中同名预设时等价于保存当前配置）`)
  } catch (e) {
    toast(e.message, true)
  }
}
const purging = ref(false)
const purgeDays = ref(30)
const purgeMsg = ref('')

async function purgeDB() {
  if (!confirm(`确定清理 ${purgeDays.value} 天前的记录？
将删除：该时间之前的批次、评分明细、评分缓存、API 用量记录。
配置与压缩缓存不受影响，此操作不可恢复！`)) return
  purging.value = true
  try {
    const r = await api('/api/db/purge', { method: 'POST', body: JSON.stringify({ days: purgeDays.value }) })
    purgeMsg.value = `✅ 已清理：批次 ${r.sessions}、评分明细 ${r.photos}、评分缓存 ${r.score_cache}、API 记录 ${r.spend_log}`
  } catch (e) {
    purgeMsg.value = '清理失败：' + e.message
  }
  purging.value = false
}

async function clearAll() {
  if (!confirm('确定清空全部数据？将删除：所有会话、评分明细、评分缓存、费用记录、压缩缓存。不影响源图与已归档照片。此操作不可恢复！')) return
  if (!confirm('再次确认：真的要清空全部数据吗？')) return
  clearing.value = true
  try {
    const r = await api('/api/clear-all', { method: 'POST', body: '{}' })
    clearMsg.value = `✅ 已清空（释放 ${r.freed_mb.toFixed(1)} MB 缓存）。历史会话与明细已删除。`
  } catch (e) {
    clearMsg.value = '清空失败：' + e.message
  }
  clearing.value = false
}

async function testConn() {
  testing.value = true
  try {
    // 先保存当前填写值再测试，保证测的是最新配置
    await save(true)
    connState.value = await api('/api/test-connection', { method: 'POST', body: '{}' })
  } catch (e) {
    connState.value = { ok: false, message: e.message }
  }
  testing.value = false
}

const saved = ref(false)
let savedTimer = null

async function save(silent) {
  silent = silent === true // @click 直接绑定时会误传 MouseEvent
  const c = cfg.value
  const body = {
    provider: { ...c.provider, api_key: c.provider.api_key || '', protocol: c.provider.protocol || 'chat' },
    model: { ...c.model, vision_patterns: visionPatterns.value.split('\n').map((s) => s.trim()).filter(Boolean) },
    score: { ...c.score, temperature: +c.score.temperature },
    pipeline: { ...c.pipeline },
    cost: { ...c.cost, prices: safeParse(pricesJson.value) },
    paths: { ...c.paths },
  }
  saving.value = true
  try {
    cfg.value = await api('/api/config', { method: 'POST', body: JSON.stringify(body) })
    state.config = cfg.value
    visionPatterns.value = cfg.value.model.vision_patterns.join('\n')
    pricesJson.value = JSON.stringify(cfg.value.cost.prices, null, 1)
    saved.value = true
    clearTimeout(savedTimer)
    savedTimer = setTimeout(() => { saved.value = false }, 2500)
    if (!silent) toast('✅ 配置已保存，立即生效')
  } catch (e) {
    toast('保存失败：' + e.message, true)
  }
  saving.value = false
}

function safeParse(s) {
  try {
    const v = JSON.parse(s)
    if (v && typeof v === 'object') return v
  } catch { /* 保留原值 */ }
  return state.config.cost.prices
}

onMounted(async () => {
  try {
    const r = await api('/api/state')
    cfg.value = r.config
    state.config = r.config
    visionPatterns.value = r.config.model.vision_patterns.join('\n')
    pricesJson.value = JSON.stringify(r.config.cost.prices, null, 1)
  } catch (e) {
    console.error('[Settings] 初始化失败:', e)
    toast('设置加载失败：' + e.message, true)
  }
})
</script>

<style scoped>
.grid { display: flex; flex-direction: column; gap: 14px; width: 100%; }
.grow { flex: 1; min-width: 240px; }
.btn.saved { background: #2f9e63; }
.danger-zone { border: 1px solid var(--danger); }
.danger-zone .title { color: var(--danger); }
</style>
