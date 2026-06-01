// 状态格式化测试覆盖已知状态映射和未知状态降级展示。
// 未知值必须可见，避免后端新增状态时页面静默显示为空。
import { describe, expect, it } from 'vitest'

import {
  formatAppStatus,
  formatKanbanStatus,
  formatMemberRole,
  formatMemberStatus,
  formatOrgStatus,
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

describe('formatKanbanStatus', () => {
  // 覆盖任务看板全部已知状态：页面应展示中文文案，而不是 Hermes 原始英文状态值。
  it.each([
    ['running', { label: '运行中', tone: 'warning' }], // running：任务正在执行，仍属于过程态。
    ['ready', { label: '就绪', tone: 'warning' }], // ready：任务已准备执行，等待调度。
    ['todo', { label: '待办', tone: 'neutral' }], // todo：任务待处理。
    ['blocked', { label: '阻塞', tone: 'danger' }], // blocked：任务被阻塞，需要人工处理。
    ['triage', { label: '待分诊', tone: 'neutral' }], // triage：任务等待分类或确认。
    ['done', { label: '已完成', tone: 'success' }], // done：任务已完成。
    ['archived', { label: '已归档', tone: 'neutral' }], // archived：任务已归档。
  ] as const)('maps %s to Chinese label', (status, expected) => {
    expect(formatKanbanStatus(status)).toEqual(expected)
  })

  // 覆盖未知状态降级：Hermes 新增状态时仍显示原值，便于定位前端映射未同步。
  it('falls back for unknown Kanban statuses', () => {
    expect(formatKanbanStatus('paused_by_policy')).toEqual({
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
