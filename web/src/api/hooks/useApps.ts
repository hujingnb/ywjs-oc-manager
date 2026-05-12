// 应用 API hooks 负责封装应用、运行时、任务和用量相关的 TanStack Query 调用。
// 本文件只维护缓存键、启用条件和 mutation 后的失效边界，不承载页面展示逻辑。
import { useMutation, useQuery, useQueryClient } from '@tanstack/vue-query'
import type { Ref } from 'vue'

import { apiRequest } from '@/api/client'
import type { AggregatedUsage } from '@/api/hooks/useUsage'
import { rangeQuery, type InstanceResourceSample, type ResourceRange } from '@/api/hooks/useRuntimeNodes'

// AppDTO 是应用详情与列表接口共用的前端视图。
// 字段名保持后端 JSON snake_case，避免在 hook 层做额外映射。
export interface AppDTO {
  // 应用主键，用于详情、运行时和渠道等子资源路由。
  id: string
  // 应用所属组织，权限判断必须和当前用户 org_id 一起使用。
  org_id: string
  // 应用拥有者用户，普通成员只能管理自己拥有的应用。
  owner_user_id: string
  // 运行节点为空表示尚未分配或初始化失败。
  runtime_node_id?: string
  // 页面展示名称。
  name: string
  // 可选说明文案，空值由页面层决定是否展示占位。
  description?: string
  // 后端应用状态机原值，由 domain/status.ts 统一格式化。
  status: string
  // 人设模式，决定应用是否覆盖组织默认人设。
  persona_mode: string
  // 应用级提示词，仅 app_override 场景有业务意义。
  app_prompt?: string
  // runtime 容器 ID，容器尚未创建或已删除时为空。
  container_id?: string
  // new-api token 绑定状态，用于控制 API key 操作按钮。
  api_key_status: string
  // new-api key ID 用于应用维度用量查询；未初始化成功时为空。
  newapi_key_id?: number
}

// RuntimeOperationResult 是运行时异步操作的提交结果。
export interface RuntimeOperationResult {
  // 后端 job ID；调用方通常把它交给 useJobQuery 轮询。
  job_id: string
  // 已提交的操作名，如 start / stop / restart / delete。
  operation: string
}

// RuntimeContainerInfo 与后端 service.RuntimeContainerInfo 字段一致。
export interface RuntimeContainerInfo {
  // Docker 容器 ID。
  id: string
  // Docker 容器名称。
  name: string
  // 容器镜像名。
  image: string
  // Docker 返回的运行状态原值。
  status: string
}

// RuntimeView 是 GET /apps/:appId/runtime 的响应视图。
// container 在 status=no_container/error 时为空。
// snapshot 由 scheduler 30s 周期 runtime_refresh_status job 写入；首次未采集时为空。
export interface RuntimeView {
  // 前端展示用的运行时状态，包含 no_container / error 等 sentinel。
  status: string
  // status 指向真实容器时才存在。
  container?: RuntimeContainerInfo
  // 最近一次采样快照；首次采集前可能为空。
  snapshot?: RuntimeSnapshotView
}

// RuntimeSnapshotView 与后端 service.RuntimeSnapshotView 字段一一对应。
// 字节单位：内存与网络都是绝对值；CPU 百分比 = 单核满载 100%，多核可超 100%。
export interface RuntimeSnapshotView {
  // CPU 使用率，单核满载为 100%，多核场景可能超过 100。
  cpu_percent: number
  // 当前内存使用字节数。
  memory_usage_bytes: number
  // 容器内存限制字节数，0 由展示层按“未限制”处理。
  memory_limit_bytes: number
  // 网络接收累计字节数。
  network_rx_bytes: number
  // 网络发送累计字节数。
  network_tx_bytes: number
  // 后端采集时间。
  collected_at: string
  // 最近一次采样错误；存在时页面应优先提示异常原因。
  last_error?: string
}

// JobDTO 描述 jobs API 响应。
export interface JobDTO {
  // job 主键，用于轮询详情。
  id: string
  // job 类型，如 runtime_start / app_delete。
  type: string
  // job 状态，终态会停止轮询。
  status: 'pending' | 'running' | 'succeeded' | 'failed' | 'canceled'
  // 已尝试次数。
  attempts: number
  // 最大尝试次数。
  max_attempts: number
  // 下次可运行时间，pending 重试场景才有意义。
  run_after?: string
  // 终态完成时间。
  finished_at?: string
  // 失败原因，页面直接展示给管理员排障。
  last_error?: string
}

