# Spec B：后端错误消息 i18n（msgKey + locale 中间件）

- 日期：2026-06-24
- 状态：设计已评审，待实现
- 所属拆分：国际化整改三件套之 **B**（A=hermes 运行时文案 / B=后端错误 i18n / C=manager 侧 locale 赋值·传播·实时展示）

## 背景

后端所有客户可见文案目前是**中文硬编码**，经 `apierror.New(code, "中文")` 出口；前端
`client.ts` 直接展示后端返回的 `message`，导致把界面语言切成 English 的用户，触发任何
后端错误时仍看到中文。

排查现状（2026-06-24）：
- `internal/api/apierror/response.go`：`ErrorResponse{Code, Message string}`，仅这两个字段。
- `internal/api/` 内 `apierror.New(` 共 **194 处**调用，其中 **172 处带中文 message**，去重
  约 **111 条**中文字面量；`internal/service/errors.go` 另有 **81 条** sentinel error（全
  `errors.New("中文")`）。合计约 **150–190 条**去重消息需要英文译文。
- error code 共 56 个，sentinel 81 条——**code 粒度粗于 message**（多对一 collapse，如
  `RUNTIME_NOT_AVAILABLE` 被 Kanban/Cron/Conversation 共用），故「前端纯按 code 查表」
  无法区分具体文案。
- 前端 `client.ts:138-142` **已在发 `Accept-Language` 头**（取自用户当前 UI locale），但
  后端中间件未消费。
- 前端展示用 naive-ui，直接显示 `message`，无错误码 i18n 表。

## 架构决策

**后端翻译**（而非前端按 code 查表）：后端读 `Accept-Language` 按 locale 返回已本地化的
`message`，**前端零改动**。理由：①前端已在发 locale；②message 粒度（~190）天然保留，不
受 code 粗粒度限制；③与「new-api/hermes：后端为事实源」一致；④`ErrorResponse` 形状不变，
openapi/`web/src/api/generated.ts`/前端类型零改动。

**翻译机制：显式 msgKey 常量 + 中心 catalog**（而非「中文串为 key」或「响应拦截中间件」）。
每条用户可见消息起一个 `MsgKey` 常量，catalog 映射 `MsgKey → {zh, en}`；写出 helper 按
请求 locale 解析。类型安全、单一来源、编译期可检。

## 目标与范围

**目标**：后端一切返回给客户端的文案随请求 locale（en/zh）输出，前端无需改动。

**范围内**：
- 194 处 `apierror.New` 文案（含 errors.go 81 条 sentinel 经 `request_errors.go` 映射的）。
- 500 / `INTERNAL` 兜底：返回通用本地化文案，不外泄技术细节。
- 请求校验 / 绑定错误（gin `ShouldBind` 失败等）：通用本地化文案 `err.invalid_request`，
  细节进日志。
- 其它返回给客户端的文案，如 `ragflow_parse_status_refresher.go` 的解析失败原因。

**范围外**：
- 日志文案、代码注释。
- 持久化数据：如 `onboarding_service.go` 的默认成员名 `"成员"`（写入 DB 的 name，非运行期
  消息），不在本 spec。
- 前端已自管的 vue-i18n 文案、审计日志 label（前端按 locale 渲染）。

## 关键事实（已确认）

- `ErrorResponse{Code, Message}`，JSON `{code, message}` 两字段，无 details/fields。
- error code 在各 handler 直接以字符串字面量创建，未集中定义常量；同一 code（如 `FORBIDDEN`）
  在不同 handler 配不同中文（`无权管理平台技能` vs `无权查看模型列表`）——故翻译单元必须
  **细于 code**，按消息（msgKey）。
- 所有错误出口都经 gin `*gin.Context`（可取 locale）。
- `config.I18nConfig.default_locale`（默认 en）与 `service.SupportedLocales = ["en","zh"]`
  已存在，locale 归一复用之。

## 架构与组件

### (a) locale 中间件（新增 `internal/api/middleware/locale.go`）
- 解析 `Accept-Language` 首选标签 → 归一到受支持 locale（en/zh；未知/缺失回落
  `config.i18n.default_locale`）→ `c.Set(localeContextKey, loc)`。
- 复用 `service.SupportedLocales` 与归一逻辑（参考 `auth_service.go` 的 `isSupportedLocale`，
  必要时抽公共 `i18n.NormalizeLocale`）。
- 注册在全局中间件链（在 auth 之前/之后均可，错误响应在其后产生即可）。
- 内部端点（bootstrap，由 oc-ops sidecar 调用，无 Accept-Language）自然回落 default。

