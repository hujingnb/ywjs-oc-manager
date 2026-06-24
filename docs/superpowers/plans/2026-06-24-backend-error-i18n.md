# 后端错误消息 i18n 实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 后端一切返回给客户端的文案随请求 locale（en/zh）输出，前端零改动（仍展示 `message`，已在发 `Accept-Language`）。

**Architecture:** locale 中间件解析 `Accept-Language` 入 gin 上下文；每条用户可见消息起显式 `MsgKey` 常量，中心 catalog 映射 `MsgKey → {zh,en}`；`apierror.JSON` 写出 helper 按上下文 locale 解析消息。`ErrorResponse{code,message}` 形状不变 → openapi/前端类型零改动。一致性守卫 + 「禁止裸中文」扫描兜底。

**Tech Stack:** Go（gin、testify）、现有 `internal/api/apierror` 与 `internal/api/middleware`、`internal/config.I18nConfig`。

**设计依据：** `docs/superpowers/specs/2026-06-24-backend-error-i18n-design.md`

---

## 前置事实（已核对真实代码）

- `internal/api/apierror/response.go`：`ErrorResponse{Code, Message string}` + `New(code, message)`。出口形态 `c.JSON(status, apierror.New(code, "中文"))`。
- `internal/api/handlers/request_errors.go`：`serviceErrorRule{target, statusCode, code, message, safe, validation}` + `mappedServiceErrorRules`（静态中文）+ `writeMappedServiceError(c, err, fallbackStatus, fallbackMessage)`。
  - `safe` 规则返回 `redactlog.SafeErrorMessage(err)`（运行期脱敏明细，**动态**）。
  - `validation` 规则返回 `validationServiceMessage(...)`（剥 sentinel 前缀后的**动态**业务原因）。
  - `bindErrorMessage` 产出动态中文模板（"缺少必填参数: <字段>" 等）。
- `internal/service/errors.go`：81 条 `errors.New("中文")` sentinel。
- `internal/api/` 内 `apierror.New(` 共 194 处（172 带中文）；handler 分布见下「domain 清单」。
- 中间件注册：`internal/api/router.go` 用 `router.Use(middleware.X)`（已有 RequestID/AccessLog/CSRF…）。
- `internal/config`：`I18nConfig.DefaultLocale`（缺省 en）；`internal/service` 有 `SupportedLocales = []string{"en","zh"}` 与 `isSupportedLocale`。
- 约 9 个 `internal/api/handlers/*_test.go` 断言中文 message，需更新。

## 范围与边界（重要）

- **静态用户可见文案**（绝大多数，~150-180 条）：全部接入 MsgKey catalog，本计划主体。
- **动态明细错误**（`safe` / `validation` 规则、`bindError` 模板）：返回运行期 CLI/service 生成的原因串。
  - `bindError` 的**模板**（"缺少必填参数: %s"）入 catalog 带占位符；字段名是数据、原样拼接。
  - `safe`/`validation` 规则**嵌入的动态明细**来自 service/hermes CLI，本计划**不**翻译其内嵌明细（那是 service 层文案，属后续工作）；但其**静态外壳/兜底**（如未命中规则的 `INTERNAL` 兜底文案）走 catalog。计划末尾的「禁止裸中文」扫描对这些动态串加白名单并注明。
- **范围外**：日志、注释、持久化数据（默认成员名 `"成员"`）、前端已自管 vue-i18n 文案。

## 文件结构

| 文件 | 动作 | 职责 |
|---|---|---|
| `internal/i18n/locale.go` | 新增 | `NormalizeLocale(raw, def)`、`SupportedLocales`、`ParseAcceptLanguage(header, def)` 公共归一（供中间件/auth 复用） |
| `internal/api/middleware/locale.go` | 新增 | `Locale(defaultLocale string) gin.HandlerFunc`：解析 Accept-Language → `apierror.SetLocale(c, loc)` |
| `internal/api/apierror/locale.go` | 新增 | `LocaleContextKey`、`SetLocale(c, loc)`、`LocaleFrom(c) string`（上下文契约归 apierror） |
| `internal/api/apierror/messages.go` | 新增 | `MsgKey`、`catalog`、`Register(entries)`、`Localize(key, loc, args...)` |
| `internal/api/apierror/messages_<domain>.go` | 新增（多） | 各 domain 的 MsgKey 常量 + `func init(){ Register(...) }` 注册 zh/en |
| `internal/api/apierror/response.go` | 改造 | 增 `JSON(c, status, code, key, args...)`；保留 `New` |
| `internal/api/apierror/messages_test.go` | 新增 | Localize / 一致性守卫 / 无裸中文扫描 |
| `internal/api/middleware/locale_test.go` | 新增 | Accept-Language 解析/归一/回落 |
| `internal/api/handlers/request_errors.go` | 改造 | 静态规则 → msgKey；bindError 模板 → msgKey+args；动态 safe/validation 保留 |
| `internal/api/handlers/*.go`（26 文件） | 改造 | 内联 `apierror.New(code,"中文")` → `apierror.JSON(c,status,code,MsgXxx)` |
| `internal/api/handlers/*_test.go`（~9 文件） | 改造 | 断言从中文串改为 code + `apierror.Localize(key, loc)` |
| `internal/api/router.go` | 改造 | `router.Use(middleware.Locale(cfg.I18n.DefaultLocale))` |

