<template>
  <DataTableList
    :title="'应用审计'"
    :eyebrow="'App · Audit'"
    :columns="columns"
    :data="logs ?? []"
    :loading="isLoading"
    :error-message="errorMessage"
    :row-key="(row: AuditLog) => row.id"
  />
</template>

<script setup lang="ts">
import { computed, h, inject, type Ref } from 'vue'
import { NTag, type DataTableColumns } from 'naive-ui'

import type { AuditLog } from '@/api'
import { useTargetAuditLogsQuery } from '@/api/hooks/useAuditLogs'
import type { AppDTO } from '@/api/hooks/useApps'
import DataTableList from '@/components/DataTableList.vue'
import { timeColumn } from '@/components/columns'
import { canViewOwnAppAudit } from '@/domain/permissions'
import { useAuthStore } from '@/stores/auth'

const props = defineProps<{ appId: string }>()
const auth = useAuthStore()
const app = inject<Ref<AppDTO | null>>('app')
const canView = computed(() => canViewOwnAppAudit(auth.user, app?.value))
const target = computed(() => canView.value ? { targetType: 'app', targetId: props.appId } : undefined)
const { data: logs, isLoading, error } = useTargetAuditLogsQuery(target)

const errorMessage = computed(() => {
  if (!canView.value) return '当前账号无权查看该应用审计。'
  if (error.value) return String(error.value)
  return undefined
})

function auditTagType(result: string): 'success' | 'warning' | 'error' | 'default' {
  switch (result) {
    case 'success': return 'success'
    case 'succeeded': return 'success'
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
