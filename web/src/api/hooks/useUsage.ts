import { useQuery } from '@tanstack/vue-query'
import type { Ref } from 'vue'

import { apiRequest } from '@/api/client'

// AppUsageSnapshot 与后端 service.AppUsageSnapshot 字段一一对应。
export interface AppUsageSnapshot {
  app_id: string
  newapi_key_id: number
  remain_quota: number
  status: number
  updated_at: string
}

// AggregatedUsage 与后端 service.AggregatedUsage 字段一一对应。
export interface AggregatedUsage {
  scope: 'organization' | 'member' | 'platform'
  scope_id?: string
  total_remain_quota: number
  apps?: AppUsageSnapshot[]
  updated_at: string
}

// useOrgUsageQuery 拉某组织维度的用量聚合。
// platform_admin 可传任意 orgId 切换组织视角。
export function useOrgUsageQuery(orgId: Ref<string | undefined>) {
  return useQuery<AggregatedUsage | null>({
    queryKey: ['usage', 'org', orgId],
    enabled: () => Boolean(orgId.value),
    refetchInterval: 8000,
    queryFn: async () => {
      if (!orgId.value) return null
      const resp = await apiRequest<{ usage: AggregatedUsage }>(
        `/api/v1/usage/organizations/${orgId.value}`,
      )
      return resp.usage
    },
  })
}

// useMemberUsageQuery 拉指定成员名下应用聚合。
export function useMemberUsageQuery(orgId: Ref<string | undefined>, memberId: Ref<string | undefined>) {
  return useQuery<AggregatedUsage | null>({
    queryKey: ['usage', 'member', orgId, memberId],
    enabled: () => Boolean(orgId.value && memberId.value),
    refetchInterval: 8000,
    queryFn: async () => {
      if (!orgId.value || !memberId.value) return null
      const resp = await apiRequest<{ usage: AggregatedUsage }>(
        `/api/v1/usage/members/${memberId.value}`,
        { query: { org_id: orgId.value } },
      )
      return resp.usage
    },
  })
}

// usePlatformUsageQuery 拉平台维度（跨所有组织）聚合，仅 platform_admin 可见。
export function usePlatformUsageQuery(enabled: Ref<boolean>) {
  return useQuery<AggregatedUsage | null>({
    queryKey: ['usage', 'platform'],
    enabled: () => enabled.value,
    refetchInterval: 10000,
    queryFn: async () => {
      const resp = await apiRequest<{ usage: AggregatedUsage }>(`/api/v1/usage/platform`)
      return resp.usage
    },
  })
}
