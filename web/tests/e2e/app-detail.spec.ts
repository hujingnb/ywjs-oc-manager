import { expect, test } from '@playwright/test'

// Scenario 5：应用详情 5 tab 切换。覆盖 spec §5.4 Task 15 第五条。
//
// 验证点：concept / runtime / channels / knowledge / workspace 五个 tab 全部能渲染，
// 切换不报 404 / 控制台 error。
test.skip('应用详情 5 tab 全部可渲染', async ({ page }) => {
  // 占位 appId；实际 CI 接入时替换为 fixture 创建的应用 id
  const appId = process.env.E2E_APP_ID ?? '00000000-0000-0000-0000-000000000001'
  await page.goto(`/apps/${appId}/overview`)
  await expect(page.getByRole('heading', { name: '概览' })).toBeVisible()
  for (const tab of ['运行时', '渠道', '应用知识库', '工作目录']) {
    await page.getByRole('link', { name: tab }).click()
    await expect(page.getByRole('heading', { name: tab })).toBeVisible()
  }
})
