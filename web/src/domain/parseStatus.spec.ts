import { describe, it, expect } from 'vitest'
import { parseStatusLabel, parseStatusTagType, PARSE_STATUS_FILTER_OPTIONS } from './parseStatus'

describe('parseStatus', () => {
  it('已知状态返回对应 i18n 键', () => {
    // label 迁移为 i18n 键后，消费方通过 t() 解析为当前语言文案；此处断言键名正确。
    expect(parseStatusLabel('queued')).toBe('domain.parseStatus.queued')
    expect(parseStatusLabel('running')).toBe('domain.parseStatus.running')
    expect(parseStatusLabel('completed')).toBe('domain.parseStatus.completed')
    expect(parseStatusLabel('failed')).toBe('domain.parseStatus.failed')
    expect(parseStatusLabel('stopped')).toBe('domain.parseStatus.stopped')
  })

  it('未知状态原样透出便于排障', () => {
    // 服务端若新增状态，前端不应吞掉，原样显示；未知状态不是 i18n 键，t() 对其原样返回。
    expect(parseStatusLabel('weird')).toBe('weird')
  })

  it('标签色按状态语义映射，未知状态用默认色', () => {
    // parseStatusTagType 不含 i18n，逻辑无变化；断言与迁移前一致。
    expect(parseStatusTagType('completed')).toBe('success')
    expect(parseStatusTagType('queued')).toBe('warning')
    expect(parseStatusTagType('running')).toBe('warning')
    expect(parseStatusTagType('failed')).toBe('error')
    expect(parseStatusTagType('stopped')).toBe('error')
    expect(parseStatusTagType('weird')).toBe('default')
  })

  it('筛选选项覆盖五个状态、value 为状态原值、label 为 i18n 键', () => {
    // 选项 label 已迁移为 i18n 键，消费方在 computed 中通过 t() 解析为当前语言文案。
    expect(PARSE_STATUS_FILTER_OPTIONS).toEqual([
      { label: 'domain.parseStatus.queued',    value: 'queued' },
      { label: 'domain.parseStatus.running',   value: 'running' },
      { label: 'domain.parseStatus.completed', value: 'completed' },
      { label: 'domain.parseStatus.failed',    value: 'failed' },
      { label: 'domain.parseStatus.stopped',   value: 'stopped' },
    ])
  })
})
