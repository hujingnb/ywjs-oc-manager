// web-publish 相关数据 hooks，覆盖企业站点列表、web-publish 配置查询
// 以及站点下线、续期、证书重试和平台管理员写操作（配置/开通/停用）。
// 查询 key 设计：
//   - ['web-publish-sites', orgId]  → 站点列表
//   - ['web-publish-config', orgId] → 企业 web-publish 配置/证书状态
import { useMutation, useQuery, useQueryClient } from '@tanstack/vue-query'
import type { Ref } from 'vue'

import { apiRequest } from '@/api/client'
import type { SiteResult, WebPublishConfigResult, components } from '@/api'

// ConfigureWebPublishRequest 从生成类型派生，对应 PUT /platform/organizations/:orgId/web-publish 请求体。
export type ConfigureWebPublishRequest = components['schemas']['handlers.ConfigureWebPublishRequest']

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
    // 开通/签发是异步过程（provisioning job 写通配 A 记录 → DNS-01 签证书 → 建 Ingress），
    // 进行中时每 3s 自动轮询，使页面无需手动刷新即可看到状态推进；达终态后停止轮询。
    refetchInterval: (query) => {
      const cfg = query.state.data as WebPublishConfigResult | null | undefined
      if (!cfg) return false
      // provisioning 进行中，或证书处于首签/续签中，均视为进行中，继续轮询。
      const inProgress =
        cfg.provisioning_status === 'provisioning' ||
        cfg.cert_status === 'issuing' ||
        cfg.cert_status === 'renewing'
      return inProgress ? 3000 : false
    },
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

// useConfigureWebPublish 供平台管理员写入企业 web-publish 配置（根域名、DNS provider、凭证、配额）。
// 对应 PUT /api/v1/platform/organizations/:orgId/web-publish。
// 凭证 credentials 为空时后端保留已有加密凭证，明文不持久化。
// 成功后失效企业 web-publish 配置缓存，使页面及时展示最新状态。
export function useConfigureWebPublish(orgId: Ref<string | undefined>) {
  const client = useQueryClient()
  return useMutation({
    mutationFn: async (body: ConfigureWebPublishRequest) => {
      if (!orgId.value) throw new Error('orgId is required')
      await apiRequest<void>(
        `/api/v1/platform/organizations/${orgId.value}/web-publish`,
        { method: 'PUT', body },
      )
    },
    onSuccess: () => {
      if (orgId.value) {
        void client.invalidateQueries({ queryKey: webPublishConfigKey(orgId.value) })
      }
    },
  })
}

// useEnableWebPublish 供平台管理员开通企业 web-publish 能力。
// 对应 POST /api/v1/platform/organizations/:orgId/web-publish/enable。
// 该接口触发异步 provisioning job，状态机写为 provisioning；
// 成功后失效企业 web-publish 配置缓存，让状态面板自动刷新。
export function useEnableWebPublish(orgId: Ref<string | undefined>) {
  const client = useQueryClient()
  return useMutation({
    mutationFn: async () => {
      if (!orgId.value) throw new Error('orgId is required')
      await apiRequest<void>(
        `/api/v1/platform/organizations/${orgId.value}/web-publish/enable`,
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

// useDisableWebPublish 供平台管理员停用企业 web-publish 能力。
// 对应 POST /api/v1/platform/organizations/:orgId/web-publish/disable。
// 该接口只写状态机为 disabled，不删除配置数据；
// 成功后失效企业 web-publish 配置缓存，让状态面板自动刷新。
export function useDisableWebPublish(orgId: Ref<string | undefined>) {
  const client = useQueryClient()
  return useMutation({
    mutationFn: async () => {
      if (!orgId.value) throw new Error('orgId is required')
      await apiRequest<void>(
        `/api/v1/platform/organizations/${orgId.value}/web-publish/disable`,
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
