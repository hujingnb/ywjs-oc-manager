// permissions.spec.ts 覆盖前端权限 helper 的关键路径，
// 重点验证 org_admin 在 orgId 省略和显式传入两种场景下均可管理自身组织知识库。
import { describe, expect, it } from 'vitest'

import { canManageOrgKnowledge, canManageApp, canCreateAppForOrg } from './permissions'

describe('canManageOrgKnowledge', () => {
  // 覆盖 ORG_ADMIN 显式传入自身 orgId 时可管理知识库（基准路径）。
  it('org_admin 显式传入自身 orgId 时返回 true', () => {
    expect(
      canManageOrgKnowledge({ role: 'org_admin', org_id: 'org-1' }, 'org-1'),
    ).toBe(true)
  })

  // 覆盖 ORG_ADMIN 不传 orgId（页面未显式传入组织上下文）时仍可管理自身组织知识库。
  // Bug 场景：/knowledge 路由不传 orgId prop，effectiveOrgId 在初始化阶段可能为 undefined，
  // 此时 canManageOrgKnowledge 必须回退到 user.org_id 而非返回 false。
  it('org_admin 省略 orgId 时回退到自身 org_id 返回 true', () => {
    expect(
      canManageOrgKnowledge({ role: 'org_admin', org_id: 'org-1' }),
    ).toBe(true)
  })

  // 覆盖 ORG_ADMIN 传入跨组织 orgId 时无权管理（组织边界隔离）。
  it('org_admin 传入跨组织 orgId 时返回 false', () => {
    expect(
      canManageOrgKnowledge({ role: 'org_admin', org_id: 'org-1' }, 'org-2'),
    ).toBe(false)
  })

  // 覆盖 platform_admin 无法管理组织知识库（只读观察视角）。
  it('platform_admin 返回 false', () => {
    expect(
      canManageOrgKnowledge({ role: 'platform_admin', org_id: undefined }, 'org-1'),
    ).toBe(false)
  })

  // 覆盖 org_member 无法管理组织知识库（仅 org_admin 可写）。
  it('org_member 返回 false', () => {
    expect(
      canManageOrgKnowledge({ role: 'org_member', org_id: 'org-1' }, 'org-1'),
    ).toBe(false)
  })

  // 覆盖 user 为 null 时返回 false（未登录保护）。
  it('user 为 null 时返回 false', () => {
    expect(canManageOrgKnowledge(null, 'org-1')).toBe(false)
  })

  // 覆盖 org_admin 自身 org_id 为空时，即使省略 orgId 也返回 false（数据异常保护）。
  it('org_admin 自身 org_id 为空且省略 orgId 时返回 false', () => {
    expect(
      canManageOrgKnowledge({ role: 'org_admin', org_id: undefined }),
    ).toBe(false)
  })
})

describe('canManageApp', () => {
  const orgApp = { org_id: 'org-1', owner_user_id: 'user-1' }

  // 覆盖 org_admin 同组织应用可管理。
  it('org_admin 同组织可管', () => {
    expect(canManageApp({ role: 'org_admin', org_id: 'org-1' }, orgApp)).toBe(true)
  })

  // 覆盖 org_admin 跨组织应用不可管理（组织隔离）。
  it('org_admin 跨组织不可管', () => {
    expect(canManageApp({ role: 'org_admin', org_id: 'org-2' }, orgApp)).toBe(false)
  })

  // 覆盖 org_member 管理自己拥有的应用。
  it('org_member 可管理自己的应用', () => {
    expect(canManageApp({ role: 'org_member', id: 'user-1' }, orgApp)).toBe(true)
  })

  // 覆盖 org_member 不可管理他人应用（成员边界）。
  it('org_member 不可管理他人应用', () => {
    expect(canManageApp({ role: 'org_member', id: 'user-2' }, orgApp)).toBe(false)
  })

  // 覆盖 platform_admin 不可管理应用（无写权限）。
  it('platform_admin 返回 false', () => {
    expect(canManageApp({ role: 'platform_admin' }, orgApp)).toBe(false)
  })
})

describe('canCreateAppForOrg', () => {
  // 覆盖 org_admin 在自身组织创建应用（正常路径）。
  it('org_admin 自身组织可创建', () => {
    expect(canCreateAppForOrg({ role: 'org_admin', org_id: 'org-1' }, 'org-1')).toBe(true)
  })

  // 覆盖 org_admin 跨组织不可创建（边界保护）。
  it('org_admin 跨组织不可创建', () => {
    expect(canCreateAppForOrg({ role: 'org_admin', org_id: 'org-1' }, 'org-2')).toBe(false)
  })

  // 覆盖 platform_admin 不可通过组织管理员入口创建应用。
  it('platform_admin 返回 false', () => {
    expect(canCreateAppForOrg({ role: 'platform_admin' }, 'org-1')).toBe(false)
  })
})
