# 后端清理合集（B+C 类）

- 日期：2026-05-09
- 范围：Go 后端
- 主线项编号：B+C 后端合集（出自 2026-05-09 全面体检报告）

## 1. 背景

A-1..A-4 主线已完成。剩余的次/低优先级清理项打包成本合集。10 项改造，机械与设计性混合，工作量大但每项独立可回退。

调研改写了几个前提：
- `internal/service` 层注释**已 100% 中文**（C-1 注释统一项 → 已完成，从合集去除）
- 当前散落 sentinel：25 个分布于 11 个 service 文件 + 5 个在 `internal/integrations/newapi`（不归本次）
- `fmt.Errorf("%w")` 全仓 315 次（spec 风险报告原估 156 次低估一半）
- `testify` 实际 0 处使用，`go.sum` 仅作为间接依赖；`t.Fatal/Error` 1070 处分布于 71 个 `_test.go` 文件
- `handler` 缺测试 11 个文件
- `users` 表无 `deleted_at`，6 个相关 SQL query 完全不涉及软删

## 2. 目标

10 项改造打包：

| ID | 项 | 类 | 工作量估算 |
|---|---|---|---|
| B-1 | 25 个 service sentinel error 集中到 `internal/service/errors.go` | B | 中 |
| B-2 | HTTP client `BaseHTTPClient` 抽象，`agent/file_client` 与 `newapi/client` 复用 | B | 中-大 |
| B-3 | handler 测试补齐 11 个文件（每个 3 用例：happy + forbidden + not-found） | B | 大 |
| B-4 | `apps_org_owner_status_idx` 重建为 `(org_id, deleted_at, created_at DESC)` | B | 小 |
| B-5 | `CreateOrganization` 的 best-effort `DeleteUser` 回滚派生独立短超时 ctx | B | 小 |
| C-1 | 全量 71 文件 testify 迁移；AGENTS.md 加新测试 testify 约定 | C | 大（机械） |
| C-2 | `users` 表加 `deleted_at` + `SoftDeleteUser` query；`DisableUser` 同步设 `deleted_at` | C | 中 |
| C-3 | `app_state_machine.go` 状态转移文档（注释/markdown） | C | 小 |
| C-4 | `newapi_audit.go` slog msg 删除 `"newapi_audit:"` 包名前缀冗余（A-4 nit） | C | 极小 |
| C-5 | `runtime/agent/heartbeat.go` `stdHBLogger` 改造为 `*slog.Logger` 字段（A-4 nit） | C | 小 |

## 3. 非目标（避免范围蔓延）

- **不**改动 `internal/integrations/newapi` 包内的 5 个 sentinel（与 service 层不同业务边界）
- **不**改 audit_logs 业务审计 schema 或逻辑
- **不**引入新依赖（testify 是 Go 生态标准，已在 go.sum；其他无）
- **不**做 OTel / metrics / tracing 扩展（A-4 已确认非目标）
- **不**改 `worker.Pool` / `scheduler.Loop` API（A-4 已切 slog，本次不动）
- **不**重写 sqlc 生成代码（仅扩 query，重新跑 sqlc generate）
- **不**对 71 个测试文件做语义改造（仅 testify 形式替换：`t.Fatalf("got X want Y", x, y)` → `assert.Equal(t, y, x)`）

## 4. 关键决策（已与决策方对齐）

| 决策点 | 选择 | 理由 |
|---|---|---|
| sentinel 整理粒度 | **25 个全部集中到 `internal/service/errors.go`** | 一锁起门：独立错误名间接依赖清零；同包内移动无 import 改动 |
| testify 迁移范围 | **全量 71 文件迁移** + AGENTS.md 加新测试约定 | 用户明确要求一次性整理；机械工作可由 implementer 批量处理 |
| handler 测试补齐 | **11 个全补**（happy + forbidden + not-found 三件套起步） | 全覆盖；workspace / app_runtime 复杂场景按现有 service test 模式 mock |
| `users.deleted_at` | **加字段 + 改 `DisableUser` 同步设 `deleted_at`** | 用户语义决定：`disabled` 即视为软删；`deleted_at` 字段本质是「下线时间戳」而非传统语义的「真删除时间」 |

