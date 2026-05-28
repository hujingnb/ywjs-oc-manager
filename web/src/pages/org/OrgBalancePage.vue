<template>
  <!-- 账户余额页：org_admin 查看自己企业的余额和充值流水，只读，无充值入口。 -->
  <n-card :bordered="true">
    <template #header>
      <div>
        <p class="eyebrow">Billing · 账户余额</p>
        <h2 style="margin: 0">账户余额</h2>
      </div>
    </template>

    <!-- 概况卡片：累计充值金额和当前剩余金额。 -->
    <n-grid :cols="2" :x-gap="14" style="margin-bottom: 24px">
      <n-grid-item>
        <n-statistic label="累计充值金额">
          <!-- 加载中展示占位符，失败时提示用户。 -->
          <template v-if="balanceQuery.isLoading.value">—</template>
          <template v-else-if="balance">
            {{ formatDisplayAmount(balance.total_recharged, billingStatus) }}
          </template>
          <template v-else>查询失败</template>
        </n-statistic>
      </n-grid-item>
      <n-grid-item>
        <n-statistic label="当前剩余金额">
          <template v-if="balanceQuery.isLoading.value">—</template>
          <template v-else-if="balance">
            {{ formatQuotaValue(balance.remain_quota, billingStatus) }}
          </template>
          <template v-else>查询失败</template>
        </n-statistic>
      </n-grid-item>
    </n-grid>

    <!-- 充值记录列表：加载中/失败态/表格三态。 -->
    <div v-if="rechargesLoading" class="state-text">加载中…</div>
    <div v-else-if="rechargesError" class="state-text danger">加载失败，请刷新重试</div>
    <n-data-table
      v-else
      size="small"
      :columns="columns"
      :data="rechargeRecords ?? []"
      :pagination="{ pageSize: 15 }"
    />
  </n-card>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { NCard, NDataTable, NGrid, NGridItem, NStatistic } from 'naive-ui'

import { useBillingStatusQuery, useOrgBalanceQuery, useRechargesQuery } from '@/api/hooks/useRecharge'
import type { RechargeRecordDTO } from '@/api/hooks/useRecharge'
import { useAuthStore } from '@/stores/auth'
import { formatDisplayAmount, formatQuotaValue } from '@/pages/usage/usageFormatting'

// OrgBalancePage 是 org_admin 的账户余额页，展示自己企业的累计充值和当前余额，不提供充值入口。
const auth = useAuthStore()

// orgId 来自当前登录用户；org_admin 的 org_id 必然存在，缺失时 query 自动禁用。
const orgId = computed(() => auth.user?.org_id)

// balanceQuery 查询当前企业余额，orgId 为空时自动禁用。
const balanceQuery = useOrgBalanceQuery(orgId)
// balance 是余额快照，含累计充值和剩余额度。
const balance = computed(() => balanceQuery.data.value ?? null)

// billingStatus 提供 new-api 的计费展示配置（货币单位、换算比例等），用于格式化金额。
const { data: billingStatus } = useBillingStatusQuery()

// rechargesQuery 列出该企业的充值流水，orgId 为空时自动禁用。
const {
  data: rechargeRecords,
  isLoading: rechargesLoading,
  error: rechargesError,
} = useRechargesQuery(orgId)

// columns 定义充值记录表格列；org_admin 无需关注操作人，故不展示操作人列。
const columns = [
  {
    // 时间列：将 ISO 时间戳截取到秒精度，去掉 T 分隔符方便阅读。
    title: '时间',
    key: 'created_at',
    render: (r: RechargeRecordDTO) => r.created_at.replace('T', ' ').slice(0, 19),
  },
  {
    // 金额列：按 new-api 计费配置格式化展示，与余额卡片保持一致。
    title: '金额',
    key: 'credit_amount',
    render: (r: RechargeRecordDTO) => formatDisplayAmount(r.credit_amount, billingStatus.value),
  },
  {
    // 备注列：无备注时展示破折号占位。
    title: '备注',
    key: 'remark',
    render: (r: RechargeRecordDTO) => r.remark || '—',
  },
  {
    // 状态列：只有 succeeded/failed 两种结果，转为中文展示。
    title: '状态',
    key: 'status',
    render: (r: RechargeRecordDTO) => r.status === 'succeeded' ? '成功' : '失败',
  },
]
</script>
