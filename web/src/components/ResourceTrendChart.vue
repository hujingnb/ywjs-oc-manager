<template>
  <section class="resource-trend-chart" :aria-label="title">
    <header class="chart-header">
      <h3>{{ title }}</h3>
      <span v-if="latestLabel" class="latest-value">{{ latestLabel }}</span>
    </header>

    <div class="chart-frame">
      <p v-if="!hasValues" class="empty-state">{{ emptyText ?? '暂无资源采样' }}</p>
      <div
        v-else
        ref="chartEl"
        class="resource-echarts"
        role="img"
        :aria-label="`${title}趋势图`"
      />
    </div>

    <footer class="chart-labels" aria-live="polite">
      <span>{{ rangeLabel }}</span>
      <span v-if="summaryLabel">{{ summaryLabel }}</span>
    </footer>
  </section>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { init, use } from 'echarts/core'
import { LineChart } from 'echarts/charts'
import { GridComponent, TooltipComponent } from 'echarts/components'
import { CanvasRenderer } from 'echarts/renderers'
import type { EChartsCoreOption, EChartsType } from 'echarts/core'
import type { LineSeriesOption } from 'echarts/charts'

use([CanvasRenderer, LineChart, GridComponent, TooltipComponent])

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

interface NumericPoint {
  index: number
  sampled_at: string
  value: number
}

type ResourceChartOption = EChartsCoreOption

interface TooltipParam {
  axisValue?: string | number
  marker?: string
  seriesName?: string
  value?: number | string | null
}

// primarySamples / secondarySamples 过滤掉后端缺失指标的采样点，避免空值把趋势线错误拉到 0。
const primarySamples = computed(() => numericSeries('value'))
const secondarySamples = computed(() => numericSeries('secondary'))
const scaleValues = computed(() => [...primarySamples.value, ...secondarySamples.value].map((sample) => sample.value))
const hasValues = computed(() => scaleValues.value.length > 0)
const chartEl = ref<HTMLElement | null>(null)
let chart: EChartsType | null = null
let resizeObserver: ResizeObserver | null = null

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

const chartOption = computed<ResourceChartOption>(() => ({
  animation: false,
  color: ['#2563eb', '#d97706'],
  grid: {
    top: 14,
    right: 12,
    bottom: 24,
    left: props.unit === 'bytes' ? 72 : 52,
    containLabel: false,
  },
  tooltip: {
    trigger: 'axis',
    axisPointer: { type: 'line' },
    confine: true,
    formatter: tooltipFormatter,
  },
  xAxis: {
    type: 'category',
    boundaryGap: false,
    data: props.samples.map((sample) => formatTime(sample.sampled_at)),
    axisTick: { show: false },
    axisLabel: {
      color: '#66758a',
      fontSize: 11,
      hideOverlap: true,
      showMinLabel: true,
      showMaxLabel: true,
    },
    axisLine: { lineStyle: { color: '#d9ddea' } },
  },
  yAxis: {
    type: 'value',
    scale: props.unit !== 'percent' && props.unit !== 'count',
    min: props.unit === 'percent' ? 0 : undefined,
    max: props.unit === 'percent' ? 100 : undefined,
    minInterval: props.unit === 'count' ? 1 : undefined,
    splitNumber: 3,
    axisLabel: {
      color: '#66758a',
      fontSize: 11,
      formatter: (value: number) => formatValue(value, props.unit),
    },
    splitLine: { lineStyle: { color: '#eef2f7' } },
  },
  series: chartSeries.value,
}))

const chartSeries = computed<LineSeriesOption[]>(() => {
  const series: LineSeriesOption[] = [{
    name: props.title,
    type: 'line',
    data: seriesData('value'),
    connectNulls: false,
    showSymbol: true,
    symbolSize: 5,
    lineStyle: { width: 2 },
    emphasis: { focus: 'series' },
  }]

  if (secondarySamples.value.length > 0) {
    series.push({
      name: '次要',
      type: 'line',
      data: seriesData('secondary'),
      connectNulls: false,
      showSymbol: true,
      symbolSize: 5,
      lineStyle: { width: 2, type: 'dashed' as const },
      emphasis: { focus: 'series' },
    })
  }

  return series
})

onMounted(() => {
  void renderChart()
})

watch(chartOption, () => {
  void renderChart()
}, { deep: true, flush: 'post' })

watch(hasValues, (visible) => {
  if (visible) {
    void renderChart()
    return
  }
  disposeChart()
}, { flush: 'post' })

onBeforeUnmount(() => {
  disposeChart()
})

async function renderChart() {
  if (!hasValues.value) {
    disposeChart()
    return
  }

  await nextTick()
  if (!chartEl.value) return

  if (!chart) {
    chart = init(chartEl.value)
    if (typeof ResizeObserver !== 'undefined') {
      resizeObserver = new ResizeObserver(() => chart?.resize())
      resizeObserver.observe(chartEl.value)
    }
  }

  chart.setOption(chartOption.value, true)
}

function disposeChart() {
  resizeObserver?.disconnect()
  resizeObserver = null
  chart?.dispose()
  chart = null
}

function numericSeries(field: 'value' | 'secondary'): NumericPoint[] {
  return props.samples.flatMap((sample, index) => {
    const value = sample[field]
    if (!isNumeric(value)) return []
    return [{ index, sampled_at: sample.sampled_at, value }]
  })
}

function seriesData(field: 'value' | 'secondary'): Array<number | null> {
  // ECharts 用 null 表示该采样缺指标，既保留横轴时间点，也避免把缺失值误画成 0。
  return props.samples.map((sample) => {
    const value = sample[field]
    return isNumeric(value) ? value : null
  })
}

function isNumeric(value: number | null | undefined): value is number {
  return typeof value === 'number' && Number.isFinite(value)
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

function tooltipFormatter(params: TooltipParam | TooltipParam[]): string {
  const points = Array.isArray(params) ? params : [params]
  const title = points[0]?.axisValue ?? ''
  const lines = points.flatMap((point) => {
    const value = tooltipValue(point.value)
    if (!isNumeric(value)) return []
    const marker = point.marker ?? ''
    const name = point.seriesName ?? ''
    return `${marker}${escapeHtml(name)}：${escapeHtml(formatValue(value, props.unit))}`
  })

  return [escapeHtml(String(title)), ...lines].join('<br />')
}

function tooltipValue(value: TooltipParam['value']): number | null {
  if (typeof value === 'number') return value
  if (typeof value === 'string' && value.trim() !== '') {
    const parsed = Number(value)
    return Number.isFinite(parsed) ? parsed : null
  }
  return null
}

function escapeHtml(value: string): string {
  // tooltip formatter 返回 HTML 字符串，转义动态文本避免样本时间或标题包含特殊字符时破坏结构。
  return value
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#39;')
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

.resource-echarts {
  width: 100%;
  height: 128px;
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
