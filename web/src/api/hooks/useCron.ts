// useCron.ts —— 实例 Hermes Cron 管理 API hooks。
// 数据来自 manager 的 /api/v1/apps/{appId}/hermes/cron/* 端点；hook 层只负责请求、
// TanStack Query 缓存键和写操作后的失效边界，不承载页面展示逻辑。
import { useMutation, useQuery, useQueryClient } from '@tanstack/vue-query'
import { computed, type Ref } from 'vue'

import { apiRequest } from '@/api/client'

// CronSchedule 描述 Cron 任务的规整调度表达式。
export interface CronSchedule {
  // kind 是表达式类别，例如 cron / every / at；旧数据可能为空。
  kind?: string
  // expr 是机器可读表达式；oc-cron 返回空值时后端会规整为空字符串。
  expr?: string
  // display 是面向用户展示的调度说明。
  display?: string
}

// CronRepeat 描述任务重复次数与已完成次数。
export interface CronRepeat {
  // times 为空表示不限次数。
  times?: number
  // completed 是已完成的调度次数。
  completed?: number
}

// CronJob 对应 service.CronJob，是列表、详情和写操作返回的权威任务对象。
export interface CronJob {
  // id 是 Cron 任务唯一标识，用于详情、写操作和历史输出路由。
  id?: string
  // name 是任务显示名称。
  name?: string
  // prompt 是任务触发时交给 Hermes 的提示词。
  prompt?: string
  // schedule 是后端规整后的调度表达式。
  schedule?: CronSchedule
  // repeat 描述有限重复任务的进度。
  repeat?: CronRepeat
  // enabled 表示调度器是否会自动触发该任务。
  enabled?: boolean
  // state 是调度状态，如 scheduled / paused / disabled / removed。
  state?: string
  // created_at 保持 oc-cron 原始时间字符串，展示层决定格式化方式。
  created_at?: string
  // next_run_at 是下一次计划运行时间。
  next_run_at?: string
  // last_run_at 是最近一次运行时间。
  last_run_at?: string
  // last_status 是最近一次运行状态。
  last_status?: string
  // last_error 是最近一次执行错误摘要。
  last_error?: string
  // last_delivery_error 是最近一次投递错误摘要。
  last_delivery_error?: string
  // deliver 是任务输出投递目标，例如 wechat。
  deliver?: string
  // script 是任务使用的仓库内脚本文件名。
  script?: string
  // no_agent 表示任务是否跳过 agent 执行路径。
  no_agent?: boolean
  // workdir 是任务运行目录；平台管理员高级字段。
  workdir?: string
  // skills 是任务声明需要的技能列表；平台管理员高级字段。
  skills?: string[]
  // model 是任务指定模型；平台管理员高级字段。
  model?: string
  // provider 是任务指定模型提供方；平台管理员高级字段。
  provider?: string
  // base_url 是任务指定 provider base URL；平台管理员高级字段。
  base_url?: string
}

// CronStatus 对应 service.CronStatus，用于展示调度器摘要和轮询健康状态。
export interface CronStatus {
  available?: boolean
  gateway_running?: boolean
  active_jobs?: number
  next_run_at?: string
  next_job_id?: string
  tick_seconds?: number
  pid?: number
  message?: string
  last_error?: string
  last_error_job_id?: string
}

// CronRunEntry 对应 oc-cron history 的单条运行记录。
export interface CronRunEntry {
  job_id?: string
  file_name?: string
  run_time?: string
  size?: number
  has_output?: boolean
  synthetic?: boolean
  status?: string
  error?: string
}

// CronRunOutput 对应 oc-cron output 返回的 markdown 内容。
export interface CronRunOutput {
  job_id?: string
  file_name?: string
  run_time?: string
  content?: string
}

// CronFeatures 是 oc-cron 的细粒度能力开关，页面据此做功能降级。
export interface CronFeatures {
  status?: boolean
  history?: boolean
  output?: boolean
  write?: boolean
  script?: boolean
  advanced_fields?: boolean
}

// CronCapabilities 对应 service.CronCapabilities。
export interface CronCapabilities {
  contract_version?: string
  oc_cron_version?: string
  hermes_version?: string
  variant?: string
  verbs?: string[]
  features?: CronFeatures
}

// CronJobFilters 是任务列表筛选条件；q/status 给前端缓存隔离使用，
// all 是当前后端已支持的查询参数。
export interface CronJobFilters {
  q?: string
  status?: string
  all?: boolean
}

