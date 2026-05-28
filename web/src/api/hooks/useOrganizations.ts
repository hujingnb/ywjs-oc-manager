// 企业 API hooks 负责平台管理员视角下的企业列表、详情、创建和状态变更。
// 企业写操作只失效企业列表；详情页如需最新数据会通过自身 query 重新拉取。
import { useMutation, useQuery, useQueryClient } from '@tanstack/vue-query'
import type { Ref } from 'vue'

import { apiRequest } from '@/api/client'
import type { Organization } from '@/api'

const ORG_LIST_KEY = ['organizations'] as const

// ModelOptionDTO 是 new-api 实时模型目录在前端使用的最小视图。
export interface ModelOptionDTO {
  // 模型 ID 会写入企业 allowlist 和实例 model_id。
  id: string
  // 模型名称用于下拉展示；当前后端通常与 id 相同。
  name: string
}

// OrganizationFormPayload 是创建企业及首个企业管理员的提交体。
export interface OrganizationFormPayload {
  // 企业登录标识，创建后不可修改。
  code: string
  // 企业名称。
  name: string
  // 联系人姓名。
  contact_name?: string
  // 联系电话。
  contact_phone?: string
  // 平台侧备注。
  remark?: string
  // 余额预警阈值；null 表示清空或使用后端默认。
  credit_warning_threshold?: number | null
  // 企业可用的助手版本 id 列表。
  assistant_version_ids: string[]
  // 首个企业管理员用户名。
  admin_username: string
  // 首个企业管理员展示名。
  admin_display_name: string
  // 首个企业管理员初始密码。
  admin_password: string
}

// useModelsQuery 获取 new-api 当前可用模型目录。
// enabled 用于只在企业表单等需要模型选择的场景发起请求。
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

// useOrganizationsQuery 提供平台维度的企业列表。
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

// useOrganizationQuery 查询单个企业信息。
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

// useCreateOrganization 创建企业，自动失效列表缓存。
// 后端会同时创建企业管理员；前端不保存初始密码。
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

// OrganizationUpdatePayload 是更新企业资料的提交体；不含 code（不可修改）和管理员账号字段（创建时专用）。
export interface OrganizationUpdatePayload {
  // 企业名称，必填。
  name: string
  // 联系人姓名。
  contact_name?: string
  // 联系电话。
  contact_phone?: string
  // 平台侧备注。
  remark?: string
  // 余额预警阈值；null 表示清空或使用后端默认。
  credit_warning_threshold?: number | null
  // 企业可用的助手版本 id 列表。
  assistant_version_ids: string[]
}

// useUpdateOrganization 更新企业资料与助手版本 allowlist，自动失效列表缓存。
// 传入 id（企业 id）与 payload（OrganizationUpdatePayload），调用 PATCH /api/v1/organizations/:id。
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

// useUpdateOrganizationStatus 启用或禁用企业。
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
