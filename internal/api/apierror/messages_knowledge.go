package apierror

// 本文件集中登记「知识库」（knowledge）domain handler 层错误文案的 MsgKey 与中/英译文。
// 范围覆盖三个 handler 文件内联的静态中文 apierror.New 调用：
//   - internal/api/handlers/knowledge.go：上传参数缺失、embedding 模型名校验、
//     writeKnowledgeError 哨兵错误映射等；
//   - internal/api/handlers/knowledge_multipart.go：分片序号/大小校验、分片请求体绑定；
//   - internal/api/handlers/runtime_knowledge.go：runtime token 缺失、上传文件读取失败等。
// zh 译文逐字取自 handler 原中文（标点/空格不改），en 为忠实英译；相同中文复用同一 key
// （如「缺少 filename 参数」「模型名称不能为空」「请求参数非法」「缺少 runtime token」各
// 出现两处）。通用语义（资源不存在 → MsgNotFound）复用 messages_common.go 已有 key，不在
// 本文件重复。
// 注意：配额明细 validationServiceMessage、SafeErrorMessage(err) 等纯动态文案不入 catalog，保留原样。
// maxKnowledgeUploadMessage 已替换为 MsgKnowledgeFileTooLarge（带 %d MB 占位符），文件超限提示
// 在调用点传入换算后的 MB 数值作为 arg，支持双语本地化。

// 知识库 domain 静态错误 MsgKey 常量。
const (
	// MsgKnowledgeFileTooLarge 单文件超过上传上限；调用时传入 MB 数值作为 %d 参数。
	// zh 逐字与原 maxKnowledgeUploadMessage 模板一致，en 为忠实英译。
	MsgKnowledgeFileTooLarge MsgKey = "err.knowledge.file_too_large"
	// MsgKnowledgeMissingFilename 缺少 filename 参数；SaveOrg/SaveApp 两处复用。
	MsgKnowledgeMissingFilename MsgKey = "err.knowledge.missing_filename"
	// MsgKnowledgeMissingFileSize 缺少有效的文件大小信息（无法做累计容量预校验）。
	MsgKnowledgeMissingFileSize MsgKey = "err.knowledge.missing_file_size"
	// MsgKnowledgeModelNameRequired embedding 模型名称不能为空；绑定失败与空值两处复用。
	MsgKnowledgeModelNameRequired MsgKey = "err.knowledge.model_name_required"
	// MsgKnowledgeForbidden 无权访问该知识库。
	MsgKnowledgeForbidden MsgKey = "err.knowledge.forbidden"
	// MsgKnowledgeRuntimeTokenInvalid runtime token 无效。
	MsgKnowledgeRuntimeTokenInvalid MsgKey = "err.knowledge.runtime_token_invalid"
	// MsgKnowledgeDatasetCreating 知识库正在初始化，请稍后重试。
	MsgKnowledgeDatasetCreating MsgKey = "err.knowledge.dataset_creating"
	// MsgKnowledgeNotConfigured 知识库未配置。
	MsgKnowledgeNotConfigured MsgKey = "err.knowledge.not_configured"
	// MsgKnowledgeMultipartUnavailable 分片上传不可用（未启用对象存储）。
	MsgKnowledgeMultipartUnavailable MsgKey = "err.knowledge.multipart_unavailable"
	// MsgKnowledgeUploadSessionNotFound 上传会话不存在或已过期。
	MsgKnowledgeUploadSessionNotFound MsgKey = "err.knowledge.upload_session_not_found"
	// MsgKnowledgePartNumberInvalid 分片序号非法。
	MsgKnowledgePartNumberInvalid MsgKey = "err.knowledge.part_number_invalid"
	// MsgKnowledgeMissingPartSize 缺少有效的分片大小信息。
	MsgKnowledgeMissingPartSize MsgKey = "err.knowledge.missing_part_size"
	// MsgKnowledgePartTooLarge 分片大小超过上限。
	MsgKnowledgePartTooLarge MsgKey = "err.knowledge.part_too_large"
	// MsgKnowledgeInvalidRequest 请求参数非法；InitOrgUpload/InitAppUpload 两处复用。
	MsgKnowledgeInvalidRequest MsgKey = "err.knowledge.invalid_request"
	// MsgKnowledgeMissingRuntimeToken 缺少 runtime token；Search/AddFile 两处复用。
	MsgKnowledgeMissingRuntimeToken MsgKey = "err.knowledge.missing_runtime_token"
	// MsgKnowledgeMissingFileField 缺少 file 字段。
	MsgKnowledgeMissingFileField MsgKey = "err.knowledge.missing_file_field"
	// MsgKnowledgeOpenFileFailed 读取上传文件失败。
	MsgKnowledgeOpenFileFailed MsgKey = "err.knowledge.open_file_failed"
)

// init 把知识库 domain 错误译文并入中心 catalog。
func init() {
	Register(map[MsgKey]map[string]string{
		MsgKnowledgeFileTooLarge:          {"zh": "单文件最大支持 %dMB", "en": "Maximum file size is %dMB"},
		MsgKnowledgeMissingFilename:       {"zh": "缺少 filename 参数", "en": "Missing filename parameter"},
		MsgKnowledgeMissingFileSize:       {"zh": "缺少有效的文件大小信息", "en": "Missing valid file size information"},
		MsgKnowledgeModelNameRequired:     {"zh": "模型名称不能为空", "en": "The model name must not be empty"},
		MsgKnowledgeForbidden:             {"zh": "无权访问该知识库", "en": "You are not allowed to access this knowledge base"},
		MsgKnowledgeRuntimeTokenInvalid:   {"zh": "runtime token 无效", "en": "Invalid runtime token"},
		MsgKnowledgeDatasetCreating:       {"zh": "知识库正在初始化，请稍后重试", "en": "The knowledge base is initializing, please try again later"},
		MsgKnowledgeNotConfigured:         {"zh": "知识库未配置", "en": "The knowledge base is not configured"},
		MsgKnowledgeMultipartUnavailable:  {"zh": "分片上传不可用", "en": "Multipart upload is unavailable"},
		MsgKnowledgeUploadSessionNotFound: {"zh": "上传会话不存在或已过期", "en": "The upload session does not exist or has expired"},
		MsgKnowledgePartNumberInvalid:     {"zh": "分片序号非法", "en": "Invalid part number"},
		MsgKnowledgeMissingPartSize:       {"zh": "缺少有效的分片大小信息", "en": "Missing valid part size information"},
		MsgKnowledgePartTooLarge:          {"zh": "分片大小超过上限", "en": "The part size exceeds the limit"},
		MsgKnowledgeInvalidRequest:        {"zh": "请求参数非法", "en": "Invalid request parameters"},
		MsgKnowledgeMissingRuntimeToken:   {"zh": "缺少 runtime token", "en": "Missing runtime token"},
		MsgKnowledgeMissingFileField:      {"zh": "缺少 file 字段", "en": "Missing file field"},
		MsgKnowledgeOpenFileFailed:        {"zh": "读取上传文件失败", "en": "Failed to read the uploaded file"},
	})
}
