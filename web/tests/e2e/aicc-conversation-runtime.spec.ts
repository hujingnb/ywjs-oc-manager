import { expect, test } from '@playwright/test'

import {
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
  })
})
