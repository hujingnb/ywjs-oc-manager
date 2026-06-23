<template>
  <DataTableList
    :title="t('audit.page.title')"
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
        :placeholder="t('audit.filters.selectOrg')"
      />
    </template>
  </DataTableList>
</template>

<script setup lang="ts">
import { computed, h, type VNode } from 'vue'
import { NSelect, NTag, NTooltip, type DataTableColumns } from 'naive-ui'
import { useI18n } from 'vue-i18n'

import { useOrgAuditLogsQuery } from '@/api/hooks/useAuditLogs'
import { usePlatformOrgSelection } from '@/composables/usePlatformOrgSelection'
import { canViewOrgAudit } from '@/domain/permissions'
import { useAuthStore } from '@/stores/auth'
import DataTableList from '@/components/DataTableList.vue'
import { timeColumn } from '@/components/columns'

// AuditLogsPage 展示组织级审计日志，平台和组织管理员可看，普通成员需去应用详情查看自己的应用审计。
const props = defineProps<{ orgId?: string }>()
const auth = useAuthStore()
const { t } = useI18n()

// 平台管理员通过组织选择器查看不同组织审计，组织用户默认使用自身组织。
const {
  isPlatformAdmin,
  selectedOrgId,
  effectiveOrgId,
  orgOptions,
  organizationsLoading,
  organizationsError,
} = usePlatformOrgSelection(computed(() => auth.user), computed(() => props.orgId))

// orgEyebrow 随角色与语言切换响应式更新副标题。
const orgEyebrow = computed(() =>
  auth.user?.role === 'platform_admin'
    ? t('audit.page.eyebrowPlatform')
    : t('audit.page.eyebrowOrg'),
)
const canView = computed(() => canViewOrgAudit(auth.user, effectiveOrgId.value))

// queryOrgId 为 undefined 时不发起查询，前端先拦截无权限场景减少 403。
const queryOrgId = computed(() => canView.value ? effectiveOrgId.value : undefined)
const { data: logs, isLoading, error } = useOrgAuditLogsQuery(queryOrgId)

// 无关联组织时展示提示；有 API 错误时展示错误信息
const errorMessage = computed(() => {
  if (organizationsError.value) return String(organizationsError.value)
  if (!effectiveOrgId.value) return isPlatformAdmin.value ? t('audit.state.noOrg') : t('audit.state.noOrgLinked')
  if (!canView.value) return t('audit.state.noPermission')
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

// labelResult 将 result 代码翻译为当前语言展示名，未知值 fallback 到原始 code。
function labelResult(result: string | undefined | null): string {
  if (!result) return ''
  return tSafe(`audit.result.${result}`, result)
}

// labelTargetType 将 target_type 代码翻译为当前语言展示名，未知值 fallback 到原始 code。
function labelTargetType(targetType: string | undefined | null): string {
  if (!targetType) return ''
  return tSafe(`audit.targetType.${targetType}`, targetType)
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

// renderPrincipal 渲染操作者 / 资源单元格的统一结构：
// - system actor 行直接展示角色标签（本地化），无副文与 hover；
// - 否则主文 name fallback shortenId(uuid) fallback role_label，副文为 sub，UUID 进 hover。
// deleted 为 true 时主文后追加「已删除」徽章（文案走 i18n）。
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
    main.push(h(NTag, { type: 'warning', size: 'tiny', bordered: false, style: 'margin-left:6px' }, { default: () => t('audit.table.deleted') }))
  }
  const sub = opts.sub ? h('small', { style: 'display:block;color:var(--color-text-secondary);font-size:12px' }, opts.sub) : null
  const children: VNode[] = sub ? [...main, sub] : main
  const cell = h('div', children)
  if (!opts.uuid) return cell
  return h(NTooltip, { trigger: 'hover', placement: 'top' }, {
    trigger: () => cell,
    default: () => opts.uuid,
  })
}

// columns 展示审计主体、资源、动作、详情和结果；错误信息作为结果列的辅助诊断文本。
// 使用 computed 确保语言切换时列头文案及单元格内容响应式更新。
const columns = computed<DataTableColumns<AuditLog>>(() => [
  timeColumn(t('audit.table.time'), r => r.created_at),
  {
    title: t('audit.table.actor'), key: 'actor_name',
    render: (row) => renderPrincipal({
      primary: row.actor_name ?? '',
      // 没有姓名时 fallback：截短 UUID 或角色标签（本地化）。
      fallback: shortenId(row.actor_id ?? '') || labelActorRole(row.actor_role),
      // 副文：角色标签（本地化，随语言切换）。
      sub: labelActorRole(row.actor_role),
      uuid: row.actor_id,
      deleted: row.actor_deleted ?? false,
      isSystem: row.actor_role === 'system' && !row.actor_id,
    }),
  },
  {
    title: t('audit.table.target'), key: 'target_name',
    render: (row) => renderPrincipal({
      primary: row.target_name ?? '',
      // 没 name 的目标（newapi_call 等）直接展示 target_id 字符串本身。
      fallback: row.target_id ?? '',
      // 副文：资源类型标签（本地化，随语言切换）。
      sub: labelTargetType(row.target_type),
      // 只有 target_id 像 UUID 才走 hover；endpoint 字符串本身在主文已经可读。
      uuid: row.target_name ? row.target_id : null,
      deleted: row.target_deleted ?? false,
    }),
  },
  {
    // 操作列：按 (target_type, action) 翻译，随语言切换响应式更新。
    title: t('audit.table.action'), key: 'action',
    render: (row) => labelAction(row.target_type, row.action),
  },
  {
    title: t('audit.table.detail'), key: 'action_detail',
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
    title: t('audit.table.result'), key: 'result',
    render: (row) => [
      // 结果标签：按 result 代码翻译，随语言切换响应式更新。
      h(NTag, { type: auditTagType(row.result ?? ''), size: 'small', bordered: false }, { default: () => labelResult(row.result) }),
      row.error_message ? h('small', { style: 'display:block;color:var(--color-danger);font-size:12px' }, row.error_message) : null,
    ],
  },
])
</script>
