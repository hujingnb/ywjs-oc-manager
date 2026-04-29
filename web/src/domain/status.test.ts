import { describe, expect, it } from 'vitest'

import {
  formatAppStatus,
  formatMemberRole,
  formatMemberStatus,
  formatOrgStatus,
} from './status'

describe('formatAppStatus', () => {
  it('formats known app statuses', () => {
    expect(formatAppStatus('running')).toEqual({ label: '运行中', tone: 'success' })
    expect(formatAppStatus('binding_failed')).toEqual({ label: '绑定失败', tone: 'danger' })
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
    expect(formatMemberRole('org_admin')).toBe('组织管理员')
    expect(formatMemberRole('platform_admin')).toBe('平台管理员')
  })

  it('falls back to raw value for unknown roles', () => {
    expect(formatMemberRole('billing_owner')).toBe('billing_owner')
  })
})