### (b) 错误消息 catalog（新增 `internal/api/apierror/messages.go`，Go 原生）
```go
type MsgKey string

const (
    // common
    MsgForbidden      MsgKey = "err.forbidden"
    MsgNotFound       MsgKey = "err.not_found"
    MsgConflict       MsgKey = "err.conflict"
    MsgUnauthorized   MsgKey = "err.unauthorized"
    MsgInternal       MsgKey = "err.internal"
    MsgInvalidRequest MsgKey = "err.invalid_request"
    MsgUpstreamUnavailable MsgKey = "err.upstream_unavailable"
    // 按 domain 分块：app / kanban / cron / conversation / knowledge /
    //   platform_skill / custom_skill / assistant_version / recharge /
    //   captcha / channel / workspace / industry_knowledge / member /
    //   enroll / runtime / usage / bootstrap … 共约 190 条
)

// zh 逐字迁移自现有硬编码；en 新译。可扩展更多语言。
var catalog = map[MsgKey]map[string]string{
    MsgForbidden: {"zh": "无权执行该操作", "en": "You are not allowed to perform this action."},
    MsgInternal:  {"zh": "服务器内部错误", "en": "Internal server error."},
    // …
}
```
key 命名 `err.<domain>.<slug>`。

### (c) apierror 写出 helper（改造 `internal/api/apierror/response.go`）
```go
// Localize：纯函数，便于单测。缺 key 回落 en，再缺回落 key 本身（永不 panic）。
func Localize(key MsgKey, loc string, args ...any) string

// JSON：读 c 的 locale，解析 key→message，写 ErrorResponse{code, message}。
func JSON(c *gin.Context, status int, code string, key MsgKey, args ...any)
```
- 动态消息（少数 fmt.Sprintf 型）：catalog 串用占位符（`%s` 或 `{name}`）+ `args`/具名参数。
- `ErrorResponse` 结构与 `New(code, message)` 保留：`New` 仅用于确不需翻译的极少数处，
  或过渡期；新代码一律走 `JSON`。

### (d) 错误→响应映射改造
- `internal/api/handlers/request_errors.go`：sentinel error → (status, code, **MsgKey**)
  （替换现 message 字符串）。
- 各 handler 内联 `c.JSON(status, apierror.New(code, "中文"))` → `apierror.JSON(c, status, code, MsgXxx)`。
- 校验/绑定错误统一 `apierror.JSON(c, 400, "INVALID_REQUEST", MsgInvalidRequest)`，原始
  绑定错误写日志。
- 500 路径统一 `MsgInternal`，真实错误写日志，不外泄。

### (e) errors.go sentinel
保留为控制流 error 值，`.Error()` 维持中文供日志/`errors.Is`。其用户可见文案在 catalog 以
对应 MsgKey 承载（zh 与 sentinel 中文重复，换取日志可读 + errors.Is 不变）。`request_errors.go`
的映射把 sentinel 与 MsgKey 关联。

## 一致性守卫（构建/测试期，仿 Spec A）

- catalog 每条同时含 `zh` 与 `en`，且非空。
- 每个被引用的 `MsgKey` 都在 catalog（Go const 引用未定义即编译错；另用测试校验
  const ↔ catalog 无孤儿、无缺译）。
- 扫描测试：`internal/api/`（除测试与白名单）内不应再出现 `apierror.New(..., "<含中文字面量>")`，
  强制走 msgKey，fail-loud。

## 测试

- **单元**：
  - locale 中间件：`Accept-Language` 解析、归一（`zh-CN`→`zh`）、未知/缺失回落 default。
  - `Localize`：zh/en 正确返回、缺 key 回落 en→key、动态占位符格式化。
  - catalog 一致性 / 覆盖 / 无裸中文扫描。
- **既有 handler 测试**：现多处断言中文 message；改为断言 `code` + 用 `Localize(key, loc)`
  生成期望 message（或按 default locale 断言）。
- **真实浏览器验证**（CLAUDE.md 强制）：以 English 用户触发各类错误（403/404/409/校验/500/
  各业务错误）确认看到英文；中文用户确认中文；核对 naive-ui message 与页面错误位、Network
  响应 `message` 字段随 `Accept-Language` 变化。

## 风险与缓解

- **改动面大**（194 + 81 站点 + 大量 handler 测试）：靠 MsgKey 编译检查 + 一致性守卫 +
  无裸中文扫描兜底；按 domain 分批改造、逐批提交、逐批跑测试。
- **动态 message（Sprintf）**：逐个转 catalog 占位符 + args，单测覆盖。
- **handler 测试断言批量更新**：从中文串改为 code/`Localize`，工作量计入计划。
- **locale 归一一致性**：复用 `config.i18n.default_locale` 与 `SupportedLocales`，避免与
  前端 LocaleSwitcher 行为漂移。
- **遗漏**：无裸中文扫描 + 真实浏览器全类别触发，确保「所有客户可见文案」全覆盖。

## 交付物清单

- 新增 `internal/api/middleware/locale.go`（+ 可选 `internal/i18n/normalize.go` 公共归一）。
- 新增 `internal/api/apierror/messages.go`（MsgKey 常量 + catalog）。
- 改造 `internal/api/apierror/response.go`（`Localize` / `JSON`）。
- 改造 `internal/api/handlers/request_errors.go` 与各 handler 错误写出 + 校验/500 兜底。
- 新增一致性守卫 / 无裸中文扫描测试；更新受影响 handler 测试。
- 前端、openapi、`web/src/api/generated.ts` 零改动。
