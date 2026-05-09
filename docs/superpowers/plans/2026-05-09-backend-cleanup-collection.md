# 后端清理合集 实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把 A-1..A-4 主线之外的 10 项后端清理打包推进：sentinel error 集中、HTTP client 抽象、handler 测试补齐、apps 索引重建、DeleteUser 独立 ctx、testify 全量迁移、users.deleted_at、app_state_machine 文档、2 个 A-4 nit。

**Architecture:** 11 个 task 按风险递增排序：先 3 个独立小项（C-3/C-4/C-5）暖场；再 5 个 B 类核心（B-1/B-4/B-5/C-2/B-2）；最后 2 个机械大项（B-3 handler 测试 + C-1 testify 全量）；末尾 AGENTS.md 约定 + DoD 全量验收。每个 task 一次独立 commit，可单独 revert。

**Tech Stack:** Go 1.25 / pgx / sqlc / golang-migrate / testify / Gin / log/slog

**Spec reference:** `docs/superpowers/specs/2026-05-09-backend-cleanup-collection-design.md`

**关键约束：**

- 每个 task 一次 commit；commit message 中文摘要 + Co-Authored-By（参考 AGENTS.md）
- 不引入新依赖（testify 已在 go.sum，本次需 require 到 go.mod）
- 不动 `internal/integrations/newapi` 包内 5 个 sentinel（业务边界）
- 不重写 sqlc 生成代码（仅扩 query 后跑 `make sqlc-generate`）
- handler 测试 mock 风格沿用现有 `members_test.go` / `runtime_nodes_test.go` 模式

---

## Task 1: C-3 app_state_machine 状态转移文档（暖场小项）

**Files:**
- Modify: `internal/domain/app_state_machine.go`

- [ ] **Step 1.1: 看文件现状**

```bash
head -30 internal/domain/app_state_machine.go
```

记录当前 package doc 与状态机常量定义。

- [ ] **Step 1.2: 在 package 头部插入状态转移文档块**

把现有 `// Package domain ...` 注释改为：

```go
// Package domain 维护应用状态机与枚举。app_state_machine.go 定义应用生命周期。
//
// # 状态机
//
//	draft  ──onboarding──▶  initializing  ──worker 完成──▶  binding_waiting
//	  │                          │                              │
//	  │                          ▼                              ▼ 渠道扫码
//	  │                       error ◀──────────────────────  binding_failed
//	  │                          ▲                              │
//	  └──────────────────────────┴──────────────────────────────┴─────▶ running
//	                                                                    │
//	                                                                    ▼ 停止
//	                                                                  stopped
//	                                                                    │
//	                                                                    ▼ 删除
//	                                                                  deleted
//
// 关键转移约束：
//   - draft → initializing：仅 onboarding job 可触发
//   - initializing → binding_waiting：worker 完成镜像拉取 + new-api 凭证配置
//   - binding_waiting → binding_failed：渠道扫码超时或 token 过期
//   - error 是吸入态：任何步骤失败都会落到 error，由用户手工 retry 才能离开
//   - deleted 是终态：deleted_at 字段非空即认为已删
//   - stopped → running：用户主动启动
//
// 维护提醒：状态机如有变化，本文档块必须同步更新；与代码不一致按代码为准。
package domain
```

如果原 package doc 在其他 .go 文件（如 enums.go），把这块插到 app_state_machine.go 文件头部，确保读这个文件的人能看到。

- [ ] **Step 1.3: vet + test + commit**

```bash
go vet ./internal/domain/...
go build ./...
git status --short
git add internal/domain/app_state_machine.go
git commit -m "$(cat <<'EOF'
docs(domain): app_state_machine 头部插入状态转移文档块

明确 draft / initializing / binding_waiting / binding_failed / running /
error / stopped / deleted 8 个状态的转移约束，便于新协作者快速建立心智
模型。文档紧贴代码（同文件头部），未来改状态机时一并更新。

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

## 报告（≤100 字）：commit SHA + 文档块行数

---

## Task 2: C-4 newapi_audit msg 删 "newapi_audit:" 前缀

**Files:**
- Modify: `internal/audit/newapi_audit.go`

- [ ] **Step 2.1: 看现状**

```bash
grep -n 'slog\.\|"newapi_audit:' internal/audit/newapi_audit.go
```

应至少 1 处含 `slog.ErrorContext(ctx, "newapi_audit: ...", ...)`。

- [ ] **Step 2.2: 删前缀**

把所有 slog 调用 msg 中的 `"newapi_audit: "` 前缀去掉（caller 路径已由 `slog.HandlerOptions{AddSource: true}` 自动给出）：

```go
// 改前
slog.ErrorContext(ctx, "newapi_audit: 写 audit_logs 失败", "error", err)
// 改后
slog.ErrorContext(ctx, "写 audit_logs 失败", "error", err)
```

如果文件内有多处类似前缀（"newapi_audit:"），全部删除。

- [ ] **Step 2.3: vet + test + commit**

```bash
go vet ./internal/audit/...
go test ./internal/audit/... -count=1
git add internal/audit/newapi_audit.go
git commit -m "$(cat <<'EOF'
refactor(audit): 删除 newapi_audit slog msg 中的 "newapi_audit:" 前缀

A-4 reviewer nit 修复：caller 路径已由 slog AddSource 自动给出，
msg 字段不再需要手工前缀。

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

## 报告（≤100 字）：删了几处前缀 + commit SHA

---

## Task 3: C-5 stdHBLogger 改造为 *slog.Logger（含接口扩散排查）

**Files:**
- Modify: `runtime/agent/heartbeat.go`
- Possibly other files if `hbLogger` interface 有多个实现

**关键约束**：先 grep `hbLogger` 接口定义与所有实现方。如果接口实现方多于 `stdHBLogger` 一处，**stop 报告 controller**，决定是合并 task 还是延后。

- [ ] **Step 3.1: 接口扩散排查**

```bash
grep -nE 'type hbLogger|hbLogger ' runtime/agent/*.go internal/integrations/agent/*.go 2>&1 | head -20
```

记录：
- `hbLogger` 接口定义（在哪个文件、含哪些方法 Infof/Warnf/Errorf）
- 所有实现方（应只有 `stdHBLogger`）

```bash
grep -nE 'hbLogger|HBLogger' runtime/agent/*.go internal/integrations/agent/*.go 2>&1 | head -20
```

如果除 stdHBLogger 外还有其他实现（mockHBLogger / agentHBLogger 等），**stop 报告 controller**。

- [ ] **Step 3.2: baseline 测试**

```bash
go test ./runtime/agent/... -count=1
```

预期全绿。

- [ ] **Step 3.3: 改造 stdHBLogger**

