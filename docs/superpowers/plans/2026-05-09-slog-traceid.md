# slog + traceID 实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把 manager-api 顶层 logger 从 stdlib `log` 迁到 `log/slog`（JSON handler + redactlog Writer 复用），新增 `middleware.RequestID()` 注入 request-scoped traceID 到 ctx + response header，slog handler 自动从 ctx 抽 `trace_id` 字段；顺手补 A-3 Task 6b 遗留的 `ensurePrincipalActive` DB 错误日志 nit。

**Architecture:** 6 个 task：(1) 新建 `internal/log/slog.go` 含 NewSlogLogger 工厂 + RequestIDExtractor 函数指针机制；(2) 新建 `internal/api/middleware/request_id.go` 中间件；(3) 主切换：worker.Pool / scheduler.Loop 的 SetLogger API 改 *slog.Logger，main.go 切 slog + 注册中间件 + 注入 extractor；(4) RuntimeOperationService 加 logger 字段 + ensurePrincipalActive 补日志；(5) 4 处 log.Printf 迁 slog.Default；(6) DoD 验收 + 本地手测。

**Tech Stack:** Go 1.25 / Gin / log/slog (stdlib 1.21+) / 现有 internal/log/redactlog 脱敏 Writer

**Spec reference:** `docs/superpowers/specs/2026-05-09-slog-traceid-design.md`

**关键约束：**

