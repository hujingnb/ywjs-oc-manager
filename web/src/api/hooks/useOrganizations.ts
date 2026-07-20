// 组织 API hooks 负责平台管理员视角下的组织列表、详情、创建和状态变更。
// 组织写操作只失效组织列表；详情页如需最新数据会通过自身 query 重新拉取。
import { useMutation, useQuery, useQueryClient } from '@tanstack/vue-query'
import type { Ref } from 'vue'

import { apiRequest } from '@/api/client'
import type { Organization } from '@/api'

const ORG_LIST_KEY = ['organizations'] as const
const ORG_AICC_CONFIG_KEY = 'organization-aicc-config'

// ModelOptionDTO 是 new-api 实时模型目录在前端使用的最小视图。
export interface ModelOptionDTO {
  // 模型 ID 会写入组织 allowlist 和实例 model_id。
  id: string
  // 模型名称用于下拉展示；当前后端通常与 id 相同。
  name: string
}

// OrganizationFormPayload 是创建组织及首个组织管理员的提交体。
export interface OrganizationFormPayload {
  // 组织登录标识，创建后不可修改。
  code: string
  // 组织名称。
  name: string
  // 联系人姓名。
  contact_name?: string
  // 联系电话。
  contact_phone?: string
  // 平台侧备注。
  remark?: string
  // 余额预警阈值；null 表示清空或使用后端默认。
  credit_warning_threshold?: number | null
  // 实例数量上限；null/undefined 表示不限制。
  max_instance_count?: number | null
  // 企业知识库累计容量上限，单位字节；未传时后端创建默认 1GB、更新保留旧值。
  knowledge_quota_bytes?: number
  // 该企业新建实例的默认知识库容量上限，单位字节；未传时后端回落 1GB。
  default_app_knowledge_quota_bytes?: number
  // 组织可用的助手版本 id 列表。
  assistant_version_ids: string[]
  // 首个组织管理员用户名。
  admin_username: string
  // 首个组织管理员展示名。
  admin_display_name: string
  // 首个组织管理员初始密码。
  admin_password: string
}

// useModelsQuery 获取 new-api 当前可用模型目录。
// enabled 用于只在组织表单等需要模型选择的场景发起请求。
export function useModelsQuery(enabled?: () => boolean) {
  return useQuery<ModelOptionDTO[]>({
    queryKey: ['models'],
    enabled,
    queryFn: async () => {
      const response = await apiRequest<{ models: ModelOptionDTO[] }>('/api/v1/models')
      return response.models
    },
  })
}

// useOrganizationsQuery 提供平台维度的组织列表。
// 仅平台管理员调用；后端会拒绝非平台管理员的访问。
// enabled 让调用方可以在非平台管理员视角下显式禁用，避免无谓 403。
export function useOrganizationsQuery(enabled?: () => boolean) {
  return useQuery<Organization[]>({
    queryKey: ORG_LIST_KEY,
    enabled: enabled,
    queryFn: async () => {
      const response = await apiRequest<{ organizations: Organization[] }>('/api/v1/organizations', {
        query: { limit: 200 },
      })
      return response.organizations
    },
  })
}

// useOrganizationQuery 查询单个组织信息。
// orgId 为响应式引用，未填写时 query 暂停执行。
export function useOrganizationQuery(orgId: Ref<string | undefined>) {
  return useQuery<Organization | null>({
    queryKey: ['organization', orgId],
    enabled: () => Boolean(orgId.value),
    queryFn: async () => {
      if (!orgId.value) return null
      const response = await apiRequest<{ organization: Organization }>(`/api/v1/organizations/${orgId.value}`)
      return response.organization
    },
  })
}

// useCreateOrganization 创建组织，自动失效列表缓存。
// 后端会同时创建组织管理员；前端不保存初始密码。
export function useCreateOrganization() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: async (payload: OrganizationFormPayload) => {
      const response = await apiRequest<{ organization: Organization }>('/api/v1/organizations', {
        method: 'POST',
        body: payload,
      })
      return response.organization
    },
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: ORG_LIST_KEY })
    },
  })
}

