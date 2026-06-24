package apierror

// 本文件集中登记「行业知识库」（industry-knowledge）domain handler 层错误文案的
// MsgKey 与中/英译文。范围覆盖 internal/api/handlers/industry_knowledge.go 中内联的
// 静态中文 apierror.New 调用：外部上传鉴权失败、上传参数缺失/格式非法、日期筛选参数
// 校验、行业库 CRUD 哨兵错误映射等。
// zh 译文逐字取自 handler 原中文（标点/空格不改），en 为忠实英译；相同中文复用同一
// key（如「行业知识库上传鉴权失败」「无权访问该知识库」各出现两处）。
// 通用语义（资源不存在 → MsgNotFound）复用 messages_common.go 已有 key，不在本文件重复。

// 行业知识库 domain 静态错误 MsgKey 常量。
const (
	// MsgIndustryKnowledgeTokenInvalid 外部上传固定 token 鉴权失败。
	MsgIndustryKnowledgeTokenInvalid MsgKey = "err.industry_knowledge.token_invalid"
	// MsgIndustryKnowledgeUploadFormatInvalid 上传 multipart 文件格式不合法。
	MsgIndustryKnowledgeUploadFormatInvalid MsgKey = "err.industry_knowledge.upload_format_invalid"
	// MsgIndustryKnowledgeMissingIndustryName 缺少 industry_name 参数。
	MsgIndustryKnowledgeMissingIndustryName MsgKey = "err.industry_knowledge.missing_industry_name"
	// MsgIndustryKnowledgeMissingFile 缺少 file 文件。
	MsgIndustryKnowledgeMissingFile MsgKey = "err.industry_knowledge.missing_file"
	// MsgIndustryKnowledgeMissingFilename 缺少 file 文件名。
	MsgIndustryKnowledgeMissingFilename MsgKey = "err.industry_knowledge.missing_filename"
	// MsgIndustryKnowledgeForbidden 无权访问该知识库。
	MsgIndustryKnowledgeForbidden MsgKey = "err.industry_knowledge.forbidden"
	// MsgIndustryKnowledgeMissingName 缺少 name 参数。
	MsgIndustryKnowledgeMissingName MsgKey = "err.industry_knowledge.missing_name"
	// MsgIndustryKnowledgeCreatedRangeInvalid created_from 不能晚于 created_to。
	MsgIndustryKnowledgeCreatedRangeInvalid MsgKey = "err.industry_knowledge.created_range_invalid"
	// MsgIndustryKnowledgeDateFormatInvalid 日期参数必须使用 YYYY-MM-DD 格式；%s 为参数名。
	MsgIndustryKnowledgeDateFormatInvalid MsgKey = "err.industry_knowledge.date_format_invalid"
	// MsgIndustryKnowledgeMissingUploadFilename 缺少 filename 参数。
	MsgIndustryKnowledgeMissingUploadFilename MsgKey = "err.industry_knowledge.missing_upload_filename"
	// MsgIndustryKnowledgeNotFound 行业知识库不存在。
	MsgIndustryKnowledgeNotFound MsgKey = "err.industry_knowledge.not_found"
	// MsgIndustryKnowledgeNameTaken 行业知识库名称已存在。
	MsgIndustryKnowledgeNameTaken MsgKey = "err.industry_knowledge.name_taken"
	// MsgIndustryKnowledgeInUse 行业知识库正在被助手版本引用，不可删除。
	MsgIndustryKnowledgeInUse MsgKey = "err.industry_knowledge.in_use"
	// MsgIndustryKnowledgeDatasetCreating 知识库正在初始化，请稍后重试。
	MsgIndustryKnowledgeDatasetCreating MsgKey = "err.industry_knowledge.dataset_creating"
	// MsgIndustryKnowledgeNotConfigured 知识库未配置。
	MsgIndustryKnowledgeNotConfigured MsgKey = "err.industry_knowledge.not_configured"
	// MsgIndustryKnowledgeInternal 行业知识库操作失败（domain 兜底）。
	MsgIndustryKnowledgeInternal MsgKey = "err.industry_knowledge.internal"
)

// init 把行业知识库 domain 错误译文并入中心 catalog。
func init() {
	Register(map[MsgKey]map[string]string{
		MsgIndustryKnowledgeTokenInvalid:          {"zh": "行业知识库上传鉴权失败", "en": "Industry knowledge base upload authentication failed"},
		MsgIndustryKnowledgeUploadFormatInvalid:   {"zh": "上传文件格式不合法", "en": "The uploaded file format is invalid"},
		MsgIndustryKnowledgeMissingIndustryName:   {"zh": "缺少 industry_name 参数", "en": "Missing industry_name parameter"},
		MsgIndustryKnowledgeMissingFile:           {"zh": "缺少 file 文件", "en": "Missing file"},
		MsgIndustryKnowledgeMissingFilename:       {"zh": "缺少 file 文件名", "en": "Missing file name"},
		MsgIndustryKnowledgeForbidden:             {"zh": "无权访问该知识库", "en": "You are not allowed to access this knowledge base"},
		MsgIndustryKnowledgeMissingName:           {"zh": "缺少 name 参数", "en": "Missing name parameter"},
		MsgIndustryKnowledgeCreatedRangeInvalid:   {"zh": "created_from 不能晚于 created_to", "en": "created_from must not be later than created_to"},
		MsgIndustryKnowledgeDateFormatInvalid:     {"zh": "%s 必须使用 YYYY-MM-DD 格式", "en": "%s must use the YYYY-MM-DD format"},
		MsgIndustryKnowledgeMissingUploadFilename: {"zh": "缺少 filename 参数", "en": "Missing filename parameter"},
		MsgIndustryKnowledgeNotFound:              {"zh": "行业知识库不存在", "en": "The industry knowledge base does not exist"},
		MsgIndustryKnowledgeNameTaken:             {"zh": "行业知识库名称已存在", "en": "The industry knowledge base name already exists"},
		MsgIndustryKnowledgeInUse:                 {"zh": "行业知识库正在被助手版本引用，不可删除", "en": "The industry knowledge base is referenced by an assistant version and cannot be deleted"},
		MsgIndustryKnowledgeDatasetCreating:       {"zh": "知识库正在初始化，请稍后重试", "en": "The knowledge base is initializing, please try again later"},
		MsgIndustryKnowledgeNotConfigured:         {"zh": "知识库未配置", "en": "The knowledge base is not configured"},
		MsgIndustryKnowledgeInternal:              {"zh": "行业知识库操作失败", "en": "Industry knowledge base operation failed"},
	})
}
