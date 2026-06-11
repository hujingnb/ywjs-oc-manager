# 整理 oc-manager API 日志 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 补齐 manager-api 的 HTTP access log、统一外部调用日志、让日志级别/格式可配置，并制定日志规范后全量重构散落日志。

**Architecture:** 沿用现有 `log/slog` + 脱敏 writer + trace_id 自动注入的基础设施。新增可配置 `Config` 入口；access log 作为 gin 中间件只打 stdout JSON；外部调用日志走自定义 `http.RoundTripper` 在传输层统一拦截；先落地规范常量与 helper，再按目录分批重构散落日志。

**Tech Stack:** Go 1.22+、`log/slog`、gin v1.10、`github.com/stretchr/testify`。

设计文档：`docs/superpowers/specs/2026-06-11-api-logging-design.md`

---

## 文件结构

| 文件 | 职责 | 动作 |
|---|---|---|
| `internal/log/config.go` | `Config` 结构 + `ParseConfigFromEnv`（解析 `LOG_LEVEL` / `LOG_FORMAT`） | 创建 |
| `internal/log/slog.go` | `NewSlogLogger` 改为接收 `Config`，支持 json/text handler | 修改 |
| `cmd/server/main.go` | 装配点：`NewSlogLogger(logOut, ParseConfigFromEnv())` | 修改（91 行附近） |
| `internal/log/attrs.go` | 统一 attr key 常量 + `Err` helper | 创建 |
| `internal/api/middleware/access_log.go` | HTTP access log 中间件 | 创建 |
| `internal/api/router.go` | 挂载 access log 中间件 | 修改（98 行附近） |
| `internal/integrations/httplog/transport.go` | logging `http.RoundTripper` | 创建 |
| `internal/integrations/newapi/client.go` | `NewClient` 注入 logging transport | 修改（211 行附近） |
| `internal/integrations/ragflow/client.go` | `NewClient` 注入 logging transport | 修改（241 行附近） |
| `docs/logging-conventions.md` | 日志规范文档 | 创建 |
| `internal/service/*`、`internal/worker/handlers/*` | 散落日志对齐规范 | 修改（分批、逐文件分提交） |

实现顺序：Task 1（可配置化）→ Task 2（规范常量+helper）→ Task 3（规范文档）→ Task 4（access log）/ Task 5（外部调用）可并行 → Task 6（全量 sweep，最后做）→ Task 7（构建与真实环境验证）。

---

## Task 1: 日志基础设施可配置化

**Files:**
- Create: `internal/log/config.go`
- Modify: `internal/log/slog.go:48-65`
- Modify: `internal/log/slog_test.go:17`
- Modify: `cmd/server/main.go:91`
- Test: `internal/log/config_test.go`

- [ ] **Step 1: 写 config 解析的失败测试**

创建 `internal/log/config_test.go`：

```go
package log

import (
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestParseLevel 覆盖 LOG_LEVEL 各取值与非法值 fallback。
func TestParseLevel(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want slog.Level
	}{
		{name: "debug 小写", in: "debug", want: slog.LevelDebug},   // 正常：debug
		{name: "INFO 大写", in: "INFO", want: slog.LevelInfo},       // 正常：大小写不敏感
		{name: "warn", in: "warn", want: slog.LevelWarn},           // 正常：warn
		{name: "error 带空格", in: " error ", want: slog.LevelError}, // 边界：首尾空格应被裁剪
		{name: "非法值 fallback Info", in: "verbose", want: slog.LevelInfo}, // 异常：非法值回退 Info
		{name: "空串 fallback Info", in: "", want: slog.LevelInfo},  // 边界：空串回退 Info
	}
	for _, tc := range cases {
		// 子测试覆盖该 LOG_LEVEL 输入对应的解析结果。
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, parseLevel(tc.in))
		})
	}
}

// TestParseFormat 覆盖 LOG_FORMAT 取值与非法值 fallback。
func TestParseFormat(t *testing.T) {
	assert.Equal(t, "text", parseFormat("text")) // 正常：text
	assert.Equal(t, "json", parseFormat("json")) // 正常：json
	assert.Equal(t, "json", parseFormat("xml"))  // 异常：非法值回退 json
	assert.Equal(t, "json", parseFormat(""))     // 边界：空串回退 json
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/log/ -run 'TestParseLevel|TestParseFormat' -v`
Expected: FAIL，`undefined: parseLevel` / `undefined: parseFormat`

- [ ] **Step 3: 实现 config.go**

创建 `internal/log/config.go`：

