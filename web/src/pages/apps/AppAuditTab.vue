<template>
  <DataTableList
    :title="'实例审计'"
    :eyebrow="'Instance · Audit'"
    :columns="columns"
    :data="logs ?? []"
    :loading="isLoading"
    :error-message="errorMessage"
    :row-key="(row: AuditLog) => row.id"
  />
</template>

<script setup lang="ts">
import { computed, h, inject, type Ref, type VNode } from 'vue'
import { NTag, NTooltip, type DataTableColumns } from 'naive-ui'

import type { AuditLog } from '@/api'
import { useTargetAuditLogsQuery } from '@/api/hooks/useAuditLogs'
import type { AppDTO } from '@/api/hooks/useApps'
import DataTableList from '@/components/DataTableList.vue'
import { timeColumn } from '@/components/columns'
import { canViewOwnAppAudit } from '@/domain/permissions'
import { useAuthStore } from '@/stores/auth'

// AppAuditTab 展示单个应用的审计记录，依赖父级 AppDetailPage 注入的应用上下文做权限判断。
const props = defineProps<{ appId: string }>()
const auth = useAuthStore()
const app = inject<Ref<AppDTO | null>>('app')
// canView 以当前账号和应用归属共同判定，避免成员查看非自己应用审计。
const canView = computed(() => canViewOwnAppAudit(auth.user, app?.value))
// target 为 undefined 时查询 hook 不发起请求，前端先拦截无权限场景减少 403。
const target = computed(() => canView.value ? { targetType: 'app', targetId: props.appId } : undefined)
const { data: logs, isLoading, error } = useTargetAuditLogsQuery(target)

// errorMessage 合并权限失败和 API 失败，交给公共列表组件显示。
const errorMessage = computed(() => {
  if (!canView.value) return '当前账号无权查看该实例审计。'
  if (error.value) return String(error.value)
  return undefined
})

// auditTagType 兼容 success/succeeded 两种后端结果文案，并为异常结果标红。
function auditTagType(result: string): 'success' | 'warning' | 'error' | 'default' {
  switch (result) {
    case 'success': case 'succeeded': return 'success'
    case 'failed': case 'error': return 'error'
    case 'partial': return 'warning'
    default: return 'default'
  }
}

// shortenId 截取 UUID 末 8 位作 fallback，避免列太长。
function shortenId(value: string | undefined | null): string {
  if (!value) return ''
  return value.length > 8 ? value.slice(-8) : value
}

// renderActor 渲染操作者单元格：
// - system 行直接展示「系统」，不附带角色副文也不展示 UUID hover；
// - 否则主文 actor_name fallback shortenId(actor_id) fallback actor_role_label，副文角色标签，UUID hover。
function renderActor(row: AuditLog) {
  if (row.actor_role === 'system' && !row.actor_id) {
    return h('strong', row.actor_role_label)
  }
  const main: VNode[] = [h('strong', row.actor_name || shortenId(row.actor_id ?? '') || row.actor_role_label)]
  if (row.actor_deleted) {
    main.push(h(NTag, { type: 'warning', size: 'tiny', bordered: false, style: 'margin-left:6px' }, { default: () => '已删除' }))
  }
  const sub = h('small', { style: 'display:block;color:var(--color-text-secondary);font-size:12px' }, row.actor_role_label)
  const cell = h('div', [...main, sub])
  if (!row.actor_id) return cell
  return h(NTooltip, { trigger: 'hover', placement: 'top' }, {
    trigger: () => cell,
    default: () => row.actor_id,
  })
}

// columns 展示审计时间、操作者、动作、详情和结果；错误信息跟随结果列作为诊断辅助。
const columns: DataTableColumns<AuditLog> = [
  timeColumn('时间', r => r.created_at),
  { title: '操作者', key: 'actor_name', render: renderActor },
  { title: '操作', key: 'action', render: (row) => row.action_label },
  {
    title: '详情', key: 'action_detail',
    minWidth: 240,
    render: (row) => row.action_detail
      ? h('span', { style: 'white-space:pre-wrap' }, row.action_detail)
      : h('span', { style: 'color:var(--color-text-secondary)' }, '—'),
  },
  {
    title: '结果', key: 'result',
    render: (row) => [
      h(NTag, { type: auditTagType(row.result), size: 'small', bordered: false }, { default: () => row.result_label }),
      row.error_message ? h('small', { style: 'display:block;color:var(--color-danger);font-size:12px' }, row.error_message) : null,
    ],
  },
]
</script>
