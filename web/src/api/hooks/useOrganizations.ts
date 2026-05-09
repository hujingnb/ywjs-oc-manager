import { useMutation, useQuery, useQueryClient } from '@tanstack/vue-query'
import type { Ref } from 'vue'

import { apiRequest } from '@/api/client'
import type { Organization } from '@/api'

const ORG_LIST_KEY = ['organizations'] as const

export interface OrganizationFormPayload {
  name: string
  contact_name?: string
  contact_phone?: string
  remark?: string
  credit_warning_threshold?: number | null
  newapi_user_id?: string
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

// useUpdateOrganizationStatus 启用或禁用组织。
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
