import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { useAuthStore } from './auth'

const clientMocks = vi.hoisted(() => ({
  apiRequest: vi.fn(),
  clearStoredTokens: vi.fn(),
  getStoredAccessToken: vi.fn(),
  getStoredRefreshToken: vi.fn(),
  setStoredTokens: vi.fn(),
}))

vi.mock('@/api/client', () => clientMocks)

describe('auth store', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
    clientMocks.apiRequest.mockResolvedValue({
      tokens: {
        access_token: 'access-token',
        refresh_token: 'refresh-token',
      },
      user: {
        id: 'user-1',
        username: 'admin',
        display_name: '管理员',
        role: 'platform_admin',
        status: 'active',
      },
    })
  })

  it('组织登录提交组织标识', async () => {
    const auth = useAuthStore()

    await auth.login('admin', 'secret-password', ' test-org ')

    expect(clientMocks.apiRequest).toHaveBeenCalledWith('/api/v1/auth/login', {
      method: 'POST',
      body: { org_code: 'test-org', username: 'admin', password: 'secret-password' },
      withAuth: false,
    })
  })

  it('平台登录保留空组织标识', async () => {
    const auth = useAuthStore()

    await auth.login('admin', 'secret-password')

    expect(clientMocks.apiRequest.mock.calls[0][1].body).toEqual({
      org_code: undefined,
      username: 'admin',
      password: 'secret-password',
    })
  })
})
