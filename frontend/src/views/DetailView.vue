<template>
  <div class="grid">
    <div class="card">
      <div class="row" style="justify-content: space-between">
        <div class="row">
          <label class="field">
            状态筛选
            <select v-model="status" @change="load(1)">
              <option value="">全部</option>
              <option value="scored">已评分</option>
              <option value="parse_fail">解析失败</option>
              <option value="failed">调用失败</option>
              <option value="bad_image">解码失败</option>
              <option value="unsupported">格式不支持</option>
              <option value="duplicate">重复</option>
              <option value="pending">待处理</option>
            </select>
          </label>
          <a class="btn plain" href="/api/report" download>导出 report.csv</a>
        </div>
        <div class="row">
          <button class="btn plain small" :disabled="page <= 1" @click="load(page - 1)">上一页</button>
          <span class="muted">第 {{ page }} / {{ totalPages }} 页 · 共 {{ total }} 条</span>
          <button class="btn plain small" :disabled="page >= totalPages" @click="load(page + 1)">下一页</button>
        </div>
      </div>
      <table class="list" v-if="items.length">
        <thead>
          <tr>
            <th>缩略</th><th>文件名</th><th>状态</th>
            <th class="sortable" @click="toggleSort('score')">总分 {{ arrow('score') }}</th>
            <th class="sortable" @click="toggleSort('technique')">技 {{ arrow('technique') }}</th>
            <th class="sortable" @click="toggleSort('composition')">构 {{ arrow('composition') }}</th>
            <th class="sortable" @click="toggleSort('content')">容 {{ arrow('content') }}</th>
            <th class="sortable" @click="toggleSort('color')">色 {{ arrow('color') }}</th>
            <th>标签</th><th>模型</th><th>来源</th><th>耗时</th><th>时间</th><th>说明</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="p in items" :key="p.id">
            <td><img v-if="p.compressed_path" :src="`/api/thumb?id=${p.id}`" class="mini clickable"
              loading="lazy" @click="preview = p" title="点击查看大图" /></td>
            <td class="clip" :title="p.src_path">{{ p.filename }}</td>
            <td><span class="tag" :class="{ warn: p.status !== 'scored' }">{{ statusLabel(p.status) }}</span></td>
            <td><b>{{ p.status === 'scored' ? p.score.toFixed(1) : '-' }}</b></td>
            <td>{{ d(p, 'technique') }}</td><td>{{ d(p, 'composition') }}</td>
            <td>{{ d(p, 'content') }}</td><td>{{ d(p, 'color') }}</td>
            <td class="clip">{{ (p.tags || []).join(' ') }}</td>
            <td class="clip">{{ p.model }}</td>
            <td>{{ p.source === 'cache' ? '缓存' : p.source === 'api' ? 'API' : '-' }}</td>
            <td>{{ p.duration_ms ? p.duration_ms + 'ms' : '-' }}</td>
            <td class="clip">{{ p.updated_at }}</td>
            <td class="clip" :title="p.error">{{ p.error || p.weakness }}</td>
            <td><button v-if="p.status === 'parse_fail' || p.status === 'failed'" class="btn plain small"
              :disabled="running" @click="rescoreOne(p)">↻ 复检</button></td>
          </tr>
        </tbody>
      </table>
      <div v-else class="muted">暂无明细</div>
    </div>

    <div v-if="preview" class="lightbox" @click.self="preview = null">
      <div class="lightbox-card">
        <img :src="`/api/thumb?id=${preview.id}`" />
        <div class="lb-info">
          <b>{{ preview.filename }}</b>
          <span v-if="preview.dims">
            总分 {{ preview.score.toFixed(1) }} · 技 {{ preview.dims.technique.toFixed(1) }} ·
            构 {{ preview.dims.composition.toFixed(1) }} · 容 {{ preview.dims.content.toFixed(1) }} ·
            色 {{ preview.dims.color.toFixed(1) }}
          </span>
          <span class="muted">{{ preview.strength }}</span>
          <span class="muted">{{ preview.weakness }}</span>
        </div>
        <button class="btn plain small lb-close" @click="preview = null">✕ 关闭</button>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, computed, onMounted } from 'vue'
import { api, toast } from '../store.js'

const items = ref([])
const total = ref(0)
const page = ref(1)
const pageSize = 50
const status = ref('')
const sortKey = ref('')
const sortAsc = ref(false)
const preview = ref(null)
const running = ref(false)
const totalPages = computed(() => Math.max(1, Math.ceil(total.value / pageSize)))

function toggleSort(k) {
  if (sortKey.value === k) {
    if (sortAsc.value) { sortKey.value = ''; sortAsc.value = false } // 第三次取消排序
    else sortAsc.value = true
  } else {
    sortKey.value = k
    sortAsc.value = false // 默认降序（高分在前）
  }
  applySort()
}

function arrow(k) {
  if (sortKey.value !== k) return '↕'
  return sortAsc.value ? '↑' : '↓'
}

function applySort() {
  if (!sortKey.value) return
  const k = sortKey.value
  const dir = sortAsc.value ? 1 : -1
  items.value = [...items.value].sort((a, b) => {
    const va = k === 'score' ? a.score : (a.dims ? a.dims[k] : -1)
    const vb = k === 'score' ? b.score : (b.dims ? b.dims[k] : -1)
    return (va - vb) * dir
  })
}

async function rescoreOne(p) {
  running.value = true
  try {
    await api('/api/rescore', { method: 'POST', body: JSON.stringify({ ids: [p.id], force: true }) })
    toast(`已提交复检：${p.filename}`)
  } catch (e) {
    toast(e.message, true)
  }
  running.value = false
}



function statusLabel(s) {
  return {
    scored: '已评分', parse_fail: '解析失败', failed: '调用失败', bad_image: '解码失败',
    unsupported: '不支持', duplicate: '重复', pending: '待处理', compressed: '已压缩',
  }[s] || s
}

function d(p, k) {
  return p.dims ? p.dims[k].toFixed(1) : '-'
}

async function load(pg) {
  page.value = pg || page.value
  const r = await api(`/api/photos?page=${page.value}&page_size=${pageSize}&status=${status.value}`)
  items.value = r.items || []
  total.value = r.total || 0
  applySort()
}

onMounted(() => load(1))
</script>

<style scoped>
.grid { display: flex; flex-direction: column; gap: 14px; max-width: 1200px; }
.mini { width: 44px; height: 33px; object-fit: cover; border-radius: 4px; }
.clip { max-width: 180px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.tag.warn { background: #fff3e0; color: #ffa300; }
th.sortable { cursor: pointer; user-select: none; white-space: nowrap; }
th.sortable:hover { color: var(--accent); }
.mini.clickable { cursor: zoom-in; transition: transform 0.12s; }
.mini.clickable:hover { transform: scale(1.08); }
.lightbox {
  position: fixed; inset: 0; background: rgba(0,0,0,0.72); z-index: 100;
  display: flex; align-items: center; justify-content: center; padding: 24px;
}
.lightbox-card {
  background: var(--card); border-radius: 12px; max-width: min(860px, 92vw);
  max-height: 90vh; overflow: hidden; display: flex; flex-direction: column; position: relative;
}
.lightbox-card img { max-width: 100%; max-height: 68vh; object-fit: contain; display: block; }
.lb-info { padding: 12px 16px; display: flex; flex-direction: column; gap: 4px; font-size: 13px; }
.lb-close { position: absolute; right: 10px; top: 10px; }
html.dark .tag.warn { background: #3a2c12; }
</style>
