import { expect, request, test, type Page } from '@playwright/test'

import {
  clearLoginState,
  forceZh,
  openAICCConsole,
  openAICCSettings,
  sendPublicAICCMessage,
  seedAICCSessionsForPagination,
  waitForAICCRuntime,
} from './aicc/helpers'
import { loadE2EFixture, loginAs } from './fixtures'

// AICC hidden app 需要在 k3d 中异步创建三容器 Pod，单条真实链路允许等待最多四分钟。
test.setTimeout(240_000)

type AICCAgentResponse = {
  agent: {
    id: string
    app_id: string
    name: string
    public_token: string
    widget_token: string
  }
}

// enableAICCForFixtureOrg 覆盖平台管理员开通企业 AICC 的前置业务流程。
async function enableAICCForFixtureOrg(page: Page): Promise<void> {
  // 整套 AICC E2E 会为隔离场景创建多名客服；常规前置给足测试配额，
  // 具体“数量上限”场景再单独收紧，避免套件自身消耗完配额后误报创建功能失败。
  await setAICCConfigForFixtureOrg(page, true, 100)
}

// setAICCConfigForFixtureOrg 通过平台管理界面更新企业开关与配额，确保 E2E 覆盖真实配置链路。
async function setAICCConfigForFixtureOrg(page: Page, enabled: boolean, agentLimit: number): Promise<void> {
  const fx = loadE2EFixture()
  await forceZh(page)
  await loginAs(page, 'platform_admin', fx, 'zh')
  await page.goto('/organizations')

  const orgRow = page.getByRole('row', { name: new RegExp(fx.org_code) })
  await expect(orgRow).toBeVisible()
  await orgRow.getByRole('button', { name: /^(编辑|Edit)$/ }).click()

  const aiccSwitch = page
    .locator('.n-form-item')
    .filter({ hasText: '开通 AICC' })
    .getByRole('switch')
  const isEnabled = await aiccSwitch.getAttribute('aria-checked') === 'true'
  if (isEnabled !== enabled) {
    await aiccSwitch.click()
  }

  await page
    .locator('.n-form-item')
    .filter({ hasText: 'AICC 智能体数量上限' })
    .locator('input')
    .fill(String(agentLimit))

  const configSaved = page.waitForResponse(response =>
    response.url().includes('/api/v1/organizations/')
    && response.url().includes('/aicc-config')
    && response.request().method() === 'PATCH',
  )
  await page.getByRole('button', { name: '保存 AICC 配置' }).click()
  expect((await configSaved).ok()).toBeTruthy()
}

// createAICCAgentAsOrgAdmin 覆盖企业管理员创建 AICC 智能体并看到公开入口的核心路径。
async function createAICCAgentAsOrgAdmin(page: Page): Promise<AICCAgentResponse['agent']> {
  const fx = loadE2EFixture()
  await forceZh(page)
  await loginAs(page, 'org_admin', fx, 'zh')
  await openAICCConsole(page)
  await openAICCSettings(page)
  await page.getByRole('button', { name: '新建智能体' }).click()

  const agentName = `E2E 接待员 ${Date.now()}`
  await page.getByPlaceholder('例如：售前咨询接待员').fill(agentName)

  const agentCreated = page.waitForResponse(response =>
    response.url().includes('/api/v1/aicc/agents')
    && response.request().method() === 'POST',
  )
  await page.getByRole('button', { name: '保存配置' }).click()
  const createdResponse = await agentCreated
  expect(createdResponse.ok()).toBeTruthy()
  const created = await createdResponse.json() as AICCAgentResponse
  await waitForAICCRuntime(created.agent.app_id)

  await expect(page.getByRole('region', { name: '当前智能体' })).toContainText(agentName)
  await page.getByRole('link', { name: '接待台', exact: true }).click()
  await expect(page.locator('.public-link-box').locator('input')).toHaveValue(/\/aicc\/[A-Za-z0-9_-]+/)
  return created.agent
}

