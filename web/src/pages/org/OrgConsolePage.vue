<template>
  <div style="display: grid; gap: 18px">
    <!-- 统计条 -->
    <n-grid :cols="6" :x-gap="14" :y-gap="14" responsive="screen" :item-responsive="true">
      <n-grid-item v-for="stat in stats" :key="stat.label" :span="1" :xs="3" :sm="2" :md="1">
        <n-card size="small" :bordered="true">
          <n-statistic :label="stat.label" :value="stat.value" />
          <div
            v-if="stat.note"
            style="font-size: 11px; margin-top: 4px"
            :style="{ color: stat.noteColor ?? '#8A94C6' }"
          >
            {{ stat.note }}
          </div>
        </n-card>
      </n-grid-item>
    </n-grid>

    <!-- 图表区 Tab -->
    <n-card :bordered="true">
      <n-tabs v-model:value="activeTab" type="line" animated @update:value="onTabChange">
        <!-- Tab 1：用量趋势 -->
        <n-tab-pane name="usage" tab="用量趋势">
          <div v-if="usageLoading" class="chart-state">加载中…</div>
          <div v-else-if="usageError" class="chart-state danger">用量服务不可用</div>
          <div v-else-if="!usageItems?.length" class="chart-state">暂无数据</div>
          <div v-else ref="usageChartEl" class="chart-container" />
        </n-tab-pane>

        <!-- Tab 2：实例状态 -->
        <n-tab-pane name="status" tab="实例状态">
          <div v-if="appsLoading" class="chart-state">加载中…</div>
          <div v-else-if="appsError" class="chart-state danger">实例数据不可用</div>
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
import { LineChart, PieChart } from 'echarts/charts'
import { GridComponent, TooltipComponent, LegendComponent } from 'echarts/components'
import { CanvasRenderer } from 'echarts/renderers'
import type { EChartsType } from 'echarts/core'
import { useQuery } from '@tanstack/vue-query'

import { useAuthStore } from '@/stores/auth'
import { apiRequest } from '@/api/client'
import { useMembersQuery } from '@/api/hooks/useMembers'
import { useAppsByOrgQuery } from '@/api/hooks/useApps'
import { useOrgBalanceQuery, useBillingStatusQuery } from '@/api/hooks/useRecharge'
import { formatQuotaValue } from '@/pages/usage/usageFormatting'

use([CanvasRenderer, LineChart, PieChart, GridComponent, TooltipComponent, LegendComponent])

// OrgConsolePage 是组织管理员专属的控制台首页：统计条 + 用量趋势/实例状态两图。
const auth = useAuthStore()
const isOrgAdmin = computed(() => auth.isOrgAdmin)
const orgId = computed(() => auth.user?.org_id)

// ── 数据查询 ──────────────────────────────────────────────
const { data: members, isLoading: membersLoading } = useMembersQuery(orgId)
const { data: apps, isLoading: appsLoading, error: appsError } = useAppsByOrgQuery(orgId)
const { data: balance } = useOrgBalanceQuery(orgId)
const { data: billingStatus } = useBillingStatusQuery()

// 近 7 天组织维度用量序列：用于用量趋势折线图和「今日 Token」统计卡片。
const { data: usageItems, isLoading: usageLoading, error: usageError } = useQuery<
  { date: string; quota: number }[]
>({
  queryKey: ['usage', 'org-console', orgId],
  enabled: () => isOrgAdmin.value && Boolean(orgId.value),
  refetchInterval: 60000,
  queryFn: async () => {
    if (!orgId.value) return []
    const now = Math.floor(Date.now() / 1000)
    const since = now - 7 * 24 * 60 * 60
    const resp = await apiRequest<{ usage: { items: { date: string; quota: number }[] } }>(
      `/api/v1/usage/organizations/${orgId.value}`,
      { query: { since: String(since), until: String(now) } },
    )
    return resp.usage?.items ?? []
  },
})

// ── 统计卡片 ──────────────────────────────────────────────
// todayTokenTotal 把今天（本地日期）在 usageItems 中的 quota 求和。
const todayTokenTotal = computed(() => {
  if (!usageItems.value?.length) return null
  const today = new Date().toISOString().slice(0, 10)
  return usageItems.value
    .filter(item => item.date === today)
    .reduce((acc, item) => acc + item.quota, 0)
})

