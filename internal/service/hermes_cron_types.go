// Package service —— hermes_cron_types.go 定义 oc-cron 服务层的业务输入 / 过滤
// 类型。oc-cron 暴露的线缆契约 DTO（CronJob / CronStatus / CronCapabilities 等）
// 已迁至 internal/integrations/ocops 包（契约属主），service 引用 ocops.X。
// 本文件仅保留 service 专属、不上线缆的 Input / Filter 类型。
package service

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
