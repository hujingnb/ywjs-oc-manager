import { expect, test } from '@playwright/test'

// Scenario 2：创建组织。覆盖 spec §5.4 Task 15 第二条。
//
// 实际跑前需要：
//  1. 已用 admin2 登录（可用 storageState 预置）；
//  2. 测试组织名要避开数据库已存在的「测试组织 A」/「smoke-org-2」。
//
// 当前 skip 原因：尚未准备 fixture / storageState。CI 接入时去掉 .skip 并加预置脚本。
test.skip('platform_admin 可创建组织', async ({ page }) => {
  await page.goto('/organizations')
  await page.getByRole('button', { name: /新建/ }).click()
  const name = `e2e-org-${Date.now()}`
  await page.getByLabel('组织名').fill(name)
  await page.getByRole('button', { name: '提交' }).click()
  await expect(page.getByText(name)).toBeVisible()
})
