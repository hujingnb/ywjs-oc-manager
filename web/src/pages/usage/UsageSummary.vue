<template>
  <div>
    <div v-if="!view" class="state-text">{{ emptyText }}</div>
    <template v-else>
      <div class="summary-grid">
        <div class="summary-card">
          <span>{{ t('usage.summary.totalTokens') }}</span>
          <strong>{{ formatNumber(totals.totalTokens, 0) }}</strong>
        </div>
        <div class="summary-card">
          <span>{{ t('usage.summary.totalAmount') }}</span>
          <strong>{{ formatQuotaValue(totals.totalQuota, billingStatus) }}</strong>
        </div>
        <div class="summary-card">
          <span>{{ t('usage.summary.totalCount') }}</span>
          <strong>{{ formatNumber(totals.totalCount, 0) }}</strong>
        </div>
        <div class="summary-card">
          <span>{{ t('usage.summary.modelCount') }}</span>
          <strong>{{ formatNumber(totals.modelCount, 0) }}</strong>
        </div>
      </div>

      <div v-if="trendPoints.length" class="chart-panel">
        <div class="chart-header">
          <strong>{{ t('usage.chart.heading') }}</strong>
          <span class="state-text">{{ t('usage.chart.lastUpdated', { time: formatTime(view.updated_at) }) }}</span>
        </div>
        <svg class="trend-chart" viewBox="0 0 720 180" role="img" :aria-label="t('usage.chart.ariaLabel')">
          <polyline class="trend-grid" points="40,24 680,24 680,140 40,140 40,24" />
          <polyline v-if="tokenPolyline" class="trend-line token-line" :points="tokenPolyline" />
          <polyline v-if="quotaPolyline" class="trend-line quota-line" :points="quotaPolyline" />
          <g v-for="point in chartDots" :key="point.date">
            <circle :cx="point.x" :cy="point.tokenY" r="3.5" class="token-dot" />
            <circle :cx="point.x" :cy="point.quotaY" r="3.5" class="quota-dot" />
          </g>
          <text v-if="trendPoints[0]" x="40" y="168" class="axis-label">{{ trendPoints[0].date }}</text>
          <text v-if="trendPoints.length > 1" x="680" y="168" text-anchor="end" class="axis-label">
            {{ trendPoints[trendPoints.length - 1].date }}
          </text>
        </svg>
        <div class="chart-legend">
          <span><i class="legend-token" />Token</span>
          <span><i class="legend-quota" />{{ t('usage.chart.legendAmount') }}</span>
        </div>
      </div>

      <n-data-table
        v-if="view.items?.length"
        :columns="tableColumns"
        :data="tableRows"
        size="small"
        :bordered="false"
      />
      <div v-else class="state-text">{{ emptyText }}</div>
    </template>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { NDataTable, type DataTableColumns } from 'naive-ui'
import { useI18n } from 'vue-i18n'

import type { AggregatedUsage } from '@/api/hooks/useUsage'

import { buildTrendPoints, normalizeUsageDate, summarizeUsage } from './usageMetrics'
import { formatNumber, formatQuotaValue, type BillingStatusDTO } from './usageFormatting'

// UsageSummary 渲染聚合用量结果，支持不同维度共享同一套空态和表格展示。
const props = defineProps<{
  view?: AggregatedUsage
  emptyText: string
  billingStatus?: BillingStatusDTO | null
}>()

const { t } = useI18n()

// totals 始终基于当前筛选结果计算，避免筛选切换后总量沿用旧上下文。
const totals = computed(() => summarizeUsage(props.view))
const trendPoints = computed(() => buildTrendPoints(props.view))

const tableRows = computed(() =>
  (props.view?.items ?? []).map((row, index) => ({
    key: index,
    date: normalizeUsageDate(row),
    tokens: getDisplayTokens(row),
    quota: getDisplayQuota(row),
    count: getDisplayCount(row),
  })),
)

// formatTime 将聚合结果更新时间展示为中文本地时间。
function formatTime(iso: string): string {
  return new Date(iso).toLocaleString('zh-CN', { hour12: false })
}

// tableColumns 明确展示用户关心字段，避免后端透传字段顺序导致 DATE 空白。
// 使用 computed 确保语言切换时列头文案响应式更新。
const tableColumns = computed<DataTableColumns<(typeof tableRows.value)[number]>>(() => [
  { title: 'DATE', key: 'date' },
  {
    title: 'Token',
    key: 'tokens',
    render: (row) => formatNumber(row.tokens, 0),
  },
  {
    title: t('usage.table.amount'),
    key: 'quota',
    render: (row) => formatQuotaValue(row.quota, props.billingStatus),
  },
  {
    title: t('usage.table.callCount'),
    key: 'count',
    render: (row) => formatNumber(row.count, 0),
  },
])

