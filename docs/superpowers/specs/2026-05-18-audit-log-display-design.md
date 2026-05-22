# 审计日志展示增强 设计文档

- 日期：2026-05-18
- 范围：`audit_logs` 表新增 `detail_message` 列；`AuditEvent` 写入端在各 service 内构造详情字符串；后端审计列表 SQL 通过 LEFT JOIN 取 actor / target 名称；前端审计页面列结构调整。
- 不动：路由、权限模型、写入主流程的事务边界、`metadata_json` 字段（保留现状，不再为「详情列展示」承担约定）。

## 1. 背景与目标

现状（截至 2026-05-18）：审计页面三列展示存在可读性短板。

1. **操作者**列展示 `actor_role_label`（如「组织管理员」）+ `actor_id` UUID，看不到具体是谁。
2. **资源**列展示 `target_type_label`（如「应用实例」）+ `target_id` UUID，看不到具体哪个实例 / 组织。
3. **操作**列只展示 `action_label`（如「更换模型」），看不到「从 A 换到 B」「+5000 元」这类关键上下文。

目标：让运维和组织管理员在审计列表里一眼看到「谁、对哪个具体资源、做了什么具体改动」。

## 2. 设计决定（已与用户确认）

| 决定点 | 方案 |
|---|---|
| 详情布局 | 在「操作」之后**新增独立列「详情」** |
| 操作者文案 | `display_name`，缺失时回退 `username` |
| 已删除实体 | 展示原名称 + 「已删除」徽章 |
| 操作者 / 资源辅助信息 | 名称为主 + 角色/资源类型为子标题，**UUID 隐藏为 hover** |
| 详情来源 | **写入时各 service 拼好字符串直接落库**（方案 D）；查询时直接返回，不做格式化 |
| 详情覆盖范围 | 全部 20 个 audit 写入点；当前无价值的 action（start/stop/restart/disable_api_key/restore_api_key/initialize）落空字符串或 NULL |
| 名称数据来源 | actor_name / target_name 通过后端 SQL JOIN **实时取最新名字**（与详情冻结策略不同） |
| 模型 ID 展示 | 直接展示原始 ID（`gpt-4o → claude-opus-4-7`），**不做中文映射** |
| `delete_member` 详情 | 「级联删除 N 个应用」 |
| `app.delete` 详情 | 「级联：N 个渠道绑定」（可选，视实现成本） |
| 老数据 | **不做迁移回填**；老记录 `detail_message IS NULL` → 前端展示「—」 |

## 3. 架构总览

```
写入路径
  service / handler 内构造 detail (string)
  → AuditService.Record(ctx, AuditEvent{..., DetailMessage: detail})
  → INSERT audit_logs(... detail_message)

读取路径
  HTTP GET /api/v1/organizations/{orgId}/audit-logs
  → AuditHandler.ListByOrg
  → AuditService.ListByOrg
  → AuditStore.ListAuditLogsByOrg   (SELECT detail_message 直接读 + LEFT JOIN 取 actor/target 名称)
  → toAuditResult                   (直接填字段)
  → handler.JSON
```

两个职责分离清楚：
- **detail_message**：事件发生时的快照，写入即冻结。"+5000 元，备注 vip 续费" 永远是当时的金额和备注。
- **actor_name / target_name**：始终反映最新实体名。实体被改名 / 软删除 → 通过 JOIN + `deleted_at` 字段更新展示。

## 4. 数据库变更

### Migration 000020

新增列 `detail_message TEXT NULL`，无 default 值。老行 NULL，新行写入时填字符串（空内容时落 `''` 而非 NULL，便于 sqlc 单一返回类型；选择哪种取决于实现习惯，二者前端处理一致）。

```sql
-- 000020_audit_logs_detail_message.up.sql
ALTER TABLE audit_logs ADD COLUMN detail_message text NULL;
COMMENT ON COLUMN audit_logs.detail_message IS '事件详情快照，由写入端拼装。NULL 表示无详情或老数据。';

-- 000020_audit_logs_detail_message.down.sql
ALTER TABLE audit_logs DROP COLUMN detail_message;
```

`metadata_json` 保留不动。当前已经写入 metadata 的几处（update_model 的 old/new_model_id、create 的 owner_user_id/channel_type/runtime_node_id、knowledge_sync 的 file_id 等）仍然落表，作为「结构化备份」，但**不再用于展示**——展示只看 detail_message。

