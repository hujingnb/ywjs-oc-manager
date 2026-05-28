// 充值 API hooks 负责组织充值记录、余额查询和充值提交。
// 充值成功后同时刷新记录与余额，避免列表和余额卡片短暂不一致。
import { useMutation, useQuery, useQueryClient } from '@tanstack/vue-query'
import type { Ref } from 'vue'

import { apiRequest } from '@/api/client'

// RechargeRecordDTO 是组织充值流水。
export interface RechargeRecordDTO {
  // 充值记录 ID。
  id: string
  // 被充值组织 ID。
  org_id: string
  // 操作人 ID，系统补偿或历史记录可能为空。
  operator_id?: string
  // 充值额度，单位与后端 new-api 额度保持一致。
  credit_amount: number
  // 操作备注。
  remark?: string
  // new-api 侧引用 ID，用于跨系统排查。
  newapi_ref_id?: string
  // 充值结果状态。
  status: 'succeeded' | 'failed'
  // 失败原因。
  error_message?: string
  // 创建时间。
  created_at: string
}

// BalanceDTO 是组织在 new-api 中的余额快照，附带本地聚合的累计充值金额。
export interface BalanceDTO {
  // new-api 用户 ID。
  newapi_user_id: number
  // 剩余额度（实时从 new-api 查询）。
  remain_quota: number
  // 已用额度。
  used_quota: number
  // 累计充值金额（来自 manager recharge_records 聚合，仅计 succeeded 记录）。
  total_recharged: number
}

// BillingStatusDTO 是 new-api 的计费展示配置，manager 只负责透传展示，不管理单价。
export interface BillingStatusDTO {
  // quota_per_unit 表示多少 raw quota 折算为一个展示单位。
  quota_per_unit: number
  // quota_display_type 是 new-api 配置的展示单位，例如 USD。
  quota_display_type?: string
  // display_in_currency 标记 new-api 是否按金额展示。
  display_in_currency?: boolean
  // custom_currency_symbol 是 new-api 自定义货币符号。
  custom_currency_symbol?: string
  // custom_currency_exchange_rate 由 new-api 管理，本端不参与计算。
  custom_currency_exchange_rate?: number
  // usd_exchange_rate 由 new-api 管理，本端不参与计算。
  usd_exchange_rate?: number
  // price 由 new-api 管理，本端只透传。
  price?: number
}

const recordsKey = (orgId: string | undefined) => ['recharges', orgId] as const
const balanceKey = (orgId: string | undefined) => ['org-balance', orgId] as const

// useRechargesQuery 列出组织充值记录。
// orgId 为空时暂停；充值记录缓存按组织隔离。
export function useRechargesQuery(orgId: Ref<string | undefined>) {
  return useQuery<RechargeRecordDTO[]>({
    queryKey: ['recharges', orgId],
    enabled: () => Boolean(orgId.value),
    queryFn: async () => {
      if (!orgId.value) return []
      const response = await apiRequest<{ recharges?: RechargeRecordDTO[] }>(
        `/api/v1/organizations/${orgId.value}/recharges`,
      )
      return response.recharges ?? []
    },
  })
}

// useOrgBalanceQuery 查询组织当前余额。
// 余额来自 new-api 薄代理；orgId 缺失时 query 被禁用，data 通常为 undefined，除非已有缓存。
export function useOrgBalanceQuery(orgId: Ref<string | undefined>) {
  return useQuery<BalanceDTO | null>({
    queryKey: ['org-balance', orgId],
    enabled: () => Boolean(orgId.value),
    queryFn: async () => {
      if (!orgId.value) return null
      const response = await apiRequest<{ balance: BalanceDTO }>(
        `/api/v1/organizations/${orgId.value}/balance`,
      )
      return response.balance
    },
  })
}

// useBillingStatusQuery 查询 new-api 当前计费展示配置。
// 该接口不包含 manager 自定义单价，所有金额换算只按 new-api 返回配置显示。
export function useBillingStatusQuery() {
  return useQuery<BillingStatusDTO | null>({
    queryKey: ['billing-status'],
    queryFn: async () => {
      const response = await apiRequest<{ billing_status: BillingStatusDTO }>(
        `/api/v1/billing/status`,
      )
      return response.billing_status
    },
  })
}

// useRechargeMutation 触发充值。
// mutation 成功后刷新充值流水和余额；失败由调用方展示 error.message。
export function useRechargeMutation(orgId: Ref<string | undefined>) {
  const client = useQueryClient()
  return useMutation({
    mutationFn: async (input: { credit_amount: number; remark?: string }) => {
      if (!orgId.value) throw new Error('缺少企业 ID')
      const response = await apiRequest<{ recharge: RechargeRecordDTO }>(
        `/api/v1/organizations/${orgId.value}/recharge`,
        { method: 'POST', body: input },
      )
      return response.recharge
    },
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: recordsKey(orgId.value) })
      void client.invalidateQueries({ queryKey: balanceKey(orgId.value) })
    },
  })
}
