// Package service —— hermes_kanban_types.go 定义 Hermes Kanban CLI --json
// 输出对应的强类型。字段名以 hermes kanban CLI 输出为准，解析时未知字段忽略，
// 缺失字段取零值，避免上游小版本变化直接 break。
package service

// KanbanBoard 对应 `hermes kanban boards list --json` 的单个 board。
type KanbanBoard struct {
	Slug        string `json:"slug"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Archived    bool   `json:"archived,omitempty"`
}

// KanbanTask 对应 `hermes kanban list --json` 的单个任务（列表视图字段）。
type KanbanTask struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Status      string `json:"status"`   // triage|todo|ready|running|blocked|done|archived
	Assignee    string `json:"assignee"`
	Priority    int    `json:"priority"`
	Body        string `json:"body,omitempty"`
	CreatedAt   int64  `json:"created_at"`
	StartedAt   int64  `json:"started_at,omitempty"`
	CompletedAt int64  `json:"completed_at,omitempty"`
	Skills      string `json:"skills,omitempty"`
}

// KanbanComment 对应任务详情里的一条评论。
type KanbanComment struct {
	Author    string `json:"author"`
	Body      string `json:"body"`
	CreatedAt int64  `json:"created_at"`
}

// KanbanEvent 对应任务事件流的一条事件（task_events / watch 输出）。
type KanbanEvent struct {
	Kind      string `json:"kind"`
	Payload   string `json:"payload,omitempty"`
	CreatedAt int64  `json:"created_at"`
}

// KanbanTaskRun 对应 `hermes kanban runs <id> --json` 的一次历史执行。
type KanbanTaskRun struct {
	Profile   string `json:"profile"`
	Status    string `json:"status"`
	WorkerPID int    `json:"worker_pid,omitempty"`
	StartedAt int64  `json:"started_at"`
	EndedAt   int64  `json:"ended_at,omitempty"`
	Outcome   string `json:"outcome,omitempty"`
	Summary   string `json:"summary,omitempty"`
	Error     string `json:"error,omitempty"`
}

// KanbanTaskDetail 对应 `hermes kanban show <id> --json` 的完整任务详情。
// 在 KanbanTask 基础上补 worker / workspace / 评论 / 事件等。
type KanbanTaskDetail struct {
	KanbanTask
	WorkspaceKind   string          `json:"workspace_kind,omitempty"`
	WorkspacePath   string          `json:"workspace_path,omitempty"`
	WorkerPID       int             `json:"worker_pid,omitempty"`
	LastHeartbeatAt int64           `json:"last_heartbeat_at,omitempty"`
	ParentID        string          `json:"parent_id,omitempty"`
	Result          string          `json:"result,omitempty"`
	Comments        []KanbanComment `json:"comments,omitempty"`
	Events          []KanbanEvent   `json:"events,omitempty"`
}

// KanbanStats 对应 `hermes kanban stats --json`，用于工具栏徽标。
// 用 map 承接 per-status 计数，避免上游状态枚举变化。
type KanbanStats struct {
	StatusCounts map[string]int `json:"status_counts"`
}
