// Package domain 的枚举测试覆盖已登记值和未知值，防止业务字符串漂移。
package domain

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestEnumValidatorsAcceptKnownValues 验证EnumValidatorsAcceptKnown值的预期行为场景。
func TestEnumValidatorsAcceptKnownValues(t *testing.T) {
	tests := []struct {
		name  string
		value string
		check func(string) bool
	}{
		{name: "用户角色", value: UserRoleOrgMember, check: IsUserRole},
		{name: "应用状态", value: AppStatusBindingWaiting, check: IsAppStatus},
		{name: "运行节点状态", value: RuntimeNodeStatusUnreachable, check: IsRuntimeNodeStatus},
		{name: "渠道状态", value: ChannelStatusPendingAuth, check: IsChannelStatus},
		{name: "任务类型", value: JobTypeWorkspaceArchiveCleanup, check: IsJobType},
	}

	for _, tt := range tests {
		// 当前子测试覆盖表格用例中该名称对应的输入组合、边界条件和期望结果。
		t.Run(tt.name, func(t *testing.T) {
			require.True(t, tt.check(tt.value))
		})
	}
}

// TestEnumValidatorsRejectUnknownValues 验证枚举校验拒绝未知值的预期行为场景。
func TestEnumValidatorsRejectUnknownValues(t *testing.T) {
	tests := []struct {
		name  string
		value string
		check func(string) bool
	}{
		{name: "空用户角色", value: "", check: IsUserRole},
		{name: "未知应用状态", value: "ready", check: IsAppStatus},
		{name: "未知运行节点状态", value: "local", check: IsRuntimeNodeStatus},
		{name: "未知渠道状态", value: "waiting", check: IsChannelStatus},
		{name: "已删除的旧任务类型", value: "knowledge_import", check: IsJobType},
	}

	for _, tt := range tests {
		// 当前子测试覆盖表格用例中该名称对应的输入组合、边界条件和期望结果。
		t.Run(tt.name, func(t *testing.T) {
			require.False(t, tt.check(tt.value))
		})
	}
}
