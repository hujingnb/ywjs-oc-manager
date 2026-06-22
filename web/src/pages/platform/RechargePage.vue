<template>
  <div style="display: grid; gap: 18px">
    <!-- 充值表单 -->
    <n-card :bordered="true">
      <template #header>
        <div style="display: flex; align-items: center; justify-content: space-between">
          <div>
            <p class="eyebrow">Platform · Billing</p>
            <h2 style="margin: 0">{{ t('platform.recharge.heading') }}</h2>
            <p v-if="orgId" class="state-text" style="margin: 4px 0 0">{{ t('platform.recharge.orgIdLabel', { id: orgId }) }}</p>
          </div>
          <RouterLink class="secondary-button" to="/organizations">{{ t('platform.recharge.backToOrgs') }}</RouterLink>
        </div>
      </template>

      <div v-if="!orgId" class="state-text">{{ t('platform.recharge.missingOrgId') }}</div>
      <div v-else>
        <p class="state-text" style="margin-bottom: 12px">
          {{ t('platform.recharge.currentBalance') }}
          <strong v-if="balanceQuery.isLoading.value">{{ t('platform.recharge.balanceLoading') }}</strong>
          <strong v-else-if="balance">
            {{ t('platform.recharge.remain', { remain: formatQuotaValue(balance.remain_quota, billingStatus) }) }} ｜ {{ t('platform.recharge.used', { used: formatQuotaValue(balance.used_quota, billingStatus) }) }}
          </strong>
          <strong v-else class="danger">{{ t('platform.recharge.balanceFail') }}</strong>
        </p>

        <n-form label-placement="top" @submit.prevent="onSubmit">
          <n-grid :cols="3" :x-gap="14">
            <n-grid-item>
              <n-form-item :label="t('platform.recharge.labelAmount')">
                <n-input-number v-model:value="amount" :min="1" :precision="0" style="width: 100%" :placeholder="t('platform.recharge.placeholderAmount')" />
              </n-form-item>
            </n-grid-item>
            <n-grid-item>
              <n-form-item :label="t('platform.recharge.labelRemark')">
                <n-input v-model:value="remark" :placeholder="t('platform.recharge.placeholderRemark')" />
              </n-form-item>
            </n-grid-item>
            <n-grid-item style="display: flex; align-items: flex-end; padding-bottom: 24px">
              <n-button
                type="primary"
                attr-type="submit"
                :disabled="!canSubmit || mutation.isPending.value"
                style="width: 100%"
              >
                {{ mutation.isPending.value ? t('platform.recharge.submitPending') : t('platform.recharge.submitButton') }}
              </n-button>
            </n-grid-item>
          </n-grid>
        </n-form>

        <p v-if="feedback" class="state-text" :class="{ danger: feedbackError }">{{ feedback }}</p>
      </div>
    </n-card>

    <ConfirmActionModal
      :visible="confirmRecharge"
      :title="t('platform.recharge.confirmTitle')"
      :message="pendingPayload ? t('platform.recharge.confirmMessage', { amount: formatDisplayAmount(pendingPayload.credit_amount, billingStatus) }) : ''"
      :confirm-label="t('platform.recharge.confirmLabel')"
      :busy="mutation.isPending.value"
      :verify-value="orgName"
      :verify-hint="confirmHint"
      @confirm="onConfirmRecharge"
      @cancel="onCancelRecharge"
    />

    <!-- 充值历史 -->
    <n-card :bordered="true">
      <template #header>
        <h2 style="margin: 0">{{ t('platform.recharge.historyTitle') }}</h2>
      </template>

      <div v-if="recordsQuery.isLoading.value" class="state-text">{{ t('platform.recharge.historyLoading') }}</div>
      <div v-else-if="recordsQuery.error.value" class="state-text danger">{{ t('platform.recharge.historyError', { msg: recordsQuery.error.value?.message }) }}</div>
      <n-data-table
        v-else
        :columns="historyColumns"
        :data="recordsQuery.data.value ?? []"
        size="small"
        :bordered="false"
        :row-key="(row) => row.id"
      />
    </n-card>
  </div>
</template>

