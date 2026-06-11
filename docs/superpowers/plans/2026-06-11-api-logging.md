# 整理 oc-manager API 日志 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 补齐 manager-api 的 HTTP access log、统一外部调用日志、新增 SQL 日志、让日志级别/格式/慢查询阈值可配置，给所有日志加 `log_type` 分类字段，并制定日志规范后全量重构散落日志。

**Architecture:** 沿用现有 `log/slog` + 脱敏 writer + trace_id 自动注入的基础设施。新增可配置 `Config` 入口；access log 作为 gin 中间件只打 stdout JSON；外部调用日志走自定义 `http.RoundTripper`、SQL 日志走包装 `sqlc.DBTX` 接口，均在基础设施层统一拦截、业务零改动；统一 `log_type` 字段（http/sql/newapi/ragflow/app），其中业务类 `app` 由 `requestIDHandler` 兜底注入、基础设施类在调用点显式带；先落地规范常量与 helper，再按目录分批重构散落日志。

**Tech Stack:** Go 1.22+、`log/slog`、gin v1.10、`database/sql` + sqlc、`github.com/stretchr/testify`。

> **2026-06-12 修订**：在原 4 模块基础上增补「SQL 日志」与统一 `log_type` 字段（见 spec 模块 5）。Task 重新编号：新增 Task 4（log_type 常量+handler 兜底）、Task 7（SQL 日志），原 access log / 外部调用 / sweep / 验证依次后移为 Task 5/6/8/9。Task 1-3 已完成，不回改其提交；增量通过新 Task 落地。

设计文档：`docs/superpowers/specs/2026-06-11-api-logging-design.md`

---

## 文件结构

| 文件 | 职责 | 动作 |
|---|---|---|
| `internal/log/config.go` | `Config` 结构 + `ParseConfigFromEnv`（解析 `LOG_LEVEL` / `LOG_FORMAT`） | 创建（Task 1 已完成） |
| `internal/log/slog.go` | `NewSlogLogger` 改为接收 `Config`；`requestIDHandler` 兜底注入 `log_type=app` | 修改（Task 1 已改 / Task 4 续改） |
| `cmd/server/main.go` | 装配点：`NewSlogLogger(logOut, ParseConfigFromEnv())` | 修改（Task 1 已完成） |
| `internal/log/attrs.go` | 统一 attr key 常量 + `Err` helper；补 `KeyLogType` 与 5 个取值常量 | 创建（Task 2 已完成 / Task 4 续补） |
| `internal/api/middleware/access_log.go` | HTTP access log 中间件（带 `log_type=http`） | 创建 |
| `internal/api/router.go` | 全局挂载 access log 中间件（RequestID 之后、auth/CSRF 之前） | 修改（98 行附近） |
| `internal/integrations/httplog/transport.go` | logging `http.RoundTripper`（`log_type=newapi/ragflow`） | 创建 |
| `internal/integrations/newapi/client.go` | `NewClient` 注入 logging transport | 修改（211 行附近） |
| `internal/integrations/ragflow/client.go` | `NewClient` 注入 logging transport | 修改（241 行附近） |
| `internal/store/dblog.go` | 包装 `sqlc.DBTX` 的 logging wrapper（`log_type=sql`、慢查询、错误） | 创建 |
| `internal/store/store.go` | `New` 与 `WithTx` 接入 logging DBTX（覆盖事务内外） | 修改 |
| `docs/logging-conventions.md` | 日志规范文档；增补 SQL 日志、`log_type`、`LOG_SLOW_QUERY_MS` 小节 | 创建（Task 3 已完成 / Task 4、7 续补） |
| `internal/service/*`、`internal/worker/handlers/*` | 散落日志对齐规范 | 修改（分批、逐文件分提交） |

实现顺序：Task 1（可配置化）✅ → Task 2（规范常量+helper）✅ → Task 3（规范文档）✅ → **Task 4（log_type 常量+handler 兜底）** → Task 5（access log）/ Task 6（外部调用）/ Task 7（SQL 日志）可并行 → Task 8（全量 sweep，最后做）→ Task 9（构建与真实环境验证）。

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

## Task 4: log_type 常量与 handler 兜底注入

> 为 Task 5（access log）/ Task 6（外部调用）/ Task 7（SQL）提供统一的 `log_type` 词汇表，并让业务日志的 `app` 类型由 handler 兜底注入，免去 sweep 时逐条手加。

