<script setup>
import { ref, nextTick, onMounted, onBeforeUnmount } from 'vue'
import DesktopPanel from './DesktopPanel.vue'
import WallpaperControls from './WallpaperControls.vue'

const themes = [
  { name: '山湖暮色', file: 'landscape.webp', color: '#9b83db' },
  { name: '薄荷海岸', file: 'coast.webp', color: '#71b9ae' },
  { name: '暖金沙丘', file: 'dunes.webp', color: '#e6ac78' }
]
const activeIndex = ref(0)
const now = ref(new Date())
const quiet = ref(false)
const engine = ref('bing')
const query = ref('')
const modal = ref(null)
const expanded = ref(false)
let opener
let clockTimer

/** 预览和放大视图共用主题和搜索状态，关闭后返回原来的触发按钮。 */
async function openPreview(event) {
  if (!modal.value || modal.value.open) return
  opener = event?.currentTarget || document.activeElement
  expanded.value = true
  await nextTick()
  if (!modal.value || modal.value.open) return
  modal.value.showModal()
  document.documentElement.classList.add('modal-open')
}
function closePreview() { modal.value?.close() }
function afterClose() {
  expanded.value = false
  document.documentElement.classList.remove('modal-open')
  if (opener instanceof HTMLElement) opener.focus({ preventScroll: true })
}
function closeBackdrop(event) {
  if (event.target !== modal.value) return
  const box = modal.value.getBoundingClientRect()
  if (event.clientX < box.left || event.clientX > box.right || event.clientY < box.top || event.clientY > box.bottom) closePreview()
}
onMounted(() => {
  clockTimer = window.setInterval(() => { now.value = new Date() }, 1000 * 10)
  document.querySelector('#open-preview')?.addEventListener('click', openPreview)
})
onBeforeUnmount(() => {
  window.clearInterval(clockTimer)
  document.querySelector('#open-preview')?.removeEventListener('click', openPreview)
  document.documentElement.classList.remove('modal-open')
})
</script>
<template>
  <DesktopPanel v-model:quiet="quiet" v-model:engine="engine" v-model:query="query" :theme="themes[activeIndex]" :now="now" @expand="openPreview"/>
  <WallpaperControls :themes="themes" :active-index="activeIndex" @select="activeIndex = $event"/>
  <Teleport to="body">
    <dialog ref="modal" class="preview-dialog" aria-label="多鱼互动桌面预览" @close="afterClose" @click="closeBackdrop">
      <template v-if="expanded">
        <DesktopPanel expanded v-model:quiet="quiet" v-model:engine="engine" v-model:query="query" :theme="themes[activeIndex]" :now="now" @close="closePreview"/>
        <WallpaperControls :themes="themes" :active-index="activeIndex" @select="activeIndex = $event"/>
      </template>
    </dialog>
  </Teleport>
</template>
