// 权限 helper 测试覆盖前端入口控制的关键角色边界。
// 后端授权仍是最终来源，这里只验证页面显示逻辑不会放宽写权限。
import { describe, expect, it } from 'vitest'

import {
  canCreateAppForOrg,
  canManageApp,
  canManageOrgKnowledge,
  canViewOrgAudit,
  canViewOwnAppAudit,
} from './permissions'

describe('role permissions', () => {
  // 覆盖平台管理员：不能通过组织入口创建/管理实例，但可作为运维兜底维护组织知识库并查看审计。
  it('keeps platform admin read-only for organization-side app writes while allowing org knowledge maintenance', () => {
    const user = { id: 'platform-user', role: 'platform_admin' as const }
    const app = { org_id: 'org-1', owner_user_id: 'member-1' }

    expect(canCreateAppForOrg(user, 'org-1')).toBe(false)
    expect(canManageOrgKnowledge(user, 'org-1')).toBe(true)
    expect(canManageApp(user, app)).toBe(false)
    expect(canViewOrgAudit(user, 'org-1')).toBe(true)
  })

  // 覆盖普通成员：只能查看自己拥有实例的审计，不能查看他人实例或组织级审计。
  it('allows org members to view only their own app audit', () => {
    const user = { id: 'member-1', org_id: 'org-1', role: 'org_member' as const }

    expect(canViewOwnAppAudit(user, { org_id: 'org-1', owner_user_id: 'member-1' })).toBe(true)
    expect(canViewOwnAppAudit(user, { org_id: 'org-1', owner_user_id: 'member-2' })).toBe(false)
    expect(canViewOrgAudit(user, 'org-1')).toBe(false)
  })
})