**Files:**
- Modify: `internal/log/attrs.go`（补 `KeyLogType` + 5 个取值常量）
- Modify: `internal/log/slog.go`（`requestIDHandler.Handle` 兜底注入 `log_type=app`）
- Modify: `docs/logging-conventions.md`（补 `log_type` 小节）
- Test: `internal/log/attrs_test.go`（取值常量）、`internal/log/slog_test.go` 或新增 `internal/log/logtype_test.go`（兜底注入）

- [ ] **Step 1: 写兜底注入的失败测试**

新增 `internal/log/logtype_test.go`：

```go
package log

import (
	"bytes"
	"encoding/json"
	"context"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// parseLast 解析 buf 最后一条 JSON 日志为 map。
func parseLast(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()
	lines := bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n"))
	require.NotEmpty(t, lines)
	var m map[string]any
	require.NoError(t, json.Unmarshal(lines[len(lines)-1], &m))
	return m
}

// TestLogType_未带时兜底注入app 验证未显式带 log_type 的日志被自动补 app。
func TestLogType_未带时兜底注入app(t *testing.T) {
	var buf bytes.Buffer
	logger := NewSlogLogger(&buf, Config{Level: slog.LevelInfo, Format: "json"})
	logger.InfoContext(context.Background(), "业务里程碑")
	m := parseLast(t, &buf)
	assert.Equal(t, LogTypeApp, m[KeyLogType]) // 未带 → 兜底 app
}

// TestLogType_已带时不被覆盖 验证显式带 log_type 的基础设施日志不被兜底改写。
func TestLogType_已带时不被覆盖(t *testing.T) {
	var buf bytes.Buffer
	logger := NewSlogLogger(&buf, Config{Level: slog.LevelInfo, Format: "json"})
	logger.LogAttrs(context.Background(), slog.LevelInfo, "http_request", slog.String(KeyLogType, LogTypeHTTP))
	m := parseLast(t, &buf)
	assert.Equal(t, LogTypeHTTP, m[KeyLogType]) // 已带 → 保持 http，不被覆盖
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/log/ -run TestLogType -v`
Expected: FAIL，`undefined: LogTypeApp` / `undefined: KeyLogType` 等。

- [ ] **Step 3: 补 attrs.go 的 log_type 常量**

在 `internal/log/attrs.go` 的 const 块补 `KeyLogType`，并新增取值常量：

```go
	KeyLogType    = "log_type"    // 日志类型分类：http/sql/newapi/ragflow/app
```

```go
// log_type 取值常量：基础设施类（http/sql/newapi/ragflow）在调用点显式带，
// 业务及其它普通日志统一为 app，由 requestIDHandler 兜底注入。
const (
	LogTypeHTTP    = "http"    // access log 中间件
	LogTypeSQL     = "sql"     // SQL 日志
	LogTypeNewAPI  = "newapi"  // 调 new-api
	LogTypeRAGFlow = "ragflow" // 调 RAGFlow
	LogTypeApp     = "app"     // 业务及其它普通日志（兜底类型）
)
```

- [ ] **Step 4: 在 requestIDHandler.Handle 兜底注入 log_type=app**

Read `internal/log/slog.go` 找到 `requestIDHandler.Handle`。在它注入 trace_id 的同处，增加：遍历 `r.Attrs` 判断是否已有 `log_type`，若无则 `r.AddAttrs(slog.String(KeyLogType, LogTypeApp))`。注意：`record.Attrs` 只遍历 record 自带 attrs，`WithAttrs` 预置的不在其列——基础设施日志的 `log_type` 是在 `LogAttrs` 调用点直接带的 record attr，故能被检出。示意：

```go
func (h *requestIDHandler) Handle(ctx context.Context, r slog.Record) error {
	// ...既有 trace_id 注入逻辑...
	hasLogType := false
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == KeyLogType {
			hasLogType = true
			return false // 命中即停止遍历
		}
		return true
	})
	if !hasLogType {
		r.AddAttrs(slog.String(KeyLogType, LogTypeApp))
	}
	return h.Handler.Handle(ctx, r)
}
```

- [ ] **Step 5: 运行测试确认通过**

Run: `go test ./internal/log/ -v`
Expected: PASS（含原有 config/attrs/redact/slog 测试与新 LogType 测试）。

