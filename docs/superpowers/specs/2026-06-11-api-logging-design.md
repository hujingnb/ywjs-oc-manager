# 整理 oc-manager API 日志 — 设计文档

- 日期：2026-06-11
- 状态：已通过 brainstorm 评审，待写实现计划
- 范围：manager-api 的日志体系整理，分 4 个模块

## 背景与现状

项目已有一套不错的日志基础设施，但围绕「API 日志」存在缺口：

- 用 `log/slog` 输出结构化 JSON，全局脱敏（`internal/log/redact.go`），trace_id 自动注入（`internal/log/slog.go` 的 `requestIDHandler`）。
- **缺 HTTP access log 中间件**：`internal/api/router.go` 只挂了 Recovery / CORS / RequestID / CSRF / Auth，单个请求的 method/path/status/耗时/user_id 没有统一落地，排故只能靠零散业务日志。
- **外部调用日志散落**：new-api（`internal/integrations/newapi/client.go`）、RAGFlow（`internal/integrations/ragflow/client.go`，经共享 `internal/integrations/httpclient`）没有出站请求日志，只在 service 层失败时零星记 Warn。
- **日志级别硬编码**：`NewSlogLogger` 固定 `slog.LevelInfo` + JSON handler，生产无法临时调 debug，本地调试读 JSON 费劲。
- **散落业务日志不统一**：各 service / worker 手写 `slog.WarnContext` / `slog.ErrorContext`，字段名、级别、措辞不一致。

## 目标

1. 补一个统一的 HTTP access log 中间件（仅 stdout JSON）。
2. 统一外部调用（new-api / RAGFlow）日志，成功失败都记元数据。
3. 让日志级别与输出格式可通过 env 配置。
4. 制定日志规范并全量重构现有散落日志。

## 非目标

- access log **不落库**、不做后台查询页（依赖 k8s 日志采集消费 stdout JSON）。
- 不引入新的日志库（继续用 `slog`）。
- 不记录外部调用的请求/响应 body（避免泄密与噪音）。
- 不动 `audit_logs` 业务审计表（与本次「运行日志」是两个体系，本次不扩展）。
- 不加 access log 开关 / 采样旋钮（保持简单）。

## 总体取舍

- access log 只打 stdout JSON，靠 slog 既有 handler 自动带 trace_id；不动数据库。
- 外部调用日志走 **自定义 `http.RoundTripper`** 在基础设施层统一拦截，沿用项目「脱敏 writer 在底层统一拦截」的既有风格，client 业务方法零改动，未来新增调用天然覆盖。
- 「全量重构散落日志」改动面最大、与 `CLAUDE.md` 的 surgical-change 原则有张力：**必须等模块 4 的规范常量 + helper 落地后再做，按目录分批、逐文件分提交**，保证每个提交可独立评审、不夹带行为变更。

---

## 模块 1 — 日志基础设施可配置化

**位置**：`internal/log/slog.go`、`cmd/server/main.go`

**改动**：

- `NewSlogLogger` 签名由 `NewSlogLogger(out io.Writer)` 改为接收配置：
  ```go
  type Config struct {
      Level  slog.Level // 由 LOG_LEVEL 解析，默认 Info
      Format string     // "json"（默认）| "text"
  }
  func NewSlogLogger(out io.Writer, cfg Config) *slog.Logger
  ```
- Format 为 `"text"` 时用 `slog.NewTextHandler`，否则 `slog.NewJSONHandler`。两个 handler 都仍包在 `NewRedactingWriter(out)` + `requestIDHandler` 内（脱敏、trace_id 注入、`AddSource=true` 不受格式影响）。
- env 解析放 `internal/log`（如 `ParseConfigFromEnv()`），读取：
  - `LOG_LEVEL`：`debug` / `info` / `warn` / `error`，大小写不敏感，非法值 fallback Info 并记一条告警。
  - `LOG_FORMAT`：`json` / `text`，非法值 fallback json。
- `cmd/server/main.go:91` 处由 `NewSlogLogger(logOut)` 改为 `NewSlogLogger(logOut, managerlog.ParseConfigFromEnv())`。

**测试**：表驱动覆盖 level/format 解析（含非法值 fallback）、debug 级别记录在 Info 配置下被过滤、text 与 json 两种格式下 trace_id 与脱敏仍生效。

---

## 模块 2 — HTTP access log 中间件

**位置**：新增 `internal/api/middleware/access_log.go`；挂载点 `internal/api/router.go`

**行为**：

- Gin 中间件，挂在 `RequestID()` 之后、`RequireUserAuth()` 之前（这样未鉴权导致的 4xx 也能记到，且能拿到 trace_id）。
- `c.Next()` 后用 `slog.LogAttrs(c.Request.Context(), level, "http_request", attrs...)` 记录字段：
  - `method`：`c.Request.Method`
  - `route`：`c.FullPath()`（路由模板，如 `/api/v1/organizations/:id`，避免把真实 ID 打进去导致日志基数爆炸）；空模板（404 未匹配）回退原始 path。
  - `status`：`c.Writer.Status()`
  - `latency_ms`：请求处理耗时（毫秒）
  - `client_ip`：`c.ClientIP()`
  - `user_id`：`c.Next()` 后从 `c.Request.Context()` 经 `auth.PrincipalFromContext` 取，未鉴权为空（omit 或空串）
  - `bytes`：`c.Writer.Size()`
  - trace_id 由现有 `requestIDHandler` 自动注入，不在中间件里手填。
