<template>
  <section class="resource-trend-chart" :aria-label="title">
    <header class="chart-header">
      <h3>{{ title }}</h3>
      <span v-if="latestLabel" class="latest-value">{{ latestLabel }}</span>
    </header>

    <div class="chart-frame">
      <p v-if="!hasValues" class="empty-state">{{ emptyText ?? '暂无资源采样' }}</p>
      <svg
        v-else
        class="chart-svg"
        viewBox="0 0 320 128"
        role="img"
        :aria-label="`${title}趋势图`"
        preserveAspectRatio="none"
      >
        <line x1="0" y1="104" x2="320" y2="104" class="axis-line" />
        <polyline class="trend-line" :points="polylinePoints" fill="none" />
      </svg>
    </div>

    <footer class="chart-labels" aria-live="polite">
      <span>{{ rangeLabel }}</span>
      <span v-if="peakLabel">峰值 {{ peakLabel }}</span>
    </footer>
  </section>
</template>

<script setup lang="ts">
import { computed } from 'vue'

interface ResourceTrendSample {
  sampled_at: string
  value?: number | null
  secondary?: number | null
}

const props = defineProps<{
  title: string
  samples: ResourceTrendSample[]
  unit: 'percent' | 'bytes' | 'rate' | 'count'
  emptyText?: string
}>()

const chartWidth = 320
const chartHeight = 128
const chartPadding = {
  top: 16,
  right: 12,
  bottom: 24,
  left: 12,
}

// numericSamples 过滤掉后端缺失指标的采样点，避免空值把趋势线错误拉到 0。
const numericSamples = computed(() => props.samples.filter((sample) => isNumeric(sample.value)))
const hasValues = computed(() => numericSamples.value.length > 0)

const latestValue = computed(() => numericSamples.value.at(-1)?.value ?? null)
const latestLabel = computed(() => (isNumeric(latestValue.value) ? formatValue(latestValue.value, props.unit) : ''))
const peakLabel = computed(() => {
  if (!hasValues.value) return ''
  const peak = Math.max(...numericSamples.value.map((sample) => sample.value as number))
  return formatValue(peak, props.unit)
})
const rangeLabel = computed(() => {
  if (!hasValues.value) return '无数据'
  const first = numericSamples.value[0]
  const last = numericSamples.value.at(-1) ?? first
  return `${formatTime(first.sampled_at)} - ${formatTime(last.sampled_at)}`
})

const polylinePoints = computed(() => {
  const values = numericSamples.value.map((sample) => sample.value as number)
  if (values.length === 0) return ''

  const min = Math.min(...values)
  const max = Math.max(...values)
  const span = max - min || 1
  const drawableWidth = chartWidth - chartPadding.left - chartPadding.right
  const drawableHeight = chartHeight - chartPadding.top - chartPadding.bottom

  return values
    .map((value, index) => {
      const x = chartPadding.left + (values.length === 1 ? drawableWidth / 2 : (index / (values.length - 1)) * drawableWidth)
      const y = chartPadding.top + ((max - value) / span) * drawableHeight
      return `${roundPoint(x)},${roundPoint(y)}`
    })
    .join(' ')
})

function isNumeric(value: number | null | undefined): value is number {
  return typeof value === 'number' && Number.isFinite(value)
}

function roundPoint(value: number): string {
  return value.toFixed(2)
}

function formatValue(value: number, unit: 'percent' | 'bytes' | 'rate' | 'count'): string {
  switch (unit) {
    case 'percent':
      return `${formatNumber(value, 1)}%`
    case 'bytes':
      return formatBytes(value)
    case 'rate':
      return `${formatNumber(value, 1)}/s`
    case 'count':
      return formatNumber(value, 0)
  }
}

function formatNumber(value: number, maximumFractionDigits: number): string {
  return new Intl.NumberFormat('zh-CN', { maximumFractionDigits }).format(value)
}

function formatBytes(value: number): string {
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let size = value
  let unitIndex = 0
  while (Math.abs(size) >= 1024 && unitIndex < units.length - 1) {
    size /= 1024
    unitIndex += 1
  }
  return `${formatNumber(size, unitIndex === 0 ? 0 : 1)} ${units[unitIndex]}`
}

function formatTime(value: string): string {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  return new Intl.DateTimeFormat('zh-CN', {
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
  }).format(date)
}
</script>

<style scoped>
.resource-trend-chart {
  display: flex;
  min-width: 0;
  flex-direction: column;
  gap: 8px;
}

.chart-header {
  display: flex;
  min-height: 24px;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}

.chart-header h3 {
  margin: 0;
  overflow: hidden;
  color: var(--color-text-primary, #1f2433);
  font-size: 14px;
  font-weight: 600;
  line-height: 20px;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.latest-value {
  flex: 0 0 auto;
  color: var(--color-text-primary, #1f2433);
  font-size: 13px;
  font-weight: 600;
  line-height: 20px;
}

.chart-frame {
  position: relative;
  min-height: 128px;
  border: 1px solid var(--color-border, #d9ddea);
  border-radius: 8px;
  background: var(--color-surface, #fff);
}

.empty-state {
  display: flex;
  min-height: 128px;
  align-items: center;
  justify-content: center;
  margin: 0;
  color: var(--color-text-secondary, #8a94c6);
  font-size: 13px;
}

.chart-svg {
  display: block;
  width: 100%;
  height: 128px;
}

.axis-line {
  stroke: var(--color-border, #d9ddea);
  stroke-width: 1;
}

.trend-line {
  stroke: var(--color-primary, #2563eb);
  stroke-linecap: round;
  stroke-linejoin: round;
  stroke-width: 2.5;
}

.chart-labels {
  display: flex;
  min-height: 18px;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  color: var(--color-text-secondary, #8a94c6);
  font-size: 12px;
  line-height: 18px;
}
</style>
