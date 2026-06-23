// cronDisplay.spec.ts —— 定时任务页面共享展示工具单元测试。
// 覆盖：状态/投递 i18n 映射与兜底、cron/every/at 翻译、display 优先级与原文回退。
// 使用真实 i18n 实例（zh 语言），断言输出与原有中文文案一致，保证翻译正确性。
import { describe, expect, it } from 'vitest'

import type { CronJob } from '@/api/hooks/useCron'
import { i18n } from '@/i18n'

import {
  filterCronJobs,
  scheduleDisplay,
  translateCronExpr,
  translateDeliver,
  translateState,
} from './cronDisplay'

// 使用真实 i18n 实例的 t 函数，设为中文以断言中文输出。
i18n.global.locale.value = 'zh'
const t = i18n.global.t

describe('translateState', () => {
  // 已知状态返回对应中文标签
  it('把已知状态映射为中文', () => {
    expect(translateState('scheduled', t)).toBe('已调度')
    expect(translateState('paused', t)).toBe('已暂停')
    expect(translateState('running', t)).toBe('运行中')
    expect(translateState('disabled', t)).toBe('已禁用')
    expect(translateState('error', t)).toBe('错误')
    expect(translateState('removed', t)).toBe('已移除')
  })
  // 空值回退 unknown，与列表旧文案保持一致，避免出现空白标签
  it('空值回退 unknown', () => {
    expect(translateState(undefined, t)).toBe('unknown')
    expect(translateState('', t)).toBe('unknown')
  })
  // 未知状态原样返回，保证未来上游新增状态不被吞掉
  it('未知状态原样返回', () => {
    expect(translateState('weird', t)).toBe('weird')
  })
})

describe('translateDeliver', () => {
  // 已知投递渠道返回中文
  it('把投递渠道映射为中文', () => {
    expect(translateDeliver('wechat', t)).toBe('微信')
    expect(translateDeliver('email', t)).toBe('邮件')
    expect(translateDeliver('none', t)).toBe('不投递')
  })
  // 空值回退占位符
  it('空值回退 —', () => {
    expect(translateDeliver(undefined, t)).toBe('—')
    expect(translateDeliver('', t)).toBe('—')
  })
  // 未知渠道原样返回
  it('未知渠道原样返回', () => {
    expect(translateDeliver('sms', t)).toBe('sms')
  })
})

describe('translateCronExpr', () => {
  // 标准 5 段 cron：每天固定时刻
  it('每天固定时刻', () => {
    expect(translateCronExpr('cron', '0 9 * * *', t)).toBe('每天 09:00')
    expect(translateCronExpr('cron', '30 18 * * *', t)).toBe('每天 18:30')
  })
  // 每周某天固定时刻，dow 0 与 7 都代表周日
  it('每周某天固定时刻', () => {
    expect(translateCronExpr('cron', '0 10 * * 1', t)).toBe('每周一 10:00')
    expect(translateCronExpr('cron', '0 10 * * 0', t)).toBe('每周日 10:00')
    expect(translateCronExpr('cron', '0 10 * * 7', t)).toBe('每周日 10:00')
  })
  // 每月某日固定时刻
  it('每月某日固定时刻', () => {
    expect(translateCronExpr('cron', '0 8 15 * *', t)).toBe('每月15日 08:00')
  })
  // 每小时（分钟固定、小时通配）
  it('每小时', () => {
    expect(translateCronExpr('cron', '0 * * * *', t)).toBe('每小时')
  })
  // 每 N 分钟：*/N 步进
  it('每 N 分钟（步进）', () => {
    expect(translateCronExpr('cron', '*/5 * * * *', t)).toBe('每 5 分钟')
  })
  // every 格式：分钟与小时
  it('every 格式', () => {
    expect(translateCronExpr('every', 'every 10m', t)).toBe('每 10 分钟')
    expect(translateCronExpr('every', '10m', t)).toBe('每 10 分钟')
    expect(translateCronExpr('every', 'every 2h', t)).toBe('每 2 小时')
    // 秒级间隔（interval 任务可能出现）
    expect(translateCronExpr('interval', 'every 30s', t)).toBe('每 30 秒')
  })
  // at 格式：一次性绝对时间，保留原始时间串
  it('at 格式保留原始时间', () => {
    expect(translateCronExpr('at', 'at 2026-06-03 09:00', t)).toBe('指定时间 2026-06-03 09:00')
  })
  // at 类型但 expr 无 'at ' 前缀（kind 已携带类型，expr 为裸时间串）
  it('at 类型无前缀时直接加中文前缀', () => {
    expect(translateCronExpr('at', '2026-06-03 09:00', t)).toBe('指定时间 2026-06-03 09:00')
  })
  // 不可识别的复杂表达式回退原文，且不抛错
  it('无法识别时回退原文', () => {
    expect(translateCronExpr('cron', '0 9 1-5 * 1,3,5', t)).toBe('0 9 1-5 * 1,3,5')
    expect(translateCronExpr('', '', t)).toBe('')
  })
})

