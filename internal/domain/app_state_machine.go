// Package domain 维护应用状态机与枚举。app_state_machine.go 定义应用生命周期。
//
// # 状态机
//
// 初始化 / 运行段（实线为合法转移，所有非终态均可意外掉入 error）：
//
//	draft ─▶ pulling_runtime_image ─▶ preparing_runtime ─▶ creating_container ─▶ starting ─▶ binding_waiting ──扫码成功──▶ running ◀──启动──▶ stopped
//	                 │                        │                      │                  │              │                       │                │
//	                 │                        │                      │                  │              │ 扫码超时              │ 异常退出       │ 异常退出
//	                 │                        │                      │                  │              ▼                       │                │
//	                 │                        │                      │                  │       binding_failed ──重启绑定──▶ binding_waiting   │
//	                 │                        │                      │                  │              │ 放弃                                  │
//	                 ▼                        ▼                      ▼                  ▼              ▼                                       ▼
//	         ┌───────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────┐
//	         │                                                              error                                                                        │
//	         └───────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────┘
//	             │ 重试入口（RequestInitialize）             │ 软删除（SoftDeleteApp，IsAppTransitionAllowed 特殊分支兜底）
//	             ▼                                            ▼
//	         pulling_runtime_image                          deleted （终态）
//
// 关键转移约束：
//   - draft → pulling_runtime_image：onboarding job 拾取后第一阶段，由 worker 触发；
//     agent 通过 docker proxy 直接从公网 registry 拉取 hermes 镜像，跳过原 pulling_image/syncing_image；
//   - pulling_runtime_image → preparing_runtime → creating_container → starting：
//     worker 在 4 个 init 子状态之间串行推进，每阶段对应明确的副作用；
//   - starting → binding_waiting：容器健康检查通过后等渠道扫码；
//   - 任意 init 子状态 → error：失败收敛到 error，last_error_status 记录来源阶段以便排障；
//   - binding_waiting → binding_failed：渠道扫码超时或 token 过期；
//   - binding_waiting → pulling_runtime_image：仅 AICC 后台镜像升级任务使用；
//   - binding_failed → binding_waiting：用户手动重启绑定流程；
//   - binding_failed → error：多次失败后用户放弃或自动收敛；
//   - running → error：运行时容器异常退出，收敛到 error 等待人工或重试；
//   - running → pulling_runtime_image：仅 AICC 后台镜像升级任务使用，普通实例入口仍拒绝运行中重试；
//   - stopped → error：停止状态下底层异常（例如镜像被清理 / 节点失联）；
//   - error → pulling_runtime_image：RequestInitialize 重试入口，从 worker 第一阶段重新开始；
//   - error → deleted：由 IsAppTransitionAllowed 内置特殊分支兜底，不进 appTransitions map；
//     deleted 是终态且只能由 error 进入，stopped / running 等都必须先收敛到 error 才能软删；
//   - deleted 是终态，deleted_at 字段非空即认为已删；
//   - stopped → running：用户主动启动；
//   - running → restarting：渠道解绑触发 RolloutRestart 重建 pod 的过渡态；
//   - restarting → running：reconciler 在 pod 重新 Ready 后收敛回运行态；
//   - restarting → error：pod 重启后坏死，reconciler 收敛到 error。
//
// 维护提醒：状态机如有变化，本文档块必须同步更新；与代码不一致按代码为准。
package domain

import "fmt"

// AppTransition 显式列出 app 状态机允许的状态转移。
// 任何 service 写库前都必须用 EnsureAppTransition 校验，避免散落 SQL 直接改写状态。
type AppTransition struct {
	From string
	To   string
}

var appTransitions = map[AppTransition]struct{}{
	// init 子状态串行推进：worker 完成一个阶段后才能进入下一个。
	// pulling_runtime_image 替代原 pulling_image + syncing_image 两阶段，
	// agent 直接通过 docker proxy 从公网 registry 拉取 hermes 镜像。
	{From: AppStatusDraft, To: AppStatusPullingRuntimeImage}:            {},
	{From: AppStatusPullingRuntimeImage, To: AppStatusPreparingRuntime}: {},
	{From: AppStatusPreparingRuntime, To: AppStatusCreatingContainer}:   {},
	{From: AppStatusCreatingContainer, To: AppStatusStarting}:           {},
	{From: AppStatusStarting, To: AppStatusBindingWaiting}:              {},

	// binding / running 段：渠道绑定与容器运行状态切换。
	{From: AppStatusBindingWaiting, To: AppStatusRunning}:       {},
	{From: AppStatusBindingWaiting, To: AppStatusBindingFailed}: {},
	// AICC 无外部渠道绑定也可处于 binding_waiting；镜像升级任务需重新进入初始化阶段。
	{From: AppStatusBindingWaiting, To: AppStatusPullingRuntimeImage}: {},
	{From: AppStatusBindingFailed, To: AppStatusBindingWaiting}:       {},
	{From: AppStatusBindingFailed, To: AppStatusError}:                {},
	{From: AppStatusRunning, To: AppStatusStopped}:                    {},
	{From: AppStatusRunning, To: AppStatusError}:                      {},
	// AICC 运行时镜像升级由后台任务自动重新进入初始化阶段；普通实例不会从 handler 走此路径。
	{From: AppStatusRunning, To: AppStatusPullingRuntimeImage}: {},
	{From: AppStatusStopped, To: AppStatusRunning}:             {},
	{From: AppStatusStopped, To: AppStatusError}:               {},

	// restarting 段：渠道解绑触发 RolloutRestart 重建 pod 的过渡态。
	// running → restarting：解绑置位，标记 pod 正在重启、oc-ops 暂不可用；
	// restarting → running：reconciler 在 pod 重新 Ready 后收敛回运行态；
	// restarting → error：pod 重启后坏死（NotFound/Failed/CrashLoop），reconciler 收敛到 error。
	{From: AppStatusRunning, To: AppStatusRestarting}: {},
	{From: AppStatusRestarting, To: AppStatusRunning}: {},
	{From: AppStatusRestarting, To: AppStatusError}:   {},

	// init 子状态失败都收敛到 error；last_error_status 记录失败阶段以便排障与重试。
	{From: AppStatusPullingRuntimeImage, To: AppStatusError}: {},
	{From: AppStatusPreparingRuntime, To: AppStatusError}:    {},
	{From: AppStatusCreatingContainer, To: AppStatusError}:   {},
	{From: AppStatusStarting, To: AppStatusError}:            {},

	// error 重试入口：RequestInitialize 把状态拨回到 worker 第一阶段重新跑。
	{From: AppStatusError, To: AppStatusPullingRuntimeImage}: {},
	// error → deleted 由 IsAppTransitionAllowed 内的特殊分支兜底，无需在 map 中重复登记。
}

