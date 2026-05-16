# 审计日志可读标签 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在后端 `AuditResult` 上新增 4 个中文 label 字段，前端直接渲染，消除审计日志页面展示原始程序字符串的问题。

**Architecture:** 新增 `internal/service/audit_label.go` 集中维护 4 张翻译映射表，`toAuditResult()` 转换时调用 label 函数填充 `AuditResult` 的 4 个新字段；重新生成 OpenAPI 类型，前端两个审计页面改用 label 字段渲染。

**Tech Stack:** Go (service 层)、swag (OpenAPI 生成)、openapi-typescript (前端类型生成)、Vue 3 + Naive UI

---

## 文件清单

| 文件 | 操作 |
|---|---|
| `internal/service/audit_label.go` | 新建 — 4 张翻译映射表 + label 函数 |
| `internal/service/audit_label_test.go` | 新建 — 单元测试 |
| `internal/service/audit_service.go` | 修改 — AuditResult 新增 4 字段，toAuditResult() 填充 |
| `openapi/openapi.yaml` | 重新生成（`make openapi-gen`） |
| `web/src/api/generated.ts` | 重新生成（`make web-types-gen`） |
| `web/src/api/index.ts` | 修改 — AuditLog WithRequired 增加 4 个 label 字段 |
| `web/src/pages/audit/AuditLogsPage.vue` | 修改 — 列渲染改用 label 字段 |
| `web/src/pages/apps/AppAuditTab.vue` | 修改 — 列渲染改用 label 字段 |

---

## Task 1: 新建 `audit_label.go` 及其测试

**Files:**
- Create: `internal/service/audit_label.go`
- Create: `internal/service/audit_label_test.go`

- [ ] **Step 1: 写失败测试**

创建 `internal/service/audit_label_test.go`，内容如下：

```go
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
		{"system", "系统"},          // 系统任务
		{"platform_admin", "平台管理员"}, // 平台管理员
		{"org_admin", "组织管理员"},     // 组织管理员
		{"org_member", "组织成员"},     // 普通成员
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
		{"succeeded", "成功"}, // 成功
		{"failed", "失败"},   // 失败
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
		{"app", "应用实例"},              // 应用实例
		{"user", "成员用户"},             // 成员用户
		{"member", "成员"},              // 成员
		{"organization", "组织"},        // 组织
		{"runtime_node", "运行节点"},      // 运行节点
		{"knowledge_sync", "知识库同步"},   // 知识库同步（dispatch 失败记录）
		{"app_knowledge_sync", "应用知识库同步"}, // 应用知识库同步（worker 完成记录）
		{"newapi_call", "API 调用"},      // new-api 调用失败
		{"future_type", "future_type"}, // 未知类型 fallback
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
		{"app", "create", "创建应用"},                            // onboarding 创建应用
		{"app", "create_for_existing_member", "为已有成员创建应用"}, // 已有成员新建应用
		{"app", "update_model", "更换模型"},                      // 修改应用绑定模型
		{"app", "channel_auth_start", "渠道认证开始"},              // 渠道认证（失败时记录）
		{"app", "channel_bound", "绑定渠道"},                      // 渠道绑定
		{"app", "start", "启动应用"},                              // 容器启动
		{"app", "stop", "停止应用"},                               // 容器停止
		{"app", "restart", "重启应用"},                            // 容器重启
		{"app", "delete", "删除应用"},                             // 容器删除
		{"app", "disable_api_key", "禁用 API Key"},              // 禁用 API key
		{"app", "restore_api_key", "恢复 API Key"},              // 恢复 API key
		{"app", "initialize", "初始化应用"},                        // worker 初始化应用容器
		// user 资源
		{"user", "delete_member", "移除成员"}, // 成员禁用/删除
		// organization 资源
		{"organization", "recharge", "组织充值"}, // 组织余额充值
		// runtime_node 资源
		{"runtime_node", "initialize", "初始化节点"},           // 节点初始化（与 app.initialize 区分）
		{"runtime_node", "node_probe_recovered", "节点恢复正常"}, // 探针恢复
		{"runtime_node", "node_probe_degraded", "节点状态降级"},  // 探针降级
		{"runtime_node", "agent_enrolled", "节点注册"},         // 首次注册
		{"runtime_node", "agent_re_enrolled", "节点重新注册"},    // 重复注册（更新）
		// knowledge_sync 资源（dispatch 失败记录）
		{"knowledge_sync", "dispatch_org_upload_file", "组织文件上传分发"}, // 组织级上传分发失败
		{"knowledge_sync", "dispatch_app_upload_file", "应用文件上传分发"}, // 应用级上传分发失败
		{"knowledge_sync", "dispatch_org_delete_file", "组织文件删除分发"}, // 组织级删除分发失败
		{"knowledge_sync", "dispatch_app_delete_file", "应用文件删除分发"}, // 应用级删除分发失败
		// app_knowledge_sync 资源（worker 完成记录）
		{"app_knowledge_sync", "upload_file", "上传文件"},         // 应用知识库文件上传
		{"app_knowledge_sync", "delete_file", "删除文件"},         // 应用知识库文件删除
		{"app_knowledge_sync", "noop", "同步重试（无变更）"},           // 重试但无实际变更
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
```

