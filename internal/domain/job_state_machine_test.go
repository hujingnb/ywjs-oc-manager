// Package domain 的任务状态机测试覆盖调度器允许的重试、取消和终态约束。
package domain

import (
	"github.com/stretchr/testify/require"
	"testing"
)

// TestIsJobTransitionAllowedValid 验证Is任务状态流转允许性合法的预期行为场景。
func TestIsJobTransitionAllowedValid(t *testing.T) {
	cases := []struct {
		from string
		to   string
	}{
		{JobStatusPending, JobStatusRunning},   // 场景：pending 任务允许被 worker 领取进入 running
		{JobStatusPending, JobStatusCanceled},  // 场景：pending 任务允许在执行前取消
		{JobStatusRunning, JobStatusSucceeded}, // 场景：running 任务允许成功结束
		{JobStatusRunning, JobStatusFailed},    // 场景：running 任务允许失败结束
		{JobStatusRunning, JobStatusPending},   // 场景：running 任务允许失败重试时回到 pending
		{JobStatusFailed, JobStatusPending},    // 场景：failed 任务允许重试回到 pending
	}
	for _, c := range cases {
		if !IsJobTransitionAllowed(c.from, c.to) {
			t.Errorf("expected %s -> %s allowed", c.from, c.to)
		}
	}
}

// TestIsJobTransitionAllowedRejectsBackToPendingFromTerminal 验证Is任务状态流转允许性拒绝回退到等待中来自终态的异常或拒绝路径场景。
func TestIsJobTransitionAllowedRejectsBackToPendingFromTerminal(t *testing.T) {
	require.False(t, IsJobTransitionAllowed(JobStatusSucceeded, JobStatusPending))
	require.False(t, IsJobTransitionAllowed(JobStatusCanceled, JobStatusRunning))
	require.False(t, IsJobTransitionAllowed(JobStatusRunning, JobStatusRunning))
}

// TestEnsureJobTransitionReturnsError 验证确保任务Transition返回错误的成功路径场景。
func TestEnsureJobTransitionReturnsError(t *testing.T) {
	err := EnsureJobTransition(JobStatusSucceeded, JobStatusPending)
	require.Error(t, err)
	err = EnsureJobTransition(JobStatusPending, JobStatusRunning)
	require.NoError(t, err)
}

// TestJobIsTerminal 验证任务Is终态的预期行为场景。
func TestJobIsTerminal(t *testing.T) {
	require.True(t, JobIsTerminal(JobStatusSucceeded))
	require.True(t, JobIsTerminal(JobStatusFailed))
	require.True(t, JobIsTerminal(JobStatusCanceled))
	require.False(t, JobIsTerminal(JobStatusPending))
	require.False(t, JobIsTerminal(JobStatusRunning))
}

// TestAllowedJobTransitionsCount 验证Allowed任务Transitions数量的预期行为场景。
func TestAllowedJobTransitionsCount(t *testing.T) {
	require.Len(t, AllowedJobTransitions(), 6)
}
