package apierror

// 本文件集中登记「工作目录」（workspace）domain handler 层错误文案的 MsgKey 与中/英译文。
// 范围覆盖 internal/api/handlers/workspace.go 中内联的静态中文 apierror.New 调用：缺少 path
// 参数、无权访问工作目录、应用不存在、应用未关联节点或 adapter 未配置、非法工作目录路径、
// 工作目录不可用等。
// zh 译文逐字取自 handler 原中文（标点/空格不改），en 为忠实英译。
// 「应用不存在」与 app domain 文案相同但分属不同 handler 子域，按 domain 自包含原则单独成 key，
// 不跨 domain 复用 MsgAppNotFound；通用语义不在本文件重复定义。

// 工作目录 domain 静态错误 MsgKey 常量。
const (
	// MsgWorkspaceMissingPath 缺少 path 参数。
	MsgWorkspaceMissingPath MsgKey = "err.workspace.missing_path"
	// MsgWorkspaceForbidden 无权访问工作目录。
	MsgWorkspaceForbidden MsgKey = "err.workspace.forbidden"
	// MsgWorkspaceAppNotFound 应用不存在（workspace 子域）。
	MsgWorkspaceAppNotFound MsgKey = "err.workspace.app_not_found"
	// MsgWorkspaceNotConfigured 应用未关联节点或 adapter 未配置。
	MsgWorkspaceNotConfigured MsgKey = "err.workspace.not_configured"
	// MsgWorkspaceInvalidPath 非法工作目录路径。
	MsgWorkspaceInvalidPath MsgKey = "err.workspace.invalid_path"
	// MsgWorkspaceUnavailable 工作目录暂不可用。
	MsgWorkspaceUnavailable MsgKey = "err.workspace.unavailable"
)

// init 把工作目录 domain 错误译文并入中心 catalog。
func init() {
	Register(map[MsgKey]map[string]string{
		MsgWorkspaceMissingPath:   {"zh": "缺少 path 参数", "en": "Missing path parameter"},
		MsgWorkspaceForbidden:     {"zh": "无权访问工作目录", "en": "You are not allowed to access the workspace"},
		MsgWorkspaceAppNotFound:   {"zh": "应用不存在", "en": "The application does not exist"},
		MsgWorkspaceNotConfigured: {"zh": "应用未关联节点或 adapter 未配置", "en": "The application is not bound to a node or the adapter is not configured"},
		MsgWorkspaceInvalidPath:   {"zh": "非法工作目录路径", "en": "Invalid workspace path"},
		MsgWorkspaceUnavailable:   {"zh": "工作目录暂不可用", "en": "The workspace is temporarily unavailable"},
	})
}
