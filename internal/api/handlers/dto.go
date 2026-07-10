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
	// OrgCode 是企业用户登录时填写的企业标识；平台管理员登录时留空。
	OrgCode string `json:"org_code"`
	// Username 是 manager 账号名，登录失败时不区分账号不存在和密码错误。
	Username string `json:"username" binding:"required"`
	// Password 是明文登录密码，仅用于本次校验，handler 不写日志。
	Password string `json:"password" binding:"required"`
	// Captcha 是 Altcha payload（base64）；验证码开启时必填，是否必填由后端按
	// captcha.enabled 在 service 层判断，故此处不加 binding:"required"。
	Captcha string `json:"captcha"`
}

// RefreshRequest 续期 access token 的请求体。
type RefreshRequest struct {
	// RefreshToken 是长生命周期刷新令牌，service 层只保存其 hash 并在刷新时轮换。
	RefreshToken string `json:"refresh_token" binding:"required"`
}

// UpdateLocaleRequest 是 PATCH /auth/me/locale 的请求体；locale 取值由 service 校验。
type UpdateLocaleRequest struct {
	// Locale 是目标界面语言，例如 en / zh。
	Locale string `json:"locale" binding:"required"`
}

// ChangePasswordRequest 是已登录用户修改自己密码的请求体。
type ChangePasswordRequest struct {
	// OldPassword 是当前登录密码，只用于本次校验，不写日志。
	OldPassword string `json:"old_password" binding:"required"`
	// NewPassword 是新登录密码，service 层会校验长度并写入 hash。
	NewPassword string `json:"new_password" binding:"required"`
}

// ===== 企业 organizations =====

// CreateOrganizationRequest 创建企业的请求体。
type CreateOrganizationRequest struct {
	// Name 是企业展示名，也是平台管理员列表中识别租户的主字段。
	Name string `json:"name" binding:"required"`
	// Code 是企业登录标识，创建后不可修改。
	Code string `json:"code" binding:"required"`
	// ContactName 是业务联系人姓名，可为空。
	ContactName string `json:"contact_name"`
	// ContactPhone 是业务联系人电话，可为空；不参与权限或登录校验。
	ContactPhone string `json:"contact_phone"`
	// Remark 是平台管理员维护的内部备注。
	Remark string `json:"remark"`
	// CreditWarningThreshold 是企业余额预警阈值；nil 表示不启用余额预警或保持预警关闭。
	CreditWarningThreshold *int32 `json:"credit_warning_threshold"`
	// MaxInstanceCount 是企业最多可创建的实例（应用）数；nil 表示不限制。
	MaxInstanceCount *int32 `json:"max_instance_count"`
	// KnowledgeQuotaBytes 是企业知识库累计容量上限，单位字节；nil 表示创建时使用默认值、更新时保留旧值。
	KnowledgeQuotaBytes *int64 `json:"knowledge_quota_bytes"`
	// DefaultAppKnowledgeQuotaBytes 是该企业新建实例的默认知识库容量上限，单位字节；nil 表示使用默认 1GB。
	DefaultAppKnowledgeQuotaBytes *int64 `json:"default_app_knowledge_quota_bytes"`
	// AssistantVersionIDs 是该企业可用的助手版本 id 列表（allowlist）。
	AssistantVersionIDs []string `json:"assistant_version_ids"`
	// AdminUsername 是随企业创建的首个 org_admin 账号名。
	AdminUsername string `json:"admin_username" binding:"required"`
	// AdminDisplayName 是首个 org_admin 的显示名。
	AdminDisplayName string `json:"admin_display_name" binding:"required"`
	// AdminPassword 是首个 org_admin 的初始密码，service 层立即 hash 后写库。
	AdminPassword string `json:"admin_password" binding:"required"`
}

