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
            :style="{ color: stat.noteColor ?? 'var(--color-text-secondary)' }"
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
        <n-tab-pane name="usage" :tab="t('org.console.tabs.usageTrend')">
          <div v-if="usageLoading" class="chart-state">{{ t('common.status.loading') }}</div>
          <div v-else-if="usageError" class="chart-state danger">{{ t('org.console.state.usageUnavailable') }}</div>
          <div v-else-if="!usageItems?.length" class="chart-state">{{ t('common.status.empty') }}</div>
          <div v-else ref="usageChartEl" class="chart-container" />
        </n-tab-pane>

        <!-- Tab 2：实例状态 -->
        <n-tab-pane name="status" :tab="t('org.console.tabs.instanceStatus')">
          <div v-if="appsLoading" class="chart-state">{{ t('common.status.loading') }}</div>
          <div v-else-if="appsError" class="chart-state danger">{{ t('org.console.state.instanceUnavailable') }}</div>
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

const auth = useAuthStore()
const { t } = useI18n()
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
// 使用 computed 确保语言切换时标签文案响应式更新。
const stats = computed(() => {
  const runningCount = apps.value?.filter(a => a.status === 'running').length ?? 0
  const errorCount = apps.value?.filter(a => a.status === 'error').length ?? 0
  const remainQuota = balance.value?.remain_quota ?? null
  return [
    {
      label: t('org.console.stats.memberCount'),
      value: membersLoading.value ? '—' : String(members.value?.length ?? 0),
      note: '',
      noteColor: undefined,
    },
    {
      label: t('org.console.stats.instanceCount'),
      value: appsLoading.value ? '—' : String(apps.value?.length ?? 0),
      note: '',
      noteColor: undefined,
    },
    {
      label: t('org.console.stats.running'),
      value: appsLoading.value ? '—' : String(runningCount),
      note: '',
      noteColor: 'var(--color-success)',
    },
    {
      label: t('org.console.stats.error'),
      value: appsLoading.value ? '—' : String(errorCount),
      note: '',
      noteColor: 'var(--color-danger)',
    },
    {
      label: t('org.console.stats.currentBalance'),
      value: remainQuota !== null ? formatQuotaValue(remainQuota, billingStatus.value) : '—',
      note: t('org.console.stats.realtimeNote'),
      noteColor: undefined,
    },
    {
      label: t('org.console.stats.todayTokens'),
      value: todayTokenTotal.value !== null
        ? todayTokenTotal.value.toLocaleString('en-US')
        : '—',
      note: todayTokenTotal.value !== null
        ? t('org.console.stats.realtimeNote')
        : usageLoading.value ? t('common.status.loading') : t('org.console.stats.unavailable'),
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

// ── 实例状态图（饼图） ──
// buildStatusChart 使用当前 t() 值；语言切换时图表标签不会自动重绘（ECharts 无响应式），
// 需切 Tab 或 resize 时重建才生效，此为已知限制，可接受。
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
    legend: { bottom: 0, textStyle: { color: CHART_TEXT_COLOR, fontSize: 12 } },
    series: [{
      type: 'pie',
      radius: ['40%', '68%'],
      center: ['50%', '44%'],
      itemStyle: { borderWidth: 2, borderColor: CHART_PIE_BORDER },
      label: { show: false },
      data: [
        { name: t('org.console.chart.running'), value: running, itemStyle: { color: CHART_SUCCESS_COLOR } },
        { name: t('org.console.chart.stopped'), value: stopped < 0 ? 0 : stopped, itemStyle: { color: CHART_MUTED_COLOR } },
        { name: t('org.console.chart.error'), value: error, itemStyle: { color: CHART_DANGER_COLOR } },
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
  color: var(--color-text-secondary);
  font-size: 13px;
}

.chart-state.danger { color: var(--color-danger); }
</style>
