import { expect, test } from '@playwright/test'

// Scenario 1：登录成功 + 失败。覆盖 spec §5.4 Task 15 第一条。
//
// 前置：manager-api 起来 + 数据库里有 platform_admin admin2 / Admin@1234（chunk-3 验证时创建）。
// 如果 CI 没这个账号，可以预先 seed-admin 一次。
test('登录成功后跳转到平台总览', async ({ page }) => {
  await page.goto('/login')
  await page.getByLabel('账号').fill('admin2')
  await page.getByLabel('密码').fill('Admin@1234')
  await page.getByRole('button', { name: '登录' }).click()
  await expect(page).toHaveURL(/\/(?:|dashboard|platform\/dashboard)/)
  await expect(page.getByRole('heading', { name: '控制台' })).toBeVisible()
})

test('密码错误返回错误提示', async ({ page }) => {
  await page.goto('/login')
  await page.getByLabel('账号').fill('admin2')
  await page.getByLabel('密码').fill('wrong-password')
  await page.getByRole('button', { name: '登录' }).click()
  await expect(page.getByText('用户名或密码错误')).toBeVisible()
})
