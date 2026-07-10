import { expect, test, type Page } from '@playwright/test'

import { loadE2EFixture, loginAs } from './fixtures'

type AICCAgentResponse = {
  agent: {
    id: string
    name: string
    public_token: string
    widget_token: string
  }
}

type AICCPublicSessionResponse = {
  session: {
    session_token: string
    restored?: boolean
  }
}

// forceZh 在页面初始化前固定中文界面，避免平台默认语言差异影响可见文案定位。
async function forceZh(page: Page): Promise<void> {
  await page.addInitScript(() => {
    window.localStorage.setItem('ocm.locale', 'zh')
  })
}

// clearLoginState 清理当前浏览器页的登录态，用同一个 page 串联平台与企业管理员流程。
async function clearLoginState(page: Page): Promise<void> {
  await page.evaluate(() => {
    window.localStorage.removeItem('ocm.access_token')
    window.localStorage.removeItem('ocm.refresh_token')
    window.localStorage.setItem('ocm.locale', 'zh')
  })
  await page.context().clearCookies()
}

// enableAICCForFixtureOrg 覆盖平台管理员开通企业 AICC 的前置业务流程。
async function enableAICCForFixtureOrg(page: Page): Promise<void> {
  const fx = loadE2EFixture()
  await forceZh(page)
  await loginAs(page, 'platform_admin', fx)
  await page.goto('/organizations')

  const orgRow = page.getByRole('row', { name: new RegExp(fx.org_code) })
  await expect(orgRow).toBeVisible()
  await orgRow.getByRole('button', { name: /^(编辑|Edit)$/ }).click()

  const aiccSwitch = page
    .locator('.n-form-item')
    .filter({ hasText: '开通 AICC' })
    .getByRole('switch')
  if (await aiccSwitch.getAttribute('aria-checked') !== 'true') {
    await aiccSwitch.click()
  }

  await page
    .locator('.n-form-item')
    .filter({ hasText: 'AICC 智能体数量上限' })
    .locator('input')
    .fill('3')

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
  await loginAs(page, 'org_admin', fx)
  await page.goto('/aicc')

  await expect(page.getByRole('heading', { name: 'AICC 接待台' })).toBeVisible()
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

  await expect(page.getByRole('button', { name: new RegExp(agentName) })).toBeVisible()
  await expect(page.locator('.public-link-box').locator('input')).toHaveValue(/\/aicc\/[A-Za-z0-9_-]+/)
  return created.agent
}

// configurePhoneLeadField 通过管理页真实配置公开页手机号必填留资字段。
async function configurePhoneLeadField(page: Page): Promise<void> {
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

// verifyPublicSessionRestore 覆盖公开页把服务端 session_token 写入本地存储，并在刷新时提交旧 token 续接会话。
async function verifyPublicSessionRestore(page: Page, agent: AICCAgentResponse['agent']): Promise<string> {
  const firstSession = page.waitForResponse(response =>
    response.url().includes(`/api/v1/public/aicc/agents/${agent.public_token}/sessions`)
    && response.request().method() === 'POST',
  )
  await page.goto(`/aicc/${agent.public_token}`)
  await expect(page.getByRole('heading', { name: agent.name })).toBeVisible()
  const firstPayload = await (await firstSession).json() as AICCPublicSessionResponse
  const sessionToken = firstPayload.session.session_token
  expect(sessionToken).toBeTruthy()
  await expect.poll(async () => page.evaluate(
    key => window.localStorage.getItem(key),
    `aicc:session:${agent.public_token}:web_link`,
  )).toBe(sessionToken)

  const restoredSession = page.waitForResponse(response =>
    response.url().includes(`/api/v1/public/aicc/agents/${agent.public_token}/sessions`)
    && response.request().method() === 'POST'
    && response.request().postDataJSON()?.session_token === sessionToken,
  )
  await page.reload()
  const restoredPayload = await (await restoredSession).json() as AICCPublicSessionResponse
  expect(restoredPayload.session.restored).toBeTruthy()
  return sessionToken
}

// verifySessionFilters 覆盖新增会话筛选条件进入 URL query 与后端 sessions 查询参数。
async function verifySessionFilters(page: Page): Promise<void> {
  await page.getByText('会话', { exact: true }).click()
  await expect(page.getByText('最近会话')).toBeVisible()
  const filtered = page.waitForResponse(response => {
    if (!response.url().includes('/sessions') || response.request().method() !== 'GET') return false
    const url = new URL(response.url())
    return url.searchParams.get('channel') === 'web_link'
      && url.searchParams.get('region') === '未知地域'
  })
  await page.locator('.session-filters .n-select').nth(2).click()
  await page.locator('.n-base-select-option').filter({ hasText: '公开链接' }).click()
  await page.getByPlaceholder('地域').fill('未知地域')
  expect((await filtered).ok()).toBeTruthy()
  await expect(page).toHaveURL(/channel=web_link/)
  await expect(page).toHaveURL(/region=/)
}

// verifyAnalyticsFilters 覆盖统计时间窗口、bucket 与当前智能体筛选进入 analytics 查询参数。
async function verifyAnalyticsFilters(page: Page, agentId: string): Promise<void> {
  await page.getByText('统计', { exact: true }).click()
  await expect(page.getByText('会话趋势')).toBeVisible()
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
  await expect(publicPage.getByText('请先留下联系信息')).toBeVisible()
  await publicPage.getByPlaceholder('联系电话').fill(phone)
  const submitted = publicPage.waitForResponse(response =>
    response.url().includes('/lead-values')
    && response.request().method() === 'POST',
  )
  await publicPage.getByRole('button', { name: '提交联系信息' }).click()
  expect((await submitted).ok()).toBeTruthy()
  await expect(publicPage.getByText('请先留下联系信息')).toBeHidden()
  await publicPage.close()

  const widgetPage = await page.context().newPage()
  await forceZh(widgetPage)
  await widgetPage.setContent(`
    <!doctype html>
    <html lang="zh-CN">
      <body>
        <h1>客户官网落地页</h1>
        <script src="http://ocm.localhost/aicc-widget.js" data-aicc-widget-token="${agent.widget_token}"></script>
      </body>
    </html>
  `)
  await widgetPage.getByRole('button', { name: '在线客服' }).click()
  const frame = widgetPage.frameLocator('[data-aicc-widget-frame]')
  await expect(frame.getByRole('heading', { name: agent.name })).toBeVisible()
  await widgetPage.close()

  await page.goto('/aicc')
  await page.getByText('线索', { exact: true }).click()
  await expect(page.getByText(phone, { exact: true })).toBeVisible()
  await expect(page.getByText('未读')).toBeVisible()

  await page.getByText('统计', { exact: true }).click()
  await expect(page.locator('.metric-tile').filter({ hasText: '未读线索' }).getByText(/[1-9]\d*/)).toBeVisible()

  const downloadPromise = page.waitForEvent('download')
  await page.getByText('线索', { exact: true }).click()
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
  await verifyPublicSessionRestore(publicPage, agent)
  await publicPage.close()

  await page.goto('/aicc')
  await page.getByRole('button', { name: new RegExp(agent.name) }).click()
  await verifySessionFilters(page)
  await verifyAnalyticsFilters(page, agent.id)
})
