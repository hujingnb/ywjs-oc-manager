<template>
  <div style="display: grid; gap: 18px">
    <!-- 统计条 -->
    <n-grid :cols="6" :x-gap="14" :y-gap="14" responsive="screen" :item-responsive="true">
      <n-grid-item v-for="stat in stats" :key="stat.label" :span="1" :xs="3" :sm="2" :md="1">
        <n-card size="small" :bordered="true">
          <n-statistic :label="stat.label" :value="stat.value">
            <template v-if="stat.suffix" #suffix>
              <span style="font-size: 11px; color: var(--color-text-secondary)">{{ stat.suffix }}</span>
            </template>
          </n-statistic>
          <div v-if="stat.note" style="font-size: 11px; margin-top: 4px" :style="{ color: stat.noteColor ?? 'var(--color-text-secondary)' }">
            {{ stat.note }}
          </div>
        </n-card>
      </n-grid-item>
    </n-grid>

    <!-- 图表区 Tab -->
    <n-card :bordered="true" style="flex: 1">
      <n-tabs v-model:value="activeTab" type="line" animated @update:value="onTabChange">
        <!-- Tab 1：Token 趋势 -->
        <n-tab-pane name="token" :tab="t('platform.console.tabs.tokenTrend')" display-directive="show:lazy">
          <div v-if="platformUsageLoading" class="chart-state">{{ t('platform.console.chart.loading') }}</div>
          <div v-else-if="platformUsageError" class="chart-state danger">{{ t('platform.console.chart.usageUnavail') }}</div>
          <div v-else ref="tokenChartEl" class="chart-container" />
        </n-tab-pane>

        <!-- Tab 2：各组织用量 -->
        <n-tab-pane name="orgs" :tab="t('platform.console.tabs.orgUsage')" display-directive="show:lazy">
          <div v-if="orgBreakdownLoading" class="chart-state">{{ t('platform.console.chart.loading') }}</div>
          <div v-else-if="orgBreakdownError" class="chart-state danger">{{ t('platform.console.chart.usageUnavail') }}</div>
          <div v-else-if="!orgBreakdownData?.length" class="chart-state">{{ t('platform.console.chart.empty') }}</div>
          <div v-else ref="orgChartEl" class="chart-container" />
        </n-tab-pane>

        <!-- Tab 3：实例状态 -->
        <n-tab-pane name="status" :tab="t('platform.console.tabs.instanceStatus')" display-directive="show:lazy">
          <div v-if="overviewLoading" class="chart-state">{{ t('platform.console.chart.loading') }}</div>
          <div v-else-if="overviewError" class="chart-state danger">{{ t('platform.console.chart.platformUnavail') }}</div>
          <div v-else ref="statusChartEl" class="chart-container" />
        </n-tab-pane>
      </n-tabs>
    </n-card>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { NCard, NGrid, NGridItem, NStatistic, NTabPane, NTabs } from 'naive-ui'
import { init, use } from 'echarts/core'
import { LineChart, BarChart, PieChart } from 'echarts/charts'
import {
  GridComponent, TooltipComponent, LegendComponent,
} from 'echarts/components'
import { CanvasRenderer } from 'echarts/renderers'
import type { EChartsType } from 'echarts/core'

import { usePlatformOverviewQuery, usePlatformOrgBreakdownQuery } from '@/api/hooks/usePlatform'
import { useAuthStore } from '@/stores/auth'
import { apiRequest } from '@/api/client'
import { useQuery } from '@tanstack/vue-query'

use([CanvasRenderer, LineChart, BarChart, PieChart, GridComponent, TooltipComponent, LegendComponent])

// ConsolePage 是平台管理员专属的控制台首页：统计条 + Token 趋势/组织用量/实例状态三图。
// 图表颜色与全局浅色主题保持一致，避免 ECharts 默认色回到深色控制台残留。
const CHART_TEXT_COLOR = '#6b7280'
const CHART_AXIS_COLOR = '#d9dde5'
const CHART_GRID_COLOR = '#edf0f5'
const CHART_INFO_COLOR = '#1677ff'
const CHART_INFO_AREA = 'rgba(22, 119, 255, 0.08)'
const CHART_SUCCESS_COLOR = '#16a34a'
const CHART_MUTED_COLOR = '#8a94a6'
const CHART_DANGER_COLOR = '#d93026'
const CHART_PIE_BORDER = '#ffffff'

const { t } = useI18n()
const auth = useAuthStore()
const isPlatformAdmin = computed(() => auth.isPlatformAdmin)

// ── 数据查询 ──────────────────────────────────────────────
const { data: overview, isLoading: overviewLoading, error: overviewError } = usePlatformOverviewQuery(isPlatformAdmin)
const { data: orgBreakdownData, isLoading: orgBreakdownLoading, error: orgBreakdownError } = usePlatformOrgBreakdownQuery(isPlatformAdmin)

