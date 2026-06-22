// 状态格式化测试覆盖已知状态映射和未知状态降级展示。
// label 迁移为 i18n 键后，测试断言键名（已知状态）或降级键+params（未知状态）。
import { describe, expect, it } from 'vitest'

import {
  formatAppStatus,
  formatKanbanStatus,
  formatMemberRole,
  formatMemberStatus,
  formatOrgStatus,
} from './status'

describe('formatAppStatus', () => {
  it('formats known app statuses to i18n keys', () => {
    // 已知状态返回对应的 i18n 键，由消费方（StatusBadge）通过 t() 解析为文案。
    expect(formatAppStatus('running')).toEqual({ label: 'domain.appStatus.running', tone: 'success' })
    expect(formatAppStatus('binding_failed')).toEqual({ label: 'domain.appStatus.binding_failed', tone: 'danger' })
    // pulling_runtime_image 是 4 阶段 init 的第一阶段，必须有对应 i18n 键
    expect(formatAppStatus('pulling_runtime_image')).toEqual({ label: 'domain.appStatus.pulling_runtime_image', tone: 'warning' })
  })

  it('returns unknown key with params for unrecognized statuses', () => {
    // 未知状态返回降级键 + params，消费方通过 t(label, params) 展示含原始状态值的降级文案。
    expect(formatAppStatus('paused_by_policy')).toEqual({
      label: 'domain.appStatus.unknown',
      tone: 'warning',
      params: { status: 'paused_by_policy' },
    })
  })
})

describe('formatKanbanStatus', () => {
  // 覆盖任务看板全部已知状态：label 为 i18n 键，tone 语义不变。
  it.each([
    ['running',  { label: 'domain.kanbanStatus.running',  tone: 'warning' }], // running：任务正在执行，过程态。
    ['ready',    { label: 'domain.kanbanStatus.ready',    tone: 'warning' }], // ready：等待调度。
    ['todo',     { label: 'domain.kanbanStatus.todo',     tone: 'neutral' }], // todo：待处理。
    ['blocked',  { label: 'domain.kanbanStatus.blocked',  tone: 'danger' }],  // blocked：被阻塞，需人工处理。
    ['triage',   { label: 'domain.kanbanStatus.triage',   tone: 'neutral' }], // triage：等待分类或确认。
    ['done',     { label: 'domain.kanbanStatus.done',     tone: 'success' }], // done：已完成。
    ['archived', { label: 'domain.kanbanStatus.archived', tone: 'neutral' }], // archived：已归档。
  ] as const)('maps %s to i18n key', (status, expected) => {
    expect(formatKanbanStatus(status)).toEqual(expected)
  })

  // 覆盖未知状态降级：返回降级键 + params，Hermes 新增状态时原始值仍可诊断。
  it('falls back for unknown Kanban statuses', () => {
    expect(formatKanbanStatus('paused_by_policy')).toEqual({
      label: 'domain.kanbanStatus.unknown',
      tone: 'warning',
      params: { status: 'paused_by_policy' },
    })
  })
})

describe('formatOrgStatus', () => {
  it('maps active and disabled to i18n keys', () => {
    // 已知组织状态返回对应 i18n 键。
    expect(formatOrgStatus('active')).toEqual({ label: 'domain.orgStatus.active', tone: 'success' })
    expect(formatOrgStatus('disabled')).toEqual({ label: 'domain.orgStatus.disabled', tone: 'warning' })
  })
})

describe('formatMemberStatus', () => {
  it('returns unknown key with params for unrecognized statuses', () => {
    // 未知成员状态同样走降级键 + params 路径。
    expect(formatMemberStatus('locked')).toEqual({ label: 'domain.memberStatus.unknown', tone: 'warning', params: { status: 'locked' } })
  })
})

describe('formatMemberRole', () => {
  it('translates roles into i18n keys', () => {
    // 已知角色返回 i18n 键，由消费方通过 t() 解析为当前语言文案。
    expect(formatMemberRole('org_admin')).toBe('domain.memberRole.org_admin')
    expect(formatMemberRole('org_member')).toBe('domain.memberRole.org_member')
    expect(formatMemberRole('platform_admin')).toBe('domain.memberRole.platform_admin')
  })

  it('falls back to raw value for unknown roles', () => {
    // 未知角色原样返回，消费方 t() 调用对非键字符串原样透出，避免页面空白。
    expect(formatMemberRole('billing_owner')).toBe('billing_owner')
  })
})