## 5. 后端字段

`service.AuditEvent` 新增 1 个字段：

```go
DetailMessage string  // 写入时由 caller 拼好；空字符串 / NULL 表示无详情
```

`service.AuditResult` 新增 5 个字段（其中 ActionDetail 现在直接读自数据库列）：

```go
ActorName     string  `json:"actor_name,omitempty"`     // LEFT JOIN users → COALESCE(display_name, username)
ActorDeleted  bool    `json:"actor_deleted"`            // user.deleted_at IS NOT NULL
TargetName    string  `json:"target_name,omitempty"`    // 按 target_type 选择性 LEFT JOIN apps/users/orgs/runtime_nodes
TargetDeleted bool    `json:"target_deleted"`           // 对应实体软删除
ActionDetail  string  `json:"action_detail,omitempty"`  // 直接读 audit_logs.detail_message
```

`AuditService.Record` 把 `event.DetailMessage` 透传到 `CreateAuditLogParams.DetailMessage`，无业务逻辑。

OpenAPI 通过 swag 注释扫描，`make openapi-gen` + `make web-types-gen` 同步到前端 `AuditLog` 类型。

## 6. SQL 改造

### `audit_logs.sql` 更新

```sql
-- name: CreateAuditLog :one
INSERT INTO audit_logs (
    actor_id, actor_role, org_id,
    target_type, target_id,
    action, result, error_message,
    ip_address, metadata_json, detail_message
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11
)
RETURNING *;

-- name: ListAuditLogsByOrg :many
SELECT
  al.*,
  COALESCE(NULLIF(au.display_name, ''), au.username) AS actor_name,
  (au.deleted_at IS NOT NULL)                        AS actor_deleted,
  COALESCE(
    (SELECT a.name FROM apps a
       WHERE al.target_type = 'app' AND a.id::text = al.target_id),
    (SELECT o.name FROM organizations o
       WHERE al.target_type = 'organization' AND o.id::text = al.target_id),
    (SELECT COALESCE(NULLIF(tu.display_name, ''), tu.username) FROM users tu
       WHERE al.target_type IN ('user', 'member') AND tu.id::text = al.target_id),
    (SELECT n.name FROM runtime_nodes n
       WHERE al.target_type = 'runtime_node' AND n.id::text = al.target_id)
  ) AS target_name,
  COALESCE(
    (SELECT a.deleted_at IS NOT NULL FROM apps a
       WHERE al.target_type = 'app' AND a.id::text = al.target_id),
    (SELECT o.deleted_at IS NOT NULL FROM organizations o
       WHERE al.target_type = 'organization' AND o.id::text = al.target_id),
    (SELECT tu.deleted_at IS NOT NULL FROM users tu
       WHERE al.target_type IN ('user', 'member') AND tu.id::text = al.target_id),
    false
  ) AS target_deleted
FROM audit_logs al
LEFT JOIN users au ON au.id = al.actor_id
WHERE al.org_id = $1
ORDER BY al.created_at DESC, al.id DESC
LIMIT $2 OFFSET $3;

-- name: ListAuditLogsByTarget :many
-- 同样的 LEFT JOIN / 相关子查询结构，按 target_type + target_id 过滤
```

### 设计要点

- 相关子查询而非 4 个 LEFT JOIN：因为 `target_id` 是 text 列（多数 UUID，但 `newapi_call.target_id` 是 endpoint 字符串）。子查询里 `WHERE al.target_type = 'app' AND a.id::text = al.target_id` 通过 type 谓词短路，endpoint 字符串永远不会去和 `a.id::text` 比较，避免 cast 异常。
- 索引：`apps.id` / `organizations.id` / `users.id` / `runtime_nodes.id` 都是主键 idx scan；`audit_logs` 沿用现有 `(org_id, created_at)` 顺序索引。
- `knowledge_sync` / `app_knowledge_sync` / `newapi_call` 没有对应实体表，`target_name` 子查询全部返回 NULL → 前端 fallback 到 target_id（endpoint 字符串本身可读）。
- 用 sqlc 生成 Go 代码：query 返回结构里增加 `ActorName pgtype.Text` / `ActorDeleted bool` / `TargetName pgtype.Text` / `TargetDeleted bool` / `DetailMessage pgtype.Text` 字段。

