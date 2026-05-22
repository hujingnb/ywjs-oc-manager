// Package handlers 内的 dto.go 集中所有请求体类型。
// 类型导出（大写）以便 swag 扫描；命名前缀按业务对象归类。
// 字段定义、json tag、binding tag 与原非导出版本保持 1:1，业务逻辑不变。
package handlers

// ErrorResponse 统一错误返回体。
// code 是稳定的接口契约标识（SCREAMING_SNAKE_CASE），前端据此分流处理；
// message 是面向前端展示的安全文案，不包含底层密钥、SQL 或外部接口细节。
// handler 通过 apierror.New 构造响应体，本类型仅供 swag 注解引用。
type ErrorResponse struct {
	// Code 是稳定的接口契约标识，一旦发布只增不改。
	Code string `json:"code" example:"APP_NOT_FOUND"`
	// Message 是面向前端展示的安全错误文案。
	Message string `json:"message" example:"应用不存在"`
}

// ===== 认证 auth =====

// LoginRequest 用户名密码登录的请求体。
type LoginRequest struct {
	// OrgCode 是组织用户登录时填写的组织标识；平台管理员登录时留空。
	OrgCode string `json:"org_code"`
	// Username 是 manager 账号名，登录失败时不区分账号不存在和密码错误。
	Username string `json:"username" binding:"required"`
	// Password 是明文登录密码，仅用于本次校验，handler 不写日志。
	Password string `json:"password" binding:"required"`
}

// RefreshRequest 续期 access token 的请求体。
type RefreshRequest struct {
	// RefreshToken 是长生命周期刷新令牌，service 层只保存其 hash 并在刷新时轮换。
	RefreshToken string `json:"refresh_token" binding:"required"`
}

// ===== 组织 organizations =====

// CreateOrganizationRequest 创建组织的请求体。
type CreateOrganizationRequest struct {
	// Name 是组织展示名，也是平台管理员列表中识别租户的主字段。
	Name string `json:"name" binding:"required"`
	// Code 是组织登录标识，创建后不可修改。
	Code string `json:"code" binding:"required"`
	// ContactName 是业务联系人姓名，可为空。
	ContactName string `json:"contact_name"`
	// ContactPhone 是业务联系人电话，可为空；不参与权限或登录校验。
	ContactPhone string `json:"contact_phone"`
	// Remark 是平台管理员维护的内部备注。
	Remark string `json:"remark"`
	// CreditWarningThreshold 是组织余额预警阈值；nil 表示不启用余额预警或保持预警关闭。
	CreditWarningThreshold *int32 `json:"credit_warning_threshold"`
	// AssistantVersionIDs 是该组织可用的助手版本 id 列表（allowlist）。
	AssistantVersionIDs []string `json:"assistant_version_ids"`
	// ModelID 历史字段：助手版本接管模型选择后，组织创建不再需要它；
	// 保留以兼容旧调用方，前端新表单不再发送，留待后续阶段移除。
	ModelID string `json:"model_id"`
	// AdminUsername 是随组织创建的首个 org_admin 账号名。
	AdminUsername string `json:"admin_username" binding:"required"`
	// AdminDisplayName 是首个 org_admin 的显示名。
	AdminDisplayName string `json:"admin_display_name" binding:"required"`
	// AdminPassword 是首个 org_admin 的初始密码，service 层立即 hash 后写库。
	AdminPassword string `json:"admin_password" binding:"required"`
}

// OrganizationRequest 更新组织的请求体。
type OrganizationRequest struct {
	// Name 是组织展示名；更新时仍必填，避免空名称进入前端列表。
	Name string `json:"name" binding:"required"`
	// ContactName 是业务联系人姓名，可置空。
	ContactName string `json:"contact_name"`
	// ContactPhone 是业务联系人电话，可置空。
	ContactPhone string `json:"contact_phone"`
	// Remark 是平台管理员维护的内部备注，可置空。
	Remark string `json:"remark"`
	// CreditWarningThreshold 是组织余额预警阈值；nil 表示清空或未设置预警阈值。
	CreditWarningThreshold *int32 `json:"credit_warning_threshold"`
	// AssistantVersionIDs 是该组织可用的助手版本 id 列表（allowlist）。
	AssistantVersionIDs []string `json:"assistant_version_ids"`
	// ModelID 是该组织所有实例统一使用的模型 ID；nil 表示本次只更新基础资料并保留原模型。
	ModelID *string `json:"model_id"`
}