<script setup lang="ts">
import { computed, h, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink, useRoute } from 'vue-router'
import { NButton, NCard, NDataTable, NForm, NFormItem, NGrid, NGridItem, NInput, NInputNumber, NTag, type DataTableColumns } from 'naive-ui'

import { useBillingStatusQuery, useOrgBalanceQuery, useRechargeMutation, useRechargesQuery, type RechargeRecordDTO } from '@/api/hooks/useRecharge'
import { useOrganizationQuery } from '@/api/hooks/useOrganizations'
import ConfirmActionModal from '@/components/ConfirmActionModal.vue'
import { formatDisplayAmount, formatQuotaValue } from '@/pages/usage/usageFormatting'

// RechargePage 是独立组织充值页，保留余额查询、充值确认和历史记录展示。
const { t } = useI18n()
const route = useRoute()
// orgId 来自路由参数，缺失时页面展示 URL 错误且相关查询不会具备有效目标。
const orgId = computed<string | undefined>(() => route.params.orgId as string | undefined)

const balanceQuery = useOrgBalanceQuery(orgId)
const balance = computed(() => balanceQuery.data.value ?? null)
const { data: billingStatus } = useBillingStatusQuery()

const recordsQuery = useRechargesQuery(orgId)
const mutation = useRechargeMutation(orgId)

const orgQuery = useOrganizationQuery(orgId)
// orgName 用于二次确认输入，组织尚未加载时降级为组织 ID。
const orgName = computed(() => orgQuery.data.value?.name ?? (orgId.value ? t('platform.recharge.orgNameFallback', { id: orgId.value }) : ''))
const confirmHint = computed(() => t('platform.recharge.confirmHint', { name: orgName.value }))

const amount = ref<number | null>(null)
const remark = ref('')
const feedback = ref('')
const feedbackError = ref(false)

const confirmRecharge = ref(false)
// pendingPayload 暂存通过基础校验的充值请求，只有二次确认后才提交。
const pendingPayload = ref<{ credit_amount: number; remark?: string } | null>(null)

// canSubmit 表示金额和组织 ID 都满足提交充值的最小条件。
const canSubmit = computed(() => Boolean(orgId.value && (amount.value ?? 0) > 0))

// onSubmit 只打开二次确认弹框，不直接调用充值接口。
function onSubmit() {
  if (!canSubmit.value) return
  pendingPayload.value = {
    credit_amount: amount.value ?? 0,
    remark: remark.value || undefined,
  }
  confirmRecharge.value = true
}

// onConfirmRecharge 调用充值 mutation；成功后清空表单，失败时保留输入并展示错误。
async function onConfirmRecharge() {
  if (!pendingPayload.value) return
  feedback.value = ''
  feedbackError.value = false
  confirmRecharge.value = false
  try {
    const result = await mutation.mutateAsync(pendingPayload.value)
    feedback.value = t('platform.recharge.successMsg', { amount: formatDisplayAmount(result.credit_amount, billingStatus.value), status: result.status })
    amount.value = null
    remark.value = ''
  } catch (err: unknown) {
    feedbackError.value = true
    feedback.value = err instanceof Error ? err.message : t('platform.recharge.failMsg')
  } finally {
    pendingPayload.value = null
  }
}

// onCancelRecharge 放弃本次待确认请求，避免下一次确认误用旧金额。
function onCancelRecharge() {
  confirmRecharge.value = false
  pendingPayload.value = null
}

// historyColumns 展示充值历史，状态列用标签色突出成功和失败记录。
const historyColumns = computed<DataTableColumns<RechargeRecordDTO>>(() => [
  { title: t('platform.recharge.columns.time'), key: 'created_at' },
  { title: t('platform.recharge.columns.amount'), key: 'credit_amount', render: (row) => formatDisplayAmount(row.credit_amount, billingStatus.value) },
  { title: t('platform.recharge.columns.remark'), key: 'remark', render: (row) => row.remark || '—' },
  {
    title: t('platform.recharge.columns.status'), key: 'status',
    render: (row) => h(NTag, {
      type: row.status === 'succeeded' ? 'success' : 'error',
      size: 'small',
      bordered: false,
    }, { default: () => row.status }),
  },
  { title: t('platform.recharge.columns.error'), key: 'error_message', render: (row) => row.error_message || '—' },
])
</script>
