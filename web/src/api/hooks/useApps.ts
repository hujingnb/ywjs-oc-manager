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

// RuntimeContainerInfo 与后端 service.RuntimeContainerInfo 字段一致。
export interface RuntimeContainerInfo {
  id: string
  name: string
  image: string
  status: string
}

// RuntimeView 是 GET /apps/:appId/runtime 的响应视图。
// container 在 status=no_container/error 时为空。
export interface RuntimeView {
  status: string
  container?: RuntimeContainerInfo
}

// JobDTO 描述 jobs API 响应。
export interface JobDTO {
  id: string
  type: string
  status: 'pending' | 'running' | 'succeeded' | 'failed' | 'canceled'
  attempts: number
  max_attempts: number
  run_after?: string
  finished_at?: string
  last_error?: string
}

const orgKey = (orgId: string | undefined) => ['apps', 'org', orgId] as const
const appKey = (appId: string | undefined) => ['app', appId] as const
const runtimeKey = (appId: string | undefined) => ['app-runtime', appId] as const
const jobKey = (jobId: string | undefined) => ['job', jobId] as const

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

// useAppRuntimeQuery 透传后端 docker inspect 视图，5 秒轮询；
// 后端在 container 不存在或节点不可达时返 sentinel 状态，前端据此切换提示。
export function useAppRuntimeQuery(appId: Ref<string | undefined>) {
  return useQuery<RuntimeView | null>({
    queryKey: ['app-runtime', appId],
    enabled: () => Boolean(appId.value),
    refetchInterval: 5000,
    queryFn: async () => {
      if (!appId.value) return null
      const response = await apiRequest<{ runtime: RuntimeView }>(`/api/v1/apps/${appId.value}/runtime`)
      return response.runtime
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
      void client.invalidateQueries({ queryKey: runtimeKey(appId.value) })
    },
  })
}

// useInitializeAppMutation 触发应用初始化重试。
// 后端在 status ∉ {error, draft} 时返 409，调用方应展示提示。
export function useInitializeAppMutation(appId: Ref<string | undefined>) {
  const client = useQueryClient()
  return useMutation({
    mutationFn: async () => {
      if (!appId.value) throw new Error('缺少应用 ID')
      const response = await apiRequest<{ runtime_operation: RuntimeOperationResult }>(
        `/api/v1/apps/${appId.value}/initialize`,
        { method: 'POST' },
      )
      return response.runtime_operation
    },
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: appKey(appId.value) })
      void client.invalidateQueries({ queryKey: runtimeKey(appId.value) })
    },
  })
}

// useJobQuery 查询 job 详情，支持轮询直至 succeeded/failed/canceled。
// 调用方通过 enabled / refetchInterval 控制轮询窗口。
export function useJobQuery(jobId: Ref<string | undefined>) {
  return useQuery<JobDTO | null>({
    queryKey: ['job', jobId],
    enabled: () => Boolean(jobId.value),
    refetchInterval: (query) => {
      const data = query.state.data as JobDTO | null | undefined
      if (!data) return 2000
      if (data.status === 'pending' || data.status === 'running') return 2000
      return false
    },
    queryFn: async () => {
      if (!jobId.value) return null
      const response = await apiRequest<{ job: JobDTO }>(`/api/v1/jobs/${jobId.value}`)
      return response.job
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
export const _appsKeys = { orgKey, appKey, runtimeKey, jobKey }
