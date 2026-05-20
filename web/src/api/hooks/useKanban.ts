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
const boardsKey = (appId: string | undefined) => ['kanban', 'boards', appId] as const
const tasksKey = (appId: string | undefined, board: string) =>
  ['kanban', 'tasks', appId, board] as const
const taskKey = (appId: string | undefined, board: string, taskId: string) =>
  ['kanban', 'task', appId, board, taskId] as const

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
export { boardsKey, tasksKey, taskKey }
