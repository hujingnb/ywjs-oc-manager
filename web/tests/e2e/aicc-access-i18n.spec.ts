import type { Page } from '@playwright/test'

import { expect, test, type E2EFixture } from './fixtures'

import { clearLoginState, forceZh } from './aicc/helpers'
import { loginAs } from './fixtures'

// AICC 权限和国际化矩阵只依赖企业开通状态，不创建 runtime，避免把角色守卫测试绑定到容器启动耗时。
test.setTimeout(120_000)

// enableFixtureAICC 通过平台企业编辑页开通 fixture 企业，并保留企业列表入口所需的真实响应数据。
async function enableFixtureAICC(page: Page, fixture: E2EFixture): Promise<void> {
  await forceZh(page)
  await loginAs(page, 'platform_admin', fixture, 'zh')
  await page.evaluate(async (orgId: string) => {
    const readCookie = (name: string): string | null => {
      const target = `${name}=`
      for (const part of document.cookie.split(';')) {
        const trimmed = part.trim()
        if (trimmed.startsWith(target)) {
          return decodeURIComponent(trimmed.slice(target.length))
        }
      }
      return null
    }

    const token = window.localStorage.getItem('ocm.access_token')
    const headers: Record<string, string> = { Accept: 'application/json' }
    if (token) {
      headers.Authorization = `Bearer ${token}`
    }
    const csrf = readCookie('csrf_token')
    if (csrf) {
      headers['X-CSRF-Token'] = csrf
    }

    const response = await fetch(`/api/v1/organizations/${orgId}/aicc-config`, { headers })
    if (!response.ok) {
      throw new Error(`读取企业 AICC 配置失败: ${response.status}`)
    }
    const body = await response.json() as {
      config: {
        enabled: boolean
        model?: string
        agent_limit?: number
        industry_knowledge_bases: Array<{ id: string }>
      }
    }
    if (body.config.enabled) {
      return
    }

    const saveResponse = await fetch(`/api/v1/organizations/${orgId}/aicc-config`, {
      method: 'PUT',
      headers: {
        ...headers,
        'Content-Type': 'application/json',
      },
      body: JSON.stringify({
        enabled: true,
        model: body.config.model ?? 'deepseek-chat',
        agent_limit: body.config.agent_limit ?? null,
        industry_knowledge_base_ids: body.config.industry_knowledge_bases.map(item => item.id),
      }),
    })
    if (!saveResponse.ok) {
      throw new Error(`保存企业 AICC 配置失败: ${saveResponse.status} ${await saveResponse.text()}`)
    }
  }, fixture.org_id)
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
test('平台管理员可从企业列表进入指定 AICC 且不能新建智能体', async ({ page, e2eFixture }) => {
  await enableFixtureAICC(page, e2eFixture)
  await page.goto('/organizations')

  const row = page.getByRole('row', { name: new RegExp(e2eFixture.org_code) })
  await row.hover()
  await row.getByRole('button', { name: '进入 AICC' }).click()
  await expect(page).toHaveURL(new RegExp(`/aicc-console\\?org_id=${e2eFixture.org_id}`))
  await expect(page.getByRole('heading', { name: 'AICC 工作台' })).toBeVisible()
  await expect(page.locator('[data-test="org-switcher"]')).toBeVisible()
  await expect(page.getByRole('button', { name: '新建智能体' })).toHaveCount(0)
})

// 验证企业管理员入口和 AICC 六个子页同时接入项目统一中英文切换机制。
test('企业管理员从概览进入 AICC 并切换中英文子页面', async ({ page, e2eFixture }) => {
  await enableFixtureAICC(page, e2eFixture)
  await clearLoginState(page)
  await loginAs(page, 'org_admin', e2eFixture, 'zh')
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
test('企业普通成员无 AICC 入口且直接访问会被拒绝', async ({ page, e2eFixture }) => {
  await enableFixtureAICC(page, e2eFixture)
  await clearLoginState(page)
  await loginAs(page, 'org_member', e2eFixture, 'zh')

  await page.goto('/aicc-console')
  await expect(page).not.toHaveURL(/\/aicc-console/)
  await expect(page.getByRole('heading', { name: /AICC (工作台|Console)/ })).toHaveCount(0)
  await expect(page.getByRole('link', { name: /AICC (客服|Service)/ })).toHaveCount(0)
})
