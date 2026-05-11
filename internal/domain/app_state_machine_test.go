// Package domain 的应用状态机测试覆盖合法路径、非法倒退和终态约束。
package domain

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestIsAppTransitionAllowedHappyPath(t *testing.T) {
	cases := [][2]string{
		{AppStatusDraft, AppStatusInitializing},
		{AppStatusInitializing, AppStatusBindingWaiting},
		{AppStatusBindingWaiting, AppStatusRunning},
		{AppStatusBindingWaiting, AppStatusBindingFailed},
		{AppStatusBindingFailed, AppStatusBindingWaiting},
		{AppStatusRunning, AppStatusStopped},
		{AppStatusStopped, AppStatusRunning},
		{AppStatusError, AppStatusInitializing},
	}
	for _, c := range cases {
		if !IsAppTransitionAllowed(c[0], c[1]) {
			t.Errorf("expected %s -> %s allowed", c[0], c[1])
		}
	}
}

func TestIsAppTransitionAllowedRejectsBackwards(t *testing.T) {
	require.False(t, IsAppTransitionAllowed(AppStatusRunning, AppStatusInitializing))
	require.False(t, IsAppTransitionAllowed(AppStatusDraft, AppStatusRunning))
	require.False(t, IsAppTransitionAllowed(AppStatusDraft, AppStatusDraft))
}

func TestIsAppTransitionAllowedDeletedOnlyFromError(t *testing.T) {
	require.False(t, IsAppTransitionAllowed(AppStatusRunning, AppStatusDeleted))
	require.True(t, IsAppTransitionAllowed(AppStatusError, AppStatusDeleted))
}

func TestEnsureAppTransitionWraps(t *testing.T) {
	err := EnsureAppTransition(AppStatusRunning, AppStatusInitializing)
	require.Error(t, err)
	err = EnsureAppTransition(AppStatusDraft, AppStatusInitializing)
	require.NoError(t, err)
}

func TestAppIsTerminalOnlyDeleted(t *testing.T) {
	require.True(t, AppIsTerminal(AppStatusDeleted))
	for _, status := range []string{AppStatusError, AppStatusRunning, AppStatusStopped, AppStatusDraft} {
		require.False(t, AppIsTerminal(status))
	}
}

func TestIsAPIKeyTransitionAllowedHappyPath(t *testing.T) {
	cases := [][2]string{
		{APIKeyStatusPending, APIKeyStatusActive},
		{APIKeyStatusPending, APIKeyStatusError},
		{APIKeyStatusActive, APIKeyStatusDisabled},
		{APIKeyStatusActive, APIKeyStatusError},
		{APIKeyStatusDisabled, APIKeyStatusActive},
		{APIKeyStatusError, APIKeyStatusPending},
	}
	for _, c := range cases {
		if !IsAPIKeyTransitionAllowed(c[0], c[1]) {
			t.Errorf("expected api_key %s -> %s allowed", c[0], c[1])
		}
	}
}

func TestAPIKeyAndAppStateAreIndependent(t *testing.T) {
	// 如果 app 进入 stopped，api_key 仍可保持 active；反之亦然。
	require.True(t, IsAppTransitionAllowed(AppStatusRunning, AppStatusStopped))
	require.False(t, IsAppTransitionAllowed(APIKeyStatusActive, AppStatusStopped))
	require.True(t, IsAPIKeyTransitionAllowed(APIKeyStatusActive, APIKeyStatusDisabled))
}

func TestEnsureAPIKeyTransitionFailsForInvalid(t *testing.T) {
	err := EnsureAPIKeyTransition(APIKeyStatusDisabled, APIKeyStatusError)
	require.Error(t, err)
	err = EnsureAPIKeyTransition(APIKeyStatusPending, APIKeyStatusActive)
	require.NoError(t, err)
}
