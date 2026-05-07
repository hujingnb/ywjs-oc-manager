import { expect, test } from '@playwright/test'

import { loginAs } from './fixtures'

// Scenario 3：platform_admin 注册节点；bootstrap_token 仅出现一次。
//
// 当前 UI 行为（web/src/pages/runtime-nodes/RuntimeNodesPage.vue）：
//   - 点击「注册节点」打开表单 → 填名称 → 点「保存」→
//     成功后顶部弹出第三个 panel 展示一次性 bootstrap token，标题文案是「Bootstrap Token」；
//   - 节点列表本身不会再展示该 token；离开页面再回来后该 panel 也消失。
test('注册节点后 bootstrap_token 仅出现一次', async ({ page }) => {
  await loginAs(page, 'platform_admin')
  await page.goto('/runtime-nodes')
  await page.getByRole('button', { name: '注册节点' }).click()
  const name = `e2e-node-${Date.now()}`
  // 表单字段是 <label><span>名称 *</span><input/></label>，accessible name 为「名称 *」。
  await page.getByLabel('名称 *').fill(name)
  await page.getByRole('button', { name: '保存' }).click()

  // 一次性 token 通过专属 panel 标题「Bootstrap Token」展示。
  const tokenPanel = page.getByText('Bootstrap Token', { exact: true })
  await expect(tokenPanel).toBeVisible()
  // 同时验证提示语包含「仅展示一次」字样，确保确实是 token 弹层而不是误命中。
  await expect(page.getByText(/仅展示一次/)).toBeVisible()

  // 离开页面再回来：token panel 不再显示。
  await page.goto('/')
  await page.goto('/runtime-nodes')
  await expect(page.getByText('Bootstrap Token', { exact: true })).toHaveCount(0)
})
