package apierror

// 本文件集中登记「渠道」（channel）domain handler 层错误文案的 MsgKey 与中/英译文。
// 范围覆盖 internal/api/handlers/channels.go 中内联的静态中文 apierror.New 调用：
// 无权操作渠道、应用或渠道绑定不存在、当前渠道未启用、渠道服务暂时不可用。
// zh 译文逐字取自 handler 原中文（标点/空格不改），en 为忠实英译。
// NOT_FOUND 分支文案为渠道专有的「应用或渠道绑定不存在」，与通用 MsgNotFound 措辞不同，
// 单独成 key 而不复用 common。

// 渠道 domain 静态错误 MsgKey 常量（前缀 err.channel.*）。
const (
	// MsgChannelForbidden 无权操作渠道。
	MsgChannelForbidden MsgKey = "err.channel.forbidden"
	// MsgChannelBindingNotFound 应用或渠道绑定不存在。
	MsgChannelBindingNotFound MsgKey = "err.channel.binding_not_found"
	// MsgChannelAdapterMissing 当前渠道未启用。
	MsgChannelAdapterMissing MsgKey = "err.channel.adapter_missing"
	// MsgChannelUnavailable 渠道服务暂时不可用。
	MsgChannelUnavailable MsgKey = "err.channel.unavailable"
	// MsgChannelInvalidRequest 渠道请求参数无效（如飞书凭证缺失或请求体格式错误）。
	MsgChannelInvalidRequest MsgKey = "err.channel.invalid_request"
)

// init 把渠道 domain 错误译文并入中心 catalog。
func init() {
	Register(map[MsgKey]map[string]string{
		MsgChannelForbidden:       {"zh": "无权操作渠道", "en": "You are not allowed to operate this channel"},
		MsgChannelBindingNotFound: {"zh": "应用或渠道绑定不存在", "en": "The application or channel binding does not exist"},
		MsgChannelAdapterMissing:  {"zh": "当前渠道未启用", "en": "The current channel is not enabled"},
		MsgChannelUnavailable:     {"zh": "渠道服务暂时不可用", "en": "The channel service is temporarily unavailable"},
		MsgChannelInvalidRequest:  {"zh": "渠道请求参数无效", "en": "Invalid channel request parameters"},
	})
}
