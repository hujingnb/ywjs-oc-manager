# 审计日志可读标签设计

**日期**：2026-05-16  
**状态**：已确认

## 背景

审计日志的 `action`（如 `create_for_existing_member`）、`target_type`（如 `app`）、`actor_role`（如 `platform_admin`）、`result`（如 `succeeded`）均为面向程序的原始字符串，直接展示给用户体验差。需要在后端翻译为中文后返回，前端直接使用。

## 方案

**方案一（采用）：AuditResult 直接增加 label 字段**

后端在现有 `AuditResult` 结构体上新增 4 个只读展示字段，service 层 `toAuditResult()` 转换时填充翻译值，原始字段保留不动，前端直接渲染 label 字段。

未采用方案：
- 独立 labels 字典 endpoint（需额外请求，前端状态更复杂）
- 后端返回 `labels map[string]string`（类型不明确，前端访问繁琐）

## 后端设计

### AuditResult 新增字段

```go
type AuditResult struct {
    // ... 现有字段保持不变 ...

    // 以下为展示用翻译字段，由 toAuditResult() 填充，未知值 fallback 到原始字符串
    ActionLabel     string `json:"action_label"`
    TargetTypeLabel string `json:"target_type_label"`
    ActorRoleLabel  string `json:"actor_role_label"`
    ResultLabel     string `json:"result_label"`
}
```

### 新文件：`internal/service/audit_label.go`

集中维护 4 张翻译映射表，提供以下函数：

- `labelTargetType(targetType string) string`
- `labelAction(targetType, action string) string`  — key 为二元组，因 `initialize` 在 `app` 和 `runtime_node` 下含义不同
- `labelActorRole(role string) string`
- `labelResult(result string) string`

所有函数对未知值 fallback 到原始字符串，保证后端扩展时前端不会显示空白。

### `toAuditResult()` 改动

在转换函数中调用上述 4 个 label 函数，填充新字段。

### 翻译表

**`actor_role`**：

| 原始值 | 中文 |
|---|---|
| `system` | 系统 |
| `platform_admin` | 平台管理员 |
| `org_admin` | 组织管理员 |
| `org_member` | 组织成员 |

**`result`**：

| 原始值 | 中文 |
|---|---|
| `succeeded` | 成功 |
| `failed` | 失败 |

**`target_type`**：

| 原始值 | 中文 |
|---|---|
| `app` | 应用实例 |
| `user` | 成员用户 |
| `member` | 成员 |
| `organization` | 组织 |
| `runtime_node` | 运行节点 |
| `knowledge_sync` | 知识库同步 |
| `app_knowledge_sync` | 应用知识库同步 |
| `newapi_call` | API 调用 |

**`(target_type, action)` 二元组**（共 28 条）：

| target_type | action | 中文 |
|---|---|---|
| `member` | `create_with_app` | 加入组织（含应用创建）|
| `app` | `create` | 创建应用 |
| `app` | `create_for_existing_member` | 为已有成员创建应用 |
| `user` | `delete_member` | 移除成员 |
| `app` | `update_model` | 更换模型 |
| `app` | `channel_auth_start` | 渠道认证开始 |
| `app` | `channel_bound` | 绑定渠道 |
| `app` | `start` | 启动应用 |
| `app` | `stop` | 停止应用 |
| `app` | `restart` | 重启应用 |
| `app` | `delete` | 删除应用 |
| `app` | `disable_api_key` | 禁用 API Key |
| `app` | `restore_api_key` | 恢复 API Key |
| `app` | `initialize` | 初始化应用 |
| `organization` | `recharge` | 组织充值 |
| `runtime_node` | `initialize` | 初始化节点 |
| `runtime_node` | `node_probe_recovered` | 节点恢复正常 |
| `runtime_node` | `node_probe_degraded` | 节点状态降级 |
| `runtime_node` | `agent_enrolled` | 节点注册 |
| `runtime_node` | `agent_re_enrolled` | 节点重新注册 |
| `knowledge_sync` | `dispatch_org_upload_file` | 组织文件上传分发 |
| `knowledge_sync` | `dispatch_app_upload_file` | 应用文件上传分发 |
| `knowledge_sync` | `dispatch_org_delete_file` | 组织文件删除分发 |
| `knowledge_sync` | `dispatch_app_delete_file` | 应用文件删除分发 |
| `app_knowledge_sync` | `upload_file` | 上传文件 |
| `app_knowledge_sync` | `delete_file` | 删除文件 |
| `app_knowledge_sync` | `noop` | 同步重试（无变更）|
| `newapi_call` | `*` | fallback 到原始 action（HTTP endpoint 路径）|

## 前端设计

### 类型变更

`make web-types-gen` 重新生成后，`generated.ts` 自动增加 4 个 label 字段。`web/src/api/index.ts` 的 `AuditLog` `WithRequired` 约束同步将 4 个 label 字段加入必填列表：

```typescript
export type AuditLog = WithRequired<
  Schemas['service.AuditResult'],
  | 'id' | 'actor_role' | 'target_type' | 'target_id'
  | 'action' | 'result' | 'created_at'
  | 'action_label' | 'target_type_label' | 'actor_role_label' | 'result_label'
>
```

### 页面改动

**`AuditLogsPage.vue`**：

| 列 | 改前 | 改后 |
|---|---|---|
| 操作者 | `row.actor_role` | `row.actor_role_label` |
| 资源 | `row.target_type` | `row.target_type_label` |
| 操作 | `row.action` | `row.action_label` |
| 结果（NTag 内文字） | `row.result` | `row.result_label` |

`auditTagType()` 颜色判断继续使用原始 `row.result`，不受翻译影响。

**`AppAuditTab.vue`**：

| 列 | 改前 | 改后 |
|---|---|---|
| 操作者 | `row.actor_role` | `row.actor_role_label` |
| 操作 | `row.action` | `row.action_label` |
| 结果（NTag 内文字） | `row.result` | `row.result_label` |

## 测试

- `internal/service/audit_label_test.go`（新建）：覆盖所有 28 条 `(target_type, action)` 对、`actor_role`、`result`、`target_type` 的已知值翻译；覆盖未知值 fallback 行为。
- 无需修改现有 handler/service 测试（label 字段为只读转换，不影响写入路径）。

## 改动文件范围

| 文件 | 改动类型 |
|---|---|
| `internal/service/audit_label.go` | 新建 |
| `internal/service/audit_label_test.go` | 新建 |
| `internal/service/audit_service.go` | 新增 4 个字段 + 填充逻辑 |
| `openapi/openapi.yaml` | 重新生成 |
| `web/src/api/generated.ts` | 重新生成 |
| `web/src/api/index.ts` | WithRequired 增加 4 个 label 字段 |
| `web/src/pages/audit/AuditLogsPage.vue` | 列渲染改用 label 字段 |
| `web/src/pages/apps/AppAuditTab.vue` | 列渲染改用 label 字段 |