```go
package log

import (
	"log/slog"
	"os"
	"strings"
)

// Config 控制顶层 logger 的输出行为，由 env 解析得到。
type Config struct {
	Level  slog.Level // 日志级别，低于此级别的记录被丢弃
	Format string     // 输出格式："json"（默认）或 "text"（本地调试友好）
}

// ParseConfigFromEnv 从 LOG_LEVEL / LOG_FORMAT 读取配置；非法值各自回退默认。
func ParseConfigFromEnv() Config {
	return Config{
		Level:  parseLevel(os.Getenv("LOG_LEVEL")),
		Format: parseFormat(os.Getenv("LOG_FORMAT")),
	}
}

// parseLevel 解析级别字符串，大小写不敏感并裁剪首尾空格；非法值回退 Info。
func parseLevel(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		// info 与一切非法值统一回退 Info，保证生产默认行为不变。
		return slog.LevelInfo
	}
}

// parseFormat 解析输出格式；非法值回退 json，保证容器日志可解析。
func parseFormat(s string) string {
	if strings.ToLower(strings.TrimSpace(s)) == "text" {
		return "text"
	}
	return "json"
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/log/ -run 'TestParseLevel|TestParseFormat' -v`
Expected: PASS

- [ ] **Step 5: 改 NewSlogLogger 签名支持 Config**

修改 `internal/log/slog.go`，将函数头注释与签名（48-65 行）替换为：

```go
// NewSlogLogger 构造 manager-api / agent 顶层 logger。
//   - 输出：cfg.Format 为 "text" 时用 TextHandler（本地调试），否则 JSONHandler（容器日志/ELK）
//   - 级别：cfg.Level（由 LOG_LEVEL 解析，默认 Info）
//   - 脱敏：Writer 经 NewRedactingWriter 包装，json/text 两种格式都生效
//   - trace_id：requestIDHandler 自动从 ctx 注入，不受格式影响
//   - source：AddSource=true 含 caller 路径，便于错误定位
//
// out 为 nil 时默认走 os.Stderr。
func NewSlogLogger(out io.Writer, cfg Config) *slog.Logger {
	if out == nil {
		out = os.Stderr
	}
	w := NewRedactingWriter(out)
	opts := &slog.HandlerOptions{
		Level:     cfg.Level,
		AddSource: true,
	}
	var base slog.Handler
	if cfg.Format == "text" {
		base = slog.NewTextHandler(w, opts)
	} else {
		base = slog.NewJSONHandler(w, opts)
	}
	return slog.New(&requestIDHandler{Handler: base})
}
```

- [ ] **Step 6: 更新现有两个调用方**

修改 `cmd/server/main.go:91`：

```go
	logger := managerlog.NewSlogLogger(logOut, managerlog.ParseConfigFromEnv())
```

修改 `internal/log/slog_test.go:17`（`logger := NewSlogLogger(buf)`）为：

```go
	logger := NewSlogLogger(buf, Config{Level: slog.LevelInfo, Format: "json"})
```

如该测试文件未 import `"log/slog"`，补上 import。

- [ ] **Step 7: 写 text 格式与级别过滤的测试**

在 `internal/log/config_test.go` 顶部已有的 import 块里补上 `"bytes"`（不要新建第二个 import 块），然后追加两个测试函数：

```go
// TestNewSlogLogger_text格式仍脱敏 验证 text 格式下脱敏仍生效。
func TestNewSlogLogger_text格式仍脱敏(t *testing.T) {
	var buf bytes.Buffer
	logger := NewSlogLogger(&buf, Config{Level: slog.LevelInfo, Format: "text"})
	logger.Info("login", slog.String("password", "hunter2"))
	out := buf.String()
	assert.NotContains(t, out, "hunter2") // 脱敏：password 值不应出现
	assert.Contains(t, out, "***")        // 脱敏：替换为 ***
}

// TestNewSlogLogger_级别过滤 验证低于配置级别的记录被丢弃。
func TestNewSlogLogger_级别过滤(t *testing.T) {
	var buf bytes.Buffer
	logger := NewSlogLogger(&buf, Config{Level: slog.LevelInfo, Format: "json"})
	logger.Debug("noisy", slog.String("k", "v")) // Debug 低于 Info，应被丢弃
	assert.Empty(t, buf.String())                // 无输出
}
```

- [ ] **Step 8: 运行 log 包全部测试**

Run: `go test ./internal/log/ -v`
Expected: PASS（含原有 redact / slog 测试）

- [ ] **Step 9: 提交**

```bash
git add internal/log/config.go internal/log/config_test.go internal/log/slog.go internal/log/slog_test.go cmd/server/main.go
git commit -m "feat(log): 日志级别与输出格式支持 env 配置

NewSlogLogger 改为接收 Config（Level/Format），通过 LOG_LEVEL（debug/info/warn/error）
与 LOG_FORMAT（json/text）env 配置；非法值各自回退 Info / json。text 与 json 两种格式
下脱敏与 trace_id 注入均保持生效。"
```

