import { expect, test, type Page } from '@playwright/test'

import { loadE2EFixture, loginAs } from './fixtures'

type AICCAgentResponse = {
  agent: {
    id: string
    name: string
    public_token: string
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
  await expect(page.getByText(/\/aicc\/[A-Za-z0-9_-]+/)).toBeVisible()
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

// AICC 主流程覆盖：平台开通企业 AICC 后，企业管理员可以创建客服智能体并取得公开链接。
test('平台开通 AICC 后企业管理员可创建客服智能体', async ({ page }) => {
  await enableAICCForFixtureOrg(page)
  await clearLoginState(page)
  await createAICCAgentAsOrgAdmin(page)
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

  await page.goto('/aicc')
  await page.getByText('线索', { exact: true }).click()
  await expect(page.getByText(phone)).toBeVisible()
  await expect(page.getByText('未读')).toBeVisible()

  await page.getByText('统计', { exact: true }).click()
  await expect(page.locator('.metric-tile').filter({ hasText: '未读线索' }).getByText(/[1-9]\d*/)).toBeVisible()

  const downloadPromise = page.waitForEvent('download')
  await page.getByText('线索', { exact: true }).click()
  await page.getByRole('button', { name: '导出 CSV' }).click()
  const download = await downloadPromise
  expect(download.suggestedFilename()).toMatch(/aicc-leads\.csv/)
})
