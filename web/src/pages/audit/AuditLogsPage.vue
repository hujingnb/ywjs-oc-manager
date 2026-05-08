<template>
  <n-card :bordered="true">
    <template #header>
      <div>
        <p class="eyebrow">{{ orgEyebrow }}</p>
        <h2 style="margin: 0">审计日志</h2>
      </div>
    </template>

    <div v-if="!effectiveOrgId" class="state-text">当前账号未关联组织，无法查看审计日志。</div>
    <n-data-table
      v-else
      :columns="columns"
      :data="logs ?? []"
      :loading="isLoading"
      size="small"
      :bordered="false"
    />
  </n-card>
</template>

<script setup lang="ts">
import { computed, h } from 'vue'
import { NCard, NDataTable, NTag, type DataTableColumns } from 'naive-ui'

import { useOrgAuditLogsQuery } from '@/api/hooks/useAuditLogs'
import { useAuthStore } from '@/stores/auth'

const props = defineProps<{ orgId?: string }>()
const auth = useAuthStore()
const effectiveOrgId = computed(() => props.orgId ?? auth.user?.org_id)
const orgEyebrow = computed(() => auth.user?.role === 'platform_admin' ? 'Platform · 审计' : '组织 · 审计')

const { data: logs, isLoading, error } = useOrgAuditLogsQuery(effectiveOrgId)

void error

type AuditLog = NonNullable<typeof logs.value>[number]

function auditTagType(result: string): 'success' | 'warning' | 'error' | 'default' {
  switch (result) {
    case 'success': return 'success'
    case 'failed': case 'error': return 'error'
    case 'partial': return 'warning'
    default: return 'default'
  }
}

function formatTime(value: string): string {
  if (!value) return '—'
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString('zh-CN', { hour12: false })
}

const columns: DataTableColumns<AuditLog> = [
  { title: '时间', key: 'created_at', render: (row) => formatTime(row.created_at) },
  {
    title: '操作者', key: 'actor_role',
    render: (row) => [
      h('strong', row.actor_role),
      row.actor_id ? h('small', { style: 'display:block;color:#8A94C6;font-size:12px' }, row.actor_id) : null,
    ],
  },
  {
    title: '资源', key: 'target_type',
    render: (row) => [
      h('strong', row.target_type),
      h('small', { style: 'display:block;color:#8A94C6;font-size:12px' }, row.target_id),
    ],
  },
  { title: '操作', key: 'action' },
  {
    title: '结果', key: 'result',
    render: (row) => [
      h(NTag, { type: auditTagType(row.result), size: 'small', bordered: false }, { default: () => row.result }),
      row.error_message ? h('small', { style: 'display:block;color:#FF3B5C;font-size:12px' }, row.error_message) : null,
    ],
  },
]
</script>
