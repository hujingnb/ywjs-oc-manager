# 审计日志展示增强 实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 审计列表的「操作者 / 资源 / 操作」三列展示具体名称和详情，UUID 仅在 hover 出现；详情由写入端冻结落库。

**Architecture:** 加列 `audit_logs.detail_message`，14 个 audit 写入点在调用前 `fmt.Sprintf` 拼好字符串塞进 `AuditEvent.DetailMessage`；List 查询通过 LEFT JOIN（users）+ 相关子查询（apps/orgs/users/runtime_nodes）取实时名称与软删除标记。前端用 `n-tooltip` 包裹名称列，UUID 隐藏到 hover。

**Tech Stack:** Go 1.22+, sqlc v1.30, pgx/v5, golang-migrate（embed.FS）, testify/require, Vue 3 + Naive UI, Vitest, swag（OpenAPI 注解扫描）。

**Spec:** `docs/superpowers/specs/2026-05-18-audit-log-display-design.md`

---

## 文件结构

按职责分组，本次 14 个写入点改造分散在多文件，但每文件改动很局部。

**新建：**
- `internal/migrations/000020_audit_logs_detail_message.up.sql` — 加 `detail_message text NULL` 列 + COMMENT
- `internal/migrations/000020_audit_logs_detail_message.down.sql` — 删列

**修改（后端核心）：**
- `sqlc.yaml` — schema 列表加入新 migration
- `internal/store/queries/audit_logs.sql` — CreateAuditLog 加列；ListAuditLogsByOrg / ListAuditLogsByTarget 重写为 JOIN/子查询
- `internal/store/sqlc/audit_logs.sql.go` / `models.go` / `querier.go` — sqlc 重生成
- `internal/service/audit_service.go` — `AuditEvent.DetailMessage` 字段；`Record` 透传；`AuditResult` 加 5 字段；新增 `toAuditResultFromOrgRow` / `toAuditResultFromTargetRow`；`AuditStore` 接口签名变更

**修改（后端 14 个写入点）：**
- `internal/service/app_service.go` — `update_model` 详情
- `internal/service/onboarding_service.go` — `create_with_app` / `create` / `create_for_existing_member` 详情
- `internal/service/channel_service.go` — `channel_auth_start` 详情
- `internal/worker/handlers/channel_login.go` — `channel_bound` 详情（在 `recordChannelAppAudit` 调用处）
- `internal/service/runtime_operation_service.go` — start/stop/restart/delete/disable_api_key/restore_api_key/initialize 落空字符串
- `internal/worker/handlers/app_initialize.go` — `initialize` 落空字符串
- `internal/service/member_service.go` — `delete_member` 详情（级联应用数）
- `internal/service/recharge_service.go` — `recharge` 详情（金额 + 备注）
- `internal/service/runtime_node_service.go` — `agent_enrolled` / `agent_re_enrolled` 详情（agent 版本）
- `internal/service/probe_reconciler.go` — `audit` helper 加 detail 参数；`node_probe_recovered/degraded` 详情
- `internal/service/knowledge_service.go` — dispatch_* 详情
- `internal/worker/handlers/knowledge_sync.go` — upload_file / delete_file 详情
- `internal/audit/newapi_audit.go` — newapi 失败详情（HTTP 状态码）

**修改（测试）：**
- `internal/service/audit_service_test.go` — 扩展 stub 适配新接口签名 + 断言 5 新字段
- 各写入点 `_test.go` — 断言 `DetailMessage` 内容
- `internal/migrations/migrations_test.go` — 现有的 up/down 配对测试自动覆盖新 migration

**修改（前端）：**
- `web/src/api/generated.ts` — `make web-types-gen` 重生成
- `web/src/pages/audit/AuditLogsPage.vue` — 操作者/资源列改造 + 新增详情列
- `web/src/pages/apps/AppAuditTab.vue` — 操作者列改造 + 新增详情列
- 对应 Vitest 测试

---

## Commit 1：新增 detail_message 列与名称查询

目标：完成数据库、查询、service 类型的所有基础改造，但 `AuditEvent.DetailMessage` 字段始终是空字符串（写入点改造放到 Commit 2）。这个 commit 后系统行为不变（详情列展示「—」，actor_name / target_name 已经能正确展示）。

### Task 1.1：新建 migration 000020

**Files:**
- Create: `internal/migrations/000020_audit_logs_detail_message.up.sql`
- Create: `internal/migrations/000020_audit_logs_detail_message.down.sql`

- [ ] **Step 1: 写 up migration**

`internal/migrations/000020_audit_logs_detail_message.up.sql`:

```sql
-- audit_logs 加 detail_message 列，存写入端冻结的详情字符串。
-- NULL 表示无详情或老数据；前端展示「—」。
ALTER TABLE audit_logs ADD COLUMN detail_message text NULL;
COMMENT ON COLUMN audit_logs.detail_message IS '事件详情快照，由写入端拼装。NULL 表示无详情或老数据。';
```

- [ ] **Step 2: 写 down migration**

`internal/migrations/000020_audit_logs_detail_message.down.sql`:

```sql
ALTER TABLE audit_logs DROP COLUMN detail_message;
```

- [ ] **Step 3: 把新 migration 加入 sqlc.yaml**

编辑 `sqlc.yaml`，在 schema 列表末尾追加一行：

```yaml
      - internal/migrations/000019_app_runtime_image.up.sql
      - internal/migrations/000020_audit_logs_detail_message.up.sql
    queries: internal/store/queries
```

- [ ] **Step 4: 验证 migration 文件对**

Run: `go test ./internal/migrations/...`
Expected: PASS（`TestFS_ContainsUpAndDownPairs` 验证 up/down 配对）

### Task 1.2：更新 SQL 查询

**Files:**
- Modify: `internal/store/queries/audit_logs.sql`

- [ ] **Step 1: 重写 audit_logs.sql 三条 query**

完整替换 `internal/store/queries/audit_logs.sql` 内容：

```sql
-- name: CreateAuditLog :one
INSERT INTO audit_logs (
    actor_id,
    actor_role,
    org_id,
    target_type,
    target_id,
    action,
    result,
    error_message,
    ip_address,
    metadata_json,
    detail_message
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11
)
RETURNING *;

-- name: ListAuditLogsByOrg :many
-- 返回审计行 + actor 实时名称 + target 实时名称（按 target_type 走子查询）。
-- 子查询里 WHERE al.target_type = X 保证 newapi_call 的 endpoint 字符串
-- 永不被尝试转 UUID，避开 cast error。
SELECT
    al.id,
    al.actor_id,
    al.actor_role,
    al.org_id,
    al.target_type,
    al.target_id,
    al.action,
    al.result,
    al.error_message,
    al.ip_address,
    al.metadata_json,
    al.created_at,
    al.detail_message,
    COALESCE(NULLIF(au.display_name, ''), au.username)              AS actor_name,
    COALESCE(au.deleted_at IS NOT NULL, false)                       AS actor_deleted,
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
-- 同 ListAuditLogsByOrg，按 target_type + target_id 过滤。
SELECT
    al.id,
    al.actor_id,
    al.actor_role,
    al.org_id,
    al.target_type,
    al.target_id,
    al.action,
    al.result,
    al.error_message,
    al.ip_address,
    al.metadata_json,
    al.created_at,
    al.detail_message,
    COALESCE(NULLIF(au.display_name, ''), au.username)              AS actor_name,
    COALESCE(au.deleted_at IS NOT NULL, false)                       AS actor_deleted,
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
WHERE al.target_type = $1 AND al.target_id = $2
ORDER BY al.created_at DESC, al.id DESC
LIMIT $3 OFFSET $4;
```

### Task 1.3：sqlc 重生成

**Files:**
- Modify: `internal/store/sqlc/audit_logs.sql.go`
- Modify: `internal/store/sqlc/models.go`
- Modify: `internal/store/sqlc/querier.go`

- [ ] **Step 1: 跑 sqlc generate**

Run: `make sqlc-generate`
Expected: 命令成功；`git status` 看到 `audit_logs.sql.go`、`models.go`、`querier.go` 改动；`AuditLog` 结构体新增 `DetailMessage pgtype.Text` 字段；新增 `ListAuditLogsByOrgRow` 和 `ListAuditLogsByTargetRow` 结构体。

- [ ] **Step 2: 确认新生成的 row 结构**

Run: `grep -n "ListAuditLogsByOrgRow\|ListAuditLogsByTargetRow\|DetailMessage" internal/store/sqlc/audit_logs.sql.go internal/store/sqlc/models.go`

Expected: 两个 Row 结构体存在，字段包括 `ID`、`ActorID`、`ActorRole`、`OrgID`、`TargetType`、`TargetID`、`Action`、`Result`、`ErrorMessage`、`IpAddress`、`MetadataJson`、`CreatedAt`、`DetailMessage`、`ActorName`（pgtype.Text）、`ActorDeleted`（bool 或 *bool）、`TargetName`（pgtype.Text 或 interface{}）、`TargetDeleted`（bool 或 interface{}）。

> 备注：sqlc 对 COALESCE 表达式可能推断为 `interface{}` 类型。若推断不稳，后续 Task 1.4 的代码片段会通过类型断言（`.(bool)` / `.(string)`）转换；如果 sqlc 推断为具体类型，直接用即可。

### Task 1.4：扩展 AuditService 类型与转换函数

**Files:**
- Modify: `internal/service/audit_service.go`

- [ ] **Step 1: 改 AuditStore 接口签名**

把 `internal/service/audit_service.go` 中 `AuditStore` 接口替换为：

