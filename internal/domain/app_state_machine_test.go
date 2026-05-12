// Package domain 的应用状态机测试覆盖合法路径、非法倒退和终态约束。
package domain

import (
	"github.com/stretchr/testify/require"
	"testing"
)

// TestIsAppTransitionAllowedHappyPath 验证Is应用状态流转允许性成功路径的成功路径场景。
func TestIsAppTransitionAllowedHappyPath(t *testing.T) {
	cases := [][2]string{
		{AppStatusDraft, AppStatusInitializing},           // 场景：草稿应用允许进入初始化流程
		{AppStatusInitializing, AppStatusBindingWaiting},  // 场景：初始化完成后允许进入绑定等待状态
		{AppStatusBindingWaiting, AppStatusRunning},       // 场景：绑定完成后允许应用进入运行状态
		{AppStatusBindingWaiting, AppStatusBindingFailed}, // 场景：绑定等待超时或失败后允许进入绑定失败状态
		{AppStatusBindingFailed, AppStatusBindingWaiting}, // 场景：绑定失败后允许重新进入绑定等待重试
		{AppStatusRunning, AppStatusStopped},              // 场景：运行中的应用允许被停止
		{AppStatusStopped, AppStatusRunning},              // 场景：已停止应用允许重新启动
		{AppStatusError, AppStatusInitializing},           // 场景：错误状态应用允许重新初始化恢复
	}
	for _, c := range cases {
		if !IsAppTransitionAllowed(c[0], c[1]) {
			t.Errorf("expected %s -> %s allowed", c[0], c[1])
		}
	}
}

// TestIsAppTransitionAllowedRejectsBackwards 验证Is应用状态流转允许性拒绝回退的异常或拒绝路径场景。
func TestIsAppTransitionAllowedRejectsBackwards(t *testing.T) {
	require.False(t, IsAppTransitionAllowed(AppStatusRunning, AppStatusInitializing))
	require.False(t, IsAppTransitionAllowed(AppStatusDraft, AppStatusRunning))
	require.False(t, IsAppTransitionAllowed(AppStatusDraft, AppStatusDraft))
}

// TestIsAppTransitionAllowedDeletedOnlyFromError 验证Is应用状态流转允许性删除态仅来自错误的预期行为场景。
func TestIsAppTransitionAllowedDeletedOnlyFromError(t *testing.T) {
	require.False(t, IsAppTransitionAllowed(AppStatusRunning, AppStatusDeleted))
	require.True(t, IsAppTransitionAllowed(AppStatusError, AppStatusDeleted))
}

// TestEnsureAppTransitionWraps 验证确保应用Transition包装的预期行为场景。
func TestEnsureAppTransitionWraps(t *testing.T) {
	err := EnsureAppTransition(AppStatusRunning, AppStatusInitializing)
	require.Error(t, err)
	err = EnsureAppTransition(AppStatusDraft, AppStatusInitializing)
	require.NoError(t, err)
}

// TestAppIsTerminalOnlyDeleted 验证应用Is终态仅删除态的预期行为场景。
func TestAppIsTerminalOnlyDeleted(t *testing.T) {
	require.True(t, AppIsTerminal(AppStatusDeleted))
	for _, status := range []string{AppStatusError, AppStatusRunning, AppStatusStopped, AppStatusDraft} {
		require.False(t, AppIsTerminal(status))
	}
}

// TestIsAPIKeyTransitionAllowedHappyPath 验证IsAPIKey状态流转允许性成功路径的成功路径场景。
func TestIsAPIKeyTransitionAllowedHappyPath(t *testing.T) {
	cases := [][2]string{
		{APIKeyStatusPending, APIKeyStatusActive},  // 场景：待创建 API key 成功后允许变为 active
		{APIKeyStatusPending, APIKeyStatusError},   // 场景：待创建 API key 失败后允许变为 error
		{APIKeyStatusActive, APIKeyStatusDisabled}, // 场景：active API key 允许被禁用
		{APIKeyStatusActive, APIKeyStatusError},    // 场景：active API key 遇到异常时允许进入 error
		{APIKeyStatusDisabled, APIKeyStatusActive}, // 场景：disabled API key 允许重新启用
		{APIKeyStatusError, APIKeyStatusPending},   // 场景：error API key 允许回到 pending 重试
	}
	for _, c := range cases {
		if !IsAPIKeyTransitionAllowed(c[0], c[1]) {
			t.Errorf("expected api_key %s -> %s allowed", c[0], c[1])
		}
	}
}

// TestAPIKeyAndAppStateAreIndependent 验证APIKey并应用StateAreIndependent的预期行为场景。
func TestAPIKeyAndAppStateAreIndependent(t *testing.T) {
	// 如果 app 进入 stopped，api_key 仍可保持 active；反之亦然。
	require.True(t, IsAppTransitionAllowed(AppStatusRunning, AppStatusStopped))
	require.False(t, IsAppTransitionAllowed(APIKeyStatusActive, AppStatusStopped))
	require.True(t, IsAPIKeyTransitionAllowed(APIKeyStatusActive, APIKeyStatusDisabled))
}

// TestEnsureAPIKeyTransitionFailsForInvalid 验证确保APIKeyTransition失败针对非法的异常或拒绝路径场景。
func TestEnsureAPIKeyTransitionFailsForInvalid(t *testing.T) {
	err := EnsureAPIKeyTransition(APIKeyStatusDisabled, APIKeyStatusError)
	require.Error(t, err)
	err = EnsureAPIKeyTransition(APIKeyStatusPending, APIKeyStatusActive)
	require.NoError(t, err)
}
