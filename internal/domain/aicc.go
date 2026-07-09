package domain

const (
	// AICCAgentStatus* 约束智能体生命周期，与迁移里的 CHECK 保持一致。
	AICCAgentStatusDraft   = "draft"
	AICCAgentStatusActive  = "active"
	AICCAgentStatusPaused  = "paused"
	AICCAgentStatusDeleted = "deleted"

	// AICCPrivacyMode* 控制访客会话是否只展示隐私说明，还是必须先同意。
	AICCPrivacyModeNotice          = "notice"
	AICCPrivacyModeConsentRequired = "consent_required"

	// AICCChannel* 区分访客入口来源，便于后续域名白名单和运营统计按渠道处理。
	AICCChannelWebLink   = "web_link"
	AICCChannelWebWidget = "web_widget"
	AICCChannelVoice     = "voice"

	// AICCResolution* 记录会话是否已解决，默认 unknown 供反馈与运营逻辑后续收敛。
	AICCResolutionResolved   = "resolved"
	AICCResolutionUnresolved = "unresolved"
	AICCResolutionUnknown    = "unknown"

	// AICCLeadStatus* 记录留资流程是否完成，供会话阻断与运营统计共用。
	AICCLeadStatusPending  = "pending"
	AICCLeadStatusComplete = "complete"
	AICCLeadStatusSkipped  = "skipped"

	// AICCMessageDirection* 区分访客消息、助手回复与系统提示。
	AICCMessageDirectionVisitor   = "visitor"
	AICCMessageDirectionAssistant = "assistant"
	AICCMessageDirectionSystem    = "system"

	// AICCMessageContentType* 描述消息载荷形态，便于后续渲染和审核。
	AICCMessageContentTypeText  = "text"
	AICCMessageContentTypeImage = "image"
	AICCMessageContentTypeMixed = "mixed"

	// AICCLeadFieldType* 限定留资字段类型，与数据库 CHECK 和前端表单配置保持一致。
	AICCLeadFieldTypeText   = "text"
	AICCLeadFieldTypePhone  = "phone"
	AICCLeadFieldTypeEmail  = "email"
	AICCLeadFieldTypeNumber = "number"

	// AICCKnowledgeScopeType* 描述智能体可挂载的知识来源类别。
	AICCKnowledgeScopeTypeOrg         = "org"
	AICCKnowledgeScopeTypeIndustry    = "industry"
	AICCKnowledgeScopeTypeAppDocument = "app_document"
)