**命令**：`go test ./...`（或 `make test`）；`make openapi-check`（应保持干净，无 API 变化）。

## 转换规则（所有 domain 统一）

- **R1**：每条静态 `apierror.New(code, "中文")` → 起一个 `MsgKey`（`err.<domain>.<slug>`），catalog 加 `{zh: 原中文逐字, en: 新译}`，调用点改 `apierror.JSON(c, status, code, MsgKey)`。
- **R2**：同一中文在多处复用 → 同一个 MsgKey（dedup）。
- **R3**：动态消息（含 `+ 变量` / `fmt.Sprintf`）→ catalog 用占位符（`%s`），`apierror.JSON(..., args...)` 传值；字段名/ID 等数据原样。
- **R4**：zh 逐字取自现有硬编码，不改字；en 为忠实英译。
- **R5**：`code` 字符串字面量保持不变（接口契约）。
- **R6**：每个 domain 改完，该 domain 的 handler 测试同步把「断言中文 message」改为「断言 code + `apierror.Localize(key, "zh")`（或按 default locale）」。

### 三个真实样例

**样例 A — 静态内联（platform_skills.go:126）**
```go
// 改前
c.JSON(http.StatusForbidden, apierror.New("FORBIDDEN", "无权管理平台技能"))
// 改后
apierror.JSON(c, http.StatusForbidden, "FORBIDDEN", apierror.MsgPlatformSkillForbidden)
```
catalog（`messages_platform_skill.go`）：
```go
const MsgPlatformSkillForbidden MsgKey = "err.platform_skill.forbidden"
func init() { Register(map[MsgKey]map[string]string{
    MsgPlatformSkillForbidden: {"zh": "无权管理平台技能", "en": "You are not allowed to manage platform skills."},
})}
```

**样例 B — 中心规则（request_errors.go:49，静态）**
```go
// 改前
{target: service.ErrKanbanForbidden, statusCode: http.StatusForbidden, code: "KANBAN_FORBIDDEN", message: "无权访问该实例任务看板"},
// 改后：规则改用 msgKey 字段
{target: service.ErrKanbanForbidden, statusCode: http.StatusForbidden, code: "KANBAN_FORBIDDEN", msgKey: MsgKanbanForbidden},
```
`writeMappedServiceError` 命中静态规则时 `apierror.JSON(c, rule.statusCode, rule.code, rule.msgKey)`；`safe`/`validation` 分支维持现状（动态明细）。

**样例 C — 动态模板（request_errors.go:159 validationErrorMessage）**
```go
// 改前
return "缺少必填参数: " + strings.Join(missing, ", ")
// 改后（调用方有 c，用 JSON+args；或返回 (MsgKey,args) 由上层写）
apierror.JSON(c, http.StatusBadRequest, "BAD_REQUEST", apierror.MsgMissingRequiredFields, strings.Join(missing, ", "))
```
catalog：`MsgMissingRequiredFields: {"zh": "缺少必填参数: %s", "en": "Missing required parameters: %s"}`。

---

## Task 1：locale 归一 + 中间件（TDD）

**Files:**
- Create: `internal/i18n/locale.go`
- Create: `internal/i18n/locale_test.go`
- Create: `internal/api/middleware/locale.go`
- Create: `internal/api/apierror/locale.go`
- Create: `internal/api/middleware/locale_test.go`
- Modify: `internal/api/router.go`

