import { expect, test } from '@playwright/test'

import { loadE2EFixture, loginAs } from './fixtures'

// 组织管理员通过站内专用弹窗重置密码：覆盖掩码、长度校验、显隐切换及关闭后的敏感值清理。
// 本场景只验证交互，不提交重置请求，避免修改共享 fixture 的登录密码。
test('org_admin 使用专用弹窗填写并清理成员新密码', { tag: '@quick' }, async ({ page }) => {
  const fx = loadE2EFixture()
  let nativeDialogOpened = false
  // 原生 dialog 监听用于回归确认页面不再调用 window.prompt；意外出现时立即关闭，避免测试挂起。
  page.on('dialog', async (dialog) => {
    nativeDialogOpened = true
    await dialog.dismiss()
  })

  await loginAs(page, 'org_admin', fx, 'zh')
  await page.goto('/members')

  const row = page.getByRole('row', { name: new RegExp(fx.org_member_login) })
  await expect(row).toBeVisible()
  await row.getByRole('button', { name: '重置密码' }).click()

  const passwordInput = page.getByLabel(`输入成员 ${fx.org_member_login} 的新密码`)
  await expect(passwordInput).toHaveAttribute('type', 'password')
  expect(nativeDialogOpened).toBe(false)

  await passwordInput.fill('Zs12345')
  await expect(page.getByRole('button', { name: '确认重置' })).toBeDisabled()

  await passwordInput.fill('Zs12345612')
  await expect(page.getByRole('button', { name: '确认重置' })).toBeEnabled()
  await page.locator('.n-input__eye').click()
  await expect(passwordInput).toHaveAttribute('type', 'text')

  await page.getByRole('button', { name: '取消' }).click()
  await expect(passwordInput).toBeHidden()

  // 重新打开后必须清空上一次未提交的敏感值，关闭前再确认输入仍为密码掩码。
  await row.getByRole('button', { name: '重置密码' }).click()
  const reopenedPasswordInput = page.getByLabel(`输入成员 ${fx.org_member_login} 的新密码`)
  await expect(reopenedPasswordInput).toHaveValue('')
  await expect(reopenedPasswordInput).toHaveAttribute('type', 'password')
  await page.getByRole('button', { name: '取消' }).click()
  await expect(reopenedPasswordInput).toBeHidden()

  expect(nativeDialogOpened).toBe(false)
})
