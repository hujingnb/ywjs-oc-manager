# 后端日志结构化：slog + traceID

- 日期：2026-05-09
- 范围：Go 后端 logger / middleware
- 主线项编号：A-4（出自 2026-05-09 全面体检报告）

## 1. 背景

当前 logging 落地不规范，影响生产排错效率：

- `cmd/server/main.go:76` 用 stdlib `log.New(redactlog.NewRedactingWriter(os.Stderr), "", log.LstdFlags)` — text 格式（仅 `日期 时间`），无 traceID / 无 caller，机器解析困难。
- 38 个 service struct 全部无 logger 字段；仅 `worker.Pool` / `scheduler.Loop` 通过 `SetLogger` 顶层注入。
- 中间件链 `Recovery → CORS → CSRF → handler` 无 request-scoped 字段（无 X-Request-ID、Principal 不进 ctx），handler / service 链路无追踪痕迹。
- 4 处 `log.Printf` 散落（`worker/handlers/app_initialize.go` ×2、`api/handlers/members.go` ×1、`audit/newapi_audit.go` ×1、`runtime/agent/heartbeat.go` ×3），都是非结构化文本。
- A-3 Task 6b 引入的 `RuntimeOperationService.ensurePrincipalActive` 在 DB 错误路径没有结构化日志（reviewer 已识别为待补 nit）。
- `go.mod` 有 7 个 OTel **indirect** 依赖（被 `docker/docker` 拉进），与本次改造无关，不动。
- 已有 `internal/log/redactlog` 脱敏 Writer（覆盖 password / api_key / token / Bearer / sk-* 等字段），是关键约束 — 迁 slog 必须复用。

## 2. 目标

- `cmd/server/main.go` logger 迁到 `log/slog`，JSON handler，复用 redactlog Writer。
- 新增 `middleware.RequestID()` 注入 request-scoped traceID 到 ctx + response `X-Request-ID` header。
- `worker.Pool` / `scheduler.Loop` 的 `SetLogger` 接 `*slog.Logger`（API 调整）。
- 4 处 `log.Printf` 迁到 `slog.Default()` 调用。
- 顺手补 `ensurePrincipalActive` 的 DB 错误结构化日志（A-3 Task 6b 遗留 nit）。

## 3. 非目标（避免范围蔓延）

- **不**引入 OTel（用户已确认）；不动 `go.mod` 中现有 OTel indirect 依赖
- **不**引入 zap / zerolog 等第三方日志库
- **不**改 38 个 service 的 struct 注入 logger（**例外**：`RuntimeOperationService` 因 ensurePrincipalActive 顺手加 logger，作为「service 选择性注入」单点示范，不扩展到其他 service）
- **不**改 audit_logs DB 表与 audit/ 业务审计逻辑
- **不**重写 redactlog 脱敏层
- **不**改各 `cmd/*/main.go` 的 `log.Fatalf`（启动期失败保留 stdlib 风格）
- **不**新增 `LOG_LEVEL` / `LOG_FORMAT` env 配置（保持简单 — JSON 写死，level=Info 写死；未来真有需求再加）

## 4. 设计

### 4.1 关键决策（已与决策方对齐）

| 决策点 | 选择 | 理由 |
|---|---|---|
| Logger 注入粒度 | **顶层结构化** — main.go / worker.Pool / scheduler.Loop / 4 处 log.Printf；service 层不动（**例外**：RuntimeOperationService 单点） | 与现有架构吻合；service 错误冒泡至 handler 由顶层 logger 打出 |
| traceID 切入点 | **新中间件 `middleware.RequestID()`** | 解耦认证；healthz / login 等未鉴权路由也有 traceID |
| 输出格式 | **JSON handler 写死** | Docker json-file 驱动 / ELK / Loki 直接消费；本地 dev 用 jq / vscode 插件可读 |
| ensurePrincipalActive 日志 | **顺手补** | 唯一一处 service 需要结构化日志，作为单点示范 |

