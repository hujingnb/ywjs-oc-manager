<template>
  <div style="display: grid; gap: 18px">
    <!-- 组织列表 -->
    <DataTableList
      :title="t('platform.orgs.title')"
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
          {{ t('platform.orgs.addButton') }}
        </n-button>
      </template>
    </DataTableList>
    <p v-if="copyFeedback" class="state-text" :class="{ danger: copyFeedbackError }">{{ copyFeedback }}</p>

    <!-- 创建 / 编辑组织表单（modalMode 区分两种模式） -->
    <n-card v-if="formVisible || editFormVisible" :bordered="true">
      <template #header>
        <div style="display: flex; align-items: center; justify-content: space-between">
          <div>
            <p class="eyebrow">{{ modalMode === 'create' ? 'New' : 'Edit' }}</p>
            <h2 style="margin: 0">{{ modalMode === 'create' ? t('platform.orgs.form.createTitle') : t('platform.orgs.form.editTitle') }}</h2>
          </div>
          <n-button quaternary circle @click="closeAnyForm">
            <template #icon><X :size="18" /></template>
          </n-button>
        </div>
      </template>
      <!-- 创建模式使用 createForm，编辑模式使用 editForm -->
      <n-form :model="modalMode === 'create' ? form : editForm" label-placement="top" @submit.prevent="submitAnyForm">
        <n-grid :cols="2" :x-gap="14">
          <n-grid-item>
            <n-form-item :label="t('platform.orgs.form.labelName')">
              <n-input
                v-if="modalMode === 'create'"
                v-model:value="form.name"
                :placeholder="t('platform.orgs.form.placeholderName')"
              />
              <n-input
                v-else
                v-model:value="editForm.name"
                :placeholder="t('platform.orgs.form.placeholderName')"
              />
            </n-form-item>
          </n-grid-item>
          <!-- 组织标识：创建时必填，编辑时只读展示 -->
          <n-grid-item v-if="modalMode === 'create'">
            <n-form-item :label="t('platform.orgs.form.labelCode')">
              <n-input v-model:value="form.code" :placeholder="t('platform.orgs.form.placeholderCode')" />
            </n-form-item>
          </n-grid-item>
          <n-grid-item v-else>
            <n-form-item :label="t('platform.orgs.form.labelCodeReadonly')">
              <n-input :value="editingOrg?.code ?? ''" disabled />
            </n-form-item>
          </n-grid-item>
          <!-- 管理员账号字段仅创建模式展示 -->
          <template v-if="modalMode === 'create'">
            <n-grid-item>
              <n-form-item :label="t('platform.orgs.form.labelAdminUsername')">
                <n-input v-model:value="form.admin_username" :placeholder="t('platform.orgs.form.placeholderAdminUsername')" />
              </n-form-item>
            </n-grid-item>
            <n-grid-item>
              <n-form-item :label="t('platform.orgs.form.labelAdminDisplayName')">
                <n-input v-model:value="form.admin_display_name" :placeholder="t('platform.orgs.form.placeholderAdminDisplayName')" />
              </n-form-item>
            </n-grid-item>
            <n-grid-item>
              <n-form-item :label="t('platform.orgs.form.labelAdminPassword')">
                <n-input v-model:value="form.admin_password" type="password" show-password-on="click" :placeholder="t('platform.orgs.form.placeholderAdminPassword')" />
              </n-form-item>
            </n-grid-item>
          </template>
          <n-grid-item>
            <n-form-item :label="t('platform.orgs.form.labelContact')">
              <n-input
                v-if="modalMode === 'create'"
                v-model:value="form.contact_name"
                :placeholder="t('platform.orgs.form.placeholderContact')"
              />
              <n-input
                v-else
                v-model:value="editForm.contact_name"
                :placeholder="t('platform.orgs.form.placeholderContact')"
              />
            </n-form-item>
          </n-grid-item>
          <n-grid-item>
            <n-form-item :label="t('platform.orgs.form.labelPhone')">
              <n-input
                v-if="modalMode === 'create'"
                v-model:value="form.contact_phone"
                :placeholder="t('platform.orgs.form.placeholderPhone')"
              />
              <n-input
                v-else
                v-model:value="editForm.contact_phone"
                :placeholder="t('platform.orgs.form.placeholderPhone')"
              />
            </n-form-item>
          </n-grid-item>
          <n-grid-item>
            <n-form-item :label="t('platform.orgs.form.labelCreditWarning')">
              <n-input-number
                v-if="modalMode === 'create'"
                v-model:value="form.credit_warning_threshold"
                :min="0" :max="100" style="width: 100%"
              />
              <n-input-number
                v-else
                v-model:value="editForm.credit_warning_threshold"
                :min="0" :max="100" style="width: 100%"
              />
            </n-form-item>
          </n-grid-item>
          <n-grid-item>
            <n-form-item :label="t('platform.orgs.form.labelMaxInstance')">
              <n-input-number
                v-if="modalMode === 'create'"
                v-model:value="form.max_instance_count"
                :min="1" :precision="0" clearable style="width: 100%"
                :placeholder="t('platform.orgs.form.placeholderMaxInstance')"
              />
              <n-input-number
                v-else
                v-model:value="editForm.max_instance_count"
                :min="1" :precision="0" clearable style="width: 100%"
                :placeholder="t('platform.orgs.form.placeholderMaxInstance')"
              />
            </n-form-item>
          </n-grid-item>
          <n-grid-item>
            <n-form-item :label="t('platform.orgs.form.labelKnowledgeQuota')">
              <n-input-number
                v-if="modalMode === 'create'"
                v-model:value="form.knowledge_quota_gb"
                :min="1" :precision="0" style="width: 100%"
              />
              <n-input-number
                v-else
                v-model:value="editForm.knowledge_quota_gb"
                :min="1" :precision="0" style="width: 100%"
              />
            </n-form-item>
          </n-grid-item>
          <n-grid-item>
            <n-form-item :label="t('platform.orgs.form.labelPersonalKnowledgeQuota')">
              <n-input-number
                v-if="modalMode === 'create'"
                v-model:value="form.personal_knowledge_quota_gb"
                :min="1" :precision="0" style="width: 100%"
              />
              <n-input-number
                v-else
                v-model:value="editForm.personal_knowledge_quota_gb"
                :min="1" :precision="0" style="width: 100%"
              />
              <template #feedback>
                {{ t('platform.orgs.form.personalKnowledgeQuotaHint') }}
              </template>
            </n-form-item>
          </n-grid-item>
          <template v-if="modalMode === 'edit'">
            <n-grid-item>
              <n-form-item :label="t('platform.orgs.form.labelAICCEnabled')">
                <n-switch v-model:value="editForm.aicc_enabled" />
              </n-form-item>
            </n-grid-item>
            <n-grid-item>
              <n-form-item :label="t('platform.orgs.form.labelAICCAgentLimit')">
                <n-input-number
                  v-model:value="editForm.aicc_agent_limit"
                  :min="0" :precision="0" clearable style="width: 100%"
                  :placeholder="t('platform.orgs.form.placeholderAICCAgentLimit')"
                />
              </n-form-item>
            </n-grid-item>
            <n-grid-item :span="2">
              <n-space justify="end">
                <n-button
                  attr-type="button"
                  :loading="aiccConfigSubmitting"
                  :disabled="aiccConfigSubmitting"
                  @click="submitAICCConfig"
                >{{ t('platform.orgs.form.saveAICCConfig') }}</n-button>
              </n-space>
              <p v-if="aiccConfigError" class="state-text danger">{{ aiccConfigError }}</p>
            </n-grid-item>
          </template>
          <n-grid-item :span="2">
            <n-form-item :label="t('platform.orgs.form.labelRemark')">
              <n-input
                v-if="modalMode === 'create'"
                v-model:value="form.remark"
                type="textarea"
                :rows="2"
              />
              <n-input
                v-else
                v-model:value="editForm.remark"
                type="textarea"
                :rows="2"
              />
            </n-form-item>
          </n-grid-item>
          <n-grid-item :span="2">
            <n-form-item :label="t('platform.orgs.form.labelVersions')">
              <n-select
                v-if="modalMode === 'create'"
                v-model:value="form.assistant_version_ids"
                multiple
                :loading="versionsQuery.isLoading.value"
                :options="versionOptions"
                :placeholder="t('platform.orgs.form.placeholderVersions')"
              />
              <n-select
                v-else
                v-model:value="editForm.assistant_version_ids"
                multiple
                :loading="versionsQuery.isLoading.value"
                :options="versionOptions"
                :placeholder="t('platform.orgs.form.placeholderVersions')"
              />
            </n-form-item>
          </n-grid-item>
          <n-grid-item :span="2">
            <n-space justify="end">
              <n-button @click="closeAnyForm">{{ t('common.actions.cancel') }}</n-button>
              <n-button
                type="primary"
                attr-type="submit"
                :loading="modalMode === 'create' ? creating : editSubmitting"
                :disabled="modalMode === 'create' ? creating : editSubmitting"
              >{{ t('common.actions.save') }}</n-button>
            </n-space>
            <p v-if="modalMode === 'create' ? submitError : editError" class="state-text danger">
              {{ modalMode === 'create' ? submitError : editError }}
            </p>
          </n-grid-item>
        </n-grid>
      </n-form>
    </n-card>

    <!-- 组织充值弹框 -->
    <n-modal v-model:show="rechargeVisible" preset="card" style="max-width: 560px" :title="t('platform.orgs.rechargeModal.title')">
      <div v-if="selectedOrg" class="recharge-dialog">
        <div>
          <p class="eyebrow">Billing</p>
          <h3 style="margin: 0">{{ selectedOrg.name }}</h3>
        </div>
        <p class="state-text">
          {{ t('platform.orgs.rechargeModal.currentBalance') }}
          <strong v-if="balanceQuery.isLoading.value">{{ t('platform.orgs.rechargeModal.balanceLoading') }}</strong>
          <strong v-else-if="balance">
            {{ t('platform.orgs.rechargeModal.remain', { remain: formatQuotaValue(balance.remain_quota, billingStatus) }) }} ｜ {{ t('platform.orgs.rechargeModal.used', { used: formatQuotaValue(balance.used_quota, billingStatus) }) }}
          </strong>
          <strong v-else class="danger">{{ t('platform.orgs.rechargeModal.balanceFail') }}</strong>
        </p>
        <n-form label-placement="top" @submit.prevent="submitRecharge">
          <n-form-item :label="t('platform.orgs.rechargeModal.labelAmount')">
            <n-input-number v-model:value="rechargeAmount" :min="1" :precision="0" style="width: 100%" :placeholder="t('platform.orgs.rechargeModal.placeholderAmount')" />
          </n-form-item>
          <n-form-item :label="t('platform.orgs.rechargeModal.labelRemark')">
            <n-input v-model:value="rechargeRemark" :placeholder="t('platform.orgs.rechargeModal.placeholderRemark')" />
          </n-form-item>
          <n-space justify="end">
            <n-button @click="closeRecharge">{{ t('common.actions.cancel') }}</n-button>
            <n-button
              type="primary"
              attr-type="submit"
              :disabled="!selectedOrgId"
              :loading="rechargeMutation.isPending.value"
            >
              {{ t('platform.orgs.rechargeModal.confirmButton') }}
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
      :title="rechargeHistoryOrg ? t('platform.orgs.historyModal.titleWithOrg', { name: rechargeHistoryOrg.name }) : t('platform.orgs.historyModal.titleFallback')"
    >
      <div v-if="rechargeHistoryOrg" style="display: grid; gap: 16px">
        <!-- 概况卡片 -->
        <n-grid :cols="2" :x-gap="14">
          <n-grid-item>
            <n-statistic :label="t('platform.orgs.historyModal.totalRecharged')">
              <template v-if="rechargeHistoryBalanceQuery.isLoading.value">—</template>
              <template v-else-if="rechargeHistoryBalance">
                {{ formatDisplayAmount(rechargeHistoryBalance.total_recharged, billingStatus) }}
              </template>
              <template v-else>{{ t('platform.orgs.historyModal.queryFail') }}</template>
            </n-statistic>
          </n-grid-item>
          <n-grid-item>
            <n-statistic :label="t('platform.orgs.historyModal.currentBalance')">
              <template v-if="rechargeHistoryBalanceQuery.isLoading.value">—</template>
              <template v-else-if="rechargeHistoryBalance">
                {{ formatQuotaValue(rechargeHistoryBalance.remain_quota, billingStatus) }}
              </template>
              <template v-else>{{ t('platform.orgs.historyModal.queryFail') }}</template>
            </n-statistic>
          </n-grid-item>
        </n-grid>
        <!-- 充值记录表格 -->
        <div v-if="rechargeHistoryLoading" class="state-text">{{ t('platform.orgs.historyModal.loading') }}</div>
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
import { computed, h, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useQueries, type UseMutationReturnType } from '@tanstack/vue-query'
import { Plus, X } from 'lucide-vue-next'
import {
  NButton, NCard, NDataTable, NForm, NFormItem, NGrid, NGridItem,
  NInput, NInputNumber, NModal, NSelect, NSpace, NStatistic, NSwitch,
} from 'naive-ui'