// IsAppTransitionAllowed 判断 from→to 是否合法。
// deleted 是终态；只允许 error → deleted 路径（由 SoftDeleteApp 调用），
// 其他状态进入 deleted 都必须先收敛到 error，避免业务侧绕过软删除流程直接置位。
// error → deleted 在此处用特殊分支显式放行，不在 appTransitions map 中重复登记，
// 与「deleted 只能由 error 进入」这一约束保持单一来源。
func IsAppTransitionAllowed(from, to string) bool {
	if from == to {
		return false
	}
	if to == AppStatusDeleted {
		// deleted 只能从 error 进入；其他来源一律拒绝。
		return from == AppStatusError
	}
	_, ok := appTransitions[AppTransition{From: from, To: to}]
	return ok
}

// EnsureAppTransition 失败时返回带上下文的错误。
func EnsureAppTransition(from, to string) error {
	if !IsAppTransitionAllowed(from, to) {
		return fmt.Errorf("非法 app 状态转移: %s -> %s", from, to)
	}
	return nil
}

// AppIsTerminal 判断 app 是否进入终态。
// 非 deleted 的状态都仍可通过状态机回到运行态。
func AppIsTerminal(status string) bool { return status == AppStatusDeleted }

// AppCanInitiateChannelAuth 判断 app 当前是否允许发起渠道登录授权（BeginAuth / BeginFeishuAuth）。
//
// 双维度守卫：业务态在 allowlist 且运行时态为 ready 才放行——否则发起会打到不可达 / 未就绪的
// oc-ops 拿到 502，前端透出 cryptic「ocops: hermes cli failed」像 bug。
//   - status 维度：running（就绪，重绑/新增渠道）、binding_waiting（首次 onboarding 等扫码，
//     微信首绑发起即在此态）、binding_failed（上轮超时，pod 仍在，允许重试——但重试的前提是
//     runtime_phase 已回到 ready，由 reconciler 持续维护；see Fix I-2，binding_failed 已纳入
//     ListRunningApps 供 reconciler 刷新其 runtime_phase）。
//   - runtime_phase 维度：必须 == ready（pod 所有关键容器含 oc-ops 都 Ready）。restarting
//     （解绑/升级/k8s 自发重启窗口）、starting（首次拉起中）、unknown（未探明）一律拦截。
//
// 业务态 allowlist 比「status==running」宽：严格 running-only 会误杀 binding_waiting 首绑与
// binding_failed 重试（二者 pod 均在服务），故按「pod 是否在服务」建模。
func AppCanInitiateChannelAuth(status, runtimePhase string) bool {
	if runtimePhase != RuntimePhaseReady {
		return false
	}
	switch status {
	case AppStatusRunning, AppStatusBindingWaiting, AppStatusBindingFailed:
		return true
	default:
		return false
	}
}

// APIKeyTransition 描述 api_key_status 的状态机。
// api_key 与 app 状态相互独立：app 可以在 api_key error 的同时仍处于 binding_waiting；
// 也可以在 api_key active 时短暂处于 stopped。
type APIKeyTransition struct {
	From string
	To   string
}

var apiKeyTransitions = map[APIKeyTransition]struct{}{
	{From: APIKeyStatusPending, To: APIKeyStatusActive}:  {},
	{From: APIKeyStatusPending, To: APIKeyStatusError}:   {},
	{From: APIKeyStatusActive, To: APIKeyStatusDisabled}: {},
	{From: APIKeyStatusActive, To: APIKeyStatusError}:    {},
	{From: APIKeyStatusDisabled, To: APIKeyStatusActive}: {},
	{From: APIKeyStatusError, To: APIKeyStatusPending}:   {},
}

// IsAPIKeyTransitionAllowed 判断 api_key 状态切换是否合法。
func IsAPIKeyTransitionAllowed(from, to string) bool {
	if from == to {
		return false
	}
	_, ok := apiKeyTransitions[APIKeyTransition{From: from, To: to}]
	return ok
}

// EnsureAPIKeyTransition 失败时返回带上下文的错误。
func EnsureAPIKeyTransition(from, to string) error {
	if !IsAPIKeyTransitionAllowed(from, to) {
		return fmt.Errorf("非法 api_key 状态转移: %s -> %s", from, to)
	}
	return nil
}