### 4.2 文件结构

新增：
```
internal/log/slog.go                          ← slog logger 工厂
internal/log/slog_test.go                     ← 单测
internal/api/middleware/request_id.go         ← RequestID 中间件
internal/api/middleware/request_id_test.go    ← 单测
```

修改：
```
cmd/server/main.go                            ← 用 slog.New(JSONHandler(redactWriter))
internal/api/router.go                        ← 注册 RequestID 中间件
internal/worker/worker.go                     ← SetLogger 接 *slog.Logger
internal/scheduler/runner.go                  ← SetLogger 接 *slog.Logger
internal/service/runtime_operation_service.go ← 加 logger 字段 + ensurePrincipalActive 补日志
internal/audit/newapi_audit.go                ← log.Printf → slog.Default
internal/worker/handlers/app_initialize.go    ← log.Printf → slog.Default（2 处）
internal/api/handlers/members.go              ← log.Printf → slog.Default
runtime/agent/heartbeat.go                    ← log.Printf → slog.Default（3 处）
```

### 4.3 slog logger 工厂

`internal/log/slog.go`：

```go
package log

import (
	"io"
	"log/slog"
	"os"
)

// NewSlogLogger 构造 manager-api / agent 顶层 logger。
//   - 输出：JSON handler，便于容器日志驱动 / ELK 解析
//   - 脱敏：Writer 经 NewRedactingWriter 包装（与现有 stdlib log 等价）
//   - 时间戳：RFC3339Nano（slog JSONHandler 默认）
//   - source：默认含 caller（程序短期内变更频繁，定位错误更快）
func NewSlogLogger(out io.Writer) *slog.Logger {
	if out == nil {
		out = os.Stderr
	}
	w := NewRedactingWriter(out)
	h := slog.NewJSONHandler(w, &slog.HandlerOptions{
		Level:     slog.LevelInfo,
		AddSource: true,
	})
	return slog.New(h)
}
```

`cmd/server/main.go` 调用：

```go
logger := log.NewSlogLogger(os.Stderr)
slog.SetDefault(logger) // 让 slog.Info / slog.Error 等顶层 API 走同一个 logger
```

### 4.4 RequestID 中间件

`internal/api/middleware/request_id.go`：

```go
package middleware

import (
	"context"
	"crypto/rand"
	"encoding/hex"

	"github.com/gin-gonic/gin"
)

// RequestIDHeader 是请求与响应中携带 traceID 的 HTTP header 名。
const RequestIDHeader = "X-Request-ID"

type ctxKey string

// RequestIDKey 是 ctx 中存储 traceID 的 key。
const RequestIDKey ctxKey = "request_id"

// RequestID 中间件保证每个请求都有 trace_id：
//   - 优先沿用客户端 X-Request-ID header（便于跨服务串联）
//   - 否则生成 16 字节随机 hex
//   - 注入到 c.Request.Context()，下游 handler / service 可读
//   - 同时写入 response X-Request-ID header，让客户端能回报
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.GetHeader(RequestIDHeader)
		if id == "" {
			id = generateRequestID()
		}
		ctx := context.WithValue(c.Request.Context(), RequestIDKey, id)
		c.Request = c.Request.WithContext(ctx)
		c.Header(RequestIDHeader, id)
		c.Next()
	}
}

// RequestIDFromContext 从 ctx 取 traceID；缺失返回空串。
func RequestIDFromContext(ctx context.Context) string {
	v, _ := ctx.Value(RequestIDKey).(string)
	return v
}

func generateRequestID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// rand.Read 在 Linux 上几乎不会失败；fallback 给个固定标记便于排查
		return "norandom-fallback"
	}
	return hex.EncodeToString(b[:])
}
```

注册顺序（`internal/api/router.go`）：