- [ ] **Step 1: 写失败测试 `internal/i18n/locale_test.go`**
```go
package i18n

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNormalizeLocale(t *testing.T) {
	// 受支持原样返回；区域后缀剥离；未知/空回落 def
	assert.Equal(t, "zh", NormalizeLocale("zh", "en"))   // 直接受支持
	assert.Equal(t, "zh", NormalizeLocale("zh-CN", "en")) // 剥区域后缀
	assert.Equal(t, "en", NormalizeLocale("fr", "en"))    // 未知回落 def
	assert.Equal(t, "en", NormalizeLocale("", "en"))      // 空回落 def
}

func TestParseAcceptLanguage(t *testing.T) {
	// 取首选标签并归一；多值取第一个；带 q 值忽略
	assert.Equal(t, "zh", ParseAcceptLanguage("zh-CN,zh;q=0.9,en;q=0.8", "en"))
	assert.Equal(t, "en", ParseAcceptLanguage("en-US,en;q=0.9", "zh"))
	assert.Equal(t, "zh", ParseAcceptLanguage("", "zh")) // 缺失回落 def
}
```

- [ ] **Step 2: 跑测试确认失败**
Run: `go test ./internal/i18n/ -run TestNormalizeLocale -v`
Expected: 编译失败（包不存在）。

- [ ] **Step 3: 实现 `internal/i18n/locale.go`**
```go
// Package i18n 提供 manager 后端的 locale 归一公共逻辑，供中间件与服务层复用，
// 避免 Accept-Language 解析与受支持语言集合在多处分叉。
package i18n

import "strings"

// SupportedLocales 是后端受支持的界面语言集合；与前端 LocaleSwitcher、
// service.SupportedLocales 保持一致，新增语言时同步扩展。
var SupportedLocales = []string{"en", "zh"}

func isSupported(loc string) bool {
	for _, l := range SupportedLocales {
		if l == loc {
			return true
		}
	}
	return false
}

// NormalizeLocale 把任意语言串归一到受支持 locale：精确命中直接返回；否则剥掉
// 区域后缀（zh-CN→zh）再判；仍不支持则回落 def。
func NormalizeLocale(raw, def string) string {
	key := strings.ToLower(strings.TrimSpace(raw))
	if key == "" {
		return def
	}
	if isSupported(key) {
		return key
	}
	if base := strings.SplitN(key, "-", 2)[0]; isSupported(base) {
		return base
	}
	return def
}

// ParseAcceptLanguage 取 Accept-Language 首选标签并归一；空或无法解析回落 def。
func ParseAcceptLanguage(header, def string) string {
	if strings.TrimSpace(header) == "" {
		return def
	}
	first := strings.SplitN(header, ",", 2)[0]   // 取第一个语言项
	tag := strings.SplitN(first, ";", 2)[0]       // 去掉 q 值
	return NormalizeLocale(tag, def)
}
```

- [ ] **Step 4: 跑测试确认通过**
Run: `go test ./internal/i18n/ -v`
Expected: PASS。

- [ ] **Step 5: 实现 `internal/api/apierror/locale.go`（上下文契约）**
```go
package apierror

import "github.com/gin-gonic/gin"

// localeContextKey 是 locale 在 gin.Context 中的存取键；由 locale 中间件写入，
// apierror 写出错误时读取。契约放在 apierror 包，避免 middleware ↔ apierror 反向依赖。
const localeContextKey = "oc_locale"

// SetLocale 由 locale 中间件调用，把归一后的 locale 写入请求上下文。
func SetLocale(c *gin.Context, loc string) { c.Set(localeContextKey, loc) }

// LocaleFrom 读取请求 locale；缺失时回落 "en"（保证任何路径都有确定语言）。
func LocaleFrom(c *gin.Context) string {
	if c == nil {
		return "en"
	}
	if v, ok := c.Get(localeContextKey); ok {
		if s, ok := v.(string); ok && s != "" {
			return s
		}
	}
	return "en"
}
```

- [ ] **Step 6: 写中间件失败测试 `internal/api/middleware/locale_test.go`**
```go
package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"

	"oc-manager/internal/api/apierror"
)

func TestLocaleMiddlewareSetsLocale(t *testing.T) {
	gin.SetMode(gin.TestMode)
	// 场景：Accept-Language=zh-CN → 上下文 locale=zh
	r := gin.New()
	r.Use(Locale("en"))
	var got string
	r.GET("/x", func(c *gin.Context) { got = apierror.LocaleFrom(c); c.Status(200) })
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9")
	r.ServeHTTP(httptest.NewRecorder(), req)
	assert.Equal(t, "zh", got)
}

func TestLocaleMiddlewareFallsBackToDefault(t *testing.T) {
	gin.SetMode(gin.TestMode)
	// 场景：无 Accept-Language → 回落传入 default
	r := gin.New()
	r.Use(Locale("en"))
	var got string
	r.GET("/x", func(c *gin.Context) { got = apierror.LocaleFrom(c); c.Status(200) })
	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/x", nil))
	assert.Equal(t, "en", got)
}
```

