// Package handlers 内的 dto.go 集中所有请求体类型。
// 类型导出（大写）以便 swag 扫描；命名前缀按业务对象归类。
// 字段定义、json tag、binding tag 与原非导出版本保持 1:1，业务逻辑不变。
package handlers

// ErrorResponse 统一错误返回体，所有 handler 错误响应均使用此结构。
type ErrorResponse struct {
	Error string `json:"error"`
}

// ===== 认证 auth =====

// LoginRequest 用户名密码登录的请求体。
type LoginRequest struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
}

// RefreshRequest 续期 access token 的请求体。
type RefreshRequest struct {
	RefreshToken string `json:"refresh_token" binding:"required"`
}

// ===== 组织 organizations =====

// CreateOrganizationRequest 创建组织的请求体。
type CreateOrganizationRequest struct {
	Name                   string `json:"name" binding:"required"`
	ContactName            string `json:"contact_name"`
	ContactPhone           string `json:"contact_phone"`
	Remark                 string `json:"remark"`
	CreditWarningThreshold *int32 `json:"credit_warning_threshold"`
	AdminUsername          string `json:"admin_username" binding:"required"`
	AdminDisplayName       string `json:"admin_display_name" binding:"required"`
	AdminPassword          string `json:"admin_password" binding:"required"`
}

// OrganizationRequest 更新组织的请求体。
type OrganizationRequest struct {
	Name                   string `json:"name" binding:"required"`
	ContactName            string `json:"contact_name"`
	ContactPhone           string `json:"contact_phone"`
	Remark                 string `json:"remark"`
	CreditWarningThreshold *int32 `json:"credit_warning_threshold"`
}

// ===== 成员 members =====

// CreateMemberRequest 创建组织成员的请求体。
type CreateMemberRequest struct {
	Username    string `json:"username" binding:"required"`
	DisplayName string `json:"display_name" binding:"required"`
	Password    string `json:"password" binding:"required"`
	Role        string `json:"role"`
}

// UpdateMemberRequest 更新成员显示名或角色的请求体。
type UpdateMemberRequest struct {
	DisplayName string `json:"display_name" binding:"required"`
	Role        string `json:"role"`
}

// ResetPasswordRequest 管理员重置成员密码的请求体。
type ResetPasswordRequest struct {
	Password string `json:"password" binding:"required"`
}

// OnboardMemberRequest 在事务中创建成员并联动初始化应用的请求体。
type OnboardMemberRequest struct {
	Username    string `json:"username" binding:"required"`
	DisplayName string `json:"display_name" binding:"required"`
	Password    string `json:"password" binding:"required"`
	Role        string `json:"role"`
	AppName     string `json:"app_name" binding:"required"`
	AppPrompt   string `json:"app_prompt"`
	PersonaMode string `json:"persona_mode"`
	ChannelType string `json:"channel_type"`
	NodeID      string `json:"runtime_node_id"`
}

// ===== 人设 persona =====

// PersonaRequest 写入组织 AI 人设的请求体。
type PersonaRequest struct {
	SystemPrompt        string `json:"system_prompt" binding:"required"`
	ConversationRules   string `json:"conversation_rules"`
	ForbiddenRules      string `json:"forbidden_rules"`
	ReplyStyle          string `json:"reply_style"`
	AllowMemberOverride bool   `json:"allow_member_override"`
}

// ===== 知识库 knowledge =====

// RetryOrgSyncRequest 重试组织知识库节点同步的请求体。
type RetryOrgSyncRequest struct {
	NodeID string `json:"node_id" binding:"required"`
}

// ===== 充值 recharge =====

// RechargeRequest 组织充值的请求体。
type RechargeRequest struct {
	CreditAmount int64  `json:"credit_amount" binding:"required"`
	Remark       string `json:"remark"`
}

// ===== Agent 端点 agent =====

// AgentEnrollRequest agent 自动注册并换取 agent token 的请求体。
type AgentEnrollRequest struct {
	AgentID             string         `json:"agent_id" binding:"required"`
	Name                string         `json:"name"`
	MaxApps             *int32         `json:"max_apps"`
	AgentDockerEndpoint string         `json:"agent_docker_endpoint"`
	AgentFileEndpoint   string         `json:"agent_file_endpoint"`
	AgentTLSCACert      string         `json:"agent_tls_ca_cert"`
	AgentVersion        string         `json:"agent_version"`
	NodeDataRoot        string         `json:"node_data_root"`
	ResourceSnapshot    map[string]any `json:"resource_snapshot"`
	Metadata            map[string]any `json:"metadata"`
}

// AgentHeartbeatRequest agent 定期上报心跳的请求体。
type AgentHeartbeatRequest struct {
	AgentToken       string         `json:"agent_token" binding:"required"`
	AgentVersion     string         `json:"agent_version"`
	ResourceSnapshot map[string]any `json:"resource_snapshot"`
	Metadata         map[string]any `json:"metadata"`
}
