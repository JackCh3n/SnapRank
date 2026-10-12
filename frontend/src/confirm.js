import { reactive } from 'vue'

// 全局确认弹窗服务：替代原生 confirm()（系统灰框与应用风格不符，且无法统一美化）。
// 用法：const ok = await confirmDialog({ title, message, danger, confirmText })
// Enter=确认，Esc/点击遮罩=取消
export const confirmState = reactive({
  open: false, title: '', message: '', danger: false,
  confirmText: '确认', cancelText: '取消', _resolve: null,
})

export function confirmDialog(opts = {}) {
  return new Promise((resolve) => {
    Object.assign(confirmState, {
      open: true,
      title: opts.title || '确认操作',
      message: opts.message || '',
      danger: !!opts.danger,
      confirmText: opts.confirmText || '确认',
      cancelText: opts.cancelText || '取消',
      _resolve: resolve,
    })
  })
}

export function answerConfirm(ok) {
  if (!confirmState.open) return
  confirmState.open = false
  const r = confirmState._resolve
  confirmState._resolve = null
  if (r) r(ok)
}