```go
// AuditStore 抽象审计日志的数据访问能力。
// ListAuditLogsByOrg / ListAuditLogsByTarget 由于 SELECT 含计算列，
// sqlc 为它们生成独立的 *Row 结构体；CreateAuditLog 仍然返回 sqlc.AuditLog。
type AuditStore interface {
	CreateAuditLog(ctx context.Context, arg sqlc.CreateAuditLogParams) (sqlc.AuditLog, error)
	GetApp(ctx context.Context, id pgtype.UUID) (sqlc.App, error)
	ListAuditLogsByOrg(ctx context.Context, arg sqlc.ListAuditLogsByOrgParams) ([]sqlc.ListAuditLogsByOrgRow, error)
	ListAuditLogsByTarget(ctx context.Context, arg sqlc.ListAuditLogsByTargetParams) ([]sqlc.ListAuditLogsByTargetRow, error)
}
```

- [ ] **Step 2: 给 AuditEvent 加 DetailMessage 字段**

定位到 `AuditEvent` struct，加字段：

```go
// AuditEvent 是其他服务记录审计时的入参。
// service 层在执行写操作后调用 AuditService.Record，将操作主体、目标和结果统一落库。
type AuditEvent struct {
	ActorID       string
	ActorRole     string
	OrgID         string
	TargetType    string
	TargetID      string
	Action        string
	Result        string
	ErrorMessage  string
	IPAddress     string
	Metadata      map[string]any
	// DetailMessage 由调用方拼好的中文短句；写入即冻结，查询时直接返回。
	// 空字符串表示无详情，前端展示「—」。
	DetailMessage string
}
```

- [ ] **Step 3: 给 AuditResult 加 5 字段**

替换 `AuditResult` struct 定义：

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
	// 以下为展示用翻译字段，由 toAuditResult*() 填充，未知值 fallback 到原始字符串。
	ActionLabel     string `json:"action_label"`
	TargetTypeLabel string `json:"target_type_label"`
	ActorRoleLabel  string `json:"actor_role_label"`
	ResultLabel     string `json:"result_label"`
	// ActorName 是 actor_id 对应用户的 display_name fallback username。
	// 写入时不取，查询时通过 LEFT JOIN 实时填充；空字符串表示无 actor / actor 已物理删除。
	ActorName string `json:"actor_name,omitempty"`
	// ActorDeleted 表示 actor 对应用户已被软删除（users.deleted_at 非空，本项目即「下线」）。
	ActorDeleted bool `json:"actor_deleted"`
	// TargetName 是 target_id 对应资源名称；按 target_type 走相关子查询，
	// 对 newapi_call / knowledge_sync 等无对应实体的类型返回空字符串。
	TargetName string `json:"target_name,omitempty"`
	// TargetDeleted 表示目标资源对应实体已软删除。
	TargetDeleted bool `json:"target_deleted"`
	// ActionDetail 是写入时冻结的详情字符串，直接读自 audit_logs.detail_message 列。
	// 空字符串表示无详情，前端展示「—」。
	ActionDetail string `json:"action_detail,omitempty"`
}
```

- [ ] **Step 4: 让 Record 透传 DetailMessage**

在 `Record` 方法内，定位 `params := sqlc.CreateAuditLogParams{...}` 块，在赋值结束后（即「Metadata 序列化」之前或之后均可，注意紧邻 store.CreateAuditLog 调用之前）追加：

```go
if event.DetailMessage != "" {
    params.DetailMessage = pgtype.Text{String: event.DetailMessage, Valid: true}
}
```

> sqlc 重新生成后 `CreateAuditLogParams` 应该已有 `DetailMessage pgtype.Text` 字段。如果 sqlc 把它生成为 `*string`，改成 `params.DetailMessage = &event.DetailMessage`（空串也写入空串，与上面 if 等价）。

- [ ] **Step 5: 重写 toAuditResult 系列函数**

完整替换文件末尾的 `toAuditResults` / `toAuditResult`，新增三个函数：

```go
// toAuditResult 把 INSERT 路径返回的 sqlc.AuditLog 转成 AuditResult。
// 写入路径没有 JOIN，所以 ActorName / TargetName / *Deleted 全部留空；
// ActionDetail 直接读 detail_message。
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
	if row.DetailMessage.Valid {
		result.ActionDetail = row.DetailMessage.String
	}
	return result
}

// toAuditResultsFromOrgRows 转换 ListAuditLogsByOrg 的查询行。
func toAuditResultsFromOrgRows(rows []sqlc.ListAuditLogsByOrgRow) []AuditResult {
	results := make([]AuditResult, 0, len(rows))
	for _, row := range rows {
		results = append(results, toAuditResultFromOrgRow(row))
	}
	return results
}

// toAuditResultFromOrgRow 把 ListAuditLogsByOrgRow 转 AuditResult。
// 因为 row 字段与 sqlc.AuditLog 同名前缀，所以先组合一个 AuditLog 复用 toAuditResult，
// 再覆盖 actor / target 名称与软删除标记。
func toAuditResultFromOrgRow(row sqlc.ListAuditLogsByOrgRow) AuditResult {
	base := toAuditResult(sqlc.AuditLog{
		ID:            row.ID,
		ActorID:       row.ActorID,
		ActorRole:     row.ActorRole,
		OrgID:         row.OrgID,
		TargetType:    row.TargetType,
		TargetID:      row.TargetID,
		Action:        row.Action,
		Result:        row.Result,
		ErrorMessage:  row.ErrorMessage,
		IpAddress:     row.IpAddress,
		MetadataJson:  row.MetadataJson,
		CreatedAt:     row.CreatedAt,
		DetailMessage: row.DetailMessage,
	})
	applyNameColumns(&base, row.ActorName, row.ActorDeleted, row.TargetName, row.TargetDeleted)
	return base
}

// toAuditResultsFromTargetRows / toAuditResultFromTargetRow 同上，复用 applyNameColumns。
func toAuditResultsFromTargetRows(rows []sqlc.ListAuditLogsByTargetRow) []AuditResult {
	results := make([]AuditResult, 0, len(rows))
	for _, row := range rows {
		results = append(results, toAuditResultFromTargetRow(row))
	}
	return results
}

func toAuditResultFromTargetRow(row sqlc.ListAuditLogsByTargetRow) AuditResult {
	base := toAuditResult(sqlc.AuditLog{
		ID:            row.ID,
		ActorID:       row.ActorID,
		ActorRole:     row.ActorRole,
		OrgID:         row.OrgID,
		TargetType:    row.TargetType,
		TargetID:      row.TargetID,
		Action:        row.Action,
		Result:        row.Result,
		ErrorMessage:  row.ErrorMessage,
		IpAddress:     row.IpAddress,
		MetadataJson:  row.MetadataJson,
		CreatedAt:     row.CreatedAt,
		DetailMessage: row.DetailMessage,
	})
	applyNameColumns(&base, row.ActorName, row.ActorDeleted, row.TargetName, row.TargetDeleted)
	return base
}

// applyNameColumns 将 List 查询行的名称 / 软删除标记字段写入 AuditResult。
// 处理 sqlc 对 COALESCE 列的可能两种类型推断：pgtype.Text/bool 或 interface{}。
func applyNameColumns(r *AuditResult, actorName any, actorDeleted any, targetName any, targetDeleted any) {
	r.ActorName = stringFromColumn(actorName)
	r.ActorDeleted = boolFromColumn(actorDeleted)
	r.TargetName = stringFromColumn(targetName)
	r.TargetDeleted = boolFromColumn(targetDeleted)
}

// stringFromColumn 兼容 sqlc 把 COALESCE 列推断为 pgtype.Text、*string 或 interface{} 的三种情况。
func stringFromColumn(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case *string:
		if t == nil {
			return ""
		}
		return *t
	case pgtype.Text:
		if t.Valid {
			return t.String
		}
		return ""
	}
	return ""
}

// boolFromColumn 兼容 sqlc 对布尔 COALESCE 列的类型推断。
func boolFromColumn(v any) bool {
	switch t := v.(type) {
	case nil:
		return false
	case bool:
		return t
	case *bool:
		if t == nil {
			return false
		}
		return *t
	}
	return false
}
```

> 说明：`applyNameColumns` 把入参类型写成 `any` 是为了让代码在 sqlc 不同类型推断下都不需要再改；如果跑完 sqlc-generate 后看到 row 字段类型是具体的 `pgtype.Text` / `bool`，把 `applyNameColumns` 入参类型改为具体类型并去掉 stringFromColumn / boolFromColumn 即可——保持 helper 也能工作。

- [ ] **Step 6: 改 ListByOrg / ListByTarget 调用新转换函数**

在 `ListByOrg` 方法末尾：

```go
return toAuditResultsFromOrgRows(rows), nil
```

替换原 `return toAuditResults(rows), nil`。

在 `ListByTarget` 方法体内（取 results 之前的位置），把原来的：

```go
results := toAuditResults(rows)
```

改成：

```go
results := toAuditResultsFromTargetRows(rows)
```

- [ ] **Step 7: 删除旧的 toAuditResults**

`toAuditResults`（接 `[]sqlc.AuditLog`）已无调用点，删除该函数避免死代码。

- [ ] **Step 8: 编译通过性检查**

Run: `go build ./...`
Expected: 编译成功，无错误。

### Task 1.5：扩展 audit_service_test.go 适配新接口

**Files:**
- Modify: `internal/service/audit_service_test.go`

- [ ] **Step 1: 改 auditStoreStub 接口实现**

把 `auditStoreStub` 的 `ListAuditLogsByOrg` / `ListAuditLogsByTarget` 改成新签名：

```go
type auditStoreStub struct {
	created   sqlc.CreateAuditLogParams
	byOrg     []sqlc.ListAuditLogsByOrgRow
	byTarget  []sqlc.ListAuditLogsByTargetRow
	lastByOrg sqlc.ListAuditLogsByOrgParams
	apps      map[string]sqlc.App
}

func (s *auditStoreStub) ListAuditLogsByOrg(_ context.Context, arg sqlc.ListAuditLogsByOrgParams) ([]sqlc.ListAuditLogsByOrgRow, error) {
	s.lastByOrg = arg
	return s.byOrg, nil
}