import { formatOrgStatus } from '@/domain/status'
import {
  useCreateOrganization, useOrganizationsQuery, useUpdateOrganization, useUpdateOrganizationAICCConfig, useUpdateOrganizationStatus,
} from '@/api/hooks/useOrganizations'
import type { OrganizationFormPayload } from '@/api/hooks/useOrganizations'
import { useAssistantVersionsQuery } from '@/api/hooks/useAssistantVersions'
import { apiRequest } from '@/api/client'
import { useBillingStatusQuery, useOrgBalanceQuery, useRechargeMutation, useRechargesQuery } from '@/api/hooks/useRecharge'
import type { BalanceDTO } from '@/api/hooks/useRecharge'
import type { Organization } from '@/api'
import DataTableList from '@/components/DataTableList.vue'
import { statusColumn, actionColumn } from '@/components/columns'
import { useFormModal } from '@/composables/useFormModal'
import { formatDisplayAmount, formatQuotaValue } from '@/pages/usage/usageFormatting'

// OrganizationsPage 是平台组织管理页，负责创建组织、编辑组织、启停组织和给组织充值。
const { t } = useI18n()
const { data: organizations, isLoading, error } = useOrganizationsQuery()
const createMutation = useCreateOrganization()
const updateMutation = useUpdateOrganization()
const updateAICCConfigMutation = useUpdateOrganizationAICCConfig()
const statusMutation = useUpdateOrganizationStatus()

