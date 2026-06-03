// apiRequest 单元测试覆盖 HTTP 客户端的错误消息提取边界。
// 测试只 mock fetch 响应，不触发真实网络或 token 存储。
import { afterEach, describe, expect, it, vi } from 'vitest'

import {
  apiRequest,
  clearStoredTokens,
  getStoredAccessToken,
  getStoredRefreshToken,
  setStoredTokens,
  setUnauthorizedHandler,
} from './client'

describe('apiRequest', () => {
  afterEach(() => {
    vi.restoreAllMocks()
    setUnauthorizedHandler(null)
    clearStoredTokens()
  })

  // 覆盖后端错误体带 message 时，HTTP 客户端应优先透出业务可读文案。
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

  // 覆盖认证接口声明特定 401 业务错误码时，可保留本地 token 并继续抛出业务错误。
  it('401 业务错误码允许保留登录态时不触发全局未授权处理', async () => {
    const unauthorizedHandler = vi.fn()
    setStoredTokens({
      accessToken: 'access-token',
      refreshToken: 'refresh-token',
    })
    setUnauthorizedHandler(unauthorizedHandler)
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(new Response(JSON.stringify({
      code: 'INVALID_CREDENTIALS',
      message: '当前密码错误',
    }), {
      status: 401,
      headers: { 'content-type': 'application/json' },
    }))

    await expect(apiRequest('/api/v1/auth/password', {
      method: 'POST',
      body: { old_password: 'wrong-password', new_password: 'new-password-123' },
      preserveAuthOnUnauthorizedCodes: ['INVALID_CREDENTIALS'],
    })).rejects.toMatchObject({
      message: '当前密码错误',
      status: 401,
    })

    expect(getStoredAccessToken()).toBe('access-token')
    expect(getStoredRefreshToken()).toBe('refresh-token')
    expect(unauthorizedHandler).not.toHaveBeenCalled()
  })

  // 覆盖已声明可保留登录态的接口遇到 token 失效 401 时，仍必须走全局清理和跳转。
  it('401 非保留错误码仍清理 token 并触发全局未授权处理', async () => {
    const unauthorizedHandler = vi.fn()
    setStoredTokens({
      accessToken: 'access-token',
      refreshToken: 'refresh-token',
    })
    setUnauthorizedHandler(unauthorizedHandler)
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(new Response(JSON.stringify({
      code: 'INVALID_TOKEN',
      message: '登录凭证无效',
    }), {
      status: 401,
      headers: { 'content-type': 'application/json' },
    }))

    await expect(apiRequest('/api/v1/auth/password', {
      method: 'POST',
      body: { old_password: 'old-password', new_password: 'new-password-123' },
      preserveAuthOnUnauthorizedCodes: ['INVALID_CREDENTIALS'],
    })).rejects.toMatchObject({
      message: '登录凭证无效',
      status: 401,
    })

    expect(getStoredAccessToken()).toBeNull()
    expect(getStoredRefreshToken()).toBeNull()
    expect(unauthorizedHandler).toHaveBeenCalledWith('/api/v1/auth/password')
  })
})
