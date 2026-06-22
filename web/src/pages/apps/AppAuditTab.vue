<template>
  <DataTableList
    :title="t('apps.audit.title')"
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
import { useI18n } from 'vue-i18n'

import type { AuditLog } from '@/api'
import { useTargetAuditLogsQuery } from '@/api/hooks/useAuditLogs'
import type { AppDTO } from '@/api/hooks/useApps'
import DataTableList from '@/components/DataTableList.vue'
import { timeColumn } from '@/components/columns'
import { canViewOwnAppAudit } from '@/domain/permissions'
import { useAuthStore } from '@/stores/auth'

// AppAuditTab 展示单个应用的审计记录，依赖父级 AppDetailPage 注入的应用上下文做权限判断。
const props = defineProps<{ appId: string }>()
const { t } = useI18n()
const auth = useAuthStore()
const app = inject<Ref<AppDTO | null>>('app')
// canView 以当前账号和应用归属共同判定，避免成员查看非自己应用审计。
const canView = computed(() => canViewOwnAppAudit(auth.user, app?.value))
// target 为 undefined 时查询 hook 不发起请求，前端先拦截无权限场景减少 403。
const target = computed(() => canView.value ? { targetType: 'app', targetId: props.appId } : undefined)
const { data: logs, isLoading, error } = useTargetAuditLogsQuery(target)