## 7. 写入端 detail_message 构造清单

所有 audit 写入点目前都已经存在。本节列出每个写入点应该往 `event.DetailMessage` 里写什么。

构造 detail 字符串的代码贴在调用 `AuditService.Record`（或直接 `store.CreateAuditLog`）之前；helper 函数留给各 service 内部自行决定，不要求统一 helper 包。

| 写入点 | action | detail_message 模板 | 已有的本地变量 |
|---|---|---|---|
| `internal/service/app_service.go:227`（UpdateModel） | `update_model` | `fmt.Sprintf("%s → %s", oldModelID, newModelID)` | `app.ModelID` / `normalizedModelID` |
| `internal/service/onboarding_service.go:242` | `create_with_app` | `fmt.Sprintf("新建成员 %s（含应用 %s）", userDisplayName, app.Name)` | `user.DisplayName` / `user.Username` / `app.Name` |
| `internal/service/onboarding_service.go:261` | `create` | `fmt.Sprintf("归属成员 %s，渠道 %s，节点 %s", ownerLabel, channelLabel, nodeName)` | `user` / `channelType` / `input.NodeID` 对应节点 |
| `internal/service/onboarding_service.go:407` | `create_for_existing_member` | 同上 | 同上 |
| `internal/service/channel_service.go:140`（StartLogin） | `channel_auth_start` | `fmt.Sprintf("渠道 %s", channelLabel)` | `channelType` |
| `internal/worker/handlers/channel_login.go:340`（recordChannelAppAudit 调用方） | `channel_bound` | `fmt.Sprintf("渠道 %s，身份 %s", channelLabel, boundIdentity)` | `progress.ChannelName` / `progress.BoundIdentity` |
| `internal/service/runtime_operation_service.go:253`（start/stop/restart/delete/disable_api_key/restore_api_key） | `string(op)` | 空字符串 | — |
| `internal/service/runtime_operation_service.go:355`（initialize for runtime_node） | `initialize` | 空字符串 | — |
| `internal/worker/handlers/app_initialize.go:534` | `initialize` (app) | 空字符串 | — |
| `internal/service/member_service.go:336` | `delete_member` | `fmt.Sprintf("级联删除 %d 个应用", cascadeAppCount)` | 删除逻辑里已经知道这个数 |
| `internal/service/recharge_service.go:150` | `recharge` | `fmt.Sprintf("+%s 元%s", amountStr, remarkSuffix)`，remarkSuffix 形如 `"，备注 vip 续费"` 或空 | `amount` / `remark` |
| `internal/service/runtime_node_service.go:273` | `agent_enrolled` / `agent_re_enrolled` | `fmt.Sprintf("Agent 版本 %s", input.AgentVersion)`（空版本则空字符串） | `input.AgentVersion` |
| `internal/service/probe_reconciler.go:125`（audit 方法签名扩展） | `node_probe_recovered` / `node_probe_degraded` | `fmt.Sprintf("状态：%s → %s", before, after)` | `before` / `updated.Status` |
| `internal/service/knowledge_service.go:106` | `dispatch_*` | `fmt.Sprintf("文件 %s", fileName)` | 已有的 file 上下文 |
| `internal/worker/handlers/knowledge_sync.go:149` | `upload_file` / `delete_file` / `noop` | `noop` 留空；其余 `fmt.Sprintf("文件 %s", fileName)` | payload 里的 file 信息 |
| `internal/audit/newapi_audit.go:86`（RecordFailure） | endpoint | `fmt.Sprintf("HTTP %d", statusCode)`（0 则空） | `fc.Status` |

### 可选改造

- `app.delete` detail 里附 `级联：N 个渠道绑定`：需要 `RuntimeOperationService` 在写 audit 前查 `channel_bindings` 计数。若实现简单可纳入，否则推迟。

### 约定

- detail_message 是 **纯展示文本**，不用 markdown / html，不出现 UUID（UUID 通过 actor/target 列的 hover 暴露）。
- 模型 ID / channel 标识 / endpoint 等英文标识符直接出现在文案里（按 §2 模型 ID 不做中文映射的决定）。
- channel_type 例外：值（`wechat` 等）→ 中文标签由 `internal/domain` 现有的 channel label 函数提供（如果有），否则在写入点直接写英文也可接受（保持简单）。
- 缺失字段：如果一个 detail 模板的某个变量为空，就把该片段裁掉，不写 `<nil>` / `unknown`。

