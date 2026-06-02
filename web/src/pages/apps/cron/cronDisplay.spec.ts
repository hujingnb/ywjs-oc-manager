// cronDisplay.spec.ts —— 定时任务页面共享展示工具单元测试。
// 覆盖：状态/投递中文映射与兜底、cron/every/at 翻译、display 优先级与原文回退。
import { describe, expect, it } from 'vitest'

import {
  scheduleDisplay,
  translateCronExpr,
  translateDeliver,
  translateState,
} from './cronDisplay'

describe('translateState', () => {
  // 已知状态返回对应中文标签
  it('把已知状态映射为中文', () => {
    expect(translateState('scheduled')).toBe('已调度')
    expect(translateState('paused')).toBe('已暂停')
    expect(translateState('running')).toBe('运行中')
    expect(translateState('disabled')).toBe('已禁用')
    expect(translateState('error')).toBe('错误')
    expect(translateState('removed')).toBe('已移除')
  })
  // 空值回退 unknown，与列表旧文案保持一致，避免出现空白标签
  it('空值回退 unknown', () => {
    expect(translateState(undefined)).toBe('unknown')
    expect(translateState('')).toBe('unknown')
  })
  // 未知状态原样返回，保证未来上游新增状态不被吞掉
  it('未知状态原样返回', () => {
    expect(translateState('weird')).toBe('weird')
  })
})

describe('translateDeliver', () => {
  // 已知投递渠道返回中文
  it('把投递渠道映射为中文', () => {
    expect(translateDeliver('wechat')).toBe('微信')
    expect(translateDeliver('email')).toBe('邮件')
    expect(translateDeliver('none')).toBe('不投递')
  })
  // 空值回退占位符
  it('空值回退 —', () => {
    expect(translateDeliver(undefined)).toBe('—')
    expect(translateDeliver('')).toBe('—')
  })
  // 未知渠道原样返回
  it('未知渠道原样返回', () => {
    expect(translateDeliver('sms')).toBe('sms')
  })
})

describe('translateCronExpr', () => {
  // 标准 5 段 cron：每天固定时刻
  it('每天固定时刻', () => {
    expect(translateCronExpr('cron', '0 9 * * *')).toBe('每天 09:00')
    expect(translateCronExpr('cron', '30 18 * * *')).toBe('每天 18:30')
  })
  // 每周某天固定时刻，dow 0 与 7 都代表周日
  it('每周某天固定时刻', () => {
    expect(translateCronExpr('cron', '0 10 * * 1')).toBe('每周一 10:00')
    expect(translateCronExpr('cron', '0 10 * * 0')).toBe('每周日 10:00')
    expect(translateCronExpr('cron', '0 10 * * 7')).toBe('每周日 10:00')
  })
  // 每月某日固定时刻
  it('每月某日固定时刻', () => {
    expect(translateCronExpr('cron', '0 8 15 * *')).toBe('每月15日 08:00')
  })
  // 每小时（分钟固定、小时通配）
  it('每小时', () => {
    expect(translateCronExpr('cron', '0 * * * *')).toBe('每小时')
  })
  // 每 N 分钟：*/N 步进
  it('每 N 分钟（步进）', () => {
    expect(translateCronExpr('cron', '*/5 * * * *')).toBe('每 5 分钟')
  })
  // every 格式：分钟与小时
  it('every 格式', () => {
    expect(translateCronExpr('every', 'every 10m')).toBe('每 10 分钟')
    expect(translateCronExpr('every', '10m')).toBe('每 10 分钟')
    expect(translateCronExpr('every', 'every 2h')).toBe('每 2 小时')
  })
  // at 格式：一次性绝对时间，保留原始时间串
  it('at 格式保留原始时间', () => {
    expect(translateCronExpr('at', 'at 2026-06-03 09:00')).toBe('指定时间 2026-06-03 09:00')
  })
  // at 类型但 expr 无 'at ' 前缀（kind 已携带类型，expr 为裸时间串）
  it('at 类型无前缀时直接加中文前缀', () => {
    expect(translateCronExpr('at', '2026-06-03 09:00')).toBe('指定时间 2026-06-03 09:00')
  })
  // 不可识别的复杂表达式回退原文，且不抛错
  it('无法识别时回退原文', () => {
    expect(translateCronExpr('cron', '0 9 1-5 * 1,3,5')).toBe('0 9 1-5 * 1,3,5')
    expect(translateCronExpr('', '')).toBe('')
  })
})

describe('scheduleDisplay', () => {
  // display 非空时优先使用上游文案，不触发前端翻译
  it('优先使用上游 display', () => {
    expect(scheduleDisplay({ kind: 'cron', expr: '0 9 * * *', display: '上游文案' })).toBe('上游文案')
  })
  // display 缺失时走前端兜底翻译
  it('display 缺失走兜底翻译', () => {
    expect(scheduleDisplay({ kind: 'cron', expr: '0 9 * * *' })).toBe('每天 09:00')
  })
  // 全部缺失返回占位符
  it('全部缺失返回 —', () => {
    expect(scheduleDisplay(undefined)).toBe('—')
    expect(scheduleDisplay({})).toBe('—')
  })
})