---

## Task 2: 日志规范常量与 helper

**Files:**
- Create: `internal/log/attrs.go`
- Test: `internal/log/attrs_test.go`

- [ ] **Step 1: 写 Err helper 测试**

创建 `internal/log/attrs_test.go`：

```go
package log

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestErr 验证 Err helper 用统一 key "error" 包装错误信息。
func TestErr(t *testing.T) {
	attr := Err(errors.New("boom"))
	assert.Equal(t, KeyError, attr.Key)        // 统一 key 常量
	assert.Equal(t, "boom", attr.Value.String()) // 值为 err.Error()
}

// TestErr_nil 验证 nil error 返回空串值，避免 panic。
func TestErr_nil(t *testing.T) {
	attr := Err(nil)
	assert.Equal(t, KeyError, attr.Key) // key 仍为 error
	assert.Equal(t, "", attr.Value.String()) // 值为空串
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/log/ -run TestErr -v`
Expected: FAIL，`undefined: Err` / `undefined: KeyError`

- [ ] **Step 3: 实现 attrs.go**

创建 `internal/log/attrs.go`：

```go
package log

import "log/slog"

// 统一 attr key 常量，避免各处字符串字面量漂移；新增日志字段优先复用这里。
const (
	KeyService    = "service"     // 外部依赖标识：newapi / ragflow
	KeyMethod     = "method"      // HTTP 方法
	KeyRoute      = "route"       // gin 路由模板（非真实路径，避免 ID 进日志）
	KeyEndpoint   = "endpoint"    // 外部调用路径（不含 query）
	KeyStatus     = "status"      // HTTP 状态码
	KeyLatencyMS  = "latency_ms"  // 处理耗时（毫秒）
	KeyClientIP   = "client_ip"   // 客户端 IP
	KeyUserID     = "user_id"     // 请求主体用户 ID
	KeyOrgID      = "org_id"      // 组织 ID
	KeyActorID    = "actor_id"    // 操作者 ID（审计语义）
	KeyTargetType = "target_type" // 操作目标类型
	KeyTargetID   = "target_id"   // 操作目标 ID
	KeyAction     = "action"      // 业务动作
	KeyBytes      = "bytes"       // 响应字节数
	KeyError      = "error"       // 错误信息统一 key
)

// Err 把 error 包装成统一 key 的 slog.Attr；nil 时值为空串，避免调用方判空。
func Err(err error) slog.Attr {
	if err == nil {
		return slog.String(KeyError, "")
	}
	return slog.String(KeyError, err.Error())
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/log/ -run TestErr -v`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add internal/log/attrs.go internal/log/attrs_test.go
git commit -m "feat(log): 新增日志字段常量与 Err helper

为 access log、外部调用日志与后续散落日志重构提供统一 attr key 常量
（service/method/route/status/latency_ms 等）与 Err(error) 包装，
统一错误字段名为 error，避免各处字符串字面量漂移。"
```

---

## Task 3: 日志规范文档

**Files:**
- Create: `docs/logging-conventions.md`

- [ ] **Step 1: 写规范文档**

创建 `docs/logging-conventions.md`：

```markdown
# 日志规范

manager-api 统一使用标准库 `log/slog`，输出经 `internal/log` 包装（脱敏 + trace_id 自动注入）。

## 字段命名

- 字段 key 一律使用 `internal/log` 中的 `Key*` 常量，不要写裸字符串字面量。
- 错误统一用 `log.Err(err)`（key 固定为 `error`），不要再写 `slog.String("err", ...)` 之类的变体。
- trace_id 由 `requestIDHandler` 从 ctx 自动注入，业务代码不手填；务必用 `*Context` 系列方法
  （`slog.InfoContext` / `WarnContext` / `ErrorContext`）并传 `ctx`，否则丢 trace_id。

## 级别原则

- **Debug**：正常流程的细粒度追踪（如外部调用成功）。生产默认不输出。
- **Info**：正常业务里程碑（如 HTTP access log、后台任务完成）。
- **Warn**：可恢复 / 不阻塞主流程的异常（如外部清理失败但主业务已成功、可重试错误）。
- **Error**：不可恢复或导致数据不一致、需人工介入的错误。

## 约束

- 外部依赖调用（new-api / RAGFlow）的请求元数据由 `internal/integrations/httplog` 的 RoundTripper
  统一记录，service 层不要重复记录成功调用与请求细节，只在需要业务上下文时补充。
- HTTP 请求由 access log 中间件统一记录，handler 不要逐个再记一遍请求行。
- 不记录请求 / 响应 body；不打印 token、密码、密钥（脱敏 writer 是兜底，不是允许打印的理由）。