// ===== 成员 members =====

// CreateMemberRequest 创建组织成员的请求体。
type CreateMemberRequest struct {
	// Username 是组织内登录账号名，创建后不可通过成员更新接口修改。
	Username string `json:"username" binding:"required"`
	// DisplayName 是前端显示名，创建与更新时都不能为空。
	DisplayName string `json:"display_name" binding:"required"`
	// Password 是初始密码，service 层立即 hash 后写库。
	Password string `json:"password" binding:"required"`
	// Role 允许 org_admin / org_member；空值由 service 层补默认 org_member。
	Role string `json:"role"`
}

// UpdateMemberRequest 更新成员显示名或角色的请求体。
type UpdateMemberRequest struct {
	// DisplayName 是成员展示名，更新接口要求显式传入非空值。
	DisplayName string `json:"display_name" binding:"required"`
	// Role 为空表示保持原角色；非空时需要管理员权限并限制在组织角色内。
	Role string `json:"role"`
}

// ResetPasswordRequest 管理员重置成员密码的请求体。
type ResetPasswordRequest struct {
	// Password 是新密码，service 层只保存 hash。
	Password string `json:"password" binding:"required"`
}

// OnboardMemberRequest 在事务中创建成员并联动初始化应用的请求体。
type OnboardMemberRequest struct {
	// Username 是新成员账号名，与普通创建成员保持同一约束。
	Username string `json:"username" binding:"required"`
	// DisplayName 是新成员展示名。
	DisplayName string `json:"display_name" binding:"required"`
	// Password 是新成员初始密码。
	Password string `json:"password" binding:"required"`
	// Role 为空时默认为 org_member；不允许创建 platform_admin。
	Role string `json:"role"`
	// AppName 是随成员初始化的默认应用名称。
	AppName string `json:"app_name" binding:"required"`
	// ChannelType 是初始化渠道绑定的渠道标识。
	ChannelType string `json:"channel_type"`
	// NodeID 是指定 runtime 节点；为空时 service 自动选择可用节点。
	NodeID string `json:"runtime_node_id"`
	// VersionID 是实例绑定的助手版本 id，必须落在组织 allowlist 内。
	VersionID string `json:"version_id" binding:"required"`
}

// CreateMemberAppRequest 为已有成员创建新实例的请求体。
type CreateMemberAppRequest struct {
	// AppName 是新实例名称，创建时必填。
	AppName string `json:"app_name" binding:"required"`
	// ChannelType 是初始化渠道绑定的渠道标识。
	ChannelType string `json:"channel_type"`
	// NodeID 是指定 runtime 节点；为空时 service 自动选择可用节点。
	NodeID string `json:"runtime_node_id"`
	// VersionID 是实例绑定的助手版本 id，必须落在组织 allowlist 内。
	VersionID string `json:"version_id" binding:"required"`
}

// ===== 知识库 knowledge =====

// RetryOrgSyncRequest 重试组织知识库节点同步的请求体。
type RetryOrgSyncRequest struct {
	// NodeID 是需要重试同步的 runtime 节点 ID。
	NodeID string `json:"node_id" binding:"required"`
}

// ===== 充值 recharge =====

// RechargeRequest 组织充值的请求体。
type RechargeRequest struct {
	// CreditAmount 是充值额度，必须为正数，service 层会同步写入 new-api。
	CreditAmount int64 `json:"credit_amount" binding:"required"`
	// Remark 是本次充值的审计备注。
	Remark string `json:"remark"`
}

// ===== Agent 端点 agent =====

