<template>
  <div>
    <div v-if="!view" class="state-text">{{ emptyText }}</div>
    <template v-else>
      <p class="summary-line">
        <strong>记录数：</strong>
        <span class="quota">{{ itemCount }}</span>
        <span v-if="view.total !== undefined" class="state-text">total {{ view.total }}</span>
        <span class="state-text">最近更新：{{ formatTime(view.updated_at) }}</span>
      </p>
      <n-data-table
        v-if="view.items?.length"
        :columns="tableColumns"
        :data="view.items"
        size="small"
        :bordered="false"
      />
      <div v-else class="state-text">{{ emptyText }}</div>
    </template>
  </div>
</template>

<script setup lang="ts">
import { computed, h } from 'vue'
import { NDataTable, type DataTableColumns } from 'naive-ui'

import type { AggregatedUsage } from '@/api/hooks/useUsage'

// UsageSummary 渲染聚合用量结果，支持不同维度共享同一套空态和表格展示。
const props = defineProps<{ view?: AggregatedUsage; emptyText: string }>()

// itemCount 只统计当前响应中的 items，total 若存在由后端单独展示。
const itemCount = computed(() => props.view?.items?.length ?? 0)

// formatCell 统一处理空值、数字和结构化字段，避免表格出现 undefined/null。
function formatCell(v: unknown): string {
  if (v === null || v === undefined) return '—'
  if (typeof v === 'number') return v.toLocaleString('en-US')
  if (typeof v === 'string') return v
  return JSON.stringify(v)
}

// formatTime 将聚合结果更新时间展示为中文本地时间。
function formatTime(iso: string): string {
  return new Date(iso).toLocaleString('zh-CN', { hour12: false })
}

// tableColumns 根据首行动态生成最多 6 列；token_id/date 用等宽字体便于识别。
const tableColumns = computed<DataTableColumns<Record<string, unknown>>>(() => {
  const first = props.view?.items?.[0]
  if (!first) return []
  return Object.keys(first).slice(0, 6).map((col) => ({
    title: col,
    key: col,
    render: (row: Record<string, unknown>) => {
      const v = row[col]
      if (col === 'token_id' || col === 'date') {
        return h('code', { style: 'font-family: ui-monospace, SFMono-Regular, monospace; font-size: 12px; color: #00F0FF' }, formatCell(v))
      }
      return String(formatCell(v))
    },
  }))
})
</script>

<style scoped>
.summary-line {
  display: flex;
  gap: 16px;
  align-items: baseline;
  margin-bottom: 12px;
}

.quota {
  font-size: 20px;
  font-weight: 600;
  color: #00F0FF;
}
</style>
