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
        <polyline v-if="primaryPoints.length > 1" class="trend-line" :points="primaryPolylinePoints" fill="none" />
        <polyline v-if="secondaryPoints.length > 1" class="secondary-line" :points="secondaryPolylinePoints" fill="none" />
        <circle
          v-for="point in secondaryPoints"
          :key="`secondary-${point.index}`"
          class="secondary-marker"
          :cx="point.x"
          :cy="point.y"
          r="3"
        />
        <circle
          v-for="point in primaryPoints"
          :key="`primary-${point.index}`"
          class="trend-marker"
          :cx="point.x"
          :cy="point.y"
          r="3.5"
        />
      </svg>
    </div>

    <footer class="chart-labels" aria-live="polite">
      <span>{{ rangeLabel }}</span>
      <span v-if="summaryLabel">{{ summaryLabel }}</span>
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

interface ChartPoint {
  index: number
  x: string
  y: string
}

interface NumericPoint {
  index: number
  sampled_at: string
  value: number
}

// primarySamples / secondarySamples 过滤掉后端缺失指标的采样点，避免空值把趋势线错误拉到 0。
const primarySamples = computed(() => numericSeries('value'))
const secondarySamples = computed(() => numericSeries('secondary'))
const scaleValues = computed(() => [...primarySamples.value, ...secondarySamples.value].map((sample) => sample.value))
const hasValues = computed(() => scaleValues.value.length > 0)

const latestValue = computed(() => primarySamples.value.at(-1)?.value ?? null)
const latestLabel = computed(() => (isNumeric(latestValue.value) ? formatValue(latestValue.value, props.unit) : ''))
const primaryPeakLabel = computed(() => {
  if (primarySamples.value.length === 0) return ''
  const peak = Math.max(...primarySamples.value.map((sample) => sample.value))
  return formatValue(peak, props.unit)
})
const secondaryPeakLabel = computed(() => {
  if (secondarySamples.value.length === 0) return ''
  const peak = Math.max(...secondarySamples.value.map((sample) => sample.value))
  return formatValue(peak, props.unit)
})
const summaryLabel = computed(() => {
  if (!hasValues.value) return ''
  const labels = []
  if (primaryPeakLabel.value) labels.push(`峰值 ${primaryPeakLabel.value}`)
  if (secondaryPeakLabel.value) labels.push(`次要 ${secondaryPeakLabel.value}`)
  return labels.join(' · ')
})
const rangeLabel = computed(() => {
  if (!hasValues.value) return '无数据'
  const visibleSamples = [...primarySamples.value, ...secondarySamples.value].sort((a, b) => a.index - b.index)
  const first = visibleSamples[0]
  const last = visibleSamples.at(-1) ?? first
  return `${formatTime(first.sampled_at)} - ${formatTime(last.sampled_at)}`
})

const primaryPoints = computed(() => chartPoints(primarySamples.value))
const secondaryPoints = computed(() => chartPoints(secondarySamples.value))
const primaryPolylinePoints = computed(() => joinPoints(primaryPoints.value))
const secondaryPolylinePoints = computed(() => joinPoints(secondaryPoints.value))

function numericSeries(field: 'value' | 'secondary'): NumericPoint[] {
  return props.samples.flatMap((sample, index) => {
    const value = sample[field]
    if (!isNumeric(value)) return []
    return [{ index, sampled_at: sample.sampled_at, value }]
  })
}

function chartPoints(series: NumericPoint[]): ChartPoint[] {
  const values = scaleValues.value
  if (values.length === 0) return []

  const min = Math.min(...values)
  const max = Math.max(...values)
  const span = max - min || 1
  const drawableWidth = chartWidth - chartPadding.left - chartPadding.right
  const drawableHeight = chartHeight - chartPadding.top - chartPadding.bottom

  return series.map((sample) => {
    const x = chartPadding.left + (props.samples.length === 1 ? drawableWidth / 2 : (sample.index / (props.samples.length - 1)) * drawableWidth)
    const y = chartPadding.top + ((max - sample.value) / span) * drawableHeight
    return { index: sample.index, x: roundPoint(x), y: roundPoint(y) }
  })
}

function joinPoints(points: ChartPoint[]): string {
  return points.map((point) => `${point.x},${point.y}`).join(' ')
}

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

.secondary-line {
  stroke: var(--color-warning, #d97706);
  stroke-dasharray: 5 4;
  stroke-linecap: round;
  stroke-linejoin: round;
  stroke-width: 2;
}

.trend-marker {
  fill: var(--color-primary, #2563eb);
}

.secondary-marker {
  fill: var(--color-warning, #d97706);
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