// OrganizationUpdatePayload 是更新组织资料的提交体；不含 code（不可修改）和管理员账号字段（创建时专用）。
export interface OrganizationUpdatePayload {
  // 组织名称，必填。
  name: string
  // 联系人姓名。
  contact_name?: string
  // 联系电话。
  contact_phone?: string
  // 平台侧备注。
  remark?: string
  // 余额预警阈值；null 表示清空或使用后端默认。
  credit_warning_threshold?: number | null
  // 实例数量上限；null/undefined 表示不限制。
  max_instance_count?: number | null
  // 企业知识库累计容量上限，单位字节；未传时后端创建默认 1GB、更新保留旧值。
  knowledge_quota_bytes?: number
  // 该企业新建实例的默认知识库容量上限，单位字节；更新未传时后端保留旧值。
  default_app_knowledge_quota_bytes?: number
  // 组织可用的助手版本 id 列表。
  assistant_version_ids: string[]
}

// OrganizationAICCConfigPayload 是平台管理员维护企业 AICC 开通状态的提交体。
export interface OrganizationAICCConfigPayload {
  // enabled 表示是否开通 AICC。
  enabled: boolean
  // model 是企业 AICC 使用的实时模型目录 ID；关闭时也提交完整配置快照。
  model: string
  // agent_limit 是智能体数量上限；null/undefined 表示不限。
  agent_limit?: number | null
  // industry_knowledge_base_ids 是平台为该企业授权的行业知识库；空数组表示不授权任何行业库。
  industry_knowledge_base_ids: string[]
}

// OrganizationAICCConfig 是企业独立 AICC 配置接口返回的完整快照。
export interface OrganizationAICCConfig {
  // org_id 标识配置所属企业，切换编辑对象时用于拒绝旧请求结果。
  org_id: string
  // enabled 表示是否开通 AICC。
  enabled: boolean
  // model 是当前客服模型；尚未配置时为空。
  model?: string
  // agent_limit 是智能体数量上限；缺省表示不限。
  agent_limit?: number
  // revision 仅在模型变化时递增。
  revision: number
  // industry_knowledge_bases 是该企业获授权的行业知识库引用。
  industry_knowledge_bases: Array<{ id: string; name: string }>
}

// useOrganizationAICCConfigQuery 按企业读取独立 AICC 配置。
// orgId 为空时暂停请求，避免编辑表单关闭期间产生无效调用。
export function useOrganizationAICCConfigQuery(orgId: Ref<string | undefined>) {
  return useQuery<OrganizationAICCConfig | null>({
    queryKey: [ORG_AICC_CONFIG_KEY, orgId],
    enabled: () => Boolean(orgId.value),
    queryFn: async () => {
      if (!orgId.value) return null
      const response = await apiRequest<{ config: OrganizationAICCConfig }>(
        `/api/v1/organizations/${orgId.value}/aicc-config`,
      )
      return response.config
    },
  })
}

// useUpdateOrganization 更新组织资料与助手版本 allowlist，自动失效列表缓存。
// 传入 id（组织 id）与 payload（OrganizationUpdatePayload），调用 PATCH /api/v1/organizations/:id。
export function useUpdateOrganization() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: async ({ id, payload }: { id: string; payload: OrganizationUpdatePayload }) => {
      const response = await apiRequest<{ organization: Organization }>(
        `/api/v1/organizations/${id}`,
        { method: 'PATCH', body: payload },
      )
      return response.organization
    },
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: ORG_LIST_KEY })
    },
  })
}

// useUpdateOrganizationAICCConfig 以完整快照更新企业 AICC 配置，成功后刷新列表和对应配置。
export function useUpdateOrganizationAICCConfig() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: async ({ id, payload }: { id: string; payload: OrganizationAICCConfigPayload }) => {
      const response = await apiRequest<{ config: OrganizationAICCConfig }>(
        `/api/v1/organizations/${id}/aicc-config`,
        { method: 'PUT', body: payload },
      )
      return response.config
    },
    onSuccess: (_config, variables) => {
      void client.invalidateQueries({ queryKey: ORG_LIST_KEY })
      void client.invalidateQueries({ queryKey: [ORG_AICC_CONFIG_KEY, variables.id] })
    },
  })
}

// useUpdateOrganizationStatus 启用或禁用组织。
// 状态变更只影响列表可见字段，因此失效 ORG_LIST_KEY 即可。
export function useUpdateOrganizationStatus() {
  const client = useQueryClient()
  return useMutation({
    mutationFn: async ({ orgId, action }: { orgId: string; action: 'enable' | 'disable' }) => {
      const response = await apiRequest<{ organization: Organization }>(
        `/api/v1/organizations/${orgId}/${action}`,
        { method: 'POST' },
      )
      return response.organization
    },
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: ORG_LIST_KEY })
    },
  })
}
