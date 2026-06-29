// Package domain 的枚举测试覆盖已登记值和未知值，防止业务字符串漂移。
package domain

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEnumValidatorsAcceptKnownValues 验证EnumValidatorsAcceptKnown值的预期行为场景。
func TestEnumValidatorsAcceptKnownValues(t *testing.T) {
	tests := []struct {
		name  string
		value string
		check func(string) bool
	}{
		{name: "用户角色", value: UserRoleOrgMember, check: IsUserRole},                       // 场景：用户角色
		{name: "应用状态", value: AppStatusBindingWaiting, check: IsAppStatus},      // 场景：应用状态
		{name: "渠道状态", value: ChannelStatusPendingAuth, check: IsChannelStatus}, // 场景：渠道状态
		{name: "任务类型", value: JobTypeWorkspaceArchiveCleanup, check: IsJobType},           // 场景：任务类型
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
		{name: "空用户角色", value: "", check: IsUserRole},                    // 场景：空用户角色
		{name: "未知应用状态", value: "ready", check: IsAppStatus},         // 场景：未知应用状态
		{name: "未知渠道状态", value: "waiting", check: IsChannelStatus},   // 场景：未知渠道状态
		{name: "已删除的旧任务类型", value: "knowledge_import", check: IsJobType}, // 场景：已删除的旧任务类型
	}

	for _, tt := range tests {
		// 当前子测试覆盖表格用例中该名称对应的输入组合、边界条件和期望结果。
		t.Run(tt.name, func(t *testing.T) {
			require.False(t, tt.check(tt.value))
		})
	}
}

// TestIsRuntimePhase 验证运行时就绪维度取值校验：4 个合法值通过、非法值与空串拒绝。
func TestIsRuntimePhase(t *testing.T) {
	// 合法取值：ready/starting/restarting/unknown 全部应通过。
	for _, v := range []string{RuntimePhaseReady, RuntimePhaseStarting, RuntimePhaseRestarting, RuntimePhaseUnknown} {
		assert.True(t, IsRuntimePhase(v), "合法 runtime_phase 应通过: %s", v)
	}
	// 非法取值：业务态字符串与空串都不是合法 runtime_phase。
	for _, v := range []string{"running", "", "bad"} {
		assert.False(t, IsRuntimePhase(v), "非法 runtime_phase 应拒绝: %q", v)
	}
}

// TestWebPublishProvisioningStatus 覆盖：四个 provisioning 状态合法，未知值非法，
// 保证写库前状态机取值受控。
func TestWebPublishProvisioningStatus(t *testing.T) {
	for _, s := range []string{ProvisioningDisabled, ProvisioningInProgress, ProvisioningReady, ProvisioningFailed} {
		assert.Truef(t, IsProvisioningStatus(s), "%s 应合法", s)
	}
	assert.False(t, IsProvisioningStatus("done"))
}

// TestWebPublishCertStatus 覆盖：五个证书状态合法，未知值非法（页面展示与巡检依赖）。
func TestWebPublishCertStatus(t *testing.T) {
	for _, s := range []string{CertStatusNone, CertStatusIssuing, CertStatusIssued, CertStatusRenewing, CertStatusFailed} {
		assert.Truef(t, IsCertStatus(s), "%s 应合法", s)
	}
	assert.False(t, IsCertStatus("expired"))
}

// TestWebPublishJobTypeRegistered 覆盖：新增的 provisioning job type 已登记，
// 否则 worker dispatch 时会报未注册类型。
func TestWebPublishJobTypeRegistered(t *testing.T) {
	assert.True(t, IsJobType(JobTypeWebPublishProvision))
}

// TestSiteStatus 覆盖：三个合法站点状态通过校验，非法值拒绝，
// 保证写库前 status 字段取值受 CHECK 约束对齐。
func TestSiteStatus(t *testing.T) {
	// 三个合法值：active/disabled/expired 均应通过。
	for _, s := range []string{SiteStatusActive, SiteStatusDisabled, SiteStatusExpired} {
		assert.Truef(t, IsSiteStatus(s), "%s 应为合法站点状态", s)
	}
	// 非法值：deleted 不属于 published_sites.status 取值集合，应拒绝。
	assert.False(t, IsSiteStatus("deleted"), "deleted 不是合法站点状态，应拒绝")
}