// errorMessage 合并权限失败和 API 失败，交给公共列表组件显示。
const errorMessage = computed(() => {
  if (!canView.value) return t('apps.audit.noPermission')
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

// tSafe 按 i18n key 翻译，若 key 未命中（vue-i18n 返回 key 本身）则 fallback 到 fallbackStr。
// vue-i18n 找不到 key 时返回 key 本身，因此以返回值 === key 检测 miss。
function tSafe(key: string, fallbackStr: string, params?: Record<string, unknown>): string {
  const translated = params ? t(key, params) : t(key)
  return translated === key ? fallbackStr : translated
}

// labelActorRole 将 actor_role 代码翻译为当前语言展示名，未知值 fallback 到原始 code。
function labelActorRole(role: string | undefined | null): string {
  if (!role) return ''
  return tSafe(`audit.actorRole.${role}`, role)
}

// labelAction 将 (target_type, action) 组合翻译为当前语言展示名，未知组合 fallback 到 action 原始字符串。
// app_skill 的 action 原始码含点号（如 skill.install），翻译前将点替换为下划线以匹配 i18n key 结构。
function labelAction(targetType: string | undefined | null, action: string | undefined | null): string {
  if (!action) return ''
  // 将 action 中的点替换为下划线，以符合 vue-i18n key 路径规范（点视为嵌套分隔符）。
  const safeAction = action.replace(/\./g, '_')
  const key = `audit.action.${targetType ?? ''}.${safeAction}`
  return tSafe(key, action)
}

// labelResult 将 result 代码翻译为当前语言展示名，未知值 fallback 到原始 code。
function labelResult(result: string | undefined | null): string {
  if (!result) return ''
  return tSafe(`audit.result.${result}`, result)
}

// labelChannelType 将渠道类型代码翻译为当前语言展示名，未知值 fallback 到原始 code。
function labelChannelType(channelType: string | undefined | null): string {
  if (!channelType) return ''
  return tSafe(`audit.channelType.${channelType}`, channelType)
}

// renderDetail 根据 (target_type, action) + metadata 构造本地化详情字符串。
// 优先用 metadata 通过 vue-i18n 模板渲染结构化详情；找不到模板或 metadata 为空时 fallback 到
// action_detail（旧行冻结字符串）；两者均无则返回空字符串（调用处渲染「—」占位）。
// 注意：detail 模板中使用 vue-i18n 命名插值 {varName}，必须通过 t(key, params) 填充，
// 不能先取模板字符串再手工 replace，否则 vue-i18n 会在取模板时以空串替换占位符。
function renderDetail(row: AuditLog): string {
  const { target_type: tt, action, metadata, action_detail } = row

  // 仅 metadata 非空时才尝试构建结构化详情；旧行 metadata 为 null/undefined/空对象。
  if (tt && action && metadata && Object.keys(metadata).length > 0) {
    const meta = metadata as Record<string, unknown>

    if (tt === 'app_skill') {
      // app_skill：action 形如 skill.install，key 中将点转为下划线。
      // skill_ref 预拼接「name@version」，避免在模板内直接写 {name}@{version}
      // 触发 vue-i18n 的 linked message 语法（@ 为保留前缀）。
      const safeAction = action.replace(/\./g, '_')
      const key = `audit.detail.app_skill.${safeAction}`
      const skillName = String(meta.skill_name ?? '')
      const skillVersion = String(meta.skill_version ?? '')
      const skillRef = skillVersion ? `${skillName}@${skillVersion}` : skillName
      const result = tSafe(key, '', {
        skill_ref: skillRef,
        skill_name: skillName,
        skill_version: skillVersion,
        app_id: String(meta.app_id ?? ''),
      })
      if (result) return result
    }

    if (tt === 'app' && action === 'channel_auth_start') {
      // 渠道认证开始：channel_type 需先本地化再传入模板。
      const channelLabel = labelChannelType(String(meta.channel_type ?? ''))
      return tSafe('audit.detail.app.channel_auth_start', '', {
        channel_type: channelLabel,
        job_id: String(meta.job_id ?? ''),
      })
    }

    if (tt === 'app' && (action === 'create' || action === 'create_for_existing_member')) {
      // 创建应用（新成员或已有成员）：展示成员名和应用名。
      const result = tSafe(`audit.detail.app.${action}`, '', {
        member_name: String(meta.member_name ?? ''),
        app_name: String(meta.app_name ?? ''),
      })
      if (result) return result
    }

    if (tt === 'app' && action === 'delete') {
      // 删除应用：级联渠道绑定数量。
      return tSafe('audit.detail.app.delete', '', { cascade_count: String(meta.cascade_count ?? 0) })
    }

    if (tt === 'user' && action === 'delete_member') {
      // 移除成员：级联删除应用数量。
      return tSafe('audit.detail.user.delete_member', '', { cascade_count: String(meta.cascade_count ?? 0) })
    }

    if (tt === 'member' && action === 'create_with_app') {
      // 成员加入企业含应用创建。
      return tSafe('audit.detail.member.create_with_app', '', {
        member_name: String(meta.member_name ?? ''),
        app_name: String(meta.app_name ?? ''),
      })
    }

    if (tt === 'organization' && action === 'recharge') {
      // 充值：有备注时使用带备注的模板，无备注使用简洁模板。
      const remark = String(meta.remark ?? '')
      if (remark) {
        return tSafe('audit.detail.organization.recharge_with_remark', '', {
          amount: String(meta.amount ?? 0),
          remark,
        })
      }
      return tSafe('audit.detail.organization.recharge', '', { amount: String(meta.amount ?? 0) })
    }

    if (tt === 'newapi_call') {
      // API 调用：status_code 为 0 表示请求未发出。
      const statusCode = Number(meta.status_code ?? 0)
      if (statusCode === 0) {
        return tSafe('audit.detail.newapi_call.not_sent', '', { endpoint: String(meta.endpoint ?? '') })
      }
      return tSafe('audit.detail.newapi_call.default', '', {
        endpoint: String(meta.endpoint ?? ''),
        status_code: String(statusCode),
      })
    }
  }

  // metadata 无法构造详情，fallback 到旧行的冻结字符串。
  return action_detail ?? ''
}

// renderActor 渲染操作者单元格：
// - system 行直接展示「系统」（本地化），不附带角色副文也不展示 UUID hover；
// - 否则主文 actor_name fallback shortenId(actor_id) fallback actor_role_label（本地化），副文角色标签，UUID hover。
function renderActor(row: AuditLog) {
  if (row.actor_role === 'system' && !row.actor_id) {
    return h('strong', labelActorRole(row.actor_role))
  }
  const main: VNode[] = [h('strong', row.actor_name || shortenId(row.actor_id ?? '') || labelActorRole(row.actor_role))]
  if (row.actor_deleted) {
    main.push(h(NTag, { type: 'warning', size: 'tiny', bordered: false, style: 'margin-left:6px' }, { default: () => t('apps.audit.deleted') }))
  }
  // 副文：角色标签（本地化，随语言切换响应式更新）。
  const sub = h('small', { style: 'display:block;color:var(--color-text-secondary);font-size:12px' }, labelActorRole(row.actor_role))
  const cell = h('div', [...main, sub])
  if (!row.actor_id) return cell
  return h(NTooltip, { trigger: 'hover', placement: 'top' }, {
    trigger: () => cell,
    default: () => row.actor_id,
  })
}

// columns 展示审计时间、操作者、动作、详情和结果；错误信息跟随结果列作为诊断辅助。
// 使用 computed 包裹以确保语言切换时列标题及单元格内容响应式更新。
const columns = computed<DataTableColumns<AuditLog>>(() => [
  timeColumn(t('apps.audit.colTime'), r => r.created_at),
  { title: t('apps.audit.colActor'), key: 'actor_name', render: renderActor },
  {
    // 操作列：按 (target_type, action) 翻译，随语言切换响应式更新。
    title: t('apps.audit.colAction'), key: 'action',
    render: (row) => labelAction(row.target_type, row.action),
  },
  {
    title: t('apps.audit.colDetail'), key: 'action_detail',
    minWidth: 240,
    render: (row) => {
      // renderDetail 优先用 metadata 模板，fallback 到 action_detail，均无则展示「—」占位。
      const detail = renderDetail(row)
      return detail
        ? h('span', { style: 'white-space:pre-wrap' }, detail)
        : h('span', { style: 'color:var(--color-text-secondary)' }, '—')
    },
  },
  {
    title: t('apps.audit.colResult'), key: 'result',
    render: (row) => [
      // 结果标签：按 result 代码翻译，随语言切换响应式更新。
      h(NTag, { type: auditTagType(row.result ?? ''), size: 'small', bordered: false }, { default: () => labelResult(row.result) }),
      row.error_message ? h('small', { style: 'display:block;color:var(--color-danger);font-size:12px' }, row.error_message) : null,
    ],
  },
])
</script>
