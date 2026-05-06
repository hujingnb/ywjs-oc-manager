<template>
  <div>
    <div v-if="!view" class="state-text">{{ emptyText }}</div>
    <template v-else>
      <p class="summary-line">
        <strong>合计余额：</strong>
        <span class="quota">{{ formatQuota(view.total_remain_quota) }}</span>
        <span class="state-text">最近更新：{{ formatTime(view.updated_at) }}</span>
      </p>
      <table v-if="view.apps?.length">
        <thead>
          <tr>
            <th>应用 ID</th>
            <th>NewAPI Token</th>
            <th>剩余额度</th>
            <th>状态</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="app in view.apps" :key="app.app_id">
            <td><code>{{ app.app_id.slice(0, 12) }}</code></td>
            <td>
              <code v-if="app.newapi_key_id">{{ app.newapi_key_id }}</code>
              <span v-else class="state-text">未绑定</span>
            </td>
            <td>{{ formatQuota(app.remain_quota) }}</td>
            <td>
              <span :class="['status-badge', `status-${app.status}`]">{{ statusLabel(app.status) }}</span>
            </td>
          </tr>
        </tbody>
      </table>
      <div v-else class="state-text">{{ emptyText }}</div>
    </template>
  </div>
</template>

<script setup lang="ts">
import type { AggregatedUsage } from '@/api/hooks/useUsage'

defineProps<{ view?: AggregatedUsage; emptyText: string }>()

// new-api 用 1/2 表示启用/禁用；其它值（0 等）一律按未知处理。
function statusLabel(s: number): string {
  if (s === 1) return '启用'
  if (s === 2) return '禁用'
  return '未知'
}

// new-api quota 按 50 万 = 1 美元 缩放，但 manager v1.0 RC 仅展示原始值；
// 这里附上千分位简化人眼阅读。
function formatQuota(value: number): string {
  return value.toLocaleString('en-US')
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

.status-badge {
  display: inline-block;
  padding: 2px 8px;
  border-radius: 10px;
  font-size: 12px;
}

.status-1 {
  background: #e6f7e0;
  color: #2c7a2c;
}

.status-2 {
  background: #ffe1e1;
  color: #b51d1d;
}
</style>