func (s *auditStoreStub) ListAuditLogsByTarget(_ context.Context, _ sqlc.ListAuditLogsByTargetParams) ([]sqlc.ListAuditLogsByTargetRow, error) {
	return s.byTarget, nil
}
```

- [ ] **Step 2: 改现有用到 sqlc.AuditLog 的测试用例**

`TestAuditServiceListByTargetFiltersOrgScope` 和 `TestAuditServiceListByTargetAllowsMemberOwnApp` 里 byTarget 用的是 `sqlc.AuditLog`，改成 `sqlc.ListAuditLogsByTargetRow`，给字段对齐。例如 `TestAuditServiceListByTargetFiltersOrgScope`：

```go
byTarget: []sqlc.ListAuditLogsByTargetRow{
    {TargetType: "app", TargetID: testAuditAppID, OrgID: mustOptionalUUID(t, testOrgID)},  // 场景：目标应用所属组织内的审计记录应允许返回。
    {TargetType: "app", TargetID: testAuditAppID, OrgID: mustOptionalUUID(t, testOrg2ID)}, // 场景：跨组织同目标审计记录用于验证组织范围过滤。
},
```

`TestAuditServiceListByTargetAllowsMemberOwnApp` 同理。

- [ ] **Step 3: 加新测试覆盖新字段**

在文件末尾追加：

```go
// TestAuditServiceListByOrgPopulatesNameColumns 验证审计列表查询返回 actor / target 名称、软删除标记和详情字符串的预期行为场景。
func TestAuditServiceListByOrgPopulatesNameColumns(t *testing.T) {
	// 场景：actor / target 名称、软删除标记、详情字符串均被透传到 AuditResult。
	store := &auditStoreStub{
		byOrg: []sqlc.ListAuditLogsByOrgRow{
			{
				TargetType:    "app",
				TargetID:      testAuditAppID,
				OrgID:         mustOptionalUUID(t, testOrgID),
				ActorRole:     domain.UserRoleOrgAdmin,
				DetailMessage: pgtype.Text{String: "gpt-4o → claude-opus-4-7", Valid: true},
				ActorName:     pgtype.Text{String: "张三", Valid: true},
				ActorDeleted:  false,
				TargetName:    pgtype.Text{String: "客服小助手", Valid: true},
				TargetDeleted: true,
			},
		},
	}
	svc := NewAuditService(store)

	results, err := svc.ListByOrg(context.Background(), platformAdmin(), testOrgID, 0, 0)
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, "张三", results[0].ActorName)
	require.False(t, results[0].ActorDeleted)
	require.Equal(t, "客服小助手", results[0].TargetName)
	require.True(t, results[0].TargetDeleted)
	require.Equal(t, "gpt-4o → claude-opus-4-7", results[0].ActionDetail)
}

// TestAuditServiceRecordPersistsDetailMessage 验证 Record 把 DetailMessage 透传到 CreateAuditLog。
func TestAuditServiceRecordPersistsDetailMessage(t *testing.T) {
	// 场景：写入端用 DetailMessage 字段时，落库参数携带相同字符串。
	store := &auditStoreStub{}
	svc := NewAuditService(store)

	_, err := svc.Record(context.Background(), AuditEvent{
		ActorRole:     domain.UserRolePlatformAdmin,
		TargetType:    "organization",
		TargetID:      "00000000-0000-0000-0000-000000000101",
		Action:        "recharge",
		Result:        "succeeded",
		DetailMessage: "+5000.00 元，备注 vip 续费",
	})
	require.NoError(t, err)
	require.True(t, store.created.DetailMessage.Valid)
	require.Equal(t, "+5000.00 元，备注 vip 续费", store.created.DetailMessage.String)
}
```

> 注：测试里 `pgtype.Text` 字段的写法假定 sqlc 把 `actor_name` 和 `target_name` 推断成 `pgtype.Text`、把 `actor_deleted` / `target_deleted` 推断成 `bool`。若实际生成的类型不同（如 `interface{}`），把测试里的 row 字段改成对应类型字面量（如 `ActorName: "张三"`，`ActorDeleted: false`）。

- [ ] **Step 4: 跑测试**

Run: `go test ./internal/service/ -run TestAuditService -v`
Expected: 全部 PASS（含新增的两条）。

### Task 1.6：SQL 集成测试（可选但建议）

**Files:**
- Create: `internal/store/audit_logs_integration_test.go`

可选 task：验证新 SQL 在 target_type=newapi_call 等非 UUID target_id 场景下不会 cast 异常，并实际触发 actor / target JOIN。该 task 跑在 `//go:build integration` 标签下，对应 CI / 本地需要 `INTEGRATION_DATABASE_URL` 才执行；时间紧时可以跳过，依赖 Task 3.5 浏览器手工验证兜底。

- [ ] **Step 1: 新建集成测试**

```go
//go:build integration

// Package store 的集成测试：验证审计日志列表 SQL 在多种 target_type 下都能正确取名 / 软删除标记，
// 特别校验 newapi_call 这种 target_id 是 endpoint 字符串的场景不触发 UUID cast 异常。
package store

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"

	"oc-manager/internal/store/sqlc"
)

// TestAuditLogsList_HandlesMixedTargetTypes 验证审计列表查询在 app / organization / newapi_call 三种 target_type 上都能返回结果，
// 且 newapi_call 行（target_id 为 HTTP endpoint 字符串）不会触发 UUID cast 异常。
func TestAuditLogsList_HandlesMixedTargetTypes(t *testing.T) {
	dsn := os.Getenv("INTEGRATION_DATABASE_URL")
	if dsn == "" {
		t.Skip("缺 INTEGRATION_DATABASE_URL")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	st, err := Open(ctx, dsn)
	require.NoError(t, err)
	defer st.Close()
	q := sqlc.New(st.Pool())

	// 准备一条组织、一条 app、一条用户作为目标实体
	// 因为是 integration test，可以直接 INSERT；具体 ID 用 gen_random_uuid()
	// 或者复用 seed-e2e 已经准备的数据（项目里有 cmd/seed-e2e/main.go 可参考）。
	// 这里不展开 seed 逻辑，假设环境 INTEGRATION_DATABASE_URL 指向已 seed 的库。

	// 写一条 newapi_call 审计（target_id 是 endpoint 字符串）
	_, err = q.CreateAuditLog(ctx, sqlc.CreateAuditLogParams{
		ActorRole:     "system",
		TargetType:    "newapi_call",
		TargetID:      "POST /api/user/",
		Action:        "POST /api/user/",
		Result:        "failed",
		DetailMessage: pgtype.Text{String: "HTTP 500", Valid: true},
	})
	require.NoError(t, err)

	// 触发 ListAuditLogsByTarget，断言不报错且 detail_message 被取回
	rows, err := q.ListAuditLogsByTarget(ctx, sqlc.ListAuditLogsByTargetParams{
		TargetType: "newapi_call",
		TargetID:   "POST /api/user/",
		Limit:      10,
		Offset:     0,
	})
	require.NoError(t, err)
	require.NotEmpty(t, rows)
	require.Equal(t, "HTTP 500", rows[0].DetailMessage.String)
}
```

- [ ] **Step 2: 跑集成测试（可选）**

Run: `INTEGRATION_DATABASE_URL=postgres://manager:manager@localhost:5432/manager?sslmode=disable go test -tags=integration ./internal/store/...`
Expected: PASS（若本地没起 manager-postgres 跳过即可）。

### Task 1.7：重生成 OpenAPI + 提交 Commit 1

**Files:**
- Modify: `openapi/openapi.yaml`

- [ ] **Step 1: 生成 OpenAPI yaml**

Run: `make openapi-gen`
Expected: `openapi/openapi.yaml` 文件被更新；`git diff openapi/openapi.yaml` 看到 `AuditLog` schema 新增 `actor_name` / `actor_deleted` / `target_name` / `target_deleted` / `action_detail` 字段。

- [ ] **Step 2: 把生成产物加入暂存**

Run: `git add internal/migrations/000020_audit_logs_detail_message.up.sql internal/migrations/000020_audit_logs_detail_message.down.sql sqlc.yaml internal/store/queries/audit_logs.sql internal/store/sqlc/audit_logs.sql.go internal/store/sqlc/models.go internal/store/sqlc/querier.go internal/service/audit_service.go internal/service/audit_service_test.go openapi/openapi.yaml`

如果 Task 1.6 集成测试也写了：`git add internal/store/audit_logs_integration_test.go`

- [ ] **Step 3: 提交**

```bash
git commit -m "$(cat <<'EOF'
feat(audit): 列表返回操作者/资源名称与详情字段

audit_logs 新增 detail_message 列存事件详情快照。

ListAuditLogsByOrg / ListAuditLogsByTarget 通过 LEFT JOIN users 与按 target_type 短路的相关子查询取实时名称与软删除标记；AuditResult 新增 actor_name / actor_deleted / target_name / target_deleted / action_detail 五个字段对外暴露。本提交只完成基础设施，所有写入点暂未填 DetailMessage。
EOF
)"
```

- [ ] **Step 4: 校验生成产物干净**

Run: `make openapi-check`
Expected: `✅ openapi.yaml 与代码同步`

---

## Commit 2：各写入点构造详情字符串

目标：14 个写入点全部填上 `DetailMessage`。每个 task 独立、可单独跑测试；最后一起 commit。

### Task 2.1：app_service.go UpdateModel 详情

**Files:**
- Modify: `internal/service/app_service.go:227`
- Modify: `internal/service/app_service_test.go`

- [ ] **Step 1: 先写失败的测试**

在 `internal/service/app_service_test.go` 中找到验证 UpdateModel 写 audit 的测试用例（用 `grep -n "update_model" internal/service/app_service_test.go` 定位）。在该测试断言部分追加：

```go
// 详情字段应记录从旧模型切到新模型，便于在审计列表里一眼识别变更。
// auditCalls 是 []sqlc.CreateAuditLogParams 类型，DetailMessage 字段是 pgtype.Text；
// 因此通过 .String 取字符串内容做断言。
require.True(t, auditCalls[0].DetailMessage.Valid)
require.Equal(t, "gpt-4o → claude-opus-4-7", auditCalls[0].DetailMessage.String)
```

