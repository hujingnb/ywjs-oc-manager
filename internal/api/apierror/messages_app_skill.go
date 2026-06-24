package apierror

// 本文件集中登记「实例级技能」（app-skill）domain handler 层错误文案的 MsgKey 与
// 中/英译文。范围覆盖 internal/api/handlers/app_skills.go 中内联的静态中文
// apierror.New 调用：请求体绑定失败，以及 writeAppSkillError 对各哨兵错误的映射
// （无权管理、skill 不存在、同名冲突、版本保护、未知来源、归档校验失败、运行时不支持、
// 上游市场不可用等）。
// zh 译文逐字取自 handler 原中文（标点/空格不改），en 为忠实英译；相同中文复用同一 key
// （「请求体格式错误」在 Install/Update 两处出现，复用同一 key）。
// 通用语义（服务器内部错误 → MsgInternal）复用 messages_common.go 已有 key，不在本文件重复。

// 实例级技能 domain 静态错误 MsgKey 常量。
const (
	// MsgAppSkillInvalidRequest 请求体格式错误；Install/Update 两处复用。
	MsgAppSkillInvalidRequest MsgKey = "err.app_skill.invalid_request"
	// MsgAppSkillDenied 无权管理该实例的 skill。
	MsgAppSkillDenied MsgKey = "err.app_skill.denied"
	// MsgAppSkillNotFound 该实例 skill 不存在。
	MsgAppSkillNotFound MsgKey = "err.app_skill.not_found"
	// MsgAppSkillNameConflict 已有同名 skill，不允许重复安装。
	MsgAppSkillNameConflict MsgKey = "err.app_skill.name_conflict"
	// MsgAppSkillProtected 当前助手版本必需的 skill 不可删除。
	MsgAppSkillProtected MsgKey = "err.app_skill.protected"
	// MsgAppSkillSourceUnknown 未知的 skill 来源。
	MsgAppSkillSourceUnknown MsgKey = "err.app_skill.source_unknown"
	// MsgAppSkillArchiveTooDangerous skill 归档解压校验失败，文件可能存在安全风险。
	MsgAppSkillArchiveTooDangerous MsgKey = "err.app_skill.archive_too_dangerous"
	// MsgAppSkillRuntimeUnsupported 当前实例运行的 hermes 版本过旧，不支持技能管理。
	MsgAppSkillRuntimeUnsupported MsgKey = "err.app_skill.runtime_unsupported"
	// MsgAppSkillUpstreamUnavailable 上游技能市场暂时不可用。
	MsgAppSkillUpstreamUnavailable MsgKey = "err.app_skill.upstream_unavailable"
)

// init 把实例级技能 domain 错误译文并入中心 catalog。
func init() {
	Register(map[MsgKey]map[string]string{
		MsgAppSkillInvalidRequest:      {"zh": "请求体格式错误", "en": "Malformed request body"},
		MsgAppSkillDenied:              {"zh": "无权管理该实例的 skill", "en": "You are not allowed to manage skills for this instance"},
		MsgAppSkillNotFound:            {"zh": "该实例 skill 不存在", "en": "The skill does not exist on this instance"},
		MsgAppSkillNameConflict:        {"zh": "已有同名 skill，不允许重复安装", "en": "A skill with the same name already exists; duplicate installation is not allowed"},
		MsgAppSkillProtected:           {"zh": "当前助手版本必需的 skill 不可删除", "en": "Skills required by the current assistant version cannot be removed"},
		MsgAppSkillSourceUnknown:       {"zh": "未知的 skill 来源", "en": "Unknown skill source"},
		MsgAppSkillArchiveTooDangerous: {"zh": "skill 归档解压校验失败，文件可能存在安全风险", "en": "The skill archive failed extraction validation and may pose a security risk"},
		MsgAppSkillRuntimeUnsupported:  {"zh": "当前实例运行的 hermes 版本过旧，不支持技能管理，请更新实例的运行时版本后重试", "en": "The hermes version running on this instance is too old to support skill management. Please update the instance runtime version and retry"},
		MsgAppSkillUpstreamUnavailable: {"zh": "上游技能市场暂时不可用，请稍后重试", "en": "The upstream skill marketplace is temporarily unavailable. Please retry later"},
	})
}
