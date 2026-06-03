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

  it('企业登录提交企业标识', async () => {
    const auth = useAuthStore()

    await auth.login('admin', 'secret-password', ' test-org ')

    expect(clientMocks.apiRequest).toHaveBeenCalledWith('/api/v1/auth/login', {
      method: 'POST',
      body: { org_code: 'test-org', username: 'admin', password: 'secret-password' },
      withAuth: false,
    })
  })

  it('平台登录保留空企业标识', async () => {
    const auth = useAuthStore()

    await auth.login('admin', 'secret-password')

    expect(clientMocks.apiRequest.mock.calls[0][1].body).toEqual({
      org_code: undefined,
      username: 'admin',
      password: 'secret-password',
    })
  })

  // 覆盖自助修改密码时提交当前密码和新密码的请求契约。
  it('修改密码提交当前密码和新密码', async () => {
    const auth = useAuthStore()

    await auth.changePassword('old-password', 'new-password-123')

    expect(clientMocks.apiRequest).toHaveBeenCalledWith('/api/v1/auth/password', {
      method: 'POST',
      body: { old_password: 'old-password', new_password: 'new-password-123' },
    })
  })

  // 覆盖自助修改密码成功后必须清理前端本地会话，避免旧 token 继续使用。
  it('修改密码成功后清理本地会话', async () => {
    const auth = useAuthStore()
    auth.user = {
      id: 'user-1',
      username: 'admin',
      display_name: '管理员',
      role: 'platform_admin',
      status: 'active',
    }

    await auth.changePassword('old-password', 'new-password-123')

    expect(clientMocks.clearStoredTokens).toHaveBeenCalledTimes(1)
    expect(auth.user).toBeNull()
  })
})
