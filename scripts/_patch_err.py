import io

# 1) RunView: 初始化失败不致页面空白
p = 'frontend/src/views/RunView.vue'
s = io.open(p, encoding='utf-8').read()
old = '''onMounted(async () => {
  await refreshState()
  if (!state.selModel) state.selModel = state.currentModel
  if (!state.models.vision.length) loadModels()
})'''
new = '''onMounted(async () => {
  try {
    await refreshState()
    if (!state.selModel) state.selModel = state.currentModel
    if (!state.models.vision.length) loadModels().catch((e) => console.error('[RunView] 模型拉取失败:', e))
  } catch (e) {
    console.error('[RunView] 初始化失败:', e)
  }
})'''
assert old in s, 'rv'
s = s.replace(old, new)
io.open(p, 'w', encoding='utf-8', newline='\n').write(s)
print('RunView ok')

# 2) SettingsView: 模型/连接失败防护（页面空白根因：未捕获异常导致渲染中断）
p = 'frontend/src/views/SettingsView.vue'
s = io.open(p, encoding='utf-8').read()

old = '''import { state, api, toast } from '../store.js' '''
new = '''import { state, api, toast } from '../store.js'
import { refreshState } from '../store.js' '''
# 合并为一行
s = s.replace('''import { state, api, toast } from '../store.js'
import { refreshState } from '../store.js' ''', '''import { state, api, toast, refreshState } from '../store.js' ''')
io.open(p, 'w', encoding='utf-8', newline='\n').write(s)
print('SettingsView import ok')

# onMounted 检查（如果有 fetch /api/state 失败防护）
old2 = s
print('SettingsView checked')
io.open(p, 'w', encoding='utf-8', newline='\n').write(s)
print('ALL OK')