// AgentNodeResourceRequest 是 agent 上报的节点资源采样。
// 所有数值字段使用指针，区分“真实 0 值”和“本次未采集到该指标”。
type AgentNodeResourceRequest struct {
	// CPUPercent 是节点 CPU 使用百分比；nil 表示 agent 未采集该指标。
	CPUPercent *float64 `json:"cpu_percent"`
	// MemoryUsedBytes 是节点内存已用字节数。
	MemoryUsedBytes *int64 `json:"memory_used_bytes"`
	// MemoryTotalBytes 是节点内存总字节数。
	MemoryTotalBytes *int64 `json:"memory_total_bytes"`
	// DiskUsedBytes 是节点磁盘已用字节数。
	DiskUsedBytes *int64 `json:"disk_used_bytes"`
	// DiskTotalBytes 是节点磁盘总字节数。
	DiskTotalBytes *int64 `json:"disk_total_bytes"`
	// NetworkRxBytes 是节点网络累计接收字节数。
	NetworkRxBytes *int64 `json:"network_rx_bytes"`
	// NetworkTxBytes 是节点网络累计发送字节数。
	NetworkTxBytes *int64 `json:"network_tx_bytes"`
	// InstanceCount 是采样时节点承载的实例数量。
	InstanceCount *int32 `json:"instance_count"`
	// LastError 是 agent 侧采样失败原因；空字符串表示本次采样未报告错误。
	LastError string `json:"last_error"`
}

// AgentEnrollRequest agent 自动注册并换取 agent token 的请求体。
type AgentEnrollRequest struct {
	// AgentID 是 agent 自报的稳定外部 ID，用于幂等 enroll 与节点复用。
	AgentID string `json:"agent_id" binding:"required"`
	// Name 是节点展示名，空值由 service 使用 AgentID 兜底。
	Name string `json:"name"`
	// MaxApps 是节点可承载应用数量上限；nil 表示不限。
	MaxApps *int32 `json:"max_apps"`
	// AgentDockerEndpoint 是 manager 访问该 agent Docker 代理的地址。
	AgentDockerEndpoint string `json:"agent_docker_endpoint"`
	// AgentFileEndpoint 是 manager 访问该 agent 文件代理的地址。
	AgentFileEndpoint string `json:"agent_file_endpoint"`
	// AgentTLSCACert 是 agent 代理 TLS CA 证书内容，可为空。
	AgentTLSCACert string `json:"agent_tls_ca_cert"`
	// AgentVersion 是 agent 当前版本，用于节点巡检与升级判断。
	AgentVersion string `json:"agent_version"`
	// NodeDataRoot 是 agent 侧应用数据根目录。
	NodeDataRoot string `json:"node_data_root"`
	// SampledAt 是资源采样时间；为空时 handler 使用当前 UTC 时间兼容旧 agent。
	SampledAt string `json:"sampled_at"`
	// NodeResource 是 agent enroll 时可选的节点资源采样。
	NodeResource *AgentNodeResourceRequest `json:"node_resource"`
	// ResourceSnapshot 是 agent enroll 时上报的资源快照，原样保存为 JSON。
	ResourceSnapshot map[string]any `json:"resource_snapshot"`
	// Metadata 是 agent 附加元数据，原样保存为 JSON。
	Metadata map[string]any `json:"metadata"`
}

// AgentHeartbeatRequest agent 定期上报心跳的请求体。
type AgentHeartbeatRequest struct {
	// AgentToken 是 enroll 后签发的节点令牌，用于认证心跳来源。
	AgentToken string `json:"agent_token" binding:"required"`
	// AgentVersion 是心跳时的 agent 版本。
	AgentVersion string `json:"agent_version"`
	// SampledAt 是资源采样时间；为空时 handler 使用当前 UTC 时间兼容旧 agent。
	SampledAt string `json:"sampled_at"`
	// NodeResource 是 agent 心跳时可选的节点资源采样。
	NodeResource *AgentNodeResourceRequest `json:"node_resource"`
	// ResourceSnapshot 是心跳时的资源快照，覆盖节点当前快照。
	ResourceSnapshot map[string]any `json:"resource_snapshot"`
	// Metadata 是心跳时的附加元数据，覆盖节点当前元数据。
	Metadata map[string]any `json:"metadata"`
}

// ===== Hermes 任务看板 hermes-kanban =====

