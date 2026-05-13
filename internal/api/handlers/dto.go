// Package handlers 内的 dto.go 集中所有请求体类型。
// 类型导出（大写）以便 swag 扫描；命名前缀按业务对象归类。
// 字段定义、json tag、binding tag 与原非导出版本保持 1:1，业务逻辑不变。
package handlers

// ErrorResponse 统一错误返回体，所有 handler 错误响应均使用此结构。
type ErrorResponse struct {
	// Error 是面向前端展示的安全错误文案，不包含底层密钥、SQL 或外部接口细节。
	Error string `json:"error"`
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
	// AppPrompt 是默认应用的初始提示词，可为空。
	AppPrompt string `json:"app_prompt"`
	// PersonaMode 控制新应用是否继承组织人设或使用独立人设。
	PersonaMode string `json:"persona_mode"`
	// ChannelType 是初始化渠道绑定的渠道标识。
	ChannelType string `json:"channel_type"`
	// NodeID 是指定 runtime 节点；为空时 service 自动选择可用节点。
	NodeID string `json:"runtime_node_id"`
}

// CreateMemberAppRequest 为已有成员创建新实例的请求体。
type CreateMemberAppRequest struct {
	// AppName 是新实例名称，创建时必填。
	AppName string `json:"app_name" binding:"required"`
	// AppPrompt 是新实例提示词，可为空。
	AppPrompt string `json:"app_prompt"`
	// PersonaMode 控制新实例是否继承组织人设或使用独立人设。
	PersonaMode string `json:"persona_mode"`
	// ChannelType 是初始化渠道绑定的渠道标识。
	ChannelType string `json:"channel_type"`
	// NodeID 是指定 runtime 节点；为空时 service 自动选择可用节点。
	NodeID string `json:"runtime_node_id"`
}

// ===== 人设 persona =====

// PersonaRequest 写入组织 AI 人设的请求体。
type PersonaRequest struct {
	// SystemPrompt 是组织默认系统提示词，不能为空。
	SystemPrompt string `json:"system_prompt" binding:"required"`
	// ConversationRules 是会话行为约束，可为空。
	ConversationRules string `json:"conversation_rules"`
	// ForbiddenRules 是组织层禁止规则，可为空。
	ForbiddenRules string `json:"forbidden_rules"`
	// ReplyStyle 是默认回复风格描述，可为空。
	ReplyStyle string `json:"reply_style"`
	// AllowMemberOverride 表示成员应用是否允许覆盖组织默认人设。
	AllowMemberOverride bool `json:"allow_member_override"`
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