- [ ] **Step 6: 补规范文档 log_type 小节**

在 `docs/logging-conventions.md` 的「字段命名」节后补一小节，说明 `log_type` 取值与「基础设施类显式带、业务类兜底注入」的约定。

- [ ] **Step 7: 提交**

```bash
git add internal/log/attrs.go internal/log/logtype_test.go internal/log/slog.go docs/logging-conventions.md
git commit -m "feat(log): 新增 log_type 分类字段与 handler 兜底注入

补 KeyLogType 常量与 http/sql/newapi/ragflow/app 取值常量;
requestIDHandler 对未携带 log_type 的日志兜底注入 app,基础设施类
日志在调用点显式带则不被覆盖,使全部日志可按类型过滤而无需逐条手加。"
```

---

## Task 5: HTTP access log 中间件

**Files:**
- Create: `internal/api/middleware/access_log.go`（带 `log_type=http`）
- Modify: `internal/api/router.go`（全局挂载，RequestID 之后、auth/CSRF 之前）
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

> 注意：生产中 AccessLog 全局挂在 auth **之前**，但因中间件先进后出、且 auth 用 `c.Request = c.Request.WithContext(...)` 替换 request，AccessLog 在 `c.Next()` 返回后仍能读到 principal。除上面 `TestAccessLog_带user_id`（auth 在前的简单情形）外，**必须再加一个测试复刻生产顺序**：先 `r.Use(AccessLog())` 再 `r.Use(注入 principal 的 auth-sim)`，断言 user_id 仍被记录。另需断言每条 access log 带 `log_type=http`。`module path` 以 `go.mod` 第一行为准替换 `oc-manager`。

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
// 级别：5xx→Error，4xx→Warn，其余→Info。健康检查路径跳过。log_type 固定 http。
//
// 挂载位置：RequestID 之后、RequireUserAuth 之前——这样未鉴权导致的 4xx、CSRF
// 拒绝、公共路由与 404 也能记到。user_id 在 c.Next() 返回后从 c.Request.Context()
// 读取：RequireUserAuth 在其阶段用 c.Request = c.Request.WithContext(...) 注入
// principal，AccessLog 比它先入栈但后收尾，故读到的仍是带 principal 的 ctx。
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
			slog.String(mlog.KeyLogType, mlog.LogTypeHTTP), // 显式带 log_type，handler 不再兜底
		)
	}
}
```

> 若 `c.Writer.Size()` 在未写 body 时返回 -1，按现状记录即可（表示无 body），无需特殊处理。

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/api/middleware/ -run TestAccessLog -v`
Expected: PASS

- [ ] **Step 5: 全局挂载到 router（RequestID 之后、auth/CSRF 之前）**

修改 `internal/api/router.go`，在 `router.Use(middleware.RequestID())` 之后、`middleware.RequireCSRF()` / auth group 之前，全局挂载：

```go
	// access log 挂在 RequestID 之后、鉴权/CSRF 之前：覆盖全部请求（含登录失败、CSRF
	// 拒绝、公共路由与未匹配 404），未鉴权导致的 4xx 也能记到；user_id 在 c.Next() 返回后
	// 从 c.Request.Context() 读取，此时 RequireUserAuth 已注入 principal，故仍能拿到。
	// 健康检查等纯噪音路径由中间件内部 skip 集合跳过。
	router.Use(middleware.AccessLog())
```

> 设计依据（见 spec 模块 2）：全局挂载而非挂在 user group，是为了让未鉴权 4xx、CSRF 拒绝、public 路由与 404 也被记录。user_id 仍能拿到，因为 `RequireUserAuth` 用 `c.Request = c.Request.WithContext(...)` 替换 request，AccessLog 比它先入栈、后收尾，读到的是带 principal 的 ctx。测试需同时覆盖「auth 在 AccessLog 之前注入」（生产顺序）这一情形。

- [ ] **Step 6: 运行 api 包测试确认未回归**

Run: `go test ./internal/api/...`
Expected: PASS

- [ ] **Step 7: 提交**

