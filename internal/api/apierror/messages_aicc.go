package apierror

// 本文件集中登记 AICC 公开接口 handler 层静态错误文案。
// 范围覆盖 internal/api/handlers/public_aicc.go 中访客会话、隐私同意、留资和图片上传的哨兵错误映射。
const (
	// MsgAICCConsentRequired 表示访客必须先同意隐私说明。
	MsgAICCConsentRequired MsgKey = "err.aicc.consent_required"
	// MsgAICCLeadRequired 表示访客必须先提交必填联系信息。
	MsgAICCLeadRequired MsgKey = "err.aicc.lead_required"
	// MsgAICCOffline 表示客服智能体当前不可对外接待。
	MsgAICCOffline MsgKey = "err.aicc.offline"
	// MsgAICCInvalidSession 表示公开会话 token 无效或已过期。
	MsgAICCInvalidSession MsgKey = "err.aicc.invalid_session"
	// MsgAICCInvalidMessage 表示目标消息不存在或不可反馈。
	MsgAICCInvalidMessage MsgKey = "err.aicc.invalid_message"
	// MsgAICCImageUnavailable 表示图片上传依赖暂不可用。
	MsgAICCImageUnavailable MsgKey = "err.aicc.image_unavailable"
	// MsgAICCDomainForbidden 表示网页挂件来源域名不在允许列表内。
	MsgAICCDomainForbidden MsgKey = "err.aicc.domain_forbidden"
	// MsgAICCRateLimited 表示公开入口请求过于频繁。
	MsgAICCRateLimited MsgKey = "err.aicc.rate_limited"
)

// init 把 AICC 公开接口错误译文并入中心 catalog。
func init() {
	Register(map[MsgKey]map[string]string{
		MsgAICCConsentRequired:  {"zh": "需要先同意隐私说明", "en": "Please consent to the privacy notice first"},
		MsgAICCLeadRequired:     {"zh": "需要先提交必填联系信息", "en": "Please submit the required contact information first"},
		MsgAICCOffline:          {"zh": "客服已下线", "en": "Customer service is offline"},
		MsgAICCInvalidSession:   {"zh": "会话已失效", "en": "The session has expired"},
		MsgAICCInvalidMessage:   {"zh": "消息不可反馈", "en": "This message cannot receive feedback"},
		MsgAICCImageUnavailable: {"zh": "图片上传不可用", "en": "Image upload is unavailable"},
		MsgAICCDomainForbidden:  {"zh": "当前网站未被允许使用该客服挂件", "en": "This site is not allowed to use this customer-service widget"},
		MsgAICCRateLimited:      {"zh": "请求过于频繁，请稍后再试", "en": "Too many requests. Please try again later"},
	})
}
