import { expect, test, type Page } from '@playwright/test'

import { loadE2EFixture, loginAs } from './fixtures'

// mockConsoleQueries 固定平台控制台三类查询结果，隔离本地业务数据波动对图表生命周期回归的干扰。
async function mockConsoleQueries(page: Page): Promise<void> {
  // 总览数据同时驱动统计卡片与实例状态饼图，固定为运行、异常、停止各有可识别数量。
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

  // 平台每日用量提供非空折线序列，确保 Token 趋势页签必须初始化真实 ECharts canvas。
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

  // 企业用量提供两个非空柱形条，确保验证已初始化图表在后续页签显隐切换后继续保留 canvas。
  await page.route('**/api/v1/platform/usage/org-breakdown?**', async (route) => {
    await route.fulfill({
      json: {
        breakdown: {
          items: [
            { org_id: '00000000-0000-0000-0000-000000000001', org_name: '企业一', total_quota: 24000 },
            { org_id: '00000000-0000-0000-0000-000000000002', org_name: '企业二', total_quota: 12000 },
          ],
          updated_at: '2026-07-17T00:00:00Z',
        },
      },
    })
  })
}

// expectChartVisible 从用户可见页签进入当前面板，并验证唯一 ECharts canvas 已挂载、图表容器具备有效布局尺寸。
async function expectChartVisible(page: Page, tabName: string): Promise<void> {
  await page.locator('.n-tabs-tab', { hasText: tabName }).click()

  // animated TabPane 会保留过渡节点；只检查唯一可见面板，避免把已隐藏面板误判为当前图表。
  const visiblePane = page.locator('.n-tab-pane:visible')
  await expect(visiblePane).toHaveCount(1)
  const chart = visiblePane.locator('.chart-container')
  await expect(chart).toBeVisible()
  await expect(chart.locator('canvas')).toHaveCount(1)

  // 容器可见还不足以证明图表可绘制，宽高必须都大于零才符合 ECharts 初始化条件。
  const bounds = await chart.boundingBox()
  expect(bounds).not.toBeNull()
  expect(bounds!.width).toBeGreaterThan(0)
  expect(bounds!.height).toBeGreaterThan(0)
}

// 验证平台管理员连续切换三类图表三轮后，首次访问并保留的图表在反复显隐时仍保持 canvas。
test('平台控制台图表连续切换三轮后仍保持显示', { tag: '@quick' }, async ({ page }) => {
  const fx = loadE2EFixture()
  const pageErrors: Error[] = []
  page.on('pageerror', error => pageErrors.push(error))

  // 登录前注册查询拦截，保证进入控制台后的首批并发请求也获得确定响应。
  await mockConsoleQueries(page)
  await loginAs(page, 'platform_admin', fx, 'zh')
  await page.goto('/console')

  // 第一轮覆盖三个 pane 的首次访问，建立图表初始化并保留 DOM 的前置状态。
  await expectChartVisible(page, 'Token 趋势')
  await expectChartVisible(page, '各企业用量')
  await expectChartVisible(page, '实例状态')

  // 第二轮回到三个已访问页签，验证保留的容器和 ECharts 实例在再次显示时仍然有效。
  await expectChartVisible(page, 'Token 趋势')
  await expectChartVisible(page, '各企业用量')
  await expectChartVisible(page, '实例状态')

  // 第三轮继续重复完整切换，验证多轮显隐后图表 canvas 不会丢失。
  await expectChartVisible(page, 'Token 趋势')
  await expectChartVisible(page, '各企业用量')
  await expectChartVisible(page, '实例状态')

  // 页签生命周期切换不应产生未捕获的浏览器运行时错误。
  expect(pageErrors).toEqual([])
})
