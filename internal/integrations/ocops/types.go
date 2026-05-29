// Package ocops —— types.go 定义 oc-ops（oc-cron / oc-kanban）稳定信封 data
// 对应的线缆契约 DTO。manager 不直接解析 hermes cron/kanban 内部文件，只依赖
// oc-ops 暴露的 manager-facing 契约字段。
//
// 这些类型是契约属主：ocops HTTP 客户端返回它们，service / handlers 引用
// ocops.X（service → ocops 单向依赖，避免 service↔ocops 循环）。JSON tag 即
// 线缆字段名，跨进程契约稳定，迁移过程中保持不变。
package ocops

// CronSchedule 描述 CronJob.schedule 的稳定结构。
type CronSchedule struct {
	// Kind 是调度表达式类别，例如 cron / every / at；旧数据可能为空。
	Kind string `json:"kind"`
	// Expr 是机器可读表达式；oc-cron 可返回 null，service 层用空字符串承接。
	Expr string `json:"expr,omitempty"`
	// Display 是面向用户展示的调度描述。
	Display string `json:"display"`
}

// CronRepeat 描述任务重复次数与已完成次数；Times 为 nil 表示无限重复。
type CronRepeat struct {
	// Times 是总重复次数，nil 表示不限制次数。
	Times *int `json:"times,omitempty"`
	// Completed 是已完成的调度次数。
	Completed int `json:"completed"`
}

// CronJob 对应 oc-cron list/show/create/update/toggle/run 返回的任务对象。
type CronJob struct {
	// ID 是 Cron 任务唯一标识，最多 64 个安全字符。
	ID string `json:"id"`
	// Name 是任务显示名称。
	Name string `json:"name"`
	// Prompt 是任务运行时交给 Hermes 的提示词，可为空。
	Prompt string `json:"prompt,omitempty"`
	// Schedule 是规整后的调度表达式。
	Schedule CronSchedule `json:"schedule"`
	// Repeat 描述重复次数和完成次数。
	Repeat CronRepeat `json:"repeat"`
	// Enabled 表示调度器是否会自动触发该任务。
	Enabled bool `json:"enabled"`
	// State 是调度状态，如 scheduled / paused / disabled / removed。
	State string `json:"state"`
	// CreatedAt 是任务创建时间，保持 oc-cron 输出字符串以避免时区二次解释。
	CreatedAt string `json:"created_at,omitempty"`
	// NextRunAt 是下一次计划运行时间，可为空。
	NextRunAt string `json:"next_run_at,omitempty"`
	// LastRunAt 是最近一次运行时间，可为空。
	LastRunAt string `json:"last_run_at,omitempty"`
	// LastStatus 是最近一次运行状态，可为空。
	LastStatus string `json:"last_status,omitempty"`
	// LastError 是最近一次执行错误摘要，可为空。
	LastError string `json:"last_error,omitempty"`
	// LastDeliveryError 是最近一次投递错误摘要，可为空。
	LastDeliveryError string `json:"last_delivery_error,omitempty"`
	// Deliver 是任务输出投递目标，例如 wechat；为空表示不投递。
	Deliver string `json:"deliver,omitempty"`
	// Script 是任务使用的仓库内脚本文件名，可为空。
	Script string `json:"script,omitempty"`
	// NoAgent 表示任务是否跳过 agent 执行路径。
	NoAgent bool `json:"no_agent,omitempty"`
	// Workdir 是任务运行目录；平台管理员高级字段，handler 层可按角色过滤。
	Workdir string `json:"workdir,omitempty"`
	// Skills 是任务声明需要的技能列表；平台管理员高级字段。
	Skills []string `json:"skills,omitempty"`
	// Model 是任务指定模型；平台管理员高级字段。
	Model string `json:"model,omitempty"`
	// Provider 是任务指定模型提供方；平台管理员高级字段。
	Provider string `json:"provider,omitempty"`
	// BaseURL 是任务指定 provider base URL；平台管理员高级字段。
	BaseURL string `json:"base_url,omitempty"`
}