// modalMode 区分当前表单是创建模式还是编辑模式，控制字段显隐和提交目标。
const modalMode = ref<'create' | 'edit'>('create')
// editingOrg 保存正在编辑的组织对象，用于只读展示 code 和预填编辑表单。
const editingOrg = ref<Organization | null>(null)
// editFormVisible 控制编辑模式下表单的显隐（与 formVisible 分离以避免状态混用）。
const editFormVisible = ref(false)
// knowledgeQuotaGBDefault 是企业表单默认展示的知识库空间，固定为 1GB。
const knowledgeQuotaGBDefault = 1
// bytesPerGB 用于将表单中的 GB 输入换算成后端接收的 bytes。
const bytesPerGB = 1024 * 1024 * 1024

// quotaBytesToGB 将后端字节容量转为企业表单中的 GB 数字。
function quotaBytesToGB(bytes?: number): number {
  if (!bytes || bytes <= 0) return knowledgeQuotaGBDefault
  return Math.round(bytes / bytesPerGB)
}

// quotaGBToBytes 将企业表单中的 GB 数字转为后端 bytes；空值回落为 1GB。
function quotaGBToBytes(gb?: number): number {
  return Math.max(1, Math.round(gb ?? knowledgeQuotaGBDefault)) * bytesPerGB
}

