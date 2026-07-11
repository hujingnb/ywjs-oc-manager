import { expect, test, type Page } from '@playwright/test'

import { clearLoginState, forceZh } from './aicc/helpers'
import { loadE2EFixture, loginAs } from './fixtures'

// AICC 权限和国际化矩阵只依赖企业开通状态，不创建 runtime，避免把角色守卫测试绑定到容器启动耗时。
test.setTimeout(120_000)

// enableFixtureAICC 通过平台企业编辑页开通 fixture 企业，并保留企业列表入口所需的真实响应数据。
async function enableFixtureAICC(page: Page): Promise<void> {
  const fx = loadE2EFixture()
  await forceZh(page)
  await loginAs(page, 'platform_admin', fx, 'zh')
  await page.goto('/organizations')
  const row = page.getByRole('row', { name: new RegExp(fx.org_code) })
  await row.getByRole('button', { name: '编辑' }).click()
  const enabled = page.locator('.n-form-item').filter({ hasText: '开通 AICC' }).getByRole('switch')
  if (await enabled.getAttribute('aria-checked') !== 'true') await enabled.click()
  const saved = page.waitForResponse(response =>
    response.url().includes(`/api/v1/organizations/${fx.org_id}/aicc-config`)
    && response.request().method() === 'PATCH',
  )
  await page.getByRole('button', { name: '保存 AICC 配置' }).click()
  expect((await saved).ok()).toBeTruthy()
  // 企业编辑抽屉保存配置后保持打开；关闭后才能真实点击列表行操作，避免覆盖层拦截指针事件。
  await page.getByRole('button', { name: '取消' }).click()
  await expect(row.getByRole('button', { name: '进入 AICC' })).toBeVisible()
}

// switchLocale 使用产品顶栏的统一语言选择器切换语言，并等待 AICC 标题完成响应式更新。
async function switchLocale(page: Page, locale: 'zh' | 'en'): Promise<void> {
  const option = locale === 'zh' ? '简体中文' : 'English'
  await page.getByRole('button', { name: /^(Language|语言|English|简体中文)$/ }).click()
  await page.locator('.n-dropdown-option', { hasText: option }).click()
  await expect(page.getByRole('heading', { name: locale === 'zh' ? 'AICC 工作台' : 'AICC Console' })).toBeVisible()
}

// assertConsoleNavigationLocale 逐项打开独立工作台子页，验证菜单选中态和核心标题均跟随当前语言。
async function assertConsoleNavigationLocale(page: Page, locale: 'zh' | 'en'): Promise<void> {
  const items = locale === 'zh'
    ? [
        { label: '接待台', path: '/aicc-console' },
        { label: '会话', path: '/aicc-console/sessions' },
        { label: '线索', path: '/aicc-console/leads' },
        { label: '知识库', path: '/aicc-console/knowledge' },
        { label: '分析', path: '/aicc-console/analytics' },
        { label: '设置', path: '/aicc-console/settings' },
      ]
    : [
        { label: 'Reception', path: '/aicc-console' },
        { label: 'Sessions', path: '/aicc-console/sessions' },
        { label: 'Leads', path: '/aicc-console/leads' },
        { label: 'Knowledge', path: '/aicc-console/knowledge' },
        { label: 'Analytics', path: '/aicc-console/analytics' },
        { label: 'Settings', path: '/aicc-console/settings' },
      ]

  for (const item of items) {
    // 每个子测试场景覆盖一个左侧模块：点击后 URL、选中态和可见菜单文案必须一致。
    const link = page.getByRole('link', { name: item.label, exact: true })
    await link.click()
    await expect(page).toHaveURL(new RegExp(`${item.path.replaceAll('/', '\\/')}(?:\\?|$)`))
    await expect(link).toHaveClass(/active/)
  }
}

// 验证平台管理员从企业列表进入指定企业，并保持只读工作台边界。
test('平台管理员可从企业列表进入指定 AICC 且不能新建智能体', async ({ page }) => {
  const fx = loadE2EFixture()
  await enableFixtureAICC(page)

  const row = page.getByRole('row', { name: new RegExp(fx.org_code) })
  await row.getByRole('button', { name: '进入 AICC' }).click()
  await expect(page).toHaveURL(new RegExp(`/aicc-console\\?org_id=${fx.org_id}`))
  await expect(page.getByRole('heading', { name: 'AICC 工作台' })).toBeVisible()
  await expect(page.locator('[data-test="org-switcher"]')).toBeVisible()
  await expect(page.getByRole('button', { name: '新建智能体' })).toHaveCount(0)
})

// 验证企业管理员入口和 AICC 六个子页同时接入项目统一中英文切换机制。
test('企业管理员从概览进入 AICC 并切换中英文子页面', async ({ page }) => {
  await enableFixtureAICC(page)
  await clearLoginState(page)
  await loginAs(page, 'org_admin', loadE2EFixture(), 'zh')
  await page.goto('/')

  const entry = page.getByRole('link', { name: /AICC 客服/ })
  await expect(entry).toBeVisible()
  await entry.click()
  await expect(page.getByRole('heading', { name: 'AICC 工作台' })).toBeVisible()
  await expect(page.locator('[data-test="org-switcher"]')).toHaveCount(0)
  await expect(page.getByRole('button', { name: '新建智能体' })).toBeVisible()
  await assertConsoleNavigationLocale(page, 'zh')

  await switchLocale(page, 'en')
  await assertConsoleNavigationLocale(page, 'en')
  await expect(page.getByRole('button', { name: 'New agent' })).toBeVisible()
})

// 验证企业普通成员既看不到子系统入口，也无法通过手工输入独立工作台路由绕过角色守卫。
test('企业普通成员无 AICC 入口且直接访问会被拒绝', async ({ page }) => {
  await enableFixtureAICC(page)
  await clearLoginState(page)
  await loginAs(page, 'org_member', loadE2EFixture(), 'zh')

  await page.goto('/aicc-console')
  await expect(page).not.toHaveURL(/\/aicc-console/)
  await expect(page.getByRole('heading', { name: /AICC (工作台|Console)/ })).toHaveCount(0)
  await expect(page.getByRole('link', { name: /AICC (客服|Service)/ })).toHaveCount(0)
})