- [ ] **Step 7: 跑测试确认失败**
Run: `go test ./internal/api/middleware/ -run TestLocaleMiddleware -v`
Expected: 编译失败（`Locale` 未定义）。

- [ ] **Step 8: 实现 `internal/api/middleware/locale.go`**
```go
package middleware

import (
	"github.com/gin-gonic/gin"

	"oc-manager/internal/api/apierror"
	"oc-manager/internal/i18n"
)

// Locale 解析请求 Accept-Language，归一到受支持 locale（回落 defaultLocale），
// 写入请求上下文供 apierror 写出错误时选择语言。
func Locale(defaultLocale string) gin.HandlerFunc {
	def := i18n.NormalizeLocale(defaultLocale, "en")
	return func(c *gin.Context) {
		apierror.SetLocale(c, i18n.ParseAcceptLanguage(c.GetHeader("Accept-Language"), def))
		c.Next()
	}
}
```

- [ ] **Step 9: 跑中间件测试确认通过**
Run: `go test ./internal/api/middleware/ -run TestLocaleMiddleware -v`
Expected: PASS（2 项）。

- [ ] **Step 10: 在 router.go 注册中间件**
在 `internal/api/router.go` 的全局中间件链里、`router.Use(middleware.RequestID())` 之后加一行（locale 与认证无关，尽早设置即可）：
```go
	router.Use(middleware.Locale(cfg.I18n.DefaultLocale))
```
注意：确认该函数能访问到 config（`cfg`/`deps` 里的 I18n.DefaultLocale）；若 router 构造未持有 config，按现有 deps 传参风格补一个 `DefaultLocale string` 依赖字段。Run: `grep -n 'func.*Router\|deps\|cfg' internal/api/router.go | head` 确认拿法。

- [ ] **Step 11: 跑包测试 + 提交**
Run: `go test ./internal/i18n/ ./internal/api/middleware/ ./internal/api/apierror/`
Expected: PASS。
```bash
git add internal/i18n/ internal/api/middleware/locale.go internal/api/middleware/locale_test.go \
        internal/api/apierror/locale.go internal/api/router.go
git commit -m "feat(api): 增加 locale 中间件与上下文契约

新增 internal/i18n 公共归一(NormalizeLocale/ParseAcceptLanguage)、locale 中间件
解析 Accept-Language 入 gin 上下文、apierror 的 locale 上下文存取契约。router 注册。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 2：apierror 消息 catalog 核心（TDD）

**Files:**
- Create: `internal/api/apierror/messages.go`
- Create: `internal/api/apierror/messages_test.go`

- [ ] **Step 1: 写失败测试 `internal/api/apierror/messages_test.go`**
```go
package apierror

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLocalizeReturnsLocaleMessage(t *testing.T) {
	// 用临时注册的 key 测解析：zh/en 各取对应；缺该语言回落 en；缺 key 回落 key 本身
	Register(map[MsgKey]map[string]string{
		"err.test.sample": {"zh": "测试", "en": "Test"},
	})
	assert.Equal(t, "测试", Localize("err.test.sample", "zh"))
	assert.Equal(t, "Test", Localize("err.test.sample", "en"))
	assert.Equal(t, "Test", Localize("err.test.sample", "fr")) // 未知语言回落 en
	assert.Equal(t, "err.test.missing", Localize("err.test.missing", "zh")) // 缺 key 回落 key
}

func TestLocalizeFormatsArgs(t *testing.T) {
	// 带占位符的动态消息用 args 格式化
	Register(map[MsgKey]map[string]string{
		"err.test.fields": {"zh": "缺少必填参数: %s", "en": "Missing required parameters: %s"},
	})
	assert.Equal(t, "缺少必填参数: a, b", Localize("err.test.fields", "zh", "a, b"))
	assert.Equal(t, "Missing required parameters: a, b", Localize("err.test.fields", "en", "a, b"))
}

