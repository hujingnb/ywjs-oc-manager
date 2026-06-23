// useKanban.ts —— 实例任务看板的 API hooks。
// 数据来自 manager 的 /api/v1/apps/{appId}/hermes/kanban/* 端点。
// 读操作用 useQuery（boards / tasks / task / runs），写操作用 useMutation（create / action）。
import { useQuery, useMutation, useQueryClient } from '@tanstack/vue-query'
import type { Ref } from 'vue'
import { apiRequest } from '@/api/client'
import { i18n } from '@/i18n'

// Kanban 任务状态枚举（与 hermes kanban 状态机一致）。
// 状态流转：triage → todo → ready → running → blocked/done/archived
export type KanbanStatus =
  | 'triage' | 'todo' | 'ready' | 'running' | 'blocked' | 'done' | 'archived'

// KanbanBoard 是一个看板（对应 service.KanbanBoard）。
// slug 是 board 的唯一标识，在 query 参数里传递。
export interface KanbanBoard {
  slug?: string
  name?: string
  description?: string
  icon?: string
  color?: string
  archived?: boolean
  is_current?: boolean
  // counts 是 board 内各状态的任务计数，key 为状态名。
  counts?: Record<string, number>
  total?: number
}

// KanbanTask 是列表视图的任务（对应 service.KanbanTask）。
// 列表接口只返回核心字段，完整详情需调用 task 详情接口。
export interface KanbanTask {
  id?: string
  title?: string
  // status 后端为 string，前端以 KanbanStatus 联合类型约束合法值，
  // 但保留 string fallback 以防 hermes 新增状态时不 break 前端。
  status?: KanbanStatus | string
  assignee?: string
  priority?: number
  body?: string
  tenant?: string
  workspace_kind?: string
  workspace_path?: string
  created_by?: string
  created_at?: number
  started_at?: number
  completed_at?: number
  result?: string
  // skills 是任务所需技能列表，为字符串数组（hermes v0.14.0 真实结构）。
  skills?: string[]
  max_retries?: number
}

// KanbanComment 对应任务详情里的一条评论（service.KanbanComment）。
export interface KanbanComment {
  author?: string
  body?: string
  created_at?: number
}

// KanbanEvent 对应任务事件流的一条事件（service.KanbanEvent）。
// payload 结构随 kind 变化（任意对象），用 unknown 类型表达。
export interface KanbanEvent {
  // task_id 是事件所属任务 ID。watch 流的事件必带（前端按 task 分组依赖它）；
  // TaskDetail.events 单任务上下文里可为空。
  task_id?: string
  kind?: string
  // payload 是任意 JSON 对象，结构随 kind 变化，前端不解析具体字段。
  payload?: unknown
  created_at?: number
  // run_id 是关联的执行 ID，类型未经实测确定（真实环境多为 null），前端不解析。
  run_id?: unknown
}

// KanbanTaskRun 对应 `hermes kanban runs <id> --json` 的一次历史执行（service.KanbanTaskRun）。
export interface KanbanTaskRun {
  profile?: string
  status?: string
  worker_pid?: number
  started_at?: number
  ended_at?: number
  outcome?: string
  summary?: string
  error?: string
}

// KanbanTaskDetail 对应 `hermes kanban show <id> --json` 的完整任务详情（service.KanbanTaskDetail）。
// 真实结构：任务核心字段嵌在 task 子对象，顶层不再平铺任务字段。
// 对应 Go 的 KanbanTaskDetail，show 输出: { task, latest_summary, parents, children, comments, events }
export interface KanbanTaskDetail {
  // task 是任务核心字段，嵌套在 show 输出的顶层 "task" 子对象内。
  task?: KanbanTask
  latest_summary?: string
  parents?: string[]
  children?: string[]
  comments?: KanbanComment[]
  events?: KanbanEvent[]
}

// KanbanStats 对应 `hermes kanban stats --json`，用于工具栏徽标展示（service.KanbanStats）。
// 字段已按真实 CLI 输出校准（hermes v0.14.0）。
export interface KanbanStats {
  // by_status 是各状态的任务计数，key 为状态名。
  by_status?: Record<string, number>
  // by_assignee 是各 assignee 下各状态的任务计数，外层 key 为 assignee，内层 key 为状态名。
  by_assignee?: Record<string, Record<string, number>>
  oldest_ready_age_seconds?: number
  now?: number
}