## 8. 前端列结构

### 8.1 `AuditLogsPage.vue`

| 列 | 渲染 |
|---|---|
| 时间 | 沿用 `timeColumn` |
| 操作者 | 主文 `actor_name`（fallback `actor_id` 后 8 位）+ 小字 `actor_role_label`；`actor_deleted` 时加 `n-tag size=tiny` 「已删除」；hover 显示完整 UUID |
| 资源 | 主文 `target_name`（fallback `target_id`）+ 小字 `target_type_label`；`target_deleted` 时加「已删除」徽章；hover 显示完整 UUID；`target_name` 为空且 `target_id` 是 endpoint 字符串（newapi_call 等）时直接展示 endpoint 字符串 |
| 操作 | `action_label`（沿用） |
| 详情 | `action_detail`；空 / NULL 展示「—」灰色字；`min-width: 240px`，允许换行 |
| 结果 | `result_label` + 错误信息（沿用） |

### 8.2 `AppAuditTab.vue`

不展示「资源」列（聚焦单 app）；其余同上。

### 8.3 细节

- `actor_role = system` 行：`actor_name` 空，主文展示「系统」，无副文，无 UUID hover。
- UUID hover 用 `n-tooltip` 包裹 cell 内容；不展开为永久 `<small>`。
- 类型通过 `make web-types-gen` 自动生成；columns 改造在 `.vue` 文件内完成，hook (`useAuditLogs.ts`) 不动。

## 9. 兼容性与边界

| 场景 | 处理 |
|---|---|
| 老 audit_logs 行（升级前） | `detail_message IS NULL` → 前端「—」；不做数据迁移回填 |
| actor 用户被物理删除 / actor_id 为空 | LEFT JOIN 返回 NULL → `actor_name` 空 → 主文 fallback 到 UUID 后 8 位或角色文案 |
| target 资源被物理删除 | 子查询返回 NULL → `target_name` 空 → 主文 fallback 到 target_id |
| `target_type = newapi_call` 等无对应实体 | 子查询全部走不到 → `target_name` 空 → 前端展示 endpoint 字符串本身 |
| 实体被改名 | actor_name / target_name 实时反映新名（JOIN）；detail_message 保持事件发生时的内容 |
| 实体被软删除（`deleted_at` 非空） | 名称仍展示（JOIN 不 filter deleted_at）+ 「已删除」徽章 |
| detail 字符串含特殊字符 | 前端纯文本渲染（非 v-html），无 XSS |
| `users.deleted_at` 语义 | 软下线 = 「已删除」徽章（与 apps/orgs 的真删除统一展示，运维语境下下线 ≈ 不再活跃） |

**不做**：
- 不做老数据 detail_message 回填
- 不做模型 ID → 中文名映射表
- 不引入新的 target_type / action
- 不动权限谓词（`auth.CanViewOrg` / `CanViewAppAudit`）
- `metadata_json` 现有写入逻辑保持不动，不专为详情列增删字段

## 10. 测试

### 10.1 后端

- **`internal/service/audit_service_test.go`**（扩展）：
  - `Record` 成功路径：DetailMessage 字段被透传到 store
  - `ListByOrg` / `ListByTarget` 断言响应里 `ActorName` / `ActorDeleted` / `TargetName` / `TargetDeleted` / `ActionDetail` 五个字段；fake store 返回带名称、deleted 标记和 detail_message 的 row。
- **写入端 service 测试**（断言传给 store 的 `DetailMessage` 字符串内容）：
  - `recharge_service_test.go`（金额 + 备注 / 金额无备注 / 失败路径）
  - `member_service_test.go`（cascade_app_count = 0 / 1 / 多）
  - `app_service_test.go`（update_model 的 detail 文案）
  - `channel_service_test.go`（channel_auth_start）
  - `onboarding_service_test.go`（create_with_app / create / create_for_existing_member）
  - `runtime_node_service.go` 的相关测试（agent_enrolled / agent_re_enrolled 的版本号有 / 无）
  - `probe_reconciler_test.go`（before/after 状态切换）
  - `worker/handlers/channel_login_test.go`（channel_bound 的 detail）
  - `worker/handlers/knowledge_sync_test.go`（detail 与 noop 的空字符串）
