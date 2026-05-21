<template>
  <div style="display: grid; gap: 18px">
    <!-- 组织列表 -->
    <DataTableList
      title="组织列表"
      eyebrow="Platform"
      :columns="columns"
      :data="organizations ?? []"
      :loading="isLoading"
      :error-message="error?.message"
      :row-key="(row: Organization) => row.id"
    >
      <template #toolbar>
        <n-button type="primary" @click="openForm">
          <template #icon><Plus :size="16" /></template>
          新增组织
        </n-button>
      </template>
    </DataTableList>
    <p v-if="copyFeedback" class="state-text" :class="{ danger: copyFeedbackError }">{{ copyFeedback }}</p>

    <!-- 创建表单 -->
    <n-card v-if="formVisible" :bordered="true">
      <template #header>
        <div style="display: flex; align-items: center; justify-content: space-between">
          <div>
            <p class="eyebrow">New</p>
            <h2 style="margin: 0">创建组织</h2>
          </div>
          <n-button quaternary circle @click="formVisible = false">
            <template #icon><X :size="18" /></template>
          </n-button>
        </div>
      </template>
      <n-form :model="form" label-placement="top" @submit.prevent="submitOrganization">
        <n-grid :cols="2" :x-gap="14">
          <n-grid-item>
            <n-form-item label="名称 *">
              <n-input v-model:value="form.name" placeholder="组织名称" />
            </n-form-item>
          </n-grid-item>
          <n-grid-item>
            <n-form-item label="组织标识 *">
              <n-input v-model:value="form.code" placeholder="test-org" />
            </n-form-item>
          </n-grid-item>
          <n-grid-item>
            <n-form-item label="管理员用户名 *">
              <n-input v-model:value="form.admin_username" placeholder="登录用户名" />
            </n-form-item>
          </n-grid-item>
          <n-grid-item>
            <n-form-item label="管理员姓名 *">
              <n-input v-model:value="form.admin_display_name" placeholder="管理员显示名" />
            </n-form-item>
          </n-grid-item>
          <n-grid-item>
            <n-form-item label="管理员密码 *">
              <n-input v-model:value="form.admin_password" type="password" show-password-on="click" placeholder="初始登录密码" />
            </n-form-item>
          </n-grid-item>
          <n-grid-item>
            <n-form-item label="联系人">
              <n-input v-model:value="form.contact_name" placeholder="联系人姓名" />
            </n-form-item>
          </n-grid-item>
          <n-grid-item>
            <n-form-item label="联系电话">
              <n-input v-model:value="form.contact_phone" placeholder="手机号" />
            </n-form-item>
          </n-grid-item>
          <n-grid-item>
            <n-form-item label="余额预警阈值 (%)">
              <n-input-number v-model:value="form.credit_warning_threshold" :min="0" :max="100" style="width: 100%" />
            </n-form-item>
          </n-grid-item>
          <n-grid-item :span="2">
            <n-form-item label="备注">
              <n-input v-model:value="form.remark" type="textarea" :rows="2" />
            </n-form-item>
          </n-grid-item>
          <n-grid-item :span="2">
            <n-form-item label="可用助手版本">
              <n-select
                v-model:value="form.assistant_version_ids"
                multiple
                :loading="versionsQuery.isLoading.value"
                :options="versionOptions"
                placeholder="选择该组织可用的助手版本（可多选，可留空）"
              />
            </n-form-item>
          </n-grid-item>
          <n-grid-item :span="2">
            <n-space justify="end">
              <n-button @click="formVisible = false">取消</n-button>
              <n-button type="primary" attr-type="submit" :loading="creating" :disabled="creating">保存</n-button>
            </n-space>
            <p v-if="submitError" class="state-text danger">{{ submitError }}</p>
          </n-grid-item>
        </n-grid>
      </n-form>
    </n-card>

    <!-- 组织充值弹框 -->
    <n-modal v-model:show="rechargeVisible" preset="card" style="max-width: 560px" title="组织充值">
      <div v-if="selectedOrg" class="recharge-dialog">
        <div>
          <p class="eyebrow">Billing</p>
          <h3 style="margin: 0">{{ selectedOrg.name }}</h3>
        </div>
        <p class="state-text">
          当前余额：
          <strong v-if="balanceQuery.isLoading.value">加载中…</strong>
          <strong v-else-if="balance">
            剩余 {{ formatQuotaValue(balance.remain_quota, billingStatus) }} ｜ 已用 {{ formatQuotaValue(balance.used_quota, billingStatus) }}
          </strong>
          <strong v-else class="danger">查询失败</strong>
        </p>
        <n-form label-placement="top" @submit.prevent="submitRecharge">
          <n-form-item label="充值金额（正整数）">
            <n-input-number v-model:value="rechargeAmount" :min="1" :precision="0" style="width: 100%" placeholder="输入金额" />
          </n-form-item>
          <n-form-item label="备注">
            <n-input v-model:value="rechargeRemark" placeholder="业务说明，可选" />
          </n-form-item>
          <n-space justify="end">
            <n-button @click="closeRecharge">取消</n-button>
            <n-button
              type="primary"
              attr-type="submit"
              :disabled="!selectedOrgId"
              :loading="rechargeMutation.isPending.value"
            >
              确认充值
            </n-button>
          </n-space>
          <p v-if="rechargeFeedback" class="state-text" :class="{ danger: rechargeFeedbackError }">
            {{ rechargeFeedback }}
          </p>
        </n-form>
      </div>
    </n-modal>

    <!-- 充值记录弹窗 -->
    <n-modal
      v-model:show="rechargeHistoryVisible"
      preset="card"
      style="max-width: 720px"
      :title="rechargeHistoryOrg ? `充值记录 · ${rechargeHistoryOrg.name}` : '充值记录'"
    >
      <div v-if="rechargeHistoryOrg" style="display: grid; gap: 16px">
        <!-- 概况卡片 -->
        <n-grid :cols="2" :x-gap="14">
          <n-grid-item>
            <n-statistic label="累计充值金额">
              <template v-if="rechargeHistoryBalanceQuery.isLoading.value">—</template>
              <template v-else-if="rechargeHistoryBalance">
                {{ formatDisplayAmount(rechargeHistoryBalance.total_recharged, billingStatus) }}
              </template>
              <template v-else>查询失败</template>
            </n-statistic>
          </n-grid-item>
          <n-grid-item>
            <n-statistic label="当前剩余金额">
              <template v-if="rechargeHistoryBalanceQuery.isLoading.value">—</template>
              <template v-else-if="rechargeHistoryBalance">
                {{ formatQuotaValue(rechargeHistoryBalance.remain_quota, billingStatus) }}
              </template>
              <template v-else>查询失败</template>
            </n-statistic>
          </n-grid-item>
        </n-grid>
        <!-- 充值记录表格 -->
        <div v-if="rechargeHistoryLoading" class="state-text">加载中…</div>
        <n-data-table
          v-else
          size="small"
          :columns="rechargeHistoryColumns"
          :data="rechargeHistoryRecords ?? []"
          :pagination="{ pageSize: 10 }"
        />
      </div>
    </n-modal>
  </div>
