import io

p = 'frontend/src/views/DetailView.vue'
s = io.open(p, encoding='utf-8').read()

# 1) 筛选下拉 change → load(1)（服务端过滤）
old = '''            <select v-model="modelFilter" @change="applySort()">'''
new = '''            <select v-model="modelFilter" @change="load(1)">'''
assert old in s, 'f1'
s = s.replace(old, new)
old = '''            <select v-model="sourceFilter" @change="applySort()">'''
new = '''            <select v-model="sourceFilter" @change="load(1)">'''
assert old in s, 'f2'
s = s.replace(old, new)

# 2) 分数段下拉
old = '''          <label class="field">
            来源
            <select v-model="sourceFilter" @change="load(1)">
              <option value="">全部</option>
              <option value="api">API</option>
              <option value="cache">缓存</option>
            </select>
          </label>'''
new = '''          <label class="field">
            来源
            <select v-model="sourceFilter" @change="load(1)">
              <option value="">全部</option>
              <option value="api">API</option>
              <option value="cache">缓存</option>
            </select>
          </label>
          <label class="field">
            分数段
            <select v-model="bandFilter" @change="load(1)">
              <option value="">全部</option>
              <option value="high">高分（精选档）</option>
              <option value="mid">中分（良好档）</option>
              <option value="low">低分（一般及以下）</option>
            </select>
          </label>'''
assert old in s, 'f3'
s = s.replace(old, new)

# 3) script：移除客户端排序/过滤，改服务端参数
old = '''const loadSortTick = ref(0)
const modelFilter = ref('')
const sourceFilter = ref('')
const bandFilter = ref('')

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

function applySort() { /* sortedViewItems 为 computed，筛选/排序自动响应 */ }'''
new = '''const modelFilter = ref('')
const sourceFilter = ref('')
const bandFilter = ref('')

const modelOptions = computed(() => {
  const set = new Set()
  for (const it of items.value) if (it.model) set.add(it.model)
  return [...set].sort()
})

function bandOf(p) {
  if (p.status !== 'scored') return null
  const t = (state.config && state.config.score && state.config.score.thresholds) || [9, 7, 5]
  if (p.score >= t[0]) return 'high'
  if (p.score >= t[1]) return 'mid'
  return 'low'
}'''
assert old in s, 'f4'
s = s.replace(old, new)

# 4) 表格渲染回 items（服务端已排序/过滤）
s = s.replace('<tr v-for="p in sortedViewItems" :key="p.id">', '<tr v-for="p in items" :key="p.id">')
s = s.replace('<table class="list" v-if="sortedViewItems.length">', '<table class="list" v-if="items.length">')

# 5) toggleSort → 服务端排序重查
old = '''function toggleSort(k) {
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
}'''
new = '''function toggleSort(k) {
  if (sortKey.value === k) {
    if (sortAsc.value) { sortKey.value = ''; sortAsc.value = false } // 第三次取消排序
    else sortAsc.value = true
  } else {
    sortKey.value = k
    sortAsc.value = false // 默认降序（高分在前）
  }
  load(1) // 服务端排序，重置到第一页
}

function arrow(k) {
  if (sortKey.value !== k) return '↕'
  return sortAsc.value ? '↑' : '↓'
}'''
assert old in s, 'f5'
s = s.replace(old, new)

# 6) load 带全参数
old = '''  const r = await api(`/api/photos?page=${page.value}&page_size=${pageSize}&status=${status.value}&session=${sessionID.value}`)
  items.value = r.items || []
  total.value = r.total || 0
}'''
new = '''  const qs = new URLSearchParams({
    page: page.value, page_size: pageSize, status: status.value, session: sessionID.value,
    sort: sortKey.value, order: sortAsc.value ? 'asc' : 'desc',
    model: modelFilter.value, source: sourceFilter.value, band: bandFilter.value,
  })
  const r = await api(`/api/photos?${qs}`)
  items.value = r.items || []
  total.value = r.total || 0
}'''
assert old in s, 'f6'
s = s.replace(old, new)

# 7) 空态文案
s = s.replace('<div v-else class="muted">暂无明细（当前筛选条件下无记录）</div>',
              '<div v-else class="muted">暂无明细（当前筛选条件下无记录）</div>')
io.open(p, 'w', encoding='utf-8', newline='\n').write(s)
print('DetailView server-side ok')
print('ALL OK')
