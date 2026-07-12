import type { Page } from '@playwright/test'

// E2EFixture 与 cmd/seed-e2e 输出 JSON 字段保持一致。
// 注意：org_id / node_id / app_id 均为数据库 UUID，schema 决定不能用 number。
export type E2EFixture = {
  platform_admin_login: string
  platform_admin_password: string
  org_id: string
  org_name: string
  org_code: string
  node_id: string
  node_name: string
  org_admin_login: string
  org_admin_password: string
  org_member_login: string
  org_member_password: string
  app_id: string
  app_name: string
}

// loadE2EFixture 读取 globalSetup 注入的 OCM_E2E_FIXTURE 环境变量。
// 缺失时直接抛错，避免 spec 静默跑空数据。
export function loadE2EFixture(): E2EFixture {
  const raw = process.env.OCM_E2E_FIXTURE
  if (!raw) {
    throw new Error('OCM_E2E_FIXTURE 未注入；确保 globalSetup 跑过 make seed-e2e')
  }
  return JSON.parse(raw) as E2EFixture
}

// loginAs 按角色完成登录，等到不再停留在 /login 即认为登录成功。
// 不强制断言到具体首页路径，因为不同角色 RoleAwareHome 落点一致（"/"）。
export async function loginAs(
  page: Page,
  role: 'platform_admin' | 'org_admin' | 'org_member',
  fx: E2EFixture = loadE2EFixture(),
  locale?: 'zh' | 'en',
): Promise<void> {
  const credential = {
    platform_admin: { u: fx.platform_admin_login, p: fx.platform_admin_password },
    org_admin: { u: fx.org_admin_login, p: fx.org_admin_password },
    org_member: { u: fx.org_member_login, p: fx.org_member_password },
  }[role]
  await page.goto('/login')
  // 登录页默认语言为 en（DEFAULT_LOCALE），但本地/CI 平台默认可能配成 zh；
  // 字段标签随语言变化，故用「中文|英文」双语锚定正则匹配，避免 loginAs 绑死某一语言。
  // 锚定 ^...$ 防止「密码」误匹配「显示密码」按钮的 aria-label。
  if (role !== 'platform_admin') {
    await page.getByLabel(/^(企业标识|Organization Code)$/).fill(fx.org_code)
  }
  await page.getByLabel(/^(账号|Username|Account)$/).fill(credential.u)
  await page.getByLabel(/^(密码|Password)$/).fill(credential.p)
  await page.getByRole('button', { name: /^(登录|Log in)$/ }).click()
  await page.waitForURL((url) => !url.pathname.startsWith('/login'))
  // 登录完成后后端会以用户偏好覆盖登录前 localStorage；测试需要固定文案时，
  // 必须通过真实语言切换器再次写入用户偏好，避免用例顺序改变后定位器失效。
  if (locale) {
    const current = locale === 'zh' ? '简体中文' : 'English'
    const switcher = page.getByRole('button', { name: /^(Language|语言|English|简体中文)$/ })
    if (await switcher.getByText(current, { exact: true }).count() === 0) {
      await switcher.click()
      await page.locator('.n-dropdown-option', { hasText: current }).click()
    }
  }
}