## 配置

- `LOG_LEVEL`：debug / info / warn / error，默认 info。生产排故可临时调 debug。
- `LOG_FORMAT`：json（默认，容器/ELK）/ text（本地调试人眼友好）。
```

- [ ] **Step 2: 提交**

```bash
git add docs/logging-conventions.md
git commit -m "docs: 增加日志规范文档

约定字段命名（Key* 常量 / log.Err）、级别使用原则（Debug/Info/Warn/Error）、
外部调用与 HTTP 请求由基础设施层统一记录的边界，以及 LOG_LEVEL/LOG_FORMAT 配置说明。"
```

---

## Task 4: HTTP access log 中间件

**Files:**
- Create: `internal/api/middleware/access_log.go`
- Modify: `internal/api/router.go:98`
- Test: `internal/api/middleware/access_log_test.go`

- [ ] **Step 1: 写中间件测试**

创建 `internal/api/middleware/access_log_test.go`：

```go
package middleware

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"oc-manager/internal/auth" // 以实际 module path 为准
)

// newCapturingLogger 返回写入 buf 的 JSON logger，供断言日志字段。
func newCapturingLogger(buf *bytes.Buffer) *slog.Logger {
	return slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

// lastLogLine 解析 buf 中最后一条 JSON 日志为 map。
func lastLogLine(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()
	lines := bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n"))
	require.NotEmpty(t, lines)
	var m map[string]any
	require.NoError(t, json.Unmarshal(lines[len(lines)-1], &m))
	return m
}

// TestAccessLog_级别按状态分流 验证 2xx→info、4xx→warn、5xx→error。
func TestAccessLog_级别按状态分流(t *testing.T) {
	cases := []struct {
		name      string
		status    int
		wantLevel string
	}{
		{name: "2xx info", status: http.StatusOK, wantLevel: "INFO"},                 // 正常请求记 Info
		{name: "4xx warn", status: http.StatusBadRequest, wantLevel: "WARN"},         // 客户端错误记 Warn
		{name: "5xx error", status: http.StatusInternalServerError, wantLevel: "ERROR"}, // 服务端错误记 Error
	}
	for _, tc := range cases {
		// 子测试覆盖该状态码对应的日志级别。
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			old := slog.Default()
			slog.SetDefault(newCapturingLogger(&buf))
			defer slog.SetDefault(old)

			gin.SetMode(gin.TestMode)
			r := gin.New()
			r.Use(AccessLog())
			r.GET("/api/v1/orgs/:id", func(c *gin.Context) { c.Status(tc.status) })

			req := httptest.NewRequest(http.MethodGet, "/api/v1/orgs/abc", nil)
			r.ServeHTTP(httptest.NewRecorder(), req)

			m := lastLogLine(t, &buf)
			assert.Equal(t, "http_request", m["msg"])
			assert.Equal(t, tc.wantLevel, m["level"])
			assert.Equal(t, "/api/v1/orgs/:id", m["route"]) // route 用模板而非真实 ID
			assert.Equal(t, "GET", m["method"])
			assert.Equal(t, float64(tc.status), m["status"])
		})
	}
}

// TestAccessLog_带user_id 验证 ctx 中有 principal 时记录 user_id。
func TestAccessLog_带user_id(t *testing.T) {
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(newCapturingLogger(&buf))
	defer slog.SetDefault(old)

	gin.SetMode(gin.TestMode)
	r := gin.New()
	// 模拟 auth 中间件：把 principal 注入 request ctx。
	r.Use(func(c *gin.Context) {
		ctx := auth.WithPrincipal(c.Request.Context(), auth.Principal{UserID: "u-123"})
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	})
	r.Use(AccessLog())
	r.GET("/ping", func(c *gin.Context) { c.Status(http.StatusOK) })

	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/ping", nil))

	m := lastLogLine(t, &buf)
	assert.Equal(t, "u-123", m["user_id"])
}

// TestAccessLog_跳过健康检查 验证 /healthz 不产生 access log。
func TestAccessLog_跳过健康检查(t *testing.T) {
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(newCapturingLogger(&buf))
	defer slog.SetDefault(old)

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(AccessLog())
	r.GET("/healthz", func(c *gin.Context) { c.Status(http.StatusOK) })

	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/healthz", nil))
	assert.Empty(t, buf.String()) // 健康检查不记日志
}
```

> 注意：`auth.WithPrincipal` 注入的是 `c.Request.Context()`，中间件顺序上 AccessLog 必须在 auth 之后，才能在 `c.Next()` 返回后读到 principal。测试里手动模拟该顺序。`module path` 以 `go.mod` 第一行为准替换 `oc-manager`。

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/api/middleware/ -run TestAccessLog -v`
Expected: FAIL，`undefined: AccessLog`

