import { expect, test } from '@playwright/test'

// Scenario 4：成员开户。覆盖 spec §5.4 Task 15 第四条。
//
// 验证点：onboard 成功后该成员行立即出现 + apps 列表也立即出现关联应用。
test.skip('org_admin 完成成员开户后立即出现成员与应用', async ({ page }) => {
  await page.goto('/members')
  await page.getByRole('link', { name: /新增成员|开户/ }).click()
  const username = `e2e-member-${Date.now()}@example.com`
  await page.getByLabel('用户名').fill(username)
  await page.getByLabel('展示名').fill('E2E 测试成员')
  await page.getByRole('button', { name: /开户/ }).click()
  await expect(page.getByText(username)).toBeVisible()
  await page.goto('/apps')
  await expect(page.getByText(/E2E 测试成员/)).toBeVisible()
})
