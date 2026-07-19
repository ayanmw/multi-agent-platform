import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import { fileURLToPath, URL } from 'node:url'

// https://vite.dev/config/
export default defineConfig({
  plugins: [vue()],
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url)),
    },
  },
  // Relative base path so the embed-friendly dist/ works regardless of
  // whether it is served from / or /app/ or behind a reverse proxy.
  base: './',
  server: {
    // Dev server: proxy /ws and /api to the Go backend
    port: 3001,
    proxy: {
      '/ws': {
        target: 'http://localhost:8080',
        ws: true,
      },
      '/api': {
        target: 'http://localhost:8080',
      },
      '/health': {
        target: 'http://localhost:8080',
      },
    },
  },
  build: {
    outDir: 'dist',
    // Generate source maps for debugging in production
    sourcemap: true,
    // Chunk size warning threshold (500 kB)
    chunkSizeWarningLimit: 500,
  },
})