读现有 `stdHBLogger` 定义，改造为 `hbLoggerAdapter` 持有 `*slog.Logger`：

```go
// 改前
type stdHBLogger struct{}

func (stdHBLogger) Infof(format string, args ...any)  { slog.Info(fmt.Sprintf("heartbeat "+format, args...)) }
func (stdHBLogger) Warnf(format string, args ...any)  { slog.Warn(fmt.Sprintf(format, args...)) }
func (stdHBLogger) Errorf(format string, args ...any) { slog.Error(fmt.Sprintf(format, args...)) }

// 改后
type hbLoggerAdapter struct {
	logger *slog.Logger
}

func (a *hbLoggerAdapter) Infof(format string, args ...any) {
	a.logger.Info(fmt.Sprintf(format, args...))
}
func (a *hbLoggerAdapter) Warnf(format string, args ...any) {
	a.logger.Warn(fmt.Sprintf(format, args...))
}
func (a *hbLoggerAdapter) Errorf(format string, args ...any) {
	a.logger.Error(fmt.Sprintf(format, args...))
}
```

注意：**接口签名 `Infof/Warnf/Errorf` 不变**（接口实现方约束）；改造的核心是把 `slog.Info/Warn/Error` 全局调用换为持有 `*slog.Logger` 字段调用，便于注入特定 logger（如测试中用 io.Discard logger）。

调用方更新（agent main.go 或类似初始化点）：

```go
// 改前
hbLog := stdHBLogger{}
heartbeat.Run(ctx, hbLog, ...)

// 改后
hbLog := &hbLoggerAdapter{logger: slog.Default()}
heartbeat.Run(ctx, hbLog, ...)
```

注意：`fmt.Sprintf` 退化无法消除（因接口约定 printf 风格）；本 task 仅完成 spec 5.11 节明确的「持有 logger 字段」改造。

- [ ] **Step 3.4: vet + test + commit**