// CreateCronJobRequest 与 handlers.CreateCronJobRequest 保持字段一致。
// 基础页面只需要 name/schedule/prompt，高级字段保留给平台管理员 UI。
export interface CreateCronJobRequest {
  name: string
  schedule: string
  prompt?: string
  deliver?: string
  repeat?: number
  script?: string
  no_agent?: boolean
  workdir?: string
  skills?: string[]
  model?: string
  provider?: string
  base_url?: string
}

// UpdateCronJobRequest 与 handlers.UpdateCronJobRequest 保持字段一致。
// 可选字段用于表达“保持原值”；空字符串是否清空由后端契约解释。
export interface UpdateCronJobRequest {
  name?: string
  schedule?: string
  prompt?: string
  deliver?: string
  repeat?: number
  clear_repeat?: boolean
  script?: string
  no_agent?: boolean
  workdir?: string
  skills?: string[]
  clear_skills?: boolean
  model?: string
  provider?: string
  base_url?: string
}

// UpdateCronJobVariables 在 mutation 变量里额外携带路径参数 jobId。
export interface UpdateCronJobVariables extends UpdateCronJobRequest {
  jobId: string
}

// CronJobAction 覆盖除 create/update 外的单任务写操作。
export type CronJobAction =
  | { verb: 'delete'; jobId: string }
  | { verb: 'pause'; jobId: string }
  | { verb: 'resume'; jobId: string }
  | { verb: 'run'; jobId: string }

// CronJobQuery 是 apiRequest 接收的列表查询参数形态。
interface CronJobQuery extends Record<string, string | number | undefined> {
  q: string
  status: string
  all?: number
}

// normalizeCronJobFilters 统一列表缓存键和请求 query，确保空筛选也有稳定 key。
function normalizeCronJobFilters(filters: CronJobFilters = {}): CronJobQuery {
  const query: CronJobQuery = {
    q: filters.q ?? '',
    status: filters.status ?? '',
  }
  if (filters.all) {
    query.all = 1
  }
  return query
}

// ─── queryKey 约定 ───────────────────────────────────────────────────
// 统一以 ['cron', 子类, appId, ...] 为前缀，mutation 使用已解包的原始值失效缓存。

export const cronJobsKey = (appId: string | undefined, filters?: CronJobFilters) =>
  filters === undefined
    ? ['cron', 'jobs', appId] as const
    : ['cron', 'jobs', appId, normalizeCronJobFilters(filters)] as const

export const cronStatusKey = (appId: string | undefined) =>
  ['cron', 'status', appId] as const

export const cronJobKey = (appId: string | undefined, jobId: string | undefined) =>
  ['cron', 'job', appId, jobId] as const

export const cronHistoryKey = (appId: string | undefined, jobId: string | undefined) =>
  ['cron', 'history', appId, jobId] as const

export const cronOutputKey = (
  appId: string | undefined,
  jobId: string | undefined,
  fileName: string | undefined,
) => ['cron', 'output', appId, jobId, fileName] as const

const cronCapabilitiesKey = (appId: string | undefined) =>
  ['cron', 'capabilities', appId] as const

const cronOutputPrefixKey = (appId: string | undefined, jobId: string | undefined) =>
  ['cron', 'output', appId, jobId] as const

// isCronStubError 用于停止 stub 实例上的轮询，避免持续打 503 并刷新 console 错误。
function isCronStubError(error: unknown): boolean {
  return Boolean((error as { body?: { code?: string } } | null | undefined)?.body?.code === 'CRON_NOT_SUPPORTED_ON_STUB')
}

// ─── 读 query hooks ──────────────────────────────────────────────────

// useCronCapabilitiesQuery 探测实例 oc-cron 契约版本与可用能力。
export function useCronCapabilitiesQuery(appId: Ref<string | undefined>) {
  return useQuery<CronCapabilities | null>({
    queryKey: ['cron', 'capabilities', appId],
    enabled: () => Boolean(appId.value),
    staleTime: Infinity,
    retry: false,
    queryFn: async () => {
      const res = await apiRequest<{ capabilities: CronCapabilities }>(
        `/api/v1/apps/${appId.value}/hermes/cron/capabilities`,
      )
      return res.capabilities ?? null
    },
  })
}

// useCronStatusQuery 拉取调度器状态；正常实例每 5s 轮询一次。
export function useCronStatusQuery(appId: Ref<string | undefined>) {
  return useQuery<CronStatus | null>({
    queryKey: ['cron', 'status', appId],
    enabled: () => Boolean(appId.value),
    refetchInterval: (query) => (isCronStubError(query.state.error) ? false : 5000),
    queryFn: async () => {
      const res = await apiRequest<{ status: CronStatus }>(
        `/api/v1/apps/${appId.value}/hermes/cron/status`,
      )
      return res.status ?? null
    },
  })
}