## 5. 设计

### 5.1 文件结构

#### 修改 / 整理（B-1, C-1, C-2, C-3, C-4, C-5）

```
internal/service/errors.go                        ← 集中 25 个 sentinel error（B-1）
internal/service/auth_service.go                  ← 删本地 sentinel，import 同包 errors.go 命名
internal/service/member_service.go                ← 同上
internal/service/workspace_service.go             ← 同上
internal/service/runtime_node_service.go          ← 同上
internal/service/app_service.go                   ← 同上
internal/service/runtime_operation_service.go     ← 同上
internal/service/persona_service.go               ← 同上
internal/service/channel_service.go               ← 同上
internal/service/organization_service.go          ← 同上 + B-5（DeleteUser 独立 ctx）
internal/service/onboarding_service.go            ← 同上
internal/service/recharge_service.go              ← 同上

internal/store/queries/users.sql                  ← 新增 SoftDeleteUser query（C-2）
internal/migrations/000009_users_deleted_at.up.sql        ← 新建（C-2）
internal/migrations/000009_users_deleted_at.down.sql      ← 同上
internal/migrations/000010_apps_index_rebuild.up.sql      ← 新建（B-4）
internal/migrations/000010_apps_index_rebuild.down.sql    ← 同上

internal/domain/app_state_machine.go              ← 头部加状态转移文档块（C-3）

internal/audit/newapi_audit.go                    ← slog msg 删 "newapi_audit:" 前缀（C-4）

runtime/agent/heartbeat.go                        ← stdHBLogger 改 *slog.Logger 字段（C-5）

AGENTS.md                                         ← 加 testify 与错误规范化两条约定
```

#### 新建（B-2 HTTP client 抽象）

```
internal/integrations/httpclient/client.go        ← 新建：BaseHTTPClient + 共用 helper
internal/integrations/httpclient/client_test.go   ← 单测
internal/integrations/agent/file_client.go        ← 改：用 BaseHTTPClient 重写 HTTP 路径
internal/integrations/newapi/client.go            ← 改：同上
```

#### 测试新建（B-3 handler 测试补齐）

```
internal/api/handlers/apps_test.go                ← 新建（happy + forbidden + not-found）
internal/api/handlers/channels_test.go            ← 同上
internal/api/handlers/persona_test.go             ← 同上
internal/api/handlers/usage_test.go               ← 同上
internal/api/handlers/jobs_test.go                ← 同上
internal/api/handlers/platform_overview_test.go   ← 同上
internal/api/handlers/workspace_test.go           ← 同上
internal/api/handlers/app_runtime_test.go         ← 同上
internal/api/handlers/knowledge_test.go           ← 同上
internal/api/handlers/recharge_test.go            ← 同上
internal/api/handlers/files_test.go               ← 如有 files 端点，否则跳
```

#### 测试形式改造（C-1 testify 迁移）

71 个 `*_test.go` 文件全部 `t.Fatal/Error → testify/assert/require` 形式替换。

### 5.2 B-1 sentinel error 集中

**最终 errors.go 形态**（含原 3 个 + 集中的 22 个）：

```go
// Package service 的所有可被 handler 层 errors.Is 检查的 sentinel error。
// 业务模块需要新增错误时优先扩展本文件，不要在各 service 文件内再定义本地 sentinel。
package service

import "errors"

// 通用错误 ----------------------------------------------------------

var ErrForbidden = errors.New("无权访问")
var ErrNotFound = errors.New("资源不存在")
var ErrConflict = errors.New("资源冲突")            // 新增

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

// （其他 sentinel 按调研实际清单补齐）
```

注意：

- 原各 service 文件 `var Err... = errors.New(...)` 删除，引用点改用 `service.ErrXxx`（**同包，无需 prefix**）
- handler 层 `errors.Is(err, service.ErrXxx)` 不变（只是物理位置变了，命名不变）
- 部分 sentinel 与 newapi 包同名（如 `ErrNotFound`）— 它们已在不同包，不冲突

