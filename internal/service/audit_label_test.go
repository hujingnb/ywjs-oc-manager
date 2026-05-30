package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestLabelActorRole 验证角色字段的中文翻译及未知值 fallback。
func TestLabelActorRole(t *testing.T) {
	// 已知角色逐一验证
	cases := []struct {
		input    string
		expected string
	}{
		{"system", "系统"},                 // 系统任务
		{"platform_admin", "平台管理员"},      // 平台管理员
		{"org_admin", "企业管理员"},           // 企业管理员
		{"org_member", "企业成员"},           // 普通企业成员
		{"unknown_role", "unknown_role"}, // 未知值 fallback
	}
	for _, tc := range cases {
		assert.Equal(t, tc.expected, labelActorRole(tc.input), "input: %s", tc.input)
	}
}

// TestLabelResult 验证操作结果的中文翻译及未知值 fallback。
func TestLabelResult(t *testing.T) {
	cases := []struct {
		input    string
		expected string
	}{
		{"succeeded", "成功"},    // 成功
		{"failed", "失败"},       // 失败
		{"unknown", "unknown"}, // 未知值 fallback
	}
	for _, tc := range cases {
		assert.Equal(t, tc.expected, labelResult(tc.input), "input: %s", tc.input)
	}
}

// TestLabelTargetType 验证资源类型的中文翻译及未知值 fallback。
func TestLabelTargetType(t *testing.T) {
	cases := []struct {
		input    string
		expected string
	}{
		{"app", "应用实例"},                // 应用实例
		{"user", "成员用户"},               // 成员用户
		{"member", "成员"},               // 成员
		{"organization", "企业"},         // 企业资源
		{"newapi_call", "API 调用"},      // new-api 调用失败
		{"future_type", "future_type"}, // 未知类型 fallback
	}
	for _, tc := range cases {
		assert.Equal(t, tc.expected, labelTargetType(tc.input), "input: %s", tc.input)
	}
}

// TestLabelAction 验证动作字段的中文翻译：key 为 (target_type, action) 二元组，
// 同名 action（如 initialize）在不同 target_type 下含义不同，需要上下文区分。
func TestLabelAction(t *testing.T) {
	cases := []struct {
		targetType string
		action     string
		expected   string
	}{
		// member 资源
		{"member", "create_with_app", "加入企业（含应用创建）"}, // onboarding 新成员
		// app 资源
		{"app", "create", "创建应用"},                            // onboarding 创建应用
		{"app", "create_for_existing_member", "为已有成员创建应用"},   // 已有成员新建应用
		{"app", "update_model", "更换模型"},                      // 修改应用绑定模型
		{"app", "channel_auth_start", "渠道认证开始"},              // 渠道认证（失败时记录）
		{"app", "channel_bound", "绑定渠道"},                     // 渠道绑定
		{"app", "start", "启动应用"},                             // 容器启动
		{"app", "stop", "停止应用"},                              // 容器停止
		{"app", "restart", "重启应用"},                           // 容器重启
		{"app", "delete", "删除应用"},                            // 容器删除
		{"app", "disable_api_key", "禁用 API Key"},             // 禁用 API key
		{"app", "restore_api_key", "恢复 API Key"},             // 恢复 API key
		{"app", "initialize", "初始化应用"},                       // worker 初始化应用容器
		{"app", AuditActionAppRuntimeImageChanged, "应用镜像变更"}, // 平台管理员手动改 apps.runtime_image_ref；本期无 UI，常量与 label 预置
		// user 资源
		{"user", "delete_member", "移除成员"}, // 成员禁用/删除
		// organization 资源
		{"organization", "recharge", "企业充值"}, // 企业余额充值
		// newapi_call 资源：action 是 HTTP endpoint，无固定枚举，fallback 到原始值
		{"newapi_call", "POST /api/user/", "POST /api/user/"}, // HTTP endpoint fallback
		// 未知二元组 fallback
		{"app", "future_action", "future_action"}, // 未来扩展的未知 action fallback
	}
	for _, tc := range cases {
		assert.Equal(t, tc.expected, labelAction(tc.targetType, tc.action),
			"targetType=%s action=%s", tc.targetType, tc.action)
	}
}
