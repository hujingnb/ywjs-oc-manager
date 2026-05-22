// 平台总览 API hook 负责平台管理员首页的聚合指标轮询。
// 本文件不做角色判断；调用方通过 enabled 控制非平台角色不发请求。
import { useQuery } from '@tanstack/vue-query'
import type { Ref } from 'vue'

import { apiRequest } from '@/api/client'

// PlatformOverview 与后端 service.PlatformOverview 字段一一对应。
export interface PlatformOverview {
  // 组织总数。
  organization_count: number
  // 成员总数。
  member_count: number
  // 应用总数。
  app_count: number
  // 当前运行中的应用数量。
  running_app_count: number
  // 当前异常应用数量。
  error_app_count: number
  // new-api 余额汇总。
  total_remain_quota: number
  // false 表示用量系统不可用，页面应展示降级提示。
  usage_available: boolean
}

// usePlatformOverviewQuery 拉平台总览，仅 platform_admin 可调；非平台管理员后端会 403。
// 10 秒轮询，让运行容器数 / 异常应用数变化能在 UI 自然刷新。
export function usePlatformOverviewQuery(enabled: Ref<boolean>) {
  return useQuery<PlatformOverview | null>({
    queryKey: ['platform', 'overview'],
    enabled: () => enabled.value,
    refetchInterval: 10000,
    queryFn: async () => {
      const resp = await apiRequest<{ overview: PlatformOverview }>('/api/v1/platform/overview')
      return resp.overview
    },
  })
}

// OrgUsageBreakdownItem 与后端 service.OrgUsageItem 字段一一对应。
export interface OrgUsageBreakdownItem {
  // 组织 UUID。
  org_id: string
  // 组织显示名。
  org_name: string
  // [since, until] 内各日 quota 累计值（new-api 原始单位）。
  total_quota: number
}

// usePlatformOrgBreakdownQuery 拉各组织近 7 天 quota 消耗汇总，仅 platform_admin 可调。
// 60 秒刷新，图表数据变化频率低于统计卡片。
export function usePlatformOrgBreakdownQuery(enabled: Ref<boolean>) {
  return useQuery<OrgUsageBreakdownItem[]>({
    queryKey: ['platform', 'usage', 'org-breakdown'],
    enabled: () => enabled.value,
    refetchInterval: 60000,
    queryFn: async () => {
      const now = Math.floor(Date.now() / 1000)
      const since = now - 7 * 24 * 60 * 60
      const resp = await apiRequest<{ breakdown: { items: OrgUsageBreakdownItem[] } }>(
        '/api/v1/platform/usage/org-breakdown',
        { query: { since: String(since), until: String(now) } },
      )
      return resp.breakdown.items ?? []
    },
  })
}