### 5.3 B-2 BaseHTTPClient 抽象

`internal/integrations/httpclient/client.go`：

```go
package httpclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
)

// BaseHTTPClient 提供共用 HTTP 调用能力：
//   - URL 拼接（基础 URL + path + query）
//   - 鉴权头注入（Bearer / 自定义 header）
//   - 请求体 JSON 序列化
//   - 响应反序列化
//   - 状态码到 sentinel error 的映射（调用方传入 mapper）
//
// 调用方（agent/file_client、newapi/client）以组合方式持有 BaseHTTPClient 实例，
// 不通过继承共享代码。共有的 5 个 sentinel error 也在此包提供。
type BaseHTTPClient struct {
	BaseURL    string
	HTTPClient *http.Client
	AuthToken  string // Bearer token；空则不注入
}

// 共用 sentinel error，调用方可用 errors.Is 判断
var (
	ErrNotFound       = errors.New("资源不存在")
	ErrUnauthorized   = errors.New("未授权或 token 失效")
	ErrConflict       = errors.New("资源冲突")
	ErrUpstream       = errors.New("上游服务异常")
	ErrPayloadInvalid = errors.New("请求体无效")
)

// DoJSON 发送 JSON 请求，反序列化响应到 out（如非 nil）。
// 状态码非 2xx 时按 ErrXxx 映射；body 解析失败回 ErrPayloadInvalid。
func (c *BaseHTTPClient) DoJSON(ctx context.Context, method, path string, query url.Values, body any, out any) error { ... }

// DoStream 发送请求并把响应 body 流式写入 dst（用于二进制下载）。
func (c *BaseHTTPClient) DoStream(ctx context.Context, method, path string, query url.Values, dst io.Writer) error { ... }
```

agent/file_client 与 newapi/client 改造为「组合 + 委托」：

```go
type FileClient struct {
	base *httpclient.BaseHTTPClient
}

func (c *FileClient) GetFile(ctx context.Context, appID, path string) (io.Reader, error) {
	// 不再手写 http.NewRequestWithContext + Do + status check
	var buf bytes.Buffer
	if err := c.base.DoStream(ctx, http.MethodGet, "/files/"+appID+"/"+path, nil, &buf); err != nil {
		return nil, err
	}
	return &buf, nil
}
```

### 5.4 B-3 handler 测试模板

每个 handler 至少 3 个用例：

```go
// internal/api/handlers/apps_test.go 模板

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

func TestAppsHandler_List_happy(t *testing.T) {
	store := newAppsStub() // mock service interface
	h := handlers.NewAppsHandler(store, ...)
	r := gin.New()
	h.RegisterAppsRoutes(r)

	req := httptest.NewRequest(http.MethodGet, "/apps?org_id=org-A", nil)
	req = withPrincipal(req, auth.Principal{Role: "platform_admin"})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	// 用 testify assert 验证响应字段
}

func TestAppsHandler_List_forbidden(t *testing.T) {
	// org_member 跨组织查询，期望 403
}

func TestAppsHandler_Get_notFound(t *testing.T) {
	// 不存在的 appID，期望 404
}
```

复杂场景（如 workspace 二进制流）按 service 层测试中已有 mock 模式，可裁剪 happy/forbidden 即可，不必 not-found。

### 5.5 B-4 apps 索引重建

`internal/migrations/000010_apps_index_rebuild.up.sql`：

```sql
DROP INDEX IF EXISTS apps_org_owner_status_idx;
CREATE INDEX apps_org_owner_status_active_idx ON apps(org_id, deleted_at, created_at DESC);
```

`down.sql`：

```sql
DROP INDEX IF EXISTS apps_org_owner_status_active_idx;
CREATE INDEX apps_org_owner_status_idx ON apps(org_id, owner_user_id, status);
```

