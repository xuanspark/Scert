import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

export default defineConfig({
  plugins: [vue()],
  publicDir: 'logo',
  server: {
    host: process.env.SCERT_DEV_HOST || '127.0.0.1',
    port: Number(process.env.SCERT_DEV_PORT || 5173),
    proxy: {
      '/api': process.env.SCERT_API_TARGET || 'http://127.0.0.1:8090',
      '/healthz': process.env.SCERT_API_TARGET || 'http://127.0.0.1:8090',
    },
  },
})