</template>

<script setup lang="ts">
import { computed, h, ref } from 'vue'
import { useQueries } from '@tanstack/vue-query'
import { Plus, X } from 'lucide-vue-next'
import {
  NButton, NCard, NDataTable, NForm, NFormItem, NGrid, NGridItem,
  NInput, NInputNumber, NModal, NSelect, NSpace, NStatistic,
} from 'naive-ui'

import { formatOrgStatus } from '@/domain/status'
import {
  useCreateOrganization, useOrganizationsQuery, useUpdateOrganizationStatus,
} from '@/api/hooks/useOrganizations'
import { useAssistantVersionsQuery } from '@/api/hooks/useAssistantVersions'
import { apiRequest } from '@/api/client'
import { useBillingStatusQuery, useOrgBalanceQuery, useRechargeMutation, useRechargesQuery } from '@/api/hooks/useRecharge'
import type { BalanceDTO } from '@/api/hooks/useRecharge'
import type { Organization } from '@/api'
import DataTableList from '@/components/DataTableList.vue'
import { statusColumn, actionColumn } from '@/components/columns'
import { useFormModal } from '@/composables/useFormModal'
import { formatDisplayAmount, formatQuotaValue } from '@/pages/usage/usageFormatting'

// OrganizationsPage 是平台组织管理页，负责创建组织、启停组织和给组织充值。
const { data: organizations, isLoading, error } = useOrganizationsQuery()
const createMutation = useCreateOrganization()
const statusMutation = useUpdateOrganizationStatus()
// selectedOrg 保存当前充值弹框的目标组织，关闭弹框不会修改列表数据。
const selectedOrg = ref<Organization | null>(null)
const selectedOrgId = computed(() => selectedOrg.value?.id)
const balanceQuery = useOrgBalanceQuery(selectedOrgId)
const balance = computed(() => balanceQuery.data.value ?? null)
const { data: billingStatus } = useBillingStatusQuery()

// orgBalanceQueries 对列表中每个组织并发查询余额，orgId 变化时自动重建查询集合。
const orgBalanceQueries = useQueries({
  queries: computed(() =>
    (organizations.value ?? []).map(org => ({
      queryKey: ['org-balance', org.id] as const,
      queryFn: async () => {
        const res = await apiRequest<{ balance: BalanceDTO }>(`/api/v1/organizations/${org.id}/balance`)
        return res.balance
      },
    }))
  ),
})

