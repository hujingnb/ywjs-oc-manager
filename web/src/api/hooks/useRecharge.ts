import { useMutation, useQuery, useQueryClient } from '@tanstack/vue-query'
import type { Ref } from 'vue'

import { apiRequest } from '@/api/client'

export interface RechargeRecordDTO {
  id: string
  org_id: string
  operator_id?: string
  credit_amount: number
  remark?: string
  newapi_ref_id?: string
  status: 'succeeded' | 'failed'
  error_message?: string
  created_at: string
}

export interface BalanceDTO {
  newapi_user_id: number
  remain_quota: number
  used_quota: number
}

const recordsKey = (orgId: string | undefined) => ['recharges', orgId] as const
const balanceKey = (orgId: string | undefined) => ['org-balance', orgId] as const

// useRechargesQuery 列出组织充值记录。
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
