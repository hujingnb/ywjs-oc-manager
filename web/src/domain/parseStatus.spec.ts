import { describe, it, expect } from 'vitest'
import { parseStatusLabel, parseStatusTagType, PARSE_STATUS_FILTER_OPTIONS } from './parseStatus'

describe('parseStatus', () => {
  it('已知状态返回中文文案', () => {
    // 覆盖五个已知解析状态的文案映射。
    expect(parseStatusLabel('queued')).toBe('等待解析')
    expect(parseStatusLabel('running')).toBe('解析中')
    expect(parseStatusLabel('completed')).toBe('已完成')
    expect(parseStatusLabel('failed')).toBe('解析失败')
    expect(parseStatusLabel('stopped')).toBe('已停止')
  })

  it('未知状态原样透出便于排障', () => {
    // 服务端若新增状态，前端不应吞掉，原样显示。
    expect(parseStatusLabel('weird')).toBe('weird')
  })

  it('标签色按状态语义映射，未知状态用默认色', () => {
    // completed→success，进行中→warning，失败/停止→error，其它→default。
    expect(parseStatusTagType('completed')).toBe('success')
    expect(parseStatusTagType('queued')).toBe('warning')
    expect(parseStatusTagType('running')).toBe('warning')
    expect(parseStatusTagType('failed')).toBe('error')
    expect(parseStatusTagType('stopped')).toBe('error')
    expect(parseStatusTagType('weird')).toBe('default')
  })

  it('筛选选项覆盖五个状态、value 为状态原值、label 为中文文案', () => {
    // 下拉用此选项；不含「全部」（由 n-select clearable 表达）。
    expect(PARSE_STATUS_FILTER_OPTIONS).toEqual([
      { label: '等待解析', value: 'queued' },
      { label: '解析中', value: 'running' },
      { label: '已完成', value: 'completed' },
      { label: '解析失败', value: 'failed' },
      { label: '已停止', value: 'stopped' },
    ])
  })
})