// KanbanFeatures 是 oc-kanban 的细粒度能力开关（对应 service.KanbanFeatures）。
export interface KanbanFeatures {
  write?: boolean
  watch?: boolean
  runs?: boolean
  stats?: boolean
}

// KanbanCapabilities 是 oc-kanban 的自描述能力（对应 service.KanbanCapabilities）。
// 前端据此降级：隐藏不支持的操作按钮、stats 徽标等。
export interface KanbanCapabilities {
  contract_version?: string
  oc_kanban_version?: string
  hermes_version?: string
  variant?: string
  // verbs 是本镜像实际支持的功能 verb 清单。
  verbs?: string[]
  features?: KanbanFeatures
}

// ─── queryKey 约定 ───────────────────────────────────────────────────
// 统一以 ['kanban', 子类, appId, ...] 为前缀，便于 mutation 精准失效查询缓存。
//
// 设计说明：
// - query hook（useQuery）的 queryKey 直接传 Ref，TanStack Vue Query 会自动解包
//   Ref 并做响应式追踪，Ref 值变化时自动重新请求。
// - 这些 helper 函数用于 invalidateQueries 等命令式场景，接收已解包的原始值
//   （string | undefined），不能传 Ref，否则匹配不到已存储的（已解包的）queryKey，
//   导致失效无效。
const boardsKey = (appId: string | undefined) => ['kanban', 'boards', appId] as const
const tasksKey = (appId: string | undefined, board: string) =>
  ['kanban', 'tasks', appId, board] as const
const taskKey = (appId: string | undefined, board: string, taskId: string) =>
  ['kanban', 'task', appId, board, taskId] as const
const runsKey = (appId: string | undefined, board: string, taskId: string | undefined) =>
  ['kanban', 'runs', appId, board, taskId] as const
const statsKey = (appId: string | undefined, board: string) =>
  ['kanban', 'stats', appId, board] as const

// ─── 读 query hooks（E1）──────────────────────────────────────────────

// useKanbanBoardsQuery 拉取实例的所有 board。
// appId 为空时暂停请求，防止向 undefined 路径发起请求。
export function useKanbanBoardsQuery(appId: Ref<string | undefined>) {
  return useQuery<KanbanBoard[]>({
    queryKey: ['kanban', 'boards', appId],
    enabled: () => Boolean(appId.value),
    queryFn: async () => {
      const res = await apiRequest<{ boards: KanbanBoard[] }>(
        `/api/v1/apps/${appId.value}/hermes/kanban/boards`,
      )
      return res.boards ?? []
    },
  })
}

// useKanbanTasksQuery 拉取某 board 的任务列表，每 5s 轮询一次。
// board 参数通过 query string 传递，为空字符串时后端默认使用 "default" board。
// stub 实例不支持看板（后端返回 KANBAN_NOT_SUPPORTED_ON_STUB），检测到该错误后
// 停止轮询，避免每 5s 重复打 503 并持续刷新 console 错误。
export function useKanbanTasksQuery(appId: Ref<string | undefined>, board: Ref<string>) {
  return useQuery<KanbanTask[]>({
    queryKey: ['kanban', 'tasks', appId, board],
    enabled: () => Boolean(appId.value),
    refetchInterval: (query) => {
      // stub 实例返回 KANBAN_NOT_SUPPORTED_ON_STUB 错误码时停止轮询。
      // 读取 ApiError 的 body.code 字段判断是否为 stub 实例。
      const err = query.state.error as { body?: { code?: string } } | null | undefined
      if (err?.body?.code === 'KANBAN_NOT_SUPPORTED_ON_STUB') return false
      // 正常实例每 5s 轮询一次。
      return 5000
    },
    queryFn: async () => {
      const res = await apiRequest<{ tasks: KanbanTask[] }>(
        `/api/v1/apps/${appId.value}/hermes/kanban/tasks`,
        { query: { board: board.value } },
      )
      // 过滤掉后端返回的无 id 任务，从数据源头保证组件层可安全使用 task.id。
      return (res.tasks ?? []).filter((t) => t.id)
    },
  })
}

