package apierror

// 本文件集中登记「审计」（audit）、「平台总览」（platform_overview）、「模型」（model）三个
// 较小 domain handler 层错误文案的 MsgKey 与中/英译文。
// 范围覆盖 internal/api/handlers/audit.go、internal/api/handlers/platform_overview.go、
// internal/api/handlers/models.go 中内联的静态中文 apierror.New 调用。
// zh 译文逐字取自 handler 原中文（标点/空格不改），en 为忠实英译。
// 通用「资源不存在」复用 common 的 MsgNotFound；其余文案因带 domain 专有措辞（如「仅平台
// 管理员可访问」「查询平台总览失败」「无权查看模型列表」）各自成 key。

// 审计 domain 静态错误 MsgKey 常量（前缀 err.audit.*）。
const (
	// MsgAuditMissingTarget 缺少 target_type 或 target_id。
	MsgAuditMissingTarget MsgKey = "err.audit.missing_target"
	// MsgAuditForbidden 无权执行该操作。
	MsgAuditForbidden MsgKey = "err.audit.forbidden"
	// MsgAuditServiceUnavailable 服务暂时不可用。
	MsgAuditServiceUnavailable MsgKey = "err.audit.service_unavailable"
)

// 平台总览 domain 静态错误 MsgKey 常量（前缀 err.platform_overview.*）。
const (
	// MsgPlatformOverviewForbidden 仅平台管理员可访问。
	MsgPlatformOverviewForbidden MsgKey = "err.platform_overview.forbidden"
	// MsgPlatformOverviewQueryFailed 查询平台总览失败。
	MsgPlatformOverviewQueryFailed MsgKey = "err.platform_overview.query_failed"
)

// 模型 domain 静态错误 MsgKey 常量（前缀 err.model.*）。
const (
	// MsgModelForbidden 无权查看模型列表。
	MsgModelForbidden MsgKey = "err.model.forbidden"
	// MsgModelUnavailable 模型列表暂时不可用。
	MsgModelUnavailable MsgKey = "err.model.unavailable"
)

// init 把审计 / 平台总览 / 模型 domain 错误译文并入中心 catalog。
func init() {
	Register(map[MsgKey]map[string]string{
		MsgAuditMissingTarget:      {"zh": "缺少 target_type 或 target_id", "en": "Missing target_type or target_id"},
		MsgAuditForbidden:          {"zh": "无权执行该操作", "en": "You are not allowed to perform this operation"},
		MsgAuditServiceUnavailable: {"zh": "服务暂时不可用", "en": "The service is temporarily unavailable"},

		MsgPlatformOverviewForbidden:   {"zh": "仅平台管理员可访问", "en": "Only platform administrators can access this"},
		MsgPlatformOverviewQueryFailed: {"zh": "查询平台总览失败", "en": "Failed to query the platform overview"},

		MsgModelForbidden:   {"zh": "无权查看模型列表", "en": "You are not allowed to view the model list"},
		MsgModelUnavailable: {"zh": "模型列表暂时不可用", "en": "The model list is temporarily unavailable"},
	})
}
