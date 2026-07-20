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
	// AppStatusRestarting 是渠道解绑等触发 pod 重启期间的过渡态：
	// 解绑会 RolloutRestart 重建 pod（Recreate 策略，~20s 停机），此窗口内 oc-ops 不可用。
	// 由 reconciler 在 pod 重新 Ready 后收敛回 running；pod 重启后坏死则收敛到 error。
	AppStatusRestarting = "restarting"

	// RuntimePhase* 描述实例运行时就绪维度，与业务态 AppStatus* 正交：
	// AppStatus 管业务生命周期(draft→...→running/stopped/error)，RuntimePhase 管 pod
	// 此刻能否服务。渠道发起闸门 = AppCanInitiateChannelAuth(status, runtime_phase)，两维
	// 皆满足才放行。坏态归业务态 error(需人工/重试)，瞬态未就绪归 runtime_phase(只需稍候)。
	RuntimePhaseReady      = "ready"      // pod 所有关键容器(hermes+oc-ops)Ready，可服务(稳态)
	RuntimePhaseStarting   = "starting"   // 首次拉起中，pod 未就绪，k8s 预期自愈(init worker 写)
	RuntimePhaseRestarting = "restarting" // 重启窗口(解绑/升级/k8s 自发)，oc-ops 暂不可用
	RuntimePhaseUnknown    = "unknown"    // 未探明(查询失败 / reconciler 未跑 / 新建未初始化)

	// APIKeyStatus* 描述 new-api token 生命周期，独立于 app.status。
	APIKeyStatusPending  = "pending"
	APIKeyStatusActive   = "active"
	APIKeyStatusDisabled = "disabled"
	APIKeyStatusError    = "error"

	// ChannelTypeWeChat 是微信渠道类型。
	ChannelTypeWeChat = "wechat"
	// ChannelTypeFeishu 是飞书 / Lark 渠道（扫码自动创建 + 手填兜底，WebSocket 长连接）。
	ChannelTypeFeishu = "feishu"
	// ChannelTypeWorkWeChat 是企业微信渠道（智能机器人 AI Bot 长连接，手填 bot_id+secret）。
	ChannelTypeWorkWeChat = "work_wechat"
	// ChannelTypeDingTalk 是钉钉渠道（手填 Client ID/Client Secret，dingtalk-stream 长连接）。
	ChannelTypeDingTalk = "dingtalk"

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

	// ProvisioningStatus* 描述企业 web-publish 能力开通的一次性 provisioning 进度
	// （写通配解析 → 签证书 → 建 Ingress）。落 org_web_publish_config.provisioning_status。
	ProvisioningDisabled   = "disabled"     // 未开通（初始态 / 已停用）
	ProvisioningInProgress = "provisioning" // 开通中，provisioning job 处理中
	ProvisioningReady      = "ready"        // 已就绪，可发布站点
	ProvisioningFailed     = "failed"       // provisioning 失败，可重试

	// CertStatus* 描述通配证书托管状态，落 org_web_publish_config.cert_status，供页面展示。
	CertStatusNone     = "none"     // 尚未签发
	CertStatusIssuing  = "issuing"  // 首次签发中
	CertStatusIssued   = "issued"   // 已签发可用
	CertStatusRenewing = "renewing" // 续签中
	CertStatusFailed   = "failed"   // 签发/续签失败

	// SiteStatus* 描述已发布站点生命周期，落 published_sites.status。
	SiteStatusActive   = "active"   // 在线可访问
	SiteStatusDisabled = "disabled" // 手动下线（site-server 立即 404）
	SiteStatusExpired  = "expired"  // TTL 到期被 reaper 回收
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
	// JobTypeWebPublishProvision 一次性开通企业 web-publish：通配解析 + 通配证书 + 通配 Ingress。
	JobTypeWebPublishProvision = "web_publish_provision"
	// JobTypeAICCModelRollout 将企业模型 revision 下发到仍未同步的 AICC 智能体。
	JobTypeAICCModelRollout = "aicc_model_rollout"
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
		AppStatusRestarting,
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

	validRuntimePhases = set(
		RuntimePhaseReady,
		RuntimePhaseStarting,
		RuntimePhaseRestarting,
		RuntimePhaseUnknown,
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
		JobTypeWebPublishProvision,
		JobTypeAICCModelRollout,
	)

	validProvisioningStatuses = set(ProvisioningDisabled, ProvisioningInProgress, ProvisioningReady, ProvisioningFailed)
	validCertStatuses         = set(CertStatusNone, CertStatusIssuing, CertStatusIssued, CertStatusRenewing, CertStatusFailed)
	validSiteStatuses         = set(SiteStatusActive, SiteStatusDisabled, SiteStatusExpired)
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

// IsRuntimePhase 校验运行时就绪维度取值是否合法。
func IsRuntimePhase(value string) bool {
	_, ok := validRuntimePhases[value]
	return ok
}

// IsJobType 校验异步任务类型是否已在调度系统登记。
func IsJobType(value string) bool {
	_, ok := validJobTypes[value]
	return ok
}

// IsProvisioningStatus 校验 web-publish 能力开通状态取值是否合法。
func IsProvisioningStatus(value string) bool {
	_, ok := validProvisioningStatuses[value]
	return ok
}

// IsCertStatus 校验通配证书状态取值是否合法。
func IsCertStatus(value string) bool {
	_, ok := validCertStatuses[value]
	return ok
}

// IsSiteStatus 校验已发布站点状态取值是否合法。
func IsSiteStatus(value string) bool {
	_, ok := validSiteStatuses[value]
	return ok
}
