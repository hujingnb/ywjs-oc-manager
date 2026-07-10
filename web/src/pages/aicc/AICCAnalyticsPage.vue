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
        <div class="placeholder-chart">
          <span>今日</span>
          <strong>{{ analytics?.today_sessions ?? 0 }}</strong>
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
const unreadPercent = computed(() => {
  const count = analytics.value?.unread_leads ?? 0
  return `${Math.min(100, Math.max(8, count * 16))}%`
})
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

.placeholder-chart {
  display: grid;
  place-items: center;
  min-height: 130px;
  border: 1px dashed var(--color-border);
  border-radius: 8px;
  background: var(--color-surface);
}

.placeholder-chart strong {
  font-size: 32px;
}

.lead-split {
  display: grid;
  grid-template-columns: auto minmax(0, 1fr) auto;
  align-items: center;
  gap: 12px;
}

.split-bar {
  height: 12px;
  overflow: hidden;
  border-radius: 999px;
  background: var(--color-border);
}

.split-bar i {
  display: block;
  height: 100%;
  border-radius: inherit;
  background: #f97316;
}

@media (max-width: 900px) {
  .metric-grid,
  .insight-grid {
    grid-template-columns: 1fr;
  }
}
</style>