// balanceByOrgId 把 useQueries 的数组结果转成 orgId → BalanceDTO 映射，供列渲染器使用。
const balanceByOrgId = computed(() => {
  const map: Record<string, BalanceDTO | undefined> = {}
  ;(organizations.value ?? []).forEach((org, i) => {
    map[org.id] = orgBalanceQueries.value[i]?.data ?? undefined
  })
  return map
})

// rechargeHistoryVisible 控制充值记录弹窗（与已有充值弹框 rechargeVisible 独立）。
const rechargeHistoryVisible = ref(false)
const rechargeHistoryOrg = ref<Organization | null>(null)
const rechargeHistoryOrgId = computed(() => rechargeHistoryOrg.value?.id)
// 与 orgBalanceQueries 共享同一 queryKey（['org-balance', orgId]），TanStack Query 会复用缓存。
// 单独订阅的原因：① 弹窗打开时触发主动刷新（staleTime=0 策略下确保数据最新）；② 获取独立的 isLoading 状态供弹窗内加载占位符使用。
const rechargeHistoryBalanceQuery = useOrgBalanceQuery(rechargeHistoryOrgId)
const rechargeHistoryBalance = computed(() => rechargeHistoryBalanceQuery.data.value ?? null)
const { data: rechargeHistoryRecords, isLoading: rechargeHistoryLoading } = useRechargesQuery(rechargeHistoryOrgId)

function openRechargeHistory(org: Organization) {
  rechargeHistoryOrg.value = org
  rechargeHistoryVisible.value = true
}

const rechargeMutation = useRechargeMutation(selectedOrgId)
const rechargeVisible = ref(false)
const rechargeAmount = ref<number | null>(null)
const rechargeRemark = ref('')
const rechargeFeedback = ref('')
const rechargeFeedbackError = ref(false)
const copyFeedback = ref('')
const copyFeedbackError = ref(false)
const adminPasswordCopyHint = '<创建时设置，系统不保存明文；如忘记请重置密码>'
// 创建组织表单状态聚合到 useFormModal；toPayload 处理可选字段的 || undefined 过滤
const { form, formVisible, creating, submitError, openForm, submit: submitForm } = useFormModal({
  initial: {
    name: '',
    code: '',
    contact_name: '',
    contact_phone: '',
    remark: '',
    credit_warning_threshold: undefined as number | undefined,
    admin_username: '',
    admin_display_name: '',
    admin_password: '',
    assistant_version_ids: [] as string[],
  },
  mutation: createMutation,
  toPayload: (f) => ({
    name: f.name,
    code: f.code,
    contact_name: f.contact_name || undefined,
    contact_phone: f.contact_phone || undefined,
    remark: f.remark || undefined,
    credit_warning_threshold: typeof f.credit_warning_threshold === 'number'
      ? f.credit_warning_threshold : undefined,
    admin_username: f.admin_username,
    admin_display_name: f.admin_display_name,
    admin_password: f.admin_password,
    assistant_version_ids: f.assistant_version_ids,
  }),
})
// versionsQuery 仅在表单打开时发起请求，避免页面初始化时的无谓请求。
const versionsQuery = useAssistantVersionsQuery(() => formVisible.value)
const versionOptions = computed(() => (versionsQuery.data.value ?? []).map(v => ({
  label: v.name,
  value: v.id,
})))

// submitOrganization 兜底处理键盘提交，避免绕过保存按钮禁用状态。
// 助手版本为可选项，无需前置校验；直接调用 submitForm。
async function submitOrganization() {
  await submitForm()
}

// columns 展示组织基础信息、状态、余额和操作；改为 computed 以引用响应式的 balanceByOrgId。
const columns = computed(() => [
  // 名称列：含 remark 副标题
  {
    title: '名称',
    key: 'name',
    render: (row: Organization) => [
      h('strong', row.name),
      row.remark
        ? h('small', { class: 'data-table-subtitle' }, row.remark)
        : null,
    ],
  },
  { title: '组织标识', key: 'code', render: (row: Organization) => row.code || '—' },
  statusColumn<Organization>('状态', r => formatOrgStatus(r.status)),
  // 联系人/电话/预警阈值列
  { title: '联系人', key: 'contact_name', render: (row: Organization) => row.contact_name || '—' },
  { title: '电话', key: 'contact_phone', render: (row: Organization) => row.contact_phone || '—' },
  {
    title: '预警阈值',
    key: 'credit_warning_threshold',
    render: (row: Organization) => typeof row.credit_warning_threshold === 'number'
      ? `${row.credit_warning_threshold}%` : '—',
  },
  // 当前余额列：从并发查询结果映射到对应行，未加载时显示省略号。
  {
    title: '当前余额',
    key: 'remain_quota',
    render: (row: Organization) => {
      const b = balanceByOrgId.value[row.id]
      if (!b) return '…'
      return formatQuotaValue(b.remain_quota, billingStatus.value)
    },
  },
  // 启用/禁用互斥：用两条 RowAction + hidden 分别渲染
  actionColumn<Organization>([
    { label: '复制信息', onClick: r => { void copyOrganizationInfo(r) } },
    { label: '充值记录', onClick: openRechargeHistory },
    { label: '充值', type: 'primary', onClick: openRecharge },
    { label: '禁用', onClick: r => onToggle(r, 'disable'), hidden: r => r.status !== 'active' },
    { label: '启用', type: 'primary', onClick: r => onToggle(r, 'enable'), hidden: r => r.status === 'active' },
  ]),
])

