package service

// audit_label.go 集中维护审计日志字段的中文翻译映射。
// 所有 label 函数对未知值均 fallback 到原始字符串，保证后端扩展时前端不显示空白。

// AuditActionAppRuntimeImageChanged 是审计事件 app.runtime_image_changed 的
// action 字段值。平台管理员手动修改 apps.runtime_image_ref 时触发，
// 写入审计时 target_type 取 "app"、action 取本常量值；metadata 应携带 from /
// to 镜像 tag 便于排查。本期不提供 UI，常量与 i18n 标签先就位，未来加 UI
// 直接消费，避免散落 magic string。
const AuditActionAppRuntimeImageChanged = "runtime_image_changed"

// actorRoleLabels 将 actor_role 原始值映射为中文展示名。
var actorRoleLabels = map[string]string{
	"system":         "系统",
	"platform_admin": "平台管理员",
	"org_admin":      "组织管理员",
	"org_member":     "组织成员",
}

// resultLabels 将 result 原始值映射为中文展示名。
var resultLabels = map[string]string{
	"succeeded": "成功",
	"failed":    "失败",
}

// targetTypeLabels 将 target_type 原始值映射为中文展示名。
var targetTypeLabels = map[string]string{
	"app":          "应用实例",
	"user":         "成员用户",
	"member":       "成员",
	"organization": "组织",
	"runtime_node": "运行节点",
	"newapi_call":  "API 调用",
}

// actionLabels 以 (target_type, action) 二元组为 key 映射中文展示名。
// 使用二元组是因为 initialize 在 app 和 runtime_node 下含义不同，需要上下文区分。
var actionLabels = map[[2]string]string{
	// member 资源
	{"member", "create_with_app"}: "加入组织（含应用创建）",
	// app 资源
	{"app", "create"}:                     "创建应用",
	{"app", "create_for_existing_member"}: "为已有成员创建应用",
	{"app", "update_model"}:               "更换模型",
	{"app", "channel_auth_start"}:         "渠道认证开始",
	{"app", "channel_bound"}:              "绑定渠道",
	{"app", "start"}:                      "启动应用",
	{"app", "stop"}:                       "停止应用",
	{"app", "restart"}:                    "重启应用",
	{"app", "delete"}:                     "删除应用",
	{"app", "disable_api_key"}:            "禁用 API Key",
	{"app", "restore_api_key"}:            "恢复 API Key",
	{"app", "initialize"}:                 "初始化应用",
	// app.runtime_image_changed：平台管理员手动改 apps.runtime_image_ref 时触发。
	// 本期不实现 UI，常量与 label 先落位避免未来散落 magic string。
	{"app", AuditActionAppRuntimeImageChanged}: "应用镜像变更",
	// user 资源
	{"user", "delete_member"}: "移除成员",
	// organization 资源
	{"organization", "recharge"}: "组织充值",
	// runtime_node 资源
	{"runtime_node", "initialize"}:           "初始化节点",
	{"runtime_node", "node_probe_recovered"}: "节点恢复正常",
	{"runtime_node", "node_probe_degraded"}:  "节点状态降级",
	{"runtime_node", "agent_enrolled"}:       "节点注册",
	{"runtime_node", "agent_re_enrolled"}:    "节点重新注册",
}

// labelActorRole 返回 actor_role 的中文展示名，未知值返回原始字符串。
func labelActorRole(role string) string {
	if label, ok := actorRoleLabels[role]; ok {
		return label
	}
	return role
}

// labelResult 返回 result 的中文展示名，未知值返回原始字符串。
func labelResult(result string) string {
	if label, ok := resultLabels[result]; ok {
		return label
	}
	return result
}

// labelTargetType 返回 target_type 的中文展示名，未知值返回原始字符串。
func labelTargetType(targetType string) string {
	if label, ok := targetTypeLabels[targetType]; ok {
		return label
	}
	return targetType
}

// labelAction 返回 (target_type, action) 二元组对应的中文展示名，未知组合返回原始 action 字符串。
func labelAction(targetType, action string) string {
	if label, ok := actionLabels[[2]string{targetType, action}]; ok {
		return label
	}
	return action
}