// stats 将 members/apps/balance/usage 汇总为统计卡片数组，顺序：成员、实例、运行中、异常、余额、今日 Token。
const stats = computed(() => {
  const runningCount = apps.value?.filter(a => a.status === 'running').length ?? 0
  const errorCount = apps.value?.filter(a => a.status === 'error').length ?? 0
  const remainQuota = balance.value?.remain_quota ?? null
  return [
    {
      label: '成员数',
      value: membersLoading.value ? '—' : String(members.value?.length ?? 0),
      note: '',
      noteColor: undefined,
    },
    {
      label: '实例数',
      value: appsLoading.value ? '—' : String(apps.value?.length ?? 0),
      note: '',
      noteColor: undefined,
    },
    {
      label: '运行中',
      value: appsLoading.value ? '—' : String(runningCount),
      note: '',
      noteColor: '#18a058',
    },
    {
      label: '异常',
      value: appsLoading.value ? '—' : String(errorCount),
      note: '',
      noteColor: '#d03050',
    },
    {
      label: '当前余额',
      value: remainQuota !== null ? formatQuotaValue(remainQuota, billingStatus.value) : '—',
      note: 'new-api 实时',
      noteColor: undefined,
    },
    {
      label: '今日 Token',
      value: todayTokenTotal.value !== null
        ? todayTokenTotal.value.toLocaleString('en-US')
        : '—',
      note: todayTokenTotal.value !== null
        ? 'new-api 实时'
        : usageLoading.value ? '加载中…' : '不可用',
      noteColor: undefined,
    },
  ]
})

// ── 图表 ──────────────────────────────────────────────────
const activeTab = ref<'usage' | 'status'>('usage')
const usageChartEl = ref<HTMLElement | null>(null)
const statusChartEl = ref<HTMLElement | null>(null)

let usageChart: EChartsType | null = null
let statusChart: EChartsType | null = null

// formatQuota 将 new-api quota 原始值格式化为可读的万/千/百万单位。
function formatQuota(v: number): string {
  if (v >= 1_000_000) return `${(v / 1_000_000).toFixed(1)}M`
  if (v >= 10_000) return `${(v / 10_000).toFixed(1)}W`
  if (v >= 1_000) return `${(v / 1_000).toFixed(1)}k`
  return String(v)
}

// ── 用量趋势图（折线） ──
function buildUsageChart() {
  if (!usageChartEl.value || !usageItems.value?.length) return
  if (!usageChart) usageChart = init(usageChartEl.value)

  // 按日聚合：同一天可能有多个 model 条目。
  const byDate = new Map<string, number>()
  for (const item of usageItems.value) {
    byDate.set(item.date, (byDate.get(item.date) ?? 0) + item.quota)
  }
  const sorted = [...byDate.entries()].sort(([a], [b]) => a.localeCompare(b))
  const dates = sorted.map(([d]) => d.slice(5)) // MM-DD
  const values = sorted.map(([, v]) => v)

  usageChart.setOption({
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
      lineStyle: { width: 2, color: '#18a058' },
      itemStyle: { color: '#18a058' },
      areaStyle: { color: 'rgba(24,160,88,0.08)' },
    }],
  })
}

// ── 实例状态图（饼图） ──
function buildStatusChart() {
  if (!statusChartEl.value || !apps.value) return
  if (!statusChart) statusChart = init(statusChartEl.value)

  const appList = apps.value
  const running = appList.filter(a => a.status === 'running').length
  const error = appList.filter(a => a.status === 'error').length
  const stopped = appList.length - running - error

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
        { name: '运行中', value: running, itemStyle: { color: '#18a058' } },
        { name: '停止', value: stopped < 0 ? 0 : stopped, itemStyle: { color: '#63748a' } },
        { name: '异常', value: error, itemStyle: { color: '#d03050' } },
      ],
    }],
  })
}

// 切 Tab 时等 DOM 渲染后再初始化/resize 图表。
function onTabChange(tab: string) {
  nextTick(() => {
    if (tab === 'usage') { usageChart ? usageChart.resize() : buildUsageChart() }
    if (tab === 'status') { statusChart ? statusChart.resize() : buildStatusChart() }
  })
}

// resizeAll 在窗口尺寸变化时通知所有已初始化的图表重绘。
function resizeAll() {
  usageChart?.resize()
  statusChart?.resize()
}

// 数据就绪后自动重绘；watch 保证初始加载完成也触发。
watch(usageItems, () => { if (activeTab.value === 'usage') nextTick(buildUsageChart) })
watch(apps, () => { if (activeTab.value === 'status') nextTick(buildStatusChart) })

onMounted(() => {
  nextTick(buildUsageChart)
  window.addEventListener('resize', resizeAll)
})

onBeforeUnmount(() => {
  window.removeEventListener('resize', resizeAll)
  usageChart?.dispose()
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
