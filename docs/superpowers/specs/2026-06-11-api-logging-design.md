# 整理 oc-manager API 日志 — 设计文档

- 日期：2026-06-11
- 状态：已通过 brainstorm 评审，待写实现计划
- 范围：manager-api 的日志体系整理，分 5 个模块
- 修订：2026-06-12 增补「SQL 日志」模块与统一 `log_type` 字段（见模块 5 与各模块的 log_type 说明）

## 背景与现状

项目已有一套不错的日志基础设施，但围绕「API 日志」存在缺口：

- 用 `log/slog` 输出结构化 JSON，全局脱敏（`internal/log/redact.go`），trace_id 自动注入（`internal/log/slog.go` 的 `requestIDHandler`）。
- **缺 HTTP access log 中间件**：`internal/api/router.go` 只挂了 Recovery / CORS / RequestID / CSRF / Auth，单个请求的 method/path/status/耗时/user_id 没有统一落地，排故只能靠零散业务日志。
- **外部调用日志散落**：new-api（`internal/integrations/newapi/client.go`）、RAGFlow（`internal/integrations/ragflow/client.go`，经共享 `internal/integrations/httpclient`）没有出站请求日志，只在 service 层失败时零星记 Warn。
- **日志级别硬编码**：`NewSlogLogger` 固定 `slog.LevelInfo` + JSON handler，生产无法临时调 debug，本地调试读 JSON 费劲。
- **散落业务日志不统一**：各 service / worker 手写 `slog.WarnContext` / `slog.ErrorContext`，字段名、级别、措辞不一致。
- **缺 SQL 日志**：sqlc 查询经 `internal/store` 的 `DBTX` 接口执行，无统一的语句/耗时/慢查询日志，排查慢查询与数据库异常缺乏抓手。
- **日志类型无统一标识**：access / 外部调用 / SQL / 业务日志混在同一 stdout 流，只能靠 `msg` 区分，不便按类型过滤。

## 目标

1. 补一个统一的 HTTP access log 中间件（仅 stdout JSON）。
2. 统一外部调用（new-api / RAGFlow）日志，成功失败都记元数据。
3. 让日志级别与输出格式可通过 env 配置。
4. 制定日志规范并全量重构现有散落日志。
5. 补一个统一的 SQL 日志（语句 / 耗时 / 慢查询 / 错误），覆盖事务内外。

此外，给**所有**日志统一加一个 `log_type` 字段用于按类型过滤，取值：`http`（access log）/ `sql` / `newapi` / `ragflow` / `app`（业务及其它普通日志）。

## 非目标

- access log **不落库**、不做后台查询页（依赖 k8s 日志采集消费 stdout JSON）。
- 不引入新的日志库（继续用 `slog`）。
- 不记录外部调用的请求/响应 body（避免泄密与噪音）。
- 不记录 SQL 的参数值（避免密码 hash / token / PII 入日志）。
- 不动 `audit_logs` 业务审计表（与本次「运行日志」是两个体系，本次不扩展）。
- 不加 access log 开关 / 采样旋钮（保持简单）。
- 不落文件、不做日志轮转；全部打 stdout 单流，由 k8s 采集消费。

## 总体取舍

- access log 只打 stdout JSON，靠 slog 既有 handler 自动带 trace_id；不动数据库。
- 外部调用日志走 **自定义 `http.RoundTripper`** 在基础设施层统一拦截，沿用项目「脱敏 writer 在底层统一拦截」的既有风格，client 业务方法零改动，未来新增调用天然覆盖。
- SQL 日志走 **包装 `sqlc.DBTX` 接口**，同一思路（基础设施层统一拦截、业务零改动、新查询天然覆盖），并覆盖事务路径。
- 统一 `log_type` 字段中，业务日志类型（`app`）由 **handler 兜底注入**，基础设施日志（http/sql/newapi/ragflow）在调用点显式带，避免给海量业务日志逐条手加。
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
- 配置来源是 `manager.yaml` 的 `logging` 段（`internal/config.LoggingConfig`，与 app/database/redis 等同构），**不读环境变量**；loader 回填默认（info / json / 200ms）：
  - `level`：`debug` / `info` / `warn` / `error`，大小写不敏感，非法值 fallback Info。
  - `format`：`json` / `text`，非法值 fallback json。
  - `slow_query_ms`：慢查询阈值（毫秒，默认 `200`），由模块 5 的 SQL 日志 wrapper 读取，详见模块 5。
- `internal/log` 提供 `ParseConfig(level, format string) Config` 把配置字符串解析为 slog Config。
- `cmd/server/main.go` 处由 `NewSlogLogger(logOut)` 改为 `NewSlogLogger(logOut, managerlog.ParseConfig(cfg.Logging.Level, cfg.Logging.Format))`，并在打开数据库前 `store.SetSlowQueryThreshold(...)` 注入慢查询阈值。

