// 状态格式化测试覆盖已知状态映射和未知状态降级展示。
// 未知值必须可见，避免后端新增状态时页面静默显示为空。
import { describe, expect, it } from 'vitest'

import {
  formatAppStatus,
  formatMemberRole,
  formatMemberStatus,
  formatOrgStatus,
  formatRuntimeNodeStatus,
} from './status'

describe('formatAppStatus', () => {
  it('formats known app statuses', () => {
    expect(formatAppStatus('running')).toEqual({ label: '运行中', tone: 'success' })
    expect(formatAppStatus('binding_failed')).toEqual({ label: '绑定失败', tone: 'danger' })
    // pulling_runtime_image 是 4 阶段 init 的第一阶段，必须有对应中文标签
    expect(formatAppStatus('pulling_runtime_image')).toEqual({ label: '拉取运行时镜像', tone: 'warning' })
  })

  it('keeps unknown statuses visible', () => {
    expect(formatAppStatus('paused_by_policy')).toEqual({
      label: '未知状态：paused_by_policy',
      tone: 'warning',
    })
  })
})

describe('formatOrgStatus', () => {
  it('maps active and disabled to readable labels', () => {
    expect(formatOrgStatus('active')).toEqual({ label: '启用', tone: 'success' })
    expect(formatOrgStatus('disabled')).toEqual({ label: '禁用', tone: 'warning' })
  })
})

describe('formatMemberStatus', () => {
  it('returns warning fallback for unknown statuses', () => {
    expect(formatMemberStatus('locked')).toEqual({ label: '未知状态：locked', tone: 'warning' })
  })
})

describe('formatMemberRole', () => {
  it('translates roles into Chinese labels', () => {
    expect(formatMemberRole('org_admin')).toBe('企业管理员')
    expect(formatMemberRole('org_member')).toBe('企业成员')
    expect(formatMemberRole('platform_admin')).toBe('平台管理员')
  })

  it('falls back to raw value for unknown roles', () => {
    expect(formatMemberRole('billing_owner')).toBe('billing_owner')
  })
})

describe('formatRuntimeNodeStatus', () => {
  it('maps known runtime node statuses', () => {
    expect(formatRuntimeNodeStatus('active')).toEqual({ label: '在线', tone: 'success' })
    expect(formatRuntimeNodeStatus('unreachable')).toEqual({ label: '失联', tone: 'danger' })
  })

  it('falls back for unknown status', () => {
    expect(formatRuntimeNodeStatus('decommissioned')).toEqual({
      label: '未知状态：decommissioned',
      tone: 'warning',
    })
  })
})
