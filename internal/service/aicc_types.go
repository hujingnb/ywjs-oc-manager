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

// AICCAgentInput 是创建或编辑 AICC 智能体的入参。
type AICCAgentInput struct {
	// OrgID 是平台管理员创建时明确指定的目标企业；企业管理员创建时忽略该值并使用自身企业。
	// 更新既有智能体时不使用该字段，资源归属始终以数据库记录为准。
	OrgID string
	// Name 是智能体展示名，创建和更新时 trim 后不能为空。
	Name string
	// Persona 是客服独立人设，不复用助手版本中的 persona 配置。
	Persona string
	// IndustryKnowledgeBaseIDs 是本次提交的企业已授权行业知识库 ID。
	IndustryKnowledgeBaseIDs []string
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
	// AllowedDomains 是允许加载网页挂件的域名列表；为空表示不限制，支持 *.example.com。
	AllowedDomains []string
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
	// Persona 是客服独立人设。
	Persona string `json:"persona,omitempty"`
	// IndustryKnowledgeBaseIDs 是客服当前启用的行业知识库 ID。
	IndustryKnowledgeBaseIDs []string `json:"industry_knowledge_base_ids"`
	// Status 是智能体生命周期状态：draft / active / paused / deleted。
	Status string `json:"status"`
	// RuntimeStatus 是隐藏运行时就绪事实与接待意图共同计算的管理端展示状态。
	RuntimeStatus string `json:"runtime_status"`
	// RuntimeMessage 是异常或启动等待时可安全展示的摘要，不包含运行时坐标和凭证。
	RuntimeMessage string `json:"runtime_message,omitempty"`
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
	// AllowedDomains 是允许加载网页挂件的域名列表；为空表示不限制，支持 *.example.com。
	AllowedDomains []string `json:"allowed_domains"`
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
	// Region 是访客地域，空值表示公开端未能解析地域。
	Region string `json:"region,omitempty"`
	// SourceURL 是访客进入会话时所在页面，可为空。
	SourceURL string `json:"source_url,omitempty"`
	// Referrer 是浏览器 referrer，可为空。
	Referrer string `json:"referrer,omitempty"`
	// MessageCount 是当前会话下的消息数量，用于运营列表快速判断会话深度。
	MessageCount int64 `json:"message_count"`
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

// AICCSessionListResult 是管理端会话列表分页响应，Total 表示筛选后的总条数。
type AICCSessionListResult struct {
	// Sessions 是当前页会话摘要。
	Sessions []AICCSessionResult `json:"sessions"`
	// Total 是符合当前筛选条件的会话总数，不受 Limit 和 Offset 影响。
	Total int64 `json:"total"`
	// Limit 是本次查询实际使用的分页条数。
	Limit int32 `json:"limit"`
	// Offset 是本次查询实际使用的分页偏移。
	Offset int32 `json:"offset"`
}

// AICCSessionIntentResult 是会话级意向画像的可解释视图。字段和值均由当前会话访客原话证据约束，
// 其中不包含自动提取的联系方式等敏感数据。
type AICCSessionIntentResult struct {
	// IntentLevel 是 low、medium 或 high 三档意向等级。
	IntentLevel string `json:"intent_level"`
	// Fields 是审核白名单内的业务意向字段。
	Fields map[string]string `json:"fields"`
	// Confidence 表示各字段的分析置信度，取值范围为 0 到 1。
	Confidence map[string]float64 `json:"confidence"`
	// Evidence 是字段在访客原话中的直接证据，供运营回溯核验。
	Evidence map[string]string `json:"evidence"`
	// InviteStatus 表示 not_invited、invited、declined 或 submitted。
	InviteStatus string `json:"invite_status"`
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
	// ClientMessageID 是访客消息幂等键；公开页刷新后用它安全重试失败任务。
	ClientMessageID string `json:"client_message_id,omitempty"`
	// TaskStatus 是访客消息异步任务状态；仅公开会话恢复时补充。
	TaskStatus string `json:"task_status,omitempty"`
	// RetryAfterSeconds 是 retry_wait 状态的建议轮询等待秒数。
	RetryAfterSeconds *int64 `json:"retry_after_seconds,omitempty"`
	// IsFallback 表示是否为兜底回答。
	IsFallback bool `json:"is_fallback"`
	// IsRefusal 表示是否为拒答。
	IsRefusal bool `json:"is_refusal"`
	// ErrorSummary 是运行时错误摘要。
	ErrorSummary string `json:"error_summary,omitempty"`
	// Sources 是助手回复已校验的知识库或网络依据；公开端仅展示其中安全字段。
	Sources []AICCResponseSource `json:"sources,omitempty"`
	// NextAction 是当前助手回复对应的下一步展示动作，公开端据此渲染留资或解决状态卡片。
	NextAction string `json:"next_action,omitempty"`
	// CreatedAt 是消息创建时间。
	CreatedAt time.Time `json:"created_at"`
}

// AICCSessionDetailResult 是管理端会话详情，包含会话摘要和消息列表。
type AICCSessionDetailResult struct {
	// Session 是会话摘要。
	Session AICCSessionResult `json:"session"`
	// LeadValues 是本会话已提交的留资字段值，便于运营结合对话上下文回看。
	LeadValues []AICCLeadValueResult `json:"lead_values"`
	// Messages 是会话消息镜像。
	Messages []AICCMessageResult `json:"messages"`
	// Intent 是会话意向画像及逐字段访客原话证据，供运营在不要求留资的前提下判断跟进价值。
	Intent *AICCSessionIntentResult `json:"intent,omitempty"`
}

// AICCSessionListOptions 是管理端会话列表筛选条件。
type AICCSessionListOptions struct {
	// ResolutionStatus 按解决状态过滤，空值表示不限。
	ResolutionStatus string
	// LeadStatus 按留资状态过滤，空值表示不限。
	LeadStatus string
	// Channel 按访客入口渠道过滤，空值表示不限。
	Channel string
	// Region 按公开端解析出的访客地域过滤，空值表示不限。
	Region string
	// StartAt 限制会话创建时间下界，零值表示不限。
	StartAt time.Time
	// EndAt 限制会话创建时间上界，零值表示不限。
	EndAt time.Time
	// Keyword 在来源 URL 和 referrer 中做模糊搜索。
	Keyword string
	// Limit 是分页条数。
	Limit int32
	// Offset 是分页偏移。
	Offset int32
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
	// Values 是该线索已沉淀的自定义留资字段值。
	Values []AICCLeadValueResult `json:"values"`
	// CreatedAt 是线索首次创建时间。
	CreatedAt time.Time `json:"created_at"`
	// UpdatedAt 是线索最近更新时间。
	UpdatedAt time.Time `json:"updated_at"`
}

// AICCLeadValueResult 是管理端展示和导出自定义留资字段值的视图。
type AICCLeadValueResult struct {
	// FieldID 是留资字段主键，用于历史字段被重命名时仍可追踪来源。
	FieldID string `json:"field_id"`
	// FieldKey 是字段稳定 key，CSV 导出使用它避免同名 label 冲突。
	FieldKey string `json:"field_key"`
	// Label 是运营侧可读字段名。
	Label string `json:"label"`
	// FieldType 是字段类型，前端可据此做轻量展示。
	FieldType string `json:"field_type"`
	// Value 是访客提交的字段值。
	Value string `json:"value"`
	// CreatedAt 是该字段值首次创建时间。
	CreatedAt time.Time `json:"created_at"`
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
}

// AICCKnowledgeResult 是管理端回显的智能体知识范围配置。
type AICCKnowledgeResult struct {
	// AgentID 是智能体主键。
	AgentID string `json:"agent_id"`
	// AppID 是绑定隐藏 app ID，前端用它跳转到当前客服知识库维护入口。
	AppID string `json:"app_id"`
	// UseOrgKnowledge 表示是否允许检索本企业共享知识库。
	UseOrgKnowledge bool `json:"use_org_knowledge"`
	// IndustryKnowledgeBaseIDs 是已挂载的平台行业知识库 ID 列表。
	IndustryKnowledgeBaseIDs []string `json:"industry_knowledge_base_ids"`
	// AppDocumentIDs 保留给旧客户端兼容；当前客服知识库默认启用，不再逐文档配置。
	AppDocumentIDs []string `json:"app_document_ids"`
}

// AICCKnowledgeOption 描述 AICC 知识范围下拉框可选项。
type AICCKnowledgeOption struct {
	// ID 是行业库主键，保存知识范围时原样提交。
	ID string `json:"id"`
	// Name 是管理端展示名。
	Name string `json:"name"`
	// DocumentCount 是行业库已缓存文档数。
	DocumentCount int64 `json:"document_count"`
}

// AICCKnowledgeOptionsResult 是 AICC 管理页所需的只读知识选项集合。
type AICCKnowledgeOptionsResult struct {
	// IndustryKnowledgeBases 是平台行业库候选项，企业管理员只读选择。
	IndustryKnowledgeBases []AICCKnowledgeOption `json:"industry_knowledge_bases"`
	// AppDocuments 保留给旧客户端兼容；当前客服知识库默认启用，不再逐文档选择。
	AppDocuments []AICCKnowledgeOption `json:"app_documents"`
}

// AICCAgentSettingsInput 是企业管理员保存 AICC 运营配置的入参。
type AICCAgentSettingsInput struct {
	// MessageLimitPerSession 限制单个公开会话最多发送多少条访客消息。
	MessageLimitPerSession int32 `json:"message_limit_per_session"`
	// SensitiveWords 是公开端发送前拦截的敏感词列表，保存前会 trim、去空和去重。
	SensitiveWords []string `json:"sensitive_words"`
	// BlockedVisitorEnabled 控制是否启用访客封禁检查。
	BlockedVisitorEnabled bool `json:"blocked_visitor_enabled"`
	// BlockedVisitorThresholdJSON 保留异常访客自动封禁阈值配置。
	BlockedVisitorThresholdJSON []byte `json:"blocked_visitor_threshold_json,omitempty"`
	// SessionResumeTTLMinutes 控制公开端刷新续接有效期。
	SessionResumeTTLMinutes int32 `json:"session_resume_ttl_minutes"`
}

// AICCAgentSettingsResult 是管理端回显的 AICC 运营配置。
type AICCAgentSettingsResult struct {
	// AgentID 是智能体主键。
	AgentID string `json:"agent_id"`
	// MessageLimitPerSession 限制单个公开会话最多发送多少条访客消息。
	MessageLimitPerSession int32 `json:"message_limit_per_session"`
	// SensitiveWords 是公开端发送前拦截的敏感词列表。
	SensitiveWords []string `json:"sensitive_words"`
	// BlockedVisitorEnabled 表示是否启用访客封禁检查。
	BlockedVisitorEnabled bool `json:"blocked_visitor_enabled"`
	// BlockedVisitorThresholdJSON 是异常访客自动封禁阈值配置，使用对象避免 JSON 响应被 base64 编码。
	BlockedVisitorThresholdJSON map[string]any `json:"blocked_visitor_threshold_json"`
	// SessionResumeTTLMinutes 控制公开端刷新续接有效期。
	SessionResumeTTLMinutes int32 `json:"session_resume_ttl_minutes"`
	// BlockedVisitorCount 是当前有效封禁访客数量。
	BlockedVisitorCount int64 `json:"blocked_visitor_count"`
}

// AICCAnalyticsResult 是管理端 AICC 运营统计卡片视图。
type AICCAnalyticsResult struct {
	// TodaySessions 是当前企业今日会话数。
	TodaySessions int64 `json:"today_sessions"`
	// TotalSessions 是统计时间范围内的会话总数。
	TotalSessions int64 `json:"total_sessions"`
	// UnreadLeads 是当前企业未读线索数。
	UnreadLeads int64 `json:"unread_leads"`
	// ResolvedSessions 是当前企业已标记解决的会话数。
	ResolvedSessions int64 `json:"resolved_sessions"`
	// UnresolvedSessions 是当前企业已标记未解决的会话数。
	UnresolvedSessions int64 `json:"unresolved_sessions"`
	// UnknownSessions 是统计时间范围内尚未判定解决状态的会话数。
	UnknownSessions int64 `json:"unknown_sessions"`
	// UnresolvedRate 是未解决会话在已判定会话中的占比；没有已判定会话时为 0。
	UnresolvedRate float64 `json:"unresolved_rate"`
	// CompletedLeadSessions 是已完成留资的会话数。
	CompletedLeadSessions int64 `json:"completed_lead_sessions"`
	// SessionTrend 是按日或周聚合的会话趋势。
	SessionTrend []AICCTrendBucket `json:"session_trend"`
	// Regions 是统计时间范围内的访客地域分布。
	Regions []AICCTopItemResult `json:"regions"`
	// TopQuestions 是访客高频问题列表。
	TopQuestions []AICCTopItemResult `json:"top_questions"`
	// TopSources 是访客来源页面分布。
	TopSources []AICCTopItemResult `json:"top_sources"`
}

// AICCAnalyticsOptions 是管理端统计看板筛选条件。
type AICCAnalyticsOptions struct {
	// OrgID 是统计企业；企业管理员可省略并使用自身企业。
	OrgID string
	// AgentID 可选限制到单个智能体。
	AgentID string
	// StartAt 是统计窗口开始时间，零值时使用默认最近 7 天。
	StartAt time.Time
	// EndAt 是统计窗口结束时间，零值时使用当前时间。
	EndAt time.Time
	// Bucket 控制趋势粒度，允许 day 或 week。
	Bucket string
}

// AICCAnalyticsSummary 是统计窗口内按解决状态汇总的会话数量。
type AICCAnalyticsSummary struct {
	// Sessions 是统计窗口内的总会话数。
	Sessions int64
	// Resolved 是已解决会话数。
	Resolved int64
	// Unresolved 是未解决会话数。
	Unresolved int64
	// Unknown 是未知解决状态会话数。
	Unknown int64
}

// AICCTrendBucket 是统计趋势中的单个时间桶。
type AICCTrendBucket struct {
	// Bucket 是日维度日期或 ISO 周维度标签。
	Bucket string `json:"bucket"`
	// Count 是该时间桶内会话数量。
	Count int64 `json:"count"`
}

// AICCTopItemResult 是统计页展示的名称和次数组合。
type AICCTopItemResult struct {
	// Label 是问题文本或来源页面。
	Label string `json:"label"`
	// Count 是该项出现次数。
	Count int64 `json:"count"`
}
