export type Role = 'platform_admin' | 'org_admin' | 'org_member'

export interface PermissionUser {
  id?: string
  org_id?: string
  role?: Role | string
}

export interface PermissionApp {
  org_id?: string
  owner_user_id?: string
}

// 组织侧创建应用必须经组织管理员入口；平台管理员只保留跨组织观察能力。
export function canCreateAppForOrg(user: PermissionUser | null | undefined, orgId?: string): boolean {
  return user?.role === 'org_admin' && Boolean(orgId) && user.org_id === orgId
}

// 组织知识库写入会影响组织共享上下文，只允许本组织管理员执行。
export function canManageOrgKnowledge(user: PermissionUser | null | undefined, orgId?: string): boolean {
  return user?.role === 'org_admin' && Boolean(orgId) && user.org_id === orgId
}

// 应用写操作包含运行时、渠道、API key 与应用知识库变更。
export function canManageApp(user: PermissionUser | null | undefined, app: PermissionApp | null | undefined): boolean {
  if (!user || !app) return false
  if (user.role === 'org_admin') return user.org_id === app.org_id
  if (user.role === 'org_member') return user.id === app.owner_user_id
  return false
}

// 组织级审计是管理视角；普通成员只能进入自己的应用审计。
export function canViewOrgAudit(user: PermissionUser | null | undefined, orgId?: string): boolean {
  if (!user || !orgId) return false
  return user.role === 'platform_admin' || (user.role === 'org_admin' && user.org_id === orgId)
}

// 应用审计是 target 视角：平台管理员可观察全部，组织管理员限本组织，成员限 owner。
export function canViewOwnAppAudit(user: PermissionUser | null | undefined, app: PermissionApp | null | undefined): boolean {
  if (!user || !app) return false
  if (user.role === 'platform_admin') return true
  if (user.role === 'org_admin') return user.org_id === app.org_id
  if (user.role === 'org_member') return user.id === app.owner_user_id
  return false
}
