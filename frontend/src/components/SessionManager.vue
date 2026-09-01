<template>
  <div class="sm-mask" @click.self="$emit('close')">
    <div class="sm-card">
      <div class="sm-head">
        <h3 class="title">批次管理</h3>
        <button class="btn plain small" @click="$emit('close')">✕ 关闭</button>
      </div>
      <div class="sm-list">
        <div v-for="s in sessions" :key="s.id" class="sm-row" :class="{ current: s.id === current }">
          <div class="sm-info">
            <input class="sm-name" v-model="s.name" :placeholder="s.id" maxlength="40"
              @keyup.enter="rename(s)" />
            <div class="sm-meta muted">
              <span class="sm-id">{{ s.id }}</span> · {{ shortDir(s.source_dir) }} · {{ s.done || 0 }} 张 ·
              {{ statusText(s.status) }}
              <span v-if="s.id === current" class="tag">当前查看</span>
            </div>
          </div>
          <div class="sm-actions">
            <button class="btn plain small" :disabled="busyId === s.id" title="保存备注"
              @click="rename(s)">💾</button>
            <button class="btn plain small sm-del" :disabled="busyId === s.id" title="删除批次（明细与缓存，不动源图/归档）"
              @click="remove(s)">🗑️</button>
          </div>
        </div>
        <div v-if="!sessions.length" class="muted">暂无批次</div>
      </div>
      <div class="sm-tip muted">备注后下拉列表将优先显示备注名；删除仅移除评分记录与压缩缓存，源图与已归档照片不受影响。</div>
    </div>
  </div>
</template>

<script setup>
import { ref } from 'vue'
import { api, toast } from '../store.js'

const props = defineProps({
  sessions: { type: Array, required: true },
  current: { type: String, default: '' },
})
const emit = defineEmits(['close', 'changed'])

const busyId = ref('')
let list = props.sessions // 直接修改行内 name（父组件传入响应式数组）

function shortDir(d) {
  if (!d) return ''
  const parts = d.split(/[\\/]+/).filter(Boolean)
  return parts.length > 2 ? '…/' + parts.slice(-2).join('/') : d
}
function statusText(s) {
  return { completed: '已完成', running: '运行中', stopped: '已停止' }[s] || s
}

async function rename(s) {
  busyId.value = s.id
  try {
    await api('/api/session/rename', { method: 'POST', body: JSON.stringify({ id: s.id, name: (s.name || '').trim() }) })
    toast('✅ 备注已保存')
    emit('changed')
  } catch (e) {
    toast(e.message, true)
  }
  busyId.value = ''
}

async function remove(s) {
  const label = s.name || s.id
  if (!confirm(`删除批次「${label}」？\n将删除该批次的评分明细与压缩缓存（源图与已归档照片不受影响）。\n此操作不可恢复！`)) return
  busyId.value = s.id
  try {
    const r = await api('/api/session/delete', { method: 'POST', body: JSON.stringify({ id: s.id }) })
    toast(`已删除批次（释放 ${r.freed_mb.toFixed(1)} MB 缓存）`)
    emit('changed')
  } catch (e) {
    toast(e.message, true)
  }
  busyId.value = ''
}
</script>

<style scoped>
.sm-mask {
  position: fixed; inset: 0; background: rgba(0, 0, 0, 0.72); z-index: 100;
  display: flex; align-items: center; justify-content: center; padding: 24px;
}
.sm-card {
  background: var(--card); border-radius: 14px; width: min(760px, 94vw);
  max-height: 86vh; display: flex; flex-direction: column; padding: 16px 18px;
  box-shadow: 0 12px 48px rgba(0, 0, 0, 0.35);
}
.sm-head { display: flex; align-items: center; justify-content: space-between; margin-bottom: 10px; }
.sm-list { overflow-y: auto; display: flex; flex-direction: column; gap: 6px; flex: 1; }
.sm-row {
  display: flex; align-items: center; gap: 12px;
  background: var(--card-2); border-radius: 10px; padding: 8px 12px;
}
.sm-row.current { outline: 2px solid var(--accent); outline-offset: -2px; }
.sm-info { flex: 1; min-width: 0; }
.sm-name {
  width: 100%; max-width: 320px; padding: 3px 8px; font-size: 13px;
  background: transparent; border-color: transparent;
}
.sm-name:hover, .sm-name:focus { background: var(--card); border-color: var(--line); }
.sm-meta { font-size: 12px; margin-top: 2px; display: flex; gap: 6px; align-items: center; flex-wrap: wrap; }
.sm-id { font-family: monospace; }
.sm-actions { display: flex; gap: 6px; flex: none; }
.sm-del:hover { color: var(--danger); border-color: var(--danger); }
.sm-tip { margin-top: 10px; font-size: 12px; }
</style>
