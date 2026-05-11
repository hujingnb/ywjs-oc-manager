// 用量 API hooks 负责组织、成员和平台维度的 new-api 用量薄代理查询。
// 各 query 使用轮询刷新，页面层根据 scope 决定 items 的具体展示方式。
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
  // 聚合维度，决定 items 的实际结构和页面表格列。
  scope: 'organization' | 'member' | 'platform' | 'app'
  // 当前维度目标 ID；平台维度可能为空。
  scope_id?: string
  // 后端透传的用量明细，具体字段由 scope 决定。
  items: Record<string, unknown>[]
  // new-api 分页总数，仅日志分页响应包含。
  total?: number
  // 后端生成聚合结果的时间。
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
// orgId 作为查询参数参与权限边界，memberId 是路径资源。
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
// enabled=false 时不发请求，避免普通组织角色进入页面时产生 403。
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