// CreateKanbanTaskRequest 是新建 Kanban 任务的请求体。
// 高级字段（Skills/Workspace/ParentID/MaxRetries）仅平台
// 管理员可生效，handler 对非平台管理员会忽略这些字段。
type CreateKanbanTaskRequest struct {
	// Board 是目标 board slug，为空时默认 "default"。
	Board string `json:"board"`
	// Title 是任务标题，必填。
	Title string `json:"title" binding:"required"`
	// Body 是任务描述，可为空。
	Body string `json:"body"`
	// Assignee 是任务分配对象（hermes profile 名），必填。
	Assignee string `json:"assignee" binding:"required"`
	// Priority 是任务优先级（0-9），0 为默认。
	Priority int `json:"priority"`
	// Skills 是高级字段，仅平台管理员生效：指定任务所需技能列表（每项对应一个 --skill 参数）。
	Skills []string `json:"skills"`
	// Workspace 是高级字段，仅平台管理员生效：workspace 参数，对应 --workspace，
	// 接受 scratch / worktree / dir:<path> 三种形式。
	Workspace string `json:"workspace"`
	// ParentID 是高级字段，仅平台管理员生效：父任务 ID。
	ParentID string `json:"parent_id"`
	// MaxRetries 是高级字段，仅平台管理员生效：最大重试次数。
	MaxRetries int `json:"max_retries"`
}

// KanbanCommentRequest 是给任务加评论的请求体。
type KanbanCommentRequest struct {
	// Board 是目标 board slug，为空时默认 "default"。
	Board string `json:"board"`
	// Body 是评论内容，必填。
	Body string `json:"body" binding:"required"`
}

// KanbanCompleteRequest 是标记任务完成的请求体。
type KanbanCompleteRequest struct {
	// Board 是目标 board slug，为空时默认 "default"。
	Board string `json:"board"`
	// Result 是可选的完成摘要。
	Result string `json:"result"`
}

// KanbanBlockRequest 是阻塞任务的请求体。
type KanbanBlockRequest struct {
	// Board 是目标 board slug，为空时默认 "default"。
	Board string `json:"board"`
	// Reason 是阻塞原因，必填。
	Reason string `json:"reason" binding:"required"`
}

// KanbanReassignRequest 是重新分配任务的请求体。
type KanbanReassignRequest struct {
	// Board 是目标 board slug，为空时默认 "default"。
	Board string `json:"board"`
	// To 是目标分配对象（hermes profile 名），必填。
	To string `json:"to" binding:"required"`
}

// KanbanBoardRequest 是仅需指定 board 的写操作（unblock / archive / reclaim）请求体。
type KanbanBoardRequest struct {
	// Board 是目标 board slug，为空时默认 "default"。
	Board string `json:"board"`
}

// ===== Hermes 定时任务 hermes-cron =====

// CreateCronJobRequest 是新建 Hermes Cron 任务的请求体。
// Skills/Model/Provider/BaseURL 属于高级运行字段，仅平台管理员提交时会透传给 service。
type CreateCronJobRequest struct {
	// Name 是任务显示名称；service 层会校验非空并生成稳定错误码。
	Name string `json:"name"`
	// Schedule 是任务调度表达式，必须由调用方显式提交。
	Schedule string `json:"schedule" binding:"required"`
	// Prompt 是任务触发时交给 Hermes 的提示词，可为空。
	Prompt string `json:"prompt"`
	// Deliver 是任务输出投递目标，例如 wechat；为空表示不投递。
	Deliver string `json:"deliver"`
	// Repeat 是任务重复次数；nil 表示不限制重复次数。
	Repeat *int `json:"repeat"`
	// Script 是仓库内脚本文件名，由 service 层校验为单文件名。
	Script string `json:"script"`
	// NoAgent 表示任务是否跳过 agent 执行路径。
	NoAgent bool `json:"no_agent"`
	// Workdir 是任务执行目录，由 service 层按 oc-cron 契约校验。
	Workdir string `json:"workdir"`
	// Skills 是高级字段，仅平台管理员生效：任务声明需要的技能列表。
	Skills []string `json:"skills"`
	// Model 是高级字段，仅平台管理员生效：任务指定模型。
	Model string `json:"model"`
	// Provider 是高级字段，仅平台管理员生效：任务指定模型提供方。
	Provider string `json:"provider"`
	// BaseURL 是高级字段，仅平台管理员生效：任务指定 provider base URL。
	BaseURL string `json:"base_url"`
}

