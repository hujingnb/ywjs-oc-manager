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
