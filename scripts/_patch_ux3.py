import io

# ============ 前端 Settings: Key 明文展示 + 预设联动 + 火山 URL 快捷 ============
p = 'frontend/src/views/SettingsView.vue'
s = io.open(p, encoding='utf-8').read()

# 1) Key 输入框直接明文（不再 password）
old = '''          API Key（点击输入框右侧图标可显示/隐藏）
          <div style="display: flex; gap: 6px">
            <input v-model="cfg.provider.api_key" :type="showKey ? 'text' : 'password'"
              :placeholder="cfg.provider.api_key ? `已设置（${cfg.provider.api_key}）` : 'sk-...'" style="flex: 1" />
            <button class="btn plain small" :title="showKey ? '隐藏 Key' : '显示 Key'"
              @click="showKey = !showKey">{{ showKey ? '🙈' : '👁' }}</button>
          </div>'''
new = '''          API Key（本地明文展示，可直接编辑）
          <input v-model="cfg.provider.api_key" placeholder="sk-..." />'''
assert old in s, 'k1'
s = s.replace(old, new)

# 2) 协议切换时：若 base_url 是火山域名自动纠正路径
old = '''        <label class="field grow">
          协议
          <select v-model="cfg.provider.protocol" style="width: 170px"
            title="火山 coding plan 等网关两种都支持；Anthropic 官方选 anthropic"
            @change="onProtocolChange">
            <option value="chat">Chat Completions</option>
            <option value="anthropic">Anthropic Messages</option>
          </select>
        </label>

'''
new = '''        <label class="field">
          协议
          <select v-model="cfg.provider.protocol" style="width: 170px"
            title="火山 coding plan 等网关两种都支持；Anthropic 官方选 anthropic"
            @change="onProtocolChange">
            <option value="chat">Chat Completions</option>
            <option value="anthropic">Anthropic Messages</option>
          </select>
        </label>'''
assert old in s, 'k2'
s = s.replace(old, new)

# 3) 火山快捷按钮
old = '''        <label class="field grow">
          Base URL
          <input v-model="cfg.provider.base_url" placeholder="https://api.tokenrhythm.studio/v1" />
        </label>'''
new = '''        <label class="field grow">
          Base URL
          <input v-model="cfg.provider.base_url" placeholder="https://api.tokenrhythm.studio/v1" />
        </label>
        <button class="btn plain small" title="填入火山 coding plan Anthropic 地址（/api/coding）"
          @click="fillVolcano('anthropic')">火山 Anthropic</button>
        <button class="btn plain small" title="填入火山 coding plan OpenAI 地址（/api/coding/v3）"
          @click="fillVolcano('chat')">火山 OpenAI v3</button>'''
assert old in s, 'k3'
s = s.replace(old, new)

# 4) script: showKey 移除、onProtocolChange、fillVolcano、存为预设带上当前协议
old = '''const presetSaveName = ref('')
const showKey = ref(false)'''
new = '''const presetSaveName = ref('')'''
assert old in s, 'k4'
s = s.replace(old, new)

old = '''async function applyPreset(name) {'''
new = '''// 火山 coding plan 两个协议入口地址
const VOLCANO = {
  anthropic: 'https://ark.cn-beijing.volces.com/api/coding',
  chat: 'https://ark.cn-beijing.volces.com/api/coding/v3',
}

function fillVolcano(proto) {
  if (!cfg.value) return
  cfg.value.provider.protocol = proto
  cfg.value.provider.base_url = VOLCANO[proto]
  cfg.value.provider.type = 'tokenrhythm'
  toast('已填入火山 coding plan 地址，请确认 Key 后保存')
}

// 切换协议时：若当前 base_url 是火山地址，自动换到对应协议入口
function onProtocolChange() {
  const c = cfg.value
  if (!c || !c.provider.base_url) return
  if (c.provider.base_url.startsWith('https://ark.cn-beijing.volces.com')) {
    c.provider.base_url = VOLCANO[c.provider.protocol || 'chat']
  }
}

async function applyPreset(name) {'''
assert old in s, 'k5'
s = s.replace(old, new)

# 5) 保存后回填真实 Key（后端不再脱敏），同步 state
old = '''    cfg.value = await api('/api/config', { method: 'POST', body: JSON.stringify(body) })
    visionPatterns.value = cfg.value.model.vision_patterns.join('\\n')
    pricesJson.value = JSON.stringify(cfg.value.cost.prices, null, 1)'''
new = '''    cfg.value = await api('/api/config', { method: 'POST', body: JSON.stringify(body) })
    state.config = cfg.value
    visionPatterns.value = cfg.value.model.vision_patterns.join('\\n')
    pricesJson.value = JSON.stringify(cfg.value.cost.prices, null, 1)'''
assert old in s, 'k6'
s = s.replace(old, new)

# 6) applyPreset 不再清空 Key（cfg 持有真实 Key）
old = '''    state.config = cfgNew
    cfg.value = cfgNew
    // 把预设的 Key 回填到输入框（脱敏显示由 placeholder 提示），避免"看不到 Key"的困惑
    cfg.value.provider.api_key = ''
    connState.value = null'''
