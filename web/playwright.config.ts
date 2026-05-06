import { defineConfig, devices } from '@playwright/test'

// Playwright 配置：覆盖 v1.0 RC spec §5.4 Task 15 列出的 6 个核心场景。
//
// 运行：
//   1. 启动后端：docker compose up -d
//   2. 启动前端：npm run dev（webServer 段会自动跑）
//   3. 装浏览器：npx playwright install chromium
//   4. 跑用例：npm run test:e2e
//
// 用例位于 web/tests/e2e/。当前 6 个场景中 login 已可运行，其余因为依赖
// 预置数据（组织 / 节点 / 成员），先标 test.skip 留作 hot-fix CI 接入时启用。
export default defineConfig({
  testDir: './tests/e2e',
  fullyParallel: false,
  retries: 0,
  workers: 1,
  reporter: [['list']],
  timeout: 30_000,
  use: {
    baseURL: process.env.PLAYWRIGHT_BASE_URL ?? 'http://localhost:5173',
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
  },
  projects: [
    {
      name: 'chromium',
      use: { ...devices['Desktop Chrome'] },
    },
  ],
})