- [ ] **Step 2: 运行测试确认编译失败**

```bash
cd /home/hujing/dir/software/ywjs/oc-manager && go test ./internal/service/ -run "TestLabel" -v 2>&1 | head -20
```

预期：编译报错，`labelActorRole` 等函数未定义。

- [ ] **Step 3: 新建 `audit_label.go` 实现翻译逻辑**

创建 `internal/service/audit_label.go`，内容如下：

```go
package service

// audit_label.go 集中维护审计日志字段的中文翻译映射。
// 所有 label 函数对未知值均 fallback 到原始字符串，保证后端扩展时前端不显示空白。

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
	"app":                "应用实例",
	"user":               "成员用户",
	"member":             "成员",
	"organization":       "组织",
	"runtime_node":       "运行节点",
	"knowledge_sync":     "知识库同步",
	"app_knowledge_sync": "应用知识库同步",
	"newapi_call":        "API 调用",
}

// actionLabels 以 (target_type, action) 二元组为 key 映射中文展示名。
// 使用二元组是因为 initialize 在 app 和 runtime_node 下含义不同，需要上下文区分。
var actionLabels = map[[2]string]string{
	// member 资源
	{"member", "create_with_app"}: "加入组织（含应用创建）",
	// app 资源
	{"app", "create"}:                  "创建应用",
	{"app", "create_for_existing_member"}: "为已有成员创建应用",
	{"app", "update_model"}:            "更换模型",
	{"app", "channel_auth_start"}:      "渠道认证开始",
	{"app", "channel_bound"}:           "绑定渠道",
	{"app", "start"}:                   "启动应用",
	{"app", "stop"}:                    "停止应用",
	{"app", "restart"}:                 "重启应用",
	{"app", "delete"}:                  "删除应用",
	{"app", "disable_api_key"}:         "禁用 API Key",
	{"app", "restore_api_key"}:         "恢复 API Key",
	{"app", "initialize"}:              "初始化应用",
	// user 资源
	{"user", "delete_member"}: "移除成员",
	// organization 资源
	{"organization", "recharge"}: "组织充值",
	// runtime_node 资源
	{"runtime_node", "initialize"}:          "初始化节点",
	{"runtime_node", "node_probe_recovered"}: "节点恢复正常",
	{"runtime_node", "node_probe_degraded"}:  "节点状态降级",
	{"runtime_node", "agent_enrolled"}:       "节点注册",
	{"runtime_node", "agent_re_enrolled"}:    "节点重新注册",
	// knowledge_sync 资源（dispatch 失败记录）
	{"knowledge_sync", "dispatch_org_upload_file"}: "组织文件上传分发",
	{"knowledge_sync", "dispatch_app_upload_file"}: "应用文件上传分发",
	{"knowledge_sync", "dispatch_org_delete_file"}: "组织文件删除分发",
	{"knowledge_sync", "dispatch_app_delete_file"}: "应用文件删除分发",
	// app_knowledge_sync 资源（worker 完成记录）
	{"app_knowledge_sync", "upload_file"}: "上传文件",
	{"app_knowledge_sync", "delete_file"}: "删除文件",
	{"app_knowledge_sync", "noop"}:        "同步重试（无变更）",
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
```

