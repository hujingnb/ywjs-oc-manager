// permissions.spec.ts 覆盖前端权限 helper 的关键路径，
// 重点验证 org_admin 在 orgId 省略和显式传入两种场景下均可管理自身组织知识库。
import { describe, expect, it } from 'vitest'

import {
  canCreateAppForOrg,
  canManageApp,
  canManageOrgKnowledge,
  canManageRAGFlowDatasetInfo,
  canUpdateAppKnowledgeQuota,
} from './permissions'

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
  it('org_admin 传入跨企业 orgId 时返回 false', () => {
    expect(
      canManageOrgKnowledge({ role: 'org_admin', org_id: 'org-1' }, 'org-2'),
    ).toBe(false)
  })

  // 覆盖 platform_admin 跨组织也可管理组织知识库
  // （运维 / 公共制度文档场景必须由平台侧介入，与后端 CanWriteOrgKnowledge 保持一致）。
  it('platform_admin 跨企业也返回 true', () => {
    expect(
      canManageOrgKnowledge({ role: 'platform_admin', org_id: undefined }, 'org-1'),
    ).toBe(true)
  })

  // 覆盖 platform_admin 不传 orgId 时也允许（管理后台默认入口场景）。
  it('platform_admin 省略 orgId 时返回 true', () => {
    expect(
      canManageOrgKnowledge({ role: 'platform_admin' }),
    ).toBe(true)
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
  it('org_admin 同企业可管', () => {
    expect(canManageApp({ role: 'org_admin', org_id: 'org-1' }, orgApp)).toBe(true)
  })

  // 覆盖 org_admin 跨组织应用不可管理（组织隔离）。
  it('org_admin 跨企业不可管', () => {
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

  // 覆盖 platform_admin 恒可管理应用（运维介入，跨组织也放开写权限）。
  it('platform_admin 跨企业也返回 true', () => {
    expect(canManageApp({ role: 'platform_admin' }, orgApp)).toBe(true)
  })
})

describe('canCreateAppForOrg', () => {
  // 覆盖 org_admin 在自身组织创建应用（正常路径）。
  it('org_admin 自身企业可创建', () => {
    expect(canCreateAppForOrg({ role: 'org_admin', org_id: 'org-1' }, 'org-1')).toBe(true)
  })

  // 覆盖 org_admin 跨组织不可创建（边界保护）。
  it('org_admin 跨企业不可创建', () => {
    expect(canCreateAppForOrg({ role: 'org_admin', org_id: 'org-1' }, 'org-2')).toBe(false)
  })

  // 覆盖 platform_admin 不可通过组织管理员入口创建应用。
  it('platform_admin 返回 false', () => {
    expect(canCreateAppForOrg({ role: 'platform_admin' }, 'org-1')).toBe(false)
  })
})

describe('canUpdateAppKnowledgeQuota', () => {
  // 覆盖平台管理员可编辑任意实例知识库容量（唯一有权角色）。
  it('allows platform admin', () => {
    expect(canUpdateAppKnowledgeQuota({ role: 'platform_admin' })).toBe(true)
  })

  // 覆盖企业管理员不可单独在实例页面调整知识库大小，入口已关闭。
  it('rejects org admin', () => {
    expect(canUpdateAppKnowledgeQuota({ role: 'org_admin', org_id: 'org-1' })).toBe(false)
  })

  // 覆盖普通成员不可编辑容量，即使是自己的实例也不允许。
  it('rejects org member', () => {
    expect(canUpdateAppKnowledgeQuota({ role: 'org_member', id: 'u1', org_id: 'org-1' })).toBe(false)
  })
})

describe('canManageRAGFlowDatasetInfo', () => {
  // 平台管理员可以跨行业库、企业库和实例库执行 RAGFlow 运维操作。
  it('仅平台管理员可查看和修改 RAGFlow dataset 信息', () => {
    expect(canManageRAGFlowDatasetInfo({ role: 'platform_admin' })).toBe(true)
    // 企业管理员不能看到入口，避免触发整库重解析这类平台运维操作。
    expect(canManageRAGFlowDatasetInfo({ role: 'org_admin', org_id: 'org-1' })).toBe(false)
    // 普通成员没有 RAGFlow 远端信息入口。
    expect(canManageRAGFlowDatasetInfo({ role: 'org_member', org_id: 'org-1' })).toBe(false)
  })
})