注意：
- 索引名不同（防 down 时与 up 同名冲突）
- **不带 `CONCURRENTLY`**：本地开发环境表很小；上线时如果 apps 行数大，运维操作时手工补 CONCURRENTLY
- AGENTS.md 风险段已有「未来加索引必须 CONCURRENTLY」约定

### 5.6 B-5 DeleteUser 独立 ctx

`organization_service.go` 的 `provisionNewAPIUser` 在失败时 best-effort 调用 `DeleteUser` 清理：

```go
// 改前：复用原 ctx，原 ctx 取消后清理也被中止
if cleanupErr := s.newapiClient.DeleteUser(ctx, ...); cleanupErr != nil { ... }

// 改后：派生独立 5 秒 ctx
cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
defer cancel()
if cleanupErr := s.newapiClient.DeleteUser(cleanupCtx, ...); cleanupErr != nil {
	s.logger.WarnContext(ctx, "best-effort 清理 newapi user 失败",
		"newapi_user_id", newapiUserID,
		"error", cleanupErr,
	)
}
```

注意：
- `context.Background()` + 显式超时（5 秒）；**不**继承原 ctx
- 错误用 slog.WarnContext（A-4 已建立路径），原 ctx 用于注入 trace_id

### 5.7 C-1 testify 迁移

71 个 `_test.go` 文件机械替换：

```go
// 改前
if got != want {
	t.Fatalf("got %v, want %v", got, want)
}

// 改后
assert.Equal(t, want, got, "...")  // 或 require.Equal 当后续步骤依赖此值时
```

替换规则：

- `if got == want { ... } else { t.Errorf/Fatalf(...) }` → `assert.NotEqual(t, want, got, ...)` 形式
- 后续断言 / cleanup 仍要执行 → `assert.*`
- 后续依赖此值（如 nil panic） → `require.*`
- table-driven 内的断言：`assert.Equalf` 含格式化字段
- 错误处理 `if err != nil { t.Fatalf("err: %v", err) }` → `require.NoError(t, err)`

`go.mod` 加：

```
require github.com/stretchr/testify v1.x.y
```

具体版本由 `npm view`-like 命令实时获取（implementer 实施时跑 `go list -m -versions github.com/stretchr/testify`）。

### 5.8 C-2 users.deleted_at

迁移 `000009_users_deleted_at.up.sql`：

```sql
ALTER TABLE users ADD COLUMN deleted_at TIMESTAMPTZ NULL;
CREATE INDEX users_active_idx ON users(deleted_at) WHERE deleted_at IS NULL;
```

`down.sql`：

```sql
DROP INDEX IF EXISTS users_active_idx;
ALTER TABLE users DROP COLUMN deleted_at;
```

`internal/store/queries/users.sql` 加：

```sql
-- name: SoftDeleteUser :exec
UPDATE users SET deleted_at = NOW(), updated_at = NOW()
WHERE id = $1 AND deleted_at IS NULL;
```

`SetUserStatus` query 增加 deleted_at 同步逻辑（决策方明确要求 DisableUser 同步设 deleted_at）：

```sql
-- name: SetUserStatus :one
UPDATE users
SET status = $2,
    deleted_at = CASE WHEN $2 = 'disabled' THEN NOW() ELSE NULL END,
    updated_at = NOW()
WHERE id = $1
RETURNING *;
```

注意：

- 「`disabled` 即视为软删」是用户决策的语义（不是传统 organizations.deleted_at「真删除」语义）
- 重新启用 (`SetUserStatus disabled → active`) 时 deleted_at 重置为 NULL
- service 层 `DisableUser` 不改逻辑（sqlc 改 SQL 后自动同步行为）
- `MemberService.SoftDelete` 当前调 `SetUserStatus(disabled)` — 行为自动获得 deleted_at；如未来要做「真软删」（无法再恢复），用 SoftDeleteUser query 即可

### 5.9 C-3 app_state_machine 文档

`internal/domain/app_state_machine.go` 头部插入文档块：

```go
// Package domain 的 app_state_machine.go 维护应用状态机的转移规则。
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
//   ...
package domain
```

### 5.10 C-4 newapi_audit msg 删前缀