const tokenPolyline = computed(() => toPolyline(trendPoints.value.map((point) => point.tokens)))
const quotaPolyline = computed(() => toPolyline(trendPoints.value.map((point) => point.quota)))
const chartDots = computed(() => {
  const tokenValues = trendPoints.value.map((point) => point.tokens)
  const quotaValues = trendPoints.value.map((point) => point.quota)
  return trendPoints.value.map((point, index) => ({
    date: point.date,
    x: chartX(index, trendPoints.value.length),
    tokenY: chartY(tokenValues[index] ?? 0, maxValue(tokenValues)),
    quotaY: chartY(quotaValues[index] ?? 0, maxValue(quotaValues)),
  }))
})

// getDisplayTokens 与 usageMetrics 保持一致，表格展示和汇总口径相同。
function getDisplayTokens(row: Record<string, unknown>): number {
  if (!props.view) return 0
  const tokenUsed = toNumber(row.token_used)
  if ((props.view.scope === 'organization' || props.view.scope === 'platform') && tokenUsed !== undefined) {
    return tokenUsed
  }
  return (toNumber(row.prompt_tokens) ?? 0) + (toNumber(row.completion_tokens) ?? 0) || tokenUsed || 0
}

function getDisplayQuota(row: Record<string, unknown>): number {
  return toNumber(row.quota) ?? 0
}

function getDisplayCount(row: Record<string, unknown>): number {
  return toNumber(row.count) ?? 1
}

function toNumber(value: unknown): number | undefined {
  if (typeof value === 'number' && Number.isFinite(value)) return value
  if (typeof value === 'string' && value.trim()) {
    const parsed = Number(value)
    if (Number.isFinite(parsed)) return parsed
  }
  return undefined
}

function toPolyline(values: number[]): string {
  if (!values.length) return ''
  const max = maxValue(values)
  return values
    .map((value, index) => `${chartX(index, values.length)},${chartY(value, max)}`)
    .join(' ')
}

function maxValue(values: number[]): number {
  return Math.max(1, ...values)
}

function chartX(index: number, length: number): number {
  if (length <= 1) return 360
  return 40 + (640 * index) / (length - 1)
}

function chartY(value: number, max: number): number {
  return 140 - (116 * value) / max
}
</script>

<style scoped>
.summary-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(150px, 1fr));
  gap: 12px;
  margin-bottom: 16px;
}

.summary-card {
  border: 1px solid var(--color-border);
  border-radius: 8px;
  padding: 12px;
  background: var(--color-surface);
}

.summary-card span {
  display: block;
  color: var(--color-text-secondary);
  font-size: 12px;
  margin-bottom: 6px;
}

.summary-card strong {
  display: block;
  color: var(--color-text-primary);
  font-size: 20px;
  line-height: 1.25;
  word-break: break-word;
}

.chart-panel {
  margin-bottom: 16px;
  border: 1px solid var(--color-border);
  border-radius: 8px;
  padding: 12px;
  background: var(--color-surface);
}

.chart-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 8px;
}

.chart-header span {
  color: var(--color-text-secondary);
}

.trend-chart {
  width: 100%;
  height: 180px;
  display: block;
}

.trend-grid {
  fill: none;
  stroke: var(--color-divider);
  stroke-width: 1;
}

.trend-line {
  fill: none;
  stroke-width: 3;
  stroke-linecap: round;
  stroke-linejoin: round;
}

.token-line,
.token-dot,
.legend-token {
  stroke: var(--color-info);
  background: var(--color-info);
}

.quota-line,
.quota-dot,
.legend-quota {
  stroke: var(--color-warning);
  background: var(--color-warning);
}

.token-dot,
.quota-dot {
  fill: var(--color-text-primary);
  stroke-width: 2;
}

.axis-label {
  fill: var(--color-text-secondary);
  font-size: 12px;
}

.chart-legend {
  display: flex;
  gap: 14px;
  color: var(--color-text-secondary);
  font-size: 12px;
}

.chart-legend span {
  display: inline-flex;
  align-items: center;
  gap: 6px;
}

.chart-legend i {
  width: 10px;
  height: 10px;
  border-radius: 999px;
  display: inline-block;
}

@media (max-width: 640px) {
  .chart-header {
    align-items: flex-start;
    flex-direction: column;
  }
}
</style>
