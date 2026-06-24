package apierror

// 本文件集中登记「应用/实例」（app）domain handler 层错误文案的 MsgKey 与中/英译文。
// 范围覆盖 internal/api/handlers/apps.go 与 internal/api/handlers/app_runtime.go 中内联的
// 静态中文 apierror.New 调用：请求体格式错误、无权访问应用、应用不存在、助手版本不在允许列表、
// 不支持的语言、服务不可用，以及运行操作的无权执行/状态冲突/运行操作不可用等。
// zh 译文逐字取自 handler 原中文（标点/空格不改），en 为忠实英译；相同中文复用同一 key
// （「请求体格式错误」三处复用；「应用不存在」跨 apps.go 与 app_runtime.go 复用同一 key）。
// 成员创建校验为运行期动态明细（validationServiceMessage），保留 handler 内动态文案，不进 catalog。
// 注意「不支持的语言，请使用 en 或 zh」与 auth domain 的「不支持的语言」措辞不同，各自独立成 key。

// 应用/实例 domain 静态错误 MsgKey 常量。
const (
	// MsgAppInvalidRequest 请求体格式错误；SwitchVersion/UpdateKnowledgeQuota/UpdateLocale 三处复用。
	MsgAppInvalidRequest MsgKey = "err.app.invalid_request"
	// MsgAppForbidden 无权访问该应用。
	MsgAppForbidden MsgKey = "err.app.forbidden"
	// MsgAppNotFound 应用不存在；apps.go 与 app_runtime.go 复用同一 key。
	MsgAppNotFound MsgKey = "err.app.not_found"
	// MsgAppVersionNotAllowed 助手版本不在企业允许列表内。
	MsgAppVersionNotAllowed MsgKey = "err.app.version_not_allowed"
	// MsgAppInvalidLocale 不支持的语言，请使用 en 或 zh（与 auth domain 措辞不同，单独成 key）。
	MsgAppInvalidLocale MsgKey = "err.app.invalid_locale"
	// MsgAppServiceUnavailable 服务暂时不可用（apps 接口兜底）。
	MsgAppServiceUnavailable MsgKey = "err.app.service_unavailable"
	// MsgAppRuntimeOpForbidden 无权执行该运行操作。
	MsgAppRuntimeOpForbidden MsgKey = "err.app.runtime_op_forbidden"
	// MsgAppNotReinitializable 应用当前状态不允许重新初始化。
	MsgAppNotReinitializable MsgKey = "err.app.not_reinitializable"
	// MsgAppRuntimeOpUnavailable 运行操作暂不可用（运行操作接口兜底）。
	MsgAppRuntimeOpUnavailable MsgKey = "err.app.runtime_op_unavailable"
)

// init 把应用/实例 domain 错误译文并入中心 catalog。
func init() {
	Register(map[MsgKey]map[string]string{
		MsgAppInvalidRequest:       {"zh": "请求体格式错误", "en": "Malformed request body"},
		MsgAppForbidden:            {"zh": "无权访问该应用", "en": "You are not allowed to access this application"},
		MsgAppNotFound:             {"zh": "应用不存在", "en": "The application does not exist"},
		MsgAppVersionNotAllowed:    {"zh": "助手版本不在企业允许列表内", "en": "The assistant version is not in the organization's allowlist"},
		MsgAppInvalidLocale:        {"zh": "不支持的语言，请使用 en 或 zh", "en": "Unsupported language; please use en or zh"},
		MsgAppServiceUnavailable:   {"zh": "服务暂时不可用", "en": "The service is temporarily unavailable"},
		MsgAppRuntimeOpForbidden:   {"zh": "无权执行该运行操作", "en": "You are not allowed to perform this runtime operation"},
		MsgAppNotReinitializable:   {"zh": "应用当前状态不允许重新初始化", "en": "The application's current status does not allow reinitialization"},
		MsgAppRuntimeOpUnavailable: {"zh": "运行操作暂不可用", "en": "The runtime operation is temporarily unavailable"},
	})
}