```go
// 改前
slog.ErrorContext(ctx, "newapi_audit: 写 audit_logs 失败", "error", err)
// 改后
slog.ErrorContext(ctx, "写 audit_logs 失败", "error", err)
// caller 路径已由 slog.HandlerOptions{AddSource: true} 自动给出（A-4 已开）
```

### 5.11 C-5 stdHBLogger 改造

`runtime/agent/heartbeat.go` 当前 `stdHBLogger` 用 `Infof`-风格接口适配：

```go
// 改前
type stdHBLogger struct{}
func (stdHBLogger) Infof(format string, args ...any)  { slog.Info(fmt.Sprintf("heartbeat "+format, args...)) }
func (stdHBLogger) Warnf(format string, args ...any)  { slog.Warn(fmt.Sprintf(format, args...)) }
func (stdHBLogger) Errorf(format string, args ...any) { slog.Error(fmt.Sprintf(format, args...)) }

// 改后：直接接 *slog.Logger，调用方按 slog 风格用 key-value
type hbLoggerAdapter struct{ logger *slog.Logger }
func (a *hbLoggerAdapter) Info(msg string, attrs ...any)  { a.logger.Info(msg, attrs...) }
func (a *hbLoggerAdapter) Warn(msg string, attrs ...any)  { a.logger.Warn(msg, attrs...) }
func (a *hbLoggerAdapter) Error(msg string, attrs ...any) { a.logger.Error(msg, attrs...) }
```

**警告**：如改造影响 hbLogger 接口的所有实现方（除 stdHBLogger 外），范围会超本 spec。implementer Step 中先 grep `hbLogger` 接口实现，如发现多个实现方需要级联改动，stop 报告 controller。

## 6. 改造分批策略

10 项映射到 plan 中的 10 个 task，每项独立 commit：

| Task | 项 | 工作量 | 风险 |
|---|---|---|---|
| 1 | C-3 app_state_machine 文档（独立小） | 极小 | 无 |
| 2 | C-4 newapi_audit msg 前缀（独立小） | 极小 | 无 |
| 3 | C-5 stdHBLogger 改造（独立小） | 小 | 接口实现方扩散 |
| 4 | B-1 sentinel error 集中 | 中 | grep 命名扩散；多文件改动 |
| 5 | B-4 apps 索引重建迁移 | 小 | 仅迁移文件 |
| 6 | B-5 DeleteUser 独立 ctx | 小 | 单点改动 |
| 7 | C-2 users.deleted_at（迁移 + sqlc + service） | 中 | 业务语义微妙（disabled 即软删） |
| 8 | B-2 HTTP client 抽象（新建 + 改造 file_client + newapi client） | 中-大 | 现有 client 单测要保持通过 |
| 9 | B-3 handler 测试补齐 11 个文件 | 大 | 工作量集中；可子分批 |
| 10 | C-1 testify 全量迁移 71 文件 | 大（机械） | 一旦其他人并行改测试会冲突 |

**建议按上面顺序做**：先简单独立项（1-3）暖场，再 B 类核心（4-8），最后两个机械大项（9-10）。

## 7. 测试策略

- **B-1 sentinel 集中**：现有所有 `errors.Is(err, ErrXxx)` 路径继续可用（命名不变，仅位置变）；service 测试不需要新增
- **B-2 HTTP client**：新建 `httpclient/client_test.go` 覆盖 DoJSON / DoStream / 状态码映射；agent/file_client 与 newapi/client 现有测试保持通过（行为等价）
- **B-3 handler 测试**：新建 33+ 用例（11 文件 × 3 用例）；用 testify assert/require
- **B-4 索引重建**：跑 `make migrate-up && make migrate-down && make migrate-up` 验证 idempotent
- **B-5 DeleteUser ctx**：现有 organization_service_test.go 中 OOS-3 测试保持通过
- **C-1 testify 迁移**：每个文件迁移后立即跑 `go test` 确认行为等价
- **C-2 users.deleted_at**：现有 member_service_test 中 DisableMember/SetMemberStatus 测试可能需要更新断言（添加 deleted_at 字段验证）
- **C-3/C-4/C-5**：纯文档/格式改造，现有测试保持通过即可

