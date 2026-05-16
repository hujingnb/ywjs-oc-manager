<template>
  <DataTableList
    :title="'审计日志'"
    :eyebrow="orgEyebrow"
    :columns="columns"
    :data="logs ?? []"
    :loading="isLoading || organizationsLoading"
    :error-message="errorMessage"
    :row-key="(row: AuditLog) => row.id"
  >
    <template #toolbar>
      <n-select
        v-if="isPlatformAdmin"
        v-model:value="selectedOrgId"
        :options="orgOptions"
        style="width: 220px"
        placeholder="选择组织"
      />
    </template>
  </DataTableList>
</template>

<script setup lang="ts">
import { computed, h } from 'vue'
import { NSelect, NTag, type DataTableColumns } from 'naive-ui'

import { useOrgAuditLogsQuery } from '@/api/hooks/useAuditLogs'
import { usePlatformOrgSelection } from '@/composables/usePlatformOrgSelection'
import { canViewOrgAudit } from '@/domain/permissions'
import { useAuthStore } from '@/stores/auth'
import DataTableList from '@/components/DataTableList.vue'
import { timeColumn } from '@/components/columns'

// AuditLogsPage 展示组织级审计日志，平台和组织管理员可看，普通成员需去应用详情查看自己的应用审计。
const props = defineProps<{ orgId?: string }>()
const auth = useAuthStore()
// 平台管理员通过组织选择器查看不同组织审计，组织用户默认使用自身组织。
const {
  isPlatformAdmin,
  selectedOrgId,
  effectiveOrgId,
  orgOptions,
  organizationsLoading,
  organizationsError,
} = usePlatformOrgSelection(computed(() => auth.user), computed(() => props.orgId))
const orgEyebrow = computed(() => auth.user?.role === 'platform_admin' ? 'Platform · 审计' : '组织 · 审计')
const canView = computed(() => canViewOrgAudit(auth.user, effectiveOrgId.value))

// queryOrgId 为 undefined 时不发起查询，用前端权限分支减少无意义 403。
const queryOrgId = computed(() => canView.value ? effectiveOrgId.value : undefined)
const { data: logs, isLoading, error } = useOrgAuditLogsQuery(queryOrgId)

// 无关联组织时展示提示；有 API 错误时展示错误信息
const errorMessage = computed(() => {
  if (organizationsError.value) return String(organizationsError.value)
  if (!effectiveOrgId.value) return isPlatformAdmin.value ? '暂无可查看组织' : '当前账号未关联组织，无法查看审计日志。'
  if (!canView.value) return '当前账号无权查看组织级审计，请在自己的实例详情中查看实例审计。'
  if (error.value) return String(error.value)
  return undefined
})

type AuditLog = NonNullable<typeof logs.value>[number]

// auditTagType 将审计结果映射为标签色，未知结果保持默认色以兼容后端扩展。
function auditTagType(result: string): 'success' | 'warning' | 'error' | 'default' {
  switch (result) {
    case 'success': return 'success'
    case 'failed': case 'error': return 'error'
    case 'partial': return 'warning'
    default: return 'default'
  }
}

// columns 展示审计主体、资源、动作和结果；错误信息作为结果列的辅助诊断文本。
// 各列使用后端返回的 *_label 字段展示中文，auditTagType 仍依赖原始 result 判断颜色。
const columns: DataTableColumns<AuditLog> = [
  timeColumn('时间', r => r.created_at),
  {
    title: '操作者', key: 'actor_role',
    render: (row) => [
      h('strong', row.actor_role_label),
      row.actor_id ? h('small', { style: 'display:block;color:#8A94C6;font-size:12px' }, row.actor_id) : null,
    ],
  },
  {
    title: '资源', key: 'target_type',
    render: (row) => [
      h('strong', row.target_type_label),
      h('small', { style: 'display:block;color:#8A94C6;font-size:12px' }, row.target_id),
    ],
  },
  { title: '操作', key: 'action', render: (row) => row.action_label },
  {
    title: '结果', key: 'result',
    render: (row) => [
      h(NTag, { type: auditTagType(row.result), size: 'small', bordered: false }, { default: () => row.result_label }),
      row.error_message ? h('small', { style: 'display:block;color:#FF3B5C;font-size:12px' }, row.error_message) : null,
    ],
  },
]
</script>
