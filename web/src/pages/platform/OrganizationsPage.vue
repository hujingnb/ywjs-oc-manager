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
                <n-switch v-model:value="editForm.aicc_enabled" :disabled="!isAICCConfigEditable" />
              </n-form-item>
            </n-grid-item>
            <n-grid-item>
              <n-form-item :label="t('platform.orgs.form.labelAICCAgentLimit')">
                <n-input-number
                  v-model:value="editForm.aicc_agent_limit"
                  :min="0" :precision="0" clearable style="width: 100%"
                  :disabled="!isAICCConfigEditable"
                  :placeholder="t('platform.orgs.form.placeholderAICCAgentLimit')"
                />
              </n-form-item>
            </n-grid-item>
            <n-grid-item :span="2">
              <n-form-item :label="t('platform.orgs.form.labelAICCModel')">
                <n-select
                  v-model:value="editForm.aicc_model"
                  filterable clearable
                  :loading="modelsQuery.isLoading.value || modelsQuery.isFetching.value || aiccConfigQuery.isLoading.value || aiccConfigQuery.isFetching.value"
                  :disabled="modelsQuery.isLoading.value || modelsQuery.isFetching.value || modelsQuery.isError.value || !isAICCConfigEditable"
                  :options="modelOptions"
                  :placeholder="t('platform.orgs.form.placeholderAICCModel')"
                />
                <template #feedback>
                  <span v-if="modelsQuery.isError.value" class="danger">{{ t('platform.orgs.form.aiccModelLoadFail') }}</span>
                  <span v-else-if="isSelectedModelUnavailable" class="danger">{{ t('platform.orgs.form.aiccSelectedModelUnavailable', { model: editForm.aicc_model }) }}</span>
                  <span v-else-if="aiccConfigQuery.isError.value" class="danger">{{ t('platform.orgs.form.aiccConfigLoadFail') }}</span>
                  <span v-else-if="editForm.aicc_enabled && !editForm.aicc_model" class="danger">{{ t('platform.orgs.form.aiccModelRequired') }}</span>
                </template>
              </n-form-item>
            </n-grid-item>
            <n-grid-item :span="2">
              <n-form-item :label="t('platform.orgs.form.labelAICCIndustryKnowledge')">
                <n-select
                  v-model:value="editForm.industry_knowledge_base_ids"
                  multiple filterable clearable
                  :disabled="!isAICCConfigEditable"
                  :options="industryKnowledgeOptions"
                  :placeholder="t('platform.orgs.form.placeholderAICCIndustryKnowledge')"
                />
              </n-form-item>
            </n-grid-item>
            <n-grid-item :span="2">
              <n-space justify="end">
                <n-button
                  attr-type="button"
                  :loading="isAICCConfigSubmitting"
                  :disabled="isAICCConfigSubmitting || editSubmitting || isAICCConfigSaveDisabled"
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
                attr-type="button"
                :loading="modalMode === 'create' ? creating : (editSubmitting || isAICCConfigSubmitting)"
                :disabled="modalMode === 'create' ? creating : (editSubmitting || isAICCConfigSubmitting || isAICCConfigSaveDisabled)"
                @click="submitAnyForm"
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

    <!-- 已启用企业换模会逐个滚动运行中客服，提交前明确告知影响范围。 -->
    <ConfirmActionModal
      :visible="modelChangeConfirmVisible"
      :title="t('platform.orgs.modelChangeConfirm.title')"
      :message="t('platform.orgs.modelChangeConfirm.message')"
      :busy="aiccConfigSubmitting || editSubmitting"
      :confirm-label="t('platform.orgs.modelChangeConfirm.confirmLabel')"
      @confirm="confirmModelChange"
      @cancel="cancelModelChange"
    />
  </div>
</template>