// configurePhoneLeadField 通过管理页真实配置公开页手机号必填留资字段。
async function configurePhoneLeadField(page: Page): Promise<void> {
  await openAICCSettings(page)
  await page.getByRole('button', { name: '添加字段' }).click()
  const row = page.locator('.lead-field-row').last()
  await row.getByPlaceholder('字段名称').fill('联系电话')
  await row.getByPlaceholder('字段 key').fill('contact_phone')
  const saved = page.waitForResponse(response =>
    response.url().includes('/lead-fields')
    && response.request().method() === 'PUT',
  )
  await page.getByRole('button', { name: '保存留资字段' }).click()
  expect((await saved).ok()).toBeTruthy()
  await expect(page.getByText('留资字段已保存')).toBeVisible()
}

// configureKnowledgeScope 通过管理页保存知识库范围，覆盖企业知识库检索配置的真实路由接线。
async function configureKnowledgeScope(page: Page): Promise<void> {
  await openAICCSettings(page)
  await expect(page.getByText('知识库范围')).toBeVisible()
  const orgKnowledgeCheckbox = page.getByText('使用企业共享知识库')
  await orgKnowledgeCheckbox.click()
  const saved = page.waitForResponse(response =>
    response.url().includes('/knowledge')
    && response.request().method() === 'PUT',
  )
  await page.getByRole('button', { name: '保存知识范围' }).click()
  expect((await saved).ok()).toBeTruthy()
  await expect(page.getByText('知识范围已保存')).toBeVisible()
}

// startAICCAgent 通过管理页启动智能体，确保公开链接进入 active 接待状态。
async function startAICCAgent(page: Page): Promise<void> {
  await page.getByRole('link', { name: '接待台', exact: true }).click()
  await expect(page).toHaveURL(/\/aicc-console(?:\?|$)/)
  const started = page.waitForResponse(response =>
    response.url().includes('/start')
    && response.request().method() === 'POST',
  )
  await page.getByRole('button', { name: '启动接待' }).click()
  expect((await started).ok()).toBeTruthy()
  await expect(page.getByText('已启动接待')).toBeVisible()
}

// configureOperationsSettings 通过管理页保存新增运营策略，覆盖安全配置表单到后端 settings 接口的接线。
async function configureOperationsSettings(page: Page): Promise<void> {
  await openAICCSettings(page)
  await page
    .locator('.n-form-item')
    .filter({ hasText: '单会话消息上限' })
    .locator('input')
    .fill('2')
  await page
    .locator('.n-form-item')
    .filter({ hasText: '会话续接有效期' })
    .locator('input')
    .fill('45')
  await page
    .locator('.n-form-item')
    .filter({ hasText: '敏感词' })
    .locator('textarea')
    .fill('禁用词')

  const saved = page.waitForResponse(response =>
    response.url().includes('/settings')
    && response.request().method() === 'PUT',
  )
  await page.getByRole('button', { name: '保存运营配置' }).click()
  const response = await saved
  expect(response.ok()).toBeTruthy()
  expect(response.request().postDataJSON()).toMatchObject({
    message_limit_per_session: 2,
    sensitive_words: ['禁用词'],
    blocked_visitor_enabled: true,
    session_resume_ttl_minutes: 45,
  })
  await expect(page.getByText('运营配置已保存')).toBeVisible()
}

