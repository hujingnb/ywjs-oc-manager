package service

import "time"

const (
	// aiccDefaultRetentionDays 是新建智能体默认数据保留期，与产品默认留存策略保持一致。
	aiccDefaultRetentionDays int32 = 180
	// aiccMaxRetentionDays 必须与数据库 CHECK 约束保持一致，避免写库时才暴露错误。
	aiccMaxRetentionDays int32 = 3650
	// aiccLeadExportLimit 限制同步 CSV 导出规模，避免单请求无上限拉取和缓冲占用过多资源。
	aiccLeadExportLimit int32 = 10000
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

// AICCSessionResult 是管理端会话列表的摘要视图。
type AICCSessionResult struct {
	// ID 是会话主键。
	ID string `json:"id"`
	// AgentID 是会话所属智能体。
	AgentID string `json:"agent_id"`
	// OrgID 是会话所属企业。
	OrgID string `json:"org_id"`
	// Channel 是访客入口渠道。
	Channel string `json:"channel"`
	// SourceURL 是访客进入会话时所在页面，可为空。
	SourceURL string `json:"source_url,omitempty"`
	// Referrer 是浏览器 referrer，可为空。
	Referrer string `json:"referrer,omitempty"`
	// ResolutionStatus 是当前解决状态。
	ResolutionStatus string `json:"resolution_status"`
	// LeadStatus 是当前留资状态。
	LeadStatus string `json:"lead_status"`
	// LastActiveAt 是最近活跃时间。
	LastActiveAt time.Time `json:"last_active_at"`
	// CreatedAt 是会话创建时间。
	CreatedAt time.Time `json:"created_at"`
	// UpdatedAt 是最近更新时间。
	UpdatedAt time.Time `json:"updated_at"`
}

// AICCMessageResult 是管理端会话详情中的消息镜像。
type AICCMessageResult struct {
	// ID 是消息主键。
	ID string `json:"id"`
	// Direction 区分访客、助手或系统消息。
	Direction string `json:"direction"`
	// ContentType 描述文本、图片或混合消息。
	ContentType string `json:"content_type"`
	// Text 是文本内容，可为空。
	Text string `json:"text,omitempty"`
	// ImageObjectKey 是图片对象 key，仅管理端排障使用。
	ImageObjectKey string `json:"image_object_key,omitempty"`
	// ImageMime 是图片 MIME。
	ImageMime string `json:"image_mime,omitempty"`
	// ImageSizeBytes 是图片大小。
	ImageSizeBytes int64 `json:"image_size_bytes,omitempty"`
	// IsFallback 表示是否为兜底回答。
	IsFallback bool `json:"is_fallback"`
	// IsRefusal 表示是否为拒答。
	IsRefusal bool `json:"is_refusal"`
	// ErrorSummary 是运行时错误摘要。
	ErrorSummary string `json:"error_summary,omitempty"`
	// CreatedAt 是消息创建时间。
	CreatedAt time.Time `json:"created_at"`
}

// AICCSessionDetailResult 是管理端会话详情，包含会话摘要和消息列表。
type AICCSessionDetailResult struct {
	// Session 是会话摘要。
	Session AICCSessionResult `json:"session"`
	// Messages 是会话消息镜像。
	Messages []AICCMessageResult `json:"messages"`
}

// AICCLeadResult 是管理端线索列表视图。
type AICCLeadResult struct {
	// ID 是线索主键。
	ID string `json:"id"`
	// OrgID 是线索所属企业。
	OrgID string `json:"org_id"`
	// DisplayName 是可展示联系人名称或联系方式摘要。
	DisplayName string `json:"display_name,omitempty"`
	// Unread 表示是否未读。
	Unread bool `json:"unread"`
	// LatestSessionID 是最近关联会话。
	LatestSessionID string `json:"latest_session_id,omitempty"`
	// CreatedAt 是线索首次创建时间。
	CreatedAt time.Time `json:"created_at"`
	// UpdatedAt 是线索最近更新时间。
	UpdatedAt time.Time `json:"updated_at"`
}

// AICCLeadFieldInput 是企业管理员配置公开页留资字段的入参。
type AICCLeadFieldInput struct {
	// FieldKey 是字段稳定 key，用于公开页提交值和后端校验。
	FieldKey string `json:"field_key"`
	// Label 是公开页展示给访客的字段名称。
	Label string `json:"label"`
	// FieldType 限定输入类型：text / phone / email / number。
	FieldType string `json:"field_type"`
	// Required 表示发送消息前是否必须填写。
	Required bool `json:"required"`
	// PromptText 是输入框占位提示，可为空。
	PromptText string `json:"prompt_text,omitempty"`
	// SortOrder 是公开页展示顺序。
	SortOrder int32 `json:"sort_order"`
}

// AICCLeadFieldResult 是管理端和公开页共享的留资字段视图。
type AICCLeadFieldResult struct {
	// ID 是字段主键；公开页只用于稳定渲染，不参与提交。
	ID string `json:"id"`
	// FieldKey 是字段稳定 key。
	FieldKey string `json:"field_key"`
	// Label 是字段展示名称。
	Label string `json:"label"`
	// FieldType 是字段输入类型。
	FieldType string `json:"field_type"`
	// Required 表示是否必填。
	Required bool `json:"required"`
	// PromptText 是输入提示。
	PromptText string `json:"prompt_text,omitempty"`
	// SortOrder 是展示顺序。
	SortOrder int32 `json:"sort_order"`
}

// AICCKnowledgeInput 是企业管理员保存智能体知识范围的完整快照。
type AICCKnowledgeInput struct {
	// UseOrgKnowledge 表示是否允许智能体检索本企业共享知识库。
	UseOrgKnowledge bool `json:"use_org_knowledge"`
	// IndustryKnowledgeBaseIDs 是额外挂载的平台行业知识库 ID 列表。
	IndustryKnowledgeBaseIDs []string `json:"industry_knowledge_base_ids"`
	// AppDocumentIDs 是该智能体隐藏 app 专属知识库中允许检索的文档 ID 列表。
	AppDocumentIDs []string `json:"app_document_ids"`
}

// AICCKnowledgeResult 是管理端回显的智能体知识范围配置。
type AICCKnowledgeResult struct {
	// AgentID 是智能体主键。
	AgentID string `json:"agent_id"`
	// AppID 是绑定隐藏 app ID，前端用它跳转到专属文档库维护入口。
	AppID string `json:"app_id"`
	// UseOrgKnowledge 表示是否允许检索本企业共享知识库。
	UseOrgKnowledge bool `json:"use_org_knowledge"`
	// IndustryKnowledgeBaseIDs 是已挂载的平台行业知识库 ID 列表。
	IndustryKnowledgeBaseIDs []string `json:"industry_knowledge_base_ids"`
	// AppDocumentIDs 是已挂载的专属文档 ID 列表。
	AppDocumentIDs []string `json:"app_document_ids"`
}

// AICCAnalyticsResult 是管理端 AICC 运营统计卡片视图。
type AICCAnalyticsResult struct {
	// TodaySessions 是当前企业今日会话数。
	TodaySessions int64 `json:"today_sessions"`
	// UnreadLeads 是当前企业未读线索数。
	UnreadLeads int64 `json:"unread_leads"`
}
