package apierror

// 本文件集中登记 handler 层「公共错误」的 MsgKey 与中/英译文。
// 范围：mappedServiceErrorRules 中的静态文案（资源不存在、运行时不可用、各实例子域 403/不支持/
// 输出不兼容、内部错误）与请求体绑定错误模板（writeBindError 系列）。
// zh 译文逐字取自 handler 层原有中文（标点/空格不改），en 为忠实英译。
// 相同中文复用同一 key（如三处「实例容器未运行…」与 kanban/conversation 的输出不兼容文案）。

// 公共静态错误 MsgKey 常量。
const (
	// MsgNotFound 通用资源不存在（404）。
	MsgNotFound MsgKey = "err.common.not_found"
	// MsgRuntimeNotAvailable 实例容器未运行；kanban/cron/conversation 三处文案一致，复用此 key。
	MsgRuntimeNotAvailable MsgKey = "err.common.runtime_not_available"
	// MsgInternal 服务器内部错误，作为兜底文案。
	MsgInternal MsgKey = "err.common.internal"
	// MsgHermesIncompatible Hermes 输出不兼容；kanban/conversation 输出非法共用此文案。
	MsgHermesIncompatible MsgKey = "err.common.hermes_incompatible"

	// MsgKanbanForbidden 无权访问该实例任务看板。
	MsgKanbanForbidden MsgKey = "err.kanban.forbidden"
	// MsgKanbanNotSupported dev 镜像不支持任务看板。
	MsgKanbanNotSupported MsgKey = "err.kanban.not_supported"

	// MsgCronForbidden 无权访问该实例定时任务。
	MsgCronForbidden MsgKey = "err.cron.forbidden"
	// MsgCronNotSupported dev 镜像不支持定时任务。
	MsgCronNotSupported MsgKey = "err.cron.not_supported"
	// MsgCronBadRequest 定时任务请求参数非法。
	MsgCronBadRequest MsgKey = "err.cron.bad_request"
	// MsgCronOutputInvalid Hermes Cron 输出不兼容（与通用 Hermes 文案措辞不同，单独成 key）。
	MsgCronOutputInvalid MsgKey = "err.cron.output_invalid"

	// MsgConversationForbidden 无权访问该实例会话。
	MsgConversationForbidden MsgKey = "err.conversation.forbidden"
	// MsgConversationNotSupported dev 镜像不支持会话。
	MsgConversationNotSupported MsgKey = "err.conversation.not_supported"
)

// 请求体绑定错误模板 MsgKey 常量（部分带 %s 占位符）。
const (
	// MsgBadRequestGeneric 绑定失败兜底：请求参数格式错误。
	MsgBadRequestGeneric MsgKey = "err.bind.bad_request"
	// MsgEmptyBody 请求体为空（io.EOF）。
	MsgEmptyBody MsgKey = "err.bind.empty_body"
	// MsgInvalidJSON 请求体不是合法 JSON（json.SyntaxError）。
	MsgInvalidJSON MsgKey = "err.bind.invalid_json"
	// MsgInvalidType 请求参数类型错误（json.UnmarshalTypeError），%s 为字段名。
	MsgInvalidType MsgKey = "err.bind.invalid_type"
	// MsgMissingRequiredFields 缺少必填参数（required tag），%s 为字段名列表。
	MsgMissingRequiredFields MsgKey = "err.bind.missing_required"
	// MsgValidationFailed 请求参数校验失败（非 required tag），%s 为字段名列表。
	MsgValidationFailed MsgKey = "err.bind.validation_failed"
)

// init 把公共错误译文并入中心 catalog。
func init() {
	Register(map[MsgKey]map[string]string{
		MsgNotFound:            {"zh": "资源不存在", "en": "Resource not found"},
		MsgRuntimeNotAvailable: {"zh": "实例容器未运行，请先在运行时 tab 启动", "en": "The instance container is not running. Please start it in the runtime tab first"},
		MsgInternal:            {"zh": "服务器内部错误", "en": "Internal server error"},
		MsgHermesIncompatible:  {"zh": "Hermes 版本可能不兼容，请联系平台管理员", "en": "The Hermes version may be incompatible. Please contact the platform administrator"},

		MsgKanbanForbidden:    {"zh": "无权访问该实例任务看板", "en": "You are not allowed to access this instance's task board"},
		MsgKanbanNotSupported: {"zh": "该实例运行的是 dev 镜像，任务看板不可用", "en": "This instance runs a dev image; the task board is unavailable"},

		MsgCronForbidden:     {"zh": "无权访问该实例定时任务", "en": "You are not allowed to access this instance's scheduled tasks"},
		MsgCronNotSupported:  {"zh": "该实例运行的是 dev 镜像，定时任务不可用", "en": "This instance runs a dev image; scheduled tasks are unavailable"},
		MsgCronBadRequest:    {"zh": "定时任务请求参数非法", "en": "Invalid scheduled task request parameters"},
		MsgCronOutputInvalid: {"zh": "Hermes Cron 版本可能不兼容，请联系平台管理员", "en": "The Hermes Cron version may be incompatible. Please contact the platform administrator"},

		MsgConversationForbidden:    {"zh": "无权访问该实例会话", "en": "You are not allowed to access this instance's conversations"},
		MsgConversationNotSupported: {"zh": "该实例运行的是 dev 镜像，会话不可用", "en": "This instance runs a dev image; conversations are unavailable"},

		MsgBadRequestGeneric:     {"zh": "请求参数格式错误", "en": "Malformed request parameters"},
		MsgEmptyBody:             {"zh": "请求体不能为空", "en": "Request body must not be empty"},
		MsgInvalidJSON:           {"zh": "请求体不是合法 JSON", "en": "Request body is not valid JSON"},
		MsgInvalidType:           {"zh": "请求参数类型错误: %s", "en": "Invalid request parameter type: %s"},
		MsgMissingRequiredFields: {"zh": "缺少必填参数: %s", "en": "Missing required parameters: %s"},
		MsgValidationFailed:      {"zh": "请求参数校验失败: %s", "en": "Request parameter validation failed: %s"},
	})
}
