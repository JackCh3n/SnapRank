<template>
  <div class="shell">
    <!-- 顶栏：微信风格标题 + 主题切换 -->
    <header class="topbar">
      <div class="brand">
        <span class="logo">🖼️</span>
        <div>
          <div class="name">帧选 SnapRank</div>
          <div class="ver muted">{{ state.version }}</div>
        </div>
      </div>
      <div class="spacer"></div>
      <span v-if="state.running" class="tag pulse">● 任务运行中</span>
      <button class="btn plain small" @click="toggleTheme" :title="'切换主题'">
        {{ state.theme === 'dark' ? '🌙 暗色' : '☀️ 亮色' }}
      </button>
    </header>

    <div class="body">
      <!-- 侧边导航（微信风格） -->
      <nav class="side">
        <div v-for="p in pages" :key="p.key" class="nav-item" :class="{ active: state.page === p.key }"
          @click="state.page = p.key">
          <span class="icon">{{ p.icon }}</span>
          <span>{{ p.label }}</span>
        </div>
      </nav>

      <!-- 主内容 -->
      <main class="content">
        <RunView v-if="state.page === 'run'" />
        <ReviewView v-else-if="state.page === 'review'" />
        <GalleryView v-else-if="state.page === 'gallery'" />
        <DetailView v-else-if="state.page === 'detail'" />
        <SettingsView v-else />
      </main>
    </div>

    <div v-if="state.toast" class="toast" :class="{ err: state.toast.err }">{{ state.toast.msg }}</div>
  </div>
</template>

<script setup>
import { onMounted } from 'vue'
import { state, setTheme, connectSSE, refreshState } from './store.js'
import RunView from './views/RunView.vue'
import ReviewView from './views/ReviewView.vue'
import GalleryView from './views/GalleryView.vue'
import DetailView from './views/DetailView.vue'
import SettingsView from './views/SettingsView.vue'

const pages = [
  { key: 'run', icon: '📸', label: '运行' },
  { key: 'review', icon: '🗂️', label: '复核归档' },
  { key: 'gallery', icon: '🖼️', label: '图库' },
  { key: 'detail', icon: '📋', label: '评分明细' },
  { key: 'settings', icon: '⚙️', label: '设置' },
]

function toggleTheme() {
  setTheme(state.theme === 'dark' ? 'light' : 'dark')
}

onMounted(async () => {
  setTheme(state.theme)
  connectSSE()
  await refreshState()
})
</script>

<style scoped>
.shell { display: flex; flex-direction: column; height: 100vh; }
.topbar {
  display: flex; align-items: center; gap: 12px;
  background: var(--card); padding: 10px 20px; box-shadow: var(--shadow); z-index: 2;
}
.brand { display: flex; align-items: center; gap: 10px; }
.logo { font-size: 26px; }
.name { font-size: 16px; font-weight: 600; }
.ver { line-height: 1; }
.spacer { flex: 1; }
.pulse { animation: blink 1.2s infinite; }
@keyframes blink { 50% { opacity: 0.4; } }
.body { display: flex; flex: 1; overflow: hidden; }
.side {
  width: 150px; background: var(--card); margin: 12px 0 12px 12px;
  border-radius: 12px; box-shadow: var(--shadow); padding: 10px 6px;
  display: flex; flex-direction: column; gap: 2px;
}
.nav-item {
  display: flex; align-items: center; gap: 10px;
  padding: 11px 14px; border-radius: 9px; cursor: pointer;
  color: var(--text-2); font-size: 14px; user-select: none;
}
.nav-item:hover { background: var(--card-2); color: var(--text); }
.nav-item.active { background: var(--accent-weak); color: var(--accent); font-weight: 600; }
.icon { font-size: 17px; }
.content { flex: 1; overflow-y: auto; padding: 12px 16px 24px; }
@media (max-width: 720px) { .side { width: 56px; } .nav-item span:last-child { display: none; } }
</style>
