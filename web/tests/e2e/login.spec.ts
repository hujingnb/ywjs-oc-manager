import { expect, test } from './fixtures'

// Scenario 1：登录成功 + 失败。覆盖 spec §5.4 Task 15 第一条。
//
// 前置：globalSetup 为当前 worker 注入隔离平台管理员；本 spec 仍从未认证 page 验证真实表单。
test('登录成功后跳转到平台总览', { tag: '@quick' }, async ({ page, e2eFixture: fx }) => {
  await page.goto('/login')
  await page.getByLabel(/^(账号|Username|Account)$/).fill(fx.platform_admin_login)
  await page.getByLabel(/^(密码|Password)$/).fill(fx.platform_admin_password)
  await page.getByRole('button', { name: /^(登录|Log in)$/ }).click()
  await expect(page).toHaveURL(/\/(?:|dashboard|platform\/dashboard)/)
  await expect(page.getByRole('heading', { name: /^(控制台|Console)$/ })).toBeVisible()
})

test('密码错误返回错误提示', { tag: '@quick' }, async ({ page, e2eFixture: fx }) => {
  await page.goto('/login')
  await page.getByLabel(/^(账号|Username|Account)$/).fill(fx.platform_admin_login)
  await page.getByLabel(/^(密码|Password)$/).fill('wrong-password')
  await page.getByRole('button', { name: /^(登录|Log in)$/ }).click()
  await expect(page.getByText(/账号或密码错误|Incorrect account or password/)).toBeVisible()
})
