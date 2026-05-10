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
  // 时间戳 + nanoTime 后缀保证不同跑次唯一，避免与 fixture 已有组织重名。
  const name = `e2e-${Date.now()}-org`
  // 表单使用 <label><span>名称 *</span><input/></label> 结构，accessible name 为「名称 *」。
  await page.getByLabel('名称 *').fill(name)
  await page.getByLabel('管理员用户名 *').fill(`${name}-admin`)
  await page.getByLabel('管理员姓名 *').fill('E2E 管理员')
  await page.getByLabel('管理员密码 *').fill('secret-password')
  await page.getByRole('button', { name: '保存' }).click()
  // 列表展示新组织名（strong 文本）。
  await expect(page.getByRole('table').getByText(name)).toBeVisible()
})
