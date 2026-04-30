import { useMutation, useQuery, useQueryClient } from '@tanstack/vue-query'
import type { Ref } from 'vue'

import { apiRequest } from '@/api/client'

export interface AppDTO {
  id: string
  org_id: string
  owner_user_id: string
  runtime_node_id?: string
  name: string
  description?: string
  status: string
  persona_mode: string
  app_prompt?: string
  container_id?: string
  api_key_status: string
}

export interface RuntimeOperationResult {
  job_id: string
  operation: string
}

const orgKey = (orgId: string | undefined) => ['apps', 'org', orgId] as const
const appKey = (appId: string | undefined) => ['app', appId] as const

// useAppsByOrgQuery 列出组织内的应用。
export function useAppsByOrgQuery(orgId: Ref<string | undefined>) {
  return useQuery<AppDTO[]>({
    queryKey: ['apps', 'org', orgId],
    enabled: () => Boolean(orgId.value),
    queryFn: async () => {
      if (!orgId.value) return []
      const response = await apiRequest<{ apps?: AppDTO[]; organization?: unknown }>(`/api/v1/organizations/${orgId.value}/apps`, {
        query: { limit: 200 },
      })
      return response.apps ?? []
    },
  })
}

// useAppQuery 查询单个应用。
export function useAppQuery(appId: Ref<string | undefined>) {
  return useQuery<AppDTO | null>({
    queryKey: ['app', appId],
    enabled: () => Boolean(appId.value),
    queryFn: async () => {
      if (!appId.value) return null
      const response = await apiRequest<{ app: AppDTO }>(`/api/v1/apps/${appId.value}`)
      return response.app
    },
  })
}

// useTriggerRuntimeOperation 触发启动/停止/重启/删除任务。
export function useTriggerRuntimeOperation(appId: Ref<string | undefined>) {
  const client = useQueryClient()
  return useMutation({
    mutationFn: async (op: 'start' | 'stop' | 'restart' | 'delete') => {
      if (!appId.value) throw new Error('缺少应用 ID')
      const response = await apiRequest<{ runtime_operation: RuntimeOperationResult }>(
        `/api/v1/apps/${appId.value}/runtime/${op}`,
        { method: 'POST' },
      )
      return response.runtime_operation
    },
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: appKey(appId.value) })
    },
  })
}

// useAppUsageQuery 查询应用维度的 token 用量。
export function useAppUsageQuery(
  appId: Ref<string | undefined>,
  context: Ref<{ orgId: string; ownerUserId: string; newapiKeyId: number } | undefined>,
) {
  return useQuery<{ usage?: { app_id: string; remain_quota: number; status: number; updated_at: string } } | null>({
    queryKey: ['app-usage', appId, context],
    enabled: () => Boolean(appId.value && context.value),
    queryFn: async () => {
      if (!appId.value || !context.value) return null
      return apiRequest(`/api/v1/apps/${appId.value}/usage`, {
        query: {
          owner_org_id: context.value.orgId,
          owner_user_id: context.value.ownerUserId,
          newapi_key_id: context.value.newapiKeyId,
        },
      })
    },
  })
}

// 占位导出，避免 tree-shake 时丢失类型。
export const _appsKeys = { orgKey, appKey }
