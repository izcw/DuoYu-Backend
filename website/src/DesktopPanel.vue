<script setup>
import { computed, ref } from 'vue'
import IconMark from './IconMark.vue'

defineProps({
  theme: { type: Object, required: true },
  now: { type: Date, required: true },
  expanded: Boolean
})
const emit = defineEmits(['expand', 'close'])
const quiet = defineModel('quiet', { type: Boolean, default: false })
const engine = defineModel('engine', { type: String, default: 'bing' })
const query = defineModel('query', { type: String, default: '' })
const field = ref(null)
const error = ref('')
const logoPath = './assets/duoyu.png'

/** 引擎分别使用自己的查询参数，交给浏览器的原生 GET 表单编码中文。 */
const engines = [
  { id: 'bing', name: 'Bing', url: 'https://www.bing.com/search', param: 'q', icon: 'bing.svg' },
  { id: 'google', name: 'Google', url: 'https://www.google.com/search', param: 'q', icon: 'google.png' },
  { id: 'baidu', name: '百度', url: 'https://www.baidu.com/s', param: 'wd', icon: 'baidu.png' },
  { id: 'github', name: 'GitHub', url: 'https://github.com/search', param: 'q', icon: 'github.png' },
  { id: 'yandex', name: 'Yandex', url: 'https://yandex.com/search/', param: 'text', icon: 'yandex.png' }
]
const links = [
  { title: 'GitHub', file: 'github.png', url: 'https://github.com/' },
  { title: '哔哩哔哩', file: 'bilibili.png', url: 'https://www.bilibili.com/' },
  { title: 'DeepSeek', file: 'deepseek.png', url: 'https://chat.deepseek.com/' },
  { title: '微信读书', file: 'weread.png', url: 'https://weread.qq.com/' }
]
const selected = computed(() => engines.find(item => item.id === engine.value) || engines[0])
const formatClock = new Intl.DateTimeFormat('zh-CN', { hour: '2-digit', minute: '2-digit', hourCycle: 'h23' })
const formatDate = new Intl.DateTimeFormat('zh-CN', { month: 'long', day: 'numeric', weekday: 'long' })

function validateSearch(event) {
  if (query.value.trim()) return
  event.preventDefault()
  error.value = '请输入搜索内容'
  field.value?.focus()
}
</script>
<template>
  <div class="browser-frame" :class="{ 'browser-expanded': expanded }">
    <div class="browser-bar">
      <span class="traffic" aria-hidden="true"><i></i><i></i><i></i></span>
      <span class="browser-tab"><img :src="logoPath" alt="">多鱼新标签页</span>
      <button v-if="!expanded" class="frame-action" type="button" aria-label="放大桌面预览" title="放大桌面预览" @click="emit('expand')"><IconMark name="expand"/></button>
      <button v-else class="frame-action" type="button" aria-label="关闭桌面预览" title="关闭桌面预览" @click="emit('close')"><IconMark name="close"/></button>
    </div>
    <div class="desktop-view">
      <img class="wallpaper" :src="'./assets/' + theme.file" :alt="theme.name + '像素风景壁纸'" fetchpriority="high">
      <div class="desk-content" :class="{ 'is-quiet': quiet }">
        <button type="button" class="desk-clock" :aria-pressed="quiet" :aria-label="quiet ? '显示桌面快捷方式' : '切换为简洁桌面'" title="点击时钟切换简洁模式" @click="quiet = !quiet"><time :datetime="now.toISOString()">{{ formatClock.format(now) }}</time></button>
        <span class="desk-date">{{ formatDate.format(now) }}</span>
        <form class="desk-search" role="search" :action="selected.url" method="get" target="_blank" rel="noopener noreferrer" @submit="validateSearch">
          <label class="engine-choice" title="切换搜索引擎">
            <img :src="'./assets/' + selected.icon" alt=""><IconMark name="chevron-down"/>
            <select v-model="engine" aria-label="选择搜索引擎"><option v-for="item in engines" :key="item.id" :value="item.id">{{ item.name }}</option></select>
          </label>
          <input ref="field" v-model.trim="query" type="text" :name="selected.param" autocomplete="off" enterkeyhint="search" aria-label="搜索关键词" placeholder="搜索点什么…" @input="error = ''">
          <button type="submit" aria-label="在新标签页搜索" title="搜索"><IconMark name="search"/></button>
          <span v-if="error" class="search-hint" role="status">{{ error }}</span>
        </form>
        <div v-show="!quiet" class="desk-links">
          <a v-for="item in links" :key="item.title" :href="item.url" target="_blank" rel="noopener noreferrer" :aria-label="'在新标签页打开' + item.title">
            <span class="app-tile"><img :src="'./assets/' + item.file" alt=""></span><span>{{ item.title }}</span>
          </a>
        </div>
      </div>
    </div>
  </div>
</template>
