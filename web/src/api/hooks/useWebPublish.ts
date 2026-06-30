// web-publish 相关数据 hooks，覆盖企业站点列表、web-publish 配置查询
// 以及站点下线、续期和证书重试三个写操作。
// 查询 key 设计：
//   - ['web-publish-sites', orgId]  → 站点列表
//   - ['web-publish-config', orgId] → 企业 web-publish 配置/证书状态
import { useMutation, useQuery, useQueryClient } from '@tanstack/vue-query'
import type { Ref } from 'vue'

import { apiRequest } from '@/api/client'
import type { SiteResult, WebPublishConfigResult } from '@/api'

// webPublishSitesKey 构造站点列表的 query key。
// 设计为函数而非常量，使不同 orgId 产生独立缓存条目。
function webPublishSitesKey(orgId: string) {
  return ['web-publish-sites', orgId] as const
}

// webPublishConfigKey 构造 web-publish 配置的 query key。
function webPublishConfigKey(orgId: string) {
  return ['web-publish-config', orgId] as const
}

// usePublishedSitesQuery 查询指定企业下的所有已发布站点（active/disabled/expired）。
// orgId 为响应式引用，未填写时 query 暂停执行；后端需 CanViewOrg 权限。
export function usePublishedSitesQuery(orgId: Ref<string | undefined>) {
  return useQuery<SiteResult[]>({
    queryKey: ['web-publish-sites', orgId],
    enabled: () => Boolean(orgId.value),
    queryFn: async () => {
      if (!orgId.value) return []
      const response = await apiRequest<{ sites: SiteResult[] }>(
        `/api/v1/organizations/${orgId.value}/published-sites`,
      )
      return response.sites ?? []
    },
  })
}

// useWebPublishConfigQuery 查询指定企业的 web-publish 配置及证书状态。
// 凭证密文不出现在响应中；后端需 CanViewOrg 权限。
// orgId 为响应式引用，未填写时 query 暂停执行。
export function useWebPublishConfigQuery(orgId: Ref<string | undefined>) {
  return useQuery<WebPublishConfigResult | null>({
    queryKey: ['web-publish-config', orgId],
    enabled: () => Boolean(orgId.value),
    queryFn: async () => {
      if (!orgId.value) return null
      return await apiRequest<WebPublishConfigResult>(
        `/api/v1/organizations/${orgId.value}/web-publish`,
      )
    },
  })
}

// useTakedownSite 将站点状态置为 disabled 并删除整站对象存储前缀。
// 成功后失效所属企业的站点列表缓存，列表页会自动重新拉取。
// 后端需 CanManageOrg 权限。
export function useTakedownSite(orgId: Ref<string | undefined>) {
  const client = useQueryClient()
  return useMutation({
    mutationFn: async (siteId: string) => {
      await apiRequest<void>(`/api/v1/published-sites/${siteId}/disable`, {
        method: 'POST',
      })
    },
    onSuccess: () => {
      if (orgId.value) {
        void client.invalidateQueries({ queryKey: webPublishSitesKey(orgId.value) })
      }
    },
  })
}

// useRenewSite 按企业 site_ttl_days 延后站点过期时间并将状态置回 active。
// 成功后失效所属企业的站点列表缓存。后端需 CanManageOrg 权限。
export function useRenewSite(orgId: Ref<string | undefined>) {
  const client = useQueryClient()
  return useMutation({
    mutationFn: async (siteId: string) => {
      await apiRequest<void>(`/api/v1/published-sites/${siteId}/renew`, {
        method: 'POST',
      })
    },
    onSuccess: () => {
      if (orgId.value) {
        void client.invalidateQueries({ queryKey: webPublishSitesKey(orgId.value) })
      }
    },
  })
}

// useRetryCert 触发平台管理员手动重试 web-publish provisioning job。
// 适用于证书签发/续签失败场景；成功后失效企业 web-publish 配置缓存，
// 使证书状态在配置面板中及时刷新。后端仅平台管理员可调用。
export function useRetryCert(orgId: Ref<string | undefined>) {
  const client = useQueryClient()
  return useMutation({
    mutationFn: async () => {
      if (!orgId.value) throw new Error('orgId is required')
      await apiRequest<void>(
        `/api/v1/platform/organizations/${orgId.value}/web-publish/cert/retry`,
        { method: 'POST' },
      )
    },
    onSuccess: () => {
      if (orgId.value) {
        void client.invalidateQueries({ queryKey: webPublishConfigKey(orgId.value) })
      }
    },
  })
}