// OrganizationRequest 更新企业的请求体。
type OrganizationRequest struct {
	// Name 是企业展示名；更新时仍必填，避免空名称进入前端列表。
	Name string `json:"name" binding:"required"`
	// ContactName 是业务联系人姓名，可置空。
	ContactName string `json:"contact_name"`
	// ContactPhone 是业务联系人电话，可置空。
	ContactPhone string `json:"contact_phone"`
	// Remark 是平台管理员维护的内部备注，可置空。
	Remark string `json:"remark"`
	// CreditWarningThreshold 是企业余额预警阈值；nil 表示清空或未设置预警阈值。
	CreditWarningThreshold *int32 `json:"credit_warning_threshold"`
	// MaxInstanceCount 是企业最多可创建的实例（应用）数；nil 表示不限制。
	MaxInstanceCount *int32 `json:"max_instance_count"`
	// KnowledgeQuotaBytes 是企业知识库累计容量上限，单位字节；nil 表示创建时使用默认值、更新时保留旧值。
	KnowledgeQuotaBytes *int64 `json:"knowledge_quota_bytes"`
	// DefaultAppKnowledgeQuotaBytes 是该企业新建实例的默认知识库容量上限，单位字节；nil 表示保留旧值。
	DefaultAppKnowledgeQuotaBytes *int64 `json:"default_app_knowledge_quota_bytes"`
	// AssistantVersionIDs 是该企业可用的助手版本 id 列表（allowlist）。
	AssistantVersionIDs []string `json:"assistant_version_ids"`
}

// UpdateOrganizationAICCConfigRequest 是平台管理员维护企业 AICC 开通状态的请求体。
type UpdateOrganizationAICCConfigRequest struct {
	// Enabled 表示是否开通 AICC。
	Enabled *bool `json:"enabled" binding:"required"`
	// AgentLimit 是智能体数量上限；nil 表示不限。
	AgentLimit *int32 `json:"agent_limit"`
}

// CreateAICCAgentRequest 是企业管理员创建 AICC 智能体的请求体。
type CreateAICCAgentRequest struct {
	// Name 是智能体展示名。
	Name string `json:"name" binding:"required"`
	// Scenario 是智能体适用业务场景说明。
	Scenario string `json:"scenario"`
	// Greeting 是访客进入会话时看到的欢迎语。
	Greeting string `json:"greeting"`
	// AnswerBoundary 是智能体回答边界说明。
	AnswerBoundary string `json:"answer_boundary"`
	// PrivacyMode 是隐私提示模式：notice / consent_required。
	PrivacyMode string `json:"privacy_mode"`
	// PrivacyText 是企业自定义隐私说明。
	PrivacyText string `json:"privacy_text"`
	// RetentionDays 是数据保留天数；0 表示使用后端默认值。
	RetentionDays int32 `json:"retention_days"`
}

// UpdateAICCAgentRequest 是企业管理员更新 AICC 智能体资料的请求体。
type UpdateAICCAgentRequest struct {
	// Name 是智能体展示名。
	Name string `json:"name" binding:"required"`
	// Scenario 是智能体适用业务场景说明。
	Scenario string `json:"scenario"`
	// Greeting 是访客进入会话时看到的欢迎语。
	Greeting string `json:"greeting"`
	// AnswerBoundary 是智能体回答边界说明。
	AnswerBoundary string `json:"answer_boundary"`
	// PrivacyMode 是隐私提示模式：notice / consent_required。
	PrivacyMode string `json:"privacy_mode"`
	// PrivacyText 是企业自定义隐私说明。
	PrivacyText string `json:"privacy_text"`
	// RetentionDays 是数据保留天数；0 表示使用后端默认值。
	RetentionDays int32 `json:"retention_days"`
}

// AICCLeadFieldRequest 是企业管理员维护公开页留资字段的单条配置。
type AICCLeadFieldRequest struct {
	// FieldKey 是字段稳定 key，访客提交时使用。
	FieldKey string `json:"field_key" binding:"required"`
	// Label 是公开页展示名称。
	Label string `json:"label" binding:"required"`
	// FieldType 是字段输入类型：text / phone / email / number。
	FieldType string `json:"field_type" binding:"required"`
	// Required 表示是否为发送消息前必填字段。
	Required bool `json:"required"`
	// PromptText 是输入提示。
	PromptText string `json:"prompt_text"`
	// SortOrder 是公开页展示顺序。
	SortOrder int32 `json:"sort_order"`
}

