package apierror

// 本文件集中登记「平台库技能」（platform-skill）与「技能市场」（skill-market）两个相邻
// domain handler 层错误文案的 MsgKey 与中/英译文。范围覆盖：
//   - internal/api/handlers/platform_skills.go：multipart 上传校验（缺 file 字段、读取
//     文件/内容失败）+ writePlatformSkillError（无权管理、技能不存在、同名同版本冲突、入参非法）。
//   - internal/api/handlers/skill_market.go：writeSkillMarketError（未知来源、入参非法、
//     无权下载、上游不可用）。
// 两 domain 文案语义不同，分别用 err.platform_skill.* / err.skill_market.* 前缀，key 全局唯一。
// zh 译文逐字取自 handler 原中文（标点/空格不改），en 为忠实英译。
// 「未知的 skill 来源」「上游技能市场暂时不可用…」虽与 app_skill domain 文案相同，但 key 全局
// 唯一约束要求各 domain 独立命名，本文件单独登记 skill_market 版本，不跨 domain 复用。
// 通用语义（服务器内部错误 → MsgInternal）复用 messages_common.go 已有 key，不在本文件重复。

// 平台库技能 domain 静态错误 MsgKey 常量。
const (
	// MsgPlatformSkillMissingFileField 缺少 file 字段。
	MsgPlatformSkillMissingFileField MsgKey = "err.platform_skill.missing_file_field"
	// MsgPlatformSkillOpenFileFailed 读取上传文件失败。
	MsgPlatformSkillOpenFileFailed MsgKey = "err.platform_skill.open_file_failed"
	// MsgPlatformSkillReadFileFailed 读取上传内容失败。
	MsgPlatformSkillReadFileFailed MsgKey = "err.platform_skill.read_file_failed"
	// MsgPlatformSkillDenied 无权管理平台技能。
	MsgPlatformSkillDenied MsgKey = "err.platform_skill.denied"
	// MsgPlatformSkillNotFound 平台技能不存在。
	MsgPlatformSkillNotFound MsgKey = "err.platform_skill.not_found"
	// MsgPlatformSkillNameVersionTaken 同名同版本的平台技能已存在。
	MsgPlatformSkillNameVersionTaken MsgKey = "err.platform_skill.name_version_taken"
	// MsgPlatformSkillInvalidInput 平台技能入参非法。
	MsgPlatformSkillInvalidInput MsgKey = "err.platform_skill.invalid_input"
)

// 技能市场 domain 静态错误 MsgKey 常量。
const (
	// MsgSkillMarketSourceUnknown 未知的 skill 来源。
	MsgSkillMarketSourceUnknown MsgKey = "err.skill_market.source_unknown"
	// MsgSkillMarketInvalidInput skill 市场操作入参非法。
	MsgSkillMarketInvalidInput MsgKey = "err.skill_market.invalid_input"
	// MsgSkillMarketDownloadDenied 无权下载该 skill 归档。
	MsgSkillMarketDownloadDenied MsgKey = "err.skill_market.download_denied"
	// MsgSkillMarketUpstreamUnavailable 上游技能市场暂时不可用。
	MsgSkillMarketUpstreamUnavailable MsgKey = "err.skill_market.upstream_unavailable"
)

// init 把平台库技能与技能市场两 domain 错误译文并入中心 catalog。
func init() {
	Register(map[MsgKey]map[string]string{
		MsgPlatformSkillMissingFileField: {"zh": "缺少 file 字段", "en": "Missing file field"},
		MsgPlatformSkillOpenFileFailed:   {"zh": "读取上传文件失败", "en": "Failed to read the uploaded file"},
		MsgPlatformSkillReadFileFailed:   {"zh": "读取上传内容失败", "en": "Failed to read the uploaded content"},
		MsgPlatformSkillDenied:           {"zh": "无权管理平台技能", "en": "You are not allowed to manage platform skills"},
		MsgPlatformSkillNotFound:         {"zh": "平台技能不存在", "en": "The platform skill does not exist"},
		MsgPlatformSkillNameVersionTaken: {"zh": "同名同版本的平台技能已存在", "en": "A platform skill with the same name and version already exists"},
		MsgPlatformSkillInvalidInput:     {"zh": "平台技能入参非法", "en": "Invalid platform skill input"},

		MsgSkillMarketSourceUnknown:       {"zh": "未知的 skill 来源", "en": "Unknown skill source"},
		MsgSkillMarketInvalidInput:        {"zh": "skill 市场操作入参非法", "en": "Invalid skill marketplace operation input"},
		MsgSkillMarketDownloadDenied:      {"zh": "无权下载该 skill 归档", "en": "You are not allowed to download this skill archive"},
		MsgSkillMarketUpstreamUnavailable: {"zh": "上游技能市场暂时不可用，请稍后重试", "en": "The upstream skill marketplace is temporarily unavailable. Please retry later"},
	})
}
