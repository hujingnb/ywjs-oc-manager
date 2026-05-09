// Package domain 维护应用状态机与枚举。app_state_machine.go 定义应用生命周期。
//
// # 状态机
//
//	draft  ──onboarding──▶  initializing  ──worker 完成──▶  binding_waiting
//	  │                          │                              │
//	  │                          ▼                              ▼ 渠道扫码
//	  │                       error ◀──────────────────────  binding_failed
//	  │                          ▲                              │
//	  └──────────────────────────┴──────────────────────────────┴─────▶ running
//	                                                                    │
//	                                                                    ▼ 停止
//	                                                                  stopped
//	                                                                    │
//	                                                                    ▼ 删除
//	                                                                  deleted
//
// 关键转移约束：
//   - draft → initializing：仅 onboarding job 可触发
//   - initializing → binding_waiting：worker 完成镜像拉取 + new-api 凭证配置
//   - binding_waiting → binding_failed：渠道扫码超时或 token 过期
//   - error 是吸入态：任何步骤失败都会落到 error，由用户手工 retry 才能离开
//   - deleted 是终态：deleted_at 字段非空即认为已删
//   - stopped → running：用户主动启动
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
	{From: AppStatusDraft, To: AppStatusInitializing}:           {},
	{From: AppStatusInitializing, To: AppStatusBindingWaiting}:  {},
	{From: AppStatusInitializing, To: AppStatusError}:           {},
	{From: AppStatusBindingWaiting, To: AppStatusRunning}:       {},
	{From: AppStatusBindingWaiting, To: AppStatusBindingFailed}: {},
	{From: AppStatusBindingFailed, To: AppStatusBindingWaiting}: {},
	{From: AppStatusBindingFailed, To: AppStatusError}:          {},
	{From: AppStatusRunning, To: AppStatusStopped}:              {},
	{From: AppStatusRunning, To: AppStatusError}:                {},
	{From: AppStatusStopped, To: AppStatusRunning}:              {},
	{From: AppStatusStopped, To: AppStatusError}:                {},
	{From: AppStatusError, To: AppStatusInitializing}:           {},
	{From: AppStatusError, To: AppStatusDeleted}:                {},
}

// IsAppTransitionAllowed 判断 from→to 是否合法。
// deleted 是终态；除 error→deleted 外，进入 deleted 必须由 SoftDeleteApp 调用单独完成，
// 这里不在状态机中暴露通用 to-deleted 路径，避免业务侧绕过软删除流程直接置位。
func IsAppTransitionAllowed(from, to string) bool {
	if from == to {
		return false
	}
	if to == AppStatusDeleted && from != AppStatusError {
		return false
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

// APIKeyTransition 描述 api_key_status 的状态机。
// api_key 与 app 状态相互独立：app 可以在 api_key error 的同时仍处于 binding_waiting；
// 也可以在 api_key active 时短暂处于 stopped。
type APIKeyTransition struct {
	From string
	To   string
}

var apiKeyTransitions = map[APIKeyTransition]struct{}{
	{From: APIKeyStatusPending, To: APIKeyStatusActive}:   {},
	{From: APIKeyStatusPending, To: APIKeyStatusError}:    {},
	{From: APIKeyStatusActive, To: APIKeyStatusDisabled}:  {},
	{From: APIKeyStatusActive, To: APIKeyStatusError}:     {},
	{From: APIKeyStatusDisabled, To: APIKeyStatusActive}:  {},
	{From: APIKeyStatusError, To: APIKeyStatusPending}:    {},
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