// ReplaceAICCLeadFieldsRequest 是整组保存 AICC 留资字段的请求体。
type ReplaceAICCLeadFieldsRequest struct {
	// Fields 是当前智能体完整留资字段列表；提交空数组表示关闭留资表单。
	Fields []AICCLeadFieldRequest `json:"fields" binding:"required"`
}

// ReplaceAICCKnowledgeRequest 是整组保存 AICC 知识范围的请求体。
type ReplaceAICCKnowledgeRequest struct {
	// UseOrgKnowledge 表示是否允许智能体检索本企业共享知识库。
	UseOrgKnowledge bool `json:"use_org_knowledge"`
	// IndustryKnowledgeBaseIDs 是额外挂载的平台行业知识库 ID 列表。
	IndustryKnowledgeBaseIDs []string `json:"industry_knowledge_base_ids"`
	// AppDocumentIDs 是该智能体隐藏 app 专属知识库中允许检索的文档 ID 列表。
	AppDocumentIDs []string `json:"app_document_ids"`
}

// CreateAICCSessionRequest 是访客创建公开会话的请求体。
type CreateAICCSessionRequest struct {
	// Channel 是公开入口渠道：web_link / web_widget。
	Channel string `json:"channel"`
	// SourceURL 是访客进入会话时所在页面 URL。
	SourceURL string `json:"source_url"`
	// Referrer 是浏览器 referrer。
	Referrer string `json:"referrer"`
}

// PublicAICCMessageRequest 是访客发送消息的请求体。
type PublicAICCMessageRequest struct {
	// Text 是访客文本消息。
	Text string `json:"text"`
	// ImageFileID 是已上传图片文件 ID，图片能力后续扩展时使用。
	ImageFileID string `json:"image_file_id"`
}

// SubmitAICCLeadValuesRequest 是访客提交留资字段的请求体。
type SubmitAICCLeadValuesRequest struct {
	// Values 是 field_key 到访客填写值的映射。
	Values map[string]string `json:"values" binding:"required"`
}

// SubmitAICCFeedbackRequest 是访客反馈单条回答是否有帮助的请求体。
type SubmitAICCFeedbackRequest struct {
	// Helpful 表示该回答是否有帮助。
	Helpful *bool `json:"helpful" binding:"required"`
}

// ===== 成员 members =====

// CreateMemberRequest 创建企业成员的请求体。
type CreateMemberRequest struct {
	// Username 是企业内登录账号名，创建后不可通过成员更新接口修改。
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
	// Role 为空表示保持原角色；非空时需要管理员权限并限制在企业角色内。
	Role string `json:"role"`
}

// ResetPasswordRequest 管理员重置成员密码的请求体。
type ResetPasswordRequest struct {
	// Password 是新密码，service 层只保存 hash。
	Password string `json:"password" binding:"required"`
}

// OnboardMemberRequest 在事务中创建成员并联动初始化应用的请求体。
// k8s 模型下不需要指定节点，pod 落点由调度器决定。
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
	// VersionID 是实例绑定的助手版本 id，必须落在企业 allowlist 内。
	VersionID string `json:"version_id" binding:"required"`
}

// FeishuChannelAuthRequest 是飞书渠道发起请求体（仅扫码自动创建，无需凭证）。
type FeishuChannelAuthRequest struct {
	// Domain 是飞书域：feishu | lark，默认 feishu；omitempty 允许留空回退，
	// 但显式传值时必须落在 feishu/lark 内，防御非法 domain 透传 oc-ops。
	Domain string `json:"domain" binding:"omitempty,oneof=feishu lark"`
}

// WorkWechatChannelAuthRequest 是企业微信渠道发起请求体（手填智能机器人凭证）。
type WorkWechatChannelAuthRequest struct {
	// BotID 是企业微信智能机器人 Bot ID（必填）。
	BotID string `json:"bot_id" binding:"required"`
	// Secret 是机器人 Secret（必填，仅入库密文与注入 Secret，不回显）。
	Secret string `json:"secret" binding:"required"`
}

