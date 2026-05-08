import { useQuery } from '@tanstack/vue-query'
import type { Ref } from 'vue'

import { apiRequest } from '@/api/client'

// AggregatedUsage 是 v1.0.2 new-api 直连改造后的薄代理响应。
// 后端 service.LogsPage / QuotaSeries 共享同一前端类型：
//   - app / member 维度（GetAppUsage / GetMemberUsage）：items 是 newapi.LogEntry 数组
//   - org / platform 维度（GetOrgUsage / GetPlatformUsage）：items 是 newapi.QuotaDate 数组
// total 仅在 LogsPage 出现（代表 newapi 分页总数）。
//
// 旧字段 total_remain_quota / apps 在改造后已废弃；UI 不再聚合余额，每条 item 自带
// quota/usage 数据，由展示层按维度区分渲染。
export interface AggregatedUsage {
  scope: 'organization' | 'member' | 'platform' | 'app'
  scope_id?: string
  items: Record<string, unknown>[]
  total?: number
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