// editQuotaBytesForPayload 在编辑表单未改动 GB 输入时保留后端原始 bytes，避免整 GB 展示导致非整 GB 容量被静默舍入。
function editQuotaBytesForPayload(): number {
  if (
    editForm.knowledge_quota_gb === editForm.knowledge_quota_original_gb
    && typeof editForm.knowledge_quota_original_bytes === 'number'
  ) {
    return editForm.knowledge_quota_original_bytes
  }
  return quotaGBToBytes(editForm.knowledge_quota_gb)
}

// editPersonalQuotaBytesForPayload 在编辑表单未改动个人知识库 GB 输入时保留后端原始 bytes，避免整 GB 展示导致非整 GB 容量被静默舍入。
function editPersonalQuotaBytesForPayload(): number {
  if (
    editForm.personal_knowledge_quota_gb === editForm.personal_knowledge_quota_original_gb
    && typeof editForm.personal_knowledge_quota_original_bytes === 'number'
  ) {
    return editForm.personal_knowledge_quota_original_bytes
  }
  return quotaGBToBytes(editForm.personal_knowledge_quota_gb)
}

// OrganizationCreateForm 在后端创建 payload 外追加 GB 输入字段，仅用于页面表单展示。
interface OrganizationCreateForm extends OrganizationFormPayload {
  // knowledge_quota_gb 是平台管理员看到的企业知识库 GB 单位，提交前转换为后端 bytes。
  knowledge_quota_gb: number
  // personal_knowledge_quota_gb 是个人知识库（实例默认）空间 GB 输入，提交前转换为 bytes。
  personal_knowledge_quota_gb: number
}

