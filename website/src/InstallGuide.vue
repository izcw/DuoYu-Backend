<script setup>
import { ref, computed, onBeforeUnmount } from 'vue'
import IconMark from './IconMark.vue'
const browsers = [
  { id: 'chrome', name: 'Chrome', address: 'chrome://extensions/', action: '加载已解压的扩展程序' },
  { id: 'edge', name: 'Edge', address: 'edge://extensions/', action: '加载解压缩的扩展' }
]
const current = ref('chrome')
const browser = computed(() => browsers.find(item => item.id === current.value) || browsers[0])
const feedback = ref('')
let messageTimer
function choose(id) {
  current.value = id
  feedback.value = ''
  clearTimeout(messageTimer)
}
/** file:// 或 HTTP 下剪贴板权限可能不可用，此时选中地址供手动复制。 */
async function copyAddress() {
  clearTimeout(messageTimer)
  const requested = browser.value
  let message = '已复制'
  try {
    await navigator.clipboard.writeText(requested.address)
  } catch {
    if (requested.id !== current.value) return
    const node = document.getElementById('browser-address')
    const selection = window.getSelection()
    if (node && selection) {
      const range = document.createRange()
      range.selectNodeContents(node)
      selection.removeAllRanges()
      selection.addRange(range)
    }
    message = '请手动复制地址'
  }
  if (requested.id !== current.value) return
  feedback.value = message
  messageTimer = setTimeout(() => { feedback.value = '' }, 3500)
}
onBeforeUnmount(() => clearTimeout(messageTimer))
</script>
<template>
  <div class="guide-card">
    <div class="browser-choices" role="group" aria-label="选择安装浏览器">
      <button v-for="item in browsers" :key="item.id" type="button" :aria-pressed="current === item.id" @click="choose(item.id)"><IconMark :name="item.id"/>{{item.name}}</button>
    </div>
    <ol class="install-steps">
      <li><span>1</span><div><b>下载并解压</b><p>将 ZIP 解压到一个可以长期保留的文件夹。</p></div></li>
      <li><span>2</span><div><b>打开扩展管理页</b><p>在地址栏打开以下地址，开启「开发者模式」。</p><div class="copy-line"><code id="browser-address">{{browser.address}}</code><button type="button" @click="copyAddress" aria-label="复制扩展管理页地址" title="复制地址"><IconMark :name="feedback === '已复制' ? 'check' : 'copy'"/></button><small role="status">{{feedback}}</small></div></div></li>
      <li><span>3</span><div><b>加载扩展</b><p>点击「{{browser.action}}」，选择包含 <code>manifest.json</code> 的文件夹。</p></div></li>
    </ol>
  </div>
</template>
