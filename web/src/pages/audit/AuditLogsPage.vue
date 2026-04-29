<template>
  <main class="dashboard-main">
    <section class="panel">
      <div class="panel-heading">
        <div>
          <p class="eyebrow">{{ orgEyebrow }}</p>
          <h2>审计日志</h2>
        </div>
      </div>

      <div v-if="!effectiveOrgId" class="state-text">当前账号未关联组织，无法查看审计日志。</div>
      <template v-else>
        <div v-if="isLoading" class="state-text">加载中…</div>
        <div v-else-if="error" class="state-text danger">查询失败：{{ error.message }}</div>
        <table v-else>
          <thead>
            <tr>
              <th>时间</th>
              <th>操作者</th>
              <th>资源</th>
              <th>操作</th>
              <th>结果</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="log in logs" :key="log.id">
              <td>{{ formatTime(log.created_at) }}</td>
              <td>
                <strong>{{ log.actor_role }}</strong>
                <small v-if="log.actor_id">{{ log.actor_id }}</small>
              </td>
              <td>
                <strong>{{ log.target_type }}</strong>
                <small>{{ log.target_id }}</small>
              </td>
              <td>{{ log.action }}</td>
              <td>
                <span :class="['status-pill', auditTone(log.result)]">{{ log.result }}</span>
                <small v-if="log.error_message" class="danger-text">{{ log.error_message }}</small>
              </td>
            </tr>
            <tr v-if="!logs?.length">
              <td colspan="5" class="state-text">暂无审计记录</td>
            </tr>
          </tbody>
        </table>
      </template>
    </section>
  </main>
</template>

<script setup lang="ts">
import { computed } from 'vue'

import { useOrgAuditLogsQuery } from '@/api/hooks/useAuditLogs'
import { useAuthStore } from '@/stores/auth'

const props = defineProps<{ orgId?: string }>()
const auth = useAuthStore()
const effectiveOrgId = computed(() => props.orgId ?? auth.user?.org_id)
const orgEyebrow = computed(() => (auth.user?.role === 'platform_admin' ? 'Platform · 审计' : '组织 · 审计'))

const { data: logs, isLoading, error } = useOrgAuditLogsQuery(effectiveOrgId)

function auditTone(result: string): 'success' | 'warning' | 'danger' | 'neutral' {
  switch (result) {
    case 'success':
      return 'success'
    case 'failed':
    case 'error':
      return 'danger'
    case 'partial':
      return 'warning'
    default:
      return 'neutral'
  }
}

function formatTime(value: string): string {
  if (!value) return '—'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  return date.toLocaleString('zh-CN', { hour12: false })
}
</script>