// editForm 是编辑模式的响应式表单对象，由 openEditForm 按当前组织数据预填。
const editForm = reactive({
  name: '',
  contact_name: '',
  contact_phone: '',
  remark: '',
  credit_warning_threshold: undefined as number | undefined,
  max_instance_count: undefined as number | undefined,
  knowledge_quota_gb: knowledgeQuotaGBDefault,
  knowledge_quota_original_gb: knowledgeQuotaGBDefault,
  knowledge_quota_original_bytes: undefined as number | undefined,
  personal_knowledge_quota_gb: knowledgeQuotaGBDefault,
  personal_knowledge_quota_original_gb: knowledgeQuotaGBDefault,
  personal_knowledge_quota_original_bytes: undefined as number | undefined,
  assistant_version_ids: [] as string[],
  aicc_enabled: false,
  aicc_agent_limit: undefined as number | undefined,
})
// editSubmitting 控制编辑提交中的 loading 状态。
const editSubmitting = ref(false)
// editError 保存编辑提交的错误信息。
const editError = ref<string | null>(null)
// aiccConfigSubmitting 控制 AICC 配置独立保存按钮的 loading 状态。
const aiccConfigSubmitting = ref(false)
// aiccConfigError 保存 AICC 配置独立保存失败时的错误信息。
const aiccConfigError = ref<string | null>(null)

// openEditForm 打开编辑模式，将当前组织的资料预填到 editForm。
function openEditForm(org: Organization) {
  editingOrg.value = org
  modalMode.value = 'edit'
  editForm.name = org.name
  editForm.contact_name = org.contact_name ?? ''
  editForm.contact_phone = org.contact_phone ?? ''
  editForm.remark = org.remark ?? ''
  editForm.credit_warning_threshold = typeof org.credit_warning_threshold === 'number'
    ? org.credit_warning_threshold : undefined
  editForm.max_instance_count = typeof org.max_instance_count === 'number'
    ? org.max_instance_count : undefined
  const knowledgeQuotaGB = quotaBytesToGB(org.knowledge_quota_bytes)
  editForm.knowledge_quota_gb = knowledgeQuotaGB
  editForm.knowledge_quota_original_gb = knowledgeQuotaGB
  editForm.knowledge_quota_original_bytes = typeof org.knowledge_quota_bytes === 'number'
    ? org.knowledge_quota_bytes : undefined
  const personalQuotaGB = quotaBytesToGB(org.default_app_knowledge_quota_bytes)
  editForm.personal_knowledge_quota_gb = personalQuotaGB
  editForm.personal_knowledge_quota_original_gb = personalQuotaGB
  editForm.personal_knowledge_quota_original_bytes = typeof org.default_app_knowledge_quota_bytes === 'number'
    ? org.default_app_knowledge_quota_bytes : undefined
  editForm.assistant_version_ids = org.assistant_version_ids ? [...org.assistant_version_ids] : []
  editForm.aicc_enabled = Boolean(org.aicc_enabled)
  editForm.aicc_agent_limit = typeof org.aicc_agent_limit === 'number'
    ? org.aicc_agent_limit : undefined
  editError.value = null
  aiccConfigError.value = null
  editFormVisible.value = true
}

