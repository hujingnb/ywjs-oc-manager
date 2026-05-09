<template>
  <DataTableList
    :title="'审计日志'"
    :eyebrow="orgEyebrow"
    :columns="columns"
    :data="logs ?? []"
    :loading="isLoading"
    :error-message="errorMessage"
    :row-key="(row: AuditLog) => row.id"
  />
</template>

<script setup lang="ts">
import { computed, h } from 'vue'
import { NTag, type DataTableColumns } from 'naive-ui'

import { useOrgAuditLogsQuery } from '@/api/hooks/useAuditLogs'
import { useAuthStore } from '@/stores/auth'
import DataTableList from '@/components/DataTableList.vue'
import { timeColumn } from '@/components/columns'

const props = defineProps<{ orgId?: string }>()
const auth = useAuthStore()
const effectiveOrgId = computed(() => props.orgId ?? auth.user?.org_id)
const orgEyebrow = computed(() => auth.user?.role === 'platform_admin' ? 'Platform · 审计' : '组织 · 审计')

const { data: logs, isLoading, error } = useOrgAuditLogsQuery(effectiveOrgId)

// 无关联组织时展示提示；有 API 错误时展示错误信息
const errorMessage = computed(() => {
  if (!effectiveOrgId.value) return '当前账号未关联组织，无法查看审计日志。'
  if (error.value) return String(error.value)
  return undefined
})

type AuditLog = NonNullable<typeof logs.value>[number]

function auditTagType(result: string): 'success' | 'warning' | 'error' | 'default' {
  switch (result) {
    case 'success': return 'success'
    case 'failed': case 'error': return 'error'
    case 'partial': return 'warning'
    default: return 'default'
  }
}

const columns: DataTableColumns<AuditLog> = [
  timeColumn('时间', r => r.created_at),
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
