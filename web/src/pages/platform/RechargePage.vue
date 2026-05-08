<template>
  <div style="display: grid; gap: 18px">
    <!-- 充值表单 -->
    <n-card :bordered="true">
      <template #header>
        <div style="display: flex; align-items: center; justify-content: space-between">
          <div>
            <p class="eyebrow">Platform · Billing</p>
            <h2 style="margin: 0">组织充值</h2>
            <p v-if="orgId" class="state-text" style="margin: 4px 0 0">组织 {{ orgId }}</p>
          </div>
          <RouterLink class="secondary-button" to="/organizations">返回组织列表</RouterLink>
        </div>
      </template>

      <div v-if="!orgId" class="state-text">URL 缺少组织 ID</div>
      <div v-else>
        <p class="state-text" style="margin-bottom: 12px">
          当前余额：
          <strong v-if="balanceQuery.isLoading.value">加载中…</strong>
          <strong v-else-if="balance">
            剩余 {{ balance.remain_quota.toLocaleString() }} ｜ 已用 {{ balance.used_quota.toLocaleString() }}
          </strong>
          <strong v-else class="danger">查询失败</strong>
        </p>

        <n-form label-placement="top" @submit.prevent="onSubmit">
          <n-grid :cols="3" :x-gap="14">
            <n-grid-item>
              <n-form-item label="充值点数（正整数）">
                <n-input-number v-model:value="amount" :min="1" style="width: 100%" placeholder="输入点数" />
              </n-form-item>
            </n-grid-item>
            <n-grid-item>
              <n-form-item label="备注（可选）">
                <n-input v-model:value="remark" placeholder="业务说明" />
              </n-form-item>
            </n-grid-item>
            <n-grid-item style="display: flex; align-items: flex-end; padding-bottom: 24px">
              <n-button
                type="primary"
                attr-type="submit"
                :disabled="!canSubmit || mutation.isPending.value"
                style="width: 100%"
              >
                {{ mutation.isPending.value ? '充值中…' : '提交充值' }}
              </n-button>
            </n-grid-item>
          </n-grid>
        </n-form>

        <p v-if="feedback" class="state-text" :class="{ danger: feedbackError }">{{ feedback }}</p>
      </div>
    </n-card>

    <ConfirmActionModal
      :visible="confirmRecharge"
      title="确认组织充值"
      :message="pendingPayload ? `将给当前组织充值 ${pendingPayload.credit_amount} Token Credit。该操作会调用 new-api 修改余额。` : ''"
      confirm-label="确认充值"
      :busy="mutation.isPending.value"
      :verify-value="orgName"
      :verify-hint="confirmHint"
      @confirm="onConfirmRecharge"
      @cancel="onCancelRecharge"
    />

    <!-- 充值历史 -->
    <n-card :bordered="true">
      <template #header>
        <h2 style="margin: 0">充值历史</h2>
      </template>

      <div v-if="recordsQuery.isLoading.value" class="state-text">加载中…</div>
      <div v-else-if="recordsQuery.error.value" class="state-text danger">查询失败：{{ recordsQuery.error.value?.message }}</div>
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
import { RouterLink, useRoute } from 'vue-router'
import { NButton, NCard, NDataTable, NForm, NFormItem, NGrid, NGridItem, NInput, NInputNumber, NTag, type DataTableColumns } from 'naive-ui'

import { useOrgBalanceQuery, useRechargeMutation, useRechargesQuery, type RechargeRecordDTO } from '@/api/hooks/useRecharge'
import { useOrganizationQuery } from '@/api/hooks/useOrganizations'
import ConfirmActionModal from '@/components/ConfirmActionModal.vue'

const route = useRoute()
const orgId = computed<string | undefined>(() => route.params.orgId as string | undefined)

const balanceQuery = useOrgBalanceQuery(orgId)
const balance = computed(() => balanceQuery.data.value ?? null)

const recordsQuery = useRechargesQuery(orgId)
const mutation = useRechargeMutation(orgId)

const orgQuery = useOrganizationQuery(orgId)
const orgName = computed(() => orgQuery.data.value?.name ?? (orgId.value ? `组织 ${orgId.value}` : ''))
const confirmHint = computed(() => `输入组织名称 "${orgName.value}" 以确认充值`)

const amount = ref<number | null>(null)
const remark = ref('')
const feedback = ref('')
const feedbackError = ref(false)

const confirmRecharge = ref(false)
const pendingPayload = ref<{ credit_amount: number; remark?: string } | null>(null)

const canSubmit = computed(() => Boolean(orgId.value && (amount.value ?? 0) > 0))

function onSubmit() {
  if (!canSubmit.value) return
  pendingPayload.value = {
    credit_amount: amount.value ?? 0,
    remark: remark.value || undefined,
  }
  confirmRecharge.value = true
}

async function onConfirmRecharge() {
  if (!pendingPayload.value) return
  feedback.value = ''
  feedbackError.value = false
  confirmRecharge.value = false
  try {
    const result = await mutation.mutateAsync(pendingPayload.value)
    feedback.value = `已充值 ${result.credit_amount} 点（${result.status}）`
    amount.value = null
    remark.value = ''
  } catch (err: unknown) {
    feedbackError.value = true
    feedback.value = err instanceof Error ? err.message : '充值失败'
  } finally {
    pendingPayload.value = null
  }
}

function onCancelRecharge() {
  confirmRecharge.value = false
  pendingPayload.value = null
}

const historyColumns: DataTableColumns<RechargeRecordDTO> = [
  { title: '时间', key: 'created_at' },
  { title: '金额', key: 'credit_amount', render: (row) => row.credit_amount.toLocaleString() },
  { title: '备注', key: 'remark', render: (row) => row.remark || '—' },
  {
    title: '状态', key: 'status',
    render: (row) => h(NTag, {
      type: row.status === 'succeeded' ? 'success' : 'error',
      size: 'small',
      bordered: false,
    }, { default: () => row.status }),
  },
  { title: '错误', key: 'error_message', render: (row) => row.error_message || '—' },
]
</script>
