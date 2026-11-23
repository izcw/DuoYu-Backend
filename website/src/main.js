import { createApp } from 'vue'
import PreviewShowcase from './PreviewShowcase.vue'
import InstallGuide from './InstallGuide.vue'

createApp(PreviewShowcase).mount('#preview-app')
createApp(InstallGuide).mount('#install-app')

const menuButton = document.querySelector('.menu-button')
const mobileNav = document.querySelector('#mobile-nav')
function toggleMenu(open) {
  mobileNav.hidden = !open
  menuButton.setAttribute('aria-expanded', String(open))
  menuButton.setAttribute('aria-label', open ? '收起导航' : '展开导航')
}
menuButton.addEventListener('click', () => toggleMenu(mobileNav.hidden))
document.addEventListener('click', event => {
  if (!mobileNav.hidden && (!event.target.closest('.site-header') || event.target.closest('a'))) toggleMenu(false)
})
document.addEventListener('keydown', event => {
  if (event.key === 'Escape' && !mobileNav.hidden) {
    toggleMenu(false)
    menuButton.focus()
  }
})
window.matchMedia('(min-width: 701px)').addEventListener('change', event => {
  if (event.matches) toggleMenu(false)
})

const sections = [...document.querySelectorAll('main > section[id]')]
const navigation = [...document.querySelectorAll('.nav-links a, #mobile-nav a')]
let pendingFrame = false
/** 使用视口位置同步导航选中状态，兼容锚点跳转和回到顶部。 */
function updateNavigation() {
  const threshold = Math.min(220, window.innerHeight * .32)
  const reachedEnd = window.innerHeight + window.scrollY >= document.documentElement.scrollHeight - 2
  const active = reachedEnd ? sections.at(-1) : sections.filter(section => section.getBoundingClientRect().top <= threshold).at(-1) || sections[0]
  for (const link of navigation) {
    if (link.hash === '#' + active.id) link.setAttribute('aria-current', 'location')
    else link.removeAttribute('aria-current')
  }
  pendingFrame = false
}
function scheduleNavigation() {
  if (pendingFrame) return
  pendingFrame = true
  requestAnimationFrame(updateNavigation)
}
window.addEventListener('scroll', scheduleNavigation, { passive: true })
window.addEventListener('resize', scheduleNavigation, { passive: true })
updateNavigation()
