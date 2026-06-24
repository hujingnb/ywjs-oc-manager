package apierror

// 本文件集中登记「助手版本」（assistant-version）domain handler 层错误文案的
// MsgKey 与中/英译文。范围覆盖 internal/api/handlers/assistant_versions.go 中内联的
// 静态中文 apierror.New 调用：版本 CRUD 与版本内 skill 操作的哨兵错误映射、请求体
// 绑定失败提示等。
// zh 译文逐字取自 handler 原中文（标点/空格不改），en 为忠实英译；「请求体格式错误」
// 在 Create/Update/AddSkillFromLibrary 三处出现，复用同一 key。
// 「行业知识库不存在」与 industry_knowledge domain 的中文完全一致，跨 domain 复用
// MsgIndustryKnowledgeNotFound（见 messages_industry_knowledge.go），不在本文件重复。

// 助手版本 domain 静态错误 MsgKey 常量。
const (
	// MsgAssistantVersionForbidden 无权操作助手版本。
	MsgAssistantVersionForbidden MsgKey = "err.assistant_version.forbidden"
	// MsgAssistantVersionNotFound 助手版本不存在。
	MsgAssistantVersionNotFound MsgKey = "err.assistant_version.not_found"
	// MsgAssistantVersionNameTaken 助手版本名称已存在。
	MsgAssistantVersionNameTaken MsgKey = "err.assistant_version.name_taken"
	// MsgAssistantVersionInUse 助手版本正被引用，不可删除。
	MsgAssistantVersionInUse MsgKey = "err.assistant_version.in_use"
	// MsgAssistantVersionSkillNameTaken 版本内 skill 名称已存在。
	MsgAssistantVersionSkillNameTaken MsgKey = "err.assistant_version.skill_name_taken"
	// MsgAssistantVersionPlatformSkillNotFound 平台技能不存在。
	MsgAssistantVersionPlatformSkillNotFound MsgKey = "err.assistant_version.platform_skill_not_found"
	// MsgAssistantVersionSkillSourceUnknown 未知的 skill 来源。
	MsgAssistantVersionSkillSourceUnknown MsgKey = "err.assistant_version.skill_source_unknown"
	// MsgAssistantVersionUpstreamUnavailable 上游技能市场暂时不可用，请稍后重试。
	MsgAssistantVersionUpstreamUnavailable MsgKey = "err.assistant_version.upstream_unavailable"
	// MsgAssistantVersionInternal 操作助手版本失败（domain 兜底）。
	MsgAssistantVersionInternal MsgKey = "err.assistant_version.internal"
	// MsgAssistantVersionInvalidRequest 请求体格式错误；Create/Update/AddSkill 三处复用。
	MsgAssistantVersionInvalidRequest MsgKey = "err.assistant_version.invalid_request"
)

// init 把助手版本 domain 错误译文并入中心 catalog。
func init() {
	Register(map[MsgKey]map[string]string{
		MsgAssistantVersionForbidden:             {"zh": "无权操作助手版本", "en": "You are not allowed to operate on assistant versions"},
		MsgAssistantVersionNotFound:              {"zh": "助手版本不存在", "en": "The assistant version does not exist"},
		MsgAssistantVersionNameTaken:             {"zh": "助手版本名称已存在", "en": "The assistant version name already exists"},
		MsgAssistantVersionInUse:                 {"zh": "助手版本正被引用，不可删除", "en": "The assistant version is referenced and cannot be deleted"},
		MsgAssistantVersionSkillNameTaken:        {"zh": "版本内 skill 名称已存在", "en": "The skill name already exists in this version"},
		MsgAssistantVersionPlatformSkillNotFound: {"zh": "平台技能不存在", "en": "The platform skill does not exist"},
		MsgAssistantVersionSkillSourceUnknown:    {"zh": "未知的 skill 来源", "en": "Unknown skill source"},
		MsgAssistantVersionUpstreamUnavailable:   {"zh": "上游技能市场暂时不可用，请稍后重试", "en": "The upstream skill marketplace is temporarily unavailable, please try again later"},
		MsgAssistantVersionInternal:              {"zh": "操作助手版本失败", "en": "Assistant version operation failed"},
		MsgAssistantVersionInvalidRequest:        {"zh": "请求体格式错误", "en": "Malformed request body"},
	})
}
