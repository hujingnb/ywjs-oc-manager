import { expect, test } from '@playwright/test'

// Scenario 6：删除联动应用软删。覆盖 spec §5.4 Task 15 第六条。
//
// 验证点：删除成员 → 关联应用 status 立刻变 'deleted'；列表查询不再可见。
test.skip('删除成员后关联应用立即软删', async ({ page }) => {
  // 占位：实际 CI 接入时用 fixture 创建一个成员 + 应用
  const memberId = process.env.E2E_MEMBER_ID ?? '00000000-0000-0000-0000-000000000001'
  const appId = process.env.E2E_APP_ID ?? '00000000-0000-0000-0000-000000000001'
  await page.goto('/members')
  await page.getByRole('row', { name: new RegExp(memberId.slice(0, 8)) }).getByRole('button', { name: '删除' }).click()
  await page.getByRole('button', { name: /确认删除/ }).click()
  // 跳到应用详情，状态应立即变成 deleted
  await page.goto(`/apps/${appId}/overview`)
  await expect(page.getByText('已删除')).toBeVisible()
})
