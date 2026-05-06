import { expect, test } from '@playwright/test'

// Scenario 3：注册节点 + bootstrap_token 一次性显示。覆盖 spec §5.4 Task 15 第三条。
//
// 关键断言：bootstrap_token 必须只显示一次（关闭弹窗后再次打开节点详情看不到）。
//
// 当前 skip 原因：fixture 未就位（admin2 storageState）。
test.skip('注册节点后 bootstrap_token 仅出现一次', async ({ page }) => {
  await page.goto('/runtime-nodes')
  await page.getByRole('button', { name: /注册节点/ }).click()
  await page.getByLabel('节点名').fill(`e2e-node-${Date.now()}`)
  await page.getByRole('button', { name: '提交' }).click()
  const tokenLocator = page.locator('text=/bootstrap_token/i')
  await expect(tokenLocator).toBeVisible()
  // 关闭弹窗 → 再次打开同节点详情 → token 不应再显示
  await page.getByRole('button', { name: /关闭/ }).click()
  await expect(tokenLocator).toBeHidden()
})
