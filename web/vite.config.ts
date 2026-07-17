/// <reference types="vitest" />
import { fileURLToPath, URL } from 'node:url'

import vue from '@vitejs/plugin-vue'
import { defineConfig } from 'vite'

export default defineConfig({
  plugins: [
    vue({
      template: {
        compilerOptions: {
          // 告诉编译器 altcha-* 是自定义元素，避免被当未知 Vue 组件报警。
          isCustomElement: (tag) => tag.startsWith('altcha-'),
        },
      },
    }),
  ],
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url)),
    },
  },
  server: {
    host: '0.0.0.0',
    port: 5173,
    proxy: {
      '/api': { target: 'http://ocm.localhost', changeOrigin: true },
    },
  },
  // vitest 与 playwright 共存：playwright 用 .spec.ts 文件名也会被 vitest 默认匹配，
  // 这里仅排除 Playwright 的 .spec.ts，保留同目录的配置单元测试由 vitest 执行。
  // 组件类用例（ConfirmActionModal 等）依赖 DOM API，统一启用 jsdom 环境。
  test: {
    environment: 'jsdom',
    exclude: ['node_modules/**', 'dist/**', 'tests/e2e/**/*.spec.ts'],
  },
})
