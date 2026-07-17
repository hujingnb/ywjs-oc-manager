// auth-state 测试锁定 worker 级登录态的持久化格式与三类角色登录请求边界。
import { describe, expect, it } from 'vitest'

import { buildStorageState, loginPayloadForRole } from './auth-state'
import type { E2EFixture } from './suite'

// validFixture 构造完整 worker fixture，避免纯 helper 测试依赖真实 seed 或网络。
function validFixture(): E2EFixture {
  return {
    run_id: 'run-a',
    worker_index: 0,
    platform_admin_login: 'platform-admin',
    platform_admin_password: 'platform-password',
    org_id: 'org-id',
    org_name: 'org-name',
    org_code: 'e2e-run-a-w0',
    org_admin_login: 'org-admin',
    org_admin_password: 'org-admin-password',
    org_member_login: 'org-member',
    org_member_password: 'org-member-password',
    app_id: 'app-id',
    app_name: 'app-name',
  }
}

describe('worker 级角色认证状态', () => {
  // 登录态必须写到 baseURL 对应 origin，并完整保留登录上下文取得的 CSRF cookie。
  it('写入 token、语言和原始 cookie', () => {
    const cookies = [{
      name: 'csrf_token',
      value: 'csrf-value',
      domain: 'ocm.localhost',
      path: '/',
      expires: 1_800_000_000,
      httpOnly: false,
      secure: false,
      sameSite: 'Lax' as const,
    }]

    const state = buildStorageState(
      'http://ocm.localhost/nested/path',
      { access_token: 'access-value', refresh_token: 'refresh-value' },
      cookies,
      'zh',
    )

    expect(state.cookies).toEqual(cookies)
    expect(state.origins).toEqual([{
      origin: 'http://ocm.localhost',
      localStorage: [
        { name: 'ocm.access_token', value: 'access-value' },
        { name: 'ocm.refresh_token', value: 'refresh-value' },
        { name: 'ocm.locale', value: 'zh' },
      ],
    }])
  })

  // 缺少 access token 时不得生成部分登录态，避免业务 spec 到页面加载阶段才隐式退回登录页。
  it('拒绝缺少 access token 的响应', () => {
    expect(() => buildStorageState(
      'http://ocm.localhost',
      { access_token: '', refresh_token: 'refresh-value' },
      [],
      'en',
    )).toThrow('access_token')
  })

  // 缺少 refresh token 同样说明登录响应不符合契约，setup 必须立即失败并触发整轮清理。
  it('拒绝缺少 refresh token 的响应', () => {
    expect(() => buildStorageState(
      'http://ocm.localhost',
      { access_token: 'access-value', refresh_token: '' },
      [],
      'en',
    )).toThrow('refresh_token')
  })

  // 平台管理员必须省略 org_code，否则后端会把它误判为组织登录并拒绝凭据。
  it('平台管理员登录请求不携带 org_code', () => {
    expect(loginPayloadForRole(validFixture(), 'platform_admin')).toEqual({
      username: 'platform-admin',
      password: 'platform-password',
    })
  })

  // 两类组织角色均必须携带 worker 独占企业标识，防止同名账号跨组织串用。
  it.each([
    // org_admin 覆盖组织管理员凭据映射。
    { role: 'org_admin' as const, username: 'org-admin', password: 'org-admin-password' },
    // org_member 覆盖普通成员凭据映射。
    { role: 'org_member' as const, username: 'org-member', password: 'org-member-password' },
  ])('$role 登录请求携带 org_code', ({ role, username, password }) => {
    expect(loginPayloadForRole(validFixture(), role)).toEqual({
      username,
      password,
      org_code: 'e2e-run-a-w0',
    })
  })
})