// useCronJobsQuery 拉取实例 Cron 任务列表，并按筛选条件隔离缓存。
export function useCronJobsQuery(appId: Ref<string | undefined>, filters?: Ref<CronJobFilters>) {
  return useQuery<CronJob[]>({
    queryKey: computed(() => cronJobsKey(appId.value, filters?.value ?? {})),
    enabled: () => Boolean(appId.value),
    refetchInterval: (query) => (isCronStubError(query.state.error) ? false : 5000),
    queryFn: async () => {
      const res = await apiRequest<{ jobs: CronJob[] }>(
        `/api/v1/apps/${appId.value}/hermes/cron/jobs`,
        { query: normalizeCronJobFilters(filters?.value) },
      )
      return res.jobs ?? []
    },
  })
}

// useCronJobQuery 拉取单个 Cron 任务详情。
export function useCronJobQuery(
  appId: Ref<string | undefined>,
  jobId: Ref<string | undefined>,
) {
  return useQuery<CronJob | null>({
    queryKey: ['cron', 'job', appId, jobId],
    enabled: () => Boolean(appId.value && jobId.value),
    queryFn: async () => {
      if (!jobId.value) return null
      const res = await apiRequest<{ job: CronJob }>(
        `/api/v1/apps/${appId.value}/hermes/cron/jobs/${jobId.value}`,
      )
      return res.job ?? null
    },
  })
}

// useCronHistoryQuery 拉取单个任务的运行历史记录。
export function useCronHistoryQuery(
  appId: Ref<string | undefined>,
  jobId: Ref<string | undefined>,
) {
  return useQuery<CronRunEntry[]>({
    queryKey: ['cron', 'history', appId, jobId],
    enabled: () => Boolean(appId.value && jobId.value),
    queryFn: async () => {
      if (!jobId.value) return []
      const res = await apiRequest<{ runs: CronRunEntry[] }>(
        `/api/v1/apps/${appId.value}/hermes/cron/jobs/${jobId.value}/history`,
      )
      return res.runs ?? []
    },
  })
}

// useCronOutputQuery 读取单次运行输出；fileName 为空时暂停请求。
export function useCronOutputQuery(
  appId: Ref<string | undefined>,
  jobId: Ref<string | undefined>,
  fileName: Ref<string | undefined>,
) {
  return useQuery<CronRunOutput | null>({
    queryKey: ['cron', 'output', appId, jobId, fileName],
    enabled: () => Boolean(appId.value && jobId.value && fileName.value),
    queryFn: async () => {
      if (!jobId.value || !fileName.value) return null
      const encodedFileName = encodeURIComponent(fileName.value)
      const res = await apiRequest<{ output: CronRunOutput }>(
        `/api/v1/apps/${appId.value}/hermes/cron/jobs/${jobId.value}/output/${encodedFileName}`,
      )
      return res.output ?? null
    },
  })
}

// ─── 写 mutation hooks ───────────────────────────────────────────────

function ensureAppId(appId: Ref<string | undefined>): string {
  if (!appId.value) throw new Error('缺少实例 ID')
  return appId.value
}

function setCronJobCache(
  client: ReturnType<typeof useQueryClient>,
  appId: string | undefined,
  jobId: string | undefined,
  job: CronJob | null | undefined,
) {
  if (job && jobId) {
    client.setQueryData(cronJobKey(appId, jobId), job)
  }
}

function invalidateCronOverview(client: ReturnType<typeof useQueryClient>, appId: string | undefined) {
  void client.invalidateQueries({ queryKey: cronJobsKey(appId) })
  void client.invalidateQueries({ queryKey: cronStatusKey(appId) })
}

function invalidateCronRunArtifacts(
  client: ReturnType<typeof useQueryClient>,
  appId: string | undefined,
  jobId: string | undefined,
) {
  void client.invalidateQueries({ queryKey: cronHistoryKey(appId, jobId) })
  void client.invalidateQueries({ queryKey: cronOutputPrefixKey(appId, jobId) })
}

