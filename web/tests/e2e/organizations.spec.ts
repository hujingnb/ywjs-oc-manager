import { expect, test } from './fixtures'

// Scenario 2：platform_admin 创建组织。覆盖 spec §5.4 Task 15 第二条。
//
// 关键路径：登录 → /organizations → 点击「新增组织」打开表单 →
// 填写唯一名称和初始管理员 → 点「保存」→ 列表里应能看到新组织名。
test('platform_admin 可创建组织', { tag: '@quick' }, async ({ platformAdminPage: page, e2eFixture: fx }) => {
  await page.goto('/organizations')
  await page.getByRole('button', { name: '新增组织' }).click()
  // worker 前缀与短时间片共同隔离并发创建；名称保持在 new-api display_name 的 20 字符限制内。
  const unique = `${fx.worker_index}${Date.now().toString(36).slice(-5)}`
  const name = `e2e-${unique}-org`
  // 派生 code 保留 fixture owning run 前缀，使 cleanup-e2e 的精确正则能够回收本场景数据。
  const code = `${fx.org_code}-c-${unique}`
  const adminLogin = `admin-${unique}`
  // Naive UI 的表单项标题不直接作为 input label，使用稳定的 placeholder 定位输入框。
  await page.getByPlaceholder('组织名称').fill(name)
  await page.getByPlaceholder('test-org').fill(code)
  await page.getByPlaceholder('登录账号').fill(adminLogin)
  await page.getByPlaceholder('管理员姓名').fill('E2E 管理员')
  await page.getByPlaceholder('初始登录密码').fill('secret-password')
  await page.getByRole('button', { name: '保存' }).click()
  // 列表展示新组织行；名称和组织标识相同，因此断言整行避免 strict mode 匹配到两个单元格。
  await expect(page.getByRole('row', { name: new RegExp(name) })).toBeVisible()
})