// DingtalkChannelAuthRequest 是钉钉渠道发起请求体（手填机器人凭证，字段名全栈统一为 client_id/client_secret）。
type DingtalkChannelAuthRequest struct {
	// ClientID 是钉钉应用 Client ID（即 AppKey，必填）。
	ClientID string `json:"client_id" binding:"required"`
	// ClientSecret 是钉钉 Client Secret（即 AppSecret，必填，仅入库密文与注入 Secret，不回显）。
	ClientSecret string `json:"client_secret" binding:"required"`
}

// CreateMemberAppRequest 为已有成员创建新实例的请求体。
// k8s 模型下不需要指定节点，pod 落点由调度器决定。
type CreateMemberAppRequest struct {
	// AppName 是新实例名称，创建时必填。
	AppName string `json:"app_name" binding:"required"`
	// ChannelType 是初始化渠道绑定的渠道标识。
	ChannelType string `json:"channel_type"`
	// VersionID 是实例绑定的助手版本 id，必须落在企业 allowlist 内。
	VersionID string `json:"version_id" binding:"required"`
}

// ===== 知识库 knowledge =====

// RuntimeKnowledgeSearchRequest 是 Hermes oc-kb 检索知识库的请求体。
type RuntimeKnowledgeSearchRequest struct {
	// Question 是用户问题或检索语句。
	Question string `json:"question" binding:"required"`
	// TopK 是每个知识库作用域的检索 chunk 上限；0 使用 service 默认值，超过 service 上限会被截断。
	TopK int32 `json:"top_k"`
}

// UpdateKnowledgeEmbeddingModelRequest 是平台管理员修改 RAGFlow dataset embedding 模型的请求体。
type UpdateKnowledgeEmbeddingModelRequest struct {
	// Name 是 RAGFlow 控制台可见的模型名，不使用 RAGFlow 内部拼接 ID。
	Name string `json:"name" binding:"required"`
	// Provider 是模型来源；为空时后端按 name 做唯一匹配。
	Provider string `json:"provider"`
}

// InitKnowledgeUploadRequest 是发起知识库分片上传的请求体。
type InitKnowledgeUploadRequest struct {
	// Filename 是原始文件名，服务端会取 base 后用于暂存 key 与最终文档名。
	Filename string `json:"filename" binding:"required"`
	// Size 是文件总字节数，用于上传前的配额预校验，必须为正。
	Size int64 `json:"size" binding:"required"`
}

// CreateIndustryKnowledgeBaseRequest 是创建行业知识库请求体。
type CreateIndustryKnowledgeBaseRequest struct {
	// Name 是行业知识库展示名，在未删除行业库中必须唯一。
	Name string `json:"name" binding:"required"`
}

// UpdateIndustryKnowledgeBaseRequest 是重命名行业知识库请求体。
type UpdateIndustryKnowledgeBaseRequest struct {
	// Name 是更新后的行业知识库展示名，在未删除行业库中必须唯一。
	Name string `json:"name" binding:"required"`
}

// ===== 充值 recharge =====

// RechargeRequest 企业充值的请求体。
type RechargeRequest struct {
	// CreditAmount 是充值额度，必须为正数，service 层会同步写入 new-api。
	CreditAmount int64 `json:"credit_amount" binding:"required"`
	// Remark 是本次充值的审计备注。
	Remark string `json:"remark"`
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
	// VersionID 是目标助手版本 id，必须在实例所属企业的 allowlist 内。
	VersionID string `json:"version_id" binding:"required"`
}

// UpdateAppLocaleRequest 是 PATCH /api/v1/apps/:appId/locale 的请求体。
type UpdateAppLocaleRequest struct {
	// Locale 是 hermes bot 对终端用户说话的语言（en/zh）；取值集合由 service 层校验。
	Locale string `json:"locale" binding:"required"`
}

