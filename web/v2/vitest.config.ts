import { defineConfig } from 'vitest/config'
import vue from '@vitejs/plugin-vue'
import { fileURLToPath, URL } from 'node:url'

// Vitest 配置 — 前端单元/组件测试。
// 复用 vite.config.ts 的 @ 别名与 vue 插件，测试环境用 jsdom 提供 DOM。
export default defineConfig({
  plugins: [vue()],
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url)),
    },
  },
  test: {
    environment: 'jsdom',
    globals: true,
    include: ['src/**/*.{test,spec}.ts'],
    // CSS 不需要真实渲染，直接 stub 掉避免污染 jsdom
    css: false,
  },
})