- 每个 task 一个 commit，commit message 用 Conventional Commits 中文摘要 + Co-Authored-By（参考 AGENTS.md）。
- 不引入新依赖（slog 是 stdlib，crypto/rand 是 stdlib）；不动 redactlog 脱敏层。
- 不动 38 个 service struct（**例外**：RuntimeOperationService 单点）；不动 audit_logs 业务审计；不改 cmd/* 启动期 log.Fatalf。
- worker.Pool / scheduler.Loop 的 SetLogger 是 API 改动，调用方仅 main.go + test 文件，必须同步更新。

---

## Task 1: 新建 `internal/log/slog.go` 与 table-driven 单测

**Files:**
- Create: `internal/log/slog.go`
- Create: `internal/log/slog_test.go`

**前置阅读：**
- `internal/log/redact.go` — 现有脱敏 Writer，看 `NewRedactingWriter(io.Writer) io.Writer` 签名
- `cmd/server/main.go:76` — 看现有 log.New 调用（理解输入输出形态）

### Step 1.1: 写 spec（TDD 红色阶段）

创建 `internal/log/slog_test.go`：

```go
package log

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
)

// captureLogger 用 bytes.Buffer 捕获日志输出便于断言。
func captureLogger() (*slog.Logger, *bytes.Buffer) {
	buf := &bytes.Buffer{}
	logger := NewSlogLogger(buf)
	return logger, buf
}

func TestNewSlogLogger_输出合法JSON并含核心字段(t *testing.T) {
	logger, buf := captureLogger()
	logger.Info("hello", "user_id", "u-1")
	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("expected valid JSON, got error: %v\n输出: %s", err, buf.String())
	}
	if got["msg"] != "hello" {
		t.Errorf("msg=%v want hello", got["msg"])
	}
	if got["level"] != "INFO" {
		t.Errorf("level=%v want INFO", got["level"])
	}
	if got["user_id"] != "u-1" {
		t.Errorf("user_id=%v want u-1", got["user_id"])
	}
	if _, ok := got["time"]; !ok {
		t.Errorf("missing time field: %v", got)
	}
	if _, ok := got["source"]; !ok {
		t.Errorf("missing source field（AddSource=true 应输出 source）: %v", got)
	}
}

func TestNewSlogLogger_redact生效(t *testing.T) {
	logger, buf := captureLogger()
	// 写入会被 redactlog 命中的字段
	logger.Info("api call", "api_key", "sk-secret-12345abcde")
	out := buf.String()
	if strings.Contains(out, "sk-secret-12345abcde") {
		t.Errorf("redact 未生效，输出含原始密钥：%s", out)
	}
}

func TestRequestIDExtractor_默认为空串(t *testing.T) {
	logger, buf := captureLogger()
	ctx := context.Background()
	logger.InfoContext(ctx, "no trace")
	out := buf.String()
	if strings.Contains(out, "trace_id") {
		t.Errorf("无 extractor 时不应有 trace_id 字段：%s", out)
	}
}

func TestSetRequestIDExtractor_注入trace_id(t *testing.T) {
	original := requestIDExtractor
	t.Cleanup(func() { requestIDExtractor = original })

	SetRequestIDExtractor(func(ctx context.Context) string {
		if v, ok := ctx.Value("test-trace").(string); ok {
			return v
		}
		return ""
	})

	logger, buf := captureLogger()
	ctx := context.WithValue(context.Background(), "test-trace", "abc123")
	logger.InfoContext(ctx, "with trace")

	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("expected valid JSON: %v", err)
	}
	if got["trace_id"] != "abc123" {
		t.Errorf("trace_id=%v want abc123", got["trace_id"])
	}
}

func TestSetRequestIDExtractor_空串不写入字段(t *testing.T) {
	original := requestIDExtractor
	t.Cleanup(func() { requestIDExtractor = original })

	SetRequestIDExtractor(func(ctx context.Context) string { return "" })

	logger, buf := captureLogger()
	logger.InfoContext(context.Background(), "no trace")

	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("expected valid JSON: %v", err)
	}
	if _, ok := got["trace_id"]; ok {
		t.Errorf("不应写入空 trace_id 字段，got %v", got)
	}
}
```

### Step 1.2: 跑测试确认编译失败

```bash
go test ./internal/log/... -run 'TestNewSlogLogger\|TestRequestIDExtractor\|TestSetRequestIDExtractor' -v 2>&1 | tail -10
```

预期：编译错误（`undefined: NewSlogLogger / SetRequestIDExtractor / requestIDExtractor`）。这是 TDD 红色阶段。

### Step 1.3: 写实现

创建 `internal/log/slog.go`：

```go
package log

import (
	"context"
	"io"
	"log/slog"
	"os"
)

// RequestIDExtractor 从 ctx 中抽 trace_id 字符串；缺失返回空串。
// 由 internal/api/middleware 在程序启动期通过 SetRequestIDExtractor 注入实际实现，
// 用函数指针解耦避免 internal/log 直接 import middleware 形成循环依赖。
type RequestIDExtractor func(context.Context) string

// 默认实现：空串（即不附加 trace_id）。启动期由 main.go 调用 SetRequestIDExtractor 替换。
var requestIDExtractor RequestIDExtractor = func(context.Context) string { return "" }

// SetRequestIDExtractor 注入 trace_id 提取函数。仅在 main.go 启动期调用一次。
func SetRequestIDExtractor(fn RequestIDExtractor) {
	if fn != nil {
		requestIDExtractor = fn
	}
}

// requestIDHandler 包装 slog.Handler，自动从 ctx 提取 trace_id 并附加到 record 中。
type requestIDHandler struct {
	slog.Handler
}

func (h *requestIDHandler) Handle(ctx context.Context, r slog.Record) error {
	if id := requestIDExtractor(ctx); id != "" {
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

// NewSlogLogger 构造 manager-api / agent 顶层 logger。
//   - 输出：JSON handler，便于容器日志驱动 / ELK 解析
//   - 脱敏：Writer 经 NewRedactingWriter 包装（与现有 stdlib log 等价）
//   - source：AddSource=true 含 caller 路径，便于错误定位
//   - level：Info（生产足够；调试时未来可加 LOG_LEVEL env，本次不做）
//
// out 为 nil 时默认走 os.Stderr。
func NewSlogLogger(out io.Writer) *slog.Logger {
	if out == nil {
		out = os.Stderr
	}
	w := NewRedactingWriter(out)
	base := slog.NewJSONHandler(w, &slog.HandlerOptions{
		Level:     slog.LevelInfo,
		AddSource: true,
	})
	return slog.New(&requestIDHandler{Handler: base})
}
```

### Step 1.4: 跑测试确认全过

```bash
go test ./internal/log/... -v 2>&1 | tail -15
go vet ./internal/log/...
```

预期：5 个用例全 PASS；vet 无新增告警。

### Step 1.5: 自检 + commit

```bash
git status --short
```

应只有：
- `A internal/log/slog.go`
- `A internal/log/slog_test.go`

```bash
git add internal/log/slog.go internal/log/slog_test.go
git commit -m "$(cat <<'EOF'
feat(log): 新增 slog logger 工厂含 RequestIDExtractor 机制

NewSlogLogger 构造 JSON handler 复用现有 redactlog Writer 脱敏；
AddSource=true 输出 caller。requestIDHandler 包装 slog.Handler，
通过 RequestIDExtractor 函数指针从 ctx 抽 trace_id 字段，避免
internal/log ↔ middleware 循环依赖。

5 个 spec 用例覆盖：JSON 输出 + redact 生效 + extractor 注入 +
空串不写字段。

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 2: 新建 `internal/api/middleware/request_id.go` 与单测

**Files:**
- Create: `internal/api/middleware/request_id.go`
- Create: `internal/api/middleware/request_id_test.go`

**前置阅读：**
- `internal/api/middleware/csrf.go`（或类似已有 middleware）— 看 gin.HandlerFunc 风格

### Step 2.1: 写 spec

创建 `internal/api/middleware/request_id_test.go`：

```go
package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func TestRequestID_无header时生成新ID(t *testing.T) {
	r := gin.New()
	var capturedID string
	r.Use(RequestID())
	r.GET("/x", func(c *gin.Context) {
		capturedID = RequestIDFromContext(c.Request.Context())
		c.Status(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if capturedID == "" {
		t.Errorf("RequestIDFromContext 应返回生成的 ID，got 空串")
	}
	if len(capturedID) != 32 {
		t.Errorf("生成的 ID 应为 32 字符 hex（16 字节），got %q (len=%d)", capturedID, len(capturedID))
	}
	resp := w.Header().Get(RequestIDHeader)
	if resp != capturedID {
		t.Errorf("response header X-Request-ID=%q 应与 ctx 中的一致 %q", resp, capturedID)
	}
}

func TestRequestID_有header时沿用客户端ID(t *testing.T) {
	r := gin.New()
	var capturedID string
	r.Use(RequestID())
	r.GET("/x", func(c *gin.Context) {
		capturedID = RequestIDFromContext(c.Request.Context())
		c.Status(http.StatusOK)
	})

	const clientID = "client-trace-12345"
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set(RequestIDHeader, clientID)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if capturedID != clientID {
		t.Errorf("应沿用客户端 ID %q，got %q", clientID, capturedID)
	}
	if w.Header().Get(RequestIDHeader) != clientID {
		t.Errorf("response header 应回写客户端 ID")
	}
}

func TestRequestIDFromContext_无值返回空串(t *testing.T) {
	got := RequestIDFromContext(httptest.NewRequest(http.MethodGet, "/x", nil).Context())
	if got != "" {
		t.Errorf("空 ctx 应返回空串，got %q", got)
	}
}

func TestGenerateRequestID_输出32字符hex(t *testing.T) {
	id := generateRequestID()
	if len(id) != 32 {
		t.Errorf("生成的 ID 应为 32 字符 hex，got %q (len=%d)", id, len(id))
	}
	for _, c := range id {
		ok := (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')
		if !ok {
			t.Errorf("ID 含非 hex 字符 %q: %s", c, id)
			break
		}
	}
	// 简单防呆：连续两次生成不重复（极小概率失败可接受）
	if id2 := generateRequestID(); id == id2 {
		t.Errorf("两次生成应不同：%q vs %q", id, id2)
	}
	_ = strings.TrimSpace(id) // 占位避免 unused import
}
```

### Step 2.2: 跑测试确认编译失败

```bash
go test ./internal/api/middleware/... -run 'TestRequestID' -v 2>&1 | tail -10
```

预期：FAIL（`undefined: RequestID / RequestIDFromContext / RequestIDHeader / generateRequestID`）。

### Step 2.3: 写实现

创建 `internal/api/middleware/request_id.go`：

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

// 不导出，避免外部 ctx 写入冲突；外部读取走 RequestIDFromContext 函数。
type ctxKey string

const requestIDKey ctxKey = "request_id"

// RequestID 中间件保证每个请求都有 trace_id：
//   - 优先沿用客户端 X-Request-ID header（便于跨服务串联）
//   - 否则生成 16 字节随机 hex（32 字符）
//   - 注入到 c.Request.Context()，下游 handler / service 可读
//   - 同时写入 response X-Request-ID header，让客户端能回报
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.GetHeader(RequestIDHeader)
		if id == "" {
			id = generateRequestID()
		}
		ctx := context.WithValue(c.Request.Context(), requestIDKey, id)
		c.Request = c.Request.WithContext(ctx)
		c.Header(RequestIDHeader, id)
		c.Next()
	}
}

// RequestIDFromContext 从 ctx 取 traceID；缺失返回空串。
// log/slog 层通过此函数（注入到 RequestIDExtractor）自动给日志附加 trace_id。
func RequestIDFromContext(ctx context.Context) string {
	v, _ := ctx.Value(requestIDKey).(string)
	return v
}

// generateRequestID 生成 16 字节随机 hex（32 字符）。
// crypto/rand.Read 在 Linux 上几乎不会失败；fallback 给固定标记便于排查。
func generateRequestID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "norandom-fallback"
	}
	return hex.EncodeToString(b[:])
}
```

### Step 2.4: 跑测试确认全过

```bash
go test ./internal/api/middleware/... -v 2>&1 | tail -15
go vet ./internal/api/middleware/...
```

预期：4 个新用例全 PASS；vet 无新增告警。

### Step 2.5: 自检 + commit

```bash
git status --short
```

应只有：
- `A internal/api/middleware/request_id.go`
- `A internal/api/middleware/request_id_test.go`

```bash
git add internal/api/middleware/request_id.go internal/api/middleware/request_id_test.go
git commit -m "$(cat <<'EOF'
feat(api): 新增 RequestID 中间件注入 request-scoped traceID

- 优先沿用客户端 X-Request-ID header；无则生成 16 字节随机 hex
- 注入 ctx 让下游 handler / service 可读；同时回写 response header
- RequestIDFromContext 供 internal/log 通过 SetRequestIDExtractor
  注入，让 slog handler 自动给日志附加 trace_id 字段

4 个用例覆盖：无 header 生成 + 有 header 沿用 + 空 ctx 取值 +
generateRequestID 输出格式。

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 3: 主切换 — worker/scheduler API 改 *slog.Logger + main.go 切 slog + 注册中间件

**Files:**
- Modify: `internal/worker/worker.go`
- Modify: `internal/scheduler/runner.go`
- Modify: `cmd/server/main.go`
- Modify: `internal/api/router.go`
- Modify: `internal/worker/runner_test.go`（如 SetLogger 调用了 stdlib log.New）
- Modify: `internal/scheduler/runner_test.go`（同上）

**前置阅读：**
- `cmd/server/main.go` 全文 — 看 logger 创建、worker.Pool 与 scheduler.Loop 的注入位置
- `internal/worker/worker.go` 找 `SetLogger` 与 logger 内部调用
- `internal/scheduler/runner.go` 同上
- `internal/api/router.go` — 看中间件注册顺序（Recovery → CORS → CSRF → handler）

### Step 3.1: baseline 测试

```bash
go test ./... -count=1 2>&1 | tail -5
go vet ./...
```

预期全绿。如有 fail 先停下告诉 controller。

### Step 3.2: 修改 worker.Pool.SetLogger 签名

读 `internal/worker/worker.go`，找 `SetLogger(*log.Logger)` 与内部 `p.logger.Printf` / `p.logger.Println` 调用点。

改动：

```go
// 原
import "log"

type Pool struct {
	// ...
	logger *log.Logger
}

func (p *Pool) SetLogger(logger *log.Logger) {
	p.logger = logger
}

// 内部调用，例
p.logger.Printf("worker started: pool_size=%d", n)

// 改后
import "log/slog"

type Pool struct {
	// ...
	logger *slog.Logger
}

// SetLogger 设置 worker 池的结构化 logger。仅供 cmd/server 启动期调用。
func (p *Pool) SetLogger(logger *slog.Logger) {
	p.logger = logger
}

// 内部调用改结构化
p.logger.Info("worker started", "pool_size", n)
```

注意：

- 把所有 `p.logger.Printf("msg fmt %s", arg)` 改为 `p.logger.Info("msg", "key", arg)` 或 `p.logger.Error(...)` 按业务语义
- 错误路径用 `Error`（含 ctx 时用 `ErrorContext(ctx, ...)`）
- 如果 logger 为 nil 时旧代码会 panic（stdlib log），slog 也一样：保持 nil-safe 不是本次范围；但若旧代码有 nil 检查（`if p.logger != nil`），保留这个检查

### Step 3.3: 修改 scheduler.Loop.SetLogger 签名

`internal/scheduler/runner.go` 同 Step 3.2 模式。

### Step 3.4: 同步更新 worker/scheduler 测试

```bash
git grep -nE 'SetLogger\(' internal/worker/ internal/scheduler/
```

找到所有调用点（应只在 `*_test.go` 与 `cmd/server/main.go`），按下面更新：

```go
// 测试中如果有 log.New(io.Discard, "", 0)，改为：
import "log/slog"
import "io"

logger := slog.New(slog.NewTextHandler(io.Discard, nil))
pool.SetLogger(logger)
```

### Step 3.5: cmd/server/main.go 切换 logger

读 `cmd/server/main.go` 找当前 `logger := log.New(redactlog.NewRedactingWriter(...), "", log.LstdFlags)` 行。

改动：

```go
// 原
import "log"
// ...
logger := log.New(redactlog.NewRedactingWriter(os.Stderr), "", log.LstdFlags)
workerPool.SetLogger(logger)
schedulerLoop.SetLogger(logger)

// 改后
import (
	stdlog "log" // 保留 stdlib log 用于 log.Fatalf 启动期错误
	"log/slog"

	managerlog "oc-manager/internal/log"
	"oc-manager/internal/api/middleware"
)

// ...

logger := managerlog.NewSlogLogger(os.Stderr)
slog.SetDefault(logger)
managerlog.SetRequestIDExtractor(middleware.RequestIDFromContext)

workerPool.SetLogger(logger)
schedulerLoop.SetLogger(logger)
```

注意：

- 如果 main.go 还在用 `log.Fatalf` 处理启动期错误，把现有 `import "log"` 改成 `import stdlog "log"`，然后调用点用 `stdlog.Fatalf(...)`。或者直接用 `slog.Error(...) ; os.Exit(1)`（按 task 5 决策保持 stdlib log.Fatalf 不动）
- 如果 main.go 完全不再需要 stdlib log（启动错误改用 slog），可以删掉 `import "log"`

实际看 main.go：如果发现 `log.Fatalf` 的调用，**保留 stdlib import**（spec 4.9 节决策）。

### Step 3.6: 注册 RequestID 中间件到 router

读 `internal/api/router.go`，找现有 `r.Use(...)` 调用位置。

改动：

```go
// 原（顺序：Recovery → CORS → CSRF）
r := gin.New()
r.Use(gin.Recovery())
if cfg.AllowedOrigins != "" {
	r.Use(middleware.CORSAllowOrigin(cfg.AllowedOrigins))
}
r.Use(middleware.RequireCSRF(cfg.CSRFToken))

// 改后（插入 RequestID 在 CORS 之后、CSRF 之前）
r := gin.New()
r.Use(gin.Recovery())
if cfg.AllowedOrigins != "" {
	r.Use(middleware.CORSAllowOrigin(cfg.AllowedOrigins))
}
r.Use(middleware.RequestID()) // 新增
r.Use(middleware.RequireCSRF(cfg.CSRFToken))
```

### Step 3.7: 跑全量 test + vet + build

```bash
go vet ./...
go test ./... -count=1 2>&1 | tail -10
go build ./...
```

预期：全过。如有 fail（特别是 worker/scheduler 测试），按 vet 或 test 输出修复（可能是测试文件中 `log.New` 调用没改）。

### Step 3.8: 自检 + commit

```bash
git status --short
git diff --stat
```

应只有：
- `M internal/worker/worker.go`
- `M internal/scheduler/runner.go`
- `M cmd/server/main.go`
- `M internal/api/router.go`
- 可能 `M internal/worker/*_test.go` 与 `internal/scheduler/runner_test.go`

```bash
git add internal/worker/ internal/scheduler/ cmd/server/main.go internal/api/router.go
git commit -m "$(cat <<'EOF'
feat(log): 顶层 logger 切换为 slog；注册 RequestID 中间件

- worker.Pool.SetLogger 与 scheduler.Loop.SetLogger 接 *slog.Logger
- 内部 p.logger.Printf 改结构化 p.logger.Info/Error
- cmd/server/main.go：用 managerlog.NewSlogLogger 构造 logger；
  调用 slog.SetDefault；注入 middleware.RequestIDFromContext 到
  log 包的 RequestIDExtractor，让 ctx 中的 trace_id 自动写入日志字段
- router.go 注册 middleware.RequestID() 在 CORS 之后、CSRF 之前
- cmd/server/main.go 仍保留 stdlib log 的 log.Fatalf 用于启动期错误

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 4: RuntimeOperationService 加 logger 字段 + ensurePrincipalActive 补日志

**Files:**
- Modify: `internal/service/runtime_operation_service.go`
- Modify: `cmd/server/main.go`（注入 logger 到 NewRuntimeOperationService）
- Modify: `internal/service/runtime_operation_service_test.go`（如有 mock 构造调用 New 函数）

**前置阅读：**
- `internal/service/runtime_operation_service.go:356-372` — 现有 `ensurePrincipalActive` 实现
- `cmd/server/main.go` — `NewRuntimeOperationService(...)` 当前调用（找参数顺序）

### Step 4.1: baseline 测试

```bash
go test ./internal/service/... -count=1 2>&1 | tail -5
```

预期全绿。

### Step 4.2: 修改 RuntimeOperationService struct + 构造函数 + ensurePrincipalActive

读 `internal/service/runtime_operation_service.go`，识别：
- struct 当前字段（store / notifier / inspector）
- 构造函数 `NewRuntimeOperationService(...)` 参数顺序
- `ensurePrincipalActive` 行号

改动 struct：

```go
import (
	// ...原有
	"log/slog"
)

type RuntimeOperationService struct {
	store     RuntimeOperationStore
	notifier  JobNotifier
	inspector RuntimeInspector
	logger    *slog.Logger // 新增
}
```

改动构造函数（**必须保持参数顺序兼容**或 main.go 同步更新）：

```go
// 原
func NewRuntimeOperationService(store RuntimeOperationStore, notifier JobNotifier, inspector RuntimeInspector) *RuntimeOperationService {
	return &RuntimeOperationService{store: store, notifier: notifier, inspector: inspector}
}

// 改后：新增 logger 参数（main.go 同步更新调用）
// NewRuntimeOperationService 构造服务实例。logger 仅用于错误诊断，不替代审计日志。
func NewRuntimeOperationService(
	store RuntimeOperationStore,
	notifier JobNotifier,
	inspector RuntimeInspector,
	logger *slog.Logger,
) *RuntimeOperationService {
	return &RuntimeOperationService{
		store:     store,
		notifier:  notifier,
		inspector: inspector,
		logger:    logger,
	}
}
```

改 `ensurePrincipalActive`：

```go
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
		// 新增结构化日志：DB 错误用 slog.ErrorContext，trace_id 自动注入
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

### Step 4.3: 同步 main.go 注入 logger

读 `cmd/server/main.go` 找 `NewRuntimeOperationService(...)` 调用位置。

改动：

```go
// 原
runtimeOpService := service.NewRuntimeOperationService(store, jobNotifier, runtimeInspector)

// 改后
runtimeOpService := service.NewRuntimeOperationService(store, jobNotifier, runtimeInspector, logger)
```

### Step 4.4: 同步 runtime_operation_service_test.go 注入 nil logger 或 mock

读 `internal/service/runtime_operation_service_test.go`，找 `NewRuntimeOperationService(...)` 调用：

```go
// 测试中 logger 不重要，可注入 io.Discard 的 slog 实例
import (
	"io"
	"log/slog"
)

testLogger := slog.New(slog.NewTextHandler(io.Discard, nil))
svc := service.NewRuntimeOperationService(store, notifier, inspector, testLogger)
```

如果测试文件有多处 `NewRuntimeOperationService` 调用，全部更新。

### Step 4.5: vet + test + build

```bash
go vet ./...
go test ./... -count=1 2>&1 | tail -10
go build ./...
```

预期全绿。

### Step 4.6: 自检 + commit

```bash
git status --short
git diff --stat
```

应只有：
- `M internal/service/runtime_operation_service.go`
- `M cmd/server/main.go`
- 可能 `M internal/service/runtime_operation_service_test.go`

```bash
git add internal/service/runtime_operation_service.go cmd/server/main.go internal/service/runtime_operation_service_test.go
git commit -m "$(cat <<'EOF'
fix(runtime): ensurePrincipalActive 补 slog 结构化日志

A-3 Task 6b 引入 ensurePrincipalActive 时 reviewer 已识别 DB 错误
路径缺日志的 nit；本次补 slog.ErrorContext，trace_id 自动通过 ctx
注入。RuntimeOperationService 构造函数接 *slog.Logger 参数（main.go
同步注入）。

这是 spec 4.6 节明确的「service 单点 logger 注入」单点示范，
不扩展到其他 service。

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 5: 4 处 log.Printf 迁 slog

**Files:**
- Modify: `internal/audit/newapi_audit.go`
- Modify: `internal/worker/handlers/app_initialize.go`
- Modify: `internal/api/handlers/members.go`
- Modify: `runtime/agent/heartbeat.go`

**前置阅读：**
- 调研报告中的 4 处 log.Printf 行号（每处都要看上下文判断 Info / Error）

### Step 5.1: 找出所有 log.Printf 调用点

```bash
git grep -nE 'log\.(Printf|Println)\(' internal/ runtime/ | grep -v '_test.go' | grep -v 'cmd/'
```

预期：约 7 个命中（4 个文件，部分文件多处）。逐个改。

### Step 5.2: internal/audit/newapi_audit.go

读现有调用，例：

```go
// 原
log.Printf("newapi_audit: 写 audit_logs 失败: %v", err)

// 改后
slog.ErrorContext(ctx, "写 audit_logs 失败", "err", err)
// 如果该函数没有 ctx 入参，用 slog.Error("...", "err", err)
```

注意 import 改：

```go
// 原
import "log"

// 改后
import "log/slog"
```

如果原文件还有别的 stdlib log 用法（如 log.Fatalf），保留 import "log"；否则删除。

### Step 5.3: internal/worker/handlers/app_initialize.go（2 处）

```bash
grep -n 'log\.(Printf\|Println)' internal/worker/handlers/app_initialize.go
```

每处按业务语义改：

```go
// 错误路径
slog.ErrorContext(ctx, "app initialize 步骤失败", "app_id", appID, "step", "...", "err", err)

// 信息路径
slog.InfoContext(ctx, "app initialize 完成", "app_id", appID)
```

注意：worker handler 通常已接收 ctx，用 `*Context(ctx, ...)` 让 trace_id 自动注入。

### Step 5.4: internal/api/handlers/members.go

读对应行：

```go
// 改后
slog.ErrorContext(c.Request.Context(), "...", "err", err, ...)
```

handler 层可用 `c.Request.Context()` 取 ctx（含 traceID）。

### Step 5.5: runtime/agent/heartbeat.go（3 处）

agent 是独立二进制（runtime/agent/main.go），不走 manager-api 中间件，**不强制 traceID**。但日志统一为 slog 仍然有价值。

注意：agent 可能没初始化 slog.SetDefault；如果 agent main.go 没有切 slog logger，这些 slog 调用会用 stdlib slog default（输出到 stderr 的 text 格式）。两个选项：

A. 仅在 manager-api（cmd/server/main.go）中切 slog；agent 暂保留 stdlib log
B. agent main.go 也切 slog（用 NewSlogLogger）

**Spec 4.8 节确认 agent 也走 slog**。所以：

- Step 5.5a: 改 `runtime/agent/heartbeat.go` 的 3 处 log.Printf 为 `slog.Info/Error(...)`（不带 Context，因为 agent 不需要 traceID）
- Step 5.5b: 改 `runtime/agent/main.go` 的 logger 初始化为 `managerlog.NewSlogLogger(os.Stderr)` + `slog.SetDefault(...)`（让 slog.Info / slog.Error 走 JSON 输出）

如果 agent main.go 当前用 `log.New(...)`，类似 manager-api 切换。如果已有 logger 注入到其他子模块（如 heartbeat 接 *log.Logger 作为参数），同步改成 *slog.Logger。

如发现 agent 改造比预想大（如 heartbeat 接 *log.Logger 字段且多个调用方），**stop 报告 controller**，决定是合并 task 还是延后做。

### Step 5.6: 验证全仓 grep 干净

```bash
git grep -nE 'log\.(Printf|Println)\(' internal/ runtime/ | grep -v '_test.go' | grep -v 'cmd/'
```

预期：输出空（4 个文件全部迁移）。如有命中（测试文件除外），漏改了。

### Step 5.7: vet + test + build

```bash
go vet ./...
go test ./... -count=1 2>&1 | tail -10
go build ./...
```

预期全绿。

### Step 5.8: 自检 + commit

```bash
git status --short
git diff --stat
```

应有：
- `M internal/audit/newapi_audit.go`
- `M internal/worker/handlers/app_initialize.go`
- `M internal/api/handlers/members.go`
- `M runtime/agent/heartbeat.go`
- 可能 `M runtime/agent/main.go`

```bash
git add internal/audit/newapi_audit.go internal/worker/handlers/app_initialize.go \
  internal/api/handlers/members.go runtime/agent/
git commit -m "$(cat <<'EOF'
refactor(log): 4 处 log.Printf 迁移到 slog 结构化日志

- internal/audit/newapi_audit.go：写 audit_logs 失败错误日志
- internal/worker/handlers/app_initialize.go：app initialize 步骤
  信息/错误日志（用 *Context 让 trace_id 自动注入）
- internal/api/handlers/members.go：handler 错误日志（同上）
- runtime/agent/heartbeat.go：agent 心跳信息/错误日志（agent 不走
  manager-api 中间件，无 traceID 上下文）
- runtime/agent/main.go：agent logger 切换 slog.JSONHandler
  （如有需要）

至此非 cmd/* 启动期的 log.Printf 全部消失。

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 6: 全量 DoD 验收 + 本地手测

**Files:**
- 无新建/修改文件；本任务仅运行命令验证 + 本地手测

### Step 6.1: 自动化 DoD 验收

逐项跑命令记录结果：

```bash
echo "=== DoD-1: internal/log/slog.go 导出 NewSlogLogger 与 SetRequestIDExtractor ==="
grep -E '^func NewSlogLogger|^func SetRequestIDExtractor' internal/log/slog.go

echo ""
echo "=== DoD-2: middleware/request_id.go 导出 RequestID / RequestIDFromContext / RequestIDHeader ==="
grep -E '^func RequestID|^func RequestIDFromContext|^const RequestIDHeader' internal/api/middleware/request_id.go

echo ""
echo "=== DoD-3: cmd/server/main.go 不再 import \"log\"（除 log.Fatalf 启动期保留）==="
grep -E 'import.*"log"' cmd/server/main.go || echo "(空 = 完全移除 stdlib log)"
grep -nE '\blog\.Fatalf\(' cmd/server/main.go && echo "(若有 = 保留启动期 log.Fatalf 是预期，import 应为 stdlog)"

echo ""
echo "=== DoD-4: worker.Pool.SetLogger 与 scheduler.Loop.SetLogger 接 *slog.Logger ==="
grep -E 'SetLogger\(.*slog\.Logger' internal/worker/worker.go internal/scheduler/runner.go

echo ""
echo "=== DoD-5: ensurePrincipalActive 含 slog.ErrorContext ==="
grep -A 2 'ensurePrincipalActive' internal/service/runtime_operation_service.go | grep -E 'ErrorContext|s\.logger\.Error'

echo ""
echo "=== DoD-6: 4 处 log.Printf 已全部迁移 ==="
git grep -nE 'log\.(Printf|Println)\(' internal/ runtime/ | grep -v '_test.go' | grep -v 'cmd/'
echo "(预期空)"

echo ""
echo "=== DoD-7: cmd/* 启动期 log.Fatalf 仍有命中（保留是预期）==="
git grep -nE 'log\.Fatalf' cmd/ runtime/ | wc -l
echo "(预期 ≥ 1，启动期错误不动)"

echo ""
echo "=== DoD-8: go test 全绿 ==="
go test ./... -count=1 2>&1 | tail -3

echo ""
echo "=== DoD-9: go vet 无新增告警 ==="
go vet ./... 2>&1 | tail -3
```

记录每项结果。如有项不通过，**停下报告 controller**。

### Step 6.2: 本地手测 1 — 无 X-Request-ID header 时自动生成

需要 manager-api 服务在跑（`docker compose up -d` 或 `make dev-up`）。

```bash
curl -i http://localhost:8080/healthz 2>&1 | grep -iE 'X-Request-Id|HTTP/'
```

预期：

- `HTTP/1.1 200 OK`
- response 含 `X-Request-Id: <32 字符 hex>`

记录截到的 request id（如 `abc123def456...`）。

### Step 6.3: 本地手测 2 — 客户端 X-Request-ID header 沿用

```bash
curl -i -H "X-Request-ID: client-trace-test" http://localhost:8080/healthz 2>&1 | grep -iE 'X-Request-Id|HTTP/'
```

预期：

- `HTTP/1.1 200 OK`
- response 含 `X-Request-Id: client-trace-test`（沿用而非生成）

### Step 6.4: 本地手测 3 — 日志 JSON 格式 + trace_id 字段

发请求触发日志（任何已登录后 API 调用，或者直接看 startup 日志）：

```bash
docker compose logs --tail=20 manager-api 2>&1 | tail -20
```

预期：日志输出是 JSON 格式（每行 `{"time":"...","level":"INFO","source":{...},"msg":"...",...}`）。

如果有任何已登录 API 调用，对应日志行应含 `"trace_id":"<32hex>"` 字段。

启动期日志（log.Fatalf / 启动 banner）可能仍是 text 格式（保留 stdlib log），这是预期。

### Step 6.5: 本地手测 4 — service 错误路径触发 trace_id

如果方便，触发一个 ensurePrincipalActive 失败场景（如改库使 GetUser 报错）：

```bash
# 触发 RuntimeOperationService 路径，看 docker compose logs 中是否有
# "查询主体状态失败" 的 JSON 日志含 trace_id 字段
```

可选 step，难触发就跳过。

### Step 6.6: 提交手测报告（不创建 commit，仅在 task 报告里记录）

不写代码也不 commit。本 task 是验收 task。如果发现 DoD 失败需要修，回到对应 task 修复。

## 报告（≤350 字）

```
状态：DONE / DONE_WITH_CONCERNS / NEEDS_CONTEXT / BLOCKED

DoD 验收明细（逐项 Y/N + 关键证据）：
- DoD-1: NewSlogLogger / SetRequestIDExtractor 导出
- DoD-2: middleware.RequestID + RequestIDFromContext + RequestIDHeader
- DoD-3: cmd/server/main.go stdlib log 状态
- DoD-4: worker / scheduler SetLogger 接 *slog.Logger
- DoD-5: ensurePrincipalActive 含 slog.ErrorContext
- DoD-6: 4 处 log.Printf grep 空
- DoD-7: cmd/* log.Fatalf 命中数
- DoD-8: go test 全绿
- DoD-9: go vet 无告警

本地手测：
- 无 header 自动生成 X-Request-ID: Y/N（截到的 ID）
- 有 header 沿用客户端 ID: Y/N
- docker compose logs 是 JSON 格式: Y/N
- 已登录 API 调用日志含 trace_id 字段: Y/N

如有 BLOCKED 请详细描述卡点。
```

---

## 完成定义

所有 task 完成后必须满足：

- [ ] **DoD-1:** `internal/log/slog.go` 存在，导出 `NewSlogLogger` + `SetRequestIDExtractor`
- [ ] **DoD-2:** `internal/api/middleware/request_id.go` 存在，导出 `RequestID()` / `RequestIDFromContext()` / `RequestIDHeader`
- [ ] **DoD-3:** `cmd/server/main.go` stdlib log 仅用于 `log.Fatalf`（启动期）；非启动期都用 slog
- [ ] **DoD-4:** `worker.Pool.SetLogger` 与 `scheduler.Loop.SetLogger` 接 `*slog.Logger`
- [ ] **DoD-5:** `RuntimeOperationService.ensurePrincipalActive` 含 `slog.ErrorContext` 调用
- [ ] **DoD-6:** `git grep -nE 'log\.(Printf|Println)\(' internal/ runtime/` 排除测试与 cmd 后输出空
- [ ] **DoD-7:** `cmd/*/main.go` 启动期 `log.Fatalf` 保留（预期 ≥ 1 命中）
- [ ] **DoD-8:** `go test ./...` 全绿
- [ ] **DoD-9:** `go vet ./...` 无新增告警
- [ ] **DoD-10:** 本地手测 1：`curl -i http://localhost:8080/healthz` response 含 `X-Request-Id` header
- [ ] **DoD-11:** 本地手测 2：`docker compose logs manager-api` 是 JSON 格式

