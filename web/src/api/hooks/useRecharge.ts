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

// BalanceDTO 是组织在 new-api 中的余额快照。
export interface BalanceDTO {
  // new-api 用户 ID。
  newapi_user_id: number
  // 剩余额度。
  remain_quota: number
  // 已用额度。
  used_quota: number
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

// useRechargeMutation 触发充值。
// mutation 成功后刷新充值流水和余额；失败由调用方展示 error.message。
export function useRechargeMutation(orgId: Ref<string | undefined>) {
  const client = useQueryClient()
  return useMutation({
    mutationFn: async (input: { credit_amount: number; remark?: string }) => {
      if (!orgId.value) throw new Error('缺少组织 ID')
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