// useKanbanTaskQuery 拉取单个任务详情（含评论、事件等扩展字段）。
// taskId 为空时禁用 query；taskId 变化时自动重新拉取。
export function useKanbanTaskQuery(
  appId: Ref<string | undefined>,
  board: Ref<string>,
  taskId: Ref<string | undefined>,
) {
  return useQuery<KanbanTaskDetail | null>({
    queryKey: ['kanban', 'task', appId, board, taskId],
    enabled: () => Boolean(appId.value && taskId.value),
    queryFn: async () => {
      // taskId 为空时返回 null 而非发起请求，与 enabled 逻辑双保险。
      if (!taskId.value) return null
      const res = await apiRequest<{ task: KanbanTaskDetail }>(
        `/api/v1/apps/${appId.value}/hermes/kanban/tasks/${taskId.value}`,
        { query: { board: board.value } },
      )
      return res.task
    },
  })
}

// useKanbanRunsQuery 拉取任务的历次执行记录。
// 仅在 appId 和 taskId 都存在时请求；用于任务详情面板的「执行历史」区域。
export function useKanbanRunsQuery(
  appId: Ref<string | undefined>,
  board: Ref<string>,
  taskId: Ref<string | undefined>,
) {
  return useQuery<KanbanTaskRun[]>({
    queryKey: ['kanban', 'runs', appId, board, taskId],
    enabled: () => Boolean(appId.value && taskId.value),
    queryFn: async () => {
      // taskId 为空时短路返回空数组，避免请求路径携带 undefined。
      if (!taskId.value) return []
      const res = await apiRequest<{ runs: KanbanTaskRun[] }>(
        `/api/v1/apps/${appId.value}/hermes/kanban/tasks/${taskId.value}/runs`,
        { query: { board: board.value } },
      )
      return res.runs ?? []
    },
  })
}

// useKanbanStatsQuery 拉取某 board 的统计信息，每 5s 轮询。
// 用于工具栏统计徽标（任务总数 + 最老就绪任务等待时长）。
// stub 实例返回 KANBAN_NOT_SUPPORTED_ON_STUB 时停止轮询，与 useKanbanTasksQuery 一致。
export function useKanbanStatsQuery(appId: Ref<string | undefined>, board: Ref<string>) {
  return useQuery<KanbanStats | null>({
    queryKey: ['kanban', 'stats', appId, board],
    enabled: () => Boolean(appId.value),
    refetchInterval: (query) => {
      // stub 实例返回 KANBAN_NOT_SUPPORTED_ON_STUB 错误码时停止轮询。
      const err = query.state.error as { body?: { code?: string } } | null | undefined
      if (err?.body?.code === 'KANBAN_NOT_SUPPORTED_ON_STUB') return false
      return 5000
    },
    queryFn: async () => {
      const res = await apiRequest<{ stats: KanbanStats }>(
        `/api/v1/apps/${appId.value}/hermes/kanban/stats`,
        { query: { board: board.value } },
      )
      return res.stats ?? null
    },
  })
}

// useKanbanCapabilitiesQuery 探测实例 oc-kanban 的契约版本与可用能力。
// capabilities 在实例生命周期内不变，故 staleTime 设为 Infinity、不轮询、不重试；
// stub 实例返回错误时查询失败，前端按既有 stub 降级路径处理。
export function useKanbanCapabilitiesQuery(appId: Ref<string | undefined>) {
  return useQuery<KanbanCapabilities | null>({
    queryKey: ['kanban', 'capabilities', appId],
    enabled: () => Boolean(appId.value),
    staleTime: Infinity,
    retry: false,
    queryFn: async () => {
      const res = await apiRequest<{ capabilities: KanbanCapabilities }>(
        `/api/v1/apps/${appId.value}/hermes/kanban/capabilities`,
      )
      return res.capabilities ?? null
    },
  })
}