- [ ] **Step 4: 运行测试确认通过**

```bash
cd /home/hujing/dir/software/ywjs/oc-manager && go test ./internal/service/ -run "TestLabel" -v
```

预期：全部 PASS，无失败用例。

- [ ] **Step 5: Commit**

```bash
cd /home/hujing/dir/software/ywjs/oc-manager && git add internal/service/audit_label.go internal/service/audit_label_test.go && git commit -m "$(cat <<'EOF'
feat(audit): 新增审计日志字段中文翻译映射

新增 audit_label.go，集中维护 actor_role / result / target_type /
(target_type, action) 四张翻译表及对应 label 函数，未知值均 fallback
到原始字符串，保证后端扩展时前端不显示空白。

覆盖 28 条已知 (target_type, action) 对，含 noop / agent_re_enrolled
等边缘路径，新增对应单元测试全部通过。
EOF
)"
```

---

## Task 2: 修改 `audit_service.go` — 新增 label 字段并填充

**Files:**
- Modify: `internal/service/audit_service.go`

- [ ] **Step 1: 修改 `AuditResult` 结构体，新增 4 个 label 字段**

在 `internal/service/audit_service.go` 的 `AuditResult` 结构体末尾（`CreatedAt` 字段之后）添加：

```go
// 以下为展示用翻译字段，由 toAuditResult() 填充，未知值 fallback 到原始字符串。
// swaggertype 注解保证生成的 OpenAPI schema 使用 string 类型。
ActionLabel     string `json:"action_label" swaggertype:"string"`
TargetTypeLabel string `json:"target_type_label" swaggertype:"string"`
ActorRoleLabel  string `json:"actor_role_label" swaggertype:"string"`
ResultLabel     string `json:"result_label" swaggertype:"string"`
```

修改后 `AuditResult` 结构体完整内容如下（确认替换正确）：

```go
// AuditResult 表示对外返回的审计日志记录。
// IP 与元数据以字符串形式输出，避免暴露内部 pgtype 结构。
type AuditResult struct {
	ID           string             `json:"id"`
	ActorID      string             `json:"actor_id,omitempty"`
	ActorRole    string             `json:"actor_role"`
	OrgID        string             `json:"org_id,omitempty"`
	TargetType   string             `json:"target_type"`
	TargetID     string             `json:"target_id"`
	Action       string             `json:"action"`
	Result       string             `json:"result"`
	ErrorMessage string             `json:"error_message,omitempty"`
	IPAddress    string             `json:"ip_address,omitempty"`
	Metadata     map[string]any     `json:"metadata,omitempty"`
	CreatedAt    pgtype.Timestamptz `json:"created_at" swaggertype:"string" format:"date-time"`
	// 以下为展示用翻译字段，由 toAuditResult() 填充，未知值 fallback 到原始字符串。
	ActionLabel     string `json:"action_label"`
	TargetTypeLabel string `json:"target_type_label"`
	ActorRoleLabel  string `json:"actor_role_label"`
	ResultLabel     string `json:"result_label"`
}
```

- [ ] **Step 2: 修改 `toAuditResult()` 填充 label 字段**

在 `toAuditResult()` 函数的 `result := AuditResult{...}` 初始化块中，新增 4 个字段：