> 注：初版实现读 `LOG_LEVEL` / `LOG_FORMAT` / `LOG_SLOW_QUERY_MS` 环境变量；2026-06-12 按「配置统一进 manager.yaml」决定改为配置文件驱动，env 不再参与。

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
  - `log_type`：固定 `http`（中间件 attrs 里显式带）。
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
  func New(base http.RoundTripper, logType string) http.RoundTripper
  ```
  `base` 为 nil 时退回 `http.DefaultTransport`。`logType` 传 `newapi` / `ragflow`。
- `RoundTrip` 计时执行内层请求，记录字段：
  - `log_type`：`newapi` / `ragflow`（构造时传入；与原计划的独立 `service` 字段合并，不再单列 `service`，避免冗余）
  - `method`、`endpoint`（`req.URL.Path`，**不含 query**，避免敏感参数泄露）
  - `status`（transport error 时省略，记 `error`）
  - `latency_ms`
- 级别：2xx/3xx → Debug；4xx/5xx 或 transport error → Warn。**不记 body**。（3xx 多为 304 条件命中或自动跟随的重定向，非错误，记 Debug 避免噪音。）
- 日志用 `req.Context()` 作为 ctx，使外部调用日志也自动带上发起该调用的请求 trace_id（实现链路串联）。
- 接入：在各 integration 构造函数内把 `http.Client.Transport`（newapi 的 `Client.HTTPClient` 与 ragflow 经由的 `httpclient.BaseHTTPClient.HTTPClient`）设置为 `httplog.New(http.DefaultTransport, "<service>")`。`cmd/server/main.go` 的 wiring 不需要改动；log_type 标签随构造函数固定。

**测试**：用 stub `http.RoundTripper` 作为 base，断言 2xx→Debug、4xx/5xx→Warn、transport error→Warn 且带 `error` 不带 `status`；`endpoint` 不含 query；`log_type` 标签正确（`newapi` / `ragflow`）。

---

## 模块 4 — 日志规范 + 全量重构散落日志

**位置**：新增 `internal/log/attrs.go`（规范常量 + helper）；规范文档；随后分批改 `internal/service/*`、`internal/worker/handlers/*`。

**规范常量与 helper**：

- 统一 attr key 常量，避免各处字符串字面量漂移：
  - `trace_id` / `org_id` / `actor_id` / `actor_role` / `target_type` / `target_id` / `action` / `result` / `error` / `log_type` / `endpoint` / `status` / `latency_ms`
- `log_type` 取值常量：`http` / `sql` / `newapi` / `ragflow` / `app`。`app` 为业务及其它普通日志的兜底类型。
- 级别使用原则（写进规范文档与常量注释）：
  - **Debug**：正常流程的细粒度追踪（如外部调用成功）。
  - **Info**：正常业务里程碑（如 access log、任务完成）。
  - **Warn**：可恢复 / 不阻塞主流程的异常（如外部清理失败但业务已成功、可重试错误）。
  - **Error**：不可恢复或导致数据不一致、需要人工介入的错误。
- 提供轻量 helper（如 `Err(error) slog.Attr` 统一错误字段名为 `error`），避免重复 `slog.String("error", err.Error())`。

**`log_type=app` 兜底注入（避免逐条手加）**：

- 在 `internal/log/slog.go` 现有 `requestIDHandler` 同层做兜底：`Handle` 时若 record 未携带 `log_type` attr，则自动补 `log_type=app`；`http` / `sql` / `newapi` / `ragflow` 四类基础设施日志在各自调用点显式带 `log_type`，handler 检测到已存在就跳过。
- 效果：「所有日志都带 `log_type`」成立，而模块 4 的全量重构**不必给每条业务日志手加** `log_type`，显著降低改动面与噪音。
- 实现注意：`Handle` 需遍历 `record.Attrs` 判断是否已有 `log_type`（attr 数量小，成本可忽略）；text 与 json 两种格式都生效。

**规范文档**：写一份简短的日志规范（放 `docs/` 下或作为 `AGENTS.md` 的「日志」小节，实现时定位），覆盖：字段命名常量、级别原则、「外部调用走 RoundTripper 不在 service 层重复记」「不打 body / 不打敏感字段」等约束。

**全量重构**（**前置依赖：上述常量与 helper 必须先合入**）：

- 按目录分批扫 `internal/service`、`internal/worker/handlers` 的现有 `slog.*Context` 调用，对齐到：统一字段常量、统一 `error` 字段、按级别原则修正用错的级别、去掉与 RoundTripper / access log 重复的记录、统一中文措辞。
- **不在 sweep 里逐条加 `log_type`**：业务日志的 `log_type=app` 由上文 handler 兜底注入统一兜住，sweep 只需保证基础设施类日志（若 service 层确有手写的 http/sql/外部调用类日志）改走对应模块、不与之重复。
- **逐文件分提交**，每个提交只做日志对齐、不夹带行为变更，commit message 说明扫描范围；保证可独立评审。
- 不为重构强加新单测（无新行为）；若某处级别修正涉及业务判断，相应补/调测试。

---

## 模块 5 — SQL 日志（logging DBTX）

**位置**：新增 `internal/store/dblog.go`（实现 `sqlc.DBTX` 的 `loggingDBTX` 包装）；接入点 `internal/store/store.go` 的 `New` 与 `WithTx`。

**接入方式**：

- `sqlc.DBTX` 是所有查询的统一入口（`ExecContext` / `QueryContext` / `QueryRowContext` / `PrepareContext`）。`*sql.DB` 与 `*sql.Tx` 都满足该接口，故 wrapper 包接口即可同时覆盖普通连接与事务：
  - `store.New`：`sqlc.New(db)` → `sqlc.New(wrap(db))`
  - `store.WithTx`：`s.Queries.WithTx(tx)` → `sqlc.New(wrap(tx))`（绕过 sqlc 自带的 `WithTx`，改为对 `tx` 重新包装，使**事务内 SQL 也被记录**）
- wrapper 内层直接委托给被包装的 `DBTX`，业务方法签名与行为不变。

**记录字段**：

- `log_type`：固定 `sql`
- `sql`：语句文本（sqlc 用 `?` 占位符参数化，语句本身不含真实值，无 PII、基数可控）
- `latency_ms`：执行耗时
- `rows`：**仅 `ExecContext`** 从 `sql.Result.RowsAffected()` 取（写操作影响行数）；`QueryContext` / `QueryRowContext` 的返回行数需要消费业务方的 `*sql.Rows` 才能数，wrapper 不得 drain，故查询类**不记** `rows`
- `error`：执行出错时记
- trace_id 由 ctx 自动注入（各方法第一个入参即 `context.Context`，wrapper 用它调用 `slog.LogAttrs`）
- **不记参数值**（避免密码 hash / token / PII 入日志）

**级别**：

- 正常 → Debug（生产默认 `LOG_LEVEL=info` 时不输出）
- 耗时 > `LOG_SLOW_QUERY_MS`（默认 200ms）→ Warn（慢查询）
- 执行 error → Error

**配置**：

- `LOG_SLOW_QUERY_MS`：慢查询阈值（毫秒），默认 200。由 wrapper 包级变量在初始化时从 env 读一次；非法值 fallback 默认。

**测试**：用 stub `sqlc.DBTX` 作为被包装对象，断言：正常→Debug、慢查询（耗时超阈值）→Warn、执行 error→Error；`rows` 只在 `ExecContext` 出现、查询类不带；日志不含参数值；`PrepareContext` 透传不额外记录（或按实现确认其处理）；事务路径（`WithTx`）内的查询同样产生 SQL 日志。

---

## 实现顺序

1. 模块 1（可配置化）— 独立、无依赖，先落。
2. 模块 4 的「规范常量 + helper + 规范文档 + `log_type` 取值常量 + handler 兜底注入」— 作为后续模块的词汇表，先于 sweep 落。
3. 模块 2（access log 中间件，带 `log_type=http`）、模块 3（外部调用 RoundTripper，带 `log_type=newapi/ragflow`）、模块 5（SQL 日志，带 `log_type=sql`）— 复用模块 4 的常量，可并行。
4. 模块 4 的「全量重构 sweep」— **最后做**，按目录分批、逐文件分提交。

## 风险与缓解

- **全量重构面大、易引噪音**：靠「常量 helper 先行 + 逐文件分提交 + 不夹带行为变更」约束；review 时每个提交聚焦单一目的。
- **route 基数 / PII**：access log 用 `c.FullPath()` 路由模板而非真实路径，外部调用只记 path 不记 query，从源头避免 ID/敏感参数进日志。
- **重复日志**：外部调用统一由 RoundTripper 记，service 层不再重复记成功/请求细节；access log 统一记 HTTP 层，handler 不再各记一遍。
- **SQL 日志噪音 / 慢查询淹没**：正常 SQL 走 Debug（生产默认不输出），仅慢查询（> 阈值）抬到 Warn、错误抬到 Error；阈值经 `LOG_SLOW_QUERY_MS` 可调，生产无需改代码即可定位慢查询。
- **SQL 参数 PII**：只记 sqlc 参数化后的语句文本（占位符 `?`），不记参数值，从源头杜绝密码 hash / token 入日志。
- **事务覆盖遗漏**：`store.WithTx` 改为对 `tx` 重新包装而非走 sqlc 原生 `WithTx`，确保事务内 SQL 同样被记录，不留盲区。

## 交付验证

- 单元测试：模块 1/2/3/5 按各自「测试」小节覆盖；模块 4 的 handler 兜底注入补测（未带 log_type 的日志自动补 `app`、已带的不被覆盖）。
- 真实环境验证（按 `CLAUDE.md` 要求，浏览器 + 实际请求）：本地 k3d 起 manager，发起若干请求 + 触发一次 new-api/RAGFlow 调用，确认 stdout 出现 `http_request`、外部调用、SQL 日志，字段齐全、`log_type` 正确分类（http/sql/newapi/ragflow/app）、trace_id 串联、敏感字段已脱敏；切 `LOG_LEVEL=debug` 后能看到正常 SQL 与外部调用 Debug 日志；构造一条慢查询确认抬到 Warn；切 `LOG_FORMAT=text` 确认配置生效；调 `LOG_SLOW_QUERY_MS` 确认阈值生效。
