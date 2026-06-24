package apierror

// 本文件集中登记「定制技能交付」（custom-skill）domain handler 层错误文案的 MsgKey 与
// 中/英译文。范围覆盖 internal/api/handlers/custom_skills.go 中内联的静态中文
// apierror.New 调用：multipart 上传校验（缺 file 字段、读取文件/内容失败、targets 非法 JSON）、
// 请求体绑定失败，以及 writeCustomSkillError 对各哨兵错误的映射（无权交付、工单不存在、
// 迭代技能名不一致、交付入参非法）。
// zh 译文逐字取自 handler 原中文（标点/空格不改），en 为忠实英译。
// 「工单不存在」与 skill_ticket domain 文案相同，但 key 全局唯一约束要求各 domain 独立命名，
// 故本文件单独登记 MsgCustomSkillTicketNotFound，不跨 domain 复用。
// 通用语义（服务器内部错误 → MsgInternal）复用 messages_common.go 已有 key，不在本文件重复。

// 定制技能交付 domain 静态错误 MsgKey 常量。
const (
	// MsgCustomSkillMissingFileField 缺少 file 字段。
	MsgCustomSkillMissingFileField MsgKey = "err.custom_skill.missing_file_field"
	// MsgCustomSkillOpenFileFailed 读取上传文件失败。
	MsgCustomSkillOpenFileFailed MsgKey = "err.custom_skill.open_file_failed"
	// MsgCustomSkillReadFileFailed 读取上传内容失败。
	MsgCustomSkillReadFileFailed MsgKey = "err.custom_skill.read_file_failed"
	// MsgCustomSkillTargetsInvalidJSON targets 不是合法 JSON。
	MsgCustomSkillTargetsInvalidJSON MsgKey = "err.custom_skill.targets_invalid_json"
	// MsgCustomSkillInvalidRequest 请求体格式错误（UpdateTargets 绑定失败）。
	MsgCustomSkillInvalidRequest MsgKey = "err.custom_skill.invalid_request"
	// MsgCustomSkillDenied 无权交付定制技能。
	MsgCustomSkillDenied MsgKey = "err.custom_skill.denied"
	// MsgCustomSkillTicketNotFound 工单不存在（交付场景）。
	MsgCustomSkillTicketNotFound MsgKey = "err.custom_skill.ticket_not_found"
	// MsgCustomSkillNameMismatch 迭代交付必须沿用同一技能名。
	MsgCustomSkillNameMismatch MsgKey = "err.custom_skill.name_mismatch"
	// MsgCustomSkillInvalidInput 交付入参非法。
	MsgCustomSkillInvalidInput MsgKey = "err.custom_skill.invalid_input"
)

// init 把定制技能交付 domain 错误译文并入中心 catalog。
func init() {
	Register(map[MsgKey]map[string]string{
		MsgCustomSkillMissingFileField:   {"zh": "缺少 file 字段", "en": "Missing file field"},
		MsgCustomSkillOpenFileFailed:     {"zh": "读取上传文件失败", "en": "Failed to read the uploaded file"},
		MsgCustomSkillReadFileFailed:     {"zh": "读取上传内容失败", "en": "Failed to read the uploaded content"},
		MsgCustomSkillTargetsInvalidJSON: {"zh": "targets 不是合法 JSON", "en": "targets is not valid JSON"},
		MsgCustomSkillInvalidRequest:     {"zh": "请求体格式错误", "en": "Malformed request body"},
		MsgCustomSkillDenied:             {"zh": "无权交付定制技能", "en": "You are not allowed to deliver custom skills"},
		MsgCustomSkillTicketNotFound:     {"zh": "工单不存在", "en": "The ticket does not exist"},
		MsgCustomSkillNameMismatch:       {"zh": "迭代交付必须沿用同一技能名", "en": "Iterative delivery must keep the same skill name"},
		MsgCustomSkillInvalidInput:       {"zh": "交付入参非法", "en": "Invalid delivery input"},
	})
}