- **SQL 集成测试**（新建或并入现有 pg 测试桩）：插入 target_type=app/organization/user/runtime_node/newapi_call 五种 row 加 detail_message，断言列表返回各字段，且 newapi_call 行不触发 cast error。
- **Migration 测试**：现有 `migrations_test.go` 自动覆盖 up/down 双向迁移，确保新 migration 可逆。

### 10.2 前端

- `AuditLogsPage.vue` / `AppAuditTab.vue` Vitest 组件测试：
  - 普通行：actor_name + actor_role_label 渲染、UUID hover 触发
  - 已删除行：「已删除」徽章存在
  - 详情列：非空字符串渲染；空字符串 / null 渲染「—」
  - system 行：「系统」主文，无副文，无 hover

### 10.3 浏览器手工验证（按 CLAUDE.md「交付前检查」要求）

- 组织管理员视角：触发 update_model / recharge / delete_member 后审计页详情列正确
- 平台管理员视角：跨组织审计页同上
- 应用详情审计 Tab：操作者列展示成员名称
- 含已删除应用的审计行：「已删除」徽章渲染

## 11. 改动文件清单

**数据库**

- `internal/migrations/000020_audit_logs_detail_message.up.sql`（新建）
- `internal/migrations/000020_audit_logs_detail_message.down.sql`（新建）

**后端**

- `internal/store/queries/audit_logs.sql`（CreateAuditLog 加列；ListAuditLogsByOrg / ListAuditLogsByTarget 加 JOIN）→ `sqlc generate` 重生成 `audit_logs.sql.go` / `querier.go` / `models.go`
- `internal/service/audit_service.go`：`AuditEvent` 加 `DetailMessage`；`Record` 透传；`AuditResult` 加 5 字段；`toAuditResult` 填充
- 写入端补 detail（按 §7 清单）：
  - `internal/service/app_service.go`（update_model）
  - `internal/service/onboarding_service.go`（create_with_app / create / create_for_existing_member）
  - `internal/service/channel_service.go`（channel_auth_start）
  - `internal/worker/handlers/channel_login.go`（channel_bound）
  - `internal/service/runtime_operation_service.go`（start/stop/restart/delete/disable_api_key/restore_api_key 落空字符串；initialize 同）
  - `internal/worker/handlers/app_initialize.go`（initialize 落空字符串）
  - `internal/service/member_service.go`（delete_member）
  - `internal/service/recharge_service.go`（recharge）
  - `internal/service/runtime_node_service.go`（agent_enrolled / re_enrolled）
  - `internal/service/probe_reconciler.go`（node_probe_*）
  - `internal/service/knowledge_service.go`（dispatch_*）
  - `internal/worker/handlers/knowledge_sync.go`（upload_file / delete_file / noop）
  - `internal/audit/newapi_audit.go`（newapi 失败）
- 对应的 `_test.go` 文件
- 触发 `make openapi-gen` 同步 `openapi/openapi.yaml`

**前端**

- 触发 `make web-types-gen` 同步 `web/src/api/generated.ts`
- `web/src/pages/audit/AuditLogsPage.vue`（columns 改造 + 详情列）
- `web/src/pages/apps/AppAuditTab.vue`（操作者列改造 + 详情列）
- 对应的 Vitest 测试

## 12. 提交拆分

按 CLAUDE.md commit 规范，拆三个独立提交：

1. **`feat(audit): 新增 detail_message 列与名称查询`**
   - migration 000020
   - audit_logs.sql 三条 query 改造 + sqlc 生成
   - AuditEvent / AuditResult 字段扩展 + Record 透传 + toAuditResult 填充
   - audit_service 单测
   - openapi-gen
2. **`feat(audit): 各写入点构造详情字符串`**
   - §7 清单里的各 service 和 worker handler 改造
   - 对应单测扩展
3. **`feat(web): 审计页展示操作者/资源名称与操作详情`**
   - web-types-gen
   - AuditLogsPage / AppAuditTab columns 改造
   - Vitest

## 13. 交付前自检（按 CLAUDE.md）

- `make openapi-check` 工作区干净
- `go test ./...` 通过（含 migrations_test 的 up/down 验证）
- `pnpm test` + `pnpm typecheck` 通过
- 浏览器手工验证审计页四种典型场景（见 10.3）
- 无密钥 / 临时调试代码 / 无关文件改动
