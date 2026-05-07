import { expect, test } from '@playwright/test'

import { loadE2EFixture, loginAs } from './fixtures'

// Scenario 6：删除应用强校验。覆盖 spec §5.4 Task 15 第六条 + T1 ConfirmActionModal。
//
// 验证项（UI 层）：
//   1. 应用列表渲染 fixture 应用；点击「删除」打开 modal；
//   2. 输错应用名 → 「确认删除」按钮 disabled（T1 强校验）；
//   3. 输对应用名 → 按钮 enabled，点击后触发 POST /runtime/delete；
//   4. 点击后 modal 关闭（toDelete 复位）。
//
// 不断言列表 row 立即消失或 status 变 'deleted'：
//   - 后端 runtime_operation_service 在 audit_logs result 字段写入 'submitted'，但
//     audit_logs.result CHECK 仅允许 succeeded/failed，导致整个请求 500（pre-existing
//     backend bug，与 T2b 范围无关）。
//   - 即便 audit 修好，handler 链上的 fileOps 还需要真实 agent，而 fixture node 指向
//     127.0.0.1:9999 的 dummy endpoint；ArchiveApp / DeleteAppPath 必然失败。
//
// 因此把「应用最终软删」断言交给 worker / service 单元测试，UI 端仅确认强校验和触发链路。
test('删除应用：输错名拒绝，输对名后触发删除请求', async ({ page }) => {
  const fx = loadE2EFixture()
  await loginAs(page, 'org_admin', fx)
  await page.goto('/apps')

  const row = page.getByRole('row', { name: new RegExp(fx.app_name) })
  await expect(row).toBeVisible()
  await row.getByRole('button', { name: '删除' }).click()

  // 强校验：先输错名，按钮 disabled。
  const verifyInput = page.locator('.modal-card .verify-input')
  await verifyInput.fill('wrong-name')
  await expect(page.getByRole('button', { name: '确认删除' })).toBeDisabled()

  // 输对名：按钮 enabled。
  await verifyInput.fill(fx.app_name)
  await expect(page.getByRole('button', { name: '确认删除' })).toBeEnabled()

  // 监听 /runtime/delete 请求被发送；不校验 status code，理由见上方注释。
  const deleteRequestPromise = page.waitForRequest(
    (req) => req.url().includes(`/api/v1/apps/${fx.app_id}/runtime/delete`) && req.method() === 'POST',
  )
  await page.getByRole('button', { name: '确认删除' }).click()
  await deleteRequestPromise

  // modal 关闭：verify-input DOM 不再存在（toDelete 已被 finally 块清空）。
  await expect(page.locator('.modal-card .verify-input')).toHaveCount(0)
})