// UpdateCronJobRequest 是更新 Hermes Cron 任务的请求体。
// 指针字段用于区分”未提交”和”提交空字符串”；ClearSkills/ClearRepeat 表示显式清空。
type UpdateCronJobRequest struct {
	// Name 是任务显示名称；nil 表示保持原值。
	Name *string `json:"name"`
	// Schedule 是任务调度表达式；nil 表示保持原值。
	Schedule *string `json:"schedule"`
	// Prompt 是任务提示词；nil 表示保持原值，空字符串表示清空。
	Prompt *string `json:"prompt"`
	// Deliver 是任务投递目标；nil 表示保持原值，空字符串表示清空。
	Deliver *string `json:"deliver"`
	// Repeat 是任务重复次数；nil 表示不修改重复次数。
	Repeat *int `json:"repeat"`
	// ClearRepeat 表示显式清空重复次数；当前 Hermes runtime 暂无稳定清空语义，提交 true 会返回 400。
	ClearRepeat bool `json:"clear_repeat"`
	// Script 是仓库内脚本文件名；nil 表示保持原值，空字符串表示清空。
	Script *string `json:"script"`
	// NoAgent 表示是否跳过 agent；nil 表示保持原值。
	NoAgent *bool `json:"no_agent"`
	// Workdir 是任务执行目录；nil 表示保持原值，空字符串表示清空。
	Workdir *string `json:"workdir"`
	// Skills 是高级字段，仅平台管理员生效：追加或替换任务技能列表。
	Skills []string `json:"skills"`
	// ClearSkills 是高级字段，仅平台管理员生效：显式清空任务技能列表。
	ClearSkills bool `json:"clear_skills"`
	// Model 是高级字段，仅平台管理员生效：任务指定模型。
	Model *string `json:"model"`
	// Provider 是高级字段，仅平台管理员生效：任务指定模型提供方。
	Provider *string `json:"provider"`
	// BaseURL 是高级字段，仅平台管理员生效：任务指定 provider base URL。
	BaseURL *string `json:"base_url"`
}

// ===== 应用 apps =====

// SwitchAppVersionRequest 是 POST /api/v1/apps/:appId/version 的请求体。
type SwitchAppVersionRequest struct {
	// VersionID 是目标助手版本 id，必须在实例所属组织的 allowlist 内。
	VersionID string `json:"version_id" binding:"required"`
}

// ===== 助手版本 assistant-versions =====

// AssistantVersionRoutingDTO 是智能路由 8 槽位的请求结构；空字符串表示走主模型。
type AssistantVersionRoutingDTO struct {
	Vision          string `json:"vision"`
	Compression     string `json:"compression"`
	WebExtract      string `json:"web_extract"`
	SessionSearch   string `json:"session_search"`
	TitleGeneration string `json:"title_generation"`
	Approval        string `json:"approval"`
	SkillsHub       string `json:"skills_hub"`
	Mcp             string `json:"mcp"`
}

// CreateAssistantVersionRequest 是创建助手版本的请求体。
type CreateAssistantVersionRequest struct {
	Name         string                     `json:"name" binding:"required"`
	Description  string                     `json:"description"`
	SystemPrompt string                     `json:"system_prompt" binding:"required"`
	ImageID      string                     `json:"image_id" binding:"required"`
	MainModel    string                     `json:"main_model" binding:"required"`
	Routing      AssistantVersionRoutingDTO `json:"routing"`
}

// UpdateAssistantVersionRequest 是编辑助手版本的请求体，字段同创建。
type UpdateAssistantVersionRequest struct {
	Name         string                     `json:"name" binding:"required"`
	Description  string                     `json:"description"`
	SystemPrompt string                     `json:"system_prompt" binding:"required"`
	ImageID      string                     `json:"image_id" binding:"required"`
	MainModel    string                     `json:"main_model" binding:"required"`
	Routing      AssistantVersionRoutingDTO `json:"routing"`
}