<script setup lang="ts">
import { computed, h, reactive, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { useQueries, type UseMutationReturnType } from '@tanstack/vue-query'
import { Plus, X } from 'lucide-vue-next'
import {
  NButton, NCard, NDataTable, NForm, NFormItem, NGrid, NGridItem,
  NInput, NInputNumber, NModal, NSelect, NSpace, NStatistic, NSwitch,
} from 'naive-ui'

import { formatOrgStatus } from '@/domain/status'
import {
  useCreateOrganization, useModelsQuery, useOrganizationAICCConfigQuery, useOrganizationsQuery,
  useUpdateOrganization, useUpdateOrganizationAICCConfig, useUpdateOrganizationStatus,
} from '@/api/hooks/useOrganizations'
import type {
  OrganizationAICCConfigPayload,
  OrganizationFormPayload,
  OrganizationUpdatePayload,
} from '@/api/hooks/useOrganizations'
import { useAssistantVersionsQuery } from '@/api/hooks/useAssistantVersions'
import { useIndustryKnowledgeBasesQuery } from '@/api/hooks/useIndustryKnowledge'
import { apiRequest } from '@/api/client'
import { useBillingStatusQuery, useOrgBalanceQuery, useRechargeMutation, useRechargesQuery } from '@/api/hooks/useRecharge'
import type { BalanceDTO } from '@/api/hooks/useRecharge'
import type { Organization } from '@/api'
import DataTableList from '@/components/DataTableList.vue'
import ConfirmActionModal from '@/components/ConfirmActionModal.vue'
import { statusColumn, actionColumn } from '@/components/columns'
import { useFormModal } from '@/composables/useFormModal'
import { formatDisplayAmount, formatQuotaValue } from '@/pages/usage/usageFormatting'

// OrganizationsPage 是平台组织管理页，负责创建组织、编辑组织、启停组织和给组织充值。
const { t } = useI18n()
const router = useRouter()
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
// editingOrgId 驱动独立 AICC 配置查询；关闭表单时清空 ID 以暂停请求。
const editingOrgId = computed(() => editingOrg.value?.id)
const aiccConfigQuery = useOrganizationAICCConfigQuery(editingOrgId)
// 模型目录仅在编辑企业时加载；任何加载异常都采用 fail-closed，禁止写入配置。
const modelsQuery = useModelsQuery(() => editFormVisible.value)
const modelOptions = computed(() => (modelsQuery.data.value ?? []).map(model => ({
  label: model.name,
  value: model.id,
})))
// 行业知识库是平台资源，仅在编辑企业时加载，作为 AICC 企业授权的多选来源。
const industryKnowledgeBasesQuery = useIndustryKnowledgeBasesQuery(() => editFormVisible.value)
const industryKnowledgeOptions = computed(() => (industryKnowledgeBasesQuery.data.value?.items ?? []).map(base => ({
  label: base.name,
  value: base.id,
})))
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
  aicc_model: '',
  aicc_agent_limit: undefined as number | undefined,
  industry_knowledge_base_ids: [] as string[],
})
// editSubmitting 控制编辑提交中的 loading 状态。
const editSubmitting = ref(false)
// editError 保存编辑提交的错误信息。
const editError = ref<string | null>(null)
// aiccConfigSubmitting 控制 AICC 配置独立保存按钮的 loading 状态。
const aiccConfigSubmitting = ref(false)
// aiccConfigSubmittingSessionToken 将异步保存状态绑定到发起时的编辑会话，避免旧请求锁住后来打开的企业。
const aiccConfigSubmittingSessionToken = ref<number | null>(null)
// aiccConfigError 保存 AICC 配置独立保存失败时的错误信息。
const aiccConfigError = ref<string | null>(null)
// originalAICCModel 记录独立 GET 快照，只要已有模型变化就需要二次确认。
const originalAICCModel = ref('')
// modelChangeConfirmVisible 与 pendingAICCSubmitMode 保存确认后的提交入口。
const modelChangeConfirmVisible = ref(false)
const pendingAICCSubmitMode = ref<'config' | 'all' | null>(null)
// pendingAICCSubmitOrgID 把确认动作绑定到发起保存时的企业，切换企业后旧确认自动失效。
const pendingAICCSubmitOrgID = ref<string | null>(null)
// successfulOrganizationPayloadSnapshot 记录已成功 PATCH 的企业资料，部分成功重试时避免重复写入相同快照。
const successfulOrganizationPayloadSnapshot = ref<{ orgID: string; serializedPayload: string } | null>(null)
// aiccConfigSessionToken 在打开、切换或关闭企业时递增；所有异步回调都必须验证自己仍属于当前会话。
const aiccConfigSessionToken = ref(0)
// aiccConfigHydrated 保存当前会话已接收的首个有效服务端快照，防止后台 refetch 覆盖管理员未保存的输入。
const aiccConfigHydrated = ref<{ sessionToken: number; orgID: string; revision: number } | null>(null)
// aiccConfigDirty 标记首个快照后的本地编辑，作为回填保护的显式业务状态。
const aiccConfigDirty = ref(false)
// isAICCConfigHydrating 防止服务端首个快照写入表单时被误判为管理员编辑。
const isAICCConfigHydrating = ref(false)

