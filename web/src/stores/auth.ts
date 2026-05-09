import { defineStore } from 'pinia'
import { computed, ref } from 'vue'

import {
  apiRequest,
  clearStoredTokens,
  getStoredAccessToken,
  getStoredRefreshToken,
  setStoredTokens,
} from '@/api/client'
import type { AuthUser, LoginResult } from '@/api'

// 登录与会话状态由 Pinia 集中管理。
// access_token 和 refresh_token 的持久化在 client 层处理，store 只负责内存中的当前用户视图。
export const useAuthStore = defineStore('auth', () => {
  const user = ref<AuthUser | null>(null)
  const loading = ref(false)
  const error = ref<string | null>(null)

  const isAuthenticated = computed(() => Boolean(user.value && getStoredAccessToken()))
  const isPlatformAdmin = computed(() => user.value?.role === 'platform_admin')
  const isOrgAdmin = computed(() => user.value?.role === 'org_admin')
  const isOrgMember = computed(() => user.value?.role === 'org_member')

  async function login(username: string, password: string): Promise<LoginResult> {
    loading.value = true
    error.value = null
    try {
      const result = await apiRequest<LoginResult>('/api/v1/auth/login', {
        method: 'POST',
        body: { username, password },
        withAuth: false,
      })
      setStoredTokens({
        accessToken: result.tokens.access_token,
        refreshToken: result.tokens.refresh_token,
      })
      user.value = result.user
      return result
    } catch (err) {
      error.value = err instanceof Error ? err.message : '登录失败'
      throw err
    } finally {
      loading.value = false
    }
  }

  async function fetchCurrentUser(): Promise<AuthUser | null> {
    if (!getStoredAccessToken()) {
      user.value = null
      return null
    }
    try {
      const response = await apiRequest<{ user: AuthUser }>('/api/v1/auth/me')
      user.value = response.user
      return response.user
    } catch (err) {
      // access token 过期时清空状态；此处不主动刷新 token，刷新逻辑由路由守卫触发。
      clearStoredTokens()
      user.value = null
      throw err
    }
  }

  async function logout(): Promise<void> {
    const refreshToken = getStoredRefreshToken()
    if (refreshToken) {
      try {
        await apiRequest<void>('/api/v1/auth/logout', {
          method: 'POST',
          body: { refresh_token: refreshToken },
        })
      } catch {
        // 注销失败不阻断本地清理，避免用户被卡死在已退出但前端仍持有 token 的状态。
      }
    }
    clearStoredTokens()
    user.value = null
  }

  return {
    user,
    loading,
    error,
    isAuthenticated,
    isPlatformAdmin,
    isOrgAdmin,
    isOrgMember,
    login,
    logout,
    fetchCurrentUser,
  }
})