// 给后续 Task E2 / G1 用的导出 queryKey 工具，避免调用方手写字面量。
export { boardsKey, tasksKey, taskKey, runsKey, statsKey }

// ─── 写 mutation hooks（E2）──────────────────────────────────────────

// useCreateKanbanTask 新建任务，成功后失效任务列表缓存触发重新拉取。
export function useCreateKanbanTask(appId: Ref<string | undefined>, board: Ref<string>) {
  const client = useQueryClient()
  return useMutation({
    mutationFn: async (payload: {
      title: string
      assignee: string
      priority: number
      body?: string
      // skills 是字符串数组，对应后端 []string，每项对应一个 --skill 参数。
      skills?: string[]
      // workspace 是单个字段，接受 scratch / worktree / dir:<path> 三种形式，
      // 对应后端 CreateKanbanTaskRequest.workspace（已合并原 workspace_kind + workspace_path）。
      workspace?: string
      parent_id?: string
      max_retries?: number
    }) => {
      // appId 为空时拒绝执行，避免向错误路径发起请求。
      if (!appId.value) throw new Error(i18n.global.t('common.errors.missingAppId'))
      const res = await apiRequest<{ task: KanbanTaskDetail }>(
        `/api/v1/apps/${appId.value}/hermes/kanban/tasks`,
        { method: 'POST', body: { board: board.value, ...payload } },
      )
      return res.task
    },
    onSuccess: () => {
      // 新建任务后立即失效任务列表，让轮询逻辑刷新数据而不等待下次 interval。
      // 注意：invalidateQueries 是命令式调用，queryKey 必须传解包后的原始值，
      // 直接传 Ref 对象无法匹配已存储的（已解包的）queryKey，会导致失效无效。
      void client.invalidateQueries({ queryKey: tasksKey(appId.value, board.value) })
      void client.invalidateQueries({ queryKey: statsKey(appId.value, board.value) })
    },
  })
}

// KanbanWriteAction 是除 create 外所有任务写操作的联合类型。
// 通过 verb 字段区分操作类型，其余字段随操作类型变化。
// 导出供组件（如 KanbanTaskActions.vue）复用，避免重复定义。
export type KanbanWriteAction =
  | { verb: 'comment'; taskId: string; body: string }
  | { verb: 'complete'; taskId: string; result?: string }
  | { verb: 'block'; taskId: string; reason: string }
  | { verb: 'unblock'; taskId: string }
  | { verb: 'archive'; taskId: string }
  | { verb: 'reassign'; taskId: string; to: string }
  | { verb: 'reclaim'; taskId: string }

// useKanbanTaskAction 是统一的任务写操作 mutation（comment/complete/block/...）。
// 单 hook 覆盖所有非 create 写操作。oc-kanban 写操作返回更新后的完整 TaskDetail，
// 成功后直接写入详情缓存（详情面板即时刷新），并失效列表与统计缓存。
export function useKanbanTaskAction(appId: Ref<string | undefined>, board: Ref<string>) {
  const client = useQueryClient()
  return useMutation({
    mutationFn: async (action: KanbanWriteAction) => {
      // appId 为空时拒绝执行，避免向错误路径发起请求。
      if (!appId.value) throw new Error(i18n.global.t('common.errors.missingAppId'))
      // 解构 verb 和 taskId 作为 URL 路径参数，剩余字段作为请求体追加 board。
      const { verb, taskId, ...rest } = action
      const res = await apiRequest<{ task: KanbanTaskDetail }>(
        `/api/v1/apps/${appId.value}/hermes/kanban/tasks/${taskId}/${verb}`,
        { method: 'POST', body: { board: board.value, ...rest } },
      )
      return res.task
    },
    onSuccess: (detail, action) => {
      // 写操作返回权威 TaskDetail，直接写入详情缓存，无需再失效详情查询。
      if (detail) {
        client.setQueryData(taskKey(appId.value, board.value, action.taskId), detail)
      }
      // 状态/计数变化仍需失效任务列表与统计徽标缓存。
      void client.invalidateQueries({ queryKey: tasksKey(appId.value, board.value) })
      void client.invalidateQueries({ queryKey: statsKey(appId.value, board.value) })
    },
  })
}