// closeAnyForm 关闭创建或编辑表单，复位 modalMode。
function closeAnyForm() {
  formVisible.value = false
  editFormVisible.value = false
  modalMode.value = 'create'
}

// submitAnyForm 根据 modalMode 分别调用创建或编辑 mutation。
async function submitAnyForm() {
  if (modalMode.value === 'create') {
    await submitOrganization()
  } else {
    await submitEditOrganization()
  }
}

// submitEditOrganization 提交编辑表单，调用 PATCH /organizations/:id。
async function submitEditOrganization() {
  if (!editingOrg.value) return
  editError.value = null
  editSubmitting.value = true
  try {
    await updateMutation.mutateAsync({
      id: editingOrg.value.id,
      payload: {
        name: editForm.name,
        contact_name: editForm.contact_name || undefined,
        contact_phone: editForm.contact_phone || undefined,
        remark: editForm.remark || undefined,
        credit_warning_threshold: typeof editForm.credit_warning_threshold === 'number'
          ? editForm.credit_warning_threshold : undefined,
        max_instance_count: typeof editForm.max_instance_count === 'number'
          ? editForm.max_instance_count : undefined,
        knowledge_quota_bytes: editQuotaBytesForPayload(),
        default_app_knowledge_quota_bytes: editPersonalQuotaBytesForPayload(),
        assistant_version_ids: editForm.assistant_version_ids,
      },
    })
    editFormVisible.value = false
    modalMode.value = 'create'
  } catch (err) {
    editError.value = err instanceof Error ? err.message : t('platform.orgs.editError')
  } finally {
    editSubmitting.value = false
  }
}

// submitAICCConfig 独立保存企业 AICC 开通配置，避免基础资料保存与 AICC 配置出现非原子双提交。
async function submitAICCConfig() {
  if (!editingOrg.value) return
  aiccConfigError.value = null
  aiccConfigSubmitting.value = true
  try {
    await updateAICCConfigMutation.mutateAsync({
      id: editingOrg.value.id,
      payload: {
        enabled: editForm.aicc_enabled,
        agent_limit: typeof editForm.aicc_agent_limit === 'number' ? editForm.aicc_agent_limit : null,
      },
    })
  } catch (err) {
    aiccConfigError.value = err instanceof Error ? err.message : t('platform.orgs.aiccConfigError')
  } finally {
    aiccConfigSubmitting.value = false
  }
}
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
// adminPasswordCopyHint 是复制企业信息时密码字段的占位文本，提示管理员密码不保存明文。
const adminPasswordCopyHint = t('platform.orgs.copy.adminPasswordHint')
// createFormMutation 适配 useFormModal 的同型表单/提交泛型，调用真实 API 前去掉 UI-only GB 字段。
const createFormMutation = {
  ...createMutation,
  mutateAsync: async (payload: OrganizationCreateForm) => createMutation.mutateAsync({
    name: payload.name,
    code: payload.code,
    contact_name: payload.contact_name,
    contact_phone: payload.contact_phone,
    remark: payload.remark,
    credit_warning_threshold: payload.credit_warning_threshold,
    max_instance_count: payload.max_instance_count,
    knowledge_quota_bytes: payload.knowledge_quota_bytes,
    default_app_knowledge_quota_bytes: payload.default_app_knowledge_quota_bytes,
    assistant_version_ids: payload.assistant_version_ids,
    admin_username: payload.admin_username,
    admin_display_name: payload.admin_display_name,
    admin_password: payload.admin_password,
  }),
} as unknown as UseMutationReturnType<Organization, Error, OrganizationCreateForm, unknown>
// 创建组织表单状态聚合到 useFormModal；toPayload 处理可选字段的 || undefined 过滤
const { form, formVisible, creating, submitError, openForm: _openForm, submit: submitForm } = useFormModal({
  initial: {
    name: '',
    code: '',
    contact_name: '',
    contact_phone: '',
    remark: '',
    credit_warning_threshold: undefined as number | undefined,
    max_instance_count: undefined as number | undefined,
    knowledge_quota_gb: knowledgeQuotaGBDefault,
    personal_knowledge_quota_gb: knowledgeQuotaGBDefault,
    admin_username: '',
    admin_display_name: '',
    admin_password: '',
    assistant_version_ids: [] as string[],
  } satisfies OrganizationCreateForm,
  mutation: createFormMutation,
  toPayload: (f): OrganizationCreateForm => ({
    name: f.name,
    code: f.code,
    contact_name: f.contact_name || undefined,
    contact_phone: f.contact_phone || undefined,
    remark: f.remark || undefined,
    credit_warning_threshold: typeof f.credit_warning_threshold === 'number'
      ? f.credit_warning_threshold : undefined,
    max_instance_count: typeof f.max_instance_count === 'number' ? f.max_instance_count : undefined,
    knowledge_quota_gb: f.knowledge_quota_gb,
    knowledge_quota_bytes: quotaGBToBytes(f.knowledge_quota_gb),
    personal_knowledge_quota_gb: f.personal_knowledge_quota_gb,
    default_app_knowledge_quota_bytes: quotaGBToBytes(f.personal_knowledge_quota_gb),
    admin_username: f.admin_username,
    admin_display_name: f.admin_display_name,
    admin_password: f.admin_password,
    assistant_version_ids: f.assistant_version_ids,
  }),
})
// versionsQuery 在创建或编辑表单打开时发起请求，避免页面初始化时的无谓请求。
const versionsQuery = useAssistantVersionsQuery(() => formVisible.value || editFormVisible.value)
const versionOptions = computed(() => (versionsQuery.data.value ?? []).map(v => ({
  label: v.name,
  value: v.id,
})))