func TestCatalogEveryEntryHasBothLangs(t *testing.T) {
	// 守卫：catalog 每条都含 zh+en 且非空（随 domain 填充持续生效）
	for key, langs := range catalog {
		require.NotEmpty(t, langs["zh"], "key %s 缺 zh", key)
		require.NotEmpty(t, langs["en"], "key %s 缺 en", key)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**
Run: `go test ./internal/api/apierror/ -run TestLocalize -v`
Expected: 编译失败（`MsgKey`/`Register`/`Localize`/`catalog` 未定义）。

- [ ] **Step 3: 实现 `internal/api/apierror/messages.go`**
```go
package apierror

import "fmt"

// MsgKey 是用户可见错误文案的稳定标识（err.<domain>.<slug>），catalog 据此存中/英译文。
type MsgKey string

// catalog 是 MsgKey → locale → 文案 的中心表；由各 domain 文件 init() 调 Register 填充。
var catalog = map[MsgKey]map[string]string{}

// Register 把一组译文并入 catalog；同 key 重复注册即 panic（编译期布局错误，尽早暴露）。
func Register(entries map[MsgKey]map[string]string) {
	for key, langs := range entries {
		if _, dup := catalog[key]; dup {
			panic("apierror: 重复注册 MsgKey " + string(key))
		}
		catalog[key] = langs
	}
}

// Localize 把 key 按 loc 解析为文案：缺该语言回落 en，再缺回落 key 本身（永不 panic）；
// 有 args 时按 fmt.Sprintf 格式化（catalog 串里用 %s/%d 占位符）。
func Localize(key MsgKey, loc string, args ...any) string {
	langs := catalog[key]
	msg := ""
	if langs != nil {
		if m, ok := langs[loc]; ok {
			msg = m
		} else {
			msg = langs["en"]
		}
	}
	if msg == "" {
		msg = string(key)
	}
	if len(args) > 0 {
		return fmt.Sprintf(msg, args...)
	}
	return msg
}
```

- [ ] **Step 4: 跑测试确认通过**
Run: `go test ./internal/api/apierror/ -v`
Expected: PASS。

- [ ] **Step 5: 提交**
```bash
git add internal/api/apierror/messages.go internal/api/apierror/messages_test.go
git commit -m "feat(api): 增加错误消息 catalog 与 Localize 解析

MsgKey/catalog/Register/Localize：中心译文表 + 按 locale 解析(缺语言回落 en、
缺 key 回落 key、支持占位符格式化)，附单元测试与「每条含 zh+en」守卫。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 3：apierror.JSON 写出 helper（TDD）

**Files:**
- Modify: `internal/api/apierror/response.go`
- Modify: `internal/api/apierror/messages_test.go`（追加）

- [ ] **Step 1: 追加失败测试**
```go
func TestJSONWritesLocalizedBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	Register(map[MsgKey]map[string]string{
		"err.test.json": {"zh": "无权", "en": "Forbidden"},
	})
	// en 上下文 → message 取 en；code 原样
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	SetLocale(c, "en")
	JSON(c, http.StatusForbidden, "FORBIDDEN", "err.test.json")
	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.JSONEq(t, `{"code":"FORBIDDEN","message":"Forbidden"}`, w.Body.String())
}
```
（test 文件顶部相应补 import：`net/http`、`net/http/httptest`、`github.com/gin-gonic/gin`。）

- [ ] **Step 2: 跑测试确认失败**
Run: `go test ./internal/api/apierror/ -run TestJSONWritesLocalizedBody -v`
Expected: 失败（`JSON` 未定义）。

- [ ] **Step 3: 在 response.go 增 JSON helper**
```go
import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// JSON 按请求 locale 解析 key→message，写回 {code, message}。新代码统一用本函数，
// 取代 c.JSON(status, New(code, "中文"))。args 用于动态占位符消息。
func JSON(c *gin.Context, status int, code string, key MsgKey, args ...any) {
	c.JSON(status, ErrorResponse{Code: code, Message: Localize(key, LocaleFrom(c), args...)})
}

// 保留 New：仅用于确不需翻译的极少数处或过渡，新代码不应再传裸中文。
```
（确认 response.go 顶部 import 加 `net/http` 仅在用到时；这里 status 由调用方传，不需要 net/http，按需省略。`gin` 必需。）

- [ ] **Step 4: 跑测试确认通过**
Run: `go test ./internal/api/apierror/ -v`
Expected: PASS。

- [ ] **Step 5: 提交**
```bash
git add internal/api/apierror/response.go internal/api/apierror/messages_test.go
git commit -m "feat(api): 增加 apierror.JSON 按 locale 写出本地化错误

JSON(c,status,code,key,args...) 读上下文 locale 解析 message,写回 {code,message}。
ErrorResponse 形状不变。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 4：中心 request_errors.go 改造（静态规则→msgKey + 动态边界）

**Files:**
- Modify: `internal/api/handlers/request_errors.go`
- Create: `internal/api/apierror/messages_common.go`
- Modify: 相关 handler 测试（如 `request_errors` 有测试则同步）

- [ ] **Step 1: 建 common catalog `internal/api/apierror/messages_common.go`**
把 `mappedServiceErrorRules` 里的**静态** message 与跨模块通用文案迁成 MsgKey（zh 逐字、en 新译）。覆盖：`MsgNotFound`(资源不存在)、`MsgKanbanForbidden`、`MsgKanbanRuntimeUnavailable`(实例容器未运行，请先在运行时 tab 启动)、`MsgKanbanNotSupported`、`MsgKanbanOutputInvalid`、`MsgCronForbidden`、`MsgCronRuntimeUnavailable`(复用 RuntimeUnavailable 同文案→同 key)、`MsgCronNotSupported`、`MsgCronBadRequest`、`MsgCronOutputInvalid`、`MsgConversationForbidden`、`MsgConversationNotSupported`、`MsgConversationOutputInvalid`、`MsgInternal`(服务器内部错误)、以及 bindError 模板键 `MsgBadRequestGeneric`/`MsgEmptyBody`/`MsgInvalidJSON`/`MsgInvalidType`/`MsgMissingRequiredFields`/`MsgValidationFailed`。
> 注意 `实例容器未运行，请先在运行时 tab 启动` 在 kanban/cron/conversation 三处文案相同 → 复用同一个 `MsgRuntimeNotAvailable` key（R2 dedup）。

- [ ] **Step 2: 给 serviceErrorRule 增 msgKey 字段，静态规则改用 msgKey**
```go
type serviceErrorRule struct {
	target     error
	statusCode int
	code       string
	msgKey     apierror.MsgKey // 静态文案走 catalog
	safe       bool            // 返回脱敏动态明细(不入表)
	validation bool            // 返回剥前缀的动态业务原因(不入表)
}
```
把 `mappedServiceErrorRules` 的每条静态 `message: "中文"` 改为 `msgKey: apierror.MsgXxx`；`safe`/`validation` 规则不加 msgKey（仍走动态分支）。

- [ ] **Step 3: 改 writeMappedServiceError 用 locale**
```go
func writeMappedServiceError(c *gin.Context, err error, fallbackStatus int, fallbackKey apierror.MsgKey) {
	for _, rule := range mappedServiceErrorRules {
		if !errors.Is(err, rule.target) {
			continue
		}
		switch {
		case rule.safe:
			c.JSON(rule.statusCode, apierror.New(rule.code, redactlog.SafeErrorMessage(err))) // 动态明细，保留原样
		case rule.validation:
			c.JSON(rule.statusCode, apierror.New(rule.code, validationServiceMessage(err, rule.target))) // 动态明细
		default:
			apierror.JSON(c, rule.statusCode, rule.code, rule.msgKey) // 静态走 catalog
		}
		return
	}
	apierror.JSON(c, fallbackStatus, "INTERNAL", fallbackKey)
}
```
> **重要（保持编译绿）**：`writeMappedServiceError` 的签名从 `fallbackMessage string` 改为 `fallbackKey apierror.MsgKey`——会让所有调用方编译失败。**本 task 内**必须 `grep -rn 'writeMappedServiceError' internal/api/handlers/` 列出全部调用点，**机械地把每处兜底中文串统一替换为 `apierror.MsgInternal`**（该参数仅在 sentinel 未命中任何规则时使用，是泛化兜底，统一 MsgInternal 语义可接受），使整包一次编译通过。后续 domain task 如需该接口专属兜底文案再细化为对应 MsgKey。**不要**把这些调用点更新拆到 domain task，否则 T4 提交即编译失败。

- [ ] **Step 4: bindError 系列改 msgKey + args**
把 `writeBindError`/`bindErrorMessage`/`validationErrorMessage` 改为基于 locale：`writeBindError(c, err)` 内部按 err 类型选 MsgKey（`MsgEmptyBody`/`MsgInvalidJSON`/`MsgInvalidType`(带 %s 字段)/`MsgMissingRequiredFields`(带 %s)/`MsgValidationFailed`(带 %s)/`MsgBadRequestGeneric`），用 `apierror.JSON(c, 400, "BAD_REQUEST", key, fieldArgs...)`。字段名拼接逻辑(jsonFieldName 等)保留。

- [ ] **Step 5: 跑测试**
Run: `go test ./internal/api/... -run 'Error|Bind|Mapped'`
Expected: 编译通过；现有相关测试若断言中文需在本任务一并改为 `apierror.Localize(key,"zh")` 或 code 断言。补一致性：`go test ./internal/api/apierror/`（守卫仍绿）。

- [ ] **Step 6: 提交**
```bash
git add internal/api/handlers/request_errors.go internal/api/apierror/messages_common.go internal/api/handlers/*_test.go
git commit -m "refactor(api): request_errors 静态规则接入 msgKey,动态明细保留

serviceErrorRule 增 msgKey;静态映射与 bindError 模板走 catalog 按 locale 输出;
safe/validation 的运行期动态明细维持原样(属后续 service 层 i18n)。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 5–N：按 domain 迁移内联 apierror.New（统一做法）

> **每个 domain 文件一个 task，按以下 6 步执行（与样例 A 同型）：**
> 1. 新建 `internal/api/apierror/messages_<domain>.go`：为该文件每条 `apierror.New(code,"中文")` 起 `MsgKey`（`err.<domain>.<slug>`）+ `func init(){ Register(...) }`（zh 逐字、en 忠实英译；复用已有同义 key 则不新建）。
> 2. 把该 handler 里 `c.JSON(status, apierror.New(code,"中文"))` → `apierror.JSON(c, status, code, apierror.MsgXxx)`；动态串用 args。
> 3. `writeMappedServiceError(...)` 调用点的兜底参数改传 MsgKey。
> 4. 跑 `go test ./internal/api/apierror/`（守卫绿）。
> 5. 更新该 domain 的 `*_test.go`：断言中文 message → 断言 code + `apierror.Localize(key, "zh")`。
> 6. `go build ./... && go test ./internal/api/...` 该包绿，提交（message：`feat(api): <domain> 错误文案接入 msgKey catalog`）。

domain 清单（按现有中文 apierror.New 数量，逐文件一 task；括号为条数）：

- [ ] **Task 5** `industry_knowledge.go`(20) → `messages_industry_knowledge.go`
- [ ] **Task 6** `assistant_versions.go`(13) → `messages_assistant_version.go`
- [ ] **Task 7** `knowledge.go`(12) + `knowledge_multipart.go`(5) + `runtime_knowledge.go`(4) → `messages_knowledge.go`
- [ ] **Task 8** `skill_tickets.go`(11) → `messages_skill_ticket.go`
- [ ] **Task 9** `app_skills.go`(11) → `messages_app_skill.go`
- [ ] **Task 10** `custom_skills.go`(10) → `messages_custom_skill.go`
- [ ] **Task 11** `platform_skills.go`(8) + `skill_market.go`(5) → `messages_platform_skill.go`
- [ ] **Task 12** `jobs.go`(8) → `messages_job.go`
- [ ] **Task 13** `auth.go`(8) → `messages_auth.go`
- [ ] **Task 14** `apps.go`(8) + `app_runtime.go`(4) → `messages_app.go`
- [ ] **Task 15** `workspace.go`(6) → `messages_workspace.go`
- [ ] **Task 16** `usage.go`(5) + `recharge.go`(4) → `messages_usage.go`
- [ ] **Task 17** `members.go`(5) + `organizations.go`(3) → `messages_member.go`
- [ ] **Task 18** `bootstrap.go`(5) → `messages_bootstrap.go`
- [ ] **Task 19** `hermes_conversation.go`(4) + `hermes_kanban.go`(1) → `messages_conversation.go`
- [ ] **Task 20** `channels.go`(4) → `messages_channel.go`
- [ ] **Task 21** `audit.go`(4) + `platform_overview.go`(2) + `models.go`(2) → `messages_misc.go`

> 每个 task 完成后该 domain 的 handler 测试同步更新。若某文件无对应 `*_test.go` 则跳过 step 5。

---

## Task 22：非错误客户可见串 + 500 兜底审计

**Files:**
- Modify: `internal/service/ragflow_parse_status_refresher.go`（解析失败原因）
- Modify: 残余 `apierror.New("INTERNAL", "中文")` 兜底点

- [ ] **Step 1:** `grep -rnP 'apierror\.New\([^)]*[\x{4e00}-\x{9fff}]' internal/api/ | grep -v _test` 应只剩动态 `safe/validation/bindError` 路径（已是 args 形态）与白名单；其余一律已转 msgKey。逐条核对剩余项。
- [ ] **Step 2:** `ragflow_parse_status_refresher.go:226` 的 `"RAGFlow 解析失败（未返回具体原因）"`：该串经响应展示给用户的话，接入 catalog（`MsgRagflowParseFailedNoReason`），调用点改为按 locale 取（若该处无 gin.Context，则在其 HTTP 出口处本地化，或保留并在白名单注明属解析状态数据）。按实际调用链决定，注释说明取舍。
- [ ] **Step 3:** 跑 `go test ./...`；提交。

---

## Task 23：启用「禁止裸中文」扫描守卫（最终闸门）

**Files:**
- Create: `internal/api/apierror/no_hardcoded_cn_test.go`

- [ ] **Step 1: 写扫描测试**
```go
package apierror_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// 扫描 internal/api 下非测试 .go：apierror.New 的第二实参不应再是中文字面量(应走 msgKey)。
// 白名单：动态明细路径(safe/validation 经 redactlog/validationServiceMessage,非字面量)天然不命中。
func TestNoHardcodedChineseInApiErrors(t *testing.T) {
	re := regexp.MustCompile(`apierror\.New\([^)]*[\x{4e00}-\x{9fff}]`)
	root := "../../.." // 调整到仓库根下 internal/api
	var offenders []string
	_ = filepath.Walk(filepath.Join(root, "internal/api"), func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		b, _ := os.ReadFile(p)
		if re.Match(b) {
			offenders = append(offenders, p)
		}
		return nil
	})
	assert.Empty(t, offenders, "这些文件仍有裸中文 apierror.New，应改走 msgKey: %v", offenders)
}
```
（路径前缀按实际包位置调整，确保能定位仓库根 `internal/api`。）

- [ ] **Step 2: 跑测试**
Run: `go test ./internal/api/apierror/ -run TestNoHardcodedChinese -v`
Expected: PASS（前序 task 已清空裸中文）。**若失败**，按报告文件补迁漏掉的。

- [ ] **Step 3: 提交**
```bash
git add internal/api/apierror/no_hardcoded_cn_test.go
git commit -m "test(api): 增加禁止裸中文 apierror.New 的扫描守卫

