package domain

import "testing"

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
		t.Run(tt.name, func(t *testing.T) {
			if !tt.check(tt.value) {
				t.Fatalf("期望 %q 是合法枚举值", tt.value)
			}
		})
	}
}

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
		t.Run(tt.name, func(t *testing.T) {
			if tt.check(tt.value) {
				t.Fatalf("期望 %q 被判定为非法枚举值", tt.value)
			}
		})
	}
}
