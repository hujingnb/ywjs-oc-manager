<template>
  <div style="display: grid; gap: 18px">
    <!-- 统计条 -->
    <n-grid :cols="6" :x-gap="14" :y-gap="14" responsive="screen" :item-responsive="true">
      <n-grid-item v-for="stat in stats" :key="stat.label" :span="1" :xs="3" :sm="2" :md="1">
        <n-card size="small" :bordered="true">
          <n-statistic :label="stat.label" :value="stat.value">
            <template v-if="stat.suffix" #suffix>
              <span style="font-size: 11px; color: #8A94C6">{{ stat.suffix }}</span>
            </template>
          </n-statistic>
          <div v-if="stat.note" style="font-size: 11px; margin-top: 4px" :style="{ color: stat.noteColor ?? '#8A94C6' }">
            {{ stat.note }}
          </div>
        </n-card>
      </n-grid-item>
    </n-grid>

    <!-- 图表区 Tab -->
    <n-card :bordered="true" style="flex: 1">
      <n-tabs v-model:value="activeTab" type="line" animated @update:value="onTabChange">
        <!-- Tab 1：Token 趋势 -->
        <n-tab-pane name="token" tab="Token 趋势">
          <div v-if="platformUsageLoading" class="chart-state">加载中…</div>
          <div v-else-if="platformUsageError" class="chart-state danger">用量服务不可用</div>
          <div v-else ref="tokenChartEl" class="chart-container" />
        </n-tab-pane>

        <!-- Tab 2：各组织用量 -->
        <n-tab-pane name="orgs" tab="各组织用量">
          <div v-if="orgBreakdownLoading" class="chart-state">加载中…</div>
          <div v-else-if="orgBreakdownError" class="chart-state danger">用量服务不可用</div>
          <div v-else-if="!orgBreakdownData?.length" class="chart-state">暂无数据</div>
          <div v-else ref="orgChartEl" class="chart-container" />
        </n-tab-pane>

        <!-- Tab 3：实例状态 -->
        <n-tab-pane name="status" tab="实例状态">
          <div v-if="overviewLoading" class="chart-state">加载中…</div>
          <div v-else ref="statusChartEl" class="chart-container" />
        </n-tab-pane>
      </n-tabs>
    </n-card>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
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
const auth = useAuthStore()
const isPlatformAdmin = computed(() => auth.isPlatformAdmin)

// ── 数据查询 ──────────────────────────────────────────────
const { data: overview, isLoading: overviewLoading } = usePlatformOverviewQuery(isPlatformAdmin)
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

// stats 将 overview + today token 转为统计卡片数组。
const stats = computed(() => {
  const o = overview.value
  return [
    { label: '组织数', value: String(o?.organization_count ?? '—'), note: '', noteColor: undefined, suffix: undefined },
    { label: '成员数', value: String(o?.member_count ?? '—'), note: '不含平台管理员', noteColor: undefined, suffix: undefined },
    { label: '实例数', value: String(o?.app_count ?? '—'), note: '', noteColor: undefined, suffix: undefined },
    { label: '运行中', value: String(o?.running_app_count ?? '—'), note: '', noteColor: '#18a058', suffix: undefined },
    { label: '异常', value: String(o?.error_app_count ?? '—'), note: '', noteColor: '#d03050', suffix: undefined },
    {
      label: '今日 Token',
      value: todayTokenTotal.value !== null ? String(todayTokenTotal.value.toLocaleString('en-US')) : '—',
      note: todayTokenTotal.value !== null ? 'new-api 实时' : platformUsageLoading.value ? '加载中…' : '不可用',
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
      axisLabel: { color: '#8A94C6', fontSize: 11 },
      axisLine: { lineStyle: { color: '#30363d' } },
      axisTick: { show: false },
    },
    yAxis: {
      type: 'value',
      axisLabel: { color: '#8A94C6', fontSize: 11, formatter: (v: number) => formatQuota(v) },
      splitLine: { lineStyle: { color: '#2d3139' } },
    },
    series: [{
      type: 'line',
      data: values,
      smooth: true,
      showSymbol: true,
      symbolSize: 5,
      lineStyle: { width: 2, color: '#1f6feb' },
      itemStyle: { color: '#1f6feb' },
      areaStyle: { color: 'rgba(31,111,235,0.08)' },
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
      axisLabel: { color: '#8A94C6', fontSize: 11, formatter: (v: number) => formatQuota(v) },
      splitLine: { lineStyle: { color: '#2d3139' } },
    },
    yAxis: {
      type: 'category',
      data: items.map(i => i.org_name),
      axisLabel: { color: '#8A94C6', fontSize: 11, width: 110, overflow: 'truncate' },
      axisLine: { show: false },
      axisTick: { show: false },
    },
    series: [{
      type: 'bar',
      data: items.map(i => i.total_quota),
      itemStyle: { color: '#1f6feb', borderRadius: [0, 3, 3, 0] },
      label: { show: true, position: 'right', color: '#8A94C6', fontSize: 11, formatter: (p: { value: number }) => formatQuota(p.value) },
    }],
  })
}

// ── 实例状态图（饼图） ──
function buildStatusChart() {
  if (!statusChartEl.value || !overview.value) return
  if (!statusChart) statusChart = init(statusChartEl.value)

  const o = overview.value
  const stopped = (o.app_count ?? 0) - (o.running_app_count ?? 0) - (o.error_app_count ?? 0)
  statusChart.setOption({
    animation: false,
    tooltip: { trigger: 'item', formatter: '{b}: {c} ({d}%)' },
    legend: { bottom: 0, textStyle: { color: '#8A94C6', fontSize: 12 } },
    series: [{
      type: 'pie',
      radius: ['40%', '68%'],
      center: ['50%', '44%'],
      itemStyle: { borderWidth: 2, borderColor: '#0d1117' },
      label: { show: false },
      data: [
        { name: '运行中', value: o.running_app_count ?? 0, itemStyle: { color: '#18a058' } },
        { name: '停止', value: stopped < 0 ? 0 : stopped, itemStyle: { color: '#63748a' } },
        { name: '异常', value: o.error_app_count ?? 0, itemStyle: { color: '#d03050' } },
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

// 数据就绪后自动重绘；watch 保证初始加载完成也触发。
watch(platformUsageItems, () => { if (activeTab.value === 'token') nextTick(buildTokenChart) })
watch(orgBreakdownData, () => { if (activeTab.value === 'orgs') nextTick(buildOrgChart) })
watch(overview, () => { if (activeTab.value === 'status') nextTick(buildStatusChart) })

onMounted(() => { nextTick(buildTokenChart) })

onBeforeUnmount(() => {
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
  color: #8a94c6;
  font-size: 13px;
}

.chart-state.danger { color: #d03050; }
</style>