- [ ] **Step 3: 实现中间件**

创建 `internal/api/middleware/access_log.go`：

```go
package middleware

import (
	"log/slog"
	"time"

	"github.com/gin-gonic/gin"

	"oc-manager/internal/auth" // 以实际 module path 为准
	mlog "oc-manager/internal/log"
)

// skipAccessLogPaths 是纯噪音、不记 access log 的路径（健康检查 / 就绪探针）。
// 与 handlers.RegisterHealthRoutes 注册的路径保持一致。
var skipAccessLogPaths = map[string]bool{
	"/healthz": true,
	"/readyz":  true,
}

// AccessLog 在每个 HTTP 请求结束后记录一条结构化访问日志（仅 stdout）。
//
// 字段：method / route（路由模板，避免真实 ID 进日志致基数爆炸）/ status /
// latency_ms / client_ip / user_id（鉴权后从 ctx 取，未鉴权为空）/ bytes。
// trace_id 由 internal/log 的 requestIDHandler 自动注入。
//
// 级别：5xx→Error，4xx→Warn，其余→Info。健康检查路径跳过。
//
// 必须挂在 RequireUserAuth 之后，才能在 c.Next() 返回后读到 principal。
func AccessLog() gin.HandlerFunc {
	return func(c *gin.Context) {
		if skipAccessLogPaths[c.Request.URL.Path] {
			c.Next()
			return
		}
		start := time.Now()
		c.Next()

		status := c.Writer.Status()
		// route 用 gin 路由模板；未匹配路由（404）FullPath 为空，回退原始 path。
		route := c.FullPath()
		if route == "" {
			route = c.Request.URL.Path
		}
		var userID string
		if p, ok := auth.PrincipalFromContext(c.Request.Context()); ok {
			userID = p.UserID
		}

		level := slog.LevelInfo
		switch {
		case status >= 500:
			level = slog.LevelError
		case status >= 400:
			level = slog.LevelWarn
		}

		slog.LogAttrs(c.Request.Context(), level, "http_request",
			slog.String(mlog.KeyMethod, c.Request.Method),
			slog.String(mlog.KeyRoute, route),
			slog.Int(mlog.KeyStatus, status),
			slog.Int64(mlog.KeyLatencyMS, time.Since(start).Milliseconds()),
			slog.String(mlog.KeyClientIP, c.ClientIP()),
			slog.String(mlog.KeyUserID, userID),
			slog.Int(mlog.KeyBytes, c.Writer.Size()),
		)
	}
}
```

> 若 `c.Writer.Size()` 在未写 body 时返回 -1，按现状记录即可（表示无 body），无需特殊处理。

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/api/middleware/ -run TestAccessLog -v`
Expected: PASS

- [ ] **Step 5: 挂载到 router**

修改 `internal/api/router.go`，在 `middleware.RequireUserAuth()` 之后挂载（access log 需在 auth 后才能拿到 user_id）。先定位 auth 中间件挂载行（约在 `RegisterHealthRoutes` 之后、业务路由组之前），在其紧后插入：

```go
	// access log 挂在 auth 之后：记录每个业务请求的 method/route/status/耗时/user_id（仅 stdout）。
	// 健康检查路径在中间件内部跳过。
	router.Use(middleware.AccessLog())
```

> 实现者注意：先 Read `internal/api/router.go` 确认 `RequireUserAuth` 的实际挂载位置与作用域（全局 `router.Use` 还是某个 group），把 `AccessLog()` 挂在与之相同的作用域、紧随其后。若 auth 是挂在业务 group 上，则 AccessLog 也挂该 group；健康检查走全局不受影响。

- [ ] **Step 6: 运行 api 包测试确认未回归**

Run: `go test ./internal/api/...`
Expected: PASS

- [ ] **Step 7: 提交**

```bash
git add internal/api/middleware/access_log.go internal/api/middleware/access_log_test.go internal/api/router.go
git commit -m "feat(api): 增加 HTTP access log 中间件

每个业务请求结束后记录一条结构化 access log（method/route/status/latency_ms/
client_ip/user_id/bytes，trace_id 自动注入），仅打 stdout。route 用 gin 路由模板
避免真实 ID 致日志基数爆炸；级别按状态分流（5xx→Error/4xx→Warn/其余 Info）；
健康检查路径跳过。挂载在 auth 中间件之后以获取 user_id。"
```

---

## Task 5: 外部调用日志（RoundTripper）

**Files:**
- Create: `internal/integrations/httplog/transport.go`
- Modify: `internal/integrations/newapi/client.go:211-218`
- Modify: `internal/integrations/ragflow/client.go:241-265`
- Test: `internal/integrations/httplog/transport_test.go`

- [ ] **Step 1: 写 RoundTripper 测试**

创建 `internal/integrations/httplog/transport_test.go`：

```go
package httplog

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// rtFunc 把函数适配为 http.RoundTripper，用作测试 base transport。
type rtFunc func(*http.Request) (*http.Response, error)