> 如果该测试目前用的旧模型 / 新模型 ID 不是这两个，按当前 fixture 值替换。如果测试还没断言 audit 写入参数（只断言行为），先加 `auditCalls` 收集逻辑或 fake store 字段。

- [ ] **Step 2: 跑测试确认 fail**

Run: `go test ./internal/service/ -run TestAppService.*UpdateModel -v`
Expected: FAIL（DetailMessage 为空，期望 "gpt-4o → claude-opus-4-7"）

- [ ] **Step 3: 在 UpdateModel 写 audit 处加 DetailMessage**

定位 `internal/service/app_service.go` 中 `store.CreateAuditLog(ctx, sqlc.CreateAuditLogParams{...Action: "update_model"...})` 块（约 line 227-238），改成：

```go
if _, err := store.CreateAuditLog(ctx, sqlc.CreateAuditLogParams{
    ActorID:       actorUUID,
    ActorRole:     principal.Role,
    OrgID:         app.OrgID,
    TargetType:    "app",
    TargetID:      uuidToString(app.ID),
    Action:        "update_model",
    Result:        "succeeded",
    MetadataJson:  metadata,
    DetailMessage: pgtype.Text{String: fmt.Sprintf("%s → %s", app.ModelID, normalizedModelID), Valid: true},
}); err != nil {
```

- [ ] **Step 4: 跑测试确认 pass**

Run: `go test ./internal/service/ -run TestAppService.*UpdateModel -v`
Expected: PASS

### Task 2.2：member_service.go DeleteMember 详情

**Files:**
- Modify: `internal/service/member_service.go:336`
- Modify: `internal/service/member_service_test.go`

- [ ] **Step 1: 写失败的测试**

在 `internal/service/member_service_test.go` 找 DeleteMember 的测试。新增 2 条子用例：

```go
// 场景：成员名下还有 1 个未删除应用，详情应显示「级联删除 1 个应用」。
t.Run("with active app emits cascade detail", func(t *testing.T) {
    // ... 复用现有 fixture 让 GetActiveAppByOwner 返回一条 ...
    require.Equal(t, "级联删除 1 个应用", store.lastAuditCreate.DetailMessage.String)
})
// 场景：成员名下没有未删除应用，详情应为「级联删除 0 个应用」。
t.Run("without app emits zero cascade", func(t *testing.T) {
    require.Equal(t, "级联删除 0 个应用", store.lastAuditCreate.DetailMessage.String)
})
```

> 如果测试 store 还没记 lastAuditCreate，先扩展 stub 加这个字段。

- [ ] **Step 2: 跑测试确认 fail**

Run: `go test ./internal/service/ -run TestMemberService.*Delete -v`
Expected: FAIL

- [ ] **Step 3: 改 DeleteMember 写 audit 处**

在 `internal/service/member_service.go` 中，把 DeleteMember 里 `hasApp` 变量后追加 `cascadeCount` 计数，并在写 audit 时填 DetailMessage：

```go
cascadeCount := 0
if hasApp {
    cascadeCount = 1
    if _, err := s.store.SoftDeleteApp(ctx, app.ID); err != nil {
        return fmt.Errorf("软删应用失败: %w", err)
    }
    // ... 现有入队逻辑 ...
}

actorUUID, _ := optionalUUID(principal.UserID)
if _, err := s.store.CreateAuditLog(ctx, sqlc.CreateAuditLogParams{
    ActorID:       actorUUID,
    ActorRole:     principal.Role,
    OrgID:         user.OrgID,
    TargetType:    "user",
    TargetID:      uuidToString(user.ID),
    Action:        "delete_member",
    Result:        "succeeded",
    DetailMessage: pgtype.Text{String: fmt.Sprintf("级联删除 %d 个应用", cascadeCount), Valid: true},
}); err != nil {
```

- [ ] **Step 4: 跑测试**

Run: `go test ./internal/service/ -run TestMemberService.*Delete -v`
Expected: PASS

### Task 2.3：recharge_service.go Recharge 详情

**Files:**
- Modify: `internal/service/recharge_service.go:150`
- Modify: `internal/service/recharge_service_test.go`

- [ ] **Step 1: 写失败的测试**

在 `internal/service/recharge_service_test.go` 找成功充值 / 失败充值的测试。追加断言：

```go
// 场景：金额 + 非空备注；详情拼成「+50.00 元，备注 vip 续费」。
require.Equal(t, "+50.00 元，备注 vip 续费", store.lastAuditCreate.DetailMessage.String)

// 场景：金额 + 空备注；详情仅金额「+50.00 元」。
require.Equal(t, "+50.00 元", store.lastAuditCreate.DetailMessage.String)
```

> Recharge 的 amount 是 int64 / 单位「分」？看 `internal/service/recharge_service.go:127`，amount 直接传给 newapi，从测试 fixture 推断单位。`+50.00 元` 假设 amount = 5000 分。如果项目以「点 / 元」为单位整数（看 `recharge_records.credit_amount` 注释），改成 `+50 元`。先看测试 fixture 决定。

- [ ] **Step 2: 跑测试确认 fail**

Run: `go test ./internal/service/ -run TestRechargeService -v`
Expected: FAIL

- [ ] **Step 3: 改 Recharge 写 audit 处**

在 `internal/service/recharge_service.go` 中，写 audit 之前构造 detail，写 audit 处填字段：

```go
// 把金额格式化为人类可读字符串；项目内 amount 单位与 recharge_records 一致（具体单位见 service 注释）。
detail := fmt.Sprintf("+%s 元", formatRechargeAmount(amount))
if remark != "" {
    detail = fmt.Sprintf("%s，备注 %s", detail, remark)
}
if _, err := s.store.CreateAuditLog(ctx, sqlc.CreateAuditLogParams{
    ActorID:       operatorUUID,
    ActorRole:     principal.Role,
    OrgID:         id,
    TargetType:    "organization",
    TargetID:      uuidToString(id),
    Action:        "recharge",
    Result:        status,
    DetailMessage: pgtype.Text{String: detail, Valid: true},
}); err != nil {
```

并在文件末尾（与 `parseInt64` 等 helper 并列）添加：

```go
// formatRechargeAmount 把 recharge 金额格式化为人类可读字符串。
// 项目当前 amount 单位与 recharge_records.credit_amount 一致；该格式化函数封装单位约定，
// 调用方不再依赖单位细节。
func formatRechargeAmount(amount int64) string {
    return strconv.FormatInt(amount, 10)
}
```

> 如果项目里金额已经有标准格式化函数（grep `FormatRecharge` / `formatCredit` 等确认），改用现有的。

- [ ] **Step 4: 跑测试**

Run: `go test ./internal/service/ -run TestRechargeService -v`
Expected: PASS

### Task 2.4：onboarding_service.go 三处 detail

**Files:**
- Modify: `internal/service/onboarding_service.go:242,261,407`
- Modify: `internal/service/onboarding_service_test.go`

- [ ] **Step 1: 写失败的测试**

在现有 onboarding 测试里追加断言（在三处对应测试中分别加）。

`create_with_app` 用例：

```go
// 场景：新建成员同时创建应用，详情应描述「新建成员 X（含应用 Y）」。
require.Equal(t, "新建成员 张三（含应用 客服小助手）", findAuditByAction(audits, "create_with_app").DetailMessage.String)
```

`create` 用例（onboarding 第二条 audit）：

```go
// 场景：onboarding 创建应用，详情应描述归属、渠道、节点。
require.Equal(t, "归属成员 张三，渠道 微信", findAuditByAction(audits, "create").DetailMessage.String)
```

`create_for_existing_member` 用例：

```go
// 场景：为已有成员补建应用，详情同 create。
require.Equal(t, "归属成员 张三，渠道 微信", findAuditByAction(audits, "create_for_existing_member").DetailMessage.String)
```

> `findAuditByAction` 是一个测试 helper，必要时新建：

```go
func findAuditByAction(calls []sqlc.CreateAuditLogParams, action string) sqlc.CreateAuditLogParams {
    for _, c := range calls {
        if c.Action == action {
            return c
        }
    }
    return sqlc.CreateAuditLogParams{}
}
```

- [ ] **Step 2: 跑测试确认 fail**

Run: `go test ./internal/service/ -run TestOnboarding -v`
Expected: FAIL

- [ ] **Step 3: 改 OnboardMember（onboarding_service.go:242）写 audit_member 处**

在 onboarding 创建 member 后写 audit `create_with_app` 处：

```go
if _, err := store.CreateAuditLog(ctx, sqlc.CreateAuditLogParams{
    ActorID:    actorUUID,
    ActorRole:  principal.Role,
    OrgID:      org.ID,
    TargetType: "member",
    TargetID:   uuidToString(user.ID),
    Action:     "create_with_app",
    Result:     "succeeded",
    DetailMessage: pgtype.Text{
        String: fmt.Sprintf("新建成员 %s（含应用 %s）", displayNameOrUsername(user), app.Name),
        Valid:  true,
    },
}); err != nil {
```

并在 `internal/service/onboarding_service.go` 文件末尾（或同包合适处）补：

```go
// displayNameOrUsername 返回 user 用于展示的名称。
// display_name 优先；空时回退 username；都为空时返回字符串 "成员"。
func displayNameOrUsername(user sqlc.User) string {
    if user.DisplayName != "" {
        return user.DisplayName
    }
    if user.Username != "" {
        return user.Username
    }
    return "成员"
}
```

- [ ] **Step 4: 改 OnboardMember 的应用 create audit（onboarding_service.go:261）**

```go
if _, err := store.CreateAuditLog(ctx, sqlc.CreateAuditLogParams{
    ActorID:      actorUUID,
    ActorRole:    principal.Role,
    OrgID:        org.ID,
    TargetType:   "app",
    TargetID:     uuidToString(app.ID),
    Action:       "create",
    Result:       "succeeded",
    MetadataJson: appAuditMetadata,
    DetailMessage: pgtype.Text{
        String: fmt.Sprintf("归属成员 %s，渠道 %s", displayNameOrUsername(user), channelLabel(channelType)),
        Valid:  true,
    },
}); err != nil {
```

