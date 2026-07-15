import { expect, test } from '@playwright/test'

import {
	assertAICCResolutionStatus,
  createStartedAICCConversationFixture,
  deleteLocalAICCPod,
  forceZh,
  sendPublicAICCMessage,
  waitForAICCRuntime,
} from './aicc/helpers'

test.setTimeout(600_000)

// 无状态运行时验收：从真实公开页面发起首轮，删除本地 Pod 后等待控制器重建，再验证同一浏览器会话可续聊。
test.describe('AICC 客服无状态运行时与故障恢复', () => {
  test.skip(process.env.OCM_AICC_CONVERSATION_E2E !== '1', '需 OCM_AICC_CONVERSATION_E2E=1 显式启用本地真实客服验收')

  // Pod 重建后 manager 重新注入受控摘要和近期消息，页面 session token 保持不变且能得到新回复。
  test('删除本地 AICC Pod 后同一公开会话可继续对话', async ({ page }) => {
    const agent = await createStartedAICCConversationFixture(page, '重启验收客服')
    await forceZh(page)
    await page.goto(`/aicc/${agent.publicToken}`)
    await sendPublicAICCMessage(page, '请记住本轮关键词：无状态续聊验证。')
    const storedToken = await page.evaluate(token => window.localStorage.getItem(`aicc:session:${token}:web_link`), agent.publicToken)
    expect(storedToken).toBeTruthy()

    deleteLocalAICCPod(agent.appID)
    await waitForAICCRuntime(agent.appID)
    await sendPublicAICCMessage(page, '请继续回答刚才的无状态续聊验证问题。')
    await expect(page.locator('.message-list')).toContainText('无状态续聊验证')
    await expect.poll(async () => page.evaluate(token => window.localStorage.getItem(`aicc:session:${token}:web_link`), agent.publicToken)).toBe(storedToken)
  })

  // 公开页失败态必须保留可访问的重试动作；此场景由本地运行时故障注入环境启用，避免普通验收主动损坏服务。
  test('模型或队列故障时公开页展示可重试动作', async ({ page }) => {
    test.skip(process.env.OCM_AICC_FAULT_INJECTION !== '1', '需由本地故障注入环境显式启用')
    const agent = await createStartedAICCConversationFixture(page, '故障验收客服')
    await forceZh(page)
    await page.goto(`/aicc/${agent.publicToken}`)
    await page.getByPlaceholder('输入您的问题').fill('请触发本地已配置的模型超时故障。')
    await page.getByRole('button', { name: '发送' }).click()
    await expect(page.getByRole('button', { name: '重试' })).toBeVisible()
    // 本地故障注入器配置为一次性失败；点击重试后必须取得真实助手文本，不能只验证错误按钮存在。
    await page.getByRole('button', { name: '重试' }).click()
    await expect(page.locator('.message-row.assistant .bubble p:not(.message-status)').last()).toBeVisible({ timeout: 240_000 })
  })

  // 故障注入由本地测试部署显式提供，避免 E2E 为了覆盖而修改任意运行时配置；每类失败都必须可恢复。
  for (const scenario of ['RAGFlow 检索失败', '公开网络搜索超时', '模型响应超时', '异步队列失败']) {
    // 场景：依赖故障返回安全失败态，恢复后同一会话仍可继续发送。
    test(`${scenario}后可恢复咨询`, async ({ page }) => {
      test.skip(process.env.OCM_AICC_FAULT_INJECTION !== '1', '需由本地故障注入环境显式启用')
      const agent = await createStartedAICCConversationFixture(page, `恢复-${scenario}`)
      await page.goto(`/aicc/${agent.publicToken}`)
      await page.getByPlaceholder('输入您的问题').fill(`请触发已配置的${scenario}。`)
      await page.getByRole('button', { name: '发送' }).click()
      await expect(page.getByRole('button', { name: '重试' })).toBeVisible()
      await page.getByRole('button', { name: '重试' }).click()
      await expect(page.locator('.message-row.assistant .bubble p:not(.message-status)').last()).toBeVisible({ timeout: 240_000 })
    })
  }

  // 未解决状态同样是访客显式动作；刷新后应保留，随后新消息才能重置为 unknown。
  test('未解决状态刷新后保留并在新消息后重置', async ({ page }) => {
    const agent = await createStartedAICCConversationFixture(page, '未解决状态客服')
    await page.goto(`/aicc/${agent.publicToken}`)
    await sendPublicAICCMessage(page, '请说明支持服务。')
    await sendPublicAICCMessage(page, '这次仍未解决。')
    await page.getByRole('button', { name: '未解决' }).click()
    const sessionToken = await page.evaluate(token => window.localStorage.getItem(`aicc:session:${token}:web_link`), agent.publicToken)
    expect(sessionToken).toBeTruthy()
    assertAICCResolutionStatus(sessionToken!, 'unresolved')
    await page.reload()
    await expect(page.getByRole('button', { name: '未解决' })).toHaveCount(0)
    await sendPublicAICCMessage(page, '我还有一个新的部署问题。')
    assertAICCResolutionStatus(sessionToken!, 'unknown')
    await expect(page.locator('.message-list')).toContainText('新的部署问题')
  })
})