// CronStatus 对应 oc-cron status 的调度器摘要。
type CronStatus struct {
	// Available 表示 oc-cron 能读取 Cron 状态。
	Available bool `json:"available"`
	// GatewayRunning 表示 hermes cron status 命令当前是否成功。
	GatewayRunning bool `json:"gateway_running"`
	// ActiveJobs 是启用且未移除的任务数量。
	ActiveJobs int `json:"active_jobs"`
	// NextRunAt 是最近一次计划运行时间，可为空。
	NextRunAt string `json:"next_run_at,omitempty"`
	// NextJobID 是最近计划运行的任务 ID，可为空。
	NextJobID string `json:"next_job_id,omitempty"`
	// TickSeconds 是调度器 tick 间隔；nil 表示当前镜像未暴露。
	TickSeconds *int `json:"tick_seconds,omitempty"`
	// PID 是调度器进程 ID；nil 表示当前镜像未暴露。
	PID *int `json:"pid,omitempty"`
	// Message 是底层 hermes cron status 的人类可读摘要。
	Message string `json:"message,omitempty"`
	// LastError 是最近的调度错误摘要，可为空。
	LastError string `json:"last_error,omitempty"`
	// LastErrorJobID 是最近错误对应的任务 ID，可为空。
	LastErrorJobID string `json:"last_error_job_id,omitempty"`
}

// CronRunEntry 对应 oc-cron history 的单条运行输出记录。
type CronRunEntry struct {
	// JobID 是所属 Cron 任务 ID。
	JobID string `json:"job_id"`
	// FileName 是输出 markdown 文件名，或 synthetic 元数据文件名。
	FileName string `json:"file_name"`
	// RunTime 是本次运行时间字符串。
	RunTime string `json:"run_time,omitempty"`
	// Size 是输出文件字节数。
	Size int64 `json:"size"`
	// HasOutput 表示是否存在真实 markdown 输出文件。
	HasOutput bool `json:"has_output"`
	// Synthetic 表示该记录是否由调度元数据合成。
	Synthetic bool `json:"synthetic"`
	// Status 是运行状态，可为空。
	Status string `json:"status,omitempty"`
	// Error 是运行错误摘要，可为空。
	Error string `json:"error,omitempty"`
}

// CronRunOutput 对应 oc-cron output 返回的 markdown 内容。
type CronRunOutput struct {
	// JobID 是所属 Cron 任务 ID。
	JobID string `json:"job_id"`
	// FileName 是输出 markdown 文件名。
	FileName string `json:"file_name"`
	// RunTime 是本次运行时间字符串。
	RunTime string `json:"run_time,omitempty"`
	// Content 是 markdown 文本内容，oc-cron 限制最大 1 MiB。
	Content string `json:"content"`
}

// CronFeatures 描述 oc-cron 的细粒度能力开关。
type CronFeatures struct {
	// Status 表示支持调度器状态查询。
	Status bool `json:"status"`
	// History 表示支持查询运行输出历史。
	History bool `json:"history"`
	// Output 表示支持读取单次运行输出。
	Output bool `json:"output"`
	// Write 表示支持写操作（create/update/delete/toggle/run）。
	Write bool `json:"write"`
	// Script 表示支持 script 字段。
	Script bool `json:"script"`
	// AdvancedFields 表示支持 workdir/skills/model/provider/base_url 等高级字段。
	AdvancedFields bool `json:"advanced_fields"`
}

// CronCapabilities 对应 oc-cron capabilities 的 data 段。
type CronCapabilities struct {
	// ContractVersion 是 oc-cron 契约版本号。
	ContractVersion string `json:"contract_version"`
	// OCCronVersion 是 oc-cron 适配层版本。
	OCCronVersion string `json:"oc_cron_version"`
	// HermesVersion 是底层 hermes 版本或构建 ref，可为空。
	HermesVersion string `json:"hermes_version,omitempty"`
	// Variant 是镜像变体标识。
	Variant string `json:"variant"`
	// Verbs 是本镜像支持的 manager-facing verb 清单。
	Verbs []string `json:"verbs"`
	// Features 是细粒度能力开关。
	Features CronFeatures `json:"features"`
}

// KanbanBoard 对应 `hermes kanban boards list --json` 的单个 board。
// 字段已按真实 CLI 输出校准（hermes v0.14.0）。
type KanbanBoard struct {
	// Slug 是 board 唯一标识，形如 "default"。
	Slug string `json:"slug"`
	// Name 是 board 显示名称。
	Name string `json:"name"`
	// Description 是 board 描述，可为空。
	Description string `json:"description,omitempty"`
	// Icon 是 board 图标字符串，可为空。
	Icon string `json:"icon,omitempty"`
	// Color 是 board 颜色标识，可为空。
	Color string `json:"color,omitempty"`
	// Archived 标记 board 是否已归档。
	Archived bool `json:"archived,omitempty"`
	// IsCurrent 标记是否为当前活动 board。
	IsCurrent bool `json:"is_current,omitempty"`
	// Counts 是 board 内各状态的任务计数，key 为状态名，value 为数量。
	Counts map[string]int `json:"counts,omitempty"`
	// Total 是 board 内任务总数。
	Total int `json:"total,omitempty"`
}

