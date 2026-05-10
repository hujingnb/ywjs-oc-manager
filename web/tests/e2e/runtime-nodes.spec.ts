import { expect, test } from '@playwright/test'

import { loginAs } from './fixtures'

// Scenario 3：platform_admin 查看自动注册的 runtime node。
//
// runtime-agent 现在启动后使用 enrollment secret 自动注册；后台不再提供手动创建节点、
// bootstrap token 或 rotate bootstrap 的入口。
test('运行节点页面展示自动注册节点且没有手动注册入口', async ({ page }) => {
  await loginAs(page, 'platform_admin')
  await page.goto('/runtime-nodes')

  await expect(page.getByText(/runtime-agent 启动后会使用 enrollment secret 自动注册到 manager/)).toBeVisible()
  await expect(page.getByRole('button', { name: '注册节点' })).toHaveCount(0)
  await expect(page.getByText('Bootstrap Token', { exact: true })).toHaveCount(0)
  await expect(page.getByText('e2e-node')).toBeVisible()
})