// AppLocaleStatusResponse 是 GET /api/v1/apps/:appId/locale-status 的响应。
type AppLocaleStatusResponse struct {
	// CurrentLanguage 是实例实时语言（取自 oc-ops，不读 DB 快照）；实例未运行 / 不可达时为 null。
	CurrentLanguage *string `json:"current_language"`
	// DesiredLanguage 是期望语言（apps.locale 配置值）。
	DesiredLanguage string `json:"desired_language"`
	// NeedsRestart 表示运行中实例当前语言≠期望，需重启生效。
	NeedsRestart bool `json:"needs_restart"`
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
	// IndustryKnowledgeBaseIDs 是该助手版本运行时额外检索的行业知识库 ID 列表。
	IndustryKnowledgeBaseIDs []string `json:"industry_knowledge_base_ids"`
}

// UpdateAssistantVersionRequest 是编辑助手版本的请求体，字段同创建。
type UpdateAssistantVersionRequest struct {
	Name         string                     `json:"name" binding:"required"`
	Description  string                     `json:"description"`
	SystemPrompt string                     `json:"system_prompt" binding:"required"`
	ImageID      string                     `json:"image_id" binding:"required"`
	MainModel    string                     `json:"main_model" binding:"required"`
	Routing      AssistantVersionRoutingDTO `json:"routing"`
	// IndustryKnowledgeBaseIDs 是该助手版本运行时额外检索的行业知识库 ID 列表；更新时未提交表示保留旧关联。
	IndustryKnowledgeBaseIDs *[]string `json:"industry_knowledge_base_ids"`
}

// ===== 平台库 skill =====
// 上传走 multipart/form-data（字段 name/version/description + file），无 JSON 请求体 DTO。

// ===== 助手版本 skill =====

// AddSkillFromLibraryRequest 是 POST /api/v1/assistant-versions/:id/skills 的请求体。
// 从市场（平台库 / ClawHub）选一个 skill 配进版本（自包含快照，不上传文件）。
type AddSkillFromLibraryRequest struct {
	// Source 是 skill 来源类型，接受 "platform" 或 "clawhub"。
	Source string `json:"source" binding:"required"`
	// SourceRef 是来源内精准标识；platform=skill name，clawhub=slug。
	SourceRef string `json:"source_ref" binding:"required"`
	// Name 是 skill 在版本内的目录名；clawhub 必填（displayName），platform 可空（以 DB 为准）。
	Name string `json:"name"`
	// Version 是要配进版本的 skill 版本号。
	Version string `json:"version" binding:"required"`
}

// ===== 实例 skill app-skills =====

// InstallAppSkillRequest 是 POST /api/v1/apps/:appId/skills 的请求体。
type InstallAppSkillRequest struct {
	// Source 是 skill 来源类型：platform（平台库）或 clawhub（ClawHub 市场）。
	Source string `json:"source" binding:"required"`
	// SourceRef 是来源内精准标识：platform=name，clawhub=slug。
	SourceRef string `json:"source_ref" binding:"required"`
	// Name 是 skill 在实例内的目录名（唯一键），不同 app 间可重名。
	Name string `json:"name" binding:"required"`
	// Version 是要安装的版本号，由来源方定义（如 semver 或日期戳）。
	Version string `json:"version" binding:"required"`
}

// UpdateAppSkillRequest 是 POST /api/v1/apps/:appId/skills/:skillName/update 的请求体。
type UpdateAppSkillRequest struct {
	// Version 是目标版本号，必须与 source 端已发布的版本对应。
	Version string `json:"version" binding:"required"`
}

// ===== 定制技能工单 =====

// SubmitSkillTicketRequest 提交需求工单请求体。
type SubmitSkillTicketRequest struct {
	Title       string `json:"title"`
	Description string `json:"description"`
}

// SendSkillTicketMessageRequest 发送工单文本消息请求体。
type SendSkillTicketMessageRequest struct {
	Text string `json:"text"`
}