```go
r := gin.New()
r.Use(gin.Recovery())
r.Use(middleware.CORSAllowOrigin(...)) // 已有
r.Use(middleware.RequestID())          // 新增
r.Use(middleware.RequireCSRF(...))     // 已有
// ...handler 注册
```

### 4.5 worker.Pool / scheduler.Loop logger 接口调整

`internal/worker/worker.go` 与 `internal/scheduler/runner.go` 的 `SetLogger` 当前接 `*log.Logger`（stdlib），改为：

```go
// SetLogger 设置 worker 池的结构化 logger。仅供 cmd/server 启动期调用。
func (p *Pool) SetLogger(logger *slog.Logger) {
	p.logger = logger
}
```

内部使用从 `p.logger.Printf("...")` 改为 `p.logger.Info("worker started", "pool_size", n)` 等结构化调用。

调用方仅 `cmd/server/main.go`，迁移成本可控。

### 4.6 RuntimeOperationService 单点 logger 注入（A-3 nit 修复）

```go
type RuntimeOperationService struct {
	store    RuntimeOperationStore
	notifier JobNotifier
	inspector RuntimeInspector
	logger   *slog.Logger // 新增
}

// NewRuntimeOperationService 构造时显式接 logger（main.go 注入）。
// logger 仅用于错误诊断（如 ensurePrincipalActive 中 DB 错误），不替代审计日志。
func NewRuntimeOperationService(...args, logger *slog.Logger) *RuntimeOperationService { ... }

// ensurePrincipalActive 校验主体未被禁用。
func (s *RuntimeOperationService) ensurePrincipalActive(ctx context.Context, principal auth.Principal) error {
	id, err := parseUUID(principal.UserID)
	if err != nil {
		return ErrRuntimeOperationDenied
	}
	user, err := s.store.GetUser(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrRuntimeOperationDenied
	}
	if err != nil {
		// 新增结构化日志：DB 错误用 slog.Error，便于运维排查
		s.logger.ErrorContext(ctx, "查询主体状态失败",
			"user_id", principal.UserID,
			"err", err.Error(),
		)
		return fmt.Errorf("查询主体状态失败: %w", err)
	}
	if user.Status == domain.StatusDisabled {
		return ErrRuntimeOperationDenied
	}
	return nil
}
```

注：`ErrorContext` 与 `Error` 的差别 — `ErrorContext` 接 ctx，未来若实现 ctx 抽 traceID 自动注入到日志字段（slog handler 钩子），无需逐处再加。本次先用 ErrorContext 准备好接口，自动 traceID 注入留待 4.7 解决。

### 4.7 traceID 自动注入到日志字段

`internal/log/slog.go` 增加自定义 handler 包装，让所有 `*Context(ctx, ...)` 自动从 ctx 抽 `trace_id` 字段：

```go
// requestIDHandler 包装一个 slog.Handler，在 Handle 时自动从 ctx 提取
// middleware.RequestIDKey 并加到 record 中作为 trace_id 字段。
type requestIDHandler struct {
	slog.Handler
}

func (h *requestIDHandler) Handle(ctx context.Context, r slog.Record) error {
	if id, ok := ctx.Value(middleware.RequestIDKey).(string); ok && id != "" {
		r.AddAttrs(slog.String("trace_id", id))
	}
	return h.Handler.Handle(ctx, r)
}

func (h *requestIDHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &requestIDHandler{Handler: h.Handler.WithAttrs(attrs)}
}

func (h *requestIDHandler) WithGroup(name string) slog.Handler {
	return &requestIDHandler{Handler: h.Handler.WithGroup(name)}
}
```

但这引入循环依赖：`internal/log` → `internal/api/middleware`，因为 middleware 用 internal/log 的话也会反向。

**解决**：把 `RequestIDKey` 类型 + 常量从 middleware 包搬到 `internal/log` 包（或新包 `internal/observability`），让 middleware 与 log 都从同一处取。或更简单：**让 internal/log 定义 `type RequestIDExtractor func(ctx) string`，middleware 注入此函数**：