```bash
git add internal/api/middleware/access_log.go internal/api/middleware/access_log_test.go internal/api/router.go
git commit -m "feat(api): 增加 HTTP access log 中间件

每个请求结束后记录一条结构化 access log（method/route/status/latency_ms/
client_ip/user_id/bytes/log_type=http，trace_id 自动注入），仅打 stdout。route 用
gin 路由模板避免真实 ID 致日志基数爆炸；级别按状态分流（5xx→Error/4xx→Warn/其余
Info）；健康检查路径跳过。全局挂在 RequestID 之后、auth/CSRF 之前，使未鉴权 4xx /
CSRF 拒绝 / public / 404 也被记录，user_id 仍可在收尾时从 ctx 取得。"
```

---

## Task 6: 外部调用日志（RoundTripper）

> 依赖 Task 4 的 `LogTypeNewAPI` / `LogTypeRAGFlow` 常量。`log_type` 取代原计划的独立 `service` 字段。

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
	assert.Equal(t, "newapi", m["log_type"]) // 用 log_type 区分外部依赖，取代独立 service 字段
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

// transport 包装内层 RoundTripper，在每次出站请求后记录 log_type/method/endpoint/status/latency。
type transport struct {
	base    http.RoundTripper
	logType string // 依赖标识 / 日志类型，如 newapi / ragflow
}

// New 返回带日志的 RoundTripper；base 为 nil 时退回 http.DefaultTransport。
// logType 传 mlog.LogTypeNewAPI / mlog.LogTypeRAGFlow。
func New(base http.RoundTripper, logType string) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return &transport{base: base, logType: logType}
}

