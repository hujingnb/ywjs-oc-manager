// auth-state 为每个 worker 的三类角色生成独立 Playwright 登录态，业务 spec 只复用状态文件。
import { mkdir, writeFile } from 'node:fs/promises'
import { dirname } from 'node:path'

import { request } from '@playwright/test'
import type { APIRequestContext, FullConfig } from '@playwright/test'

import { authStatePath, type E2EFixture } from './suite'

// StorageState 复用 Playwright APIRequestContext 的真实返回类型，避免手写 cookie 字段漂移。
type StorageState = Awaited<ReturnType<APIRequestContext['storageState']>>

// AuthRole 是 seed-e2e 为每个 worker 固定创建的三类权限角色。
export type AuthRole = 'platform_admin' | 'org_admin' | 'org_member'

// LoginTokens 对齐 POST /api/v1/auth/login 返回的 tokens 字段。
export type LoginTokens = {
  // access_token 供前端 Authorization header 使用。
  access_token: string
  // refresh_token 供刷新和注销会话使用。
  refresh_token: string
}

// LoginPayload 描述真实登录 API 请求；平台管理员必须省略 org_code。
type LoginPayload = {
  // username 是当前 worker 对应角色的隔离账号。
  username: string
  // password 只发送给登录 API，不写入状态文件或诊断日志。
  password: string
  // org_code 仅组织管理员和普通成员需要。
  org_code?: string
}

// loginPayloadForRole 把完整 fixture 映射为后端真实登录请求，集中约束平台与组织登录分支。
export function loginPayloadForRole(fixture: E2EFixture, role: AuthRole): LoginPayload {
  if (role === 'platform_admin') {
    return {
      username: fixture.platform_admin_login,
      password: fixture.platform_admin_password,
    }
  }

  if (role === 'org_admin') {
    return {
      username: fixture.org_admin_login,
      password: fixture.org_admin_password,
      org_code: fixture.org_code,
    }
  }

  return {
    username: fixture.org_member_login,
    password: fixture.org_member_password,
    org_code: fixture.org_code,
  }
}

// buildStorageState 将 API 登录得到的 cookie 与 token 转成浏览器可直接加载的状态。
export function buildStorageState(
  baseURL: string,
  tokens: LoginTokens,
  cookies: StorageState['cookies'],
  locale: 'zh' | 'en',
): StorageState {
  // 两类 token 任一缺失都属于后端响应契约损坏，不允许生成会静默退回登录页的部分状态。
  if (typeof tokens.access_token !== 'string' || tokens.access_token.trim() === '') {
    throw new Error('登录响应缺少有效 access_token')
  }
  if (typeof tokens.refresh_token !== 'string' || tokens.refresh_token.trim() === '') {
    throw new Error('登录响应缺少有效 refresh_token')
  }

  // StorageState 的 origin 不接受路径；显式归一化保证带前缀的 baseURL 也写到正确 localStorage。
  const origin = new URL(baseURL).origin
  return {
    // 登录 API 下发的 csrf_token 等 cookie 必须原样保留，页面写请求才能通过 double-submit 校验。
    cookies,
    origins: [{
      origin,
      localStorage: [
        { name: 'ocm.access_token', value: tokens.access_token },
        { name: 'ocm.refresh_token', value: tokens.refresh_token },
        { name: 'ocm.locale', value: locale },
      ],
    }],
  }
}

// loginTokensFromPayload 严格提取真实响应中的 tokens，错误信息不得回显敏感令牌。
function loginTokensFromPayload(payload: unknown, role: AuthRole): LoginTokens {
  if (typeof payload !== 'object' || payload === null || !('tokens' in payload)) {
    throw new Error(`角色 ${role} 登录响应缺少 tokens`)
  }
  const tokens = payload.tokens
  if (typeof tokens !== 'object' || tokens === null) {
    throw new Error(`角色 ${role} 登录响应 tokens 格式无效`)
  }

  const accessToken = 'access_token' in tokens ? tokens.access_token : undefined
  const refreshToken = 'refresh_token' in tokens ? tokens.refresh_token : undefined
  if (typeof accessToken !== 'string' || accessToken.trim() === '') {
    throw new Error(`角色 ${role} 登录响应缺少有效 access_token`)
  }
  if (typeof refreshToken !== 'string' || refreshToken.trim() === '') {
    throw new Error(`角色 ${role} 登录响应缺少有效 refresh_token`)
  }
  return { access_token: accessToken, refresh_token: refreshToken }
}

// writeWorkerAuthStates 顺序登录当前 worker 的三类角色，并写入按 run、worker 隔离的状态文件。
export async function writeWorkerAuthStates(config: FullConfig, fixture: E2EFixture): Promise<void> {
  const baseURL = config.projects[0]?.use.baseURL
  if (typeof baseURL !== 'string' || baseURL.trim() === '') {
    throw new Error('Playwright projects[0].use.baseURL 必须是有效字符串')
  }

  const roles: AuthRole[] = ['platform_admin', 'org_admin', 'org_member']
  for (const role of roles) {
    // 每个角色使用独立 APIRequestContext，防止 cookie jar 在登录之间互相覆盖或串用。
    const context = await request.newContext({ baseURL })
    try {
      const response = await context.post('/api/v1/auth/login', {
        data: loginPayloadForRole(fixture, role),
      })
      if (!response.ok()) {
        // 非 2xx 响应体只用于定位登录错误；正常响应绝不进入日志，避免泄漏 token。
        const body = await response.text()
        throw new Error(`角色 ${role} 登录失败：status=${response.status()} body=${body}`)
      }

      const tokens = loginTokensFromPayload(await response.json(), role)
      const apiState = await context.storageState()
      const state = buildStorageState(baseURL, tokens, apiState.cookies, 'zh')
      const path = authStatePath(fixture.run_id, fixture.worker_index, role)
      await mkdir(dirname(path), { recursive: true })
      await writeFile(path, JSON.stringify(state), 'utf8')
    } finally {
      // 即使登录、校验或写文件失败也关闭当前 context，避免 setup 异常路径泄漏连接与 cookie jar。
      await context.dispose()
    }
  }
}
