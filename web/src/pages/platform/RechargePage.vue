<template>
  <main class="dashboard-main">
    <section class="panel">
      <DataTableToolbar title="组织充值" eyebrow="Platform · Billing" :subtitle="orgId ? `组织 ${orgId}` : ''">
        <template #actions>
          <RouterLink class="secondary-button" to="/organizations">返回组织列表</RouterLink>
        </template>
      </DataTableToolbar>

      <div v-if="!orgId" class="state-text">URL 缺少组织 ID</div>
      <div v-else>
        <p class="state-text">
          当前余额：
          <strong v-if="balanceQuery.isLoading.value">加载中…</strong>
          <strong v-else-if="balance">
            剩余 {{ balance.remain_quota.toLocaleString() }} ｜ 已用 {{ balance.used_quota.toLocaleString() }}
          </strong>
          <strong v-else class="danger">查询失败</strong>
        </p>

        <form class="form-grid" @submit.prevent="onSubmit">
          <label>
            充值点数（正整数）
            <input v-model.number="amount" type="number" min="1" required />
          </label>
          <label>
            备注（可选）
            <input v-model.trim="remark" type="text" placeholder="业务说明" />
          </label>
          <button class="primary-button" type="submit" :disabled="!canSubmit || mutation.isPending.value">
            {{ mutation.isPending.value ? '充值中…' : '提交充值' }}
          </button>
        </form>

        <p v-if="feedback" class="state-text" :class="{ danger: feedbackError }">{{ feedback }}</p>
      </div>
    </section>

    <section class="panel">
      <h3>充值历史</h3>
      <p v-if="recordsQuery.isLoading.value" class="state-text">加载中…</p>
      <p v-else-if="recordsQuery.error.value" class="state-text danger">查询失败：{{ recordsQuery.error.value?.message }}</p>
      <table v-else>
        <thead>
          <tr>
            <th>时间</th>
            <th>金额</th>
            <th>备注</th>
            <th>状态</th>
            <th>错误</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="record in recordsQuery.data.value ?? []" :key="record.id">
            <td>{{ record.created_at }}</td>
            <td>{{ record.credit_amount.toLocaleString() }}</td>
            <td>{{ record.remark || '—' }}</td>
            <td><span :class="['status-pill', record.status]">{{ record.status }}</span></td>
            <td>{{ record.error_message || '—' }}</td>
          </tr>
          <tr v-if="!recordsQuery.data.value?.length">
            <td colspan="5" class="state-text">暂无充值记录</td>
          </tr>
        </tbody>
      </table>
    </section>
  </main>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { RouterLink, useRoute } from 'vue-router'

import { useOrgBalanceQuery, useRechargeMutation, useRechargesQuery } from '@/api/hooks/useRecharge'
import DataTableToolbar from '@/components/DataTableToolbar.vue'

const route = useRoute()
const orgId = computed<string | undefined>(() => route.params.orgId as string | undefined)

const balanceQuery = useOrgBalanceQuery(orgId)
const balance = computed(() => balanceQuery.data.value ?? null)

const recordsQuery = useRechargesQuery(orgId)
const mutation = useRechargeMutation(orgId)

const amount = ref<number | undefined>()
const remark = ref('')
const feedback = ref('')
const feedbackError = ref(false)

const canSubmit = computed(() => Boolean(orgId.value && (amount.value ?? 0) > 0))

async function onSubmit() {
  feedback.value = ''
  feedbackError.value = false
  try {
    const result = await mutation.mutateAsync({
      credit_amount: amount.value ?? 0,
      remark: remark.value || undefined,
    })
    feedback.value = `已充值 ${result.credit_amount} 点（${result.status}）`
    amount.value = undefined
    remark.value = ''
  } catch (err: unknown) {
    feedbackError.value = true
    feedback.value = err instanceof Error ? err.message : '充值失败'
  }
}
</script>

<style scoped>
.form-grid {
  display: grid;
  grid-template-columns: 1fr 2fr auto;
  gap: 12px;
  align-items: end;
  margin-top: 16px;
}

.form-grid label {
  display: flex;
  flex-direction: column;
  gap: 4px;
  font-size: 13px;
}

.form-grid input {
  padding: 8px 10px;
  border: 1px solid rgba(0, 0, 0, 0.15);
  border-radius: 6px;
}

.status-pill.succeeded {
  background: rgba(34, 197, 94, 0.15);
  color: rgb(22, 101, 52);
  padding: 2px 10px;
  border-radius: 999px;
  font-size: 12px;
}

.status-pill.failed {
  background: rgba(220, 38, 38, 0.15);
  color: rgb(127, 29, 29);
  padding: 2px 10px;
  border-radius: 999px;
  font-size: 12px;
}

.danger {
  color: rgba(220, 38, 38, 1);
}
</style>