## 8. 风险与缓解

| 风险 | 严重度 | 缓解 |
|---|---|---|
| sentinel 集中后某 service 文件遗漏 import 同包 errors（编译错） | 低 | 编译期立即暴露；逐文件 grep 验证 |
| testify 全量迁移期间其他人并行改测试导致冲突 | 中 | spec 落地后 1 个 PR 完成；commit message 提示 reviewer 不要并行修改测试 |
| handler 测试 mock 与 service interface 不对齐 | 中 | 参考现有 members_test.go / runtime_nodes_test.go mock 模式；如发现 mock interface 太大，stop 报告 controller |
| **C-2 用户语义偏差**：`disabled` 即软删可能让运维误解（"用户被禁用 != 删除"） | 中 | spec 落地时 AGENTS.md 加一行说明：「users.deleted_at 在本项目语义为「下线时间戳」（即 status=disabled 同步设置），与 organizations.deleted_at 真删除语义不同」 |
| HTTP client 抽象后行为细微偏差（如 header 顺序、Content-Type 默认值） | 中 | 现有 file_client / newapi/client 单测必须保持全过；如有 fail 是行为变化的证据 |
| stdHBLogger 接口实现方多于 1 处，本 spec 范围扩散 | 中 | Task 3 Step 1 显式 grep；扩散立即 stop |
| apps 索引重建后某些查询性能下降（实际查询模式与 spec 假设不符） | 低 | 本地开发环境无大数据测试；上线后 explain analyze 看实际效果 |
| `app_state_machine` 文档与代码不同步导致误导 | 低 | 文档紧贴代码（同文件头部）；未来改状态机时一并更新 |

## 9. 完成定义（DoD）

- [ ] **DoD-1:** `internal/service/errors.go` 含 25 个 sentinel；其他 service 文件 grep 不到 `^var Err` 定义（B-1）
- [ ] **DoD-2:** `internal/integrations/httpclient/client.go` 存在，`agent/file_client.go` 与 `newapi/client.go` 用组合（B-2）
- [ ] **DoD-3:** 11 个 handler 文件都有对应 `_test.go`，每个至少 3 个用例（B-3）
- [ ] **DoD-4:** `apps_org_owner_status_active_idx` 在 000010 迁移；旧索引已删（B-4）
- [ ] **DoD-5:** `provisionNewAPIUser` 失败路径用 `context.Background()` + 5 秒 timeout（B-5）
- [ ] **DoD-6:** 71 个 `_test.go` 全部用 testify；`grep -c 't.Fatalf\|t.Errorf' internal/ runtime/` 输出剩余仅 helper 模板（C-1）
- [ ] **DoD-7:** `users` 表含 `deleted_at`；`SetUserStatus` 同步设置 deleted_at（C-2）
- [ ] **DoD-8:** `app_state_machine.go` 头部含状态转移文档块（C-3）
- [ ] **DoD-9:** `newapi_audit.go` slog 调用 msg 不含 `"newapi_audit:"` 前缀（C-4）
- [ ] **DoD-10:** `stdHBLogger` 已改为 `hbLoggerAdapter` 接 `*slog.Logger`（C-5）
- [ ] **DoD-11:** `go test ./...` 全绿；`go vet ./...` 无新增告警；`make build` 成功
- [ ] **DoD-12:** AGENTS.md 含 testify 约定 + users.deleted_at 语义约定

## 10. 后续

本 spec 落地后进入 writing-plans 出 10 个 task 的实施计划。

未来可能的扩展（不在本次范围）：
- newapi/client.go 内 5 个 sentinel 的整理（不归 service 包）
- handler 测试覆盖率从「3 用例起步」扩展到全 endpoint 全错误路径
- testify 迁移后引入 mock 库（`testify/mock` 或 `gomock`）减少手写 mock
- B-3 中复杂场景（workspace 流式 / app_runtime 复杂 service 交互）补全
