import { expect, test as base } from '@playwright/test'
import type { Page } from '@playwright/test'

import { parseE2EFixturePool, type E2EFixture } from './fixture-schema'
import { fixtureForWorker } from './suite'

// E2EFixture 从无 Playwright 依赖的 schema 模块转出，保持现有 spec 的类型导入兼容。
export type { E2EFixture } from './fixture-schema'

// E2EWorkerFixtures 声明 worker 级 fixture，确保同一 worker 内复用且不同 worker 间隔离。
type E2EWorkerFixtures = {
  // e2eFixture 是由 parallelIndex 唯一选择的当前 worker 数据。
  e2eFixture: E2EFixture
}

// test 扩展 Playwright 基础 fixture，在 worker 启动时解析 globalSetup 注入的完整 pool。
export const test = base.extend<{}, E2EWorkerFixtures>({
  e2eFixture: [async ({}, use, workerInfo) => {
    const raw = process.env.OCM_E2E_FIXTURE_POOL
    if (!raw) {
      throw new Error('OCM_E2E_FIXTURE_POOL 未注入；确保 globalSetup 已生成 fixture pool')
    }

    const pool = parseE2EFixturePool(raw)
    const fixture = fixtureForWorker(pool, workerInfo.parallelIndex)
    await use(fixture)
  }, { scope: 'worker' }],
})

export { expect }

// loadE2EFixture 仅暂时保留导出，让尚未在 Task 6/7 迁移的旧 spec 继续通过类型检查。
// 函数没有 workerInfo，无法安全确定 parallelIndex，因此禁止读取环境或猜测当前 worker。
export function loadE2EFixture(): E2EFixture {
  throw new Error('loadE2EFixture 已停用；请使用 Playwright 注入的 e2eFixture')
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