// openForm 设置创建模式后打开表单，确保 modalMode 与 formVisible 始终同步。
function openForm() {
  modalMode.value = 'create'
  _openForm()
}

// submitOrganization 兜底处理键盘提交，避免绕过保存按钮禁用状态。
// 助手版本为可选项，无需前置校验；直接调用 submitForm。
async function submitOrganization() {
  await submitForm()
}

// columns 展示组织基础信息、状态、余额和操作；改为 computed 以引用响应式的 balanceByOrgId 和 t()。
const columns = computed(() => [
  // 名称列：含 remark 副标题
  {
    title: t('platform.orgs.columns.name'),
    key: 'name',
    render: (row: Organization) => [
      h('strong', row.name),
      row.remark
        ? h('small', { class: 'data-table-subtitle' }, row.remark)
        : null,
    ],
  },
  { title: t('platform.orgs.columns.code'), key: 'code', render: (row: Organization) => row.code || '—' },
  statusColumn<Organization>(t('platform.orgs.columns.status'), r => formatOrgStatus(r.status)),
  // 联系人/电话/预警阈值列
  { title: t('platform.orgs.columns.contact'), key: 'contact_name', render: (row: Organization) => row.contact_name || '—' },
  { title: t('platform.orgs.columns.phone'), key: 'contact_phone', render: (row: Organization) => row.contact_phone || '—' },
  {
    title: t('platform.orgs.columns.warningThreshold'),
    key: 'credit_warning_threshold',
    render: (row: Organization) => typeof row.credit_warning_threshold === 'number'
      ? `${row.credit_warning_threshold}%` : '—',
  },
  {
    title: t('platform.orgs.columns.maxInstance'),
    key: 'max_instance_count',
    render: (row: Organization) => typeof row.max_instance_count === 'number'
      ? String(row.max_instance_count) : t('platform.orgs.columns.unlimited'),
  },
  {
    title: t('platform.orgs.columns.knowledgeQuota'),
    key: 'knowledge_quota_bytes',
    render: (row: Organization) => typeof row.knowledge_quota_bytes === 'number'
      ? `${Math.round(row.knowledge_quota_bytes / bytesPerGB)}GB` : '1GB',
  },
  // 当前余额列：从并发查询结果映射到对应行，未加载时显示省略号。
  {
    title: t('platform.orgs.columns.balance'),
    key: 'remain_quota',
    render: (row: Organization) => {
      const b = balanceByOrgId.value[row.id]
      if (!b) return '…'
      return formatQuotaValue(b.remain_quota, billingStatus.value)
    },
  },
  // 启用/禁用互斥：用两条 RowAction + hidden 分别渲染；编辑按钮放在首位方便操作
  actionColumn<Organization>([
    { label: t('platform.orgs.actions.edit'), onClick: openEditForm },
    { label: t('platform.orgs.actions.copyInfo'), onClick: r => { void copyOrganizationInfo(r) } },
    { label: t('platform.orgs.actions.rechargeHistory'), onClick: openRechargeHistory },
    { label: t('platform.orgs.actions.recharge'), type: 'primary', onClick: openRecharge },
    { label: t('platform.orgs.actions.disable'), onClick: r => onToggle(r, 'disable'), hidden: r => r.status !== 'active' },
    { label: t('platform.orgs.actions.enable'), type: 'primary', onClick: r => onToggle(r, 'enable'), hidden: r => r.status === 'active' },
  ]),
])

