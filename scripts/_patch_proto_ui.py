import io

p = 'frontend/src/views/SettingsView.vue'
s = io.open(p, encoding='utf-8').read()

old = '''        <label class="field">
          API Key（点击输入框右侧图标可显示/隐藏）'''
new = '''        <label class="field">
          协议
          <select v-model="cfg.provider.protocol" style="width: 170px"
            title="火山 coding plan 等网关两种都支持；Anthropic 官方选 anthropic">
            <option value="chat">Chat Completions</option>
            <option value="anthropic">Anthropic Messages</option>
          </select>
        </label>
        <label class="field">
          API Key（点击输入框右侧图标可显示/隐藏）'''
assert old in s, 's1'
s = s.replace(old, new)

old = '''    provider: { ...c.provider, api_key: c.provider.api_key || '' },'''
new = '''    provider: { ...c.provider, api_key: c.provider.api_key || '', protocol: c.provider.protocol || 'chat' },'''
assert old in s, 's2'
s = s.replace(old, new)

old = '''      body: JSON.stringify({ name, base_url: c.provider.base_url, api_key: c.provider.api_key || '' }),'''
new = '''      body: JSON.stringify({ name, base_url: c.provider.base_url, api_key: c.provider.api_key || '', protocol: c.provider.protocol || 'chat' }),'''
assert old in s, 's3'
s = s.replace(old, new)
io.open(p, 'w', encoding='utf-8', newline='\n').write(s)
print('settings ok')
print('ALL OK')