function optionalAdminUsername(org: Organization) {
  return org.admin_username ?? ''
}

// formatOrganizationCopyInfo 固定对外复制格式，便于平台管理员直接发送给组织管理员。
function formatOrganizationCopyInfo(org: Organization) {
  return [
    `标识： ${org.code || ''}`,
    `名称： ${org.name}`,
    `管理员用户名： ${optionalAdminUsername(org)}`,
    `管理员密码： ${adminPasswordCopyHint}`,
  ].join('\n')
}

// copyOrganizationInfo 使用浏览器剪贴板写入组织登录信息，并在页面内暴露结果。
async function copyOrganizationInfo(org: Organization) {
  copyFeedback.value = ''
  copyFeedbackError.value = false
  try {
    await navigator.clipboard.writeText(formatOrganizationCopyInfo(org))
    copyFeedback.value = `已复制 ${org.name} 的组织信息`
  } catch {
    copyFeedbackError.value = true
    copyFeedback.value = '复制失败，请检查浏览器剪贴板权限'
  }
}

// onToggle 调用组织状态切换接口，状态刷新由 mutation hook 的缓存失效策略处理。
function onToggle(org: Organization, action: 'enable' | 'disable') {
  statusMutation.mutate({ orgId: org.id, action })
}

// openRecharge 初始化充值弹框状态，并加载当前组织余额。
function openRecharge(org: Organization) {
  selectedOrg.value = org
  rechargeAmount.value = null
  rechargeRemark.value = ''
  rechargeFeedback.value = ''
  rechargeFeedbackError.value = false
  rechargeVisible.value = true
}

// closeRecharge 只关闭弹框，保留反馈文本由下次 openRecharge 统一重置。
function closeRecharge() {
  rechargeVisible.value = false
}

// submitRecharge 调用 new-api 充值链路；成功后清空输入，失败时在弹框内展示错误。
//
// n-input-number 设了 :precision 后输入期间不会更新 v-model，只在 blur 时提交；
// 点击「确认充值」按钮会先让金额输入框 blur，因此进入本函数时 rechargeAmount
// 已是最新值。校验放在这里而不是按钮 disabled，避免「输入完按钮还灰着点不动」。
async function submitRecharge() {
  if (!selectedOrgId.value) return
  if (!((rechargeAmount.value ?? 0) > 0)) {
    rechargeFeedbackError.value = true
    rechargeFeedback.value = '请输入正整数充值金额'
    return
  }
  rechargeFeedback.value = ''
  rechargeFeedbackError.value = false
  try {
    const result = await rechargeMutation.mutateAsync({
      credit_amount: rechargeAmount.value ?? 0,
      remark: rechargeRemark.value || undefined,
    })
    rechargeFeedback.value = `已充值 ${formatDisplayAmount(result.credit_amount, billingStatus.value)}`
    rechargeAmount.value = null
    rechargeRemark.value = ''
  } catch (err: unknown) {
    rechargeFeedbackError.value = true
    rechargeFeedback.value = err instanceof Error ? err.message : '充值失败'
  }
}

// rechargeHistoryColumns 是充值记录弹窗的表格列定义；含操作人 ID（平台管理员可见）。
const rechargeHistoryColumns = [
  { title: '时间', key: 'created_at', render: (r: { created_at: string }) => r.created_at.replace('T', ' ').slice(0, 19) },
  {
    title: '金额',
    key: 'credit_amount',
    render: (r: { credit_amount: number }) => formatDisplayAmount(r.credit_amount, billingStatus.value),
  },
  { title: '备注', key: 'remark', render: (r: { remark?: string }) => r.remark || '—' },
  {
    title: '状态',
    key: 'status',
    render: (r: { status: string }) => r.status === 'succeeded' ? '成功' : '失败',
  },
  { title: '操作人', key: 'operator_id', render: (r: { operator_id?: string }) => r.operator_id ? r.operator_id.slice(0, 8) + '…' : '—' },
]
</script>

<style scoped>
.recharge-dialog { display: grid; gap: 14px; }
</style>
