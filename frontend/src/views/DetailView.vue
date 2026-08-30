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
            <th>缩略</th><th>文件名</th><th>状态</th><th>总分</th>
            <th>技</th><th>构</th><th>容</th><th>色</th>
            <th>标签</th><th>模型</th><th>来源</th><th>耗时</th><th>时间</th><th>说明</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="p in items" :key="p.id">
            <td><img v-if="p.compressed_path" :src="`/api/thumb?id=${p.id}`" class="mini" loading="lazy" /></td>
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
          </tr>
        </tbody>
      </table>
      <div v-else class="muted">暂无明细</div>
    </div>
  </div>
</template>

<script setup>
import { ref, computed, onMounted } from 'vue'
import { api } from '../store.js'

const items = ref([])
const total = ref(0)
const page = ref(1)
const pageSize = 50
const status = ref('')
const totalPages = computed(() => Math.max(1, Math.ceil(total.value / pageSize)))

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
}

onMounted(() => load(1))
</script>

<style scoped>
.grid { display: flex; flex-direction: column; gap: 14px; max-width: 1200px; }
.mini { width: 44px; height: 33px; object-fit: cover; border-radius: 4px; }
.clip { max-width: 180px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.tag.warn { background: #fff3e0; color: #ffa300; }
html.dark .tag.warn { background: #3a2c12; }
</style>
