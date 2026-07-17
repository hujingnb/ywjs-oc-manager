import { expect, test } from './fixtures'

// Scenario 5：实例详情 5 tab 全部可渲染。
//
// AppDetailPage 用 <RouterLink> 实现 tab，对应 a 标签 role=link，
// 因此用 getByRole('link', ...)；每切一次 tab 后断言无红色 error 文本。
test('实例详情 5 tab 全部可渲染', { tag: '@quick' }, async ({ orgAdminPage: page, e2eFixture: fx }) => {
  await page.goto(`/apps/${fx.app_id}/overview`)

  // 等到顶部实例名标题加载，确保 useAppQuery 已经完成。
  await expect(page.getByRole('heading', { name: new RegExp(fx.app_name) })).toBeVisible()

  // 实际中文 tab 文案见 AppDetailPage.vue 的 tabs 数组。
  const tabs = ['概览', '运行时', '渠道', '实例知识库', '工作目录']
  for (const tab of tabs) {
    await page.getByRole('link', { name: tab, exact: true }).click()
    // 切换后确保没有 .danger 状态文本（"查询失败：..." 提示）。
    await expect(page.locator('.state-text.danger')).toHaveCount(0)
    // 当前 tab 的 active class 应当存在，避免 tab 实际未切换。
    await expect(page.locator('.tab-link-active', { hasText: tab })).toBeVisible()
  }
})