```bash
go vet ./...
go test ./runtime/agent/... -count=1
go build ./...
git add runtime/agent/
git commit -m "$(cat <<'EOF'
refactor(agent): stdHBLogger 改为 hbLoggerAdapter 接 *slog.Logger 字段

A-4 reviewer nit 修复：原 stdHBLogger 用 slog 全局调用，无法注入测试
专用 logger（如 io.Discard）。改造为持有 *slog.Logger 字段的 adapter。
hbLogger 接口签名不变（仍是 Infof/Warnf/Errorf printf 风格，因接口
约定无法消除 fmt.Sprintf 退化）。

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

## 报告（≤150 字）：grep 找到 hbLogger 实现方 N 个 + 改造文件清单 + commit SHA。如发现接口扩散停下报告。

---

## Task 4: B-1 sentinel error 集中到 internal/service/errors.go

**Files:**
- Modify: `internal/service/errors.go`（扩展）
- Modify: 11 个 service 文件（删本地 sentinel）：
  - `auth_service.go` / `member_service.go` / `workspace_service.go` / `runtime_node_service.go`
  - `app_service.go` / `runtime_operation_service.go` / `persona_service.go`
  - `channel_service.go` / `organization_service.go` / `onboarding_service.go` / `recharge_service.go`
- Possibly modify tests if they reference these errors

- [ ] **Step 4.1: 全仓盘点 service 包内所有 sentinel**

```bash
grep -nE '^var Err[A-Z]' internal/service/*.go | grep -v _test.go
```

记录每个 sentinel：
- 名字
- 当前定义位置（文件:行）
- error message 文本

预期约 25 个（spec 调研基数）。

- [ ] **Step 4.2: baseline 测试**

```bash
go test ./internal/service/... -count=1
```

全绿。

- [ ] **Step 4.3: 扩展 errors.go 集中所有 sentinel**

读 `internal/service/errors.go` 现状（应只有 `ErrForbidden / ErrNotFound / ErrNoNodeAvailable`）。

按 spec 5.2 节模板把 25 个 sentinel 全部加进去，按业务对象分组：

```go
// Package service 的所有可被 handler 层 errors.Is 检查的 sentinel error。
// 业务模块需要新增错误时优先扩展本文件，不要在各 service 文件内再定义本地 sentinel。
package service

import "errors"

// 通用错误 ----------------------------------------------------------

var ErrForbidden = errors.New("无权访问")
var ErrNotFound = errors.New("资源不存在")
var ErrConflict = errors.New("资源冲突") // 新增

// 节点 -------------------------------------------------------------

var ErrNoNodeAvailable = errors.New("当前无可用 runtime 节点")

// 认证 -------------------------------------------------------------

var ErrInvalidCredentials = errors.New("用户名或密码错误")
var ErrInvalidToken = errors.New("token 无效或已过期")
var ErrUserDisabled = errors.New("用户已禁用")
var ErrOrgDisabled = errors.New("组织已禁用")

// 成员 -------------------------------------------------------------

var ErrMemberCreateInvalid = errors.New("成员创建参数无效")

// 工作区 -----------------------------------------------------------

var ErrWorkspaceForbidden = errors.New("无权访问该应用工作区")
var ErrWorkspaceMissing = errors.New("应用工作区缺失")
var ErrWorkspaceBadPath = errors.New("工作区路径不合法")

// 应用 / 运行时操作 ------------------------------------------------

var ErrAppNotReinitializable = errors.New("应用当前状态不允许重新初始化")
var ErrRuntimeOperationDenied = errors.New("无权执行运行操作")

// （Step 4.1 盘点的其余 sentinel 全部按业务分组补齐，message 文本 1:1 复制原版）
```

**严格约束**：error message 文本必须 1:1 复制原版（错误文案是契约的一部分）。

- [ ] **Step 4.4: 各 service 文件删本地 sentinel 定义**

逐个文件处理：

```go
// 例 auth_service.go 改前
var (
	ErrInvalidCredentials = errors.New("用户名或密码错误")
	ErrInvalidToken       = errors.New("token 无效或已过期")
	ErrUserDisabled       = errors.New("用户已禁用")
	ErrOrgDisabled        = errors.New("组织已禁用")
)

// 改后：删除整块（已在 errors.go 集中）
// 同包同名引用，文件内其他代码无需改动
```

注意：

- 同包内引用，无 import 改动
- 如某文件 `errors` 包仅为定义 sentinel 用，删除后 `import "errors"` 也要删
- 如该文件还有 `errors.Is(err, pgx.ErrNoRows)` 等用法，保留 `import "errors"`

- [ ] **Step 4.5: 验证 grep 干净**

```bash
grep -nE '^var Err[A-Z]' internal/service/*.go | grep -v 'errors.go'
```

预期：输出空（只有 errors.go 含 var Err 定义）。

```bash
go vet ./internal/service/...
go test ./internal/service/... -count=1
go build ./...
```

预期：全过。如有 fail（typecheck 或测试），通常是某文件 import "errors" 未删干净 / 测试 mock 还引用旧名字。

- [ ] **Step 4.6: 自检 + commit**

```bash
git status --short
```

应有 errors.go + 11 个 service 文件改动。

```bash
git add internal/service/
git commit -m "$(cat <<'EOF'
refactor(service): 25 个 sentinel error 集中到 internal/service/errors.go

按业务对象分组（通用 / 节点 / 认证 / 成员 / 工作区 / 应用-运行时操作 等）。
原 11 个 service 文件中本地 var Err... 定义全部删除，引用点同包同名
不变。新增 ErrConflict 通用错误供未来扩展。

handler 层的 errors.Is(err, service.ErrXxx) 路径不受影响（仅物理位置变了）。

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

## 报告（≤200 字）：盘点 sentinel 总数 + 改动文件数 + commit SHA + 任何 concern（如错误 message 文本与 spec 模板不一致需保留原版）。

---

## Task 5: B-4 apps_org_owner_status_idx 重建

**Files:**
- Create: `internal/migrations/000010_apps_index_rebuild.up.sql`
- Create: `internal/migrations/000010_apps_index_rebuild.down.sql`

- [ ] **Step 5.1: 确认当前迁移最大编号**

```bash
ls internal/migrations/ | sort | tail -5
```

预期：`000008` 是最新。如果实际更高（如已有 000009），用更高编号 + 1。

- [ ] **Step 5.2: 写 up.sql**

新建 `internal/migrations/000010_apps_index_rebuild.up.sql`：

```sql
-- apps_org_owner_status_idx 重建为 (org_id, deleted_at, created_at DESC) 以匹配
-- ListAppsByOrg 查询的 WHERE org_id = ? AND deleted_at IS NULL ORDER BY created_at DESC。
-- 旧索引 (org_id, owner_user_id, status) 顺序不利于该查询。
DROP INDEX IF EXISTS apps_org_owner_status_idx;
CREATE INDEX apps_org_active_created_idx ON apps(org_id, deleted_at, created_at DESC);
```

注意：

- 索引名不同（防 down 时与 up 同名冲突）
- **不带 `CONCURRENTLY`**：本地开发环境表很小；生产部署时由 DBA 手工补 CONCURRENTLY

- [ ] **Step 5.3: 写 down.sql**

新建 `internal/migrations/000010_apps_index_rebuild.down.sql`：

```sql
DROP INDEX IF EXISTS apps_org_active_created_idx;
CREATE INDEX apps_org_owner_status_idx ON apps(org_id, owner_user_id, status);
```

- [ ] **Step 5.4: 跑迁移验证 idempotent**

```bash
make migrate-up 2>&1 | tail -5
make migrate-down 2>&1 | tail -5
make migrate-up 2>&1 | tail -5
```

预期：每次 up/down 都成功，无错误。

如 `make migrate-down` 不能退到 000010 之前（如 down 报 unique constraint 冲突），按错误调整 down.sql。

- [ ] **Step 5.5: 自检 + commit**

```bash
git add internal/migrations/000010_apps_index_rebuild.up.sql internal/migrations/000010_apps_index_rebuild.down.sql
git commit -m "$(cat <<'EOF'
refactor(db): apps 索引重建匹配 ListAppsByOrg 查询模式

旧 apps_org_owner_status_idx (org_id, owner_user_id, status) 与
ListAppsByOrg 的 WHERE org_id = ? AND deleted_at IS NULL ORDER BY
created_at DESC 不对齐。重建为 apps_org_active_created_idx
(org_id, deleted_at, created_at DESC)，谓词下推与排序顺序均匹配。

注意：本迁移未带 CONCURRENTLY；生产部署需 DBA 手工补。

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

## 报告（≤100 字）：迁移文件 + up/down 验证结果 + commit SHA

---

## Task 6: B-5 DeleteUser 派生独立短超时 ctx

**Files:**
- Modify: `internal/service/organization_service.go`

- [ ] **Step 6.1: 看现状**

```bash
grep -nE 'DeleteUser|provisionNewAPIUser|context\.Background' internal/service/organization_service.go | head -20
```

定位 `provisionNewAPIUser`（或类似函数）中失败时的 best-effort `DeleteUser` 调用点（spec 调研约 134-161 行）。

- [ ] **Step 6.2: baseline 测试**

```bash
go test ./internal/service/... -count=1 -run 'TestCreateOrganization\|TestProvision'
```

预期全绿。

- [ ] **Step 6.3: 改 best-effort 清理路径**

读现有清理代码（应该类似）：

```go
// 改前：复用原 ctx，原 ctx 取消后清理也被中止
if cleanupErr := s.newapiClient.DeleteUser(ctx, newapiUserID); cleanupErr != nil {
	// audit 写失败原因
}
```

改后：

```go
// 改后：派生独立 5 秒 ctx
cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
defer cancel()
if cleanupErr := s.newapiClient.DeleteUser(cleanupCtx, newapiUserID); cleanupErr != nil {
	s.logger.WarnContext(ctx, "best-effort 清理 newapi user 失败",
		"newapi_user_id", newapiUserID,
		"error", cleanupErr,
	)
	// 同时保留原有 audit 写入逻辑
}
```

注意：

- `context.Background()` + `WithTimeout(5 * time.Second)`；不继承原 ctx
- `s.logger.WarnContext(ctx, ...)` 仍传**原 ctx** 用于 trace_id 注入（A-4 已建立的链路），**仅 newapiClient 调用用 cleanupCtx**
- 如果原代码没有 logger 字段（OrganizationService 当前可能没有），需先加 logger 字段（同 RuntimeOperationService 模式）；这扩展了改造范围 — 如发现没 logger，**stop 报告 controller**

- [ ] **Step 6.4: vet + test + commit**

```bash
go vet ./internal/service/...
go test ./internal/service/... -count=1
git add internal/service/organization_service.go
# 如果 main.go 同步注入了 logger，也 add main.go
git commit -m "$(cat <<'EOF'
fix(service): CreateOrganization best-effort DeleteUser 派生独立短超时 ctx

体检报告 reviewer 已识别：原 ctx 被取消后，best-effort 清理也会被中止
导致 newapi 残留半成品。改用 context.Background() + 5s timeout 的独立
ctx 调用 newapiClient.DeleteUser；trace_id 仍用原 ctx 注入到 logger。

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

## 报告（≤150 字）：改动行号 + OrganizationService 是否已有 logger 字段（如无则停下） + commit SHA

---

## Task 7: C-2 users.deleted_at（迁移 + sqlc + service 同步）

**Files:**
- Create: `internal/migrations/000009_users_deleted_at.up.sql`
- Create: `internal/migrations/000009_users_deleted_at.down.sql`
- Modify: `internal/store/queries/users.sql`
- Run: `make sqlc-generate`（生成 sqlc 代码）
- Possibly modify: `AGENTS.md`（语义说明，本 task 暂不动，留 Task 11）

注意编号：Task 5 已用 000010，本 task 用 000009 才对（因为 spec 写作时假设 000009 是 users.deleted_at、000010 是 apps 索引）。**如果 Task 5 已先做用了 000010**，且 000009 还空着，本 task 就用 000009。如果 Task 5 因某种原因占了 000009，本 task 用 000011。**先 ls 一下确认**。

- [ ] **Step 7.1: 确认迁移编号**

```bash
ls internal/migrations/ | sort | tail -5
```

按上面注释决定本 task 用什么编号。下面命令以 `000009` 为例。

- [ ] **Step 7.2: 写迁移 up.sql**

新建 `internal/migrations/000009_users_deleted_at.up.sql`：

```sql
-- users 加 deleted_at 字段，语义为「下线时间戳」（即 status=disabled 同步设置）。
-- 与 organizations.deleted_at「真删除时间」语义不同，AGENTS.md 已加约定。
ALTER TABLE users ADD COLUMN deleted_at TIMESTAMPTZ NULL;

-- 加部分索引：仅活跃用户（deleted_at IS NULL）的查询走该索引
CREATE INDEX users_active_idx ON users(deleted_at) WHERE deleted_at IS NULL;
```

- [ ] **Step 7.3: 写迁移 down.sql**

新建 `internal/migrations/000009_users_deleted_at.down.sql`：

```sql
DROP INDEX IF EXISTS users_active_idx;
ALTER TABLE users DROP COLUMN deleted_at;
```

- [ ] **Step 7.4: 修改 sqlc query**

读 `internal/store/queries/users.sql`，找 `SetUserStatus` query。

改动：

```sql
-- 原
-- name: SetUserStatus :one
UPDATE users
SET status = $2,
    updated_at = NOW()
WHERE id = $1
RETURNING *;

-- 改后：disabled 时同步写 deleted_at；enabled 时清空（让重启用户能恢复）
-- name: SetUserStatus :one
UPDATE users
SET status = $2,
    deleted_at = CASE WHEN $2 = 'disabled' THEN NOW() ELSE NULL END,
    updated_at = NOW()
WHERE id = $1
RETURNING *;
```

新增 `SoftDeleteUser` query：

```sql
-- name: SoftDeleteUser :exec
UPDATE users SET deleted_at = NOW(), updated_at = NOW()
WHERE id = $1 AND deleted_at IS NULL;
```

- [ ] **Step 7.5: 跑 sqlc generate**

```bash
make sqlc-generate 2>&1 | tail -5
```

预期：成功，`internal/store/sqlc/users.sql.go` 自动更新含 `SoftDeleteUser` 方法 + `User` struct 含 `DeletedAt` 字段。

如果 sqlc 报错（如 generated.yaml 中映射缺失），按错误调整。

- [ ] **Step 7.6: 跑迁移**

```bash
make migrate-up 2>&1 | tail -5
make migrate-down 2>&1 | tail -5
make migrate-up 2>&1 | tail -5
```

预期：up/down idempotent。

- [ ] **Step 7.7: 同步 service 测试**

`internal/service/member_service_test.go` 中如果有断言 `User.Status == "disabled"` 并依赖 deleted_at == NULL 的测试，可能要同步检查（调用 SetUserStatus 后 user.DeletedAt 不再 NULL）。

```bash
go test ./internal/service/... -count=1 -run 'TestDisable\|TestSetMemberStatus'
```

如有 fail，按业务期望调整测试断言（disabled → DeletedAt 非空）。

- [ ] **Step 7.8: vet + 全量 test + commit**

```bash
go vet ./...
go test ./... -count=1
go build ./...
git add internal/migrations/000009_users_deleted_at.up.sql \
  internal/migrations/000009_users_deleted_at.down.sql \
  internal/store/queries/users.sql \
  internal/store/sqlc/  # sqlc 生成的代码
git add internal/service/member_service_test.go  # 如有改动
git commit -m "$(cat <<'EOF'
feat(db): users 加 deleted_at；DisableUser 同步写 deleted_at

体检报告 C-2：users 表本无 deleted_at；本次：
- 迁移 000009 加 deleted_at TIMESTAMPTZ NULL + users_active_idx 部分索引
- queries/users.sql：SetUserStatus 在 disabled 时 deleted_at = NOW()，
  enabled 时清空（让重启用户能恢复）
- 新增 SoftDeleteUser query 供未来明确软删场景

语义约定：本项目 users.deleted_at = 「下线时间戳」（status=disabled 同步），
与 organizations.deleted_at「真删除时间」语义不同。AGENTS.md 在合集
最后一个 task 加约定段。

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

## 报告（≤200 字）：迁移编号 + sqlc generate 结果 + 测试同步改了几处 + commit SHA

---

## Task 8: B-2 HTTP client BaseHTTPClient 抽象

**Files:**
- Create: `internal/integrations/httpclient/client.go`
- Create: `internal/integrations/httpclient/client_test.go`
- Modify: `internal/integrations/agent/file_client.go`
- Modify: `internal/integrations/newapi/client.go`

**关键约束**：现有 `agent/file_client_test.go` 与 `newapi/client_test.go` 必须保持全过（行为等价是验收硬性指标）。

- [ ] **Step 8.1: baseline 测试**

```bash
go test ./internal/integrations/... -count=1
```

全绿。如有 fail 先停下。

- [ ] **Step 8.2: 新建 httpclient 包**

新建 `internal/integrations/httpclient/client.go`：

```go
// Package httpclient 提供 BaseHTTPClient 共用 HTTP 调用能力。
// agent / newapi 等 integrations 子包通过组合此 client 复用 URL 拼接 /
// 鉴权头注入 / JSON 序列化 / 状态码到 sentinel error 的映射。
package httpclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

// 共用 sentinel error，调用方可用 errors.Is 判断。
var (
	ErrNotFound       = errors.New("资源不存在")
	ErrUnauthorized   = errors.New("未授权或 token 失效")
	ErrConflict       = errors.New("资源冲突")
	ErrUpstream       = errors.New("上游服务异常")
	ErrPayloadInvalid = errors.New("请求体无效")
)

// BaseHTTPClient 共用 HTTP 调用基础类。调用方组合方式持有实例。
type BaseHTTPClient struct {
	BaseURL    string       // 基础 URL，如 "http://agent:7002"
	HTTPClient *http.Client // 自定义 transport / timeout；nil 走 http.DefaultClient
	AuthToken  string       // Bearer token；空则不注入 Authorization
}

// DoJSON 发送 JSON 请求，反序列化响应到 out（可为 nil 跳过反序列化）。
// query 拼接到 path；body 序列化为 JSON；状态码非 2xx 时按 sentinel error 映射。
func (c *BaseHTTPClient) DoJSON(ctx context.Context, method, path string, query url.Values, body any, out any) error {
	u := c.buildURL(path, query)
	var reqBody io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("序列化请求体失败: %w", err)
		}
		reqBody = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, reqBody)
	if err != nil {
		return fmt.Errorf("构造请求失败: %w", err)
	}
	if reqBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	if c.AuthToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.AuthToken)
	}
	resp, err := c.client().Do(req)
	if err != nil {
		return fmt.Errorf("发送请求失败: %w", err)
	}
	defer resp.Body.Close()
	if err := mapStatusToError(resp); err != nil {
		return err
	}
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return fmt.Errorf("反序列化响应失败: %w", err)
		}
	}
	return nil
}

// DoStream 发送请求并把响应 body 流式写入 dst（用于二进制下载）。
func (c *BaseHTTPClient) DoStream(ctx context.Context, method, path string, query url.Values, dst io.Writer) error {
	u := c.buildURL(path, query)
	req, err := http.NewRequestWithContext(ctx, method, u, nil)
	if err != nil {
		return fmt.Errorf("构造请求失败: %w", err)
	}
	if c.AuthToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.AuthToken)
	}
	resp, err := c.client().Do(req)
	if err != nil {
		return fmt.Errorf("发送请求失败: %w", err)
	}
	defer resp.Body.Close()
	if err := mapStatusToError(resp); err != nil {
		return err
	}
	if _, err := io.Copy(dst, resp.Body); err != nil {
		return fmt.Errorf("拷贝响应流失败: %w", err)
	}
	return nil
}

func (c *BaseHTTPClient) buildURL(path string, query url.Values) string {
	u := c.BaseURL + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	return u
}

func (c *BaseHTTPClient) client() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return http.DefaultClient
}

func mapStatusToError(resp *http.Response) error {
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return nil
	case resp.StatusCode == http.StatusNotFound:
		return ErrNotFound
	case resp.StatusCode == http.StatusUnauthorized:
		return ErrUnauthorized
	case resp.StatusCode == http.StatusConflict:
		return ErrConflict
	case resp.StatusCode == http.StatusBadRequest:
		return ErrPayloadInvalid
	default:
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("%w: status=%d body=%s", ErrUpstream, resp.StatusCode, string(body))
	}
}
```

- [ ] **Step 8.3: 写 httpclient 单测**

新建 `internal/integrations/httpclient/client_test.go`：

```go
package httpclient

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDoJSON_happy(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
		assert.Equal(t, "Bearer secret-token", r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"name":"alice"}`))
	}))
	defer server.Close()

	c := &BaseHTTPClient{BaseURL: server.URL, AuthToken: "secret-token"}
	var out struct{ Name string `json:"name"` }
	err := c.DoJSON(context.Background(), http.MethodPost, "/users", nil, map[string]string{"x": "y"}, &out)
	require.NoError(t, err)
	assert.Equal(t, "alice", out.Name)
}

func TestDoJSON_404_returnsNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()
	c := &BaseHTTPClient{BaseURL: server.URL}
	err := c.DoJSON(context.Background(), http.MethodGet, "/x", nil, nil, nil)
	assert.True(t, errors.Is(err, ErrNotFound))
}

func TestDoJSON_401_returnsUnauthorized(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()
	c := &BaseHTTPClient{BaseURL: server.URL}
	err := c.DoJSON(context.Background(), http.MethodGet, "/x", nil, nil, nil)
	assert.True(t, errors.Is(err, ErrUnauthorized))
}

func TestDoJSON_500_returnsUpstream(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`upstream details`))
	}))
	defer server.Close()
	c := &BaseHTTPClient{BaseURL: server.URL}
	err := c.DoJSON(context.Background(), http.MethodGet, "/x", nil, nil, nil)
	assert.True(t, errors.Is(err, ErrUpstream))
	assert.Contains(t, err.Error(), "upstream details")
}

func TestDoStream_happy(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("binary content"))
	}))
	defer server.Close()
	c := &BaseHTTPClient{BaseURL: server.URL}
	var buf strings.Builder
	err := c.DoStream(context.Background(), http.MethodGet, "/x", nil, &buf)
	require.NoError(t, err)
	assert.Equal(t, "binary content", buf.String())
}
```

注意：本测试**已经用 testify**（spec 强制：新增测试一律 testify）。go.mod 加 require：

```bash
cd /home/hujing/dir/software/ywjs/oc-manager
go list -m -versions github.com/stretchr/testify | tail -3   # 找最新稳定版
go get github.com/stretchr/testify@latest
go mod tidy
```

- [ ] **Step 8.4: 改 file_client.go 用 BaseHTTPClient**

读 `internal/integrations/agent/file_client.go` 现状（约 475 行）。

改造策略：

1. struct 加 `base *httpclient.BaseHTTPClient` 字段
2. 构造函数（`NewFileClient(...)`）内部 build base
3. 各 method（GetFile / ListDir / DeleteFile 等）内部从手写 `http.NewRequest` + `Do` + `decode` 改为调用 `c.base.DoJSON(...)` 或 `c.base.DoStream(...)`
4. 共用 sentinel error：保留原有的（如 file_client 自定义的 sentinel），但 ErrNotFound / ErrUnauthorized 等通用的可以用 `httpclient.ErrXxx`

例：

```go
// 改前
func (c *FileClient) GetFile(ctx context.Context, appID, path string) (io.Reader, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpoint("/files/"+appID+"/"+path), nil)
	if err != nil { return nil, err }
	c.authorize(req)
	resp, err := c.httpClient.Do(req)
	if err != nil { return nil, err }
	defer resp.Body.Close()
	if err := c.expectSuccess(resp); err != nil { return nil, err }
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, resp.Body); err != nil { return nil, err }
	return &buf, nil
}

// 改后
func (c *FileClient) GetFile(ctx context.Context, appID, path string) (io.Reader, error) {
	var buf bytes.Buffer
	if err := c.base.DoStream(ctx, http.MethodGet, "/files/"+appID+"/"+path, nil, &buf); err != nil {
		return nil, err
	}
	return &buf, nil
}
```

逐个 method 重构。重构期间 frequent build / test。

- [ ] **Step 8.5: 改 newapi/client.go 用 BaseHTTPClient**

同 Step 8.4 模式。

注意：

- newapi/client.go 内部有 5 个 sentinel error（ErrNotFound 等），它们与 httpclient.ErrXxx 同名但**不同包**，不冲突。可以选择保留 newapi 自己的（让 newapi 包内部代码用 newapi.ErrXxx）；也可以让 newapi.ErrXxx 直接 alias 到 httpclient.ErrXxx：

```go
// 选项 A：保留独立 sentinel
var ErrNotFound = errors.New("...")  // 不动

// 选项 B：alias 到 httpclient
var ErrNotFound = httpclient.ErrNotFound
```

推荐 B（统一行为）；如果 newapi 调用方 `errors.Is(err, newapi.ErrNotFound)` 用 alias 后仍正确（alias 是同一个 error 实例），无 break。

- [ ] **Step 8.6: 跑 baseline 测试确认无回归**

```bash
go test ./internal/integrations/... -count=1
```

预期：file_client_test.go / newapi/client_test.go 全部保持 PASS。如有 fail，是行为变化（如 header 顺序、Content-Type 默认值），定位修复 BaseHTTPClient 实现。

- [ ] **Step 8.7: vet + 全量 test + commit**

```bash
go vet ./...
go test ./... -count=1
go build ./...
git add internal/integrations/httpclient/ internal/integrations/agent/file_client.go internal/integrations/newapi/client.go go.mod go.sum
git commit -m "$(cat <<'EOF'
refactor(integrations): 抽出 BaseHTTPClient 复用 agent/newapi HTTP 路径

新建 internal/integrations/httpclient 包：
- BaseHTTPClient.DoJSON / DoStream 共用 URL 拼接、Bearer 鉴权、JSON
  序列化、状态码到 sentinel error 映射
- 共用 5 个 sentinel：ErrNotFound / ErrUnauthorized / ErrConflict /
  ErrUpstream / ErrPayloadInvalid

agent/file_client.go 与 newapi/client.go 改用组合（持有 *BaseHTTPClient
字段），原手写 NewRequest + Do + status check 重构为 base.DoJSON/DoStream
调用。newapi 包内 5 个 sentinel alias 到 httpclient.ErrXxx 统一行为。

新增 httpclient 单测用 testify（spec 强制：新测试一律 testify）。

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

## 报告（≤300 字）：httpclient 单测用例数 + file_client/newapi/client 重构后行数变化 + go.mod testify 版本 + commit SHA

---

## Task 9: B-3 handler 测试补齐 11 个文件

**Files:**
- Create: 11 个 `internal/api/handlers/<name>_test.go`：
  - `apps_test.go` / `channels_test.go` / `persona_test.go` / `usage_test.go`
  - `jobs_test.go` / `platform_overview_test.go` / `workspace_test.go` / `app_runtime_test.go`
  - `knowledge_test.go` / `recharge_test.go` / `files_test.go`（如有 files 端点）

**关键约束**：

- 每个文件至少 3 个用例：happy / forbidden / not-found
- mock 风格沿用现有 `members_test.go` / `runtime_nodes_test.go`
- 用 testify assert/require（spec C-1 决策已要求新测试 testify）
- 复杂场景（workspace 二进制流 / app_runtime 复杂 service 交互）可裁剪：仅 happy + 最关键的失败路径

- [ ] **Step 9.1: 看现有 handler 测试模板**

```bash
head -80 internal/api/handlers/members_test.go
head -80 internal/api/handlers/runtime_nodes_test.go
```

记录：
- import 列表（testify / httptest / gin 等）
- mock store 怎么构造（newMembersStub() 之类）
- gin context 怎么注入 Principal（withPrincipal helper）
- 断言风格

- [ ] **Step 9.2: 11 个文件子分批**

每个文件单独建 + 3 个用例。建议子分批顺序（核心业务在前）：

1. `apps_test.go`（核心）
2. `channels_test.go`（核心）
3. `persona_test.go`（核心）
4. `usage_test.go`（核心）
5. `knowledge_test.go`
6. `recharge_test.go`
7. `jobs_test.go`
8. `platform_overview_test.go`
9. `workspace_test.go`（复杂）
10. `app_runtime_test.go`（复杂）
11. `files_test.go`（如存在）

每个文件实施流程：
1. 读对应 handler 文件，识别公共方法（grep `^func (h \*XxxHandler)`）
2. 选 3 个最关键 endpoint 写 happy/forbidden/not-found
3. mock 实现该 handler 的 store interface
4. 用 testify assert/require 写断言

例 `apps_test.go` 模板：

```go
package handlers_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"oc-manager/internal/api/handlers"
	"oc-manager/internal/auth"
	"oc-manager/internal/service"
)

func init() { gin.SetMode(gin.TestMode) }

// appsStub 是 AppsHandler 依赖的 service interface mock。
// 字段命名 / 方法签名按 handlers.AppsHandler 实际依赖调整。
type appsStub struct {
	listResult []service.AppResult
	listErr    error
	getResult  *service.AppResult
	getErr     error
}

func (s *appsStub) ListApps(ctx interface{}, principal auth.Principal, orgID string) ([]service.AppResult, error) {
	return s.listResult, s.listErr
}
// ... 其他方法

func newAppsHandler(stub *appsStub) (*gin.Engine, *handlers.AppsHandler) {
	r := gin.New()
	h := handlers.NewAppsHandler(stub /* + 其他依赖 */)
	h.RegisterAppsRoutes(r)
	return r, h
}