// verifyPublicSessionRestore 覆盖首次消息延迟创建 session、token 持久化和刷新恢复消息。
async function verifyPublicSessionRestore(page: Page, agent: AICCAgentResponse['agent']): Promise<string> {
  let sessionCreatedBeforeMessage = false
  const createListener = (request: { url(): string, method(): string }) => {
    if (request.url().includes(`/api/v1/public/aicc/agents/${agent.public_token}/sessions`) && request.method() === 'POST') {
      sessionCreatedBeforeMessage = true
    }
  }
  page.on('request', createListener)
  await page.goto(`/aicc/${agent.public_token}`)
  await expect(page.getByRole('heading', { name: agent.name })).toBeVisible()
  page.off('request', createListener)
  expect(sessionCreatedBeforeMessage).toBeFalsy()

  const firstSession = page.waitForResponse(response =>
    response.url().includes(`/api/v1/public/aicc/agents/${agent.public_token}/sessions`)
    && response.request().method() === 'POST',
  )
  await page.getByPlaceholder('输入您的问题').fill('请回复这条续接测试消息')
  const assistantReply = page.waitForResponse(response =>
    response.url().includes('/messages') && response.request().method() === 'POST',
  )
  await page.getByRole('button', { name: '发送' }).click()
  const firstPayload = await (await firstSession).json() as { session: { session_token: string } }
  const sessionToken = firstPayload.session.session_token
  expect(sessionToken).toBeTruthy()
  expect((await assistantReply).ok()).toBeTruthy()
  await expect.poll(async () => page.evaluate(
    key => window.localStorage.getItem(key),
    `aicc:session:${agent.public_token}:web_link`,
  )).toBe(sessionToken)

  const restoredSession = page.waitForResponse(response =>
    response.url().includes(`/api/v1/public/aicc/sessions/${sessionToken}`)
    && response.request().method() === 'GET',
  )
  await page.reload()
  expect((await restoredSession).ok()).toBeTruthy()
  // 只校验访客消息行，助手回复可能引用原问题，不能用全页文本定位造成严格模式歧义。
  await expect(page.locator('.message-row.visitor').getByText('请回复这条续接测试消息', { exact: true })).toBeVisible()
  return sessionToken
}

// verifySessionFilters 覆盖新增会话筛选条件进入 URL query 与后端 sessions 查询参数。
async function verifySessionFilters(page: Page): Promise<void> {
  await page.getByRole('link', { name: '会话', exact: true }).click()
  await expect(page.getByText('最近会话')).toBeVisible()
  const filtered = page.waitForResponse(response => {
    if (!response.url().includes('/sessions') || response.request().method() !== 'GET') return false
    const url = new URL(response.url())
    return url.searchParams.get('channel') === 'web_link'
      && url.searchParams.get('region') === '本地网络'
  })
  await page.locator('.session-filters .n-select').nth(2).click()
  await page.locator('.n-base-select-option').filter({ hasText: '公开链接' }).click()
  await page.getByPlaceholder('地域').fill('本地网络')
  expect((await filtered).ok()).toBeTruthy()
  await expect(page).toHaveURL(/channel=web_link/)
  await expect(page).toHaveURL(/region=/)
}

// verifyAnalyticsFilters 覆盖统计时间窗口、bucket 与当前智能体筛选进入 analytics 查询参数。
async function verifyAnalyticsFilters(page: Page, agentId: string): Promise<void> {
  await page.getByRole('link', { name: '分析', exact: true }).click()
  await expect(page.getByRole('heading', { name: '会话趋势', exact: true })).toBeVisible()
  const weekly = page.waitForResponse(response => {
    if (!response.url().includes('/api/v1/aicc/analytics') || response.request().method() !== 'GET') return false
    const url = new URL(response.url())
    return url.searchParams.get('bucket') === 'week'
      && url.searchParams.get('agent_id') === agentId
      && Boolean(url.searchParams.get('start_at'))
      && Boolean(url.searchParams.get('end_at'))
  })
  await page.getByText('周', { exact: true }).click()
  expect((await weekly).ok()).toBeTruthy()

  const monthly = page.waitForResponse(response => {
    if (!response.url().includes('/api/v1/aicc/analytics') || response.request().method() !== 'GET') return false
    const url = new URL(response.url())
    return url.searchParams.get('bucket') === 'week'
      && url.searchParams.get('agent_id') === agentId
      && Boolean(url.searchParams.get('start_at'))
      && Boolean(url.searchParams.get('end_at'))
  })
  await page.getByRole('button', { name: '近 30 天' }).click()
  expect((await monthly).ok()).toBeTruthy()
}

// AICC 主流程覆盖：平台开通企业 AICC 后，企业管理员可以创建客服智能体并取得公开链接。
test('平台开通 AICC 后企业管理员可创建客服智能体', async ({ page }) => {
  await enableAICCForFixtureOrg(page)
  await clearLoginState(page)
  await createAICCAgentAsOrgAdmin(page)
  await configureKnowledgeScope(page)
})