```go
func toAuditResult(row sqlc.AuditLog) AuditResult {
	result := AuditResult{
		ID:              uuidToString(row.ID),
		ActorID:         uuidToOptionalString(row.ActorID),
		ActorRole:       row.ActorRole,
		OrgID:           uuidToOptionalString(row.OrgID),
		TargetType:      row.TargetType,
		TargetID:        row.TargetID,
		Action:          row.Action,
		Result:          row.Result,
		CreatedAt:       row.CreatedAt,
		ActionLabel:     labelAction(row.TargetType, row.Action),
		TargetTypeLabel: labelTargetType(row.TargetType),
		ActorRoleLabel:  labelActorRole(row.ActorRole),
		ResultLabel:     labelResult(row.Result),
	}
	if row.ErrorMessage.Valid {
		result.ErrorMessage = row.ErrorMessage.String
	}
	if row.IpAddress != nil {
		result.IPAddress = row.IpAddress.String()
	}
	if len(row.MetadataJson) > 0 {
		metadata := map[string]any{}
		if err := json.Unmarshal(row.MetadataJson, &metadata); err == nil {
			result.Metadata = metadata
		}
	}
	return result
}
```

- [ ] **Step 3: 编译检查**

```bash
cd /home/hujing/dir/software/ywjs/oc-manager && go build ./internal/service/...
```

预期：无编译错误输出。

- [ ] **Step 4: 运行 service 全量测试**

```bash
cd /home/hujing/dir/software/ywjs/oc-manager && go test ./internal/service/... -v 2>&1 | tail -30
```

预期：所有测试 PASS，无 FAIL。

- [ ] **Step 5: Commit**

```bash
cd /home/hujing/dir/software/ywjs/oc-manager && git add internal/service/audit_service.go && git commit -m "$(cat <<'EOF'
feat(audit): AuditResult 新增 4 个中文 label 展示字段

在 AuditResult 结构体新增 action_label / target_type_label /
actor_role_label / result_label 字段，toAuditResult() 转换时
调用 audit_label.go 中的翻译函数填充，原始字段保留不变。
EOF
)"
```

---

## Task 3: 重新生成 OpenAPI 类型并更新前端类型定义

**Files:**
- Modify: `openapi/openapi.yaml` （make openapi-gen 重新生成）
- Modify: `web/src/api/generated.ts` （make web-types-gen 重新生成）
- Modify: `web/src/api/index.ts`

- [ ] **Step 1: 重新生成 OpenAPI yaml**

```bash
cd /home/hujing/dir/software/ywjs/oc-manager && make openapi-gen
```

预期：`openapi/openapi.yaml` 中 `service.AuditResult` schema 新增 4 个 label 属性，无报错。

验证：
```bash
grep -A2 'action_label\|target_type_label\|actor_role_label\|result_label' openapi/openapi.yaml
```

预期输出包含这 4 个字段。

- [ ] **Step 2: 重新生成前端 TypeScript 类型**

```bash
cd /home/hujing/dir/software/ywjs/oc-manager && make web-types-gen
```

预期：`web/src/api/generated.ts` 中 `service.AuditResult` 包含 4 个新 label 字段（类型为 `string`）。

验证：
```bash
grep 'action_label\|target_type_label\|actor_role_label\|result_label' web/src/api/generated.ts
```

预期输出包含这 4 个字段。

- [ ] **Step 3: 更新 `web/src/api/index.ts` 的 `AuditLog` 类型约束**

将 `AuditLog` 的 `WithRequired` 约束从：

```typescript
// AuditLog：id / actor_role / target_type / target_id / action / result / created_at 后端必返
export type AuditLog = WithRequired<
  Schemas['service.AuditResult'],
  'id' | 'actor_role' | 'target_type' | 'target_id' | 'action' | 'result' | 'created_at'
>
```

改为：

```typescript
// AuditLog：id / actor_role / target_type / target_id / action / result / created_at 后端必返，
// *_label 为对应字段的中文展示名，后端同步填充。
export type AuditLog = WithRequired<
  Schemas['service.AuditResult'],
  | 'id' | 'actor_role' | 'target_type' | 'target_id' | 'action' | 'result' | 'created_at'
  | 'action_label' | 'target_type_label' | 'actor_role_label' | 'result_label'
>
```

- [ ] **Step 4: 前端类型检查**

```bash
cd /home/hujing/dir/software/ywjs/oc-manager/web && npx tsc --noEmit 2>&1 | head -30
```

预期：无类型错误（如果有错误则为 label 字段引用问题，需修正 Step 3 中的字段名）。

- [ ] **Step 5: Commit**

