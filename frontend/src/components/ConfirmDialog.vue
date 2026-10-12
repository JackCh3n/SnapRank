<template>
  <div v-if="cs.open" class="cd-mask" @click.self="answer(false)">
    <div class="cd-card" :class="{ danger: cs.danger }">
      <h3 class="title" :style="cs.danger ? 'color: var(--danger)' : ''">
        {{ (cs.danger ? '⚠️ ' : '') + cs.title }}
      </h3>
      <div class="cd-msg">{{ cs.message }}</div>
      <div class="row" style="justify-content: flex-end; margin-top: 14px">
        <button class="btn plain" @click="answer(false)">{{ cs.cancelText }}</button>
        <button class="btn" :class="{ danger: cs.danger }" @click="answer(true)">{{ cs.confirmText }}</button>
      </div>
    </div>
  </div>
</template>

<script setup>
import { onMounted, onBeforeUnmount } from 'vue'
import { confirmState as cs, answerConfirm } from '../confirm.js'

function answer(ok) { answerConfirm(ok) }
function onKey(e) {
  if (!cs.open) return
  if (e.key === 'Enter') { e.preventDefault(); answerConfirm(true) }
  else if (e.key === 'Escape') { e.preventDefault(); answerConfirm(false) }
}
onMounted(() => window.addEventListener('keydown', onKey))
onBeforeUnmount(() => window.removeEventListener('keydown', onKey))
</script>

<style scoped>
.cd-mask {
  position: fixed; inset: 0; background: rgba(0, 0, 0, 0.6); z-index: 120;
  display: flex; align-items: center; justify-content: center; padding: 24px;
}
.cd-card {
  background: var(--card); border: 1px solid var(--line); border-radius: 12px;
  width: min(460px, 94vw); padding: 16px 18px;
  box-shadow: 0 12px 48px rgba(0, 0, 0, 0.35);
  display: flex; flex-direction: column;
}
.cd-card.danger { border: 2px solid var(--danger); }
.cd-msg { color: var(--text-2); font-size: 14px; line-height: 1.7; white-space: pre-line; margin-top: 6px; }
</style>
