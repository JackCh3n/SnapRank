import io

# 1) core: RemoveDirHistory 支持 __ALL__
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
new = '''// RemoveDirHistory 删除目录历史标签；dir=__ALL__ 清空全部
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
assert old in s, 'c1'
s = s.replace(old, new)
io.open(p, 'w', encoding='utf-8', newline='\n').write(s)
print('core ok')

# 2) RunView: 🗑️ 一键清理 + clearDirHistory
p = 'frontend/src/views/RunView.vue'
s = io.open(p, encoding='utf-8').read()
old = '''      <div v-if="dirHistory.length" class="dir-history">
        <span class="muted">最近：</span>'''
new = '''      <div v-if="dirHistory.length" class="dir-history">
        <span class="muted">最近：</span>
        <span class="chip-x" title="清空全部目录历史" style="cursor:pointer;border:none;background:none"
          @click.stop="clearDirHistory">🗑️</span>'''
assert old in s, 'rv1'
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
assert old in s, 'rv2'
s = s.replace(old, new)
io.open(p, 'w', encoding='utf-8', newline='\n').write(s)
print('RunView ok')
print('ALL OK')
