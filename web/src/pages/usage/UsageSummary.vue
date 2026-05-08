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
      <table v-if="view.items?.length" class="usage-table">
        <thead>
          <tr>
            <th v-for="col in columns" :key="col">{{ col }}</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="(row, idx) in view.items" :key="idx">
            <td v-for="col in columns" :key="col">
              <code v-if="col === 'token_id' || col === 'date'">{{ formatCell(row[col]) }}</code>
              <span v-else>{{ formatCell(row[col]) }}</span>
            </td>
          </tr>
        </tbody>
      </table>
      <div v-else class="state-text">{{ emptyText }}</div>
    </template>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'

import type { AggregatedUsage } from '@/api/hooks/useUsage'

const props = defineProps<{ view?: AggregatedUsage; emptyText: string }>()

const itemCount = computed(() => props.view?.items?.length ?? 0)

// items 字段在 app / member（log entry）vs org / platform（quota date）之间结构不同，
// 这里按首条 item 的字段动态渲染表头：
//   - LogEntry：含 token_id / model_name / quota / created_at / ...
//   - QuotaDate：含 date / quota / count / ...
// 简化为列出所有 key（控制最多 6 列），避免硬编码字段集与后端漂移。
const columns = computed<string[]>(() => {
  const first = props.view?.items?.[0]
  if (!first) return []
  return Object.keys(first).slice(0, 6)
})

function formatCell(v: unknown): string {
  if (v === null || v === undefined) return '—'
  if (typeof v === 'number') return v.toLocaleString('en-US')
  if (typeof v === 'string') return v
  return JSON.stringify(v)
}

function formatTime(iso: string): string {
  return new Date(iso).toLocaleString('zh-CN', { hour12: false })
}
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
  color: #276d5c;
}

.usage-table {
  width: 100%;
  border-collapse: collapse;
  font-size: 13px;
}

.usage-table th,
.usage-table td {
  border: 1px solid #e5e7eb;
  padding: 6px 10px;
  text-align: left;
}

.usage-table th {
  background: #f3f4f6;
  font-weight: 600;
}

.usage-table code {
  font-family: ui-monospace, SFMono-Regular, monospace;
  font-size: 12px;
}
</style>
