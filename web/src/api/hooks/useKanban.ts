// useKanban.ts —— 实例任务看板的 API hooks。
// 数据来自 manager 的 /api/v1/apps/{appId}/hermes/kanban/* 端点。
// 读操作用 useQuery（boards / tasks / task / runs），写操作用 useMutation（create / action）。
import { useQuery, useMutation, useQueryClient } from '@tanstack/vue-query'
import type { Ref } from 'vue'
import { apiRequest } from '@/api/client'

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
  archived?: boolean
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
  created_at?: number
  started_at?: number
  completed_at?: number
  skills?: string
}

// KanbanComment 对应任务详情里的一条评论（service.KanbanComment）。
export interface KanbanComment {
  author?: string
  body?: string
  created_at?: number
}

// KanbanEvent 对应任务事件流的一条事件（service.KanbanEvent）。
// 由 hermes kanban watch 的 NDJSON 流逐条推送。
export interface KanbanEvent {
  kind?: string
  payload?: string
  created_at?: number
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
// 在 KanbanTask 基础上补 worker / workspace / 评论 / 事件等扩展字段。
export interface KanbanTaskDetail extends KanbanTask {
  workspace_kind?: string
  workspace_path?: string
  worker_pid?: number
  last_heartbeat_at?: number
  parent_id?: string
  result?: string
  comments?: KanbanComment[]
  events?: KanbanEvent[]
}

// KanbanStats 对应 `hermes kanban stats --json`，用于工具栏徽标展示（service.KanbanStats）。
// status_counts 是各状态的任务计数 map，key 为 KanbanStatus 值。
export interface KanbanStats {
  status_counts?: Record<string, number>
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
export function useKanbanTasksQuery(appId: Ref<string | undefined>, board: Ref<string>) {
  return useQuery<KanbanTask[]>({
    queryKey: ['kanban', 'tasks', appId, board],
    enabled: () => Boolean(appId.value),
    refetchInterval: 5000,
    queryFn: async () => {
      const res = await apiRequest<{ tasks: KanbanTask[] }>(
        `/api/v1/apps/${appId.value}/hermes/kanban/tasks`,
        { query: { board: board.value } },
      )
      return res.tasks ?? []
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

// 给后续 Task E2 / G1 用的导出 queryKey 工具，避免调用方手写字面量。
export { boardsKey, tasksKey, taskKey, runsKey }

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
      skills?: string
      workspace_kind?: string
      workspace_path?: string
      parent_id?: string
      max_retries?: number
    }) => {
      // appId 为空时拒绝执行，避免向错误路径发起请求。
      if (!appId.value) throw new Error('缺少实例 ID')
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
// 单 hook 覆盖所有非 create 写操作，避免为每个 verb 重复定义几乎相同的 hook。
// 成功后同时失效任务列表（badge 计数）和任务详情（状态/评论变化）两个缓存。
export function useKanbanTaskAction(appId: Ref<string | undefined>, board: Ref<string>) {
  const client = useQueryClient()
  return useMutation({
    mutationFn: async (action: KanbanWriteAction) => {
      // appId 为空时拒绝执行，避免向错误路径发起请求。
      if (!appId.value) throw new Error('缺少实例 ID')
      // 解构 verb 和 taskId 作为 URL 路径参数，剩余字段作为请求体追加 board。
      const { verb, taskId, ...rest } = action
      await apiRequest<void>(
        `/api/v1/apps/${appId.value}/hermes/kanban/tasks/${taskId}/${verb}`,
        { method: 'POST', body: { board: board.value, ...rest } },
      )
    },
    onSuccess: (_data, action) => {
      // 任务状态或内容变化，同时失效列表缓存（状态计数徽标）和详情缓存。
      // 注意：invalidateQueries 是命令式调用，queryKey 必须传解包后的原始值，
      // 直接传 Ref 对象无法匹配已存储的（已解包的）queryKey，会导致失效无效。
      void client.invalidateQueries({ queryKey: tasksKey(appId.value, board.value) })
      void client.invalidateQueries({
        queryKey: taskKey(appId.value, board.value, action.taskId),
      })
    },
  })
}