// isAICCConfigReady 拒绝把上一企业或未完成加载的配置误用于当前编辑对象。
const isAICCConfigReady = computed(() => (
  Boolean(editingOrgId.value)
  && aiccConfigQuery.data.value?.org_id === editingOrgId.value
  && !aiccConfigQuery.isLoading.value
  && !aiccConfigQuery.isFetching.value
  && !aiccConfigQuery.isError.value
))
// isAICCConfigEditable 允许已水合会话在后台刷新时继续编辑，但首次请求尚未得到有效快照或请求失败时一律禁用。
const isAICCConfigEditable = computed(() => (
  Boolean(editingOrgId.value)
  && aiccConfigHydrated.value?.sessionToken === aiccConfigSessionToken.value
  && aiccConfigHydrated.value.orgID === editingOrgId.value
  && !aiccConfigQuery.isError.value
))
// isAICCConfigSubmitting 只反映当前会话发起的独立保存，旧会话迟到请求不应阻断当前企业编辑。
const isAICCConfigSubmitting = computed(() => (
  aiccConfigSubmitting.value
  && aiccConfigSubmittingSessionToken.value === aiccConfigSessionToken.value
  && Boolean(editingOrgId.value)
))
// isSelectedModelUnavailable 防止目录刷新后继续提交刚下架的新选择。
const isSelectedModelUnavailable = computed(() => (
  Boolean(editForm.aicc_model)
  && !modelsQuery.isLoading.value
  && !modelsQuery.isFetching.value
  && !modelsQuery.isError.value
  && !(modelsQuery.data.value ?? []).some(model => model.id === editForm.aicc_model)
))
// isAICCConfigSaveDisabled 汇总加载态、错误态和启用时必选模型规则，主保存与独立保存保持一致。
const isAICCConfigSaveDisabled = computed(() => (
  !isAICCConfigReady.value
  || modelsQuery.isLoading.value
  || modelsQuery.isFetching.value
  || modelsQuery.isError.value
  || isSelectedModelUnavailable.value
  || (editForm.aicc_enabled && !editForm.aicc_model)
))

// 管理员编辑首个 AICC 快照后，如果字段发生变化即视为本地未保存输入；后台刷新不得覆盖。
watch(
  () => [
    editForm.aicc_enabled,
    editForm.aicc_model,
    editForm.aicc_agent_limit,
    editForm.industry_knowledge_base_ids.join(','),
  ],
  () => {
    if (isAICCConfigHydrating.value) return
    if (
      aiccConfigHydrated.value?.sessionToken === aiccConfigSessionToken.value
      && aiccConfigHydrated.value.orgID === editingOrgId.value
    ) aiccConfigDirty.value = true
  },
  { flush: 'sync' },
)

// 独立 GET 只回填当前编辑会话的首个完整快照；后续同企业数据更新保留本地未保存输入。
watch(
  [
    editFormVisible,
    () => aiccConfigQuery.data.value,
    () => aiccConfigQuery.isLoading.value,
    () => aiccConfigQuery.isFetching.value,
    () => aiccConfigQuery.isError.value,
  ],
  ([visible, config]) => {
    if (
      !visible || !config || config.org_id !== editingOrgId.value
      || aiccConfigQuery.isLoading.value || aiccConfigQuery.isFetching.value || aiccConfigQuery.isError.value
    ) return
    if (
      aiccConfigHydrated.value?.sessionToken === aiccConfigSessionToken.value
      && aiccConfigHydrated.value.orgID === config.org_id
    ) return
    isAICCConfigHydrating.value = true
    editForm.aicc_enabled = config.enabled
    editForm.aicc_model = config.model ?? ''
    editForm.aicc_agent_limit = typeof config.agent_limit === 'number' ? config.agent_limit : undefined
    editForm.industry_knowledge_base_ids = config.industry_knowledge_bases.map(base => base.id)
    originalAICCModel.value = config.model ?? ''
    aiccConfigHydrated.value = {
      sessionToken: aiccConfigSessionToken.value,
      orgID: config.org_id,
      revision: config.revision,
    }
    aiccConfigDirty.value = false
    isAICCConfigHydrating.value = false
  },
  { immediate: true },
)

