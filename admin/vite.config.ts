import { defineConfig } from 'vite';
import vue from '@vitejs/plugin-vue';
export default defineConfig({
  base: '/admin/',
  plugins: [vue()],
  server: {
    port: 5270,
    proxy: {
      '/api': 'http://127.0.0.1:5261',
      '/auth': 'http://127.0.0.1:5261',
    },
  },
  build: { chunkSizeWarningLimit: 1200 },
});