function optionalAdminUsername(org: Organization) {
  return org.admin_username ?? ''
}

// formatOrganizationCopyInfo 固定对外复制格式，便于平台管理员直接发送给组织管理员。
function formatOrganizationCopyInfo(org: Organization) {
  return [
    t('platform.orgs.copy.formatCode', { code: org.code || '' }),
    t('platform.orgs.copy.formatName', { name: org.name }),
    t('platform.orgs.copy.formatAdminUsername', { username: optionalAdminUsername(org) }),
    t('platform.orgs.copy.formatAdminPassword', { hint: adminPasswordCopyHint }),
  ].join('\n')
}

// copyOrganizationInfo 使用浏览器剪贴板写入组织登录信息，并在页面内暴露结果。
async function copyOrganizationInfo(org: Organization) {
  copyFeedback.value = ''
  copyFeedbackError.value = false
  try {
    await navigator.clipboard.writeText(formatOrganizationCopyInfo(org))
    copyFeedback.value = t('platform.orgs.copy.successMsg', { name: org.name })
  } catch {
    copyFeedbackError.value = true
    copyFeedback.value = t('platform.orgs.copy.failMsg')
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
    rechargeFeedback.value = t('platform.orgs.rechargeModal.invalidAmount')
    return
  }
  rechargeFeedback.value = ''
  rechargeFeedbackError.value = false
  try {
    const result = await rechargeMutation.mutateAsync({
      credit_amount: rechargeAmount.value ?? 0,
      remark: rechargeRemark.value || undefined,
    })
    rechargeFeedback.value = t('platform.orgs.rechargeModal.successMsg', { amount: formatDisplayAmount(result.credit_amount, billingStatus.value) })
    rechargeAmount.value = null
    rechargeRemark.value = ''
  } catch (err: unknown) {
    rechargeFeedbackError.value = true
    rechargeFeedback.value = err instanceof Error ? err.message : t('platform.orgs.rechargeModal.failMsg')
  }
}

// rechargeHistoryColumns 是充值记录弹窗的表格列定义；含操作人 ID（平台管理员可见）。
const rechargeHistoryColumns = computed(() => [
  { title: t('platform.orgs.historyModal.columns.time'), key: 'created_at', render: (r: { created_at: string }) => r.created_at.replace('T', ' ').slice(0, 19) },
  {
    title: t('platform.orgs.historyModal.columns.amount'),
    key: 'credit_amount',
    render: (r: { credit_amount: number }) => formatDisplayAmount(r.credit_amount, billingStatus.value),
  },
  { title: t('platform.orgs.historyModal.columns.remark'), key: 'remark', render: (r: { remark?: string }) => r.remark || '—' },
  {
    title: t('platform.orgs.historyModal.columns.status'),
    key: 'status',
    render: (r: { status: string }) => r.status === 'succeeded' ? t('platform.orgs.historyModal.columns.statusSucceeded') : t('platform.orgs.historyModal.columns.statusFailed'),
  },
  { title: t('platform.orgs.historyModal.columns.operator'), key: 'operator_id', render: (r: { operator_id?: string }) => r.operator_id ? r.operator_id.slice(0, 8) + '…' : '—' },
])
</script>

<style scoped>
.recharge-dialog { display: grid; gap: 14px; }
</style>
