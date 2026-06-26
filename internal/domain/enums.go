// Package domain 集中定义跨 handler、service、worker 共享的业务枚举和状态机约束。
package domain

// 业务枚举统一放在 domain 层，避免 handler、service、worker 各自散落硬编码字符串。
// 数据库仍通过 CHECK 约束兜底；这些常量用于进入数据库前的业务校验和状态机判断。

const (
	// 用户角色按平台、组织管理、组织成员三层授权；权限谓词集中在 internal/auth/authorizer.go。
	UserRolePlatformAdmin = "platform_admin"
	UserRoleOrgAdmin      = "org_admin"
	UserRoleOrgMember     = "org_member"

	// 通用状态用于 users / organizations 等基础资源；users.deleted_at 语义是下线时间戳。
	StatusActive   = "active"
	StatusDisabled = "disabled"
	StatusDeleted  = "deleted"

	// AppStatus* 描述应用生命周期，合法转移由 app_state_machine.go 维护。
	// init 子状态对应 worker 初始化阶段；前端按 status 直接展示当前阶段。
	AppStatusDraft = "draft"
	// AppStatusPullingRuntimeImage 由 phasePullRuntimeImage 驱动，
	// 让每个 agent 直接从公网 registry 拉取 hermes 镜像。
	AppStatusPullingRuntimeImage = "pulling_runtime_image"
	AppStatusPreparingRuntime    = "preparing_runtime"
	AppStatusCreatingContainer   = "creating_container"
	AppStatusStarting            = "starting"
	AppStatusBindingWaiting      = "binding_waiting"
	AppStatusBindingFailed       = "binding_failed"
	AppStatusRunning             = "running"
	AppStatusStopped             = "stopped"
	AppStatusError               = "error"
	AppStatusDeleted             = "deleted"

	// APIKeyStatus* 描述 new-api token 生命周期，独立于 app.status。
	APIKeyStatusPending  = "pending"
	APIKeyStatusActive   = "active"
	APIKeyStatusDisabled = "disabled"
	APIKeyStatusError    = "error"

	// ChannelTypeWeChat 是微信渠道类型。
	ChannelTypeWeChat = "wechat"
	// ChannelTypeFeishu 是飞书 / Lark 渠道（扫码自动创建 + 手填兜底，WebSocket 长连接）。
	ChannelTypeFeishu = "feishu"

	// ChannelStatus* 描述渠道绑定流程和用户主动解绑状态。
	ChannelStatusUnbound       = "unbound"
	ChannelStatusPendingAuth   = "pending_auth"
	ChannelStatusBound         = "bound"
	ChannelStatusFailed        = "failed"
	ChannelStatusExpired       = "expired"
	ChannelStatusUnboundByUser = "unbound_by_user"
	ChannelStatusDeleted       = "deleted"

	// JobStatus* 描述异步任务调度状态，合法转移由 job_state_machine.go 维护。
	JobStatusPending   = "pending"
	JobStatusRunning   = "running"
	JobStatusSucceeded = "succeeded"
	JobStatusFailed    = "failed"
	JobStatusCanceled  = "canceled"
)

const (
	// JobTypeAppInitialize 初始化应用目录、容器、new-api token 和运行时配置。
	JobTypeAppInitialize = "app_initialize"
	// JobTypeAppStartContainer 启动已初始化应用的 runtime 容器。
	JobTypeAppStartContainer = "app_start_container"
	// JobTypeAppStopContainer 停止应用 runtime 容器但保留应用数据。
	JobTypeAppStopContainer = "app_stop_container"
	// JobTypeAppRestartContainer 对应用 runtime 容器执行停止后启动的重启流程。
	JobTypeAppRestartContainer = "app_restart_container"
	// JobTypeAppDelete 清理应用容器、运行时数据和关联 new-api token 状态。
	JobTypeAppDelete = "app_delete"
	// JobTypeChannelStartLogin 启动渠道登录流程，例如微信扫码授权。
	JobTypeChannelStartLogin = "channel_start_login"
	// JobTypeChannelCheckBinding 轮询渠道授权结果并写回绑定状态。
	JobTypeChannelCheckBinding = "channel_check_binding"
	// JobTypeNewAPIDisableKey 在应用停用或删除时禁用对应 new-api token。
	JobTypeNewAPIDisableKey = "newapi_disable_key"
	// JobTypeNewAPIRestoreKey 在应用恢复时重新启用对应 new-api token。
	JobTypeNewAPIRestoreKey = "newapi_restore_key"
	// JobTypeWorkspaceArchiveCleanup 清理超过保留期的应用工作区归档。
	JobTypeWorkspaceArchiveCleanup = "workspace_archive_cleanup"
)

var (
	validUserRoles = set(UserRolePlatformAdmin, UserRoleOrgAdmin, UserRoleOrgMember)

	validAppStatuses = set(
		AppStatusDraft,
		AppStatusPullingRuntimeImage,
		AppStatusPreparingRuntime,
		AppStatusCreatingContainer,
		AppStatusStarting,
		AppStatusBindingWaiting,
		AppStatusBindingFailed,
		AppStatusRunning,
		AppStatusStopped,
		AppStatusError,
		AppStatusDeleted,
	)

	validChannelStatuses = set(
		ChannelStatusUnbound,
		ChannelStatusPendingAuth,
		ChannelStatusBound,
		ChannelStatusFailed,
		ChannelStatusExpired,
		ChannelStatusUnboundByUser,
		ChannelStatusDeleted,
	)

	validJobTypes = set(
		JobTypeAppInitialize,
		JobTypeAppStartContainer,
		JobTypeAppStopContainer,
		JobTypeAppRestartContainer,
		JobTypeAppDelete,
		JobTypeChannelStartLogin,
		JobTypeChannelCheckBinding,
		JobTypeNewAPIDisableKey,
		JobTypeNewAPIRestoreKey,
		JobTypeWorkspaceArchiveCleanup,
	)
)

func set(values ...string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}

// IsUserRole 校验用户角色是否属于当前系统支持的角色集合。
func IsUserRole(value string) bool {
	_, ok := validUserRoles[value]
	return ok
}

// IsAppStatus 校验应用状态是否属于应用状态机允许的状态集合。
func IsAppStatus(value string) bool {
	_, ok := validAppStatuses[value]
	return ok
}

// IsChannelStatus 校验渠道绑定状态是否属于通用渠道状态机。
func IsChannelStatus(value string) bool {
	_, ok := validChannelStatuses[value]
	return ok
}

// IsJobType 校验异步任务类型是否已在调度系统登记。
func IsJobType(value string) bool {
	_, ok := validJobTypes[value]
	return ok
}
