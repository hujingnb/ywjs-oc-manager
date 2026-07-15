import { execFileSync } from 'node:child_process'
import { randomUUID } from 'node:crypto'

import { expect, type Page } from '@playwright/test'

import { loadE2EFixture, loginAs } from '../fixtures'

const localK3DContext = 'k3d-ocm'

// assertLocalK3DContext 防止 E2E 中删除 AICC Pod 的恢复用例误连共享或生产集群。
export function assertLocalK3DContext(): void {
  const context = execFileSync('kubectl', ['config', 'current-context'], { encoding: 'utf8' }).trim()
  if (context !== localK3DContext) {
    throw new Error(`AICC E2E 只允许本地 ${localK3DContext}，当前 context 为 ${context || '(空)'}`)
  }
}

// deleteLocalAICCPod 删除当前测试智能体的本地 Pod，用于验证无状态 Pod 重建后的续聊。
// 函数先校验 context，再显式传入 context，避免 kubectl 默认上下文在测试期间被其他进程切换。
export function deleteLocalAICCPod(appID: string): void {
  assertLocalK3DContext()
  execFileSync('kubectl', [
    '--context', localK3DContext, '-n', 'oc-apps', 'delete', 'pod', '-l', `app=${appID}`, '--wait=true',
  ], { stdio: 'pipe' })
}

// assertNoUnauthorizedAICCSourceAudit 读取 manager 持久化的受信任工具来源审计。
// AICC 只会把受控 knowledge/web 工具的当前轮审计来源写入 aicc_message_sources；
// 因此操作性拒绝后该会话不应产生任何来源记录，避免模型文本伪造“已执行”的痕迹。
export function assertNoUnauthorizedAICCSourceAudit(sessionToken: string): void {
  assertLocalK3DContext()
  const escapedToken = sessionToken.replaceAll("'", "''")
  const count = execFileSync('kubectl', [
    '--context', localK3DContext, '-n', 'ocm', 'exec', 'mysql-0', '--', 'sh', '-c',
    `mysql -uroot -p"$MYSQL_ROOT_PASSWORD" ocm -N -e "SELECT COUNT(*) FROM aicc_message_sources src JOIN aicc_messages msg ON msg.id=src.message_id JOIN aicc_sessions s ON s.id=msg.session_id WHERE s.session_token='${escapedToken}'" 2>/dev/null`,
  ], { encoding: 'utf8' }).trim()
  if (count !== '0') {
    throw new Error(`操作性拒绝不应持久化任何受信任工具来源审计，实际记录数为 ${count || '(空)'}`)
  }
}

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

export interface AICCConversationFixture {
  id: string
  appID: string
  name: string
  publicToken: string
  widgetToken: string
}

// createStartedAICCConversationFixture 走真实管理页面创建并启动一名客服，供公开页 E2E 使用。
// 每个 spec 生成独立智能体和公开 token，避免并发或重复三轮运行共享会话、线索与审计数据。
export async function createStartedAICCConversationFixture(page: Page, prefix: string): Promise<AICCConversationFixture> {
  const fixture = loadE2EFixture()
  await forceZh(page)
  await loginAs(page, 'platform_admin', fixture, 'zh')
  await page.goto('/organizations')
  const orgRow = page.getByRole('row', { name: new RegExp(fixture.org_code) })
  await expect(orgRow).toBeVisible()
  await orgRow.getByRole('button', { name: '编辑' }).click()
  const enabledSwitch = page.locator('.n-form-item').filter({ hasText: '开通 AICC' }).getByRole('switch')
  if (await enabledSwitch.getAttribute('aria-checked') !== 'true') await enabledSwitch.click()
  const enabledSaved = page.waitForResponse(response => response.url().includes('/aicc-config') && response.request().method() === 'PATCH')
  await page.getByRole('button', { name: '保存 AICC 配置' }).click()
  expect((await enabledSaved).ok()).toBeTruthy()

  await clearLoginState(page)
  await loginAs(page, 'org_admin', fixture, 'zh')
  await openAICCConsole(page)
  await openAICCSettings(page)
  await page.getByRole('button', { name: '新建智能体' }).click()
  const name = `${prefix} ${Date.now()}`
  await page.getByPlaceholder('例如：售前咨询接待员').fill(name)
  const created = page.waitForResponse(response => response.url().includes('/api/v1/aicc/agents') && response.request().method() === 'POST')
  await page.getByRole('button', { name: '保存配置' }).click()
  const payload = await (await created).json() as { agent: { id: string, app_id: string, name: string, public_token: string, widget_token: string } }
  await waitForAICCRuntime(payload.agent.app_id)
  // 高意向动作只有配置了可提交字段时才渲染为公开页留资卡；在统一 fixture 中配置最小手机号字段，
  // 让意向场景验证真实的邀请/拒绝/合并链路，而不是因空表单静默跳过。
  await page.getByRole('button', { name: '添加字段' }).click()
  const leadField = page.locator('.lead-field-row').last()
  await leadField.getByPlaceholder('字段名称').fill('联系电话')
  await leadField.getByPlaceholder('字段 key').fill('contact_phone')
  const fieldsSaved = page.waitForResponse(response => response.url().includes('/lead-fields') && response.request().method() === 'PUT')
  await page.getByRole('button', { name: '保存留资字段' }).click()
  expect((await fieldsSaved).ok()).toBeTruthy()
  await page.getByRole('link', { name: '接待台', exact: true }).click()
  const started = page.waitForResponse(response => response.url().includes('/start') && response.request().method() === 'POST')
  await page.getByRole('button', { name: '启动接待' }).click()
  expect((await started).ok()).toBeTruthy()
  return { id: payload.agent.id, appID: payload.agent.app_id, name: payload.agent.name, publicToken: payload.agent.public_token, widgetToken: payload.agent.widget_token }
}