func withPrincipal(req *http.Request, p auth.Principal) *http.Request {
	// 按现有 handlers tests 中的实际 helper 改
	return req
}

func TestAppsHandler_List_happy(t *testing.T) {
	stub := &appsStub{listResult: []service.AppResult{{ID: "app-1", Name: "test"}}}
	r, _ := newAppsHandler(stub)
	req := httptest.NewRequest(http.MethodGet, "/apps?org_id=org-A", nil)
	req = withPrincipal(req, auth.Principal{Role: "platform_admin"})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
}

func TestAppsHandler_List_forbidden(t *testing.T) {
	stub := &appsStub{listErr: service.ErrForbidden}
	r, _ := newAppsHandler(stub)
	req := httptest.NewRequest(http.MethodGet, "/apps?org_id=other-org", nil)
	req = withPrincipal(req, auth.Principal{Role: "org_member", OrgID: "org-A"})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestAppsHandler_Get_notFound(t *testing.T) {
	stub := &appsStub{getErr: service.ErrNotFound}
	r, _ := newAppsHandler(stub)
	req := httptest.NewRequest(http.MethodGet, "/apps/missing", nil)
	req = withPrincipal(req, auth.Principal{Role: "platform_admin"})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}
```

**注意 mock 接口**：每个 handler 依赖的 service interface 不同，mock 必须实现完整 interface（用 grep 看 handlers/<name>.go 中 `type XxxStore interface`）。如某 mock 接口非常大（>10 个方法），可以仅 stub 测试用到的方法，其他方法返回零值（用 panic("不该被调用") 防呆也行）。

- [ ] **Step 9.3: 逐文件实施 + frequent build/test**

每加一个 handler 测试文件就跑：

```bash
go test ./internal/api/handlers/... -count=1 -run 'TestApps' 2>&1 | tail -5
```

确认这批用例全过再继续下一文件。

- [ ] **Step 9.4: 全量 vet + test + commit**

```bash
go vet ./...
go test ./... -count=1 2>&1 | tail -5
git add internal/api/handlers/
git commit -m "$(cat <<'EOF'
test(api): 11 个 handler 补齐 testify 单测（happy + forbidden + not-found）

补齐：apps / channels / persona / usage / knowledge / recharge / jobs /
platform_overview / workspace / app_runtime / files 共 33+ 个用例。

mock 风格沿用 members_test.go / runtime_nodes_test.go；新写测试一律
用 testify assert/require（C-1 spec 强制）。复杂场景（workspace 二进制
流 / app_runtime）按 happy + 关键失败路径裁剪覆盖。

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

## 报告（≤350 字）：每文件用例数清单 + 总用例数 + 可能跳过的 file_test.go（如该 handler 不存在） + commit SHA

---

## Task 10: C-1 testify 全量 71 文件迁移

**Files:**
- Modify: 71 个 `*_test.go` 文件（全仓 internal/ runtime/ 下，排除 cmd/）

**关键约束**：

- 每文件迁移后立即跑 `go test` 确认行为等价
- 替换是机械的，但需注意 `assert.*` vs `require.*` 选择：
  - 错误检查（`if err != nil { t.Fatalf(...) }`）→ `require.NoError(t, err)`（fail 即停）
  - 字段断言后续还有验证 → `assert.Equal(t, want, got)`（fail 也继续）
  - panic 防御（如指针断言后立即 deref）→ `require.NotNil(t, ptr)`

- [ ] **Step 10.1: go.mod 加 testify require（如 Task 8 已加则跳过）**

```bash
grep 'github.com/stretchr/testify' go.mod
```

如果命中，已加。否则：

```bash
go get github.com/stretchr/testify@latest
go mod tidy
```

- [ ] **Step 10.2: 列出待迁移文件**

```bash
git grep -lE 'func Test[A-Z]' internal/ runtime/ | grep -v _test.go.bak | sort
```

预期 ~71 个。记录清单。

- [ ] **Step 10.3: 子分批迁移**

71 个文件按目录分批：

1. `internal/auth/`（少数文件）
2. `internal/log/`（少数文件）
3. `internal/api/middleware/`
4. `internal/api/handlers/`（含 Task 9 已用 testify 的 11 个新文件 — 不需要再迁）
5. `internal/service/`（最大批，含 top 5 高密度文件）
6. `internal/worker/` `internal/worker/handlers/` `internal/scheduler/`
7. `internal/integrations/`（含 newapi/client_test.go 457 行）
8. `internal/store/` `internal/audit/` `internal/redis/` 等
9. `runtime/agent/`

每个批次内逐文件机械替换。**每文件迁移完跑 `go test ./<对应包路径>/... -count=1` 确认全过再继续下一文件**。如果一个文件迁完测试 fail，回滚那个文件的改动（`git checkout`），跳过它，最后向 controller 报告。

- [ ] **Step 10.4: 替换规则参考**

```go
// 错误检查
if err != nil { t.Fatalf("err: %v", err) }
→ require.NoError(t, err)

if err == nil { t.Fatalf("expected err") }
→ require.Error(t, err)

if !errors.Is(err, ErrXxx) { t.Fatalf("...") }
→ require.ErrorIs(t, err, ErrXxx)

// 等值断言
if got != want { t.Errorf("got %v want %v", got, want) }
→ assert.Equal(t, want, got)
// 注意：assert.Equal(t, expected, actual) 顺序与 t.Errorf 反过来

// 不等
if got == bad { t.Errorf("...") }
→ assert.NotEqual(t, bad, got)

// 长度
if len(s) != 3 { t.Fatalf(...) }
→ assert.Len(t, s, 3)

// nil 检查
if x == nil { t.Fatalf(...) }
→ require.NotNil(t, x)

// 字符串包含
if !strings.Contains(s, "xxx") { t.Errorf(...) }
→ assert.Contains(t, s, "xxx")

// bool
if !ok { t.Fatalf(...) }
→ require.True(t, ok)

if ok { t.Fatalf(...) }
→ require.False(t, ok)
```

- [ ] **Step 10.5: 全仓验证**

迁移完成后：

```bash
git grep -nE 't\.(Errorf|Fatalf)\(' internal/ runtime/ | wc -l
```

预期：大幅下降（从 1070 降到接近 0；可能少量 helper / table-driven 内的复杂消息保留 t.Errorf 是 OK）。

```bash
go vet ./...
go test ./... -count=1 2>&1 | tail -5
go build ./...
```

预期：全过。

- [ ] **Step 10.6: 自检 + commit**

```bash
git status --short
git diff --stat | tail -10
```

预期：71 个 *_test.go 文件改动。

```bash
git add internal/ runtime/ go.mod go.sum
git commit -m "$(cat <<'EOF'
test: 全量 71 个测试文件迁移到 testify assert/require

C-1 spec 决策：1070 处 t.Fatal/Error 机械替换为 testify assert.* /
require.*。错误检查用 require.NoError / require.Error / require.ErrorIs；
等值断言用 assert.Equal；后续步骤依赖此值不能继续时用 require.*。

新增 / 重构测试今后强制 testify（AGENTS.md 在 Task 11 加约定）。

go.mod 加 require github.com/stretchr/testify。

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

## 报告（≤300 字）：迁移文件总数 / t.Fatalf grep 减少量 / 任何 fail 后回滚的文件清单 / commit SHA

---

## Task 11: AGENTS.md 约定 + DoD 全量验收

**Files:**
- Modify: `AGENTS.md`

- [ ] **Step 11.1: AGENTS.md 加两条约定**

读 AGENTS.md 现状，在合适位置（建议「权限校验」之后、「OpenAPI 同步」之前或类似位置）追加：

```markdown
## 测试断言

- 新增 / 重构单元测试一律使用 `github.com/stretchr/testify` 的 `assert` /
  `require`：错误检查用 `require.NoError` / `require.Error` / `require.ErrorIs`；
  等值断言用 `assert.Equal`（注意顺序：expected 在前）；后续依赖此值不能继续时
  用 `require.*` 让 fail 立即停止。
- stdlib `t.Fatalf` / `t.Errorf` 仅在极个别 helper / table-driven 复杂格式化场景
  保留；不再做新增。

## users.deleted_at 语义

- `users.deleted_at` 字段语义为「下线时间戳」（即 `status=disabled` 时由 SQL
  自动设置 `deleted_at = NOW()`，重新启用时清空）。**与 `organizations.deleted_at`
  「真删除时间」语义不同**。
- 查询活跃用户：`WHERE deleted_at IS NULL`；用部分索引 `users_active_idx`。
- 真软删除场景（如未来要做「彻底下线、不可恢复」）用 `SoftDeleteUser` query。
```

- [ ] **Step 11.2: 跑 DoD 验收**

逐项验证 spec 第 9 节 DoD：

```bash
echo "=== DoD-1: errors.go 含 25 个 sentinel；其他 service 文件无 var Err 定义 ==="
grep -c '^var Err' internal/service/errors.go
grep -nE '^var Err[A-Z]' internal/service/*.go | grep -v 'errors.go'
echo "(预期最后一行空)"

echo "=== DoD-2: httpclient 包存在；agent/newapi 已用组合 ==="
ls internal/integrations/httpclient/
grep -nE '\*httpclient\.BaseHTTPClient|httpclient\.BaseHTTPClient' internal/integrations/agent/file_client.go internal/integrations/newapi/client.go | head -5

echo "=== DoD-3: 11 个 handler 测试文件存在 ==="
for f in apps channels persona usage knowledge recharge jobs platform_overview workspace app_runtime files; do
  [ -f "internal/api/handlers/${f}_test.go" ] && echo "  $f ✓" || echo "  $f ✗"
done

echo "=== DoD-4: apps 索引重建迁移 ==="
ls internal/migrations/000010* 2>/dev/null

echo "=== DoD-5: provisionNewAPIUser 用 context.Background() + Timeout ==="
grep -A 3 -E 'context\.Background' internal/service/organization_service.go | head -8

echo "=== DoD-6: testify 全仓覆盖 ==="
git grep -cE 't\.(Errorf|Fatalf)\(' internal/ runtime/ 2>&1 | wc -l
git grep -lE 'github.com/stretchr/testify' internal/ runtime/ | wc -l

echo "=== DoD-7: users 表有 deleted_at；SetUserStatus 同步 ==="
grep 'deleted_at' internal/store/queries/users.sql | head -3

echo "=== DoD-8: app_state_machine 头部含状态转移文档 ==="
head -20 internal/domain/app_state_machine.go | grep -E '状态机|draft|deleted'

echo "=== DoD-9: newapi_audit slog msg 不含 newapi_audit 前缀 ==="
grep -E 'slog.*newapi_audit:' internal/audit/newapi_audit.go || echo "(空 = 已删)"

echo "=== DoD-10: hbLoggerAdapter 接 *slog.Logger ==="
grep -nE 'hbLoggerAdapter|stdHBLogger' runtime/agent/heartbeat.go

echo "=== DoD-11: 全量 test / vet / build ==="
go vet ./...
go test ./... -count=1 2>&1 | tail -5
go build ./...

echo "=== DoD-12: AGENTS.md 含 testify + users.deleted_at 语义两段 ==="
grep -A 3 '^## 测试断言' AGENTS.md
grep -A 3 '^## users.deleted_at 语义' AGENTS.md
```

记录每项结果。如某项失败 stop 报告 controller。

- [ ] **Step 11.3: commit AGENTS.md**

```bash
git add AGENTS.md
git commit -m "$(cat <<'EOF'
docs(agents): 增加 testify 与 users.deleted_at 语义两段约定

testify：新增 / 重构单元测试一律 assert.* / require.*；错误检查用
require.NoError / require.ErrorIs；后续依赖断言用 require.*。

users.deleted_at：本项目语义为「下线时间戳」（status=disabled 同步），
与 organizations.deleted_at「真删除时间」语义不同；查询活跃用户用
WHERE deleted_at IS NULL（走 users_active_idx 部分索引）。

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

## 报告（≤350 字）：12 项 DoD 逐项 Y/N + commit SHA

---

## 完成定义

所有 task 完成后必须满足 spec 第 9 节的 12 项 DoD（Task 11 Step 11.2 已逐项验证）。

---

## 回滚策略

每个 task 一次独立 commit，可单独 `git revert`。

风险点：
- Task 4（sentinel 集中）涉及 11 个 service 文件改动；如有问题 revert 此一 commit 即完整回滚
- Task 8（HTTP client 抽象）涉及 file_client + newapi/client 两个 client 重构；现有 client 测试是回归网
- Task 9 / Task 10（机械大项）单独 revert 不影响其他 task

---

## 风险与应对

| 风险 | 何时出现 | 应对 |
|---|---|---|
| Task 3 stdHBLogger 接口扩散到多处 | Task 3 Step 3.1 | grep 验证；扩散立即 stop 报告 controller |
| Task 4 某 service 文件 import "errors" 删后还需要（如 errors.Is(err, pgx.ErrNoRows)） | Task 4 Step 4.5 | 编译期立即暴露；逐文件 grep 修复 |
| Task 6 OrganizationService 当前没有 logger 字段 | Task 6 Step 6.3 | stop 报告 controller；如需先加 logger 字段，扩展本 task 范围 |
| Task 7 sqlc generate 与 service 测试断言不对齐（DisableUser → DeletedAt） | Task 7 Step 7.7 | 同步调整测试断言；如不能确定语义，stop 报告 |
| Task 8 BaseHTTPClient 抽象后行为细微偏差 | Task 8 Step 8.6 | 现有 file_client / newapi/client 测试必须保持全过；fail 是行为变化的证据 |
| Task 9 mock interface 太大不便实现 | Task 9 Step 9.2 | 仅 stub 测试用到的方法，其他用 panic("不该被调用") 防呆；如某 handler interface 巨大且不便剪裁，stop 报告 |
| Task 10 某个文件迁移后测试 fail | Task 10 Step 10.3 | 回滚那个文件改动，跳过它；最后报告 controller 哪些文件未迁 |
| Task 10 testify 全量迁移期间其他人并行改测试导致冲突 | Task 10 期间 | 一次 PR 完成；commit message 提示 reviewer 不要并行修改测试文件 |
| Task 11 DoD 某项失败 | Task 11 Step 11.2 | stop 报告 controller，定位失败的 task 修复 |
