package apierror

// 本文件集中登记「bootstrap」domain handler 层错误文案的 MsgKey 与中/英译文。
// 范围覆盖 internal/api/handlers/bootstrap.go 中内联的静态中文 apierror.New 调用：
// 缺 / 无效 control token、token 与目标 app 不匹配、app 未就绪、组装失败。
// bootstrap 是内部端点（oc-ops / app pod 调用），同样将静态中文接入 catalog 以保持一致。
// zh 译文逐字取自 handler 原中文（标点/空格不改），en 为忠实英译。

// bootstrap domain 静态错误 MsgKey 常量（前缀 err.bootstrap.*）。
const (
	// MsgBootstrapMissingToken 缺少 control token。
	MsgBootstrapMissingToken MsgKey = "err.bootstrap.missing_token"
	// MsgBootstrapInvalidToken control token 无效。
	MsgBootstrapInvalidToken MsgKey = "err.bootstrap.invalid_token"
	// MsgBootstrapTokenMismatch control token 与目标 app 不匹配。
	MsgBootstrapTokenMismatch MsgKey = "err.bootstrap.token_mismatch"
	// MsgBootstrapAppNotReady app 未就绪。
	MsgBootstrapAppNotReady MsgKey = "err.bootstrap.app_not_ready"
	// MsgBootstrapObjectStorageRequired 普通应用 bootstrap 缺少对象存储依赖。
	MsgBootstrapObjectStorageRequired MsgKey = "err.bootstrap.object_storage_required"
	// MsgBootstrapUnsupportedAppType bootstrap 不支持该应用类型。
	MsgBootstrapUnsupportedAppType MsgKey = "err.bootstrap.unsupported_app_type"
	// MsgBootstrapAssembleFailed bootstrap 组装失败。
	MsgBootstrapAssembleFailed MsgKey = "err.bootstrap.assemble_failed"
)

// init 把 bootstrap domain 错误译文并入中心 catalog。
func init() {
	Register(map[MsgKey]map[string]string{
		MsgBootstrapMissingToken:          {"zh": "缺少 control token", "en": "Missing control token"},
		MsgBootstrapInvalidToken:          {"zh": "control token 无效", "en": "Invalid control token"},
		MsgBootstrapTokenMismatch:         {"zh": "control token 与目标 app 不匹配", "en": "The control token does not match the target app"},
		MsgBootstrapAppNotReady:           {"zh": "app 未就绪", "en": "The app is not ready"},
		MsgBootstrapObjectStorageRequired: {"zh": "普通应用启动需要对象存储", "en": "Object storage is required to bootstrap a standard app"},
		MsgBootstrapUnsupportedAppType:    {"zh": "不支持的应用类型", "en": "Unsupported app type"},
		MsgBootstrapAssembleFailed:        {"zh": "bootstrap 组装失败", "en": "Failed to assemble the bootstrap response"},
	})
}