func (f rtFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// resp 构造一个指定状态码的最小响应。
func resp(code int) *http.Response {
	return &http.Response{StatusCode: code, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header)}
}

func capture(buf *bytes.Buffer) *slog.Logger {
	return slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

func lastLine(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()
	lines := bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n"))
	require.NotEmpty(t, lines)
	var m map[string]any
	require.NoError(t, json.Unmarshal(lines[len(lines)-1], &m))
	return m
}

// TestRoundTrip_成功记debug 验证 2xx 记 Debug 且字段齐全、endpoint 不含 query。
func TestRoundTrip_成功记debug(t *testing.T) {
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(capture(&buf))
	defer slog.SetDefault(old)

	base := rtFunc(func(r *http.Request) (*http.Response, error) { return resp(200), nil })
	rt := New(base, "newapi")
	req, _ := http.NewRequest(http.MethodGet, "http://x/api/user?token=secret", nil)
	_, err := rt.RoundTrip(req)
	require.NoError(t, err)

	m := lastLine(t, &buf)
	assert.Equal(t, "external_request", m["msg"])
	assert.Equal(t, "DEBUG", m["level"])
	assert.Equal(t, "newapi", m["service"])
	assert.Equal(t, "GET", m["method"])
	assert.Equal(t, "/api/user", m["endpoint"]) // 不含 query
	assert.Equal(t, float64(200), m["status"])
	assert.NotContains(t, buf.String(), "secret") // query 不进日志
}

// TestRoundTrip_非2xx记warn 验证 4xx/5xx 记 Warn。
func TestRoundTrip_非2xx记warn(t *testing.T) {
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(capture(&buf))
	defer slog.SetDefault(old)

	base := rtFunc(func(r *http.Request) (*http.Response, error) { return resp(500), nil })
	req, _ := http.NewRequest(http.MethodGet, "http://x/api/user", nil)
	_, _ = New(base, "ragflow").RoundTrip(req)

	m := lastLine(t, &buf)
	assert.Equal(t, "WARN", m["level"])
	assert.Equal(t, float64(500), m["status"])
}

// TestRoundTrip_传输错误记warn 验证 transport error 记 Warn、带 error 不带 status。
func TestRoundTrip_传输错误记warn(t *testing.T) {
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(capture(&buf))
	defer slog.SetDefault(old)

	base := rtFunc(func(r *http.Request) (*http.Response, error) { return nil, errors.New("dial fail") })
	req, _ := http.NewRequest(http.MethodGet, "http://x/api/user", nil)
	_, err := New(base, "newapi").RoundTrip(req)
	require.Error(t, err) // 错误必须原样透传给调用方

	m := lastLine(t, &buf)
	assert.Equal(t, "WARN", m["level"])
	assert.Equal(t, "dial fail", m["error"])
	_, hasStatus := m["status"]
	assert.False(t, hasStatus) // transport error 无状态码
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/integrations/httplog/ -v`
Expected: FAIL，`undefined: New`

- [ ] **Step 3: 实现 transport.go**

创建 `internal/integrations/httplog/transport.go`：

```go
// Package httplog 提供记录出站 HTTP 调用元数据的 RoundTripper，
// 供 newapi / ragflow 等 integration client 在传输层统一接入，
// 业务方法无需逐个手写调用日志。
package httplog

import (
	"log/slog"
	"net/http"
	"time"

	mlog "oc-manager/internal/log" // 以实际 module path 为准
)

// transport 包装内层 RoundTripper，在每次出站请求后记录 service/method/endpoint/status/latency。
type transport struct {
	base    http.RoundTripper
	service string // 依赖标识，如 newapi / ragflow
}

// New 返回带日志的 RoundTripper；base 为 nil 时退回 http.DefaultTransport。
func New(base http.RoundTripper, service string) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return &transport{base: base, service: service}
}

// RoundTrip 计时执行内层请求并记录元数据；不读取/不记录 body，错误原样透传。
func (t *transport) RoundTrip(req *http.Request) (*http.Response, error) {
	start := time.Now()
	resp, err := t.base.RoundTrip(req)
	latency := time.Since(start).Milliseconds()

	// 用 req.Context() 作为 ctx，使外部调用日志自动带上发起方请求的 trace_id，实现链路串联。
	ctx := req.Context()
	attrs := []slog.Attr{
		slog.String(mlog.KeyService, t.service),
		slog.String(mlog.KeyMethod, req.Method),
		slog.String(mlog.KeyEndpoint, req.URL.Path), // 仅 path，不含 query，避免敏感参数泄露
		slog.Int64(mlog.KeyLatencyMS, latency),
	}
	if err != nil {
		attrs = append(attrs, mlog.Err(err))
		slog.LogAttrs(ctx, slog.LevelWarn, "external_request", attrs...)
		return resp, err
	}
	attrs = append(attrs, slog.Int(mlog.KeyStatus, resp.StatusCode))
	level := slog.LevelDebug
	if resp.StatusCode >= 300 {
		level = slog.LevelWarn
	}
	slog.LogAttrs(ctx, level, "external_request", attrs...)
	return resp, err
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/integrations/httplog/ -v`
Expected: PASS

- [ ] **Step 5: newapi client 接入**

修改 `internal/integrations/newapi/client.go` 的 `NewClient`（211-218 行）。先确认文件已 import `"net/http"`（已有），并 import httplog 包，把构造改为给 `HTTPClient` 注入 logging transport，同时把同一 client 赋给 `c.base.HTTPClient` 保证两条请求路径都被覆盖：

```go
func NewClient(baseURL, adminToken string, adminUserID int64) *Client {
	// 注入带日志的 transport：所有出站 new-api 调用在传输层统一记录元数据。
	httpClient := &http.Client{Transport: httplog.New(nil, "newapi")}
	c := &Client{
		BaseURL:     baseURL,
		AdminToken:  adminToken,
		AdminUserID: adminUserID,
		HTTPClient:  httpClient,
	}
	c.base = &httpclient.BaseHTTPClient{
		BaseURL:    baseURL,
		AuthToken:  adminToken,
		HTTPClient: httpClient,
	}
	return c
}
```

import 块加入（以实际 module path 为准）：

```go
	"oc-manager/internal/integrations/httplog"
```

- [ ] **Step 6: ragflow client 接入**

修改 `internal/integrations/ragflow/client.go` 的 `NewClient`（241-265 行），把 `http: &http.Client{Timeout: timeout}` 改为带 logging transport：

```go
		http: &http.Client{
			Timeout:   timeout,
			Transport: httplog.New(nil, "ragflow"),
		},
```

import 块加入：

```go
	"oc-manager/internal/integrations/httplog"
```

- [ ] **Step 7: 运行相关包测试确认未回归**

Run: `go test ./internal/integrations/...`
Expected: PASS

> 若 newapi / ragflow 既有测试通过自定义 `HTTPClient`（如 `httptest.Server` + 注入 transport）断言请求，注入 logging transport 不改变请求语义，应保持通过；如有测试直接构造 `Client{}` 字面量而非走 `NewClient`，不受影响。

- [ ] **Step 8: 提交**

```bash
git add internal/integrations/httplog/ internal/integrations/newapi/client.go internal/integrations/ragflow/client.go
git commit -m "feat(integrations): 外部调用统一记录元数据日志

新增 httplog.RoundTripper，在传输层记录 new-api / RAGFlow 出站调用的
service/method/endpoint/status/latency_ms（成功 Debug、非 2xx 与传输错误 Warn），
不记 body、endpoint 去掉 query 避免泄密，用请求 ctx 串联 trace_id。
newapi / ragflow client 在构造函数注入该 transport，业务方法零改动。"
```

---

## Task 6: 散落日志全量重构（最后做，分批逐文件提交）

> **前置依赖**：Task 2（`Key*` 常量 + `Err`）与 Task 3（规范文档）必须已合入。本任务是机械对齐，不引入行为变更；逐文件提交以便独立评审。

**Files:**
- Modify: `internal/service/*.go`、`internal/worker/handlers/*.go`（逐文件）

- [ ] **Step 1: 列出待改文件清单**

Run（绕过可能的输出压缩，直接 grep）：

```bash
grep -rln "slog\." internal/service internal/worker/handlers
```

把输出存为待办清单。按文件逐个处理，**一个文件一个提交**。

- [ ] **Step 2: 对单个文件应用对齐规则**

对清单中每个文件，按以下规则改写其中的 slog 调用（只改日志，不动业务逻辑）：

1. **字段 key 换常量**：裸字符串 key（如 `slog.String("org_id", ...)`）换成 `mlog.KeyOrgID` 等常量；无对应常量且确属通用字段的，先回 Task 2 的 `attrs.go` 补常量再用。
2. **错误字段统一**：`slog.String("error"/"err", err.Error())` 一律换成 `mlog.Err(err)`。
3. **级别修正**：按规范原则核对——可恢复/不阻塞主流程用 `WarnContext`，不可恢复/数据不一致用 `ErrorContext`，正常里程碑用 `InfoContext`，细粒度追踪用 `DebugContext`。改级别若涉及业务判断，在 commit message 注明理由。
4. **必须传 ctx**：把 `slog.Warn(...)` 之类无 ctx 调用换成 `slog.WarnContext(ctx, ...)`（就近可得的 ctx），以保留 trace_id；无 ctx 可得的启动期日志保持原样。
5. **去重**：删除与 access log（HTTP 请求行）或 httplog（外部调用成功/请求细节）重复的记录；保留带额外业务上下文的记录。
6. 文件若尚未 import `internal/log`，加别名 import：`mlog "oc-manager/internal/log"`（以实际 module path 为准）。

**worked example**（`internal/service/organization_service.go` 风格示意）：

改写前：

```go
slog.WarnContext(ctx, "清理 new-api user 失败", slog.String("org_id", orgID), slog.String("error", err.Error()))
```

改写后：

```go
slog.WarnContext(ctx, "清理 new-api user 失败", slog.String(mlog.KeyOrgID, orgID), mlog.Err(err))
```

- [ ] **Step 3: 该文件相关测试 + 构建**

Run（替换 `<pkg>` 为该文件所属包）：

```bash
go build ./... && go test ./<pkg>/
```

Expected: PASS（无行为变更，原测试应继续通过）

- [ ] **Step 4: 提交该文件**

```bash
git add <该文件>
git commit -m "refactor(log): 对齐 <包名> 日志字段与级别

将裸字符串字段 key 换为 internal/log 常量、错误统一用 log.Err，
按规范修正级别，无行为变更。"
```

- [ ] **Step 5: 重复 Step 2-4 直到清单清空**

每个文件独立走一遍 Step 2-4。全部完成后进入 Task 7。

---

## Task 7: 全量构建与真实环境验证

**Files:** 无（验证任务）

- [ ] **Step 1: 全量构建与测试**

Run:

```bash
go build ./... && go test ./...
```

Expected: PASS

- [ ] **Step 2: OpenAPI 同步检查**

> 本次未改 handler 签名 / 请求体 / 响应类型 / 路由（access log 是中间件，不改契约），理论上 openapi 无变化。仍按项目规范确认工作区干净：

Run:

```bash
make openapi-check
```

Expected: 工作区干净（无 diff）。如有 diff 说明误触契约，需排查。

- [ ] **Step 3: 本地 k3d 真实环境验证**

按 `CLAUDE.md` 要求做真实环境验证（非 curl 替代浏览器逻辑，但日志验证需看后端 stdout）：

1. 本地起 manager（`make local-up` 或重新 rollout manager-api，确保加载新镜像）。
2. 浏览器登录 `http://ocm.localhost`（admin / admin123），点几下页面触发若干 API。
3. 触发一次会调用 new-api / RAGFlow 的操作（如查看余额 / 用量、或知识库相关操作）。
4. 看 manager-api pod 日志（`ywjskubectl` 仅用于线上；本地用 `rtk proxy kubectl -n ocm logs ...` 或 `make` 提供的本地日志入口），确认：
   - 出现 `"msg":"http_request"` 行，含 method/route（模板形式）/status/latency_ms/user_id/trace_id；
   - 出现 `"msg":"external_request"` 行，service=newapi/ragflow，含 endpoint（无 query）/status/latency_ms，且与触发它的请求 trace_id 一致（链路串联）；
   - 任意日志中均无明文 token/password/sk- key（脱敏生效）。
5. 临时设 `LOG_LEVEL=debug` 重启，确认外部调用成功的 Debug 行出现；设 `LOG_FORMAT=text` 确认本地可读文本格式生效；验证后恢复默认。

- [ ] **Step 4: 交付说明**

汇总：改动文件矩阵、各任务测试结果、真实环境验证证据（关键日志行摘录，注意先脱敏再贴）。若某步未能运行（如本地环境不可用），明确写明原因与风险。

---

## Self-Review 记录

- **Spec 覆盖**：模块 1（可配置化）→ Task 1；模块 2（外部调用）→ Task 5；模块 3 拆为常量/helper（Task 2）+ 规范文档（Task 3）+ 全量 sweep（Task 6）；模块 2（access log）→ Task 4。spec 全部章节有对应任务。
- **占位符扫描**：Task 6 因跨多文件无法逐行预写，已用「明确规则 + worked example + 逐文件提交」表达，非占位符；其余任务均含完整代码。
- **类型一致性**：`Config{Level, Format}`、`Key*` 常量、`Err`、`New(base, service)`、`AccessLog()` 在定义与使用处命名一致；module path 统一标注「以实际 go.mod 为准」需实现者替换 `oc-manager`。