// 平台近 7 天 quota 序列：用于 Token 趋势折线图和「今日 Token」统计卡片。
const { data: platformUsageItems, isLoading: platformUsageLoading, error: platformUsageError } = useQuery<
  { date: string; quota: number }[]
>({
  queryKey: ['usage', 'platform', '7days'],
  enabled: () => isPlatformAdmin.value,
  refetchInterval: 60000,
  queryFn: async () => {
    const now = Math.floor(Date.now() / 1000)
    const since = now - 7 * 24 * 60 * 60
    const resp = await apiRequest<{ usage: { items: { date: string; quota: number }[] } }>(
      '/api/v1/usage/platform',
      { query: { since: String(since), until: String(now) } },
    )
    return resp.usage?.items ?? []
  },
})

// ── 统计卡片 ──────────────────────────────────────────────
// todayTokenTotal 把今天（本地日期）在 platformUsageItems 中的 quota 求和。
const todayTokenTotal = computed(() => {
  if (!platformUsageItems.value?.length) return null
  const today = new Date().toISOString().slice(0, 10) // YYYY-MM-DD
  return platformUsageItems.value
    .filter(item => item.date === today)
    .reduce((acc, item) => acc + item.quota, 0)
})

// stats 将 overview + today token 转为统计卡片数组，标签响应语言切换。
const stats = computed(() => {
  const o = overview.value
  return [
    { label: t('platform.console.stats.orgCount'), value: String(o?.organization_count ?? '—'), note: '', noteColor: undefined, suffix: undefined },
    { label: t('platform.console.stats.memberCount'), value: String(o?.member_count ?? '—'), note: t('platform.console.stats.memberNote'), noteColor: undefined, suffix: undefined },
    { label: t('platform.console.stats.instanceCount'), value: String(o?.app_count ?? '—'), note: '', noteColor: undefined, suffix: undefined },
    { label: t('platform.console.stats.running'), value: String(o?.running_app_count ?? '—'), note: '', noteColor: 'var(--color-success)', suffix: undefined },
    { label: t('platform.console.stats.error'), value: String(o?.error_app_count ?? '—'), note: '', noteColor: 'var(--color-danger)', suffix: undefined },
    {
      label: t('platform.console.stats.todayToken'),
      value: todayTokenTotal.value !== null ? String(todayTokenTotal.value.toLocaleString('en-US')) : '—',
      note: todayTokenTotal.value !== null
        ? t('platform.console.stats.todayTokenNoteRealtime')
        : platformUsageLoading.value
          ? t('platform.console.stats.todayTokenNoteLoading')
          : t('platform.console.stats.todayTokenNoteUnavail'),
      noteColor: undefined,
      suffix: undefined,
    },
  ]
})

// ── 图表 ──────────────────────────────────────────────────
const activeTab = ref<'token' | 'orgs' | 'status'>('token')
const tokenChartEl = ref<HTMLElement | null>(null)
const orgChartEl = ref<HTMLElement | null>(null)
const statusChartEl = ref<HTMLElement | null>(null)

let tokenChart: EChartsType | null = null
let orgChart: EChartsType | null = null
let statusChart: EChartsType | null = null

// formatQuota 将 new-api quota 原始值格式化为可读的万/千/百万单位。
function formatQuota(v: number): string {
  if (v >= 1_000_000) return `${(v / 1_000_000).toFixed(1)}M`
  if (v >= 10_000) return `${(v / 10_000).toFixed(1)}W`
  if (v >= 1_000) return `${(v / 1_000).toFixed(1)}k`
  return String(v)
}

// ── Token 趋势图（折线） ──
function buildTokenChart() {
  if (!tokenChartEl.value || !platformUsageItems.value?.length) return
  if (!tokenChart) tokenChart = init(tokenChartEl.value)

  // 按日聚合：同一天可能有多个 model 条目。
  const byDate = new Map<string, number>()
  for (const item of platformUsageItems.value) {
    byDate.set(item.date, (byDate.get(item.date) ?? 0) + item.quota)
  }
  const sorted = [...byDate.entries()].sort(([a], [b]) => a.localeCompare(b))
  const dates = sorted.map(([d]) => d.slice(5)) // MM-DD
  const values = sorted.map(([, v]) => v)

  tokenChart.setOption({
    animation: false,
    grid: { top: 14, right: 16, bottom: 28, left: 60, containLabel: false },
    tooltip: { trigger: 'axis', formatter: (params: { value: number }[]) => formatQuota(params[0]?.value ?? 0) },
    xAxis: {
      type: 'category',
      data: dates,
      axisLabel: { color: CHART_TEXT_COLOR, fontSize: 11 },
      axisLine: { lineStyle: { color: CHART_AXIS_COLOR } },
      axisTick: { show: false },
    },
    yAxis: {
      type: 'value',
      axisLabel: { color: CHART_TEXT_COLOR, fontSize: 11, formatter: (v: number) => formatQuota(v) },
      splitLine: { lineStyle: { color: CHART_GRID_COLOR } },
    },
    series: [{
      type: 'line',
      data: values,
      smooth: true,
      showSymbol: true,
      symbolSize: 5,
      lineStyle: { width: 2, color: CHART_INFO_COLOR },
      itemStyle: { color: CHART_INFO_COLOR },
      areaStyle: { color: CHART_INFO_AREA },
    }],
  })
}