```go
// internal/log/slog.go
type RequestIDExtractor func(context.Context) string

var requestIDExtractor RequestIDExtractor = func(context.Context) string { return "" }

// SetRequestIDExtractor 让 middleware 在 init / main 启动期注入提取函数。
func SetRequestIDExtractor(fn RequestIDExtractor) {
	if fn != nil {
		requestIDExtractor = fn
	}
}

// requestIDHandler.Handle 改为：
if id := requestIDExtractor(ctx); id != "" { r.AddAttrs(slog.String("trace_id", id)) }
```

`cmd/server/main.go` 启动期：

```go
log.SetRequestIDExtractor(middleware.RequestIDFromContext)
```

避免循环依赖；middleware 包仅暴露提取函数，log 包通过函数指针调用。

### 4.8 4 处 log.Printf 迁移

| 文件 | 现状 | 改为 |
|---|---|---|
| `internal/audit/newapi_audit.go` | `log.Printf("newapi_audit: 写 audit_logs 失败: %v", err)` | `slog.Error("写 audit_logs 失败", "err", err)` |
| `internal/worker/handlers/app_initialize.go` ×2 | `log.Printf("...")` | `slog.InfoContext(ctx, "...")` 或 `slog.ErrorContext(ctx, "...")`（按业务语义） |
| `internal/api/handlers/members.go` | `log.Printf("...")` | `slog.ErrorContext(c.Request.Context(), "...")` |
| `runtime/agent/heartbeat.go` ×3 | `log.Printf("...")` | `slog.Info(...)` / `slog.Error(...)`（agent 不走 traceID 中间件，无 ctx 也行）|

注意：worker handler 与 handler 层用 `*Context(ctx, ...)` 形式，让 traceID 自动注入。

### 4.9 cmd/*/main.go 的 log.Fatalf

19 处 `log.Fatalf` 全在启动期（cmd/server / migrate / seed-admin / seed-e2e / agent main），**保留不动**。理由：
- 启动期失败追溯不需要 traceID（无 request 概念）
- log.Fatalf 行为（写入 stderr + os.Exit(1)）与 slog 实现等价但更显式
- 减少改动面

## 5. 改造分批策略

每步独立可回滚，每步一个 commit。