// 缓存键 helper 统一 mutation 的失效范围，避免散落字符串导致局部页面不刷新。
const orgKey = (orgId: string | undefined) => ['apps', 'org', orgId] as const
const appKey = (appId: string | undefined) => ['app', appId] as const
const runtimeKey = (appId: string | undefined) => ['app-runtime', appId] as const
const jobKey = (jobId: string | undefined) => ['job', jobId] as const

// useAppsByOrgQuery 列出组织内的应用。
// orgId 为空时暂停请求；列表缓存按组织隔离，避免切换组织时复用旧数据。
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
// appId 为空时 query 被禁用；data 通常为 undefined，除非同一缓存键已有旧数据。
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

// useAppResourcesQuery 查询单个应用实例的资源趋势。
// 权限沿用应用读取权限，调用方只需要传入当前时间范围即可复用同一套趋势图。
export function useAppResourcesQuery(appId: Ref<string | undefined>, range: Ref<ResourceRange>) {
  return useQuery<InstanceResourceSample[]>({
    queryKey: ['app-resources', appId, range],
    enabled: () => Boolean(appId.value),
    refetchInterval: 30_000,
    queryFn: async () => {
      if (!appId.value) return []
      const response = await apiRequest<{ samples?: InstanceResourceSample[] }>(
        `/api/v1/apps/${appId.value}/resources`,
        { query: rangeQuery(range.value) },
      )
      return response.samples ?? []
    },
  })
}

// useTriggerRuntimeOperation 触发启动/停止/重启/删除任务。
// mutation 成功只代表 job 已入队，因此同时失效应用详情与运行时视图，后续由轮询呈现终态。
export function useTriggerRuntimeOperation(appId: Ref<string | undefined>) {
  const client = useQueryClient()
  return useMutation({
    mutationFn: async (op: 'start' | 'stop' | 'restart' | 'delete') => {
      if (!appId.value) throw new Error('缺少实例 ID')
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

// useToggleAppAPIKey 触发禁用 / 恢复应用绑定的 new-api token。
// 后端只允许应用所属组织管理员；平台管理员和普通成员调用会 403。
// 操作只影响应用详情中的 api_key_status，不需要刷新运行时快照。
export function useToggleAppAPIKey(appId: Ref<string | undefined>) {
  const client = useQueryClient()
  return useMutation({
    mutationFn: async (action: 'disable' | 'restore') => {
      if (!appId.value) throw new Error('缺少实例 ID')
      const response = await apiRequest<{ runtime_operation: RuntimeOperationResult }>(
        `/api/v1/apps/${appId.value}/api-key/${action}`,
        { method: 'POST' },
      )
      return response.runtime_operation
    },
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: appKey(appId.value) })
    },
  })
}

// useInitializeAppMutation 触发应用初始化重试。
// 后端在 status ∉ {error, draft} 时返 409，调用方应展示提示。
// 初始化会重新创建运行时相关资源，因此同时刷新应用详情与 runtime query。
export function useInitializeAppMutation(appId: Ref<string | undefined>) {
  const client = useQueryClient()
  return useMutation({
    mutationFn: async () => {
      if (!appId.value) throw new Error('缺少实例 ID')
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
// context 来自应用拥有者和 new-api key 信息；缺任何一项都不能调用后端薄代理。
export function useAppUsageQuery(
  appId: Ref<string | undefined>,
  context: Ref<{ orgId: string; ownerUserId: string; newapiKeyId: number } | undefined>,
) {
  return useQuery<AggregatedUsage | null>({
    queryKey: ['app-usage', appId, context],
    enabled: () => Boolean(appId.value && context.value),
    queryFn: async () => {
      if (!appId.value || !context.value) return null
      const response = await apiRequest<{ usage: AggregatedUsage }>(`/api/v1/apps/${appId.value}/usage`, {
        query: {
          owner_org_id: context.value.orgId,
          owner_user_id: context.value.ownerUserId,
          newapi_key_id: context.value.newapiKeyId,
        },
      })
      return response.usage
    },
  })
}

// 占位导出，避免 tree-shake 时丢失类型。
export const _appsKeys = { orgKey, appKey, runtimeKey, jobKey }
