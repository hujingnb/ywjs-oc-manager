<template>
  <section class="analytics-view">
    <n-alert v-if="analyticsQuery.error.value" type="error" :bordered="false">
      {{ analyticsQuery.error.value.message }}
    </n-alert>

    <div class="analytics-toolbar">
      <n-button-group>
        <n-button size="small" :type="rangePreset === 'today' ? 'primary' : 'default'" @click="setRangePreset('today')">今天</n-button>
        <n-button size="small" :type="rangePreset === '7d' ? 'primary' : 'default'" @click="setRangePreset('7d')">近 7 天</n-button>
        <n-button size="small" :type="rangePreset === '30d' ? 'primary' : 'default'" @click="setRangePreset('30d')">近 30 天</n-button>
      </n-button-group>
      <n-radio-group v-model:value="bucket" size="small">
        <n-radio-button value="day">日</n-radio-button>
        <n-radio-button value="week">周</n-radio-button>
      </n-radio-group>
    </div>

    <div class="metric-grid">
      <div class="metric-tile">
        <span>今日会话</span>
        <strong>{{ analytics?.today_sessions ?? '-' }}</strong>
      </div>
      <div class="metric-tile">
        <span>筛选会话</span>
        <strong>{{ analytics?.total_sessions ?? '-' }}</strong>
      </div>
      <div class="metric-tile">
        <span>未读线索</span>
        <strong>{{ analytics?.unread_leads ?? '-' }}</strong>
      </div>
      <div class="metric-tile">
        <span>已解决</span>
        <strong>{{ analytics?.resolved_sessions ?? '-' }}</strong>
      </div>
      <div class="metric-tile">
        <span>未解决</span>
        <strong>{{ analytics?.unresolved_sessions ?? '-' }}</strong>
      </div>
      <div class="metric-tile">
        <span>未知状态</span>
        <strong>{{ analytics?.unknown_sessions ?? '-' }}</strong>
      </div>
      <div class="metric-tile">
        <span>未解决率</span>
        <strong>{{ unresolvedRateText }}</strong>
      </div>
      <div class="metric-tile">
        <span>已留资</span>
        <strong>{{ analytics?.completed_lead_sessions ?? '-' }}</strong>
      </div>
      <div class="metric-tile">
        <span>智能体数量</span>
        <strong>{{ agentCount }}</strong>
      </div>
      <div class="metric-tile">
        <span>接待中</span>
        <strong>{{ activeAgentCount }}</strong>
      </div>
    </div>

    <div class="insight-grid">
      <article class="insight-panel">
        <div class="panel-heading">
          <div>
            <p class="eyebrow">Trend</p>
            <h4>会话趋势</h4>
          </div>
          <BarChart3 :size="18" />
        </div>
        <div v-if="sessionTrend.length === 0" class="empty-list">暂无趋势数据</div>
        <div v-else class="trend-list">
          <div v-for="item in sessionTrend" :key="item.bucket">
            <span>{{ item.bucket }}</span>
            <i><b :style="{ width: trendPercent(item.count) }" /></i>
            <strong>{{ item.count }}</strong>
          </div>
        </div>
      </article>

      <article class="insight-panel">
        <div class="panel-heading">
          <div>
            <p class="eyebrow">Follow-up</p>
            <h4>线索状态</h4>
          </div>
          <ListChecks :size="18" />
        </div>
        <div class="lead-split">
          <span>未读</span>
          <div class="split-bar">
            <i :style="{ width: unreadPercent }" />
          </div>
          <strong>{{ analytics?.unread_leads ?? 0 }}</strong>
        </div>
        <div class="resolution-bars">
          <div>
            <span>未解决率</span>
            <i><b :style="{ width: unresolvedRateBar }" /></i>
            <strong>{{ unresolvedRateText }}</strong>
          </div>
          <div>
            <span>未知状态</span>
            <i><b :style="{ width: unknownPercent }" /></i>
            <strong>{{ analytics?.unknown_sessions ?? 0 }}</strong>
          </div>
        </div>
      </article>

      <article class="insight-panel">
        <div class="panel-heading">
          <div>
            <p class="eyebrow">Regions</p>
            <h4>访客地域</h4>
          </div>
          <MapPin :size="18" />
        </div>
        <div v-if="regions.length === 0" class="empty-list">暂无地域数据</div>
        <ol v-else class="rank-list">
          <li v-for="item in regions" :key="item.label">
            <span>{{ item.label }}</span>
            <strong>{{ item.count }}</strong>
          </li>
        </ol>
      </article>

      <article class="insight-panel">
        <div class="panel-heading">
          <div>
            <p class="eyebrow">Questions</p>
            <h4>热门问题</h4>
          </div>
          <ListChecks :size="18" />
        </div>
        <div v-if="topQuestions.length === 0" class="empty-list">暂无问题数据</div>
        <ol v-else class="rank-list">
          <li v-for="item in topQuestions" :key="item.label">
            <span>{{ item.label }}</span>
            <strong>{{ item.count }}</strong>
          </li>
        </ol>
      </article>

      <article class="insight-panel">
        <div class="panel-heading">
          <div>
            <p class="eyebrow">Sources</p>
            <h4>来源页面</h4>
          </div>
          <BarChart3 :size="18" />
        </div>
        <div v-if="topSources.length === 0" class="empty-list">暂无来源数据</div>
        <ol v-else class="rank-list">
          <li v-for="item in topSources" :key="item.label">
            <span>{{ item.label }}</span>
            <strong>{{ item.count }}</strong>
          </li>
        </ol>
      </article>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { NAlert, NButton, NButtonGroup, NRadioButton, NRadioGroup } from 'naive-ui'