并在文件末尾补：

```go
// channelLabel 把 channel_type 枚举（如 "wechat"）翻译为中文。
// 未知枚举回退到原始字符串。
func channelLabel(channelType string) string {
    switch channelType {
    case domain.ChannelTypeWeChat:
        return "微信"
    default:
        return channelType
    }
}
```

- [ ] **Step 5: 改 CreateAppForMember（onboarding_service.go:407）**

```go
if _, err := store.CreateAuditLog(ctx, sqlc.CreateAuditLogParams{
    ActorID:      actorUUID,
    ActorRole:    principal.Role,
    OrgID:        org.ID,
    TargetType:   "app",
    TargetID:     uuidToString(app.ID),
    Action:       "create_for_existing_member",
    Result:       "succeeded",
    MetadataJson: metadata,
    DetailMessage: pgtype.Text{
        String: fmt.Sprintf("归属成员 %s，渠道 %s", displayNameOrUsername(user), channelLabel(channelType)),
        Valid:  true,
    },
}); err != nil {
```

- [ ] **Step 6: 跑测试**

Run: `go test ./internal/service/ -run TestOnboarding -v`
Expected: PASS

### Task 2.5：channel_service.go StartLogin 详情

**Files:**
- Modify: `internal/service/channel_service.go:140`
- Modify: `internal/service/channel_service_test.go`

- [ ] **Step 1: 写失败的测试**

在 channel_service_test.go 的 StartLogin 测试里加：

```go
// 场景：StartLogin 写 channel_auth_start 审计时详情包含渠道名（中文）。
require.Equal(t, "渠道 微信", store.lastAuditCreate.DetailMessage.String)
```

- [ ] **Step 2: 跑测试确认 fail**

Run: `go test ./internal/service/ -run TestChannelService.*StartLogin -v`
Expected: FAIL

- [ ] **Step 3: 改 channel_auth_start 写 audit 处**

`channel_service.go:140`：

```go
if _, err := s.store.CreateAuditLog(ctx, sqlc.CreateAuditLogParams{
    ActorID:       actorUUID,
    ActorRole:     principal.Role,
    OrgID:         app.OrgID,
    TargetType:    "app",
    TargetID:      uuidToString(app.ID),
    Action:        "channel_auth_start",
    Result:        "succeeded",
    MetadataJson:  auditMetadata,
    DetailMessage: pgtype.Text{String: fmt.Sprintf("渠道 %s", channelLabel(channelType)), Valid: true},
}); err != nil {
```

`channelLabel` 在 Task 2.4 步骤 4 已在 onboarding_service.go 添加。如果该函数在 channel_service.go 包内不可见（不同包），把它移到 `internal/service/labels.go`（新建文件，包级共享）并 export 为小写 `channelLabel`。两文件同包（`service`），所以 onboarding_service.go 里的 `channelLabel` 可直接复用，无需移动。

- [ ] **Step 4: 跑测试**

Run: `go test ./internal/service/ -run TestChannelService.*StartLogin -v`
Expected: PASS

### Task 2.6：worker channel_login.go channel_bound 详情

**Files:**
- Modify: `internal/worker/handlers/channel_login.go`（340 行 recordChannelAppAudit 和它的调用方）
- Modify: `internal/worker/handlers/channel_login_test.go`

- [ ] **Step 1: 写失败的测试**

在 `channel_login_test.go` 的成功绑定路径测试里追加：

```go
// 场景：channel_bound 成功审计的详情包含渠道与身份。
require.Equal(t, "渠道 微信，身份 18601000000", store.lastAuditCreate.DetailMessage.String)
```

- [ ] **Step 2: 跑测试确认 fail**

Run: `go test ./internal/worker/handlers/ -run TestChannelCheckBinding -v`
Expected: FAIL

- [ ] **Step 3: 扩展 recordChannelAppAudit 签名**

`internal/worker/handlers/channel_login.go:340` 处：

```go
func recordChannelAppAudit(ctx context.Context, store ChannelLoginStore, app sqlc.App, action, result, errorMessage, detailMessage string, metadata map[string]any) error {
	raw, err := json.Marshal(metadata)
	if err != nil {
		return fmt.Errorf("序列化渠道审计元数据失败: %w", err)
	}
	params := sqlc.CreateAuditLogParams{
		ActorRole:    "system",
		OrgID:        app.OrgID,
		TargetType:   "app",
		TargetID:     uuidToString(app.ID),
		Action:       action,
		Result:       result,
		MetadataJson: raw,
	}
	if errorMessage != "" {
		params.ErrorMessage = pgtype.Text{String: errorMessage, Valid: true}
	}
	if detailMessage != "" {
		params.DetailMessage = pgtype.Text{String: detailMessage, Valid: true}
	}
	if _, err := store.CreateAuditLog(ctx, params); err != nil {
		slog.ErrorContext(ctx, "写渠道应用审计失败", "app_id", uuidToString(app.ID), "action", action, "error", err)
		return fmt.Errorf("写入渠道应用审计日志失败: %w", err)
	}
	return nil
}
```

- [ ] **Step 4: 改三处 recordChannelAppAudit 调用方**

文件内有 3 处 `recordChannelAppAudit(...)` 调用（行 266 / 288 / 317 附近）。改成：

成功（行 266，identity 已绑定）：

```go
if err := recordChannelAppAudit(ctx, h.store, app, "channel_bound", "succeeded", "",
    fmt.Sprintf("渠道 %s，身份 %s", channelLabelWorker(payload.ChannelType), identity),
    map[string]any{
        "channel_type":   payload.ChannelType,
        "bound_identity": identity,
        "channel_name":   progress.ChannelName,
    }); err != nil {
    return err
}
```

失败（行 288）：

```go
if err := recordChannelAppAudit(ctx, h.store, app, "channel_bound", "failed", safeMessage,
    fmt.Sprintf("渠道 %s", channelLabelWorker(payload.ChannelType)),
    map[string]any{
        "channel_type": payload.ChannelType,
        "auth_status":  string(progress.Status),
    }); err != nil {
    return err
}
```

fallback 成功（行 317）：

```go
if err := recordChannelAppAudit(ctx, h.store, app, "channel_bound", "succeeded", "",
    fmt.Sprintf("渠道 %s，身份 %s", channelLabelWorker(payload.ChannelType), identity),
    map[string]any{
        "channel_type":   payload.ChannelType,
        "bound_identity": identity,
        "channel_name":   progress.ChannelName,
    }); err != nil {
    return err
}
```

- [ ] **Step 5: 在 channel_login.go 内补 channelLabelWorker**

worker handler 包不能直接复用 service.channelLabel（包不同）。在 `channel_login.go` 文件末尾或 helper 段加：

```go
// channelLabelWorker 是 worker 包内的渠道枚举到中文映射，与 service.channelLabel 同义。
// worker 不依赖 service 包，因而在此独立维护一份；新增渠道时两份同步更新。
func channelLabelWorker(channelType string) string {
    switch channelType {
    case domain.ChannelTypeWeChat:
        return "微信"
    default:
        return channelType
    }
}
```

> 如果 worker handlers 已经能 import `internal/service`，删掉这个 helper 直接调 `service.ChannelLabel`（小写 → 改成大写 export）。先用本地 helper 避免反向依赖。

- [ ] **Step 6: 跑测试**

Run: `go test ./internal/worker/handlers/ -run TestChannelCheckBinding -v`
Expected: PASS

### Task 2.7：runtime_operation_service.go 写入处加空 DetailMessage

**Files:**
- Modify: `internal/service/runtime_operation_service.go:253,354`

- [ ] **Step 1: 显式留空字段并加注释说明意图**

Trigger 写 audit（行 253）和 RequestInitialize 写 audit（行 354）都不需要填 DetailMessage（按 spec §2，start/stop/restart/delete/disable_api_key/restore_api_key/initialize 详情列展示「—」）。代码不需要新增 DetailMessage 字段（不填即 pgtype.Text{Valid: false} → 数据库 NULL），但要追加一行注释解释为何故意留空，避免读者误以为是漏写：

```go
if _, err := s.store.CreateAuditLog(ctx, sqlc.CreateAuditLogParams{
    ActorID:    actorUUID,
    ActorRole:  principal.Role,
    OrgID:      app.OrgID,
    TargetType: "app",
    TargetID:   uuidToString(app.ID),
    Action:     string(op),
    Result:     "succeeded",
    // 不填 DetailMessage：start/stop/restart/delete/disable_api_key/restore_api_key
    // 的详情与「谁触发」列重复，按设计文档落 NULL（前端展示「—」）。
}); err != nil {
```

RequestInitialize（行 354 附近）同理：

```go
if _, err := s.store.CreateAuditLog(ctx, sqlc.CreateAuditLogParams{
    ActorID:    actorUUID,
    ActorRole:  principal.Role,
    OrgID:      app.OrgID,
    TargetType: "app",
    TargetID:   uuidToString(app.ID),
    Action:     "initialize",
    Result:     "succeeded",
    // 不填 DetailMessage：initialize 的资源列已展示 app 名，详情列冗余。
}); err != nil {
```

- [ ] **Step 2: 跑测试确认未破坏**

Run: `go test ./internal/service/ -run TestRuntimeOperationService -v`
Expected: PASS（无新增断言，只是确认改动没引入回归）。

### Task 2.8：worker app_initialize.go writeInitAuditLog 留空 detail

**Files:**
- Modify: `internal/worker/handlers/app_initialize.go:533`

- [ ] **Step 1: 加注释**

`writeInitAuditLog` 中 `CreateAuditLog(ctx, sqlc.CreateAuditLogParams{...})`（行 533-540）补一行注释：

