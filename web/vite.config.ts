/// <reference types="vitest" />
import { fileURLToPath, URL } from 'node:url'

import vue from '@vitejs/plugin-vue'
import { defineConfig } from 'vite'

export default defineConfig({
  plugins: [vue()],
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url)),
    },
  },
  server: {
    host: '0.0.0.0',
    port: 5173,
    proxy: {
      '/api': 'http://manager-api:8080',
    },
  },
  // vitest 与 playwright 共存：playwright 用 .spec.ts 文件名也会被 vitest 默认匹配，
  // 这里显式排除 tests/e2e 目录，避免 vitest 误把 playwright API 当 vitest 跑。
  // 组件类用例（ConfirmActionModal 等）依赖 DOM API，统一启用 jsdom 环境。
  test: {
    environment: 'jsdom',
    exclude: ['node_modules/**', 'dist/**', 'tests/e2e/**'],
  },
})