new = '''    state.config = cfgNew
    cfg.value = cfgNew
    connState.value = null'''
assert old in s, 'k7'
s = s.replace(old, new)

# 7) 存为预设：若当前已选中同名预设，等价于保存当前配置
old = '''async function saveAsPreset() {
  const c = cfg.value
  const name = presetSaveName.value.trim()
  if (!name) { toast('请先填写预设名', true); return }'''
new = '''async function saveAsPreset() {
  const c = cfg.value
  const name = (presetSaveName.value.trim() || activePresetName.value)
  if (!name) { toast('请先填写预设名', true); return }'''
assert old in s, 'k8'
s = s.replace(old, new)

# 8) 存为预设后刷新列表并选中
old = '''    const st = await api('/api/state')
    state.config = st.config
    toast(`预设「${name}」已保存`)'''
new = '''    const st = await api('/api/state')
    state.config = st.config
    presetSaveName.value = name
    toast(`预设「${name}」已保存`)'''
assert old in s, 'k9'
s = s.replace(old, new)
io.open(p, 'w', encoding='utf-8', newline='\n').write(s)
print('settings ok')

# ============ RunView: 目录历史一键清理 ============
p = 'frontend/src/views/RunView.vue'
s = io.open(p, encoding='utf-8').read()
old = '''      <div v-if="dirHistory.length" class="dir-history">
        <span class="muted">最近：</span>'''
new = '''      <div v-if="dirHistory.length" class="dir-history">
        <span class="muted">最近：</span>
        <span class="chip-x" style="border:none;background:none" title="清空全部目录历史"
          @click.stop="clearDirHistory">🗑️</span>'''
assert old in s, 'rh1'
s = s.replace(old, new)

old = '''async function removeDir(d) {'''
new = '''async function clearDirHistory() {
  if (!confirm('清空全部目录历史？')) return
  try {
    await api('/api/dir-history/remove', { method: 'POST', body: JSON.stringify({ dir: '__ALL__' }) })
    const r = await api('/api/state')
    state.config = r.config
    toast('目录历史已清空')
  } catch (e) {
    toast(e.message, true)
  }
}

async function removeDir(d) {'''
assert old in s, 'rh2'
s = s.replace(old, new)
io.open(p, 'w', encoding='utf-8', newline='\n').write(s)
print('RunView ok')

# ============ core: dir=__ALL__ 清空全部 ============
p = 'internal/core/core.go'
s = io.open(p, encoding='utf-8').read()
old = '''// RemoveDirHistory 删除一条目录历史标签
func (c *Core) RemoveDirHistory(dir string) error {
	c.cfgMu.Lock()
	var out []string
	for _, d := range c.cfg.DirHistory {
		if d != dir {
			out = append(out, d)
		}
	}
	c.cfg.DirHistory = out
	err := c.cfg.Save()
	c.cfgMu.Unlock()
	return err
}'''
new = '''// RemoveDirHistory 删除目录历史：dir=__ALL__ 时清空全部
func (c *Core) RemoveDirHistory(dir string) error {
	c.cfgMu.Lock()
	defer c.cfgMu.Unlock()
	if dir == "__ALL__" {
		c.cfg.DirHistory = nil
	} else {
		var out []string
		for _, d := range c.cfg.DirHistory {
			if d != dir {
				out = append(out, d)
			}
		}
		c.cfg.DirHistory = out
	}
	return c.cfg.Save()
}'''
assert old in s, 'core rh'
s = s.replace(old, new)
io.open(p, 'w', encoding='utf-8', newline='\n').write(s)
print('core rh ok')

# ============ server/provider: 控制台详细日志 ============
p = 'internal/provider/provider.go'
s = io.open(p, encoding='utf-8').read()
old = '''		content := resp.Choices[0].Message.Content'''
new = '''		content := resp.Choices[0].Message.Content
		logutil.Info("[评分] 模型=%s 完成 %dms", model, time.Since(start)/time.Millisecond)'''
assert old in s, 'log1'
s = s.replace(old, new)

old = '''// Score 调用视觉模型；带 json_object 模式，报错自动降级重试一次
func (t *TokenRhythm) Score(ctx context.Context, model string, req ScoreRequest) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, req.Timeout)
	defer cancel()'''
new = '''// Score 调用视觉模型；带 json_object 模式，报错自动降级重试一次
func (t *TokenRhythm) Score(ctx context.Context, model string, req ScoreRequest) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, req.Timeout)
	defer cancel()
	start := time.Now()
	logutil.Info("[评分] 开始 模型=%s", model)'''
assert old in s, 'log2'
s = s.replace(old, new)

if '"snaprank/internal/logutil"' not in s:
    s = s.replace('"snaprank/internal/config"', '"snaprank/internal/config"\n\t"snaprank/internal/logutil"')
io.open(p, 'w', encoding='utf-8', newline='\n').write(s)
print('provider log ok')
print('ALL OK')