```go
if _, err := h.store.CreateAuditLog(ctx, sqlc.CreateAuditLogParams{
    ActorRole:    "system",
    OrgID:        app.OrgID,
    TargetType:   "app",
    TargetID:     uuidToString(app.ID),
    Action:       "initialize",
    Result:       "succeeded",
    MetadataJson: auditMetadata,
    // 不填 DetailMessage：initialize 的资源列已展示 app 名，详情列冗余。
}); err != nil {
```

- [ ] **Step 2: 编译通过性**

Run: `go build ./...`
Expected: 编译成功。

### Task 2.9：runtime_node_service.go EnrollAgent 详情

**Files:**
- Modify: `internal/service/runtime_node_service.go:273`
- Modify: `internal/service/runtime_node_service_test.go`

- [ ] **Step 1: 写失败测试**

在 runtime_node_service_test.go EnrollAgent 测试里加：

```go
// 场景：agent_enrolled 审计详情包含 agent 版本。
require.Equal(t, "Agent 版本 v1.2.3", store.lastAuditCreate.DetailMessage.String)

// 场景：无 agent_version 时 detail 为空字符串。
require.False(t, store.lastAuditCreate.DetailMessage.Valid)
```

- [ ] **Step 2: 跑测试确认 fail**

Run: `go test ./internal/service/ -run TestRuntimeNodeService.*Enroll -v`
Expected: FAIL

- [ ] **Step 3: 改 EnrollAgent 写 audit 处**

`runtime_node_service.go:273`：

```go
auditDetail := pgtype.Text{}
if v := strings.TrimSpace(input.AgentVersion); v != "" {
    auditDetail = pgtype.Text{String: "Agent 版本 " + v, Valid: true}
}
if _, err := s.store.CreateAuditLog(ctx, sqlc.CreateAuditLogParams{
    ActorRole:     "system",
    TargetType:    "runtime_node",
    TargetID:      uuidToString(node.ID),
    Action:        action,
    Result:        "succeeded",
    DetailMessage: auditDetail,
}); err != nil {
```

- [ ] **Step 4: 跑测试**

Run: `go test ./internal/service/ -run TestRuntimeNodeService.*Enroll -v`
Expected: PASS

### Task 2.10：probe_reconciler.go audit helper 加 detail 参数

**Files:**
- Modify: `internal/service/probe_reconciler.go:125`
- Modify: `internal/service/probe_reconciler_test.go`

- [ ] **Step 1: 写失败测试**

在 probe_reconciler_test.go 的 recordSuccess / recordFailure 测试里加：

```go
// 场景：探测恢复审计详情包含状态切换。
require.Equal(t, "状态：degraded → active", store.lastAuditCreate.DetailMessage.String)

// 场景：探测降级审计详情包含状态切换。
require.Equal(t, "状态：active → degraded", store.lastAuditCreate.DetailMessage.String)
```

- [ ] **Step 2: 跑测试确认 fail**

Run: `go test ./internal/service/ -run TestRuntimeNodeProbeReconciler -v`
Expected: FAIL

- [ ] **Step 3: 改 audit helper 签名**

```go
// audit 写一条节点 probe 审计。before / after 是切换前后的节点状态，用于详情字段。
func (r *RuntimeNodeProbeReconciler) audit(ctx context.Context, nodeID pgtype.UUID, action, before, after string) error {
    if _, err := r.store.CreateAuditLog(ctx, sqlc.CreateAuditLogParams{
        ActorRole:     "system",
        TargetType:    "runtime_node",
        TargetID:      uuidToString(nodeID),
        Action:        action,
        Result:        "succeeded",
        DetailMessage: pgtype.Text{String: fmt.Sprintf("状态：%s → %s", before, after), Valid: true},
    }); err != nil {
        return fmt.Errorf("写节点 probe 审计失败: %w", err)
    }
    return nil
}
```

- [ ] **Step 4: 改两处 audit 调用方**

recordSuccess（行 104）：

```go
if before == domain.RuntimeNodeStatusDegraded && updated.Status == domain.RuntimeNodeStatusActive {
    return r.audit(ctx, updated.ID, "node_probe_recovered", before, updated.Status)
}
```

recordFailure（行 120）：

```go
if before == domain.RuntimeNodeStatusActive && updated.Status == domain.RuntimeNodeStatusDegraded {
    return r.audit(ctx, updated.ID, "node_probe_degraded", before, updated.Status)
}
```

- [ ] **Step 5: 跑测试**

Run: `go test ./internal/service/ -run TestRuntimeNodeProbeReconciler -v`
Expected: PASS

### Task 2.11：knowledge_service.go dispatch_* 详情

**Files:**
- Modify: `internal/service/knowledge_service.go:106`
- Modify: `internal/service/knowledge_service_test.go`

- [ ] **Step 1: 写失败测试**

```go
// 场景：dispatch 失败审计详情包含 scope 与文件路径。
require.Equal(t, "组织文件 docs/foo.pdf", store.lastAuditCreate.DetailMessage.String)
// app scope：
require.Equal(t, "应用文件 docs/foo.pdf", store.lastAuditCreate.DetailMessage.String)
```

- [ ] **Step 2: 跑测试确认 fail**

Run: `go test ./internal/service/ -run TestKnowledgeService.*Dispatch -v`
Expected: FAIL

- [ ] **Step 3: 改 recordDispatchFailure**

`internal/service/knowledge_service.go:96` 起的 `recordDispatchFailure` 函数中，在构造 event 之前算 detail：

```go
detail := fmt.Sprintf("组织文件 %s", relPath)
if appID != "" {
    detail = fmt.Sprintf("应用文件 %s", relPath)
}
event := AuditEvent{
    ActorRole:    "system",
    OrgID:        orgID,
    TargetType:   "knowledge_sync",
    TargetID:     targetID,
    Action:       action,
    Result:       "failed",
    ErrorMessage: dispatchErr.Error(),
    Metadata: map[string]any{
        "app_id":   appID,
        "rel_path": relPath,
    },
    DetailMessage: detail,
}
```

- [ ] **Step 4: 跑测试**

Run: `go test ./internal/service/ -run TestKnowledgeService.*Dispatch -v`
Expected: PASS

### Task 2.12：worker knowledge_sync.go recordAppSync 详情

**Files:**
- Modify: `internal/worker/handlers/knowledge_sync.go:145`
- Modify: `internal/worker/handlers/knowledge_sync_test.go`

- [ ] **Step 1: 写失败测试**

```go
// 场景：upload_file 同步成功的详情包含文件路径。
require.Equal(t, "文件 docs/foo.pdf", store.lastAuditCreate.DetailMessage.String)
// 场景：noop 不携带详情。
require.False(t, store.lastAuditCreate.DetailMessage.Valid)
```

- [ ] **Step 2: 跑测试确认 fail**

Run: `go test ./internal/worker/handlers/ -run TestKnowledgeSync -v`
Expected: FAIL

- [ ] **Step 3: 改 recordAppSync**

```go
detail := ""
if payload.ChangeType != "noop" && payload.RelPath != "" {
    detail = fmt.Sprintf("文件 %s", payload.RelPath)
}
event := service.AuditEvent{
    ActorRole:    "system",
    OrgID:        payload.OrgID,
    TargetType:   "app_knowledge_sync",
    TargetID:     payload.AppID,
    Action:       payload.ChangeType,
    Result:       result,
    ErrorMessage: errMsg,
    Metadata: map[string]any{
        "node_id":  payload.NodeID,
        "rel_path": payload.RelPath,
    },
    DetailMessage: detail,
}
```

- [ ] **Step 4: 跑测试**

Run: `go test ./internal/worker/handlers/ -run TestKnowledgeSync -v`
Expected: PASS

### Task 2.13：newapi_audit.go RecordFailure 详情

**Files:**
- Modify: `internal/audit/newapi_audit.go:86`

- [ ] **Step 1: 写测试（直接在现有测试文件或新建）**

`internal/audit/newapi_audit_test.go` 或同级测试文件加：

```go
// 场景：HTTP 状态码非 0 时详情应为「HTTP <status>」。
require.Equal(t, "HTTP 500", recorder.lastEvent.DetailMessage)
// 场景：status=0 时详情为空。
require.Equal(t, "", recorder.lastEvent.DetailMessage)
```

> 如果该测试文件不存在，参考其他 helper 测试文件模式新建。

- [ ] **Step 2: 跑测试确认 fail**

Run: `go test ./internal/audit/...`
Expected: FAIL

- [ ] **Step 3: 改 RecordFailure**

```go
detail := ""
if fc.Status > 0 {
    detail = fmt.Sprintf("HTTP %d", fc.Status)
}
event := service.AuditEvent{
    ActorID:       fc.ActorID,
    ActorRole:     actorRole,
    OrgID:         fc.OrgID,
    TargetType:    "newapi_call",
    TargetID:      fc.Endpoint,
    Action:        fc.Endpoint,
    Result:        "failed",
    ErrorMessage:  msg,
    Metadata:      metadata,
    DetailMessage: detail,
}
```

文件顶部 import 加 `"fmt"` 如缺。

- [ ] **Step 4: 跑测试**

Run: `go test ./internal/audit/...`
Expected: PASS

### Task 2.14：Commit 2

- [ ] **Step 1: 全量跑测试**

Run: `make test`（容器内 `go test ./...`）或 `go test ./...`（本机）
Expected: 全部 PASS。若某测试报错，回到对应 task 修复。

- [ ] **Step 2: 重新生成 OpenAPI（DetailMessage 字段在 commit 1 已经入 yaml，但 service 端字段名变化也可能影响）**

Run: `make openapi-check`
Expected: `✅ openapi.yaml 与代码同步`。如果失败，跑 `make openapi-gen` 把改动加进暂存。

- [ ] **Step 3: 暂存并提交**