// sendPublicAICCMessage 只通过公开页面表单发言，并等待异步轮询得到实际客服回复。
// 使用可访问名称和 placeholder，不依赖 Vue/Naive UI 的内部 DOM 层级。
export async function sendPublicAICCMessage(page: Page, question: string): Promise<string> {
  await page.getByPlaceholder('输入您的问题').fill(question)
  await page.getByRole('button', { name: '发送' }).click()
  // 排队/处理中占位同样位于 bubble，必须排除 status，确保断言的是服务端完成的真实文本。
  const assistant = page.locator('.message-row.assistant .bubble p:not(.message-status)').last()
  await expect(assistant).toBeVisible({ timeout: 240_000 })
  return (await assistant.innerText()).trim()
}

// waitForAICCRuntime 等待异步创建的 hidden app Pod Ready，避免把初始化窗口误判为消息转发故障。
export async function waitForAICCRuntime(appId: string): Promise<void> {
  assertLocalK3DContext()
  await expect.poll(() => execFileSync(
    'kubectl',
    ['--context', localK3DContext, '-n', 'oc-apps', 'get', 'pods', '-l', `app=${appId}`, '-o', 'name'],
    { encoding: 'utf8' },
  ).trim(), { timeout: 180_000 }).not.toBe('')

  execFileSync(
    'kubectl',
    ['--context', localK3DContext, '-n', 'oc-apps', 'wait', '--for=condition=Ready', 'pod', '-l', `app=${appId}`, '--timeout=180s'],
    { stdio: 'pipe' },
  )

  await expect.poll(() => execFileSync(
    'kubectl',
    [
      '--context', localK3DContext, '-n', 'ocm', 'exec', 'mysql-0', '--', 'sh', '-c',
      `mysql -uroot -p"$MYSQL_ROOT_PASSWORD" ocm -N -e "SELECT runtime_phase FROM apps WHERE id='${appId}'" 2>/dev/null`,
    ],
    { encoding: 'utf8' },
  ).trim(), { timeout: 60_000 }).toBe('ready')
}

// seedAICCSessionsForPagination 为浏览器分页场景补充带消息的历史会话。
// 数据只写入 seed-e2e 创建的当前 agent/org，避免为翻页展示重复调用 Hermes 并拖慢整套回归。
export function seedAICCSessionsForPagination(agentId: string, orgId: string, count: number): void {
  const statements: string[] = []
  for (let index = 0; index < count; index += 1) {
    const sessionId = randomUUID()
    const messageId = randomUUID()
    const token = `e2e-page-${randomUUID()}`
    // 每条 fixture 都包含一条访客消息，符合后台“零消息会话不展示”的正式查询规则。
    statements.push(
      `INSERT INTO aicc_sessions (id, agent_id, org_id, session_token, channel, region, resolution_status, lead_status, expires_at, created_at) VALUES ('${sessionId}', '${agentId}', '${orgId}', '${token}', 'web_link', '本地网络', 'unknown', 'skipped', DATE_ADD(NOW(), INTERVAL 1 DAY), DATE_SUB(NOW(), INTERVAL ${index + 1} MINUTE));`,
      `INSERT INTO aicc_messages (id, session_id, agent_id, direction, content_type, text_content) VALUES ('${messageId}', '${sessionId}', '${agentId}', 'visitor', 'text', '分页验证消息 ${index + 1}');`,
    )
  }
  execFileSync(
    'kubectl',
    [
      '--context', localK3DContext, '-n', 'ocm', 'exec', 'mysql-0', '--', 'sh', '-c',
      `mysql -uroot -p"$MYSQL_ROOT_PASSWORD" ocm -e "${statements.join(' ')}" 2>/dev/null`,
    ],
    { stdio: 'pipe' },
  )
}
