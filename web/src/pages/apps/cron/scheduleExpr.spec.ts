// scheduleExpr.spec.ts —— schedule 点选器纯逻辑单测。
// 覆盖：日历模式拼 cron、间隔模式拼 every、表达式回填、触发点枚举与笛卡尔积告警。
import { describe, expect, it } from 'vitest'

import {
  buildScheduleExpr,
  parseScheduleExpr,
  describeSchedule,
  defaultScheduleState,
  type ScheduleState,
} from './scheduleExpr'

// 构造日历态的便捷函数，减少样板。
function calendar(partial: Partial<ScheduleState['calendar']>): ScheduleState {
  return {
    ...defaultScheduleState(),
    mode: 'calendar',
    calendar: { frequency: 'daily', weekdays: [], times: [{ hour: 9, minute: 0 }], ...partial },
  }
}

describe('buildScheduleExpr', () => {
  // 每天单时间点：生成最简 cron。
  it('每天 09:00 → cron 0 9 * * *', () => {
    expect(buildScheduleExpr(calendar({ frequency: 'daily', times: [{ hour: 9, minute: 0 }] }))).toBe('cron 0 9 * * *')
  })

  // 每周多日 + 多个同分钟时间点：分钟去重，小时升序列表，dow 升序列表。
  it('每周一三五 09:00 18:00 → cron 0 9,18 * * 1,3,5', () => {
    const expr = buildScheduleExpr(calendar({
      frequency: 'weekly',
      weekdays: [1, 3, 5],
      times: [{ hour: 18, minute: 0 }, { hour: 9, minute: 0 }],
    }))
    expect(expr).toBe('cron 0 9,18 * * 1,3,5')
  })

  // 分钟不一致：分钟字段也变成列表，构成笛卡尔积（多触发，由 describe 暴露）。
  it('分钟不一致 → 分钟与小时都成列表', () => {
    const expr = buildScheduleExpr(calendar({
      frequency: 'daily',
      times: [{ hour: 9, minute: 0 }, { hour: 18, minute: 30 }],
    }))
    expect(expr).toBe('cron 0,30 9,18 * * *')
  })

  // 间隔模式：拼 every 表达式。
  it('间隔 10 分钟 → every 10m', () => {
    const state: ScheduleState = { ...defaultScheduleState(), mode: 'interval', interval: { value: 10, unit: 'm' } }
    expect(buildScheduleExpr(state)).toBe('every 10m')
  })

  // 高级表达式模式：原样输出（trim）。
  it('表达式模式原样输出', () => {
    const state: ScheduleState = { ...defaultScheduleState(), mode: 'expr', expr: '  cron 0 0 1 * *  ' }
    expect(buildScheduleExpr(state)).toBe('cron 0 0 1 * *')
  })

  // 周模式未选任何星期：视为非法，返回空串让 UI 拦截提交。
  it('每周但未选星期 → 空串', () => {
    expect(buildScheduleExpr(calendar({ frequency: 'weekly', weekdays: [] }))).toBe('')
  })

  // 无时间点：返回空串。
  it('无时间点 → 空串', () => {
    expect(buildScheduleExpr(calendar({ times: [] }))).toBe('')
  })
})

describe('parseScheduleExpr', () => {
  // 裸 cron 表达式（无 cron 前缀，列表页 expr 形态）回填日历态，并把小时×分钟笛卡尔展开为时间点。
  it('回填每天多时间点', () => {
    const state = parseScheduleExpr('0 9,18 * * *')
    expect(state.mode).toBe('calendar')
    expect(state.calendar.frequency).toBe('daily')
    expect(state.calendar.times).toEqual([{ hour: 9, minute: 0 }, { hour: 18, minute: 0 }])
  })

  // 带 cron 前缀 + 周列表 + 范围都能解析；dow 7 归一到 0（周日）。
  it('回填每周（范围）', () => {
    const state = parseScheduleExpr('cron 0 9 * * 1-5')
    expect(state.mode).toBe('calendar')
    expect(state.calendar.frequency).toBe('weekly')
    expect(state.calendar.weekdays).toEqual([1, 2, 3, 4, 5])
  })

  // every 表达式回填间隔态。
  it('回填间隔', () => {
    const state = parseScheduleExpr('every 30m')
    expect(state.mode).toBe('interval')
    expect(state.interval).toEqual({ value: 30, unit: 'm' })
  })

  // 无法识别为日历/间隔的复杂表达式（带步进）落到 expr 模式，原值保留。
  it('复杂表达式落到 expr 模式', () => {
    const state = parseScheduleExpr('*/15 * * * *')
    expect(state.mode).toBe('expr')
    expect(state.expr).toBe('*/15 * * * *')
  })

  // 空串返回默认日历态，不抛错。
  it('空串安全降级为默认态', () => {
    expect(parseScheduleExpr('').mode).toBe('calendar')
  })
})

describe('describeSchedule', () => {
  // 同分钟多时间点：预览精确，无告警。
  it('每天 09:00、18:00 无告警', () => {
    const { text, warn } = describeSchedule(calendar({
      frequency: 'daily',
      times: [{ hour: 9, minute: 0 }, { hour: 18, minute: 0 }],
    }))
    expect(text).toBe('每天 09:00、18:00')
    expect(warn).toBe(false)
  })

  // 分钟不一致：枚举出 4 个触发点并告警（用户选了 2 个）。
  it('分钟不一致触发笛卡尔积告警', () => {
    const { text, warn } = describeSchedule(calendar({
      frequency: 'daily',
      times: [{ hour: 9, minute: 0 }, { hour: 18, minute: 30 }],
    }))
    expect(text).toBe('每天 09:00、09:30、18:00、18:30')
    expect(warn).toBe(true)
  })

  // 每周预览带星期文案。
  it('每周预览', () => {
    const { text } = describeSchedule(calendar({ frequency: 'weekly', weekdays: [1, 5], times: [{ hour: 8, minute: 0 }] }))
    expect(text).toBe('周一、周五 08:00')
  })

  // 间隔模式预览。
  it('间隔预览', () => {
    const state: ScheduleState = { ...defaultScheduleState(), mode: 'interval', interval: { value: 2, unit: 'h' } }
    expect(describeSchedule(state).text).toBe('每 2 小时')
  })
})