// 智能体管理和工作台移动端覆盖：编辑、暂停、重启和删除必须作用于顶部已选智能体；
// 390px 视口下左侧导航与表单不能产生横向溢出，也不能让关键操作失去可点击性。
test('企业管理员可完整管理已选智能体并在移动端操作工作台', async ({ page }, testInfo) => {
  testInfo.setTimeout(480_000)
  await enableAICCForFixtureOrg(page)
  await clearLoginState(page)
  const agent = await createAICCAgentAsOrgAdmin(page)
  await startAICCAgent(page)

  await openAICCSettings(page)
  const renamed = `已编辑客服 ${Date.now()}`
  await page.locator('#aicc-agent-name').fill(renamed)
  const updated = page.waitForResponse(response =>
    response.url().includes(`/api/v1/aicc/agents/${agent.id}`) && response.request().method() === 'PATCH',
  )
  await page.getByRole('button', { name: '保存配置' }).click()
  expect((await updated).ok()).toBeTruthy()
  await expect(page.getByRole('region', { name: '当前智能体' })).toContainText(renamed)

  await page.getByRole('link', { name: '接待台', exact: true }).click()
  const stopped = page.waitForResponse(response => response.url().includes('/stop') && response.request().method() === 'POST')
  await page.getByRole('button', { name: '停止接待' }).click()
  expect((await stopped).ok()).toBeTruthy()
  await expect(page.getByRole('button', { name: '启动接待' })).toBeVisible()
  const restarted = page.waitForResponse(response => response.url().includes('/start') && response.request().method() === 'POST')
  await page.getByRole('button', { name: '启动接待' }).click()
  expect((await restarted).ok()).toBeTruthy()

  await page.setViewportSize({ width: 390, height: 844 })
  await expect(page.getByRole('link', { name: '设置', exact: true })).toBeVisible()
  await expect(page.getByRole('button', { name: '停止接待' })).toBeVisible()
  expect(await page.evaluate(() => document.documentElement.scrollWidth > document.documentElement.clientWidth)).toBeFalsy()

  await page.setViewportSize({ width: 1440, height: 900 })
  const deleteRequest = page.waitForResponse(response =>
    response.url().includes(`/api/v1/aicc/agents/${agent.id}`) && response.request().method() === 'DELETE',
  )
  await page.getByRole('button', { name: '删除' }).click()
  await page.getByRole('textbox').last().fill(renamed)
  await page.getByRole('dialog').getByRole('button', { name: '删除', exact: true }).click()
  expect((await deleteRequest).ok()).toBeTruthy()
  await expect(page.getByRole('region', { name: '当前智能体' })).not.toContainText(renamed)
})

// 平台运营边界覆盖：数量上限拒绝额外智能体，关闭企业后公开链接立即离线。
test('平台限制智能体数量并在关闭 AICC 后下线公开入口', async ({ page }) => {
  const fx = loadE2EFixture()
  await enableAICCForFixtureOrg(page)
  await clearLoginState(page)
  const agent = await createAICCAgentAsOrgAdmin(page)

  await expect(page.locator('#aicc-public-link')).toHaveValue(new RegExp(`/aicc/${agent.public_token}$`))
  await expect(page.locator('.qr-preview img')).toHaveAttribute('src', /^data:image\/png;base64,/)

  await clearLoginState(page)
  await setAICCConfigForFixtureOrg(page, true, 1)
  await clearLoginState(page)
  await loginAs(page, 'org_admin', fx, 'zh')
  await openAICCConsole(page)
  await openAICCSettings(page)
  await page.getByRole('button', { name: '新建智能体' }).click()
  await page.getByPlaceholder('例如：售前咨询接待员').fill('超额客服')
  const limited = page.waitForResponse(response =>
    response.url().includes('/api/v1/aicc/agents') && response.request().method() === 'POST',
  )
  await page.getByRole('button', { name: '保存配置' }).click()
  expect((await limited).status()).toBe(409)
  await expect(page.getByText('AICC 智能体数量已达上限')).toBeVisible()

  await clearLoginState(page)
  await setAICCConfigForFixtureOrg(page, false, 1)
  const offline = await page.request.get(`/api/v1/public/aicc/agents/${agent.public_token}/config`)
  expect(offline.status()).toBe(404)
  expect((await offline.json()) as { code: string }).toMatchObject({ code: 'AICC_OFFLINE' })
})

