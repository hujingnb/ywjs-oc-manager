<template>
  <section class="analytics-view">
    <n-alert v-if="analyticsQuery.error.value" type="error" :bordered="false">
      {{ analyticsQuery.error.value.message }}
    </n-alert>

    <div class="metric-grid">
      <div class="metric-tile">
        <span>今日会话</span>
        <strong>{{ analytics?.today_sessions ?? '-' }}</strong>
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
        <div class="resolution-bars">
          <div>
            <span>已解决</span>
            <i><b :style="{ width: resolvedPercent }" /></i>
            <strong>{{ analytics?.resolved_sessions ?? 0 }}</strong>
          </div>
          <div>
            <span>未解决</span>
            <i><b :style="{ width: unresolvedPercent }" /></i>
            <strong>{{ analytics?.unresolved_sessions ?? 0 }}</strong>
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
import { computed } from 'vue'
import { NAlert } from 'naive-ui'
import { BarChart3, ListChecks } from 'lucide-vue-next'

import { useAICCAnalyticsQuery } from '@/api/hooks/useAICC'

defineProps<{
  agentCount: number
  activeAgentCount: number
}>()

const analyticsQuery = useAICCAnalyticsQuery()
const analytics = computed(() => analyticsQuery.data.value)
const topQuestions = computed(() => analytics.value?.top_questions ?? [])
const topSources = computed(() => analytics.value?.top_sources ?? [])
const unreadPercent = computed(() => {
  const count = analytics.value?.unread_leads ?? 0
  return `${Math.min(100, Math.max(8, count * 16))}%`
})
const resolutionTotal = computed(() => (analytics.value?.resolved_sessions ?? 0) + (analytics.value?.unresolved_sessions ?? 0))
const resolvedPercent = computed(() => percentage(analytics.value?.resolved_sessions ?? 0, resolutionTotal.value))
const unresolvedPercent = computed(() => percentage(analytics.value?.unresolved_sessions ?? 0, resolutionTotal.value))

function percentage(value: number, total: number) {
  if (total <= 0) return '0%'
  return `${Math.max(6, Math.round((value / total) * 100))}%`
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
  grid-template-columns: repeat(6, minmax(0, 1fr));
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
.resolution-bars div {
  display: grid;
  grid-template-columns: auto minmax(0, 1fr) auto;
  align-items: center;
  gap: 12px;
}

.resolution-bars {
  display: grid;
  gap: 12px;
}

.split-bar,
.resolution-bars i {
  height: 12px;
  overflow: hidden;
  border-radius: 999px;
  background: var(--color-border);
}

.split-bar i,
.resolution-bars b {
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
  .metric-grid,
  .insight-grid {
    grid-template-columns: 1fr;
  }
}
</style>
