import { useQuery } from '@tanstack/vue-query'
import type { Ref } from 'vue'

import { apiRequest } from '@/api/client'

// PlatformOverview 与后端 service.PlatformOverview 字段一一对应。
export interface PlatformOverview {
  organization_count: number
  member_count: number
  app_count: number
  running_app_count: number
  error_app_count: number
  total_remain_quota: number
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
