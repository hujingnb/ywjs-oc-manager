import { describe, expect, it } from 'vitest'

import { formatDisplayAmount, formatQuotaValue, type BillingStatusDTO } from '../usageFormatting'

describe('usage formatting helpers', () => {
  it('按 new-api 状态将 quota 展示为金额', () => {
    const status: BillingStatusDTO = {
      quota_per_unit: 500000,
      quota_display_type: 'USD',
      display_in_currency: true,
      custom_currency_symbol: '¤',
      custom_currency_exchange_rate: 1,
      usd_exchange_rate: 7.3,
      price: 7.3,
    }

    expect(formatQuotaValue(500000, status)).toContain('USD')
  })

  it('没有 new-api 状态时直接展示原始 quota', () => {
    expect(formatQuotaValue(500000, undefined)).toContain('500,000')
  })

  it('充值金额直接按展示单位显示，不再二次折算 quota', () => {
    const status: BillingStatusDTO = {
      quota_per_unit: 500000,
      quota_display_type: 'USD',
      display_in_currency: true,
    }

    expect(formatDisplayAmount(10, status)).toBe('USD 10')
  })
})