确保所有用户可见错误文案都走 msgKey catalog,防止后续回潮。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 24：全量校验 + openapi 不变

- [ ] **Step 1:** `go test ./...` 全绿（重点 internal/api/...、internal/i18n）。
- [ ] **Step 2:** `make openapi-check`：应保持干净（ErrorResponse 形状未变，无 API 契约变化）。若有 diff，说明误改了响应结构，排查。
- [ ] **Step 3:** `go vet ./...` 干净。

---

## Task 25：真实浏览器中英双语验证（CLAUDE.md 强制）

- [ ] **Step 1:** 本地 k3d 部署改后 manager-api（`make local-build` 推镜像 + rollout）。
- [ ] **Step 2:** 浏览器登录 manager（http://ocm.localhost）。**English 用户**（顶栏切 English / 或 Accept-Language=en）触发各类错误：403（越权）、404（不存在）、409（冲突）、400（缺参/非法 JSON）、各业务错误（如重复名、配额满）、500，确认 naive-ui message 与页面错误位**显示英文**；Network 面板核对响应 `message` 字段为英文。
- [ ] **Step 3:** 切中文，触发同样错误，确认**显示中文**；核对 `Accept-Language: zh` 时 `message` 为中文。
- [ ] **Step 4:** 三角色（platform_admin / org_admin / org_member）各覆盖关键越权错误（参考 memory 验证矩阵要求），产出逐场景 × 双语言矩阵存 `docs/reports/`。
- [ ] **Step 5:** 发现任何仍是中文/未翻/混乱的错误，回对应 domain task 补 catalog/调用点，重新验证，直到全部正确。

---

## 完成判据

- `go test ./...` 全绿（含 locale 中间件、Localize、catalog 一致性守卫、禁止裸中文扫描）。
- `make openapi-check` 干净（无 API 契约变化）；前端与 `generated.ts` 零改动。
- 真机：English 用户全类别错误显示英文、中文用户显示中文；三角色矩阵归档。
- 各 domain 迁移、基础设施、守卫按业务边界分开提交。

## 风险与回退

- **改动面大**（194 站点 + 81 sentinel + ~9 测试文件）：靠 msgKey 编译检查 + catalog 一致性守卫 + 禁止裸中文扫描 + 全量测试兜底；逐 domain 提交，单 domain 出错可独立 revert。
- **动态明细未译**（safe/validation 内嵌 CLI/service 原因）：本计划明确不译，扫描守卫天然不命中（非字面量）；真机验证时若发现这类英文/中文混杂明细影响体验，作为 service 层 i18n 后续单独立项。
- **locale 与前端漂移**：复用 config.i18n.default_locale 与统一 SupportedLocales，避免分叉。
- **handler 测试断言**：从中文串改 code/Localize，工作量计入各 domain task。
