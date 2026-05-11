import { describe, expect, it } from 'vitest'

import {
  canCreateAppForOrg,
  canManageApp,
  canManageOrgKnowledge,
  canViewOrgAudit,
  canViewOwnAppAudit,
} from './permissions'

describe('role permissions', () => {
  it('keeps platform admin read-only for organization-side app and knowledge writes', () => {
    const user = { id: 'platform-user', role: 'platform_admin' as const }
    const app = { org_id: 'org-1', owner_user_id: 'member-1' }

    expect(canCreateAppForOrg(user, 'org-1')).toBe(false)
    expect(canManageOrgKnowledge(user, 'org-1')).toBe(false)
    expect(canManageApp(user, app)).toBe(false)
    expect(canViewOrgAudit(user, 'org-1')).toBe(true)
  })

  it('allows org members to view only their own app audit', () => {
    const user = { id: 'member-1', org_id: 'org-1', role: 'org_member' as const }

    expect(canViewOwnAppAudit(user, { org_id: 'org-1', owner_user_id: 'member-1' })).toBe(true)
    expect(canViewOwnAppAudit(user, { org_id: 'org-1', owner_user_id: 'member-2' })).toBe(false)
    expect(canViewOrgAudit(user, 'org-1')).toBe(false)
  })
})
