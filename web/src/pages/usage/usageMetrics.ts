import type { AggregatedUsage } from '@/api/hooks/useUsage'

// UsageTotals 是当前筛选条件下的汇总口径，所有数值只来自当前响应 items。
export interface UsageTotals {
  // totalTokens 表示当前筛选条件命中的 token 总量。
  totalTokens: number
  // totalQuota 表示当前筛选条件命中的 new-api quota 总量，展示层再按计费状态格式化为金额。
  totalQuota: number
  // totalCount 表示当前筛选条件命中的请求/记录数量。
  totalCount: number
  // modelCount 表示当前筛选条件下归一化后的模型种类数量。
  modelCount: number
}

// UsageTrendPoint 是折线图使用的按日期聚合结果。
export interface UsageTrendPoint {
  // date 使用 YYYY-MM-DD，确保表格和图表展示同一日期口径。
  date: string
  // tokens 是该日期的 token 汇总。
  tokens: number
  // quota 是该日期的 quota 汇总。
  quota: number
  // count 是该日期的请求/记录数量。
  count: number
}

// normalizeModelName 避免 new-api 日志中空 model_name 让表格出现空白。
export function normalizeModelName(value: unknown): string {
  if (typeof value !== 'string') return '未知模型'
  const trimmed = value.trim()
  return trimmed || '未知模型'
}

// normalizeUsageDate 优先使用聚合响应 date；日志响应缺 date 时从 created_at 补齐。
export function normalizeUsageDate(row: Record<string, unknown>): string {
  const date = row.date
  if (typeof date === 'string' && date.trim()) return date.trim()

  const createdAt = toNumber(row.created_at)
  if (createdAt === undefined) return '—'

  // new-api 日志可能返回秒级或毫秒级时间戳，这里按量级兼容两种来源。
  const milliseconds = createdAt < 1_000_000_000_000 ? createdAt * 1000 : createdAt
  const parsed = new Date(milliseconds)
  if (Number.isNaN(parsed.getTime())) return '—'
  return parsed.toISOString().slice(0, 10)
}

// getRowTokens 根据维度读取 token：聚合维度用 token_used，日志维度用 prompt+completion。
export function getRowTokens(scope: AggregatedUsage['scope'], row: Record<string, unknown>): number {
  const tokenUsed = toNumber(row.token_used)
  if ((scope === 'organization' || scope === 'platform') && tokenUsed !== undefined) {
    return tokenUsed
  }

  const promptTokens = toNumber(row.prompt_tokens) ?? 0
  const completionTokens = toNumber(row.completion_tokens) ?? 0
  const logTokens = promptTokens + completionTokens
  if (logTokens > 0) return logTokens

  return tokenUsed ?? 0
}

// getRowQuota 读取 new-api quota，非数字字段按 0 处理以免污染汇总。
export function getRowQuota(row: Record<string, unknown>): number {
  return toNumber(row.quota) ?? 0
}

// summarizeUsage 汇总当前响应 items，确保筛选条件变化后总量和图表一起变化。
export function summarizeUsage(view?: AggregatedUsage | null): UsageTotals {
  const totals: UsageTotals = {
    totalTokens: 0,
    totalQuota: 0,
    totalCount: 0,
    modelCount: 0,
  }
  if (!view) return totals

  const modelNames = new Set<string>()
  for (const row of view.items ?? []) {
    totals.totalTokens += getRowTokens(view.scope, row)
    totals.totalQuota += getRowQuota(row)
    totals.totalCount += toNumber(row.count) ?? 1
    modelNames.add(normalizeModelName(row.model_name))
  }

  if ((view.scope === 'member' || view.scope === 'app') && view.total !== undefined) {
    totals.totalCount = view.total
  }
  totals.modelCount = modelNames.size
  return totals
}

// buildTrendPoints 将明细按日期升序聚合，供折线图在不同筛选条件下复用。
export function buildTrendPoints(view?: AggregatedUsage | null): UsageTrendPoint[] {
  if (!view) return []

  const grouped = new Map<string, UsageTrendPoint>()
  for (const row of view.items ?? []) {
    const date = normalizeUsageDate(row)
    if (date === '—') continue

    const current = grouped.get(date) ?? { date, tokens: 0, quota: 0, count: 0 }
    current.tokens += getRowTokens(view.scope, row)
    current.quota += getRowQuota(row)
    current.count += toNumber(row.count) ?? 1
    grouped.set(date, current)
  }

  return Array.from(grouped.values()).sort((a, b) => a.date.localeCompare(b.date))
}

// toNumber 兼容 new-api 透传字段里的数字字符串，无法解析的值不参与统计。
function toNumber(value: unknown): number | undefined {
  if (typeof value === 'number' && Number.isFinite(value)) return value
  if (typeof value === 'string' && value.trim()) {
    const parsed = Number(value)
    if (Number.isFinite(parsed)) return parsed
  }
  return undefined
}
