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
		{"system", "系统"},               // 系统任务
		{"platform_admin", "平台管理员"},    // 平台管理员
		{"org_admin", "组织管理员"},         // 组织管理员
		{"org_member", "组织成员"},         // 普通成员
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
		{"succeeded", "成功"},     // 成功
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
		{"app", "应用实例"},                    // 应用实例
		{"user", "成员用户"},                   // 成员用户
		{"member", "成员"},                   // 成员
		{"organization", "组织"},             // 组织
		{"runtime_node", "运行节点"},           // 运行节点
		{"knowledge_sync", "知识库同步"},        // 知识库同步（dispatch 失败记录）
		{"app_knowledge_sync", "应用知识库同步"}, // 应用知识库同步（worker 完成记录）
		{"newapi_call", "API 调用"},           // new-api 调用失败
		{"future_type", "future_type"},       // 未知类型 fallback
	}
	for _, tc := range cases {
		assert.Equal(t, tc.expected, labelTargetType(tc.input), "input: %s", tc.input)
	}
}

// TestLabelAction 验证动作字段的中文翻译：key 为 (target_type, action) 二元组，
// 因 initialize 在 app 和 runtime_node 下含义不同，需要上下文区分。
func TestLabelAction(t *testing.T) {
	cases := []struct {
		targetType string
		action     string
		expected   string
	}{
		// member 资源
		{"member", "create_with_app", "加入组织（含应用创建）"}, // onboarding 新成员
		// app 资源
		{"app", "create", "创建应用"},                           // onboarding 创建应用
		{"app", "create_for_existing_member", "为已有成员创建应用"}, // 已有成员新建应用
		{"app", "update_model", "更换模型"},                     // 修改应用绑定模型
		{"app", "channel_auth_start", "渠道认证开始"},             // 渠道认证（失败时记录）
		{"app", "channel_bound", "绑定渠道"},                     // 渠道绑定
		{"app", "start", "启动应用"},                             // 容器启动
		{"app", "stop", "停止应用"},                              // 容器停止
		{"app", "restart", "重启应用"},                           // 容器重启
		{"app", "delete", "删除应用"},                            // 容器删除
		{"app", "disable_api_key", "禁用 API Key"},             // 禁用 API key
		{"app", "restore_api_key", "恢复 API Key"},             // 恢复 API key
		{"app", "initialize", "初始化应用"},                       // worker 初始化应用容器
		// user 资源
		{"user", "delete_member", "移除成员"}, // 成员禁用/删除
		// organization 资源
		{"organization", "recharge", "组织充值"}, // 组织余额充值
		// runtime_node 资源
		{"runtime_node", "initialize", "初始化节点"},            // 节点初始化（与 app.initialize 区分）
		{"runtime_node", "node_probe_recovered", "节点恢复正常"}, // 探针恢复
		{"runtime_node", "node_probe_degraded", "节点状态降级"},  // 探针降级
		{"runtime_node", "agent_enrolled", "节点注册"},          // 首次注册
		{"runtime_node", "agent_re_enrolled", "节点重新注册"},     // 重复注册（更新）
		// knowledge_sync 资源（dispatch 失败记录）
		{"knowledge_sync", "dispatch_org_upload_file", "组织文件上传分发"}, // 组织级上传分发失败
		{"knowledge_sync", "dispatch_app_upload_file", "应用文件上传分发"}, // 应用级上传分发失败
		{"knowledge_sync", "dispatch_org_delete_file", "组织文件删除分发"}, // 组织级删除分发失败
		{"knowledge_sync", "dispatch_app_delete_file", "应用文件删除分发"}, // 应用级删除分发失败
		// app_knowledge_sync 资源（worker 完成记录）
		{"app_knowledge_sync", "upload_file", "上传文件"},       // 应用知识库文件上传
		{"app_knowledge_sync", "delete_file", "删除文件"},       // 应用知识库文件删除
		{"app_knowledge_sync", "noop", "同步重试（无变更）"},         // 重试但无实际变更
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