// SetSkillTicketQuoteRequest 管理员报价请求体(分)。
type SetSkillTicketQuoteRequest struct {
	QuoteAmountCents int64 `json:"quote_amount_cents"`
}

// RejectSkillTicketRequest 管理员拒绝请求体。
type RejectSkillTicketRequest struct {
	Reason string `json:"reason"`
}

// CustomSkillTargetDTO 交付定制技能时的单条目标范围。
// targets 以 JSON 数组字符串形式随 multipart/form-data 提交，每条描述一个组织及其可见受众。
type CustomSkillTargetDTO struct {
	OrgID    string `json:"org_id" binding:"required"`
	Audience string `json:"audience" binding:"required"` // all_org|org_admins|requester_only
}

// UpdateCustomSkillTargetsRequest 编辑已交付定制技能可见范围请求体。
type UpdateCustomSkillTargetsRequest struct {
	Targets []CustomSkillTargetDTO `json:"targets"`
}

// DeliverCustomSkillRequest 交付定制技能(归档走 multipart file,本体描述目标范围与描述)。
// 注:交付实际走 multipart/form-data(字段 ticket_id/description/targets(JSON 串)+ file),
// 不以 JSON struct 绑定;此类型仅作文档占位与字段约定记录,不直接用于请求绑定。

// ===== 实例会话 hermes-conversation =====

// CreateConversationRequest 是 POST /hermes/conversations 的请求体（新建 web 会话）。
type CreateConversationRequest struct {
	// Title 是可选会话标题；前端不填时 service 层按默认逻辑命名。
	Title string `json:"title"`
}

// ConversationChatRequest 是续聊请求体。
type ConversationChatRequest struct {
	// Message 是消息内容：文字字符串，或多模态 parts 数组
	// [{type:"text",text} | {type:"input_file",file_id,filename,mime}]。
	// 文件 part 由 service 富化为带 file_url 的预签名引用后转发 oc-ops。
	Message any `json:"message" binding:"required"`
}

// RenameConversationRequest 是 PATCH /hermes/conversations/:sid 的请求体。
type RenameConversationRequest struct {
	Title string `json:"title" binding:"required"` // 新标题，必填
}

// ===== 公开配置 config =====

// PublicConfigResponse 是 GET /api/v1/config 的响应：登录前可读的平台级前端配置。
type PublicConfigResponse struct {
	// DefaultLocale 是平台默认界面语言（en/zh），登录页 localStorage 为空时采用。
	DefaultLocale string `json:"default_locale"`
	// SupportedLocales 是平台受支持的界面语言集合，供前端渲染语言选择器。
	SupportedLocales []string `json:"supported_locales"`
	// WebPublishDevMode 表示平台是否开启 web-publish 本地/dev 自签模式（config.WebPublish.DevSelfSignedCert）。
	// 仅供前端决定是否在 DNS provider 下拉中展示「本地调试(local)」选项；生产恒为 false。
	WebPublishDevMode bool `json:"web_publish_dev_mode"`
}

// ===== 企业 web-publish 发布能力配置 web-publish =====

// ConfigureWebPublishRequest 是平台管理员配置企业发布能力的请求体。
type ConfigureWebPublishRequest struct {
	// BaseDomain 是企业 web-publish 的根域名（如 apps.example.com），必填。
	BaseDomain string `json:"base_domain" binding:"required"`
	// DNSProvider 是 DNS provider 枚举值（如 aliyun），必填；service 层校验白名单。
	DNSProvider string `json:"dns_provider" binding:"required"`
	// Credentials 是 provider 凭证明文 map（如 access_key_id / access_key_secret）；
	// 为空时不更新已有凭证密文，service 端加密后落库，明文不持久化。
	Credentials map[string]string `json:"credentials"`
	// SiteTTLDays 是站点 TLS 证书/DNS 记录的 TTL 天数；<=0 时 service 默认填 7。
	SiteTTLDays int32 `json:"site_ttl_days"`
	// MaxSites 是企业下最大站点数；<=0 时 service 默认填 20。
	MaxSites int32 `json:"max_sites"`
}
