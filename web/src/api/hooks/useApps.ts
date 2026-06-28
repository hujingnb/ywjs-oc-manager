// 应用 API hooks 负责封装应用、运行时、任务和用量相关的 TanStack Query 调用。
// 本文件只维护缓存键、启用条件和 mutation 后的失效边界，不承载页面展示逻辑。
import { useMutation, useQuery, useQueryClient } from '@tanstack/vue-query'
import type { Ref } from 'vue'

import { apiRequest, type ApiError } from '@/api/client'
import { i18n } from '@/i18n'
import type { AggregatedUsage } from '@/api/hooks/useUsage'
import type { components } from '@/api/generated'

// AppLocaleStatus 复用 OpenAPI 生成类型，避免与后端契约漂移。
// current_language 在实例未运行/不可达时为 null/缺省；needs_restart=true 表示当前语言与期望语言不一致需重启生效。
export type AppLocaleStatus = components['schemas']['handlers.AppLocaleStatusResponse']

// AppDTO 是应用详情与列表接口共用的前端视图。
// 字段名保持后端 JSON snake_case，避免在 hook 层做额外映射。
// spec-A2b：runtime_node_id / container_id 字段已随节点概念删除（migration 000003 对应后端去字段），
// 前端 DTO 同步去掉，避免读取恒为空的字段误导页面逻辑。
export interface AppDTO {
  // 应用主键，用于详情、运行时和渠道等子资源路由。
  id: string
  // 应用所属组织，权限判断必须和当前用户 org_id 一起使用。
  org_id: string
  // 应用拥有者用户，普通成员只能管理自己拥有的应用。
  owner_user_id: string
  // 页面展示名称。
  name: string
  // 可选说明文案，空值由页面层决定是否展示占位。
  description?: string
  // 后端应用状态机原值，由 domain/status.ts 统一格式化。
  status: string
  // new-api token 绑定状态，用于控制 API key 操作按钮。
  api_key_status: string
  // new-api key ID 用于应用维度用量查询；未初始化成功时为空。
  newapi_key_id?: number
  // progress_current 是当前 status 阶段已完成量（字节或秒），缺省表示未知。
  progress_current?: number
  // progress_total 是当前 status 阶段总量；0 或缺省时 UI 走不定进度条。
  progress_total?: number
  // last_error_status 是上次进入 error 时所在状态值，用于在错误态展示失败阶段。
  last_error_status?: string
  // last_error_message 是上次进入 error 时的错误原始文本，供页面直接展示给用户。
  last_error_message?: string
  // version_synced 标记实例运行时是否已与绑定的助手版本对齐；false 表示版本被编辑过，需重启生效。
  version_synced?: boolean
  // version_id 是实例绑定的助手版本 id；空表示未绑定（仅历史数据）。
  version_id?: string
  // knowledge_quota_bytes 是实例知识库累计容量上限，单位字节。
  knowledge_quota_bytes: number
  // runtime_image_ref 是 phasePullRuntimeImage 拉取的镜像引用；仅平台管理员可见。
  runtime_image_ref?: string
  // runtime_image_sha256 是 docker inspect 返回的镜像 config digest；仅平台管理员可见。
  runtime_image_sha256?: string
  // runtime_phase 是运行时就绪维度（与 status 正交）：ready/starting/restarting/unknown。
  // 渠道发起闸门需 status allowlist 且 runtime_phase===ready；非 ready 时按 phase 细化提示。
  runtime_phase?: string
}

// RuntimeOperationResult 是运行时异步操作的提交结果。
export interface RuntimeOperationResult {
  // 后端 job ID；调用方通常把它交给 useJobQuery 轮询。
  job_id: string
  // 已提交的操作名，如 start / stop / restart / delete。
  operation: string
}

