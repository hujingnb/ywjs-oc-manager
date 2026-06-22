import { expect, test } from '@playwright/test'

import { loadE2EFixture, loginAs } from './fixtures'

// locale.spec.ts：覆盖语言切换主链路的 E2E 用例。
//
// 前置：make local-up（k3d 全栈）+ npm run dev。
// 无后端时，登录页相关断言仍可独立运行（不依赖 fixture），
// 登录后断言通过 test.skip 条件跳过，避免误报失败。
//
// 运行：npm run test:e2e -- locale.spec.ts

// ─── 登录页：语言选择器出现 ────────────────────────────────────────────────

test('登录页右上角渲染语言选择器', async ({ page }) => {
  // 导航到登录页，不依赖后端数据，只验证 LocaleSwitcher 组件是否挂载。
  await page.goto('/login')

  // LocaleSwitcher 渲染一个带 aria-label="Language"（英文默认）或"语言"（中文）的 n-button。
  // 用 getByRole 按 aria-label 精确匹配，鲁棒性优于 class/text 选择器。
  const switcher = page.getByRole('button', { name: /^(Language|语言)$/ })
  await expect(switcher).toBeVisible()
})

// ─── 登录页：切换到中文后 localStorage 写入 ───────────────────────────────

test('登录页切换到中文后 localStorage ocm.locale 写入 zh', async ({ page }) => {
  // 进入登录页；默认语言为英文（DEFAULT_LOCALE = 'en'）。
  await page.goto('/login')

  // 等待语言选择器出现，当前标签应为英文自报名"English"。
  const switcher = page.getByRole('button', { name: /^(Language|语言|English|简体中文)$/ })
  await expect(switcher).toBeVisible()

  // 点击选择器按钮，展开 n-dropdown 下拉列表。
  await switcher.click()

  // n-dropdown 选项通过 document body 附加，label 为各语言自报名（common.languageName）。
  // "简体中文"是中文选项的自报名，始终为中文字符，与 UI 当前语言无关。
  await page.getByRole('option', { name: '简体中文' }).click()

  // 切换后 store.apply() 写入 localStorage；用 page.evaluate 跨框架读取并断言。
  const stored = await page.evaluate(() => localStorage.getItem('ocm.locale'))
  expect(stored).toBe('zh')
})

// ─── 登录页：切换到中文后 naive-ui 语言联动 ───────────────────────────────

test('登录页切换语言后语言选择器按钮文案跟随更新', async ({ page }) => {
  // 先切到中文，再切回英文，验证按钮标签跟随 i18n 更新。
  await page.goto('/login')

  // 等待初始选择器出现（英文默认）。
  const switcher = page.getByRole('button', { name: /^(Language|语言|English|简体中文)$/ })
  await expect(switcher).toBeVisible()

  // 第一步：切换到中文。
  await switcher.click()
  await page.getByRole('option', { name: '简体中文' }).click()

  // 切换后按钮自报名应变为"简体中文"，同时 aria-label 变为"语言"。
  // 用 getByLabel 精确断言 aria-label，与 currentLabel 联动一起验证。
  await expect(page.getByRole('button', { name: '语言' })).toBeVisible()

  // 第二步：切回英文，验证双向切换均正常。
  await page.getByRole('button', { name: '语言' }).click()
  await page.getByRole('option', { name: 'English' }).click()

  // 切回英文后按钮标签恢复为"Language"。
  await expect(page.getByRole('button', { name: 'Language' })).toBeVisible()

  // localStorage 也应回到'en'。
  const stored = await page.evaluate(() => localStorage.getItem('ocm.locale'))
  expect(stored).toBe('en')
})

// ─── 登录后：顶栏切换语言并刷新保持 ─────────────────────────────────────

test('登录后顶栏切换到英文刷新后仍为英文（localStorage 持久化）', async ({ page }) => {
  // 本用例依赖后端和 seed-e2e fixture；无后端时跳过，避免误报。
  let fx: ReturnType<typeof loadE2EFixture>
  try {
    fx = loadE2EFixture()
  } catch {
    // OCM_E2E_FIXTURE 未注入说明 globalSetup 未能跑 seed-e2e（无后端），直接跳过。
    test.skip(true, '无后端 fixture，跳过登录后断言')
    return
  }

  // 以平台管理员身份登录，进入 Dashboard。
  await loginAs(page, 'platform_admin', fx)

  // 等待顶栏稳定后找到顶栏中的 LocaleSwitcher（persist=true）。
  // 顶栏选择器与登录页同一组件，同一 aria-label 模式。
  const topbarSwitcher = page.getByRole('button', { name: /^(Language|语言|English|简体中文)$/ })
  await expect(topbarSwitcher).toBeVisible()

  // 先切换到中文，保证后续切回英文时有对比。
  await topbarSwitcher.click()
  await page.getByRole('option', { name: '简体中文' }).click()

  // 确认已切到中文。
  await expect(page.getByRole('button', { name: '语言' })).toBeVisible()

  // 再切回英文。
  await page.getByRole('button', { name: '语言' }).click()
  await page.getByRole('option', { name: 'English' }).click()

  // 断言 localStorage 写入。
  const beforeReload = await page.evaluate(() => localStorage.getItem('ocm.locale'))
  expect(beforeReload).toBe('en')

  // 刷新页面，验证 localeStore.init() 从 localStorage 恢复并保持英文。
  await page.reload()

  // 顶栏选择器重新出现且显示英文标签（Language）。
  await expect(page.getByRole('button', { name: 'Language' })).toBeVisible()

  // localStorage 刷新后仍为'en'。
  const afterReload = await page.evaluate(() => localStorage.getItem('ocm.locale'))
  expect(afterReload).toBe('en')
})

// ─── 登录后：退出并重新登录，语言 DB 持久化跟随用户 ─────────────────────

test('顶栏切换语言后退出重登仍保持（DB 持久化）', async ({ page }) => {
  // 同上，无后端 fixture 时跳过。
  let fx: ReturnType<typeof loadE2EFixture>
  try {
    fx = loadE2EFixture()
  } catch {
    test.skip(true, '无后端 fixture，跳过 DB 持久化断言')
    return
  }

  // 登录后切换到中文（persist=true 会 PATCH /api/v1/auth/me/locale → DB 写 zh）。
  await loginAs(page, 'platform_admin', fx)

  const topbarSwitcher = page.getByRole('button', { name: /^(Language|语言|English|简体中文)$/ })
  await expect(topbarSwitcher).toBeVisible()

  await topbarSwitcher.click()
  await page.getByRole('option', { name: '简体中文' }).click()
  await expect(page.getByRole('button', { name: '语言' })).toBeVisible()

  // 清除 localStorage，模拟全新浏览器打开（只保留 DB 端的 locale）。
  await page.evaluate(() => localStorage.removeItem('ocm.locale'))

  // 退出登录。
  await page.getByRole('button', { name: '退出' }).click()
  await page.waitForURL(/\/login/)

  // 重新登录，localeStore.init() 会从 GET /api/v1/config 获取默认，
  // 登录成功后 applyFromUser(user.locale='zh') 覆盖为中文。
  await loginAs(page, 'platform_admin', fx)

  // 登录后顶栏语言应为中文（来自 DB user.locale='zh'）。
  await expect(page.getByRole('button', { name: '语言' })).toBeVisible()

  // 恢复为英文，避免影响后续用例。
  await page.getByRole('button', { name: '语言' }).click()
  await page.getByRole('option', { name: 'English' }).click()
})
