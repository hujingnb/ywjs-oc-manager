import { defineConfig, devices } from '@playwright/test'

// Playwright 配置：覆盖 v1.0 RC spec §5.4 Task 15 列出的 6 个核心场景。
//
// 运行：
//   1. 启动后端：make local-up（k3d 全栈，已取代 docker compose）
//   2. 启动前端：npm run dev（webServer 段会自动跑）
//   3. 装浏览器：npx playwright install chromium
//   4. 跑用例：npm run test:e2e
//
// 用例位于 web/tests/e2e/。当前 6 个场景中 login 已可运行，其余因为依赖
// 预置数据（组织 / 节点 / 成员），先标 test.skip 留作 hot-fix CI 接入时启用。
export default defineConfig({
  testDir: './tests/e2e',
  // Playwright 只收集 .spec.ts；同目录的 .test.ts 属于 Vitest，避免两个运行器同时加载 matcher。
  testMatch: '**/*.spec.ts',
  fullyParallel: false,
  retries: 0,
  workers: 1,
  reporter: [['list']],
  timeout: 30_000,
  // globalSetup：跑 make seed-e2e，把 fixture JSON 注入 process.env.OCM_E2E_FIXTURE。
  globalSetup: './tests/e2e/global-setup.ts',
  use: {
    baseURL: process.env.PLAYWRIGHT_BASE_URL ?? 'http://ocm.localhost',
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
    video: 'retain-on-failure',
  },
  projects: [
    {
      name: 'chromium',
      use: { ...devices['Desktop Chrome'] },
    },
    // chrome-headed 使用宿主机已安装的 Chrome Stable 做发布前验收；保留 chromium
    // 项目供快速回归，避免日常测试必须占用图形桌面。
    {
      name: 'chrome-headed',
      retries: 1,
      use: {
        ...devices['Desktop Chrome'],
        channel: 'chrome',
        headless: false,
        trace: 'on-first-retry',
        // Playwright 的 screenshot 仅支持 failure 模式；首轮重试失败会保留截图，
        // 与 trace/video 的 on-first-retry 一起形成可回放证据。
        screenshot: 'only-on-failure',
        video: 'on-first-retry',
      },
    },
  ],
})
