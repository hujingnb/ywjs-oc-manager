# 平台控制台图表 Tab 生命周期修复 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 修复平台管理员控制台三个图表在连续切换 Tab 后变为空白的问题，并用定向 Playwright 用例锁定回归行为。

**Architecture:** 保持现有查询和 ECharts 实例管理不变，仅把三个 Naive UI TabPane 的显示策略从默认 `if` 改为 `show:lazy`，使每个图表容器首次访问后持续保留。Playwright 用例通过拦截三个控制台数据接口提供确定性数据，再对三个 Tab 连续切换三轮并断言活动图表始终包含有效 canvas。

**Tech Stack:** Vue 3、TypeScript、Naive UI 2.44.1、ECharts 6、Playwright 1.59.1

---

## 文件结构

- Create: `web/tests/e2e/console.spec.ts` — 平台控制台图表 Tab 往返切换的定向浏览器回归测试。
- Modify: `web/src/pages/platform/ConsolePage.vue` — 为三个图表 TabPane 设置 `show:lazy`，统一 DOM 与 ECharts 实例生命周期。

本修复不改后端接口、OpenAPI、生成类型或用户手册。实现与测试属于同一缺陷边界，完成验证后合并为一个 `fix(web)` 提交。

### Task 1: 用定向浏览器测试复现图表容器丢失

**Files:**
- Create: `web/tests/e2e/console.spec.ts`
- Reference: `web/tests/e2e/fixtures.ts`
- Reference: `web/src/pages/platform/ConsolePage.vue`

- [ ] **Step 1: 写入会在当前实现上失败的 Playwright 用例**

创建 `web/tests/e2e/console.spec.ts`，内容如下：

```ts
import { expect, test, type Page } from '@playwright/test'

import { loadE2EFixture, loginAs } from './fixtures'

// mockConsoleQueries 为三个控制台查询提供确定性数据，避免 new-api 实时用量波动影响图表断言。
async function mockConsoleQueries(page: Page): Promise<void> {
  await page.route('**/api/v1/platform/overview', async (route) => {
    await route.fulfill({
      json: {
        overview: {
          organization_count: 2,
          member_count: 5,
          app_count: 4,
          running_app_count: 2,
          error_app_count: 1,
          total_remain_quota: 0,
          usage_available: true,
        },
      },
    })
  })
  await page.route('**/api/v1/usage/platform?**', async (route) => {
    await route.fulfill({
      json: {
        usage: {
          scope: 'platform',
          items: [
            { date: '2026-07-16', quota: 12000 },
            { date: '2026-07-17', quota: 34000 },
          ],
          updated_at: '2026-07-17T00:00:00Z',
        },
      },
    })
  })
  await page.route('**/api/v1/platform/usage/org-breakdown?**', async (route) => {
    await route.fulfill({
      json: {
        breakdown: {
          items: [
            { org_id: 'org-1', org_name: '企业一', total_quota: 24000 },
            { org_id: 'org-2', org_name: '企业二', total_quota: 12000 },
          ],
          updated_at: '2026-07-17T00:00:00Z',
        },
      },
    })
  })
}

// expectChartVisible 打开指定 Tab，并校验活动图表拥有可见容器和 ECharts canvas。
async function expectChartVisible(page: Page, tabName: string): Promise<void> {
  await page.locator('.n-tabs-tab', { hasText: tabName }).click()
  const visiblePane = page.locator('.n-tab-pane:visible')
  await expect(visiblePane).toHaveCount(1)
  const container = visiblePane.locator('.chart-container')
  await expect(container).toBeVisible()
  await expect(container.locator('canvas')).toHaveCount(1)
  const size = await container.evaluate((element) => {
    const rect = element.getBoundingClientRect()
    return { width: rect.width, height: rect.height }
  })
  expect(size.width).toBeGreaterThan(0)
  expect(size.height).toBeGreaterThan(0)
}

// 该用例覆盖三个控制台图表首次加载及连续往返切换时的 DOM 生命周期回归。
test('平台控制台图表连续切换三轮后仍保持显示', async ({ page }) => {
  const fx = loadE2EFixture()
  const pageErrors: string[] = []
  page.on('pageerror', error => pageErrors.push(error.message))
  await mockConsoleQueries(page)
  await loginAs(page, 'platform_admin', fx, 'zh')
  await page.goto('/console')

  // 第一轮覆盖三个图表的首次按需初始化。
  await expectChartVisible(page, 'Token 趋势')
  await expectChartVisible(page, '各企业用量')
  await expectChartVisible(page, '实例状态')

  // 第二轮复现旧实现中 ECharts 实例仍指向已销毁容器的异常路径。
  await expectChartVisible(page, 'Token 趋势')
  await expectChartVisible(page, '各企业用量')
  await expectChartVisible(page, '实例状态')

  // 第三轮确认三个已初始化图表可继续重复显示和 resize。
  await expectChartVisible(page, 'Token 趋势')
  await expectChartVisible(page, '各企业用量')
  await expectChartVisible(page, '实例状态')

  // 页面切换过程中不应产生未捕获的 JavaScript 异常。
  expect(pageErrors).toEqual([])
})
```