// 挂件安全覆盖：产品预览加载真实脚本；白名单宿主可创建会话并沉淀来源页，未授权宿主不能消耗会话配额。
test('网页挂件校验允许域名并记录授权来源页', async ({ page }) => {
  await enableAICCForFixtureOrg(page)
  await clearLoginState(page)
  const agent = await createAICCAgentAsOrgAdmin(page)
  await openAICCSettings(page)
  await page.locator('#aicc-allowed-domains').fill('allowed.localhost')
  const saved = page.waitForResponse(response =>
    response.url().includes(`/api/v1/aicc/agents/${agent.id}`) && response.request().method() === 'PATCH',
  )
  await page.getByRole('button', { name: '保存配置' }).click()
  expect((await saved).ok()).toBeTruthy()
  await startAICCAgent(page)

  const previewOpened = page.waitForEvent('popup')
  await page.getByRole('button', { name: '预览挂件效果' }).click()
  const previewPage = await previewOpened
  await expect(previewPage.getByRole('button', { name: '在线客服' })).toBeVisible()
  await previewPage.getByRole('button', { name: '在线客服' }).click()
  await expect(previewPage.frameLocator('[data-aicc-widget-frame]').getByRole('heading', { name: agent.name })).toBeVisible()
  await previewPage.close()

  const allowedSourceURL = 'http://allowed.localhost/landing'
  const allowedRequest = await request.newContext({
    baseURL: 'http://ocm.localhost',
    extraHTTPHeaders: { Origin: 'http://allowed.localhost', Referer: allowedSourceURL },
  })
  const created = await allowedRequest.post(`/api/v1/public/aicc/agents/${agent.widget_token}/sessions`, {
    data: { channel: 'web_widget', source_url: allowedSourceURL, referrer: allowedSourceURL },
  })
  expect(created.status()).toBe(201)
  const createdPayload = await created.json() as { session: { session_token: string } }
  // 后台正式列表过滤零消息会话；发送真实访客消息后才能验证来源页是否在运营视图沉淀。
  const messageSent = await allowedRequest.post(`/api/v1/public/aicc/sessions/${createdPayload.session.session_token}/messages`, {
    data: { text: '记录授权来源页' },
    timeout: 180_000,
  })
  expect(messageSent.ok()).toBeTruthy()
  await allowedRequest.dispose()

  const blockedSourceURL = 'http://blocked.localhost/landing'
  const blockedRequest = await request.newContext({
    baseURL: 'http://ocm.localhost',
    extraHTTPHeaders: { Origin: 'http://blocked.localhost', Referer: blockedSourceURL },
  })
  const forbidden = await blockedRequest.post(`/api/v1/public/aicc/agents/${agent.widget_token}/sessions`, {
    data: { channel: 'web_widget', source_url: blockedSourceURL, referrer: blockedSourceURL },
  })
  expect(forbidden.status()).toBe(403)
  expect((await forbidden.json()) as { code: string }).toMatchObject({ code: 'AICC_DOMAIN_FORBIDDEN' })
  await blockedRequest.dispose()

  await openAICCConsole(page)
  await page.getByRole('link', { name: '会话', exact: true }).click()
  await expect(page.locator('.session-row')).toHaveCount(1)
  await page.locator('.session-row').first().click()
  await expect(page.locator('.session-summary')).toContainText(allowedSourceURL)
})

