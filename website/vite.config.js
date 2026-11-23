import { fileURLToPath } from 'node:url'
import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

export default defineConfig({
  base: './',
  plugins: [vue({ template: { transformAssetUrls: false } })],
  define: { 'process.env.NODE_ENV': '"production"', __VUE_OPTIONS_API__: false },
  build: {
    // 单入口经典脚本兼容双击 HTML，也允许将整个 dist 放在任意静态子目录。
    outDir: 'dist',
    emptyOutDir: false,
    lib: {
      entry: fileURLToPath(new URL('./src/main.js', import.meta.url)),
      name: 'DuoYuSite',
      formats: ['iife'],
      fileName: () => 'assets/site.js'
    }
  }
})
