package apierror

// 本文件集中登记「成员」（member）与「组织」（organization）两个 domain handler 层错误文案的
// MsgKey 与中/英译文。
// 范围覆盖 internal/api/handlers/members.go 与 internal/api/handlers/organizations.go 中内联的
// 静态中文 apierror.New 调用。
// zh 译文逐字取自 handler 原中文（标点/空格不改），en 为忠实英译。
// 相同中文复用同一 key：「无权执行该操作」「服务暂时不可用」在 members/organizations 两个
// writeXxxError 兜底分支文案一致，各复用一个共享 key；通用「资源不存在」复用 common 的 MsgNotFound。
// validationServiceMessage(...) 透出的冲突 / 校验明细为运行时动态文案，保留 apierror.New，不纳入本 catalog。

// 成员 / 组织 domain 静态错误 MsgKey 常量。
const (
	// MsgMemberForbidden 无权执行该操作；members/organizations 两处 ErrForbidden 分支共用。
	MsgMemberForbidden MsgKey = "err.member.forbidden"
	// MsgMemberServiceUnavailable 服务暂时不可用；members/organizations 两处兜底分支共用。
	MsgMemberServiceUnavailable MsgKey = "err.member.service_unavailable"
	// MsgMemberOnboardDisabled 成员联动应用流程暂未启用。
	MsgMemberOnboardDisabled MsgKey = "err.member.onboard_disabled"
	// MsgMemberCreateAppDisabled 成员实例创建流程暂未启用。
	MsgMemberCreateAppDisabled MsgKey = "err.member.create_app_disabled"
)

// init 把成员 / 组织 domain 错误译文并入中心 catalog。
func init() {
	Register(map[MsgKey]map[string]string{
		MsgMemberForbidden:          {"zh": "无权执行该操作", "en": "You are not allowed to perform this operation"},
		MsgMemberServiceUnavailable: {"zh": "服务暂时不可用", "en": "The service is temporarily unavailable"},
		MsgMemberOnboardDisabled:    {"zh": "成员联动应用流程暂未启用", "en": "The member onboarding workflow is not enabled yet"},
		MsgMemberCreateAppDisabled:  {"zh": "成员实例创建流程暂未启用", "en": "The member instance creation workflow is not enabled yet"},
	})
}