// useCreateCronJob 新建任务，成功后写入详情缓存并刷新列表/状态。
export function useCreateCronJob(appId: Ref<string | undefined>) {
  const client = useQueryClient()
  return useMutation({
    mutationFn: async (payload: CreateCronJobRequest) => {
      const id = ensureAppId(appId)
      const res = await apiRequest<{ job: CronJob }>(
        `/api/v1/apps/${id}/hermes/cron/jobs`,
        { method: 'POST', body: payload },
      )
      return res.job
    },
    onSuccess: (job) => {
      setCronJobCache(client, appId.value, job?.id, job)
      invalidateCronOverview(client, appId.value)
    },
  })
}

// useUpdateCronJob 更新任务基础字段；返回的权威任务对象直接写入详情缓存。
export function useUpdateCronJob(appId: Ref<string | undefined>) {
  const client = useQueryClient()
  return useMutation({
    mutationFn: async ({ jobId, ...payload }: UpdateCronJobVariables) => {
      const id = ensureAppId(appId)
      const res = await apiRequest<{ job: CronJob }>(
        `/api/v1/apps/${id}/hermes/cron/jobs/${jobId}`,
        { method: 'PATCH', body: payload },
      )
      return res.job
    },
    onSuccess: (job, variables) => {
      setCronJobCache(client, appId.value, job?.id || variables.jobId, job)
      invalidateCronOverview(client, appId.value)
    },
  })
}

// useCronJobAction 统一处理 pause/resume/run/delete 四类单任务写操作。
export function useCronJobAction(appId: Ref<string | undefined>) {
  const client = useQueryClient()
  return useMutation({
    mutationFn: async (action: CronJobAction) => {
      const id = ensureAppId(appId)
      if (action.verb === 'delete') {
        await apiRequest<void>(`/api/v1/apps/${id}/hermes/cron/jobs/${action.jobId}`, {
          method: 'DELETE',
        })
        return { action, job: null as CronJob | null }
      }
      const res = await apiRequest<{ job: CronJob }>(
        `/api/v1/apps/${id}/hermes/cron/jobs/${action.jobId}/${action.verb}`,
        { method: 'POST' },
      )
      return { action, job: res.job }
    },
    onSuccess: ({ action, job }) => {
      if (action.verb === 'delete') {
        client.removeQueries({ queryKey: cronJobKey(appId.value, action.jobId) })
        client.removeQueries({ queryKey: cronHistoryKey(appId.value, action.jobId) })
        client.removeQueries({ queryKey: cronOutputPrefixKey(appId.value, action.jobId) })
      } else {
        setCronJobCache(client, appId.value, job?.id || action.jobId, job)
      }
      invalidateCronOverview(client, appId.value)
      if (action.verb === 'run') {
        invalidateCronRunArtifacts(client, appId.value, action.jobId)
      }
    },
  })
}

// 便捷 mutation hooks 供页面按按钮语义分别引入，底层仍保持同一套缓存处理规则。
export function useDeleteCronJob(appId: Ref<string | undefined>) {
  const action = useCronJobAction(appId)
  return {
    ...action,
    mutate: (jobId: string) => action.mutate({ verb: 'delete', jobId }),
    mutateAsync: (jobId: string) => action.mutateAsync({ verb: 'delete', jobId }),
  }
}

export function usePauseCronJob(appId: Ref<string | undefined>) {
  const action = useCronJobAction(appId)
  return {
    ...action,
    mutate: (jobId: string) => action.mutate({ verb: 'pause', jobId }),
    mutateAsync: (jobId: string) => action.mutateAsync({ verb: 'pause', jobId }),
  }
}

export function useResumeCronJob(appId: Ref<string | undefined>) {
  const action = useCronJobAction(appId)
  return {
    ...action,
    mutate: (jobId: string) => action.mutate({ verb: 'resume', jobId }),
    mutateAsync: (jobId: string) => action.mutateAsync({ verb: 'resume', jobId }),
  }
}

export function useRunCronJob(appId: Ref<string | undefined>) {
  const action = useCronJobAction(appId)
  return {
    ...action,
    mutate: (jobId: string) => action.mutate({ verb: 'run', jobId }),
    mutateAsync: (jobId: string) => action.mutateAsync({ verb: 'run', jobId }),
  }
}

// 无 Query 后缀别名保留给页面代码按资源名直观引用。
export const useCronCapabilities = useCronCapabilitiesQuery
export const useCronStatus = useCronStatusQuery
export const useCronJobs = useCronJobsQuery
export const useCronJob = useCronJobQuery
export const useCronHistory = useCronHistoryQuery
export const useCronOutput = useCronOutputQuery

// 占位导出私有 key，便于后续需要批量失效时集中复用。
export const _cronKeys = { cronCapabilitiesKey, cronOutputPrefixKey }