// 公开图片闭环覆盖：合法小图片可随消息发送并在刷新后恢复，非法类型与超限图片不进入消息链路。
test('访客图片上传恢复并拒绝非法或超限文件', async ({ page }) => {
  await enableAICCForFixtureOrg(page)
  await clearLoginState(page)
  const agent = await createAICCAgentAsOrgAdmin(page)
  await startAICCAgent(page)

  const publicPage = await page.context().newPage()
  await forceZh(publicPage)
  await publicPage.goto(`/aicc/${agent.public_token}`)
  const sessionCreated = publicPage.waitForResponse(response =>
    response.url().includes(`/api/v1/public/aicc/agents/${agent.public_token}/sessions`) && response.request().method() === 'POST',
  )
  const imageUploaded = publicPage.waitForResponse(response =>
    response.url().includes('/images?filename=e2e.png') && response.request().method() === 'POST',
  )
  const messageSent = publicPage.waitForResponse(response =>
    response.url().includes('/messages') && response.request().method() === 'POST',
  )
  await publicPage.locator('#aicc-public-image').setInputFiles({
    name: 'e2e.png',
    mimeType: 'image/png',
    buffer: Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVQIHWP4z8DwHwAFgAI/ScL3JwAAAABJRU5ErkJggg==', 'base64'),
  })
  await expect(publicPage.locator('.pending-image img')).toBeVisible()
  await publicPage.getByRole('button', { name: '发送' }).click()
  expect((await sessionCreated).status()).toBe(201)
  expect((await imageUploaded).ok()).toBeTruthy()
  expect((await messageSent).ok()).toBeTruthy()
  await expect(publicPage.locator('.message-list img')).toBeVisible()
  // 公开聊天中的欢迎语、访客图片消息和客服回复均应在气泡下方展示本地 HH:mm 发送时间。
  await expect(publicPage.locator('.message-time')).toHaveCount(3)
  await expect(publicPage.locator('.message-time').first()).toHaveText(/^\d{2}:\d{2}$/)
  await publicPage.reload()
  // 图片消息的运行时回复可能描述图片内容；这里只校验访客消息恢复后不展示内部占位文本。
  await expect(publicPage.locator('.message-row.visitor').getByText('访客发送了一张图片')).toHaveCount(0)
  // 刷新恢复后，持久化的访客消息和客服回复仍应保留服务端创建时间。
  await expect(publicPage.locator('.message-time')).toHaveCount(2)

  await publicPage.locator('#aicc-public-image').setInputFiles({ name: 'not-image.txt', mimeType: 'text/plain', buffer: Buffer.from('x') })
  await expect(publicPage.getByText('请选择图片文件')).toBeVisible()
  await publicPage.locator('#aicc-public-image').setInputFiles({ name: 'large.png', mimeType: 'image/png', buffer: Buffer.alloc(10 * 1024 * 1024 + 1) })
  await expect(publicPage.getByText('图片不能超过 10MiB')).toBeVisible()
  await publicPage.close()
})

// 运营安全覆盖：设置页保存敏感词和会话上限后，公开端拒绝敏感词与第二条超额消息。
test('公开端执行敏感词和单会话消息上限', async ({ page }) => {
  await enableAICCForFixtureOrg(page)
  await clearLoginState(page)
  const agent = await createAICCAgentAsOrgAdmin(page)
  await openAICCSettings(page)
  await page.locator('#aicc-message-limit').fill('1')
  await page.locator('#aicc-sensitive-words').fill('违禁词')
  const saved = page.waitForResponse(response =>
    response.url().includes('/settings') && response.request().method() === 'PUT',
  )
  await page.getByRole('button', { name: '保存运营配置' }).click()
  expect((await saved).ok()).toBeTruthy()
  await startAICCAgent(page)

  const publicPage = await page.context().newPage()
  await forceZh(publicPage)
  await publicPage.goto(`/aicc/${agent.public_token}`)
  await publicPage.getByPlaceholder('输入您的问题').fill('这是一条违禁词消息')
  const created = publicPage.waitForResponse(response =>
    response.url().includes(`/api/v1/public/aicc/agents/${agent.public_token}/sessions`) && response.request().method() === 'POST',
  )
  const sensitive = publicPage.waitForResponse(response =>
    response.url().includes('/messages') && response.request().method() === 'POST',
  )
  await publicPage.getByRole('button', { name: '发送' }).click()
  expect((await created).status()).toBe(201)
  expect((await sensitive).status()).toBe(400)
  await expect(publicPage.getByText('这条消息包含暂不支持发送的内容，请调整后再试。')).toBeVisible()

  const firstMessage = publicPage.waitForResponse(response =>
    response.url().includes('/messages') && response.request().method() === 'POST',
  )
  await publicPage.getByPlaceholder('输入您的问题').fill('第一条正常消息')
  await publicPage.getByRole('button', { name: '发送' }).click()
  expect((await firstMessage).ok()).toBeTruthy()

  const limited = publicPage.waitForResponse(response =>
    response.url().includes('/messages') && response.request().method() === 'POST',
  )
  await publicPage.getByPlaceholder('输入您的问题').fill('第二条正常消息')
  await publicPage.getByRole('button', { name: '发送' }).click()
  expect((await limited).status()).toBe(429)
  await expect(publicPage.getByText('本次会话消息数量已达上限，请稍后重新打开客服。')).toBeVisible()
  await publicPage.close()
})