import { BarChart3, ListChecks, MapPin } from 'lucide-vue-next'

import { useAICCAnalyticsQuery } from '@/api/hooks/useAICC'
import type { AICCAnalyticsFilters } from '@/domain/aicc'

const props = defineProps<{
  agentId?: string
  agentCount: number
  activeAgentCount: number
}>()

type RangePreset = 'today' | '7d' | '30d'

const rangePreset = ref<RangePreset>('7d')
const bucket = ref<'day' | 'week'>('day')
const range = ref(makeRange('7d'))
const analyticsFilters = computed<AICCAnalyticsFilters>(() => ({
  start_at: range.value.start_at,
  end_at: range.value.end_at,
  bucket: bucket.value,
  agent_id: props.agentId,
}))
const analyticsQuery = useAICCAnalyticsQuery(analyticsFilters)
const analytics = computed(() => analyticsQuery.data.value)
const topQuestions = computed(() => analytics.value?.top_questions ?? [])
const topSources = computed(() => analytics.value?.top_sources ?? [])
const regions = computed(() => analytics.value?.regions ?? [])
const sessionTrend = computed(() => analytics.value?.session_trend ?? [])
const unreadPercent = computed(() => {
  const count = analytics.value?.unread_leads ?? 0
  return `${Math.min(100, Math.max(8, count * 16))}%`
})
const resolutionTotal = computed(() => (analytics.value?.resolved_sessions ?? 0) + (analytics.value?.unresolved_sessions ?? 0))
const unresolvedRateValue = computed(() => analytics.value?.unresolved_rate ?? (
  resolutionTotal.value > 0 ? (analytics.value?.unresolved_sessions ?? 0) / resolutionTotal.value : 0
))
const unresolvedRateText = computed(() => `${Math.round(unresolvedRateValue.value * 100)}%`)
const unresolvedRateBar = computed(() => percentage(unresolvedRateValue.value, 1))
const unknownPercent = computed(() => percentage(analytics.value?.unknown_sessions ?? 0, analytics.value?.total_sessions ?? 0))
const maxTrendCount = computed(() => Math.max(0, ...sessionTrend.value.map(item => item.count)))

function percentage(value: number, total: number) {
  if (total <= 0) return '0%'
  return `${Math.max(6, Math.round((value / total) * 100))}%`
}

function trendPercent(value: number) {
  return percentage(value, maxTrendCount.value)
}

function setRangePreset(preset: RangePreset) {
  rangePreset.value = preset
  range.value = makeRange(preset)
}

function makeRange(preset: RangePreset): { start_at: string; end_at: string } {
  const end = new Date()
  const start = new Date(end)
  if (preset === 'today') {
    start.setHours(0, 0, 0, 0)
  } else {
    start.setDate(end.getDate() - (preset === '7d' ? 6 : 29))
    start.setHours(0, 0, 0, 0)
  }
  return { start_at: start.toISOString(), end_at: end.toISOString() }
}
</script>

<style scoped>
.analytics-view {
  display: grid;
  gap: 14px;
}

.metric-grid,
.insight-grid {
  display: grid;
  gap: 12px;
}

.metric-grid {
  grid-template-columns: repeat(4, minmax(0, 1fr));
}

.analytics-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}

.metric-tile,
.insight-panel {
  border: 1px solid var(--color-divider);
  border-radius: 8px;
  background: var(--color-surface-muted);
}

.metric-tile {
  display: grid;
  gap: 6px;
  min-height: 92px;
  padding: 14px;
}

.metric-tile span,
.lead-split span {
  color: var(--color-text-secondary);
}

.metric-tile strong {
  font-size: 26px;
}

.insight-grid {
  grid-template-columns: minmax(0, 1fr) minmax(0, 1fr);
}

.insight-panel {
  display: grid;
  gap: 18px;
  padding: 14px;
  min-height: 210px;
}

.panel-heading {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}

.panel-heading h4 {
  margin: 2px 0 0;
  font-size: 16px;
}

.lead-split,
.resolution-bars div,
.trend-list div {
  display: grid;
  grid-template-columns: auto minmax(0, 1fr) auto;
  align-items: center;
  gap: 12px;
}

.resolution-bars,
.trend-list {
  display: grid;
  gap: 12px;
}

.split-bar,
.resolution-bars i,
.trend-list i {
  height: 12px;
  overflow: hidden;
  border-radius: 999px;
  background: var(--color-border);
}

.split-bar i,
.resolution-bars b,
.trend-list b {
  display: block;
  height: 100%;
  border-radius: inherit;
}

.split-bar i {
  background: #f97316;
}

.resolution-bars b {
  background: var(--color-brand);
}

.trend-list b {
  background: #0ea5e9;
}

.trend-list span {
  min-width: 76px;
  color: var(--color-text-secondary);
  font-size: 12px;
}

.rank-list {
  display: grid;
  gap: 10px;
  margin: 0;
  padding: 0;
  list-style: none;
}

.rank-list li {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto;
  gap: 12px;
  padding-bottom: 8px;
  border-bottom: 1px solid var(--color-divider);
}

.rank-list span {
  min-width: 0;
  overflow: hidden;
  color: var(--color-text-secondary);
  text-overflow: ellipsis;
  white-space: nowrap;
}

.empty-list {
  color: var(--color-text-secondary);
  font-size: 13px;
}

@media (max-width: 900px) {
  .analytics-toolbar {
    align-items: stretch;
    flex-direction: column;
  }

  .metric-grid,
  .insight-grid {
    grid-template-columns: 1fr;
  }
}
</style>
