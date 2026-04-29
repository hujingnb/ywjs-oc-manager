import { describe, expect, it } from 'vitest'

import { formatAppStatus } from './status'

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
