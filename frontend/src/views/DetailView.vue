<template>
  <div class="grid">
    <div class="card">
      <div class="row" style="justify-content: space-between">
        <div class="row">
          <label class="field">
            会话
            <select v-model="sessionID" @change="load(1)">
              <option v-for="s in sessions" :key="s.id" :value="s.id">
                {{ s.id }}（{{ shortDir(s.source_dir) }}，{{ s.done || 0 }} 张）
              </option>
            </select>
          </label>
          <label class="field">
            状态
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
          <label class="field">
            模型
            <select v-model="modelFilter" @change="applySort()">
              <option value="">全部</option>
              <option v-for="m in modelOptions" :key="m" :value="m">{{ m }}</option>
            </select>
          </label>
          <label class="field">
            来源
            <select v-model="sourceFilter" @change="applySort()">
              <option value="">全部</option>
              <option value="api">API</option>
              <option value="cache">缓存</option>
            </select>
          </label>
          <label class="field">
            分数段
            <select v-model="bandFilter" @change="applySort()">
              <option value="">全部</option>
              <option value="high">高分（精选档）</option>
              <option value="mid">中分（良好档）</option>
              <option value="low">低分（一般及以下）</option>
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
      <table class="list" v-if="sortedViewItems.length">
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
          <tr v-for="p in sortedViewItems" :key="p.id">
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
              :disabled="running" title="查看详情并复检" @click="preview = p">↻ 复检</button></td>
          </tr>
        </tbody>
      </table>
      <div v-else class="muted">暂无明细（当前筛选条件下无记录）</div>
    </div>

    <!-- 统一照片详情弹窗 -->
    <PhotoModal v-if="preview" :photo="preview" :busy="running"
      @close="closePreview" />
  </div>
</template>

<script setup>
import { ref, computed, onMounted } from 'vue'
import { api, toast, state } from '../store.js'
import PhotoModal from '../components/PhotoModal.vue'

const items = ref([])
const total = ref(0)
const page = ref(1)
const pageSize = 50
const status = ref('')
const sortKey = ref('')
const sortAsc = ref(false)
const preview = ref(null)
const running = ref(false)
const loadSortTick = ref(0)
const modelFilter = ref('')
const sourceFilter = ref('')
const bandFilter = ref('')
const sessionID = ref('')
const sessions = ref([])

// 分数段判定（跟随配置阈值 t=[high,mid,low]）
function bandOf(p) {
  if (p.status !== 'scored') return null
  const t = (state.config && state.config.score && state.config.score.thresholds) || [9, 7, 5]
  if (p.score >= t[0]) return 'high'
  if (p.score >= t[1]) return 'mid'
  return 'low'
}
const totalPages = computed(() => Math.max(1, Math.ceil(total.value / pageSize)))

const modelOptions = computed(() => {
  const set = new Set()
  for (const it of items.value) if (it.model) set.add(it.model)
  return [...set].sort()
})

// 过滤后的行（模型/来源为前端过滤；表格渲染与排序均基于它）
const viewItems = computed(() => {
  return items.value.filter((it) => {
    if (modelFilter.value && (it.model || '') !== modelFilter.value) return false
    if (sourceFilter.value && (it.source || '') !== sourceFilter.value) return false
    if (bandFilter.value && bandOf(it) !== bandFilter.value) return false
    return true
  })
})

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

// 排序键（保持响应式：viewItems 渲染时按 sortKey/sortAsc 动态排序）
const sortedViewItems = computed(() => {
  if (!sortKey.value) return viewItems.value
  const k = sortKey.value
  const dir = sortAsc.value ? 1 : -1
  return [...viewItems.value].sort((a, b) => {
    const va = k === 'score' ? a.score : (a.dims ? a.dims[k] : -1)
    const vb = k === 'score' ? b.score : (b.dims ? b.dims[k] : -1)
    return (va - vb) * dir
  })
})

function applySort() { /* sortedViewItems 为 computed，筛选/排序自动响应 */ }

function closePreview() {
  preview.value = null
  load() // 复检可能已改变状态，关闭时刷新
}



function statusLabel(s) {
  return {
    scored: '已评分', parse_fail: '解析失败', failed: '调用失败', bad_image: '解码失败',
    unsupported: '不支持', duplicate: '重复', pending: '待处理', compressed: '已压缩',
  }[s] || s
}

function shortDir(d) {
  if (!d) return ''
  const parts = d.split(/[\/]+/).filter(Boolean)
  return parts.length > 2 ? '…/' + parts.slice(-2).join('/') : d
}

function d(p, k) {
  return p.dims ? p.dims[k].toFixed(1) : '-'
}

async function load(pg) {
  page.value = pg || page.value
  const r = await api(`/api/photos?page=${page.value}&page_size=${pageSize}&status=${status.value}&session=${sessionID.value}`)
  items.value = r.items || []
  total.value = r.total || 0
}

async function loadSessions() {
  try {
    sessions.value = await api('/api/sessions')
    if (!sessionID.value && sessions.value.length) sessionID.value = sessions.value[0].id
  } catch { /* 无会话 */ }
}

onMounted(async () => {
  await loadSessions()
  load(1)
})
</script>

<style scoped>
.grid { display: flex; flex-direction: column; gap: 14px; width: 100%; }
.mini { width: 44px; height: 33px; object-fit: cover; border-radius: 4px; }
.clip { max-width: 180px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.tag.warn { background: #fff3e0; color: #ffa300; }
th.sortable { cursor: pointer; user-select: none; white-space: nowrap; }
th.sortable:hover { color: var(--accent); }
.mini.clickable { cursor: zoom-in; transition: transform 0.12s; }
.mini.clickable:hover { transform: scale(1.08); }

html.dark .tag.warn { background: #3a2c12; }
</style>
