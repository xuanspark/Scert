import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

export default defineConfig({
  plugins: [vue()],
  publicDir: 'logo',
  build: {
    sourcemap: false,
  },
  server: (() => {
    const apiTarget = process.env.SCERT_API_TARGET || 'https://127.0.0.1:5200';
    const apiProxy = {
      target: apiTarget,
      changeOrigin: false,
      secure: false,
    };

    return {
      host: process.env.SCERT_DEV_HOST || '127.0.0.1',
      port: Number(process.env.SCERT_DEV_PORT || 5173),
      proxy: {
        '/api': apiProxy,
        '/healthz': apiProxy,
        '/reset': {
          ...apiProxy,
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
    };
  })(),
})