- [ ] **Step 2: 运行定向用例并确认它准确复现缺陷**

Run:

```bash
cd web
npm run test:e2e -- console.spec.ts --project=chromium
```

Expected: FAIL。第一轮首次访问三个图表通过，第二轮切回「Token 趋势」时 `container.locator('canvas')` 的实际数量为 `0`，而期望数量为 `1`。不得在此阶段放宽断言、增加重试或跳过用例。

### Task 2: 保留首次访问后的 Tab 图表容器

**Files:**
- Modify: `web/src/pages/platform/ConsolePage.vue:23-42`
- Test: `web/tests/e2e/console.spec.ts`

- [ ] **Step 1: 为三个图表 TabPane 设置 `show:lazy`**

在 `web/src/pages/platform/ConsolePage.vue` 中把三个起始标签分别改为：

```vue
<n-tab-pane name="token" :tab="t('platform.console.tabs.tokenTrend')" display-directive="show:lazy">
```

```vue
<n-tab-pane name="orgs" :tab="t('platform.console.tabs.orgUsage')" display-directive="show:lazy">
```

```vue
<n-tab-pane name="status" :tab="t('platform.console.tabs.instanceStatus')" display-directive="show:lazy">
```

不要修改 `animated`、`onTabChange`、三个 `build*Chart` 函数或查询逻辑。`show:lazy` 让 pane 首次访问后以 `v-show` 保留，现有实例变量与容器 ref 因而保持一一对应。

- [ ] **Step 2: 重新运行定向 Playwright 用例**

Run:

```bash
cd web
npm run test:e2e -- console.spec.ts --project=chromium
```

Expected: PASS，输出包含 `1 passed`。三轮切换中每个活动图表均包含一个 canvas，且没有 page error。

- [ ] **Step 3: 运行前端类型检查**

Run:

```bash
cd web
npm run typecheck
```

Expected: PASS，`vue-tsc --noEmit` 退出码为 0 且没有 TypeScript 错误。

- [ ] **Step 4: 使用本地真实浏览器复验原始路径**

确认 `http://ocm.localhost` 可访问后，以 AGENTS.md 记录的本地平台管理员账号登录。使用 Chromium 无头模式完成以下操作：

1. 进入 `/console`，确认 Token 趋势显示；
2. 连续执行三轮「Token 趋势 → 各企业用量 → 实例状态」；
3. 每次点击后检查活动 `.chart-container` 包含 canvas，容器宽高大于零；
4. 截取最终活动 Tab 的页面截图到 `/tmp` 供当次检查，不加入 git；
5. 检查浏览器 `pageerror` 为空，三个控制台 API 没有新增失败请求。

Expected: 三个有数据图表始终显示；若各企业用量接口返回空列表，则该 Tab 保持现有「暂无数据」状态，其余两个图表仍需通过切换验证。

- [ ] **Step 5: 检查差异范围和格式**

Run:

```bash
git diff --check
git status --short
git diff -- web/src/pages/platform/ConsolePage.vue web/tests/e2e/console.spec.ts
```

Expected: `git diff --check` 无输出；状态中仅包含 `ConsolePage.vue` 和新建的 `console.spec.ts`，差异只包含回归测试与三个 `display-directive` 属性。

- [ ] **Step 6: 提交修复和回归测试**

```bash
git add web/src/pages/platform/ConsolePage.vue web/tests/e2e/console.spec.ts
git commit -m "fix(web): 修复控制台图表切换后空白" -m "三个图表 Tab 首次访问后保留容器，避免 ECharts 实例继续引用已销毁的 DOM。\n\n增加定向 Playwright 用例，覆盖三个 Tab 连续切换三轮后的画布与容器尺寸。"
```

Expected: 生成一个同时包含最小修复与对应回归测试的 `fix(web)` 提交。