```bash
git add internal/service/app_service.go internal/service/app_service_test.go \
        internal/service/member_service.go internal/service/member_service_test.go \
        internal/service/recharge_service.go internal/service/recharge_service_test.go \
        internal/service/onboarding_service.go internal/service/onboarding_service_test.go \
        internal/service/channel_service.go internal/service/channel_service_test.go \
        internal/worker/handlers/channel_login.go internal/worker/handlers/channel_login_test.go \
        internal/service/runtime_operation_service.go \
        internal/worker/handlers/app_initialize.go \
        internal/service/runtime_node_service.go internal/service/runtime_node_service_test.go \
        internal/service/probe_reconciler.go internal/service/probe_reconciler_test.go \
        internal/service/knowledge_service.go internal/service/knowledge_service_test.go \
        internal/worker/handlers/knowledge_sync.go internal/worker/handlers/knowledge_sync_test.go \
        internal/audit/newapi_audit.go internal/audit/newapi_audit_test.go
```

```bash
git commit -m "$(cat <<'EOF'
feat(audit): 各写入点构造详情字符串

14 个 audit 写入点在调用 Record / CreateAuditLog 之前 fmt.Sprintf 拼好详情字符串，落入 audit_logs.detail_message。

包含：update_model 的模型切换、recharge 的金额与备注、delete_member 的级联应用数、onboarding 的归属成员与渠道、渠道登录的身份、节点 probe 的状态切换、agent 注册的版本、知识库同步的文件路径、new-api 失败的 HTTP 状态码。app 生命周期类（start/stop/restart 等）按设计明确留空。
EOF
)"
```

---

## Commit 3：前端审计页改造

### Task 3.1：重新生成前端类型

**Files:**
- Modify: `web/src/api/generated.ts`

- [ ] **Step 1: 生成**

Run: `make web-types-gen`
Expected: `git diff web/src/api/generated.ts` 看到 `AuditLog` 类型新增 `actor_name?`、`actor_deleted`、`target_name?`、`target_deleted`、`action_detail?` 字段。

### Task 3.2：改 AuditLogsPage.vue 列结构

**Files:**
- Modify: `web/src/pages/audit/AuditLogsPage.vue`

- [ ] **Step 1: 整体替换 columns 定义和相关 imports**

替换文件内容：

```vue
<template>
  <DataTableList
    :title="'审计日志'"
    :eyebrow="orgEyebrow"
    :columns="columns"
    :data="logs ?? []"
    :loading="isLoading || organizationsLoading"
    :error-message="errorMessage"
    :row-key="(row: AuditLog) => row.id"
  >
    <template #toolbar>
      <n-select
        v-if="isPlatformAdmin"
        v-model:value="selectedOrgId"
        :options="orgOptions"
        style="width: 220px"
        placeholder="选择组织"
      />
    </template>
  </DataTableList>
</template>

<script setup lang="ts">
import { computed, h } from 'vue'
import { NSelect, NTag, NTooltip, type DataTableColumns } from 'naive-ui'

import { useOrgAuditLogsQuery } from '@/api/hooks/useAuditLogs'
import { usePlatformOrgSelection } from '@/composables/usePlatformOrgSelection'
import { canViewOrgAudit } from '@/domain/permissions'
import { useAuthStore } from '@/stores/auth'
import DataTableList from '@/components/DataTableList.vue'
import { timeColumn } from '@/components/columns'

// AuditLogsPage 展示组织级审计日志，平台和组织管理员可看，普通成员需去应用详情查看自己的应用审计。
const props = defineProps<{ orgId?: string }>()
const auth = useAuthStore()
// 平台管理员通过组织选择器查看不同组织审计，组织用户默认使用自身组织。
const {
  isPlatformAdmin,
  selectedOrgId,
  effectiveOrgId,
  orgOptions,
  organizationsLoading,
  organizationsError,
} = usePlatformOrgSelection(computed(() => auth.user), computed(() => props.orgId))
const orgEyebrow = computed(() => auth.user?.role === 'platform_admin' ? 'Platform · 审计' : '组织 · 审计')
const canView = computed(() => canViewOrgAudit(auth.user, effectiveOrgId.value))

// queryOrgId 为 undefined 时不发起查询，前端先拦截无权限场景减少 403。
const queryOrgId = computed(() => canView.value ? effectiveOrgId.value : undefined)
const { data: logs, isLoading, error } = useOrgAuditLogsQuery(queryOrgId)

// 无关联组织时展示提示；有 API 错误时展示错误信息
const errorMessage = computed(() => {
  if (organizationsError.value) return String(organizationsError.value)
  if (!effectiveOrgId.value) return isPlatformAdmin.value ? '暂无可查看组织' : '当前账号未关联组织，无法查看审计日志。'
  if (!canView.value) return '当前账号无权查看组织级审计，请在自己的实例详情中查看实例审计。'
  if (error.value) return String(error.value)
  return undefined
})

type AuditLog = NonNullable<typeof logs.value>[number]

// auditTagType 将审计结果映射为标签色，未知结果保持默认色以兼容后端扩展。
function auditTagType(result: string): 'success' | 'warning' | 'error' | 'default' {
  switch (result) {
    case 'success': case 'succeeded': return 'success'
    case 'failed': case 'error': return 'error'
    case 'partial': return 'warning'
    default: return 'default'
  }
}

// shortenId 截取 UUID 末 8 位用作 fallback 展示，避免列太长。
function shortenId(value: string | undefined | null): string {
  if (!value) return ''
  return value.length > 8 ? value.slice(-8) : value
}

// renderPrincipal 渲染操作者 / 资源单元格的统一结构：
// - system actor 行直接展 actor_role_label（系统），无副文与 hover；
// - 否则主文 name fallback shortenId(uuid) fallback role_label，副文为 sub，UUID 进 hover。
// deleted 为 true 时主文后追加「已删除」徽章。
function renderPrincipal(opts: {
  primary: string
  fallback: string
  sub: string
  uuid: string | null | undefined
  deleted: boolean
  isSystem?: boolean
}) {
  if (opts.isSystem) {
    return h('strong', opts.primary)
  }
  const main: any[] = [h('strong', opts.primary || opts.fallback)]
  if (opts.deleted) {
    main.push(h(NTag, { type: 'warning', size: 'tiny', bordered: false, style: 'margin-left:6px' }, { default: () => '已删除' }))
  }
  const sub = opts.sub ? h('small', { style: 'display:block;color:#8A94C6;font-size:12px' }, opts.sub) : null
  const cell = h('div', [main, sub])
  if (!opts.uuid) return cell
  return h(NTooltip, { trigger: 'hover', placement: 'top' }, {
    trigger: () => cell,
    default: () => opts.uuid,
  })
}

// columns 展示审计主体、资源、动作、详情和结果；错误信息作为结果列的辅助诊断文本。
const columns: DataTableColumns<AuditLog> = [
  timeColumn('时间', r => r.created_at),
  {
    title: '操作者', key: 'actor_name',
    render: (row) => renderPrincipal({
      primary: row.actor_name ?? '',
      fallback: shortenId(row.actor_id ?? '') || row.actor_role_label,
      sub: row.actor_role_label,
      uuid: row.actor_id,
      deleted: row.actor_deleted,
      isSystem: row.actor_role === 'system' && !row.actor_id,
    }),
  },
  {
    title: '资源', key: 'target_name',
    render: (row) => renderPrincipal({
      primary: row.target_name ?? '',
      // 没 name 的目标（newapi_call 等）直接展示 target_id 字符串本身。
      fallback: row.target_id,
      sub: row.target_type_label,
      // 只有 target_id 像 UUID 才走 hover；endpoint 字符串本身在主文已经可读。
      uuid: row.target_name ? row.target_id : null,
      deleted: row.target_deleted,
    }),
  },
  { title: '操作', key: 'action', render: (row) => row.action_label },
  {
    title: '详情', key: 'action_detail',
    minWidth: 240,
    render: (row) => row.action_detail
      ? h('span', { style: 'white-space:pre-wrap' }, row.action_detail)
      : h('span', { style: 'color:#8A94C6' }, '—'),
  },
  {
    title: '结果', key: 'result',
    render: (row) => [
      h(NTag, { type: auditTagType(row.result), size: 'small', bordered: false }, { default: () => row.result_label }),
      row.error_message ? h('small', { style: 'display:block;color:#FF3B5C;font-size:12px' }, row.error_message) : null,
    ],
  },
]
</script>
```

- [ ] **Step 2: 前端 lint / typecheck**

Run: `cd web && pnpm typecheck`
Expected: 无类型错误。

### Task 3.3：改 AppAuditTab.vue 列结构

**Files:**
- Modify: `web/src/pages/apps/AppAuditTab.vue`

- [ ] **Step 1: 整体替换**