describe('scheduleDisplay', () => {
  // 关键回归：实测 cron 任务 oc-cron 的 display 只是回显原始 expr，必须走前端翻译
  it('cron 任务 display 回显 expr 时翻译为中文', () => {
    expect(scheduleDisplay({ kind: 'cron', expr: '0 9 * * *', display: '0 9 * * *' }, t)).toBe('每天 09:00')
  })
  // 关键回归：实测 interval 任务没有 expr，表达式以英文放在 display，需取 display 翻译
  it('interval 任务表达式在 display 时翻译为中文', () => {
    expect(scheduleDisplay({ kind: 'interval', display: 'every 10m' }, t)).toBe('每 10 分钟')
  })
  // 仅 expr、无 display 时翻译为中文
  it('仅 expr 时翻译为中文', () => {
    expect(scheduleDisplay({ kind: 'cron', expr: '0 9 * * *' }, t)).toBe('每天 09:00')
  })
  // 翻不动的复杂表达式：保留上游 display 作为更友好的描述
  it('无法翻译时保留上游 display', () => {
    expect(scheduleDisplay({ kind: 'cron', expr: '0 9 * * 1-5', display: '工作日早九点' }, t)).toBe('工作日早九点')
  })
  // 翻不动且无 display：退回原始表达式，不显示空白
  it('无法翻译且无 display 时退回原始表达式', () => {
    expect(scheduleDisplay({ kind: 'cron', expr: '0 9 1-5 * 1,3,5' }, t)).toBe('0 9 1-5 * 1,3,5')
  })
  // 全部缺失返回占位符
  it('全部缺失返回 —', () => {
    expect(scheduleDisplay(undefined, t)).toBe('—')
    expect(scheduleDisplay({}, t)).toBe('—')
  })
})

describe('filterCronJobs', () => {
  // 构造一组覆盖不同状态与名称/prompt 的任务
  const jobs: CronJob[] = [
    { id: '1', name: '晨间周报', prompt: '生成晨间周报', state: 'scheduled' },
    { id: '2', name: '夜间巡检', prompt: '巡检并上报', state: 'paused' },
    { id: '3', name: '月度结算', prompt: '结算账单', state: 'error' },
  ]

  // 无搜索无状态：返回全部
  it('无筛选返回全部', () => {
    expect(filterCronJobs(jobs, '', '').map((j) => j.id)).toEqual(['1', '2', '3'])
  })
  // 状态筛选按 job.state 精确匹配：已暂停只剩夜间巡检
  it('按状态精确筛选', () => {
    expect(filterCronJobs(jobs, '', 'paused').map((j) => j.id)).toEqual(['2'])
  })
  // 搜索按名称子串匹配：巡检命中夜间巡检
  it('按名称子串搜索', () => {
    expect(filterCronJobs(jobs, '巡检', '').map((j) => j.id)).toEqual(['2'])
  })
  // 搜索也匹配 prompt：结算命中月度结算（prompt 含"结算"）
  it('搜索同时匹配 prompt', () => {
    expect(filterCronJobs(jobs, '结算', '').map((j) => j.id)).toEqual(['3'])
  })
  // 搜索与状态并存为 AND：状态 scheduled 且名称含"报"只剩晨间周报
  it('搜索与状态为 AND 关系', () => {
    expect(filterCronJobs(jobs, '报', 'scheduled').map((j) => j.id)).toEqual(['1'])
  })
  // 搜索不区分大小写并 trim 首尾空白
  it('搜索 trim 且不区分大小写', () => {
    const mixed: CronJob[] = [{ id: 'a', name: 'Daily-Report', state: 'scheduled' }]
    expect(filterCronJobs(mixed, '  daily  ', '').map((j) => j.id)).toEqual(['a'])
  })
  // 无匹配返回空数组
  it('无匹配返回空', () => {
    expect(filterCronJobs(jobs, '不存在XYZ', '')).toEqual([])
  })
})