```bash
cd /home/hujing/dir/software/ywjs/oc-manager && git add openapi/openapi.yaml web/src/api/generated.ts web/src/api/index.ts && git commit -m "$(cat <<'EOF'
chore(openapi): 重新生成 AuditResult OpenAPI 类型，新增 4 个 label 字段

make openapi-gen + make web-types-gen 同步生成，AuditLog 类型约束
同步将 4 个 label 字段加入 WithRequired 必填列表。
EOF
)"
```

---

## Task 4: 更新前端两个审计页面

**Files:**
- Modify: `web/src/pages/audit/AuditLogsPage.vue`
- Modify: `web/src/pages/apps/AppAuditTab.vue`

- [ ] **Step 1: 修改 `AuditLogsPage.vue` 列渲染**

在 `web/src/pages/audit/AuditLogsPage.vue` 中，将 `columns` 定义中的 4 处原始字段替换为 label 字段：

操作者列（将 `row.actor_role` 改为 `row.actor_role_label`）：
```typescript
{
  title: '操作者', key: 'actor_role',
  render: (row) => [
    h('strong', row.actor_role_label),
    row.actor_id ? h('small', { style: 'display:block;color:#8A94C6;font-size:12px' }, row.actor_id) : null,
  ],
},
```

资源列（将 `row.target_type` 改为 `row.target_type_label`）：
```typescript
{
  title: '资源', key: 'target_type',
  render: (row) => [
    h('strong', row.target_type_label),
    h('small', { style: 'display:block;color:#8A94C6;font-size:12px' }, row.target_id),
  ],
},
```

操作列（纯文本列，改用 `action_label`）：
```typescript
{ title: '操作', key: 'action', render: (row) => row.action_label },
```

结果列（NTag 内文字改用 `result_label`，tag 类型判断仍用原始 `row.result`）：
```typescript
{
  title: '结果', key: 'result',
  render: (row) => [
    h(NTag, { type: auditTagType(row.result), size: 'small', bordered: false }, { default: () => row.result_label }),
    row.error_message ? h('small', { style: 'display:block;color:#FF3B5C;font-size:12px' }, row.error_message) : null,
  ],
},
```

- [ ] **Step 2: 修改 `AppAuditTab.vue` 列渲染**

在 `web/src/pages/apps/AppAuditTab.vue` 中，将 `columns` 定义中的 3 处原始字段替换为 label 字段：

操作者列（将 `row.actor_role` 改为 `row.actor_role_label`）：
```typescript
{
  title: '操作者', key: 'actor_role',
  render: (row) => [
    h('strong', row.actor_role_label),
    row.actor_id ? h('small', { style: 'display:block;color:#8A94C6;font-size:12px' }, row.actor_id) : null,
  ],
},
```

操作列（纯文本列，改用 `action_label`）：
```typescript
{ title: '操作', key: 'action', render: (row) => row.action_label },
```

结果列（NTag 内文字改用 `result_label`，tag 类型判断仍用原始 `row.result`）：
```typescript
{
  title: '结果', key: 'result',
  render: (row) => [
    h(NTag, { type: auditTagType(row.result), size: 'small', bordered: false }, { default: () => row.result_label }),
    row.error_message ? h('small', { style: 'display:block;color:#FF3B5C;font-size:12px' }, row.error_message) : null,
  ],
},
```

- [ ] **Step 3: 前端类型检查**

```bash
cd /home/hujing/dir/software/ywjs/oc-manager/web && npx tsc --noEmit 2>&1 | head -30
```

预期：无类型错误。

- [ ] **Step 4: Commit**

```bash
cd /home/hujing/dir/software/ywjs/oc-manager && git add web/src/pages/audit/AuditLogsPage.vue web/src/pages/apps/AppAuditTab.vue && git commit -m "$(cat <<'EOF'
feat(web): 审计日志页面改用后端 label 字段展示中文

AuditLogsPage 和 AppAuditTab 的操作者、资源、操作、结果列
均改为渲染 *_label 字段，auditTagType() 颜色判断仍依赖原始 result 值。
EOF
)"
```