```vue
<template>
  <DataTableList
    :title="'实例审计'"
    :eyebrow="'Instance · Audit'"
    :columns="columns"
    :data="logs ?? []"
    :loading="isLoading"
    :error-message="errorMessage"
    :row-key="(row: AuditLog) => row.id"
  />
</template>

<script setup lang="ts">
import { computed, h, inject, type Ref } from 'vue'
import { NTag, NTooltip, type DataTableColumns } from 'naive-ui'

import type { AuditLog } from '@/api'
import { useTargetAuditLogsQuery } from '@/api/hooks/useAuditLogs'
import type { AppDTO } from '@/api/hooks/useApps'
import DataTableList from '@/components/DataTableList.vue'
import { timeColumn } from '@/components/columns'
import { canViewOwnAppAudit } from '@/domain/permissions'
import { useAuthStore } from '@/stores/auth'

// AppAuditTab 展示单个应用的审计记录，依赖父级 AppDetailPage 注入的应用上下文做权限判断。
const props = defineProps<{ appId: string }>()
const auth = useAuthStore()
const app = inject<Ref<AppDTO | null>>('app')
// canView 以当前账号和应用归属共同判定，避免成员查看非自己应用审计。
const canView = computed(() => canViewOwnAppAudit(auth.user, app?.value))
// target 为 undefined 时查询 hook 不发起请求，前端先拦截无权限场景减少 403。
const target = computed(() => canView.value ? { targetType: 'app', targetId: props.appId } : undefined)
const { data: logs, isLoading, error } = useTargetAuditLogsQuery(target)

// errorMessage 合并权限失败和 API 失败，交给公共列表组件显示。
const errorMessage = computed(() => {
  if (!canView.value) return '当前账号无权查看该实例审计。'
  if (error.value) return String(error.value)
  return undefined
})

function auditTagType(result: string): 'success' | 'warning' | 'error' | 'default' {
  switch (result) {
    case 'success': case 'succeeded': return 'success'
    case 'failed': case 'error': return 'error'
    case 'partial': return 'warning'
    default: return 'default'
  }
}

function shortenId(value: string | undefined | null): string {
  if (!value) return ''
  return value.length > 8 ? value.slice(-8) : value
}

function renderActor(row: AuditLog) {
  // 系统行：主文「系统」，无副文与 hover。
  if (row.actor_role === 'system' && !row.actor_id) {
    return h('strong', row.actor_role_label)
  }
  const main: any[] = [h('strong', row.actor_name || shortenId(row.actor_id ?? '') || row.actor_role_label)]
  if (row.actor_deleted) {
    main.push(h(NTag, { type: 'warning', size: 'tiny', bordered: false, style: 'margin-left:6px' }, { default: () => '已删除' }))
  }
  const cell = h('div', [main, h('small', { style: 'display:block;color:#8A94C6;font-size:12px' }, row.actor_role_label)])
  if (!row.actor_id) return cell
  return h(NTooltip, { trigger: 'hover', placement: 'top' }, {
    trigger: () => cell,
    default: () => row.actor_id,
  })
}

// columns 展示审计时间、操作者、动作、详情和结果；不再单独展示 UUID 作为副文。
const columns: DataTableColumns<AuditLog> = [
  timeColumn('时间', r => r.created_at),
  { title: '操作者', key: 'actor_name', render: renderActor },
  { title: '操作', key: 'action', render: (row) => row.action_label },
  {
    title: '详情', key: 'action_detail',
    minWidth: 240,
    render: (row) => row.action_detail
      ? h('span', { style: 'white-space:pre-wrap' }, row.action_detail)
      : h('span', { style: 'color:#8A94C6' }, '—'),
  },
  {
    title: '结果', key: 'result',
    render: (row) => [
      h(NTag, { type: auditTagType(row.result), size: 'small', bordered: false }, { default: () => row.result_label }),
      row.error_message ? h('small', { style: 'display:block;color:#FF3B5C;font-size:12px' }, row.error_message) : null,
    ],
  },
]
</script>
```

- [ ] **Step 2: 前端 typecheck**

Run: `cd web && pnpm typecheck`
Expected: 无类型错误。

### Task 3.4：Vitest 组件测试

**Files:**
- Create: `web/src/pages/audit/__tests__/AuditLogsPage.test.ts`（如不存在）
- Create: `web/src/pages/apps/__tests__/AppAuditTab.test.ts`（如不存在）

- [ ] **Step 1: 写组件测试（AuditLogsPage）**

如果 `web/src/pages/audit/__tests__` 目录或已有测试文件存在，扩展之；否则新建：

```ts
// AuditLogsPage 在不同审计场景下应该正确渲染操作者 / 资源 / 详情列。
// 各用例通过 mock useOrgAuditLogsQuery 返回不同审计 fixture，验证名称、UUID hover、已删除徽章、详情 fallback。
import { describe, it, expect, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import AuditLogsPage from '../AuditLogsPage.vue'

vi.mock('@/api/hooks/useAuditLogs', () => ({
  useOrgAuditLogsQuery: () => ({
    data: { value: [
      {
        id: '1',
        actor_id: '06258106-7b34-49b0-9a2b-ed13b8ba1524',
        actor_role: 'org_admin',
        actor_role_label: '组织管理员',
        actor_name: '张三',
        actor_deleted: false,
        target_id: '4eee1d51-c4c7-427c-addc-cb4a51848e4e',
        target_type: 'app',
        target_type_label: '应用实例',
        target_name: '客服小助手',
        target_deleted: true,
        action: 'update_model',
        action_label: '更换模型',
        action_detail: 'gpt-4o → claude-opus-4-7',
        result: 'succeeded',
        result_label: '成功',
        created_at: '2026-05-18T10:00:00Z',
      },
    ] },
    isLoading: { value: false },
    error: { value: undefined },
  }),
}))
// 其他必须的 mock，例如 useAuthStore / usePlatformOrgSelection，按现有测试约定补全

describe('AuditLogsPage', () => {
  it('renders actor name with role subtitle and target deleted badge', () => {
    const wrapper = mount(AuditLogsPage)
    const html = wrapper.html()
    expect(html).toContain('张三')
    expect(html).toContain('组织管理员')
    expect(html).toContain('客服小助手')
    expect(html).toContain('已删除')
  })

  it('renders action detail string for update_model', () => {
    const wrapper = mount(AuditLogsPage)
    expect(wrapper.html()).toContain('gpt-4o → claude-opus-4-7')
  })

  it('falls back to dash for empty action_detail', () => {
    // 第二条 audit 行 action_detail 为空
    // ... extend the mock data above with an additional row ...
  })
})
```

> 若现有项目里没有 `web/src/pages/audit/__tests__/` 目录，参考 `web/src/components/__tests__/` 的结构。具体 mock 写法以现有 `web/src` Vitest 测试为模板。

- [ ] **Step 2: 写组件测试（AppAuditTab）**

`web/src/pages/apps/__tests__/AppAuditTab.test.ts`：参考 AuditLogsPage 测试，覆盖：
- system 行展示「系统」无 hover；
- 普通行展示 actor_name + 副文角色 + hover UUID；
- 详情列空 → 「—」；
- actor_deleted 显示徽章。

- [ ] **Step 3: 跑测试**

Run: `cd web && pnpm test`
Expected: 全部 PASS。

### Task 3.5：浏览器手工验证

按 CLAUDE.md「新功能开发完成后，必须调用浏览器进行全面功能验证」执行。

- [ ] **Step 1: 启动本地环境**

Run: `make dev-up`（或项目通用启动命令）
Expected: manager-api / manager-web 起来；浏览器访问 `http://localhost:5173`（实际端口以项目 README 为准）。

- [ ] **Step 2: 验证场景 A（组织管理员视角）**

- 登录组织管理员账号
- 触发 update_model：到某 App 详情页换个模型
- 触发 recharge：以平台管理员身份给组织充值（如果组织管理员没充值权限，跳到平台管理员账号）
- 触发 delete_member：删除某成员
- 打开审计页 → 校验「操作者」列展示组织管理员姓名、「资源」列展示应用 / 组织名、「详情」列展示对应字符串
- hover 操作者 / 资源单元格 → UUID 显示

- [ ] **Step 3: 验证场景 B（平台管理员视角）**

- 登录平台管理员
- 通过组织下拉切换不同组织
- 校验：跨组织看到的审计行 actor_name / target_name 都对应正确组织实体

- [ ] **Step 4: 验证场景 C（应用详情审计 Tab）**

- 打开任意 App 详情 → 审计 Tab
- 校验操作者列展示名称；详情列展示对应字符串

- [ ] **Step 5: 验证场景 D（含已删除应用 / 已下线成员）**

- 找一行 `delete_member` audit；校验目标列展示成员名 + 「已删除」徽章
- 找一行 update_model 但应用后续被删除的；校验「已删除」徽章在资源列出现

- [ ] **Step 6: 若发现问题**

回到对应 task 修复，重跑 typecheck / 单测 / 浏览器验证。

### Task 3.6：Commit 3

- [ ] **Step 1: 全部校验**

Run（三条并行）：
- `make openapi-check`
- `go test ./...`
- `cd web && pnpm test && pnpm typecheck`

Expected: 全部 PASS。

- [ ] **Step 2: 暂存并提交**

```bash
git add web/src/api/generated.ts \
        web/src/pages/audit/AuditLogsPage.vue \
        web/src/pages/apps/AppAuditTab.vue \
        web/src/pages/audit/__tests__/ \
        web/src/pages/apps/__tests__/
```

```bash
git commit -m "$(cat <<'EOF'
feat(web): 审计页展示操作者/资源名称与操作详情

操作者 / 资源列以名称为主、角色 / 资源类型为副标题，UUID 隐藏到 hover；新增详情列展示后端冻结的事件描述字符串，已删除实体加「已删除」徽章。

后端 detail_message 字段在浏览器中验证 update_model / recharge / delete_member / app delete 等四类典型场景，列宽 / 文案 / 徽章渲染符合设计。
EOF
)"
```

---

## 最终验证清单

按 spec §13「交付前自检」执行：

- [ ] `make openapi-check` 工作区干净
- [ ] `go test ./...` 全部 PASS
- [ ] `cd web && pnpm test && pnpm typecheck` 全部 PASS
- [ ] 浏览器手工验证四种典型场景（Task 3.5）通过
- [ ] `git status` 无未提交改动 / 无误提交的密钥、临时调试代码、`.local/` / `.worktrees/` 等非业务文件

---

## 备忘 / 已知风险

- **sqlc 对 COALESCE 列的类型推断**可能是 `pgtype.Text` / `interface{}` / `*string` 之一。Task 1.4 提供了适配 helper `stringFromColumn` / `boolFromColumn`，避免一处推断变化就让代码全挂。
- **`recharge` 的金额单位**：spec 写「+5000.00 元」假设是元；代码里现存 `amount int64` 单位需要查 fixture 验证。Task 2.3 引入 `formatRechargeAmount` 封装单位约定。
- **`channelLabel` / `channelLabelWorker` 同步**：worker 包不依赖 service 包，两处独立维护中文映射；新增渠道时两处都要改。当前只有 wechat 一种，影响面小。
- **可选改造 `app.delete` 的 `级联：N 个渠道绑定`**：本计划未纳入，避免给 RuntimeOperationService 加查询；如未来要加，可在 Task 2.7 处扩展，给 Trigger 写 audit 之前先查 channel_bindings 计数。