// KanbanTask 对应 `hermes kanban list --json` 和 `hermes kanban create ... --json`
// 的单个任务对象。字段已按真实 CLI 输出校准（hermes v0.14.0）。
// 可空字段（body/tenant/workspace_path/started_at/completed_at/result/max_retries）
// 用值类型 + omitempty：hermes 输出 null 时 json.Unmarshal 保持零值，可接受。
type KanbanTask struct {
	// ID 是任务唯一标识，形如 "t_85620ed7"。
	ID string `json:"id"`
	// Title 是任务标题。
	Title string `json:"title"`
	// Body 是任务描述，可为 null（零值为空字符串）。
	Body string `json:"body,omitempty"`
	// Assignee 是当前分配的 hermes profile 名称。
	Assignee string `json:"assignee"`
	// Status 是任务状态（triage|todo|ready|running|blocked|done|archived）。
	Status string `json:"status"`
	// Priority 是任务优先级（0-9）。
	Priority int `json:"priority"`
	// Tenant 是多租户标识，可为 null（零值为空字符串）。
	Tenant string `json:"tenant,omitempty"`
	// WorkspaceKind 是 workspace 类型（scratch|dir|worktree）。
	WorkspaceKind string `json:"workspace_kind,omitempty"`
	// WorkspacePath 是 workspace 路径，可为 null（零值为空字符串）。
	WorkspacePath string `json:"workspace_path,omitempty"`
	// CreatedBy 是任务创建方（"user" 或 profile 名）。
	CreatedBy string `json:"created_by,omitempty"`
	// CreatedAt 是任务创建时间戳（Unix 秒）。
	CreatedAt int64 `json:"created_at"`
	// StartedAt 是任务开始执行时间戳（Unix 秒），可为 null（零值为 0）。
	StartedAt int64 `json:"started_at,omitempty"`
	// CompletedAt 是任务完成时间戳（Unix 秒），可为 null（零值为 0）。
	CompletedAt int64 `json:"completed_at,omitempty"`
	// Result 是任务完成结果摘要，可为 null（零值为空字符串）。
	Result string `json:"result,omitempty"`
	// Skills 是任务所需技能列表，为字符串数组（空任务时为 []）。
	Skills []string `json:"skills,omitempty"`
	// MaxRetries 是最大重试次数，可为 null（零值为 0）。
	MaxRetries int `json:"max_retries,omitempty"`
}

// KanbanComment 对应任务详情里的一条评论。
// 字段已按真实 CLI show 输出校准（hermes v0.14.0）。
type KanbanComment struct {
	// Author 是评论作者（hermes profile 名）。
	Author string `json:"author"`
	// Body 是评论内容。
	Body string `json:"body"`
	// CreatedAt 是评论创建时间戳（Unix 秒）。
	CreatedAt int64 `json:"created_at"`
}

// KanbanEvent 对应任务事件流的一条事件（show 输出的 events 数组元素）。
// 字段已按真实 CLI show 输出校准（hermes v0.14.0）。
type KanbanEvent struct {
	// TaskID 是事件所属任务 ID。watch 流的事件必带（前端按 task 分组依赖它）；
	// TaskDetail.events 单任务上下文里可为空，故 omitempty。
	TaskID string `json:"task_id,omitempty"`
	// Kind 是事件类型，如 "created"、"status_changed" 等。
	Kind string `json:"kind"`
	// Payload 是事件附加数据，结构随 Kind 变化（任意对象）。
	// 用 any 类型（swag 可正确解析为 object），json.Unmarshal 会把 JSON 对象解为 map[string]any。
	Payload any `json:"payload,omitempty"`
	// CreatedAt 是事件创建时间戳（Unix 秒）。
	CreatedAt int64 `json:"created_at"`
	// RunID 是关联的执行 ID，可为 null。真实环境多为 null，类型未经实测确定，
	// 用 any 容忍 hermes 任意输出（整数 / 字符串 / null），manager 仅透传不解析。
	RunID any `json:"run_id,omitempty"`
}

