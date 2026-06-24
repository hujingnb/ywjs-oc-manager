package apierror

// 本文件集中登记「用量」（usage）与「充值」（recharge）两个 domain handler 层错误文案的
// MsgKey 与中/英译文。
// 范围覆盖 internal/api/handlers/usage.go 与 internal/api/handlers/recharge.go 中内联的
// 静态中文 apierror.New 调用。
// zh 译文逐字取自 handler 原中文（标点/空格不改），en 为忠实英译。
// 通用语义不在本文件重复定义：资源不存在直接复用 messages_common.go 的 MsgNotFound。
// recharge 兜底分支文案为运行时动态明细（redactlog.SafeErrorMessage），保留 apierror.New，
// 不纳入本 catalog。

// 用量 domain 静态错误 MsgKey 常量（前缀 err.usage.*）。
const (
	// MsgUsageMissingOrgID 缺少 org_id 查询参数。
	MsgUsageMissingOrgID MsgKey = "err.usage.missing_org_id"
	// MsgUsageForbidden 无权访问该用量。
	MsgUsageForbidden MsgKey = "err.usage.forbidden"
	// MsgUsageUnavailable 用量服务暂不可用。
	MsgUsageUnavailable MsgKey = "err.usage.unavailable"
	// MsgUsageInternal 用量服务异常（与通用内部错误措辞不同，单独成 key）。
	MsgUsageInternal MsgKey = "err.usage.internal"
)

// 充值 domain 静态错误 MsgKey 常量（前缀 err.recharge.*）。
const (
	// MsgRechargeForbidden 无权执行该操作。
	MsgRechargeForbidden MsgKey = "err.recharge.forbidden"
	// MsgRechargeOrgNotFound 企业不存在。
	MsgRechargeOrgNotFound MsgKey = "err.recharge.org_not_found"
	// MsgRechargeInvalidAmount 充值金额必须为正。
	MsgRechargeInvalidAmount MsgKey = "err.recharge.invalid_amount"
	// MsgRechargeOrgMissingNewAPIUser 企业未关联 new-api 账户。
	MsgRechargeOrgMissingNewAPIUser MsgKey = "err.recharge.org_missing_newapi_user"
)

// init 把用量 / 充值 domain 错误译文并入中心 catalog。
func init() {
	Register(map[MsgKey]map[string]string{
		MsgUsageMissingOrgID: {"zh": "缺少 org_id", "en": "Missing org_id"},
		MsgUsageForbidden:    {"zh": "无权访问该用量", "en": "You are not allowed to access this usage data"},
		MsgUsageUnavailable:  {"zh": "用量服务暂不可用", "en": "The usage service is temporarily unavailable"},
		MsgUsageInternal:     {"zh": "用量服务异常", "en": "The usage service encountered an error"},

		MsgRechargeForbidden:            {"zh": "无权执行该操作", "en": "You are not allowed to perform this operation"},
		MsgRechargeOrgNotFound:          {"zh": "企业不存在", "en": "The organization does not exist"},
		MsgRechargeInvalidAmount:        {"zh": "充值金额必须为正", "en": "The recharge amount must be positive"},
		MsgRechargeOrgMissingNewAPIUser: {"zh": "企业未关联 new-api 账户", "en": "The organization is not linked to a new-api account"},
	})
}