| 批次 | 内容 |
|---|---|
| 1 | 新建 `internal/log/slog.go` + `slog_test.go`（含 RequestIDExtractor 函数指针机制；不接入 main） |
| 2 | 新建 `internal/api/middleware/request_id.go` + `request_id_test.go` |
| 3 | `cmd/server/main.go` 切换 logger 为 slog；注册 RequestID 中间件；调用 `log.SetRequestIDExtractor(middleware.RequestIDFromContext)` + `slog.SetDefault(logger)` |
| 4 | `worker.Pool` / `scheduler.Loop` 的 SetLogger API 改为 `*slog.Logger`；内部 logger 调用改结构化（main.go 同步更新注入） |
| 5 | RuntimeOperationService 加 logger 字段 + ensurePrincipalActive 补 slog.ErrorContext；main.go 注入 |
| 6 | 4 处 log.Printf 迁移到 slog.Default 调用（含 worker handler / members handler / newapi_audit / agent heartbeat） |
| 7 | 全量验收：grep `log.Printf` / `log.Println` 仅在 cmd/* 启动期；本地手测一个 endpoint 看 JSON 日志含 trace_id |

## 6. 测试策略

- **`internal/log/slog_test.go`**：构造 logger，把 Writer 换成 buffer 截获 JSON；assert：
  - 输出是合法 JSON
  - 含 `time` / `level` / `source` / `msg` 字段
  - redact 字段（如 `api_key=sk-xxx` 会被替换为 `api_key=***REDACTED***`）
  - 注入 traceID 后日志含 `trace_id` 字段
- **`internal/api/middleware/request_id_test.go`**：
  - 无 `X-Request-ID` header → 生成 uuid 并写入 ctx + response header
  - 有 header → 沿用而非生成
  - `RequestIDFromContext(ctx)` 在 middleware 之后能取到值
- **回归**：`worker_test.go` / `scheduler/runner_test.go` 中 `SetLogger` 调用接 `*slog.Logger`，需要更新（API 调整）
- 本地手测：`curl -i http://localhost:8080/healthz` 看 response header 含 `X-Request-ID: <uuid>`；docker compose logs manager-api 看 JSON 日志含 `trace_id`

## 7. 风险与缓解

| 风险 | 严重度 | 缓解 |
|---|---|---|
| `worker.Pool.SetLogger` API 改动破坏调用方 | 低 | 调用方仅 main.go；test 文件需要同步更新 |
| `slog.JSONHandler` 输出 source 字段含完整路径影响日志体积 | 低 | spec 落地后跑一次本地观察日志大小；如过大可关 AddSource |
| `RequestIDExtractor` 函数指针注入时机晚于 logger 创建，导致首批日志无 trace_id | 中 | main.go 启动期立即注入（在 logger 创建后立即 SetRequestIDExtractor） |
| ctx 在 handler → service 链路某处中断（如 helper 用 context.Background()） | 中 | spec 落地后 grep `context.Background()` 在 handler / service 路径；如有需要修补；本次 spec 不主动覆盖 |
| 容器日志驱动 (docker json-file) 大量 JSON 日志体积膨胀 | 低 | docker-compose.yml 加 `logging: { options: { max-size: "10m", max-file: "3" } }`（可选，不在本次 spec 范围）|
| stdlib `log` 包仍被 cmd/* 用于 log.Fatalf — 与 slog 双轨存在 | 低 | spec 明确：log.Fatalf 启动期保留；非启动期统一用 slog |
| agent runtime/agent/heartbeat.go 不走中间件，无 traceID 上下文 | 低 | agent 是独立二进制，trace 由 agent 自己生成（如有需要）；本次仅迁 log.Printf → slog.Default，不引入 traceID 概念 |

## 8. 完成定义（DoD）

- [ ] `internal/log/slog.go` 存在，导出 `NewSlogLogger` + `SetRequestIDExtractor`
- [ ] `internal/api/middleware/request_id.go` 存在，导出 `RequestID()` + `RequestIDFromContext()` + `RequestIDHeader` + `RequestIDKey`
- [ ] `cmd/server/main.go` 不再 import `"log"`（除非保留 log.Fatalf）
- [ ] `internal/worker/worker.go` 与 `internal/scheduler/runner.go` 的 SetLogger 接 `*slog.Logger`
- [ ] `RuntimeOperationService.ensurePrincipalActive` 含 `slog.ErrorContext` 调用
- [ ] 4 处 `log.Printf` 全部迁移
- [ ] `git grep -nE 'log\.(Printf|Println)\(' internal/ runtime/` 输出仅含已迁移文件的 `slog` 等价调用（无 stdlib log.Printf 残留）
- [ ] `git grep -nE 'log\.Fatalf' cmd/ runtime/` 仍有命中（启动期保留是预期）
- [ ] `go test ./...` 全绿（含新单测）
- [ ] `go vet ./...` 无新增告警
- [ ] 本地手测：`curl -i http://localhost:8080/healthz` 含 `X-Request-ID` response header
- [ ] 本地手测：`docker compose logs manager-api` 看 JSON 日志含 `trace_id` 字段（命中已登录 API 时）

## 9. 后续

- 本 spec 落地后进入 writing-plans 出更细的 task 拆分。
- 「LOG_LEVEL / LOG_FORMAT env 配置」「docker compose 日志体积限制」「service 选择性 logger 注入扩展（含 audit、organization、member 等）」留给后续独立 spec。