// RoundTrip 计时执行内层请求并记录元数据；不读取/不记录 body，错误原样透传。
func (t *transport) RoundTrip(req *http.Request) (*http.Response, error) {
	start := time.Now()
	resp, err := t.base.RoundTrip(req)
	latency := time.Since(start).Milliseconds()

	// 用 req.Context() 作为 ctx，使外部调用日志自动带上发起方请求的 trace_id，实现链路串联。
	ctx := req.Context()
	attrs := []slog.Attr{
		slog.String(mlog.KeyLogType, t.logType),
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
log_type/method/endpoint/status/latency_ms（成功 Debug、非 2xx 与传输错误 Warn），
不记 body、endpoint 去掉 query 避免泄密，用请求 ctx 串联 trace_id。
log_type 取 newapi/ragflow 区分依赖。newapi / ragflow client 在构造函数注入该
transport，业务方法零改动。"
```

---

## Task 7: SQL 日志（logging DBTX）

> 依赖 Task 4 的 `LogTypeSQL` 常量。接入点是 sqlc 的 `DBTX` 接口，`*sql.DB` 与 `*sql.Tx` 都满足它，故包接口即可覆盖普通连接与事务。

**Files:**
- Create: `internal/store/dblog.go`
- Modify: `internal/store/store.go`（`New` 与 `WithTx` 接入）
- Modify: `docs/logging-conventions.md`（补 SQL 日志 + `LOG_SLOW_QUERY_MS` 小节）
- Test: `internal/store/dblog_test.go`

- [ ] **Step 1: 写 logging DBTX 的失败测试**

创建 `internal/store/dblog_test.go`：用 stub `sqlc.DBTX` 作为被包装对象（可控制返回的 `sql.Result`、error、以及通过 sleep 制造慢查询），配合捕获型 `slog.Default()` 断言：

```go
package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubDBTX 是受控的 sqlc.DBTX 实现，用于驱动 loggingDBTX 各分支。
type stubDBTX struct {
	execResult sql.Result
	execErr    error
	delay      time.Duration // 模拟慢查询
}

func (s stubDBTX) ExecContext(ctx context.Context, q string, args ...interface{}) (sql.Result, error) {
	if s.delay > 0 {
		time.Sleep(s.delay)
	}
	return s.execResult, s.execErr
}
func (s stubDBTX) PrepareContext(ctx context.Context, q string) (*sql.Stmt, error) { return nil, nil }
func (s stubDBTX) QueryContext(ctx context.Context, q string, args ...interface{}) (*sql.Rows, error) {
	return nil, s.execErr
}
func (s stubDBTX) QueryRowContext(ctx context.Context, q string, args ...interface{}) *sql.Row {
	return nil
}

// fakeResult 返回固定 RowsAffected，用于断言写操作行数。
type fakeResult struct{ rows int64 }

func (f fakeResult) LastInsertId() (int64, error) { return 0, nil }
func (f fakeResult) RowsAffected() (int64, error) { return f.rows, nil }

func parseLast(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()
	lines := bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n"))
	require.NotEmpty(t, lines)
	var m map[string]any
	require.NoError(t, json.Unmarshal(lines[len(lines)-1], &m))
	return m
}

// TestLoggingDBTX_正常Exec记debug带行数 验证正常写操作记 Debug、带 rows、log_type=sql、不含参数值。
func TestLoggingDBTX_正常Exec记debug带行数(t *testing.T) {
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer slog.SetDefault(old)

	w := newLoggingDBTX(stubDBTX{execResult: fakeResult{rows: 3}}, 200*time.Millisecond)
	_, err := w.ExecContext(context.Background(), "UPDATE users SET name=? WHERE id=?", "secret-name", "u1")
	require.NoError(t, err)

	m := parseLast(t, &buf)
	assert.Equal(t, "DEBUG", m["level"])
	assert.Equal(t, "sql", m["log_type"])
	assert.Equal(t, float64(3), m["rows"])
	assert.NotContains(t, buf.String(), "secret-name") // 不记参数值
}

// TestLoggingDBTX_慢查询记warn 验证耗时超阈值的查询抬到 Warn。
func TestLoggingDBTX_慢查询记warn(t *testing.T) {
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer slog.SetDefault(old)

	w := newLoggingDBTX(stubDBTX{execResult: fakeResult{rows: 1}, delay: 20 * time.Millisecond}, 5*time.Millisecond)
	_, _ = w.ExecContext(context.Background(), "UPDATE t SET x=1", nil)

	m := parseLast(t, &buf)
	assert.Equal(t, "WARN", m["level"]) // 超阈值 → 慢查询
}

// TestLoggingDBTX_执行错误记error 验证执行出错记 Error 且带 error 字段。
func TestLoggingDBTX_执行错误记error(t *testing.T) {
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer slog.SetDefault(old)

	w := newLoggingDBTX(stubDBTX{execErr: errors.New("deadlock")}, 200*time.Millisecond)
	_, err := w.ExecContext(context.Background(), "UPDATE t SET x=1", nil)
	require.Error(t, err) // 错误原样透传

	m := parseLast(t, &buf)
	assert.Equal(t, "ERROR", m["level"])
	assert.Equal(t, "deadlock", m["error"])
}

// TestLoggingDBTX_Query不带行数 验证查询类不消费 rows、不带 rows 字段。
func TestLoggingDBTX_Query不带行数(t *testing.T) {
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer slog.SetDefault(old)

	w := newLoggingDBTX(stubDBTX{}, 200*time.Millisecond)
	_, _ = w.QueryContext(context.Background(), "SELECT 1", nil)

	m := parseLast(t, &buf)
	_, hasRows := m["rows"]
	assert.False(t, hasRows) // 查询类不记 rows
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/store/ -run TestLoggingDBTX -v`
Expected: FAIL，`undefined: newLoggingDBTX`。

- [ ] **Step 3: 实现 dblog.go**

创建 `internal/store/dblog.go`，定义 `loggingDBTX` 实现 `sqlc.DBTX`，包级慢查询阈值从 `LOG_SLOW_QUERY_MS` 读取（默认 200ms）：

```go
package store

import (
	"context"
	"database/sql"
	"log/slog"
	"os"
	"strconv"
	"time"

	mlog "oc-manager/internal/log" // 以实际 module path 为准
	"oc-manager/internal/store/sqlc"
)

// defaultSlowQueryThreshold 是慢查询阈值默认值；可由 LOG_SLOW_QUERY_MS 覆盖。
const defaultSlowQueryThreshold = 200 * time.Millisecond

// slowQueryThreshold 在包初始化时从 env 读一次；非法值回退默认。
var slowQueryThreshold = parseSlowQueryThreshold(os.Getenv("LOG_SLOW_QUERY_MS"))

// parseSlowQueryThreshold 解析毫秒数；空或非法回退默认值。
func parseSlowQueryThreshold(s string) time.Duration {
	if s == "" {
		return defaultSlowQueryThreshold
	}
	ms, err := strconv.Atoi(s)
	if err != nil || ms < 0 {
		return defaultSlowQueryThreshold
	}
	return time.Duration(ms) * time.Millisecond
}

// loggingDBTX 包装 sqlc.DBTX，在每次 Exec/Query 后记录语句/耗时/行数/错误。
// 不记录参数值（避免密码 hash / token / PII 入日志）。log_type 固定 sql。
type loggingDBTX struct {
	inner     sqlc.DBTX
	threshold time.Duration
}

// newLoggingDBTX 构造包装器；threshold 为慢查询阈值。
func newLoggingDBTX(inner sqlc.DBTX, threshold time.Duration) sqlc.DBTX {
	return &loggingDBTX{inner: inner, threshold: threshold}
}

// logQuery 按耗时/错误分级记录一条 SQL 日志。rows<0 表示不记行数（查询类）。
func (l *loggingDBTX) logQuery(ctx context.Context, query string, start time.Time, rows int64, err error) {
	latency := time.Since(start)
	attrs := []slog.Attr{
		slog.String(mlog.KeyLogType, mlog.LogTypeSQL),
		slog.String("sql", query), // sqlc 占位符已参数化，语句不含真实值
		slog.Int64(mlog.KeyLatencyMS, latency.Milliseconds()),
	}
	if rows >= 0 {
		attrs = append(attrs, slog.Int64("rows", rows))
	}
	level := slog.LevelDebug
	switch {
	case err != nil:
		attrs = append(attrs, mlog.Err(err))
		level = slog.LevelError
	case latency > l.threshold:
		level = slog.LevelWarn // 慢查询
	}
	slog.LogAttrs(ctx, level, "sql_query", attrs...)
}

func (l *loggingDBTX) ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error) {
	start := time.Now()
	res, err := l.inner.ExecContext(ctx, query, args...)
	var rows int64 = -1
	if err == nil && res != nil {
		if n, e := res.RowsAffected(); e == nil {
			rows = n // 仅写操作零成本拿到影响行数
		}
	}
	l.logQuery(ctx, query, start, rows, err)
	return res, err
}

func (l *loggingDBTX) QueryContext(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error) {
	start := time.Now()
	rows, err := l.inner.QueryContext(ctx, query, args...)
	l.logQuery(ctx, query, start, -1, err) // 查询类不数行数，避免消费业务方 rows
	return rows, err
}

func (l *loggingDBTX) QueryRowContext(ctx context.Context, query string, args ...interface{}) *sql.Row {
	start := time.Now()
	row := l.inner.QueryRowContext(ctx, query, args...)
	// QueryRow 的 error 延迟到 Scan 才暴露，这里无法取得；按正常路径记录（rows 不适用）。
	l.logQuery(ctx, query, start, -1, nil)
	return row
}

// PrepareContext 透传：预编译语句本身不产生查询日志，真正执行时由 *sql.Stmt 触发（不经本包装）。
func (l *loggingDBTX) PrepareContext(ctx context.Context, query string) (*sql.Stmt, error) {
	return l.inner.PrepareContext(ctx, query)
}
```

> 说明：`QueryRowContext` 的错误要 `Scan` 时才暴露，包装层拿不到，故按正常 Debug 记录；这是已知取舍，不在本任务消化。`PrepareContext` 走预编译路径，sqlc 生成代码极少用到，按透传处理并在测试中确认不额外记录。

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/store/ -run TestLoggingDBTX -v`
Expected: PASS

- [ ] **Step 5: store.go 接入（覆盖事务内外）**

修改 `internal/store/store.go`：

`New`（约 78 行）把 `sqlc.New(db)` 换成包装：

```go
func New(db *sql.DB) *Store {
	return &Store{db: db, Queries: sqlc.New(newLoggingDBTX(db, slowQueryThreshold))}
}
```

`WithTx`（约 97 行）把 `fn(s.Queries.WithTx(tx))` 换成对 tx 重新包装（绕过 sqlc 原生 WithTx，使事务内 SQL 也被记录）：

```go
	if err := fn(sqlc.New(newLoggingDBTX(tx, slowQueryThreshold))); err != nil {
```

> 注意：`sqlc.New` 形参是 `sqlc.DBTX` 接口，`*sql.DB` 与 `*sql.Tx` 均满足；`newLoggingDBTX` 返回的也是 `sqlc.DBTX`，类型自洽。改 `WithTx` 后原 `s.Queries.WithTx(tx)`（sqlc 自带）不再使用，但 `Queries` 字段仍保留供非事务查询。

- [ ] **Step 6: 补规范文档 SQL + 慢查询配置小节**

在 `docs/logging-conventions.md` 补：SQL 日志记什么（语句/耗时/写操作行数、不记参数值）、级别（Debug/慢查询 Warn/错误 Error），以及「配置」节加 `LOG_SLOW_QUERY_MS`（默认 200）。

- [ ] **Step 7: 运行 store 包测试 + 构建确认未回归**

Run: `go test ./internal/store/... && go build ./...`
Expected: PASS（既有 store 测试不受影响；logging wrapper 仅在 Debug 级别输出，默认 logger 不打扰测试）。

- [ ] **Step 8: 提交**

```bash
git add internal/store/dblog.go internal/store/dblog_test.go internal/store/store.go docs/logging-conventions.md
git commit -m "feat(store): 新增 SQL 日志,覆盖事务内外

包装 sqlc.DBTX 接口记录每条 SQL 的语句文本/耗时/写操作影响行数/错误,
log_type=sql;正常 Debug、慢查询(>LOG_SLOW_QUERY_MS,默认 200ms)Warn、
执行错误 Error;不记参数值避免 PII。store.New 与 WithTx 均接入包装器,
事务内 SQL 同样被记录;trace_id 经 ctx 串联。"
```

---

## Task 8: 散落日志全量重构（最后做，分批逐文件提交）

> **前置依赖**：Task 2（`Key*` 常量 + `Err`）、Task 3（规范文档）、Task 4（`log_type` 兜底注入）必须已合入。本任务是机械对齐，不引入行为变更；逐文件提交以便独立评审。**业务日志的 `log_type=app` 由 Task 4 的 handler 兜底注入，sweep 时不要逐条手加。**

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

## Task 9: 全量构建与真实环境验证

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
   - 出现 `"msg":"http_request"` 行，含 method/route（模板形式）/status/latency_ms/user_id/trace_id，`log_type=http`；
   - 出现 `"msg":"external_request"` 行，`log_type=newapi/ragflow`，含 endpoint（无 query）/status/latency_ms，且与触发它的请求 trace_id 一致（链路串联）；
   - 设 `LOG_LEVEL=debug` 后出现 `"msg":"sql_query"` 行，`log_type=sql`，含 sql（占位符形式、无参数值）/latency_ms，写操作带 rows；与触发它的请求 trace_id 一致；
   - 业务日志（service/worker 产生的）带 `log_type=app`（handler 兜底注入生效）；
   - 任意日志中均无明文 token/password/sk- key、无 SQL 参数值（脱敏 + 不记参数生效）。
5. 临时设 `LOG_LEVEL=debug` 重启，确认外部调用成功与正常 SQL 的 Debug 行出现；构造一条慢查询（或临时把 `LOG_SLOW_QUERY_MS` 调到极小值）确认 SQL 行抬到 Warn；设 `LOG_FORMAT=text` 确认本地可读文本格式生效；验证后恢复默认。

- [ ] **Step 4: 交付说明**

汇总：改动文件矩阵、各任务测试结果、真实环境验证证据（关键日志行摘录，注意先脱敏再贴）。若某步未能运行（如本地环境不可用），明确写明原因与风险。

---

## Self-Review 记录

- **Spec 覆盖**（2026-06-12 修订后）：模块 1（可配置化）→ Task 1✅；模块 4 常量/helper → Task 2✅，规范文档 → Task 3✅，`log_type` 常量+handler 兜底 → Task 4，全量 sweep → Task 8；模块 2（access log，含 log_type=http）→ Task 5；模块 3（外部调用，log_type=newapi/ragflow）→ Task 6；模块 5（SQL 日志）→ Task 7；交付验证 → Task 9。spec 全部章节有对应任务。
- **占位符扫描**：Task 8 因跨多文件无法逐行预写，已用「明确规则 + worked example + 逐文件提交」表达，非占位符；其余任务均含完整代码。
- **类型一致性**：`Config{Level, Format}`、`Key*` 常量（含 `KeyLogType`）、`LogType*` 取值常量、`Err`、`httplog.New(base, logType)`、`AccessLog()`、`newLoggingDBTX(inner, threshold)` 在定义与使用处命名一致；`log_type` 取代原 `service` 字段，已同步 attrs/transport/test；module path 统一标注「以实际 go.mod 为准」需实现者替换 `oc-manager`。
- **执行状态**：Task 1-3 已提交完成；Task 5（access log）代码已在工作区（待补 log_type=http 与全局挂载测试后提交）；Task 4/6/7/8/9 待执行。