---

## 回滚策略

每个 task 一个独立 commit，可单独 `git revert`。

风险点：
- Task 3 是最大改动批（worker/scheduler API + main.go + router）；如有问题 revert 此一个 commit 就完整回滚到 baseline
- Task 4 与 Task 5 互独立，可单独回退

---

## 风险与应对

| 风险 | 何时出现 | 应对 |
|---|---|---|
| `RequestIDExtractor` 函数指针注入时机晚于 logger 创建，首批日志无 trace_id | Task 3 main.go | spec 4.7 已明确：在 logger 创建后立即 SetRequestIDExtractor；测试覆盖了 extractor 默认空串行为 |
| worker/scheduler 内部 `p.logger.Printf` 改结构化时业务字段命名混乱 | Task 3 | 内部调用按 `p.logger.Info("event", "key1", val1, "key2", val2)` 风格；改完后人工 review 一致性 |
| agent runtime/agent/main.go 切 slog 范围超预期（heartbeat 接 *log.Logger 字段等） | Task 5 Step 5.5 | implementer 一旦发现范围超出，stop 报告 controller 决定是否拆 task |
| docker compose logs 体积膨胀（JSON 比 text 多） | 长期运行 | 不在本次 spec 范围；后续 docker-compose.yml 加 logging max-size 即可解决 |
| 测试文件中 mock logger 改 slog 时 import "io" 重复或不必要 | Task 3-4 | implementer 自行处理；任何编译错先 stop |
