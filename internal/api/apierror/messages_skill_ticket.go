package apierror

// 本文件集中登记「定制技能工单」（skill-ticket）domain handler 层错误文案的 MsgKey 与
// 中/英译文。范围覆盖 internal/api/handlers/skill_tickets.go 中内联的静态中文
// apierror.New 调用：请求体绑定失败、上传文件读取失败、工单哨兵错误映射（无权/不存在/
// 入参非法）等。
// zh 译文逐字取自 handler 原中文（标点/空格不改），en 为忠实英译；相同中文复用同一 key
// （「请求体格式错误」在 Submit/SendMessage/SetQuote/Reject 四处出现，复用同一 key）。
// 通用语义（服务器内部错误 → MsgInternal）复用 messages_common.go 已有 key，不在本文件重复。

// 定制技能工单 domain 静态错误 MsgKey 常量。
const (
	// MsgSkillTicketInvalidRequest 请求体格式错误；Submit/SendMessage/SetQuote/Reject 四处复用。
	MsgSkillTicketInvalidRequest MsgKey = "err.skill_ticket.invalid_request"
	// MsgSkillTicketMissingFileField 缺少 file 字段。
	MsgSkillTicketMissingFileField MsgKey = "err.skill_ticket.missing_file_field"
	// MsgSkillTicketOpenFileFailed 读取上传文件失败。
	MsgSkillTicketOpenFileFailed MsgKey = "err.skill_ticket.open_file_failed"
	// MsgSkillTicketReadFileFailed 读取上传内容失败。
	MsgSkillTicketReadFileFailed MsgKey = "err.skill_ticket.read_file_failed"
	// MsgSkillTicketForbidden 无权操作该工单。
	MsgSkillTicketForbidden MsgKey = "err.skill_ticket.forbidden"
	// MsgSkillTicketNotFound 工单不存在。
	MsgSkillTicketNotFound MsgKey = "err.skill_ticket.not_found"
	// MsgSkillTicketInvalidInput 工单入参非法。
	MsgSkillTicketInvalidInput MsgKey = "err.skill_ticket.invalid_input"
)

// init 把定制技能工单 domain 错误译文并入中心 catalog。
func init() {
	Register(map[MsgKey]map[string]string{
		MsgSkillTicketInvalidRequest:   {"zh": "请求体格式错误", "en": "Malformed request body"},
		MsgSkillTicketMissingFileField: {"zh": "缺少 file 字段", "en": "Missing file field"},
		MsgSkillTicketOpenFileFailed:   {"zh": "读取上传文件失败", "en": "Failed to read the uploaded file"},
		MsgSkillTicketReadFileFailed:   {"zh": "读取上传内容失败", "en": "Failed to read the uploaded content"},
		MsgSkillTicketForbidden:        {"zh": "无权操作该工单", "en": "You are not allowed to operate on this ticket"},
		MsgSkillTicketNotFound:         {"zh": "工单不存在", "en": "The ticket does not exist"},
		MsgSkillTicketInvalidInput:     {"zh": "工单入参非法", "en": "Invalid ticket input"},
	})
}
