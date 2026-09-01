<template>
  <div class="grid" v-if="cfg">
    <div class="card">
      <h3 class="title">平台接入</h3>
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
          API Key（留空保持不变）
          <input v-model="cfg.provider.api_key" type="password" :placeholder="cfg.provider.api_key ? `已设置（${cfg.provider.api_key}）` : 'sk-...'" />
        </label>
      </div>
      <div class="row" style="margin-top: 12px">
        <button class="btn plain" @click="testConn">{{ testing ? '测试中…' : '测试连接' }}</button>
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
      <button class="btn" :disabled="saving" @click="save">保存配置</button>
      <span class="muted">保存后立即生效；模型切换在下一批次生效</span>
    </div>
  </div>
</template>

<script setup>
import { ref, onMounted } from 'vue'
import { state, api, toast } from '../store.js'

const cfg = ref(null)
const visionPatterns = ref('')
const pricesJson = ref('')
const testing = ref(false)
const saving = ref(false)
const connState = ref(null)

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

async function save(silent = false) {
  const c = cfg.value
  const body = {
    provider: { ...c.provider, api_key: c.provider.api_key || '' },
    model: { ...c.model, vision_patterns: visionPatterns.value.split('\n').map((s) => s.trim()).filter(Boolean) },
    score: { ...c.score, temperature: +c.score.temperature },
    pipeline: { ...c.pipeline },
    cost: { ...c.cost, prices: safeParse(pricesJson.value) },
    paths: { ...c.paths },
  }
  cfg.value = await api('/api/config', { method: 'POST', body: JSON.stringify(body) })
  cfg.value.provider.api_key = '' // 脱敏回显不回填输入框
  visionPatterns.value = cfg.value.model.vision_patterns.join('\n')
  pricesJson.value = JSON.stringify(cfg.value.cost.prices, null, 1)
  if (!silent) toast('配置已保存')
}

function safeParse(s) {
  try {
    const v = JSON.parse(s)
    if (v && typeof v === 'object') return v
  } catch { /* 保留原值 */ }
  return state.config.cost.prices
}

onMounted(async () => {
  const r = await api('/api/state')
  cfg.value = r.config
  state.config = r.config
  visionPatterns.value = r.config.model.vision_patterns.join('\n')
  pricesJson.value = JSON.stringify(r.config.cost.prices, null, 1)
})
</script>

<style scoped>
.grid { display: flex; flex-direction: column; gap: 14px; width: 100%; }
.grow { flex: 1; min-width: 240px; }
</style>