// AICC 线索闭环覆盖：公开访客提交留资后，企业管理员可在线索页看到未读线索、统计变化，并导出 CSV。
test('公开访客提交留资后企业管理员可查看线索和导出 CSV', async ({ page }) => {
  await enableAICCForFixtureOrg(page)
  await clearLoginState(page)
  const agent = await createAICCAgentAsOrgAdmin(page)
  await configurePhoneLeadField(page)
  await startAICCAgent(page)

  const publicPage = await page.context().newPage()
  await forceZh(publicPage)
  await publicPage.goto(`/aicc/${agent.public_token}`)
  await expect(publicPage.getByRole('heading', { name: agent.name })).toBeVisible()

  const phone = `139${Date.now().toString().slice(-8)}`
  // 留资卡只在模型识别到高购买意向后出现；先走真实公开咨询，不能把配置手机号字段误当作自动邀约。
  await sendPublicAICCMessage(publicPage, '我们计划采购 50 个席位，预算已批准，请联系我安排演示。')
  await expect(publicPage.getByText('请先留下联系信息')).toBeVisible()
  await publicPage.getByPlaceholder('联系电话').fill(phone)
  const submitted = publicPage.waitForResponse(response =>
    response.url().includes('/lead-values')
    && response.request().method() === 'POST',
  )
  await publicPage.getByRole('button', { name: '提交联系信息' }).click()
  expect((await submitted).ok()).toBeTruthy()
  await expect(publicPage.getByText('请先留下联系信息')).toBeHidden()

  const messageSent = publicPage.waitForResponse(response =>
    response.url().includes('/messages')
    && response.request().method() === 'POST',
  )
  await publicPage.getByPlaceholder('输入您的问题').fill('请介绍一下服务内容')
  await publicPage.getByRole('button', { name: '发送' }).click()
  expect((await messageSent).ok()).toBeTruthy()
  await publicPage.reload()
  await expect(publicPage.getByText('请先留下联系信息')).toBeHidden()
  await expect(publicPage.getByText('请介绍一下服务内容')).toBeVisible()
  await publicPage.close()

  await openAICCConsole(page)
  await page.getByRole('link', { name: '线索', exact: true }).click()
  const leadRow = page.locator('.lead-row').filter({ hasText: phone })
  await expect(leadRow).toBeVisible()
  await expect(leadRow.getByText('未读', { exact: true })).toBeVisible()
  await leadRow.getByRole('button', { name: '查看对话' }).click()
  await expect(page.getByText('请介绍一下服务内容')).toBeVisible()
  await page.getByRole('button', { name: '关闭对话' }).click()
  await expect(leadRow.getByRole('button', { name: '标记已读' })).toBeDisabled()
  await expect(page.getByText('已读', { exact: true })).toBeVisible()

  await page.getByRole('link', { name: '分析', exact: true }).click()
  await expect(page.locator('.metric-tile').filter({ hasText: '未读线索' }).getByText('0', { exact: true })).toBeVisible()

  const downloadPromise = page.waitForEvent('download')
  await page.getByRole('link', { name: '线索', exact: true }).click()
  await page.getByRole('button', { name: '导出 CSV' }).click()
  const download = await downloadPromise
  expect(download.suggestedFilename()).toMatch(/aicc-leads\.csv/)
})

