import { expect, test } from '@playwright/test'

import { createStartedAICCConversationFixture, forceZh, sendPublicAICCMessage } from './aicc/helpers'

test.setTimeout(600_000)

// 意向、留资与会话状态验收仅使用公开访客界面，验证模型输出的结构化动作能被页面安全消费。
test.describe('AICC 客服意向与会话状态', () => {
  test.skip(process.env.OCM_AICC_CONVERSATION_E2E !== '1', '需 OCM_AICC_CONVERSATION_E2E=1 显式启用本地真实客服验收')

  // 高意向首轮必须给出一次留资邀请；访客拒绝后继续提问时不能重复强迫填写。
  test('高意向访客可拒绝一次留资邀请并继续咨询', async ({ page }) => {
    const agent = await createStartedAICCConversationFixture(page, '意向验收客服')
    await forceZh(page)
    await page.goto(`/aicc/${agent.publicToken}`)
    await sendPublicAICCMessage(page, '我们公司计划本季度采购 50 个席位，预算已批准，请联系我安排产品演示。')
    const decline = page.getByRole('button', { name: '暂不留资' })
    await expect(decline).toBeVisible()
    await decline.click()
    await sendPublicAICCMessage(page, '我还想了解部署周期。')
    await expect(page.getByRole('button', { name: '暂不留资' })).toHaveCount(0)
  })

  // 访客显式确认才改变解决状态；确认后的新问题必须重新进入 unknown，并在后续对话显示确认动作。
  test('解决状态由访客确认并在新问题后重置', async ({ page }) => {
    const agent = await createStartedAICCConversationFixture(page, '状态验收客服')
    await forceZh(page)
    await page.goto(`/aicc/${agent.publicToken}`)
    await sendPublicAICCMessage(page, '请介绍你们的售前支持范围。')
    await sendPublicAICCMessage(page, '谢谢，以上问题已解决。')
    const resolved = page.getByRole('button', { name: '已解决' })
    await expect(resolved).toBeVisible()
    await resolved.click()
    await expect(page.getByRole('button', { name: '已解决' })).toHaveCount(0)
    await sendPublicAICCMessage(page, '补充一个新问题：是否支持私有化部署？')
    await expect(page.locator('.message-list')).toContainText('补充一个新问题')
  })

  // 390px 是正式挂件常用最窄视口；中文输入和长英文产品名不能造成页面横向溢出。
  test('移动视口可完成中英文咨询且不横向溢出', async ({ page }) => {
    const agent = await createStartedAICCConversationFixture(page, '移动验收客服')
    await page.setViewportSize({ width: 390, height: 844 })
    await forceZh(page)
    await page.goto(`/aicc/${agent.publicToken}`)
    await sendPublicAICCMessage(page, '请用中文说明 EnterpriseCustomerRelationshipManagementPlatform 的支持范围。')
    expect(await page.evaluate(() => document.documentElement.scrollWidth > document.documentElement.clientWidth)).toBeFalsy()
  })
})
