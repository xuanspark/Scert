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
      '/reset': {
        target: process.env.SCERT_API_TARGET || 'http://127.0.0.1:8090',
        changeOrigin: false,
        bypass(request) {
          if (
            request.method === 'GET' &&
            request.headers.accept &&
            request.headers.accept.includes('text/html')
          ) {
            return '/index.html';
          }
          return undefined;
        },
      },
    },
  },
})