// RuntimeView 是 GET /apps/:appId/runtime 的响应视图。
// spec-A2b：container 字段已随节点概念删除（后端 InspectApp 恒不填充 Container，彻底去除）。
// snapshot 由 scheduler 30s 周期 runtime_refresh_status job 写入；首次未采集时为空。
export interface RuntimeView {
  // 前端展示用的运行时状态，包含 no_container / error 等 sentinel。
  status: string
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
// spec-A2b：appResourcesKey 随资源采样 API 一起删除（节点概念已去，资源趋势图不再展示）。
const orgKey = (orgId: string | undefined) => ['apps', 'org', orgId] as const
const appKey = (appId: string | undefined) => ['app', appId] as const
const runtimeKey = (appId: string | undefined) => ['app-runtime', appId] as const
const localeStatusKey = (appId: string | undefined) => ['app-locale-status', appId] as const
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
// 应用处于 init / draft / starting / binding_waiting 等过渡状态时启用 1.5s 轮询，
// 让前端进度条与状态 tag 能跟随后端 4 阶段状态机实时变化；进入稳态（running / stopped / error
// 等）后停止轮询，避免长期占用带宽与 DB。
export function useAppQuery(appId: Ref<string | undefined>) {
  return useQuery<AppDTO | null>({
    queryKey: ['app', appId],
    enabled: () => Boolean(appId.value),
    refetchInterval: (query) => {
      // status 为过渡态时每 1.5s 轮询一次，其余状态停止轮询。
      const status = query.state.data?.status
      const transitionalStatuses = new Set([
        'draft',
        'pulling_runtime_image',
        'preparing_runtime',
        'creating_container',
        'starting',
        'binding_waiting',
      ])
      return status && transitionalStatuses.has(status) ? 1500 : false
    },
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

// useAppLocaleStatusQuery 查询实例实时语言状态（current_language / desired_language / needs_restart）。
// current_language 取自 oc-ops，实例未运行/不可达时为 null/缺省；needs_restart=true 表示需重启生效。
// 响应非包裹结构，直接返回 AppLocaleStatus 对象。
export function useAppLocaleStatusQuery(appId: Ref<string | undefined>) {
  return useQuery<AppLocaleStatus | null>({
    queryKey: ['app-locale-status', appId],
    enabled: () => Boolean(appId.value),
    queryFn: async () => {
      if (!appId.value) return null
      return await apiRequest<AppLocaleStatus>(`/api/v1/apps/${appId.value}/locale-status`)
    },
  })
}

// useTriggerRuntimeOperation 触发启动/停止/重启/删除任务。
// mutation 成功只代表 job 已入队，因此同时失效应用详情与运行时视图，后续由轮询呈现终态。
export function useTriggerRuntimeOperation(appId: Ref<string | undefined>) {
  const client = useQueryClient()
  return useMutation({
    mutationFn: async (op: 'start' | 'stop' | 'restart' | 'delete') => {
      if (!appId.value) throw new Error(i18n.global.t('common.errors.missingAppId'))
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
      if (!appId.value) throw new Error(i18n.global.t('common.errors.missingAppId'))
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
      if (!appId.value) throw new Error(i18n.global.t('common.errors.missingAppId'))
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
// 4xx 错误（403/404/400）视为终态：停止轮询且不重试，避免后端权限或资源不存在时
// 把 2s 一次的请求打到永远；5xx 仍然走 TanStack Query 默认重试以容忍偶发故障。
export function useJobQuery(jobId: Ref<string | undefined>) {
  return useQuery<JobDTO | null>({
    queryKey: ['job', jobId],
    enabled: () => Boolean(jobId.value),
    retry: (failureCount, error) => {
      const status = (error as ApiError | undefined)?.status
      if (status !== undefined && status >= 400 && status < 500) {
        return false
      }
      return failureCount < 3
    },
    refetchInterval: (query) => {
      const err = query.state.error as ApiError | null
      if (err && err.status >= 400 && err.status < 500) {
        return false
      }
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

// useInvalidateAppData 返回一个失效函数，供页面在后台任务（重启 / 初始化 / 恢复 key 等）
// 到达终态时主动刷新实例详情与运行时视图。
// 触发 mutation 时的 onSuccess 只在「任务入队」那一刻失效一次，无法反映任务真正执行完成后的
// 结果（如 version_synced 由 false 变 true、「需重启」标签消失、运行时快照更新）；对于
// restart 镜像不变分支，status 全程保持 running，useAppQuery 的过渡态轮询不会触发，
// 因此必须在 job 终态时再失效一次，让 UI 无需用户手动刷新即可对齐最新状态。
export function useInvalidateAppData(appId: Ref<string | undefined>) {
  const client = useQueryClient()
  return () => {
    void client.invalidateQueries({ queryKey: appKey(appId.value) })
    void client.invalidateQueries({ queryKey: runtimeKey(appId.value) })
  }
}

// useAppUsageQuery 查询应用维度的 token 用量。
// 应用归属与权限由后端按 appId 从数据库校验，前端不再传 owner 参数（避免伪造 owner 越权）；
// 这里只需带上 new-api key 信息：key 缺失（newapiKeyId 为 0/未定义）表示实例尚未初始化，不调后端。
export function useAppUsageQuery(
  appId: Ref<string | undefined>,
  context: Ref<{ newapiKeyId: number } | undefined>,
) {
  return useQuery<AggregatedUsage | null>({
    queryKey: ['app-usage', appId, context],
    enabled: () => Boolean(appId.value && context.value),
    queryFn: async () => {
      if (!appId.value || !context.value) return null
      const response = await apiRequest<{ usage: AggregatedUsage }>(`/api/v1/apps/${appId.value}/usage`, {
        query: {
          newapi_key_id: context.value.newapiKeyId,
        },
      })
      return response.usage
    },
  })
}

// useSwitchAppVersion 切换实例绑定的助手版本。
// 切换后实例进入需重启态（version_synced=false）；失效应用详情让 UI 立即反映新版本与需重启提示。
export function useSwitchAppVersion(appId: Ref<string | undefined>) {
  const client = useQueryClient()
  return useMutation({
    mutationFn: async (versionId: string) => {
      if (!appId.value) throw new Error(i18n.global.t('common.errors.missingAppId'))
      const response = await apiRequest<{ app: AppDTO }>(
        `/api/v1/apps/${appId.value}/version`,
        { method: 'POST', body: { version_id: versionId } },
      )
      return response.app
    },
    onSuccess: () => { void client.invalidateQueries({ queryKey: appKey(appId.value) }) },
  })
}

// useUpdateAppKnowledgeQuota 更新单个实例知识库容量，并刷新实例详情与知识库列表。
export function useUpdateAppKnowledgeQuota(appId: Ref<string | undefined>) {
  const client = useQueryClient()
  return useMutation({
    mutationFn: async (quotaBytes: number) => {
      if (!appId.value) throw new Error(i18n.global.t('common.errors.missingAppId'))
      const response = await apiRequest<{ app: AppDTO }>(
        `/api/v1/apps/${appId.value}/knowledge/quota`,
        { method: 'PATCH', body: { quota_bytes: quotaBytes } },
      )
      return response.app
    },
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: appKey(appId.value) })
      void client.invalidateQueries({ queryKey: ['knowledge', 'app', appId.value] })
    },
  })
}

// 占位导出，避免 tree-shake 时丢失类型。
export const _appsKeys = { orgKey, appKey, runtimeKey, localeStatusKey, jobKey }
