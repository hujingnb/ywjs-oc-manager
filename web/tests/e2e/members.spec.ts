import { expect, test } from '@playwright/test'

import { loadE2EFixture, loginAs } from './fixtures'

// Scenario 4：org_admin 重置成员密码，顺带覆盖 T1 ConfirmActionModal 强校验：
//   - 输入框文案不匹配登录名 → 「确认重置」按钮 disabled；
//   - 输入框等于登录名 → 按钮 enabled，点击后请求成功并展示「已重置密码」。
//
// 注意：MembersPage.vue 用 window.prompt 收集新密码（≥8 位），Playwright 默认会
// dismiss prompt，因此这里在 page evaluate 里覆写 window.prompt 让它返回新密码。
test('org_admin 重置成员密码 — 强校验输错名应拒绝', async ({ page }) => {
  const fx = loadE2EFixture()
  // 在导航到 /members 之前覆写 prompt：通过 addInitScript 保证每个 doc 都生效。
  await page.addInitScript(() => {
    // 任何对 window.prompt 的调用都返回固定新密码。
    window.prompt = () => 'new-pass-12345'
  })
  await loginAs(page, 'org_admin', fx)
  await page.goto('/members')

  const row = page.getByRole('row', { name: new RegExp(fx.org_member_login) })
  await expect(row).toBeVisible()
  await row.getByRole('button', { name: '重置密码' }).click()

  // ConfirmActionModal 的强校验输入框统一是 .modal-card .verify-input；
  // 提示语用 <span> 而非 placeholder，因此用 class 锁定输入框。
  const verifyInput = page.locator('.modal-card .verify-input')
  await verifyInput.fill('wrong-name')
  await expect(page.getByRole('button', { name: '确认重置' })).toBeDisabled()

  await verifyInput.fill(fx.org_member_login)
  await expect(page.getByRole('button', { name: '确认重置' })).toBeEnabled()
  await page.getByRole('button', { name: '确认重置' }).click()

  // 成功后页面底部的状态文本会显示「已重置密码」（resetFeedback）。
  await expect(page.getByText('已重置密码')).toBeVisible()
})