// KanbanTaskDetail 对应 `hermes kanban show <id> --json` 的完整任务详情。
// 任务核心字段嵌在 task 子对象里（顶层不平铺），同时包含评论/事件等关联数据。
// 字段已按真实 CLI show 输出校准（hermes v0.14.0）。
type KanbanTaskDetail struct {
	// Task 是任务核心字段，对应 show 输出的顶层 "task" 子对象。
	Task KanbanTask `json:"task"`
	// LatestSummary 是最新执行摘要，可为 null（零值为空字符串）。
	LatestSummary string `json:"latest_summary,omitempty"`
	// Parents 是父任务 ID 列表（task id 字符串数组）。
	Parents []string `json:"parents,omitempty"`
	// Children 是子任务 ID 列表（task id 字符串数组）。
	Children []string `json:"children,omitempty"`
	// Comments 是任务评论列表。
	Comments []KanbanComment `json:"comments,omitempty"`
	// Events 是任务事件流列表。
	Events []KanbanEvent `json:"events,omitempty"`
}

// KanbanStats 对应 `hermes kanban stats --json`，用于工具栏徽标。
// 字段已按真实 CLI 输出校准（hermes v0.14.0）。
type KanbanStats struct {
	// ByStatus 是各状态的任务计数，key 为状态名，value 为任务数量。
	ByStatus map[string]int `json:"by_status"`
	// ByAssignee 是各 assignee 下各状态的任务计数，外层 key 为 assignee，内层 key 为状态名。
	ByAssignee map[string]map[string]int `json:"by_assignee,omitempty"`
	// OldestReadyAgeSeconds 是最老的 ready 状态任务已等待的秒数。
	OldestReadyAgeSeconds int64 `json:"oldest_ready_age_seconds,omitempty"`
	// Now 是 stats 生成时的 Unix 时间戳（秒），用于客户端计算相对时间。
	Now int64 `json:"now,omitempty"`
}

// KanbanTaskRun 对应 `hermes kanban runs <id> --json` 的一次历史执行记录。
// 注意：元素结构未经真实环境实测（空任务返回 []，无法观察实际字段），
// 以下字段为调研报告推测，待有实际运行过的任务时校准。
type KanbanTaskRun struct {
	// Profile 是执行该任务的 hermes profile 名称。
	Profile string `json:"profile"`
	// Status 是本次执行状态。
	Status string `json:"status"`
	// WorkerPID 是 worker 进程 ID，0 表示未知或已退出。
	WorkerPID int `json:"worker_pid,omitempty"`
	// StartedAt 是执行开始时间戳（Unix 秒）。
	StartedAt int64 `json:"started_at"`
	// EndedAt 是执行结束时间戳（Unix 秒），0 表示尚未结束。
	EndedAt int64 `json:"ended_at,omitempty"`
	// Outcome 是执行结果（如 "success"/"failure"）。
	Outcome string `json:"outcome,omitempty"`
	// Summary 是执行摘要文本。
	Summary string `json:"summary,omitempty"`
	// Error 是执行失败时的错误信息。
	Error string `json:"error,omitempty"`
}

// KanbanFeatures 描述 oc-kanban 的细粒度能力开关，对应 capabilities.features。
type KanbanFeatures struct {
	// Write 表示是否支持写操作（create/comment/...）。
	Write bool `json:"write"`
	// Watch 表示是否支持实时事件流。
	Watch bool `json:"watch"`
	// Runs 表示是否支持查询执行历史。
	Runs bool `json:"runs"`
	// Stats 表示是否支持统计。
	Stats bool `json:"stats"`
}

// KanbanCapabilities 对应 `oc-kanban capabilities` 的 data 段，
// 供 manager 探测实例 oc-kanban 的契约版本与可用能力、据此降级。
type KanbanCapabilities struct {
	// ContractVersion 是 oc-kanban 契约版本号（MAJOR.MINOR）。
	ContractVersion string `json:"contract_version"`
	// OCKanbanVersion 是 oc-kanban 实现版本。
	OCKanbanVersion string `json:"oc_kanban_version"`
	// HermesVersion 是底层 hermes 版本（信息性，可能为空）。
	HermesVersion string `json:"hermes_version,omitempty"`
	// Variant 是镜像变体标识。
	Variant string `json:"variant"`
	// Verbs 是本镜像实际支持的功能 verb 清单。
	Verbs []string `json:"verbs"`
	// Features 是细粒度能力开关。
	Features KanbanFeatures `json:"features"`
}
