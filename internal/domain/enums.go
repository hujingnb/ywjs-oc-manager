package domain

// 业务枚举统一放在 domain 层，避免 handler、service、worker 各自散落硬编码字符串。
// 数据库仍通过 CHECK 约束兜底；这些常量用于进入数据库前的业务校验和状态机判断。

const (
	UserRolePlatformAdmin = "platform_admin"
	UserRoleOrgAdmin      = "org_admin"
	UserRoleOrgMember     = "org_member"

	StatusActive   = "active"
	StatusDisabled = "disabled"
	StatusDeleted  = "deleted"

	AppStatusDraft          = "draft"
	AppStatusInitializing   = "initializing"
	AppStatusBindingWaiting = "binding_waiting"
	AppStatusBindingFailed  = "binding_failed"
	AppStatusRunning        = "running"
	AppStatusStopped        = "stopped"
	AppStatusError          = "error"
	AppStatusDeleted        = "deleted"

	PersonaModeOrgInherited = "org_inherited"
	PersonaModeAppOverride  = "app_override"

	APIKeyStatusPending  = "pending"
	APIKeyStatusActive   = "active"
	APIKeyStatusDisabled = "disabled"
	APIKeyStatusError    = "error"

	RuntimeNodeStatusPending     = "pending"
	RuntimeNodeStatusActive      = "active"
	RuntimeNodeStatusUnreachable = "unreachable"
	RuntimeNodeStatusDisabled    = "disabled"
	RuntimeNodeStatusDegraded    = "degraded"

	ChannelTypeWeChat = "wechat"

	ChannelStatusUnbound       = "unbound"
	ChannelStatusPendingAuth   = "pending_auth"
	ChannelStatusBound         = "bound"
	ChannelStatusFailed        = "failed"
	ChannelStatusExpired       = "expired"
	ChannelStatusUnboundByUser = "unbound_by_user"
	ChannelStatusDeleted       = "deleted"

	JobStatusPending   = "pending"
	JobStatusRunning   = "running"
	JobStatusSucceeded = "succeeded"
	JobStatusFailed    = "failed"
	JobStatusCanceled  = "canceled"
)

const (
	JobTypeAppInitialize              = "app_initialize"
	JobTypeAppStartContainer          = "app_start_container"
	JobTypeAppStopContainer           = "app_stop_container"
	JobTypeAppRestartContainer        = "app_restart_container"
	JobTypeAppDelete                  = "app_delete"
	JobTypeChannelStartLogin          = "channel_start_login"
	JobTypeChannelCheckBinding        = "channel_check_binding"
	JobTypeKnowledgeSyncNode          = "knowledge_sync_node"
	JobTypeRuntimeNodeHealthReconcile = "runtime_node_health_reconcile"
	JobTypeRuntimeRefreshStatus       = "runtime_refresh_status"
	JobTypeAppHealthCheck             = "app_health_check"
	JobTypeNewAPIDisableKey           = "newapi_disable_key"
	JobTypeNewAPIRestoreKey           = "newapi_restore_key"
	JobTypeWorkspaceArchiveCleanup    = "workspace_archive_cleanup"
)

var (
	validUserRoles = set(UserRolePlatformAdmin, UserRoleOrgAdmin, UserRoleOrgMember)

	validAppStatuses = set(
		AppStatusDraft,
		AppStatusInitializing,
		AppStatusBindingWaiting,
		AppStatusBindingFailed,
		AppStatusRunning,
		AppStatusStopped,
		AppStatusError,
		AppStatusDeleted,
	)

	validRuntimeNodeStatuses = set(
		RuntimeNodeStatusPending,
		RuntimeNodeStatusActive,
		RuntimeNodeStatusUnreachable,
		RuntimeNodeStatusDisabled,
		RuntimeNodeStatusDegraded,
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
		JobTypeKnowledgeSyncNode,
		JobTypeRuntimeNodeHealthReconcile,
		JobTypeRuntimeRefreshStatus,
		JobTypeAppHealthCheck,
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

// IsRuntimeNodeStatus 校验运行节点状态是否属于节点注册和心跳流程允许的状态集合。
func IsRuntimeNodeStatus(value string) bool {
	_, ok := validRuntimeNodeStatuses[value]
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