// AICC 运营补齐覆盖：企业管理员保存运营配置后，公开页可续接会话，后台会话和统计筛选使用新增参数。
test('企业管理员可配置运营策略并验证公开会话续接和筛选统计', async ({ page }) => {
  await enableAICCForFixtureOrg(page)
  await clearLoginState(page)
  const agent = await createAICCAgentAsOrgAdmin(page)
  await configureOperationsSettings(page)
  await startAICCAgent(page)

  const publicPage = await page.context().newPage()
  await forceZh(publicPage)
  const firstSessionToken = await verifyPublicSessionRestore(publicPage, agent)

  let eagerSessionCreated = false
  const sessionListener = (request: { url(): string, method(): string }) => {
    if (request.url().includes('/sessions') && request.method() === 'POST') eagerSessionCreated = true
  }
  publicPage.on('request', sessionListener)
  await publicPage.getByRole('button', { name: '结束本次咨询' }).click()
  await publicPage.waitForTimeout(300)
  expect(eagerSessionCreated).toBeFalsy()
  publicPage.off('request', sessionListener)

  // 结束咨询只清理续接凭证；访客重新打开入口后，首条新消息才创建新的 session。
  await publicPage.reload()

  const secondSession = publicPage.waitForResponse(response =>
    response.url().includes(`/api/v1/public/aicc/agents/${agent.public_token}/sessions`)
    && response.request().method() === 'POST',
  )
  await publicPage.getByPlaceholder('输入您的问题').fill('这是新会话消息')
  await publicPage.getByRole('button', { name: '发送' }).click()
  const secondSessionPayload = await (await secondSession).json() as { session: { session_token: string } }
  expect(secondSessionPayload.session.session_token).not.toBe(firstSessionToken)

  await publicPage.setViewportSize({ width: 390, height: 844 })
  await expect(publicPage.getByRole('button', { name: '结束本次咨询' })).toBeVisible()
  const hasHorizontalOverflow = await publicPage.evaluate(() => document.documentElement.scrollWidth > document.documentElement.clientWidth)
  expect(hasHorizontalOverflow).toBeFalsy()
  const composerBox = await publicPage.locator('.composer').boundingBox()
  expect(composerBox).not.toBeNull()
  expect((composerBox?.x ?? 0) + (composerBox?.width ?? 0)).toBeLessThanOrEqual(390)

  await publicPage.close()

  const englishPage = await page.context().newPage()
  await englishPage.addInitScript(() => window.localStorage.setItem('ocm.locale', 'en'))
  await englishPage.goto(`/aicc/${agent.public_token}`)
  await expect(englishPage.getByRole('button', { name: 'End this consultation' })).toBeVisible()
  await englishPage.close()

  seedAICCSessionsForPagination(agent.id, loadE2EFixture().org_id, 19)
  await page.setViewportSize({ width: 1440, height: 900 })
  await openAICCConsole(page)
  await expect(page.getByRole('region', { name: '当前智能体' })).toContainText(agent.name)
  await verifySessionFilters(page)
  await page.goto('/aicc-console/sessions')
  await expect(page.locator('.session-row').first()).toContainText('跟进中')
  const pageTwo = page.waitForResponse(response => {
    if (!response.url().includes('/sessions') || response.request().method() !== 'GET') return false
    const url = new URL(response.url())
    return url.searchParams.get('offset') === '20' && url.searchParams.get('limit') === '20'
  })
  await page.locator('.session-pagination .n-pagination-item').filter({ hasText: /^2$/ }).click()
  expect((await pageTwo).ok()).toBeTruthy()
  await expect(page.locator('.session-row')).toHaveCount(1)
  await verifyAnalyticsFilters(page, agent.id)
})
