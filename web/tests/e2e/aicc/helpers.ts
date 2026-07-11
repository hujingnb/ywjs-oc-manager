import { execFileSync } from 'node:child_process'

import { expect, type Page } from '@playwright/test'

// forceZh 在页面初始化前固定中文界面，避免平台默认语言差异影响可见文案定位。
export async function forceZh(page: Page): Promise<void> {
  await page.addInitScript(() => {
    window.localStorage.setItem('ocm.locale', 'zh')
  })
}

// clearLoginState 清理当前浏览器页的登录态，用同一个 page 串联不同角色流程。
export async function clearLoginState(page: Page): Promise<void> {
  await page.evaluate(() => {
    window.localStorage.removeItem('ocm.access_token')
    window.localStorage.removeItem('ocm.refresh_token')
    window.localStorage.setItem('ocm.locale', 'zh')
  })
  await page.context().clearCookies()
}

// openAICCConsole 通过最终独立路由进入工作台，并等待工作台上下文加载完成。
export async function openAICCConsole(page: Page): Promise<void> {
  await page.goto('/aicc-console')
  await expect(page.getByRole('heading', { name: 'AICC 工作台' })).toBeVisible()
}

// openAICCSettings 打开当前智能体的独立设置页，避免测试依赖已移除的内容区标签页。
export async function openAICCSettings(page: Page): Promise<void> {
  await page.getByRole('link', { name: '设置', exact: true }).click()
  await expect(page).toHaveURL(/\/aicc-console\/settings/)
  await expect(page.getByRole('heading', { name: '设置', exact: true })).toBeVisible()
}

// waitForAICCRuntime 等待异步创建的 hidden app Pod Ready，避免把初始化窗口误判为消息转发故障。
export async function waitForAICCRuntime(appId: string): Promise<void> {
  await expect.poll(() => execFileSync(
    'kubectl',
    ['-n', 'oc-apps', 'get', 'pods', '-l', `app=${appId}`, '-o', 'name'],
    { encoding: 'utf8' },
  ).trim(), { timeout: 60_000 }).not.toBe('')

  execFileSync(
    'kubectl',
    ['-n', 'oc-apps', 'wait', '--for=condition=Ready', 'pod', '-l', `app=${appId}`, '--timeout=180s'],
    { stdio: 'pipe' },
  )

  await expect.poll(() => execFileSync(
    'kubectl',
    [
      '-n', 'ocm', 'exec', 'mysql-0', '--', 'sh', '-c',
      `mysql -uroot -p"$MYSQL_ROOT_PASSWORD" ocm -N -e "SELECT runtime_phase FROM apps WHERE id='${appId}'" 2>/dev/null`,
    ],
    { encoding: 'utf8' },
  ).trim(), { timeout: 60_000 }).toBe('ready')
}
