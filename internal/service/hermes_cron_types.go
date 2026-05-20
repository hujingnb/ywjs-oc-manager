// Package service —— hermes_cron_types.go 定义 oc-cron 稳定信封 data
// 对应的服务层 DTO。manager 不直接解析 hermes cron 内部文件，只依赖 oc-cron
// 暴露的 manager-facing 契约字段。
package service

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

// CronJobFilter 是 ListJobs 的过滤条件。
type CronJobFilter struct {
	// All 为 true 时包含 disabled/removed 等非活动任务，对应 oc-cron list --all。
	All bool
}

// CreateCronJobInput 是创建 Cron 任务的输入。
// Name/Schedule 为基础必填字段；其余高级字段保留在 service DTO 中，供 handler/UI
// 后续按 platform_admin 权限决定是否透传，而不是在 service 层隐藏能力。
type CreateCronJobInput struct {
	Name     string
	Schedule string
	Prompt   string
	Deliver  string
	Repeat   *int
	Script   string
	NoAgent  bool
	Workdir  string
	Skills   []string
	Model    string
	Provider string
	BaseURL  string
}

// UpdateCronJobInput 是更新 Cron 任务的输入。
// 字符串字段用指针区分“未提交”和“提交空字符串”；handler 层后续可据此实现清空语义。
type UpdateCronJobInput struct {
	Name        *string
	Schedule    *string
	Prompt      *string
	Deliver     *string
	Repeat      *int
	Script      *string
	NoAgent     bool
	Agent       bool
	Workdir     *string
	Skills      []string
	ClearSkills bool
	Model       *string
	Provider    *string
	BaseURL     *string
}
