package service

import "time"

const (
	// aiccDefaultRetentionDays 是新建智能体默认数据保留期，与产品默认留存策略保持一致。
	aiccDefaultRetentionDays int32 = 180
	// aiccMaxRetentionDays 必须与数据库 CHECK 约束保持一致，避免写库时才暴露错误。
	aiccMaxRetentionDays int32 = 3650
)

// AICCAgentInput 是企业管理员创建或编辑 AICC 智能体的入参。
type AICCAgentInput struct {
	// Name 是智能体展示名，创建和更新时 trim 后不能为空。
	Name string
	// Scenario 描述智能体适用业务场景，用于后续生成提示词和前端展示。
	Scenario string
	// Greeting 是访客进入会话时看到的欢迎语。
	Greeting string
	// AnswerBoundary 描述智能体不能回答的边界，后续 public runtime 生成回复时会使用。
	AnswerBoundary string
	// PrivacyMode 控制访客隐私提示模式；空值回落为 notice。
	PrivacyMode string
	// PrivacyText 是企业自定义隐私说明，可为空。
	PrivacyText string
	// RetentionDays 是会话与留资数据保留天数；0 表示使用默认值。
	RetentionDays int32
	// ThemeJSON 保留前端主题配置原始 JSON，由后续投放配置使用。
	ThemeJSON []byte
	// AllowedDomainsJSON 保留允许嵌入的域名列表原始 JSON，由 public widget 校验使用。
	AllowedDomainsJSON []byte
}

// AICCAgentResult 是 AICC 智能体对外响应视图。
type AICCAgentResult struct {
	// ID 是智能体主键。
	ID string `json:"id"`
	// OrgID 是智能体所属企业，用于平台只读排障和企业侧过滤。
	OrgID string `json:"org_id"`
	// AppID 是该智能体绑定的隐藏 app。
	AppID string `json:"app_id"`
	// Name 是智能体展示名。
	Name string `json:"name"`
	// Status 是智能体生命周期状态：draft / active / paused / deleted。
	Status string `json:"status"`
	// Scenario 是业务场景说明。
	Scenario string `json:"scenario,omitempty"`
	// Greeting 是欢迎语。
	Greeting string `json:"greeting,omitempty"`
	// AnswerBoundary 是回答边界说明。
	AnswerBoundary string `json:"answer_boundary,omitempty"`
	// PrivacyMode 是隐私提示模式。
	PrivacyMode string `json:"privacy_mode"`
	// PrivacyText 是隐私说明文本。
	PrivacyText string `json:"privacy_text,omitempty"`
	// RetentionDays 是数据保留天数。
	RetentionDays int32 `json:"retention_days"`
	// PublicToken 是公开链接 token；仅管理面返回，访客端不反查其它租户信息。
	PublicToken string `json:"public_token,omitempty"`
	// WidgetToken 是嵌入组件 token；仅管理面返回。
	WidgetToken string `json:"widget_token,omitempty"`
	// CreatedAt 是创建时间。
	CreatedAt time.Time `json:"created_at"`
	// UpdatedAt 是最近更新时间。
	UpdatedAt time.Time `json:"updated_at"`
}
