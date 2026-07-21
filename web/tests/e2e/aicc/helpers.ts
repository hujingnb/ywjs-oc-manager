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
    '--context', localK3DContext, '-n', 'oc-aicc', 'delete', 'pod', '-l', `app=${appID}`, '--wait=true',
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

// assertAICCSessionChannel 从 manager 会话事实校验公开入口渠道，避免只凭 iframe URL 推断服务端是否按挂件入库。
export function assertAICCSessionChannel(sessionToken: string, expectedChannel: 'web_link' | 'web_widget'): void {
  assertLocalK3DContext()
  const escapedToken = sessionToken.replaceAll("'", "''")
  const channel = execFileSync('kubectl', [
    '--context', localK3DContext, '-n', 'ocm', 'exec', 'mysql-0', '--', 'sh', '-c',
    `mysql -uroot -p"$MYSQL_ROOT_PASSWORD" ocm -N -e "SELECT channel FROM aicc_sessions WHERE session_token='${escapedToken}'" 2>/dev/null`,
  ], { encoding: 'utf8' }).trim()
  if (channel !== expectedChannel) throw new Error(`AICC session 渠道应为 ${expectedChannel}，实际为 ${channel || '(空)'}`)
}

// countAICCIntentAnalysisRetries 读取当前 session 的重试事实，用于验证一次失败确实持久化、恢复后又被清理。
export function countAICCIntentAnalysisRetries(sessionToken: string): number {
  assertLocalK3DContext()
  const escapedToken = sessionToken.replaceAll("'", "''")
  const result = execFileSync('kubectl', [
    '--context', localK3DContext, '-n', 'ocm', 'exec', 'mysql-0', '--', 'sh', '-c',
    `mysql -uroot -p"$MYSQL_ROOT_PASSWORD" ocm -N -e "SELECT COUNT(*) FROM aicc_intent_analysis_retries r JOIN aicc_sessions s ON s.id=r.session_id WHERE s.session_token='${escapedToken}'" 2>/dev/null`,
  ], { encoding: 'utf8' }).trim()
  return Number(result)
}

// assertAICCResolutionStatus 从会话事实核对访客动作后的状态，避免只凭前端卡片收起推断写入成功。
export function assertAICCResolutionStatus(sessionToken: string, expected: 'resolved' | 'unresolved' | 'unknown'): void {
  assertLocalK3DContext()
  const escapedToken = sessionToken.replaceAll("'", "''")
  const status = execFileSync('kubectl', [
    '--context', localK3DContext, '-n', 'ocm', 'exec', 'mysql-0', '--', 'sh', '-c',
    `mysql -uroot -p"$MYSQL_ROOT_PASSWORD" ocm -N -e "SELECT resolution_status FROM aicc_sessions WHERE session_token='${escapedToken}'" 2>/dev/null`,
  ], { encoding: 'utf8' }).trim()
  if (status !== expected) throw new Error(`AICC session 状态应为 ${expected}，实际为 ${status || '(空)'}`)
}

