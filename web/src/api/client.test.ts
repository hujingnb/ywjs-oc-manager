// apiRequest 单元测试覆盖 HTTP 客户端的错误消息提取边界。
// 测试只 mock fetch 响应，不触发真实网络或 token 存储。
import { afterEach, describe, expect, it, vi } from 'vitest'

import { apiRequest } from './client'

describe('apiRequest', () => {
  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('错误响应包含 message 字段时使用后端业务文案', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(new Response(JSON.stringify({
      code: 'NO_NODE_AVAILABLE',
      message: '暂无可用 Runtime Node，请联系平台管理员调整节点容量或新增节点',
    }), {
      status: 503,
      headers: { 'content-type': 'application/json' },
    }))

    await expect(apiRequest('/api/v1/organizations/org-1/members/onboard', {
      method: 'POST',
      body: {},
    })).rejects.toThrow('暂无可用 Runtime Node，请联系平台管理员调整节点容量或新增节点')
  })
})
