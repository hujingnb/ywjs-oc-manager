// BillingStatusDTO 对应 new-api /api/status 的计费展示配置。
export interface BillingStatusDTO {
  // quota_per_unit 表示多少 quota 折算为一个展示单位。
  quota_per_unit: number
  // quota_display_type 是 new-api 管理员配置的展示单位，例如 USD。
  quota_display_type?: string
  // display_in_currency 标记 new-api 是否按金额展示 quota。
  display_in_currency?: boolean
  // custom_currency_symbol 是自定义货币符号。
  custom_currency_symbol?: string
  // custom_currency_exchange_rate 由 new-api 管理，本端仅透传展示。
  custom_currency_exchange_rate?: number
  // usd_exchange_rate 由 new-api 管理，本端仅透传展示。
  usd_exchange_rate?: number
  // price 由 new-api 管理，本端不参与单价计算。
  price?: number
}

// formatNumber 统一大数字展示，避免 summary 卡和表格各自格式不一致。
export function formatNumber(value: number, maximumFractionDigits = 2): string {
  return value.toLocaleString('en-US', {
    maximumFractionDigits,
  })
}

// formatQuotaValue 将 raw quota 按 new-api 展示状态转换为金额/额度文本。
export function formatQuotaValue(value: number, status?: BillingStatusDTO | null): string {
  if (!status?.quota_per_unit || status.quota_per_unit <= 0) {
    return formatNumber(value)
  }

  const displayValue = value / status.quota_per_unit
  return formatDisplayAmount(displayValue, status)
}

// formatDisplayAmount 用于充值输入和充值记录；这些值已经是 new-api 展示单位。
export function formatDisplayAmount(value: number, status?: BillingStatusDTO | null): string {
  if (!status) return formatNumber(value)

  if (status.display_in_currency) {
    const label = status.quota_display_type || status.custom_currency_symbol || '金额'
    return `${label} ${formatNumber(value, 6)}`
  }

  const label = status.quota_display_type || '额度'
  return `${formatNumber(value, 6)} ${label}`
}
