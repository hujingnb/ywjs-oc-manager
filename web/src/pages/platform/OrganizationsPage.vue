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
      <n-form :model="form" label-placement="top" @submit.prevent="submit">
        <n-grid :cols="2" :x-gap="14">
          <n-grid-item>
            <n-form-item label="名称 *">
              <n-input v-model:value="form.name" placeholder="组织名称" />
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
            <n-space justify="end">
              <n-button @click="formVisible = false">取消</n-button>
              <n-button type="primary" attr-type="submit" :loading="creating">保存</n-button>
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
            剩余 {{ balance.remain_quota.toLocaleString() }} ｜ 已用 {{ balance.used_quota.toLocaleString() }}
          </strong>
          <strong v-else class="danger">查询失败</strong>
        </p>
        <n-form label-placement="top" @submit.prevent="submitRecharge">
          <n-form-item label="充值点数（正整数）">
            <n-input-number v-model:value="rechargeAmount" :min="1" style="width: 100%" placeholder="输入点数" />
          </n-form-item>
          <n-form-item label="备注">
            <n-input v-model:value="rechargeRemark" placeholder="业务说明，可选" />
          </n-form-item>
          <n-space justify="end">
            <n-button @click="closeRecharge">取消</n-button>
            <n-button
              type="primary"
              attr-type="submit"
              :disabled="!canSubmitRecharge"
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
  </div>
</template>

<script setup lang="ts">
import { computed, h, ref } from 'vue'
import { Plus, X } from 'lucide-vue-next'
import {
  NButton, NCard, NForm, NFormItem, NGrid, NGridItem,
  NInput, NInputNumber, NModal, NSpace,
} from 'naive-ui'

import { formatOrgStatus } from '@/domain/status'
import {
  useCreateOrganization, useOrganizationsQuery, useUpdateOrganizationStatus,
} from '@/api/hooks/useOrganizations'
import { useOrgBalanceQuery, useRechargeMutation } from '@/api/hooks/useRecharge'
import type { Organization } from '@/api'
import DataTableList from '@/components/DataTableList.vue'
import { statusColumn, actionColumn } from '@/components/columns'
import { useFormModal } from '@/composables/useFormModal'

// OrganizationsPage 是平台组织管理页，负责创建组织、启停组织和给组织充值。
const { data: organizations, isLoading, error } = useOrganizationsQuery()
const createMutation = useCreateOrganization()
const statusMutation = useUpdateOrganizationStatus()
// selectedOrg 保存当前充值弹框的目标组织，关闭弹框不会修改列表数据。
const selectedOrg = ref<Organization | null>(null)
const selectedOrgId = computed(() => selectedOrg.value?.id)
const balanceQuery = useOrgBalanceQuery(selectedOrgId)
const balance = computed(() => balanceQuery.data.value ?? null)
const rechargeMutation = useRechargeMutation(selectedOrgId)
const rechargeVisible = ref(false)
const rechargeAmount = ref<number | null>(null)
const rechargeRemark = ref('')
const rechargeFeedback = ref('')
const rechargeFeedbackError = ref(false)
// canSubmitRecharge 表示当前弹框是否具备调用充值接口的最小条件。
const canSubmitRecharge = computed(() => Boolean(selectedOrgId.value && (rechargeAmount.value ?? 0) > 0))

// 创建组织表单状态聚合到 useFormModal；toPayload 处理可选字段的 || undefined 过滤
const { form, formVisible, creating, submitError, openForm, submit } = useFormModal({
  initial: {
    name: '',
    contact_name: '',
    contact_phone: '',
    remark: '',
    credit_warning_threshold: undefined as number | undefined,
    admin_username: '',
    admin_display_name: '',
    admin_password: '',
  },
  mutation: createMutation,
  toPayload: (f) => ({
    name: f.name,
    contact_name: f.contact_name || undefined,
    contact_phone: f.contact_phone || undefined,
    remark: f.remark || undefined,
    credit_warning_threshold: typeof f.credit_warning_threshold === 'number'
      ? f.credit_warning_threshold : undefined,
    admin_username: f.admin_username,
    admin_display_name: f.admin_display_name,
    admin_password: f.admin_password,
  }),
})

// columns 展示组织基础信息、状态和操作；启用/禁用按钮按当前状态互斥显示。
const columns = [
  // 名称列：含 remark 副标题，保留页面内 render
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
  statusColumn<Organization>('状态', r => formatOrgStatus(r.status)),
  // 联系人/电话/预警阈值列：保留页面内 render
  { title: '联系人', key: 'contact_name', render: (row: Organization) => row.contact_name || '—' },
  { title: '电话', key: 'contact_phone', render: (row: Organization) => row.contact_phone || '—' },
  {
    title: '预警阈值',
    key: 'credit_warning_threshold',
    render: (row: Organization) => typeof row.credit_warning_threshold === 'number'
      ? `${row.credit_warning_threshold}%` : '—',
  },
  // 启用/禁用互斥：用两条 RowAction + hidden 分别渲染
  actionColumn<Organization>([
    { label: '充值', type: 'primary', onClick: openRecharge },
    { label: '禁用', onClick: r => onToggle(r, 'disable'), hidden: r => r.status !== 'active' },
    { label: '启用', type: 'primary', onClick: r => onToggle(r, 'enable'), hidden: r => r.status === 'active' },
  ]),
]

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
async function submitRecharge() {
  if (!canSubmitRecharge.value) return
  rechargeFeedback.value = ''
  rechargeFeedbackError.value = false
  try {
    const result = await rechargeMutation.mutateAsync({
      credit_amount: rechargeAmount.value ?? 0,
      remark: rechargeRemark.value || undefined,
    })
    rechargeFeedback.value = `已充值 ${result.credit_amount.toLocaleString()} 点`
    rechargeAmount.value = null
    rechargeRemark.value = ''
  } catch (err: unknown) {
    rechargeFeedbackError.value = true
    rechargeFeedback.value = err instanceof Error ? err.message : '充值失败'
  }
}
</script>

<style scoped>
.recharge-dialog { display: grid; gap: 14px; }
</style>