// ── 各组织用量图（横向柱状） ──
function buildOrgChart() {
  if (!orgChartEl.value || !orgBreakdownData.value?.length) return
  if (!orgChart) orgChart = init(orgChartEl.value)

  const items = [...orgBreakdownData.value].reverse() // echarts bar 从底到顶，反转让最高的在上
  orgChart.setOption({
    animation: false,
    grid: { top: 8, right: 80, bottom: 8, left: 120, containLabel: false },
    tooltip: {
      trigger: 'axis',
      axisPointer: { type: 'shadow' },
      formatter: (params: { value: number }[]) => formatQuota(params[0]?.value ?? 0),
    },
    xAxis: {
      type: 'value',
      axisLabel: { color: CHART_TEXT_COLOR, fontSize: 11, formatter: (v: number) => formatQuota(v) },
      splitLine: { lineStyle: { color: CHART_GRID_COLOR } },
    },
    yAxis: {
      type: 'category',
      data: items.map(i => i.org_name),
      axisLabel: { color: CHART_TEXT_COLOR, fontSize: 11, width: 110, overflow: 'truncate' },
      axisLine: { show: false },
      axisTick: { show: false },
    },
    series: [{
      type: 'bar',
      data: items.map(i => i.total_quota),
      itemStyle: { color: CHART_INFO_COLOR, borderRadius: [0, 3, 3, 0] },
      label: { show: true, position: 'right', color: CHART_TEXT_COLOR, fontSize: 11, formatter: (p: { value: number }) => formatQuota(p.value) },
    }],
  })
}

// ── 实例状态图（饼图）：饼图 legend 标签随语言响应式切换 ──
function buildStatusChart() {
  if (!statusChartEl.value || !overview.value) return
  if (!statusChart) statusChart = init(statusChartEl.value)

  const o = overview.value
  const stopped = (o.app_count ?? 0) - (o.running_app_count ?? 0) - (o.error_app_count ?? 0)
  statusChart.setOption({
    animation: false,
    tooltip: { trigger: 'item', formatter: '{b}: {c} ({d}%)' },
    legend: { bottom: 0, textStyle: { color: CHART_TEXT_COLOR, fontSize: 12 } },
    series: [{
      type: 'pie',
      radius: ['40%', '68%'],
      center: ['50%', '44%'],
      itemStyle: { borderWidth: 2, borderColor: CHART_PIE_BORDER },
      label: { show: false },
      data: [
        { name: t('platform.console.chart.pieRunning'), value: o.running_app_count ?? 0, itemStyle: { color: CHART_SUCCESS_COLOR } },
        { name: t('platform.console.chart.pieStopped'), value: stopped < 0 ? 0 : stopped, itemStyle: { color: CHART_MUTED_COLOR } },
        { name: t('platform.console.chart.pieError'), value: o.error_app_count ?? 0, itemStyle: { color: CHART_DANGER_COLOR } },
      ],
    }],
  })
}

// 切 Tab 时等 DOM 渲染后再初始化/resize 图表。
function onTabChange(tab: string) {
  nextTick(() => {
    if (tab === 'token') { tokenChart ? tokenChart.resize() : buildTokenChart() }
    if (tab === 'orgs') { orgChart ? orgChart.resize() : buildOrgChart() }
    if (tab === 'status') { statusChart ? statusChart.resize() : buildStatusChart() }
  })
}

// resizeAll 在窗口尺寸变化时通知所有已初始化的图表重绘。
function resizeAll() {
  tokenChart?.resize()
  orgChart?.resize()
  statusChart?.resize()
}

// 数据就绪后自动重绘；watch 保证初始加载完成也触发。
watch(platformUsageItems, () => { if (activeTab.value === 'token') nextTick(buildTokenChart) })
watch(orgBreakdownData, () => { if (activeTab.value === 'orgs') nextTick(buildOrgChart) })
watch(overview, () => { if (activeTab.value === 'status') nextTick(buildStatusChart) })

onMounted(() => {
  nextTick(buildTokenChart)
  window.addEventListener('resize', resizeAll)
})

onBeforeUnmount(() => {
  window.removeEventListener('resize', resizeAll)
  tokenChart?.dispose()
  orgChart?.dispose()
  statusChart?.dispose()
})
</script>

<style scoped>
.chart-container {
  width: 100%;
  height: 320px;
}

.chart-state {
  display: flex;
  align-items: center;
  justify-content: center;
  height: 320px;
  color: var(--color-text-secondary);
  font-size: 13px;
}

.chart-state.danger { color: var(--color-danger); }
</style>
