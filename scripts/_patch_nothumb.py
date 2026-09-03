import io

# ReviewView: 缩略图缺失占位
p = 'frontend/src/views/ReviewView.vue'
s = io.open(p, encoding='utf-8').read()
old = '''          <div class="thumb-wrap clickable" :class="{ 'parse-fail': p.status === 'parse_fail' }"
            @click="preview = p" title="点击查看大图与评分详情">
            <img :src="`/api/thumb?id=${p.id}`" loading="lazy" @error="thumbFail(p)" />'''
new = '''          <div class="thumb-wrap clickable" :class="{ 'parse-fail': p.status === 'parse_fail' }"
            @click="preview = p" title="点击查看大图与评分详情">
            <img v-if="!p._noThumb" :src="`/api/thumb?id=${p.id}`" loading="lazy" @error="thumbFail(p)" />
            <div v-else class="no-thumb">缓存命中<br />无压缩图</div>'''
assert old in s, 'rv1'
s = s.replace(old, new)

old = '''function thumbFail(p) { p._noThumb = true }'''
new = '''function thumbFail(p) { p._noThumb = true }

// 缓存命中但无压缩图的照片（老批次）正常参与列表展示'''
assert old in s, 'rv2'
s = s.replace(old, new)

old = '''.scope-sel { font-size: 13px; padding: 4px 8px; vertical-align: middle; max-width: 240px; }'''
new = '''.scope-sel { font-size: 13px; padding: 4px 8px; vertical-align: middle; max-width: 240px; }
.no-thumb {
  width: 100%; height: 100%; display: flex; align-items: center; justify-content: center;
  color: var(--text-2); font-size: 12px; text-align: center; background: var(--card-2);
}'''
assert old in s, 'rv3'
s = s.replace(old, new)
io.open(p, 'w', encoding='utf-8', newline='\n').write(s)
print('ReviewView ok')

# PhotoModal: 大图缺失占位
p = 'frontend/src/components/PhotoModal.vue'
s = io.open(p, encoding='utf-8').read()
old = '''      <!-- 照片区 -->
      <div class="pm-photo">
        <img :src="`/api/thumb?id=${p.id}`" @click="$emit('close')" />'''
new = '''      <!-- 照片区 -->
      <div class="pm-photo">
        <img v-if="!noThumb" :src="`/api/thumb?id=${p.id}`" @error="noThumb = true" @click="$emit('close')" />
        <div v-else class="pm-noimg">缓存命中，无压缩图预览</div>'''
assert old in s, 'pm1'
s = s.replace(old, new)

old = '''const sharing = ref(false)
const copyHint = ref('📋 复制分享卡片')
const shareUrl = ref('')'''
new = '''const sharing = ref(false)
const copyHint = ref('📋 复制分享卡片')
const shareUrl = ref('')
const noThumb = ref(false)'''
assert old in s, 'pm2'
s = s.replace(old, new)

old = '''watch(() => p.value && p.value.id, () => {
  shareUrl.value = ''
  copyHint.value = '📋 复制分享卡片'
  resetRescore()
})'''
new = '''watch(() => p.value && p.value.id, () => {
  shareUrl.value = ''
  copyHint.value = '📋 复制分享卡片'
  noThumb.value = false
  resetRescore()
})'''
assert old in s, 'pm3'
s = s.replace(old, new)

old = '''.pm-photo img { max-width: 100%; max-height: 52vh; object-fit: contain; display: block; cursor: zoom-out; }'''
new = '''.pm-photo img { max-width: 100%; max-height: 52vh; object-fit: contain; display: block; cursor: zoom-out; }
.pm-noimg {
  min-height: 200px; display: flex; align-items: center; justify-content: center;
  color: var(--text-2); font-size: 14px;
}'''
assert old in s, 'pm4'
s = s.replace(old, new)
io.open(p, 'w', encoding='utf-8', newline='\n').write(s)
print('PhotoModal ok')
print('ALL OK')
