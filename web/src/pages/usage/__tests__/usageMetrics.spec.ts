import { describe, expect, it } from 'vitest'

import type { AggregatedUsage } from '@/api/hooks/useUsage'

import {
  buildTrendPoints,
  normalizeModelName,
  normalizeUsageDate,
  summarizeUsage,
} from '../usageMetrics'

describe('usage metrics helpers', () => {
  it('为空模型名提供稳定兜底文案', () => {
    expect(normalizeModelName('')).toBe('未知模型')
    expect(normalizeModelName(null)).toBe('未知模型')
    expect(normalizeModelName(' gpt-4o-mini ')).toBe('gpt-4o-mini')
  })

  it('从日志时间补齐日期字段', () => {
    expect(normalizeUsageDate({ created_at: 1778562000 })).toBe('2026-05-12')
  })

  it('按平台聚合响应汇总 token、金额额度和请求数', () => {
    const usage: AggregatedUsage = {
      scope: 'platform',
      items: [{ token_used: 10, quota: 5, count: 2, model_name: '' }],
      updated_at: '2026-05-12T00:00:00Z',
    }

    expect(summarizeUsage(usage)).toMatchObject({
      totalTokens: 10,
      totalQuota: 5,
      totalCount: 2,
      modelCount: 1,
    })
  })

  it('按成员日志响应汇总 token、金额额度和分页总数', () => {
    const usage: AggregatedUsage = {
      scope: 'member',
      items: [{ prompt_tokens: 3, completion_tokens: 7, quota: 4, model_name: 'm' }],
      total: 1,
      updated_at: '2026-05-12T00:00:00Z',
    }

    expect(summarizeUsage(usage)).toMatchObject({
      totalTokens: 10,
      totalQuota: 4,
      totalCount: 1,
      modelCount: 1,
    })
  })

  it('按日期生成折线图数据并跳过无日期记录', () => {
    const usage: AggregatedUsage = {
      scope: 'member',
      items: [
        { created_at: 1778562000, prompt_tokens: 3, completion_tokens: 7, quota: 4 },
        { date: '2026-05-12', token_used: 2, quota: 1, count: 2 },
        { model_name: 'no-date', quota: 9 },
      ],
      updated_at: '2026-05-12T00:00:00Z',
    }

    expect(buildTrendPoints(usage)).toEqual([{ date: '2026-05-12', tokens: 12, quota: 5, count: 3 }])
  })
})
