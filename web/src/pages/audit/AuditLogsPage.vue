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
import { computed, h, type VNode } from 'vue'
import { NSelect, NTag, NTooltip, type DataTableColumns } from 'naive-ui'

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

// queryOrgId 为 undefined 时不发起查询，前端先拦截无权限场景减少 403。
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
    case 'success': case 'succeeded': return 'success'
    case 'failed': case 'error': return 'error'
    case 'partial': return 'warning'
    default: return 'default'
  }
}

// shortenId 截取 UUID 末 8 位用作 fallback 展示，避免列太长。
function shortenId(value: string | undefined | null): string {
  if (!value) return ''
  return value.length > 8 ? value.slice(-8) : value
}

// renderPrincipal 渲染操作者 / 资源单元格的统一结构：
// - system actor 行直接展 actor_role_label（系统），无副文与 hover；
// - 否则主文 name fallback shortenId(uuid) fallback role_label，副文为 sub，UUID 进 hover。
// deleted 为 true 时主文后追加「已删除」徽章。
function renderPrincipal(opts: {
  primary: string
  fallback: string
  sub: string
  uuid: string | null | undefined
  deleted: boolean
  isSystem?: boolean
}) {
  if (opts.isSystem) {
    return h('strong', opts.primary || opts.fallback)
  }
  const main: VNode[] = [h('strong', opts.primary || opts.fallback)]
  if (opts.deleted) {
    main.push(h(NTag, { type: 'warning', size: 'tiny', bordered: false, style: 'margin-left:6px' }, { default: () => '已删除' }))
  }
  const sub = opts.sub ? h('small', { style: 'display:block;color:#8A94C6;font-size:12px' }, opts.sub) : null
  const children: VNode[] = sub ? [...main, sub] : main
  const cell = h('div', children)
  if (!opts.uuid) return cell
  return h(NTooltip, { trigger: 'hover', placement: 'top' }, {
    trigger: () => cell,
    default: () => opts.uuid,
  })
}

// columns 展示审计主体、资源、动作、详情和结果；错误信息作为结果列的辅助诊断文本。
const columns: DataTableColumns<AuditLog> = [
  timeColumn('时间', r => r.created_at),
  {
    title: '操作者', key: 'actor_name',
    render: (row) => renderPrincipal({
      primary: row.actor_name ?? '',
      fallback: shortenId(row.actor_id ?? '') || row.actor_role_label,
      sub: row.actor_role_label,
      uuid: row.actor_id,
      deleted: row.actor_deleted ?? false,
      isSystem: row.actor_role === 'system' && !row.actor_id,
    }),
  },
  {
    title: '资源', key: 'target_name',
    render: (row) => renderPrincipal({
      primary: row.target_name ?? '',
      // 没 name 的目标（newapi_call 等）直接展示 target_id 字符串本身。
      fallback: row.target_id,
      sub: row.target_type_label,
      // 只有 target_id 像 UUID 才走 hover；endpoint 字符串本身在主文已经可读。
      uuid: row.target_name ? row.target_id : null,
      deleted: row.target_deleted ?? false,
    }),
  },
  { title: '操作', key: 'action', render: (row) => row.action_label },
  {
    title: '详情', key: 'action_detail',
    minWidth: 240,
    render: (row) => row.action_detail
      ? h('span', { style: 'white-space:pre-wrap' }, row.action_detail)
      : h('span', { style: 'color:#8A94C6' }, '—'),
  },
  {
    title: '结果', key: 'result',
    render: (row) => [
      h(NTag, { type: auditTagType(row.result), size: 'small', bordered: false }, { default: () => row.result_label }),
      row.error_message ? h('small', { style: 'display:block;color:#FF3B5C;font-size:12px' }, row.error_message) : null,
    ],
  },
]
</script>
