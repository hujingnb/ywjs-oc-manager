// auth store 维护登录用户的内存状态，并协调 token 持久化与会话接口。
// token 的读写细节在 api/client.ts 中封装，store 只暴露页面需要的角色派生状态。
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

  // isAuthenticated 同时要求内存用户和 access token 存在，避免刷新后未拉取 /me 时误判。
  const isAuthenticated = computed(() => Boolean(user.value && getStoredAccessToken()))
  // 以下角色 computed 只服务页面展示和入口控制，后端仍负责最终授权。
  const isPlatformAdmin = computed(() => user.value?.role === 'platform_admin')
  const isOrgAdmin = computed(() => user.value?.role === 'org_admin')
  const isOrgMember = computed(() => user.value?.role === 'org_member')

  // login 成功后先持久化 token 再写入 user，保证随后的路由跳转能带 Authorization。
  // captcha 为可选字段：后端验证码开启时由登录页传入 Altcha payload，关闭时传 undefined。
  async function login(
    username: string,
    password: string,
    orgCode = '',
    captcha?: string,
  ): Promise<LoginResult> {
    loading.value = true
    error.value = null
    try {
      const result = await apiRequest<LoginResult>('/api/v1/auth/login', {
        method: 'POST',
        body: { org_code: orgCode.trim() || undefined, username, password, captcha },
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

  // fetchCurrentUser 用现有 access token 恢复当前用户。
  // token 缺失直接清空 user；接口失败则清理本地 token，让路由守卫回到登录页。
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

  // 自助改密成功后旧 token 不再继续信任，清理本地会话并要求用户重新登录。
  async function changePassword(oldPassword: string, newPassword: string): Promise<void> {
    await apiRequest<void>('/api/v1/auth/password', {
      method: 'POST',
      body: { old_password: oldPassword, new_password: newPassword },
      preserveAuthOnUnauthorizedCodes: ['INVALID_CREDENTIALS'],
    })
    clearStoredTokens()
    user.value = null
  }

  // logout 尽力通知后端失效 refresh token，然后无条件清理本地会话。
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
    changePassword,
  }
})