- 级别分流：`status >= 500` → Error；`400 <= status < 500` → Warn；其余 → Info。
- 硬编码跳过纯噪音路径（不记 access log）：健康检查路由（`RegisterHealthRoutes` 注册的路径，如 `/healthz` / `/readyz`，以代码实际为准）。清单在实现时确认并在注释中列明。

**测试**：用 `httptest` + 一个挂了该中间件的最小 gin engine，配合捕获型 `slog.Handler` 断言：2xx→Info、4xx→Warn、5xx→Error；`route` 用模板而非真实 ID；鉴权后带 `user_id`、未鉴权不带；健康检查路径被跳过。

---

## 模块 3 — 外部调用日志（RoundTripper）

**位置**：新增 `internal/integrations/httplog`（如 `httplog/transport.go`）；接入点 `internal/integrations/newapi/client.go`、`internal/integrations/ragflow/client.go` 的构造函数。

**行为**：

- 提供一个包装 `http.RoundTripper` 的 logging transport：
  ```go
  func New(base http.RoundTripper, service string) http.RoundTripper
  ```
  `base` 为 nil 时退回 `http.DefaultTransport`。
- `RoundTrip` 计时执行内层请求，记录字段：
  - `service`：`newapi` / `ragflow`（构造时传入的标签）
  - `method`、`endpoint`（`req.URL.Path`，**不含 query**，避免敏感参数泄露）
  - `status`（transport error 时省略，记 `error`）
  - `latency_ms`
- 级别：2xx → Debug；非 2xx 或 transport error → Warn。**不记 body**。
- 日志用 `req.Context()` 作为 ctx，使外部调用日志也自动带上发起该调用的请求 trace_id（实现链路串联）。
- 接入：在各 integration 构造函数内把 `http.Client.Transport`（newapi 的 `Client.HTTPClient` 与 ragflow 经由的 `httpclient.BaseHTTPClient.HTTPClient`）设置为 `httplog.New(http.DefaultTransport, "<service>")`。`cmd/server/main.go` 的 wiring 不需要改动；service 标签随构造函数固定。

**测试**：用 stub `http.RoundTripper` 作为 base，断言 2xx→Debug、4xx/5xx→Warn、transport error→Warn 且带 `error` 不带 `status`；`endpoint` 不含 query；`service` 标签正确。

---

## 模块 4 — 日志规范 + 全量重构散落日志

**位置**：新增 `internal/log/attrs.go`（规范常量 + helper）；规范文档；随后分批改 `internal/service/*`、`internal/worker/handlers/*`。

**规范常量与 helper**：

- 统一 attr key 常量，避免各处字符串字面量漂移：
  - `trace_id` / `org_id` / `actor_id` / `actor_role` / `target_type` / `target_id` / `action` / `result` / `error` / `service` / `endpoint` / `status` / `latency_ms`
- 级别使用原则（写进规范文档与常量注释）：
  - **Debug**：正常流程的细粒度追踪（如外部调用成功）。
  - **Info**：正常业务里程碑（如 access log、任务完成）。
  - **Warn**：可恢复 / 不阻塞主流程的异常（如外部清理失败但业务已成功、可重试错误）。
  - **Error**：不可恢复或导致数据不一致、需要人工介入的错误。
- 提供轻量 helper（如 `Err(error) slog.Attr` 统一错误字段名为 `error`），避免重复 `slog.String("error", err.Error())`。

**规范文档**：写一份简短的日志规范（放 `docs/` 下或作为 `AGENTS.md` 的「日志」小节，实现时定位），覆盖：字段命名常量、级别原则、「外部调用走 RoundTripper 不在 service 层重复记」「不打 body / 不打敏感字段」等约束。

**全量重构**（**前置依赖：上述常量与 helper 必须先合入**）：

- 按目录分批扫 `internal/service`、`internal/worker/handlers` 的现有 `slog.*Context` 调用，对齐到：统一字段常量、统一 `error` 字段、按级别原则修正用错的级别、去掉与 RoundTripper / access log 重复的记录、统一中文措辞。
- **逐文件分提交**，每个提交只做日志对齐、不夹带行为变更，commit message 说明扫描范围；保证可独立评审。
- 不为重构强加新单测（无新行为）；若某处级别修正涉及业务判断，相应补/调测试。

---

## 实现顺序

1. 模块 1（可配置化）— 独立、无依赖，先落。
2. 模块 4 的「规范常量 + helper + 规范文档」— 作为后续模块的词汇表，先于 sweep 落。
3. 模块 2（access log 中间件）、模块 3（外部调用 RoundTripper）— 复用模块 4 的常量，可并行。
4. 模块 4 的「全量重构 sweep」— **最后做**，按目录分批、逐文件分提交。

## 风险与缓解

- **全量重构面大、易引噪音**：靠「常量 helper 先行 + 逐文件分提交 + 不夹带行为变更」约束；review 时每个提交聚焦单一目的。
- **route 基数 / PII**：access log 用 `c.FullPath()` 路由模板而非真实路径，外部调用只记 path 不记 query，从源头避免 ID/敏感参数进日志。
- **重复日志**：外部调用统一由 RoundTripper 记，service 层不再重复记成功/请求细节；access log 统一记 HTTP 层，handler 不再各记一遍。

## 交付验证

- 单元测试：模块 1/2/3 按各自「测试」小节覆盖。
- 真实环境验证（按 `CLAUDE.md` 要求，浏览器 + 实际请求）：本地 k3d 起 manager，发起若干请求 + 触发一次 new-api/RAGFlow 调用，确认 stdout 出现 `http_request` 与外部调用日志、字段齐全、trace_id 串联、敏感字段已脱敏；切 `LOG_LEVEL=debug` / `LOG_FORMAT=text` 确认配置生效。
