import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import { fileURLToPath, URL } from 'node:url'

const backend = process.env.INFRA_MONITOR_URL ?? 'http://localhost:8080'

export default defineConfig({
  base: '/',
  plugins: [vue()],
  resolve: {
    alias: { '@': fileURLToPath(new URL('./src', import.meta.url)) },
  },
  server: {
    port: 5173,
    proxy: {
      '/api': { target: backend, changeOrigin: false },
      '/healthz': { target: backend },
    },
  },
  build: {
    outDir: 'dist',
    // keep web/dist/.gitkeep (required by the Go embed directive)
    emptyOutDir: false,
    sourcemap: false,
    chunkSizeWarningLimit: 900,
  },
})