// setLocalAICCIntentFailureOnce 仅在 k3d 本地 manager-api 上启停一次性分析失败注入器。
// 该变量由 server 入口额外要求 app.env=local，测试结束必须清除并等待滚动完成，避免污染后续场景。
export function setLocalAICCIntentFailureOnce(enabled: boolean): void {
  assertLocalK3DContext()
  const values = enabled
    ? ['OCM_AICC_TEST_FAIL_INTENT_ONCE=1', 'OCM_AICC_TEST_PAUSE_INTENT_RETRIES=1']
    : ['OCM_AICC_TEST_FAIL_INTENT_ONCE-', 'OCM_AICC_TEST_PAUSE_INTENT_RETRIES-']
  execFileSync('kubectl', [
    '--context', localK3DContext, '-n', 'ocm', 'set', 'env', 'deployment/manager-api', ...values,
  ], { stdio: 'pipe' })
  execFileSync('kubectl', [
    '--context', localK3DContext, '-n', 'ocm', 'rollout', 'status', 'deployment/manager-api', '--timeout=180s',
  ], { stdio: 'pipe' })
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

export interface AICCAgentFixture {
  id: string
  app_id: string
  name: string
  public_token: string
  widget_token: string
}

// openFixtureOrganizationAICCConfig 以平台管理员身份打开 fixture 企业的独立 AICC 配置表单。
// 模型目录与独立配置会异步加载，调用方只能在该页面的可见控件上继续操作。
async function openFixtureOrganizationAICCConfig(page: Page): Promise<void> {
  const fixture = loadE2EFixture()
  await forceZh(page)
  await loginAs(page, 'platform_admin', fixture, 'zh')
  await page.goto('/organizations')
  const orgRow = page.getByRole('row', { name: new RegExp(fixture.org_code) })
  await expect(orgRow).toBeVisible()
  await orgRow.getByRole('button', { name: /^(编辑|Edit)$/ }).click()
  await expect(page.getByText('客服模型', { exact: true })).toBeVisible()
}

// setAICCConfigForFixtureOrg 通过真实平台页面保存开关、配额和指定客服模型，并返回成功写入的模型。
export async function setAICCConfigForFixtureOrg(
  page: Page,
  enabled: boolean,
  agentLimit: number,
  requestedModel?: string,
): Promise<string> {
  await openFixtureOrganizationAICCConfig(page)
  const aiccSwitch = page.locator('.n-form-item').filter({ hasText: '开通 AICC' }).getByRole('switch')
  if ((await aiccSwitch.getAttribute('aria-checked') === 'true') !== enabled) await aiccSwitch.click()
  await page.locator('.n-form-item').filter({ hasText: 'AICC 智能体数量上限' }).locator('input').fill(String(agentLimit))

  const modelField = page.locator('.n-form-item').filter({ hasText: '客服模型' })
  const modelSelect = modelField.locator('.n-base-selection')
  await expect(modelSelect).toBeVisible()
  if (requestedModel) {
    await modelSelect.click()
    await page.locator('.n-base-select-option', { hasText: requestedModel }).click()
  }
  const selectedModel = (await modelSelect.innerText()).trim()
  if (!selectedModel) throw new Error('fixture 企业未加载可用客服模型，无法保存 AICC 配置')

  const configSaved = page.waitForResponse(response => response.url().includes('/aicc-config') && response.request().method() === 'PUT')
  await page.getByRole('button', { name: '保存 AICC 配置' }).click()
  const response = await configSaved
  expect(response.ok()).toBeTruthy()
  const payload = await response.json() as { config?: { model?: string } }
  const model = payload.config?.model
  if (!model) throw new Error('AICC 配置保存成功但响应未返回客服模型')
  return model
}

// changeAICCModelToAnotherAvailableOption 真实确认换模影响，并返回与原配置不同的已保存模型。
// 当本地模型目录不足两个时立即给出可诊断错误，避免把环境缺口伪装成静默重启失败。
export async function changeAICCModelToAnotherAvailableOption(page: Page): Promise<string> {
  await openFixtureOrganizationAICCConfig(page)
  const modelField = page.locator('.n-form-item').filter({ hasText: '客服模型' })
  const modelSelect = modelField.locator('.n-base-selection')
  const currentModel = (await modelSelect.innerText()).trim()
  await modelSelect.click()
  // Naive UI 下拉项没有 ARIA option 角色，必须使用组件公开 class；getByRole('option')
  // 会得到空集合并把“模型不足”误报为环境问题。
  const options = page.locator('.n-base-select-option')
  const availableModels = await options.allTextContents()
  const nextModel = availableModels.map(model => model.trim()).find(model => model && model !== currentModel)
  if (!nextModel) throw new Error(`本地 AICC 模型目录至少需要两个可选模型；当前仅检测到 ${currentModel || '0 个'}`)
  await page.locator('.n-base-select-option', { hasText: nextModel }).click()

  const configSaved = page.waitForResponse(response => response.url().includes('/aicc-config') && response.request().method() === 'PUT')
  await page.getByRole('button', { name: '保存 AICC 配置' }).click()
  // Naive UI 的对话框没有把标题关联为 accessible name；保留 dialog 语义并按可见确认文案缩小范围。
  const confirmDialog = page.getByRole('dialog').filter({ hasText: '确认更换客服模型' })
  await expect(confirmDialog).toContainText('逐个静默重启')
  await confirmDialog.getByRole('button', { name: '确认更换' }).click()
  const response = await configSaved
  expect(response.ok()).toBeTruthy()
  const payload = await response.json() as { config?: { model?: string } }
  const model = payload.config?.model
  if (!model || model === currentModel) throw new Error('客服模型切换未返回不同的新模型')
  return model
}

// createAICCAgentAsOrgAdmin 以企业管理员身份创建客服并保存可选人设，确认界面不暴露普通助手版本或智能路由。
export async function createAICCAgentAsOrgAdmin(page: Page, persona?: string): Promise<AICCAgentFixture> {
  const fixture = loadE2EFixture()
  await forceZh(page)
  await loginAs(page, 'org_admin', fixture, 'zh')
  await openAICCConsole(page)
  await openAICCSettings(page)
  await page.getByRole('button', { name: '新建智能体' }).click()
  await expect(page.getByText('助手版本', { exact: true })).toHaveCount(0)
  await expect(page.getByText('智能路由', { exact: true })).toHaveCount(0)
  const name = `E2E 接待员 ${Date.now()}`
  await page.getByPlaceholder('例如：售前咨询接待员').fill(name)
  if (persona) await page.locator('#aicc-persona').fill(persona)
  const createdResponse = page.waitForResponse(response => response.url().includes('/api/v1/aicc/agents') && response.request().method() === 'POST')
  await page.getByRole('button', { name: '保存配置' }).click()
  const created = await createdResponse
  expect(created.ok()).toBeTruthy()
  const payload = await created.json() as { agent: AICCAgentFixture }
  await waitForAICCRuntime(payload.agent.app_id)
  await expect(page.getByRole('region', { name: '当前智能体' })).toContainText(name)
  await page.getByRole('link', { name: '接待台', exact: true }).click()
  await expect(page.locator('.public-link-box').locator('input')).toHaveValue(/\/aicc\/[A-Za-z0-9_-]+/)
  return payload.agent
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
  // 意向场景把单测总时限放宽到 10 分钟以覆盖真实模型回复；这里仍须保留页面跳转的独立上限，
  // 否则路由链接因状态切换暂时不可点击时会把整套回归无诊断地卡满 10 分钟。
  await page.getByRole('link', { name: '接待台', exact: true }).click({ timeout: 30_000 })
  await expect(page.getByRole('button', { name: '启动接待' })).toBeVisible({ timeout: 30_000 })
  const started = page.waitForResponse(response => response.url().includes('/start') && response.request().method() === 'POST')
  await page.getByRole('button', { name: '启动接待' }).click()
  expect((await started).ok()).toBeTruthy()
  return { id: payload.agent.id, appID: payload.agent.app_id, name: payload.agent.name, publicToken: payload.agent.public_token, widgetToken: payload.agent.widget_token }
}

// sendPublicAICCMessage 只通过公开页面表单发言，并等待异步轮询得到实际客服回复。
// 使用可访问名称和 placeholder，不依赖 Vue/Naive UI 的内部 DOM 层级。
export async function sendPublicAICCMessage(page: Page, question: string): Promise<string> {
	// 公开页初始即有欢迎语；记录发送前的助手正文数量，避免把它误判为本轮异步任务的完成回复。
	const assistantMessages = page.locator('.message-row.assistant .bubble p:not(.message-status)')
	const previousAssistantCount = await assistantMessages.count()
	await page.getByPlaceholder('输入您的问题').fill(question)
	await page.getByRole('button', { name: '发送' }).click()
	// 排队/处理中占位同样位于 bubble，必须排除 status；数量增长才表示服务端完成了本轮真实回复。
	await expect(assistantMessages).toHaveCount(previousAssistantCount + 1, { timeout: 240_000 })
	const assistant = assistantMessages.last()
  await expect(assistant).toBeVisible({ timeout: 240_000 })
  return (await assistant.innerText()).trim()
}

// waitForAICCRuntime 等待异步创建的 hidden app Pod Ready，避免把初始化窗口误判为消息转发故障。
export async function waitForAICCRuntime(appId: string): Promise<void> {
  assertLocalK3DContext()
  await expect.poll(() => execFileSync(
    'kubectl',
    ['--context', localK3DContext, '-n', 'oc-aicc', 'get', 'pods', '-l', `app=${appId}`, '-o', 'name'],
    { encoding: 'utf8' },
  ).trim(), { timeout: 180_000 }).not.toBe('')

  // 不使用 kubectl wait：删除重建窗口内 selector 可能短暂同时命中 Terminating 与新 Pod，
  // kubectl wait 会继续等待已终止副本，掩盖新运行时已经就绪的真实状态。
  await expect.poll(() => execFileSync(
    'kubectl',
    ['--context', localK3DContext, '-n', 'oc-aicc', 'get', 'pods', '-l', `app=${appId}`, '-o', 'jsonpath={range .items[*]}{.status.conditions[?(@.type=="Ready")].status}{","}{end}'],
    { encoding: 'utf8' },
  ).trim(), { timeout: 180_000 }).toMatch(/^True,$/)

  await expect.poll(() => execFileSync(
    'kubectl',
    [
      '--context', localK3DContext, '-n', 'ocm', 'exec', 'mysql-0', '--', 'sh', '-c',
      `mysql -uroot -p"$MYSQL_ROOT_PASSWORD" ocm -N -e "SELECT runtime_phase FROM apps WHERE id='${appId}'" 2>/dev/null`,
    ],
    { encoding: 'utf8' },
  // Pod Ready 与 manager 的状态收敛由独立的 leader worker 执行；在镜像重建或
  // 控制器切主期间，数据库 runtime_phase 可能晚于 Kubernetes Ready 一个轮询周期。
  // 与 Pod 就绪窗口保持一致，避免真实运行时已可用时把 E2E 误报为启动失败。
  ).trim(), { timeout: 180_000 }).toBe('ready')
}

// waitForAICCModelRollout 等待模型变更产生的最新 rollout Job 成功；app_id 反查企业可避免测试把并行企业的任务当作当前客服完成。
export async function waitForAICCModelRollout(appId: string): Promise<void> {
  assertLocalK3DContext()
  const escapedAppID = appId.replaceAll("'", "''")
  await expect.poll(() => execFileSync(
    'kubectl',
    [
      '--context', localK3DContext, '-n', 'ocm', 'exec', 'mysql-0', '--', 'sh', '-c',
      `mysql -uroot -p"$MYSQL_ROOT_PASSWORD" ocm -N -e "SELECT status FROM jobs WHERE type='aicc_model_rollout' AND JSON_UNQUOTE(JSON_EXTRACT(payload_json, '$.org_id'))=(SELECT org_id FROM apps WHERE id='${escapedAppID}') ORDER BY created_at DESC LIMIT 1" 2>/dev/null`,
    ],
    { encoding: 'utf8' },
  ).trim(), { timeout: 240_000 }).toBe('succeeded')
}

// uploadAICCKnowledgeFile 通过当前页面的文件输入上传内存文本知识。
export async function uploadAICCKnowledgeFile(page: Page, filename: string, content: string): Promise<void> {
  const uploaded = page.waitForResponse(response =>
    response.url().includes('/knowledge')
    && !response.url().includes('/knowledge-uploads')
    && response.request().method() === 'POST',
  )
  await page.locator('input[type="file"]').setInputFiles({
    name: filename,
    mimeType: 'text/plain',
    buffer: Buffer.from(content, 'utf8'),
  })
  expect((await uploaded).ok()).toBeTruthy()
}

// waitForAICCKnowledgeParsed 轮询后端列表，确认指定文档已完成解析并在当前页面可见。
export async function waitForAICCKnowledgeParsed(page: Page, endpoint: string, filename: string): Promise<void> {
  await expect.poll(async () => {
    return await page.evaluate(async ({ endpoint, filename }) => {
      const token = window.localStorage.getItem('ocm.access_token')
      const response = await fetch(`${endpoint}?page=1&page_size=50`, {
        headers: token ? { Authorization: `Bearer ${token}` } : {},
      })
      if (!response.ok) return ''
      const payload = await response.json() as { items?: Array<{ name?: string, parse_status?: string }> }
      return payload.items?.find(item => item.name === filename)?.parse_status ?? ''
    }, { endpoint, filename })
  }, { timeout: 180_000, intervals: [2_000, 3_000, 5_000] }).toBe('completed')
  const knowledgeCard = page.locator('.knowledge-drop-zone')
  await expect(knowledgeCard.getByText(filename, { exact: true })).toBeVisible({ timeout: 30_000 })
  await expect(knowledgeCard.getByText('已完成', { exact: true })).toBeVisible({ timeout: 30_000 })
}

// waitForRuntimeKnowledgeSearch 等待 RAGFlow 索引进入运行时可检索状态。
export async function waitForRuntimeKnowledgeSearch(appID: string, question: string, expected: string): Promise<void> {
  assertLocalK3DContext()
  await expect.poll(() => execFileSync(
    'kubectl',
    ['--context', localK3DContext, '-n', 'oc-aicc', 'exec', `deploy/app-${appID}`, '-c', 'hermes', '--', 'oc-kb', 'search', question, '--top-k', '8'],
    { encoding: 'utf8' },
  ), { timeout: 300_000, intervals: [2_000, 5_000, 10_000] }).toContain(expected)
}

// waitForRuntimeKnowledgeSearchNotContaining 等待运行时检索结果不再包含指定文本，用于验证删除后的索引收敛。
export async function waitForRuntimeKnowledgeSearchNotContaining(appID: string, question: string, unexpected: string): Promise<void> {
  assertLocalK3DContext()
  await expect.poll(() => execFileSync(
    'kubectl',
    ['--context', localK3DContext, '-n', 'oc-aicc', 'exec', `deploy/app-${appID}`, '-c', 'hermes', '--', 'oc-kb', 'search', question, '--top-k', '8'],
    { encoding: 'utf8' },
  ), { timeout: 300_000, intervals: [2_000, 5_000, 10_000] }).not.toContain(unexpected)
}

// askPublicAICCQuestion 从公开页发送一条消息，并返回完整消息列表文本。
export async function askPublicAICCQuestion(page: Page, publicToken: string, question: string): Promise<string> {
  await forceZh(page)
  await page.goto(`/aicc/${publicToken}`)
  const assistantMessages = page.locator('.message-row.assistant .bubble p:not(.message-status)')
  const previousAssistantCount = await assistantMessages.count()
  const replied = page.waitForResponse(response =>
    response.url().includes('/messages') && response.request().method() === 'POST',
    { timeout: 180_000 },
  )
  await page.getByPlaceholder('输入您的问题').fill(question)
  await page.getByRole('button', { name: '发送' }).click()
  expect((await replied).ok()).toBeTruthy()
  await expect(assistantMessages).toHaveCount(previousAssistantCount + 1, { timeout: 240_000 })
  return await page.locator('.message-list').innerText()
}

// queryLocalManagerDB 在本地 manager MySQL 中执行只读查询，供 E2E 断言服务端事实。
export function queryLocalManagerDB(sql: string): string {
  assertLocalK3DContext()
  return execFileSync('kubectl', [
    '--context', localK3DContext, '-n', 'ocm', 'exec', 'mysql-0', '--', 'sh', '-c',
    'mysql -uroot -p"$MYSQL_ROOT_PASSWORD" ocm -N -e "$1" 2>/dev/null',
    'mysql-query',
    sql,
  ], { encoding: 'utf8' }).trim()
}

// countAICCLeadsByPhone 通过手机号查正式线索数量，用于重复提交和并发去重验证。
export function countAICCLeadsByPhone(phone: string): number {
  const escapedPhone = phone.replaceAll("'", "''")
  const result = queryLocalManagerDB(
    `SELECT COUNT(*) FROM aicc_lead_values WHERE value='${escapedPhone}'`,
  )
  return Number(result || '0')
}

// getAICCRuntimePhase 读取隐藏 app 的运行时阶段，配合 Kubernetes Ready 等待确认重启收敛。
export function getAICCRuntimePhase(appID: string): string {
  const escapedAppID = appID.replaceAll("'", "''")
  return queryLocalManagerDB(`SELECT runtime_phase FROM apps WHERE id='${escapedAppID}'`)
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
