import { describe, expect, it } from 'vitest'

import { formatDisplayAmount, formatQuotaValue, type BillingStatusDTO } from '../usageFormatting'

describe('usage formatting helpers', () => {
  // 有 billing status 时，quota 按 quota_per_unit 折算后加 ￥ 前缀
  it('按 quota_per_unit 折算并展示 ￥ 前缀', () => {
    const status: BillingStatusDTO = {
      quota_per_unit: 500000,
      quota_display_type: 'USD',
      display_in_currency: true,
      custom_currency_symbol: '¤',
      custom_currency_exchange_rate: 1,
      usd_exchange_rate: 7.3,
      price: 7.3,
    }

    expect(formatQuotaValue(500000, status)).toBe('￥1')
  })

  // 没有 billing status 时，直接展示原始数值加 ￥ 前缀
  it('没有 billing status 时直接展示原始 quota 加 ￥', () => {
    expect(formatQuotaValue(500000, undefined)).toBe('￥500,000')
  })

  // formatDisplayAmount 统一加 ￥ 前缀，不依赖 quota_display_type
  it('充值金额统一展示 ￥ 前缀', () => {
    const status: BillingStatusDTO = {
      quota_per_unit: 500000,
      quota_display_type: 'USD',
      display_in_currency: true,
    }

    expect(formatDisplayAmount(10, status)).toBe('￥10')
  })
})