// openEditForm 打开编辑模式，将当前组织的资料预填到 editForm。
function openEditForm(org: Organization) {
  // 每次打开或切换企业都启动全新的编辑会话，旧会话的部分成功和确认状态不得复用。
  successfulOrganizationPayloadSnapshot.value = null
  modelChangeConfirmVisible.value = false
  pendingAICCSubmitMode.value = null
  pendingAICCSubmitOrgID.value = null
  aiccConfigSessionToken.value += 1
  aiccConfigHydrated.value = null
  aiccConfigDirty.value = false
  isAICCConfigHydrating.value = false
  aiccConfigSubmitting.value = false
  aiccConfigSubmittingSessionToken.value = null
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
  // AICC 字段等待独立 GET 回填，不能使用组织列表中的兼容字段作为可提交真值。
  editForm.aicc_enabled = false
  editForm.aicc_model = ''
  editForm.aicc_agent_limit = undefined
  editForm.industry_knowledge_base_ids = []
  originalAICCModel.value = ''
  editError.value = null
  aiccConfigError.value = null
  editFormVisible.value = true
}

// closeAnyForm 关闭创建或编辑表单，复位 modalMode。
function closeAnyForm() {
  formVisible.value = false
  editFormVisible.value = false
  editingOrg.value = null
  modelChangeConfirmVisible.value = false
  pendingAICCSubmitMode.value = null
  pendingAICCSubmitOrgID.value = null
  successfulOrganizationPayloadSnapshot.value = null
  aiccConfigSessionToken.value += 1
  aiccConfigHydrated.value = null
  aiccConfigDirty.value = false
  isAICCConfigHydrating.value = false
  aiccConfigSubmitting.value = false
  aiccConfigSubmittingSessionToken.value = null
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

// buildAICCConfigPayload 构造 PUT 所需的完整配置快照，避免部分更新遗漏模型。
function buildAICCConfigPayload(): OrganizationAICCConfigPayload {
  return {
    enabled: editForm.aicc_enabled,
    model: editForm.aicc_model,
    agent_limit: typeof editForm.aicc_agent_limit === 'number' ? editForm.aicc_agent_limit : null,
    industry_knowledge_base_ids: [...editForm.industry_knowledge_base_ids],
  }
}

// buildOrganizationUpdatePayload 捕获当前企业资料的完整 PATCH 快照，异步提交期间不再读取响应式表单。
function buildOrganizationUpdatePayload(): OrganizationUpdatePayload {
  return {
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
    assistant_version_ids: [...editForm.assistant_version_ids],
  }
}

// validateAICCConfig 作为点击与键盘提交的共同兜底，目录或配置不可信时一律拒绝保存。
function validateAICCConfig(): boolean {
  aiccConfigError.value = null
  if (editSubmitting.value || isAICCConfigSubmitting.value) return false
  if (modelsQuery.isError.value) {
    aiccConfigError.value = t('platform.orgs.form.aiccModelLoadFail')
    return false
  }
  if (!isAICCConfigReady.value || modelsQuery.isLoading.value || modelsQuery.isFetching.value) {
    aiccConfigError.value = t('platform.orgs.form.aiccConfigNotReady')
    return false
  }
  if (isSelectedModelUnavailable.value) {
    aiccConfigError.value = t('platform.orgs.form.aiccSelectedModelUnavailable', { model: editForm.aicc_model })
    return false
  }
  if (editForm.aicc_enabled && !editForm.aicc_model) {
    aiccConfigError.value = t('platform.orgs.form.aiccModelRequired')
    return false
  }
  return true
}

// needsModelChangeConfirmation 只要已有模型变化就确认；关闭状态下后端同样会安排 rollout。
function needsModelChangeConfirmation(): boolean {
  return Boolean(originalAICCModel.value)
    && editForm.aicc_model !== originalAICCModel.value
}

// requestAICCSubmit 在需要换模确认时暂存入口，否则直接执行对应写入。
async function requestAICCSubmit(mode: 'config' | 'all') {
  if (editSubmitting.value || isAICCConfigSubmitting.value) return
  if (!validateAICCConfig()) return
  if (needsModelChangeConfirmation()) {
    pendingAICCSubmitMode.value = mode
    pendingAICCSubmitOrgID.value = editingOrgId.value ?? null
    modelChangeConfirmVisible.value = true
    return
  }
  if (mode === 'all') await executeEditOrganization()
  else await executeAICCConfigUpdate()
}

// submitEditOrganization 提交编辑表单；AICC 校验与换模确认通过后才开始任何写操作。
async function submitEditOrganization() {
  await requestAICCSubmit('all')
}

// executeEditOrganization 调用组织 PATCH 与独立 AICC PUT，维持既有主保存按钮行为。
async function executeEditOrganization() {
  if (!editingOrg.value) return
  // 所有请求参数必须在第一个 await 前冻结，避免关闭表单或切换企业后读到另一编辑会话的数据。
  const orgID = editingOrg.value.id
  const organizationPayload = buildOrganizationUpdatePayload()
  const aiccPayload = buildAICCConfigPayload()
  const serializedOrganizationPayload = JSON.stringify(organizationPayload)
  const canReuseSuccessfulPatch = successfulOrganizationPayloadSnapshot.value?.orgID === orgID
    && successfulOrganizationPayloadSnapshot.value.serializedPayload === serializedOrganizationPayload
  editError.value = null
  editSubmitting.value = true
  let organizationUpdated = canReuseSuccessfulPatch
  try {
    if (!canReuseSuccessfulPatch) {
      await updateMutation.mutateAsync({ id: orgID, payload: organizationPayload })
      organizationUpdated = true
      // 仅当前编辑会话仍是原企业时保存部分成功快照，迟到请求不得污染新会话。
      if (editingOrgId.value === orgID) {
        successfulOrganizationPayloadSnapshot.value = { orgID, serializedPayload: serializedOrganizationPayload }
      }
    }
    // 企业编辑页的主保存必须同时提交 AICC 完整快照，避免管理员误以为已保存但配置仍为旧值。
    await updateAICCConfigMutation.mutateAsync({ id: orgID, payload: aiccPayload })
    // 迟到成功只完成原请求，不得关闭管理员随后打开的另一企业表单。
    if (editingOrgId.value === orgID) closeAnyForm()
  } catch (err) {
    const message = err instanceof Error ? err.message : t('platform.orgs.editError')
    // 迟到失败不得把原企业错误展示到新编辑会话。
    if (editingOrgId.value === orgID) {
      editError.value = organizationUpdated
        ? t('platform.orgs.aiccPartialSuccessError', { message })
        : message
    }
  } finally {
    editSubmitting.value = false
  }
}

// submitAICCConfig 独立保存企业 AICC 配置，先执行目录校验与换模确认。
async function submitAICCConfig() {
  await requestAICCSubmit('config')
}

// executeAICCConfigUpdate 发送独立 PUT；成功后的缓存刷新由 hook 统一处理。
async function executeAICCConfigUpdate() {
  if (!editingOrg.value) return
  // 独立保存同样必须在第一个 await 前冻结企业与完整 payload，防止切换会话后串写。
  const orgID = editingOrg.value.id
  const payload = buildAICCConfigPayload()
  const sessionToken = aiccConfigSessionToken.value
  aiccConfigError.value = null
  aiccConfigSubmitting.value = true
  aiccConfigSubmittingSessionToken.value = sessionToken
  try {
    await updateAICCConfigMutation.mutateAsync({ id: orgID, payload })
  } catch (err) {
    // 迟到失败只能写回同一企业、同一编辑会话，避免把旧错误展示给新企业。
    if (editingOrgId.value === orgID && aiccConfigSessionToken.value === sessionToken) {
      aiccConfigError.value = err instanceof Error ? err.message : t('platform.orgs.aiccConfigError')
    }
  } finally {
    // 迟到 finally 不得解除新会话的保存状态。
    if (editingOrgId.value === orgID && aiccConfigSessionToken.value === sessionToken) {
      aiccConfigSubmitting.value = false
      aiccConfigSubmittingSessionToken.value = null
    }
  }
}

// confirmModelChange 按原入口继续提交，确保取消时不会提前写入组织或 AICC 配置。
async function confirmModelChange() {
  const mode = pendingAICCSubmitMode.value
  const orgID = pendingAICCSubmitOrgID.value
  modelChangeConfirmVisible.value = false
  pendingAICCSubmitMode.value = null
  pendingAICCSubmitOrgID.value = null
  // 确认时重新校验企业归属、共同提交锁和最新模型目录，确认窗口内发生的变化必须 fail-closed。
  if (!mode || !orgID || editingOrgId.value !== orgID || !validateAICCConfig()) return
  if (mode === 'all') await executeEditOrganization()
  else if (mode === 'config') await executeAICCConfigUpdate()
}

// cancelModelChange 清空待执行入口，关闭弹框不产生任何 mutation。
function cancelModelChange() {
  modelChangeConfirmVisible.value = false
  pendingAICCSubmitMode.value = null
  pendingAICCSubmitOrgID.value = null
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

// enterAICCConsole 从企业列表进入 AICC 子系统，并把当前企业作为平台管理员初始查看范围。
function enterAICCConsole(org: Organization) {
  void router.push({ path: '/aicc-console', query: { org_id: org.id } })
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
    { label: t('platform.orgs.actions.enterAICC'), type: 'primary', onClick: enterAICCConsole, hidden: r => r.aicc_enabled !== true },
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
