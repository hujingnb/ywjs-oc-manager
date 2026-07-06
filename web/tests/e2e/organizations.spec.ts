import { expect, test } from '@playwright/test'

import { loginAs } from './fixtures'

// Scenario 2：platform_admin 创建组织。覆盖 spec §5.4 Task 15 第二条。
//
// 关键路径：登录 → /organizations → 点击「新增组织」打开表单 →
// 填写唯一名称和初始管理员 → 点「保存」→ 列表里应能看到新组织名。
test('platform_admin 可创建组织', async ({ page }) => {
  await loginAs(page, 'platform_admin')
  await page.goto('/organizations')
  await page.getByRole('button', { name: '新增组织' }).click()
  // 秒级时间戳保证不同跑次唯一，同时避免 new-api display_name 超过 20 字符。
  const name = `e2e-${Math.floor(Date.now() / 1000)}-org`
  // Naive UI 的表单项标题不直接作为 input label，使用稳定的 placeholder 定位输入框。
  await page.getByPlaceholder('组织名称').fill(name)
  await page.getByPlaceholder('test-org').fill(name)
  await page.getByPlaceholder('登录账号').fill(`${name}-admin`)
  await page.getByPlaceholder('管理员姓名').fill('E2E 管理员')
  await page.getByPlaceholder('初始登录密码').fill('secret-password')
  await page.getByRole('button', { name: '保存' }).click()
  // 列表展示新组织行；名称和组织标识相同，因此断言整行避免 strict mode 匹配到两个单元格。
  await expect(page.getByRole('row', { name: new RegExp(name) })).toBeVisible()
})
