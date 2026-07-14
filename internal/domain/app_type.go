package domain

// AppType 描述 apps.app_type 的业务枚举；数据库 CHECK 约束负责持久化兜底。
type AppType string

const (
	// AppTypeStandard 表示面向组织成员的普通应用，也是新建普通应用的默认类型。
	AppTypeStandard AppType = "standard"
	// AppTypeAICC 表示面向外部访客的 AICC 应用，需要走客服专属权限和运行时配置。
	AppTypeAICC AppType = "aicc"
)

// IsAICCAppType 判断应用类型是否需要采用 AICC 专属业务语义。
// 仅显式 aicc 被视为客服应用，未知类型默认不进入客服隔离分支。
func IsAICCAppType(appType AppType) bool {
	return appType == AppTypeAICC
}
