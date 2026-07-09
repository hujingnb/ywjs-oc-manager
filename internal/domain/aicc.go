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
)
