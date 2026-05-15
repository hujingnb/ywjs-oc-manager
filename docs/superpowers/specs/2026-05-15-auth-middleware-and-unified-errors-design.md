# 认证中间件与统一错误响应设计

> 把 21 个 handler 文件中 17 处重复的 token 校验样板下沉为 `RequireUserAuth` 中间件，
> 把 `ErrorResponse` 改为 `{Code, Message}` 双字段并建一张 sentinel → code 映射表，
> 让前端拿到稳定 `code` 字段，让 handler 回归"绑请求 → 调 service → 写响应"的薄壳形态。
> 改造拆为两个独立 PR：phase 1（中间件 + ErrorResponse 字段直切）、phase 2（base 表收口 + writeXxxError 薄化）。

## 1. 背景与动机

当前 HTTP 层有两类重复与不一致：

- **认证模板重复**：`bearerToken(c.GetHeader("Authorization"))` + `h.tokens.VerifyAccessToken(token)`
  这 6 行模板，在 17 个 handler 里抄了一遍，文字「缺少访问令牌」「访问令牌无效」
  出现 38 次。任何对认证的扩展（限流、审计、租户头解析、IP 白名单）都得手抄 17 处。
- **错误响应散落且不一致**：
  - `internal/api/handlers/dto.go::ErrorResponse` 只有 `Error string` 一个字段，几乎无人使用；
    handler 全部直接 `c.JSON(status, gin.H{"error": "..."})`。
  - 13 个 `writeXxxError` 函数 switch-case 高度同形，但对同一 sentinel
    （`ErrForbidden` / `ErrNotFound`）给出的文案各异、缺少机器可读字段。
  - `members.go::NO_NODE_AVAILABLE` 单独发明了 `{code, message}` 双字段响应，
    与全局 `{error}` 风格不一致，前端契约已经分叉。
  - 已有的 `writeMappedServiceError` 雏形只挂了 3 条 rule，没被推广。

可观察的痛点：

- 前端拿不到稳定的错误标识，只能靠中文文案 startsWith 匹配做条件分支。
- 接口契约里"哪些路径需要 access token"必须靠 grep `bearerToken` 反推，不显式。
- handler 单测里有大量「缺 token / token 无效」用例（粗估 ~34 条），其实测的是同一段代码。

本次改造目标：

1. 抽出 `RequireUserAuth` 中间件并按路由分组挂载，handler 不再持有 `*auth.TokenManager`（`AuthHandler` 除外）。
2. 把 `auth.Principal` 通过 `c.Request.Context()` 注入下游，handler 用 `auth.PrincipalFromContext(ctx)` 取。
3. 把 `ErrorResponse` 改为 `{Code, Message}` 两字段，破坏性变更，不保留兼容字段。
4. 建立 sentinel → `(status, code, message)` 映射表，13 个 `writeXxxError` 薄化为 override-only 入口。
5. 改造拆两个 PR 独立交付：phase 1 = middleware + ErrorResponse 字段切；phase 2 = 映射表收口。

非目标（明确拒绝）：

- 不下沉 `errorCode` 字段到 service —— service 不知道 HTTP 是设计边界。
- 不统一 agent 路由的认证方式（enrollment_secret / agent_token 各有合理性）。
- 不引入前端 code 驱动的 UX 分支（如 NO_NODE_AVAILABLE CTA）—— 留待后续按业务诉求逐个加。
- 不挂限流 / 审计 / 租户隔离 middleware —— 本次只完成位置准备，不做功能扩展。
- 不动 service `errors.go` 中的 26 条 sentinel；不动路由 URL / 请求字段名。

## 2. 现状要点

- Go 业务代码 26.7k 行（21 handler / 26 service / 28 internal 包）；handler / service 测试覆盖 1:1。
- `auth.TokenManager` 已有 `VerifyAccessToken(token) (Principal, error)`；`Principal{UserID, OrgID, Role}` 已存在。
- 认证类型有三种，本次中间件只覆盖第一种：
  - `Authorization: Bearer <access_token>`：用户接口。
  - `Authorization: Bearer <enrollment_secret>`：agent enroll，目前由 `AgentEndpointsHandler` 自校验。
  - 请求体 `agent_token` 字段：agent heartbeat，目前由 service 层校验。
- 26 条 sentinel 全部集中在 `internal/service/errors.go`。
- `request_errors.go::writeMappedServiceError` 已是雏形，但只挂了 3 条 rule；本次会被 `WriteServiceErrorWith` 取代。
- handler / service 测试齐全（20 个 handler test 文件），有兜底重构的安全网。

## 3. 总体架构

新增的包与文件：

```
internal/
├── api/
│   ├── apierror/
│   │   └── response.go          # 新：ErrorResponse + New() 构造器
│   ├── handlers/
│   │   └── errors_table.go      # 新（phase 2）：baseServiceErrorTable + WriteServiceError(With)
│   ├── middleware/
│   │   └── auth.go              # 新：RequireUserAuth
│   └── router.go                # 改：public / agent / user 三组路由
└── auth/
    └── context.go               # 新：WithPrincipal / PrincipalFromContext
```

包依赖方向（无新循环依赖）：

```
handlers   ──► auth (Principal, PrincipalFromContext)
handlers   ──► service (sentinel errors)
handlers   ──► apierror (ErrorResponse, New)
middleware ──► auth (TokenManager, WithPrincipal)
middleware ──► apierror (ErrorResponse)
service    ──► auth (Principal)         # 已有，不动
```

路由分三组：

- **public**：`/health`、`/api/v1/auth/login`、`/api/v1/auth/refresh`。无中间件。
- **agent**：`/api/v1/agent/enroll`、`/api/v1/agent/heartbeat`、`/api/v1/agent/...`。
  保留各自的 enrollment_secret / agent_token 校验，**不挂** `RequireUserAuth`。
- **user**：其余全部接口。挂 `RequireUserAuth`，handler 从 ctx 拿 principal。

## 4. 中间件设计

`internal/api/middleware/auth.go`：

```go
// RequireUserAuth 校验 Authorization: Bearer <access_token>，把 Principal 注入
// c.Request.Context()，并把同一个 ctx 写回 *gin.Context.Request。
// 校验失败时直接 c.AbortWithStatusJSON 401，handler 不会被执行。
//
// 设计取舍：
//   - 不做角色 / 资源权限判断，仅"凭证有效性"。资源 / 角色级仍由 service 层
//     借助 authorizer.Can* 完成（避免 middleware 提前 403 误伤跨组织数据访问）。
//   - 多种失败原因（缺失 / 格式错 / 签名失败 / 过期）统一返回 401 + code=UNAUTHENTICATED，
//     不暴露具体原因（防探测）。
func RequireUserAuth(tokens *auth.TokenManager) gin.HandlerFunc {
    return func(c *gin.Context) {
        raw := c.GetHeader("Authorization")
        token, ok := parseBearer(raw)
        if !ok {
            abortUnauthenticated(c, "缺少访问令牌")
            return
        }
        principal, err := tokens.VerifyAccessToken(token)
        if err != nil {
            abortUnauthenticated(c, "访问令牌无效")
            return
        }
        ctx := auth.WithPrincipal(c.Request.Context(), principal)
        c.Request = c.Request.WithContext(ctx)
        c.Next()
    }
}

func parseBearer(header string) (string, bool) {
    scheme, token, ok := strings.Cut(header, " ")
    return token, ok && strings.EqualFold(scheme, "Bearer") && token != ""
}

func abortUnauthenticated(c *gin.Context, msg string) {
    c.AbortWithStatusJSON(http.StatusUnauthorized,
        apierror.New("UNAUTHENTICATED", msg))
}
```

`internal/auth/context.go`：

```go
type principalContextKey struct{}

func WithPrincipal(ctx context.Context, p Principal) context.Context {
    return context.WithValue(ctx, principalContextKey{}, p)
}

func PrincipalFromContext(ctx context.Context) (Principal, bool) {
    p, ok := ctx.Value(principalContextKey{}).(Principal)
    return p, ok
}
```

handler 新写法：

```go
func (h *AppRuntimeHandler) GetRuntime(c *gin.Context) {
    principal, _ := auth.PrincipalFromContext(c.Request.Context())
    view, err := h.service.InspectApp(c.Request.Context(), principal, c.Param("appId"))
    if err != nil {
        writeAppRuntimeError(c, err) // phase 1 仍是它，phase 2 薄化
        return
    }
    c.JSON(http.StatusOK, gin.H{"runtime": view})
}
```

中间件已保证 principal 必然存在，`_` 忽略 `bool` 是有意为之，不加 belt-and-braces 防御代码。

router.go 改造形态：

```go
public := router.Group("")
handlers.RegisterHealthRoutes(public)
if dep.AuthService != nil {
    handlers.RegisterPublicAuthRoutes(public, authHandler) // login, refresh
}

agent := router.Group("")
if dep.RuntimeNodeService != nil {
    handlers.RegisterAgentRoutes(agent, agentHandler)
}

user := router.Group("")
user.Use(middleware.RequireUserAuth(dep.TokenManager))
{
    handlers.RegisterAuthMeRoutes(user, authHandler) // logout, me
    handlers.RegisterOrganizationRoutes(user, ...)
    // ... 其余 register 调用
}
```

中间件挂载顺序：`gin.Recovery` → `CORSAllowOrigin`（可选）→ `RequestID` → `RequireCSRF` → `RequireUserAuth`。
CSRF 在前、auth 在后，与现状一致。

## 5. `ErrorResponse` 与 base 表

`internal/api/apierror/response.go`（phase 1 新建）：

```go
package apierror

// ErrorResponse 是所有 HTTP 错误响应的统一结构。
//
// Code 是机器可读的稳定标识，前端用来做 i18n / 跳转 / 条件分支判断；
// Message 是面向终端用户展示的中文文案，可能因 handler 语境而异。
type ErrorResponse struct {
    Code    string `json:"code" example:"APP_NOT_FOUND"`
    Message string `json:"message" example:"应用不存在"`
}

func New(code, message string) ErrorResponse {
    return ErrorResponse{Code: code, Message: message}
}
```

`internal/api/handlers/errors_table.go`（phase 2 新建）：

```go
// serviceErrorEntry 描述一条 sentinel → HTTP 响应映射。
type serviceErrorEntry struct {
    sentinel error
    status   int
    code     string
    message  string // 空时使用 redactlog.SafeErrorMessage(err)
    safe     bool   // true → 用脱敏后的 service 错误原文做 message
    validate bool   // true → 剥离 sentinel 前缀作为 message（兼容 %w 包装）
}

// baseServiceErrorTable 覆盖跨接口语义稳定的 sentinel。详见第 6 章命名表。
var baseServiceErrorTable = []serviceErrorEntry{ /* 见 §6 */ }

// WriteServiceErrorWith 优先匹配 handler 提供的 overrides，再 fallback 到 base 表。
// 双方都未命中时记录 ErrorContext 日志，返回 500 + INTERNAL。
//
// override 与 base 表对同一 sentinel 匹配时：override 完全替换 base，一次响应只有一个 code。
func WriteServiceErrorWith(c *gin.Context, err error, overrides ...serviceErrorEntry) {
    if entry, ok := matchEntry(err, overrides); ok {
        writeEntry(c, err, entry)
        return
    }
    if entry, ok := matchEntry(err, baseServiceErrorTable); ok {
        writeEntry(c, err, entry)
        return
    }
    slog.ErrorContext(c.Request.Context(), "未识别 service 错误", "error", err)
    c.JSON(http.StatusInternalServerError, apierror.New("INTERNAL", "服务暂时不可用"))
}

func WriteServiceError(c *gin.Context, err error) {
    WriteServiceErrorWith(c, err)
}
```

handler 端 override 示例（phase 2 之后）：

```go
// 原 12 行的 writeAppRuntimeError 薄化为：
func writeAppRuntimeError(c *gin.Context, err error) {
    WriteServiceErrorWith(c, err,
        serviceErrorEntry{service.ErrNotFound, http.StatusNotFound,
            "APP_NOT_FOUND", "应用不存在", false, false},
        serviceErrorEntry{service.ErrRuntimeOperationDenied, http.StatusForbidden,
            "RUNTIME_OP_FORBIDDEN", "无权执行该运行操作", false, false},
    )
}
```

## 6. Code 命名表

26 条 sentinel + 3 条非 sentinel 响应。code 风格：`SCREAMING_SNAKE_CASE`，
命名优先「领域 + 原因」让前端无需看 URL 即可分流。

通用：

| sentinel | HTTP | code | message 来源 |
|---|---|---|---|
| `ErrForbidden` | 403 | `FORBIDDEN` | "无权执行该操作"（handler 可 override） |
| `ErrNotFound` | 404 | `NOT_FOUND` | "资源不存在"（handler 可 override） |
| `ErrConflict` | 409 | `CONFLICT` | "资源冲突"（handler 可 override） |

认证：

| sentinel | HTTP | code | message 来源 |
|---|---|---|---|
| `ErrInvalidCredentials` | 401 | `INVALID_CREDENTIALS` | "用户名或密码错误" |
| `ErrInvalidToken` | 401 | `INVALID_TOKEN` | "登录凭证无效" |
| `ErrUserDisabled` | 403 | `USER_DISABLED` | safe（脱敏后的 service 错误文案） |
| `ErrOrgDisabled` | 403 | `ORG_DISABLED` | safe |

节点调度：

| sentinel | HTTP | code | message 来源 |
|---|---|---|---|
| `ErrNoNodeAvailable` | 503 | `NO_NODE_AVAILABLE` | "暂无可用 Runtime Node，请联系平台管理员调整节点容量或新增节点" |

成员：

| sentinel | HTTP | code | message 来源 |
|---|---|---|---|
| `ErrMemberCreateInvalid` | 400 | `MEMBER_INVALID` | validate（剥离 sentinel 前缀的具体原因） |

渠道：

| sentinel | HTTP | code | message 来源 |
|---|---|---|---|
| `ErrChannelAdapterMissing` | 503 | `CHANNEL_ADAPTER_MISSING` | "当前渠道未启用" |

工作目录：

| sentinel | HTTP | code | message 来源 |
|---|---|---|---|
| `ErrWorkspaceForbidden` | 403 | `WORKSPACE_FORBIDDEN` | "无权访问工作目录" |
| `ErrWorkspaceMissing` | 503 | `WORKSPACE_NOT_CONFIGURED` | "应用未关联节点或 adapter 未配置" |
| `ErrWorkspaceBadPath` | 400 | `WORKSPACE_INVALID_PATH` | "非法工作目录路径" |

人设：

| sentinel | HTTP | code | message 来源 |
|---|---|---|---|
| `ErrPersonaNotFound` | 404 | `PERSONA_NOT_FOUND` | "组织尚未配置人设" |
| `ErrPersonaDenied` | 403 | `PERSONA_FORBIDDEN` | "无权访问该组织人设" |

知识库：

| sentinel | HTTP | code | message 来源 |
|---|---|---|---|
| `ErrKnowledgeForbidden` | 403 | `KNOWLEDGE_FORBIDDEN` | "无权访问该知识库" |
| `ErrKnowledgeMissing` | 503 | `KNOWLEDGE_NOT_CONFIGURED` | "知识库主副本未配置" |

充值：

| sentinel | HTTP | code | message 来源 |
|---|---|---|---|
| `ErrRechargeDenied` | 403 | `RECHARGE_FORBIDDEN` | "无权执行充值" |
| `ErrOrgMissingNewAPIUserID` | 409 | `ORG_MISSING_NEWAPI_USER` | "组织未关联 new-api 账户" |
| `ErrInvalidRechargeAmount` | 400 | `INVALID_RECHARGE_AMOUNT` | "充值金额必须为正" |

Agent：

| sentinel | HTTP | code | message 来源 |
|---|---|---|---|
| `ErrAgentTokenInvalid` | 401 | `AGENT_TOKEN_INVALID` | safe |
| `ErrEnrollInputInvalid` | 400 | `ENROLL_INVALID` | validate |

运行操作：

| sentinel | HTTP | code | message 来源 |
|---|---|---|---|
| `ErrRuntimeOperationDenied` | 403 | `RUNTIME_OP_FORBIDDEN` | "无权执行运行操作" |
| `ErrAppNotReinitializable` | 409 | `APP_NOT_REINIT` | "应用当前状态不允许重新初始化" |

资源指标：

| sentinel | HTTP | code | message 来源 |
|---|---|---|---|
| `ErrInvalidResourceRange` | 400 | `INVALID_RESOURCE_RANGE` | "资源查询范围不合法" |

用量：

| sentinel | HTTP | code | message 来源 |
|---|---|---|---|
| `ErrUsageUnavailable` | 503 | `USAGE_UNAVAILABLE` | "用量服务暂不可用" |

非 sentinel 响应：

| 触发点 | HTTP | code | message 来源 |
|---|---|---|---|
| 中间件未认证 | 401 | `UNAUTHENTICATED` | "缺少访问令牌" 或 "访问令牌无效" |
| handler 未识别错误兜底 | 500 | `INTERNAL` | "服务暂时不可用" |
| 请求体绑定 / 校验失败 | 400 | `BAD_REQUEST` | 由 `bindErrorMessage` / `validationErrorMessage` 生成 |

**code 视为接口契约**：一旦本表合入并发布，code 名只能新增不能改名。
后续重命名需在专门的破坏性变更窗口提出，并在 spec 中显式声明。

## 7. 迁移策略（两阶段）

### Phase 1：认证中间件 + ErrorResponse 字段直切（一个 PR）

变更点：

1. 新建 `internal/api/apierror/response.go`（`ErrorResponse{Code, Message}`、`New()`）。
2. 新建 `internal/auth/context.go`（`WithPrincipal` / `PrincipalFromContext`）。
3. 新建 `internal/api/middleware/auth.go`（`RequireUserAuth`、`parseBearer`、`abortUnauthenticated`）。
4. 改 `internal/api/handlers/dto.go`：删旧 `ErrorResponse{Error}`，handler 一律用 `apierror.ErrorResponse`。
5. 改 `internal/api/router.go`：分 public / agent / user 三组，user 组挂 `RequireUserAuth`；
   handler 构造器去 `TokenManager` 入参（除 `AuthHandler`）。
6. 改全部 21 个 handler 文件：
   - 删 `bearerToken(...) + tokens.VerifyAccessToken(...)` 6 行模板（17 处）。
   - `principal` 改为 `auth.PrincipalFromContext(c.Request.Context())`。
   - 改构造器签名（去 `*auth.TokenManager`）。
   - `writeXxxError` 内部仍 switch-case（phase 2 才薄化），但 `c.JSON` 调用全部换为
     `apierror.New(code, msg)` —— code 在 phase 1 用 inline 字符串，phase 2 才迁到 base 表。
7. 改 `members.go::NO_NODE_AVAILABLE` 那段不一致的 `{code, message}` 响应：
   用 `apierror.New("NO_NODE_AVAILABLE", "...")` 重写。
8. handler test：所有 `gin.H{"error": "..."}` 断言改为对 `apierror.ErrorResponse{Code, Message}` 的反序列化断言。
9. middleware test：新增 `auth_test.go`，覆盖 缺 header / 非 Bearer / 空 token / 篡改签名 / 过期 / 合法 六个用例。
10. 跑 `make openapi-gen && make web-types-gen`，提交生成产物。
11. 前端同 PR：
    - `web/src/api/client.ts`：把响应体 `error` 字段解析改为 `message`，把 `code` 字段透传到 `ApiError`。
    - `ApiError` 类增加 `code: string` 字段（默认 `UNKNOWN`）。
    - 全局错误提示组件 / `useMessage()` 调用点 —— 改读 `.message`。
    - 不引入 code 分支：phase 1 只做字段重命名 + 类型对齐。
    - 跑 `make web-typecheck`、`make web-test`。

### Phase 2：base 表收口 + writeXxxError 薄化（一个 PR）

变更点：

1. 新建 `internal/api/handlers/errors_table.go`：`baseServiceErrorTable`、`WriteServiceError`、`WriteServiceErrorWith`、`serviceErrorEntry`。
2. 把 `request_errors.go` 里的 `mappedServiceErrorRules` 三条规则合入 `baseServiceErrorTable`，删 `writeMappedServiceError`。
3. 改 13 个 `writeXxxError`，每个薄化为「最多 2-3 行 override」+ `WriteServiceErrorWith` 调用。
4. handler test：每个 handler 至少加一个用例断言 base 表对 `ErrForbidden`/`ErrNotFound` 的默认 code/message 生效。
5. 跑 `make openapi-gen && make web-types-gen`（理论上 yaml 不变；如有差异说明字段动了）。

### 两阶段之间

- Phase 1 已让 ErrorResponse 是 `{Code, Message}`，对外契约稳定；phase 2 未合时前端拿到的 code 仍是真实的。
- 两个 PR 可独立 review、独立 revert。

### 回滚策略

- Phase 1 出问题：回滚 = 删 `user.Use(...)` + 在 handler 恢复 `bearerToken` + `VerifyAccessToken` + ErrorResponse 字段回退。
  21 个 handler 都改了，回滚成本高 —— 所以 phase 1 PR 必须在测试环境跑全量浏览器手动验证 + smoke-v102。
- Phase 2 出问题：回滚 = 把 13 个 `writeXxxError` 改回 switch-case。影响小。

## 8. OpenAPI 与前端契约影响

OpenAPI 变化：

- `ErrorResponse` schema 字段：删 `error`，新增 `code` 和 `message`，两者都是 required。
- swag 注解里 `@Failure ... {object} ErrorResponse` 不需要改，类型引用自动跟随。
- 每个 handler 注解的 `@Param Authorization header string true "Bearer access_token"` **保留**：
  注解仍然真实反映"调用方需要带 Authorization"，与代码无矛盾，去掉只会让 swag 文档信息丢失。
- `agent.go::Enroll` 注解保持 `Bearer enrollment_secret`，不被影响。

前端类型变化（`web/src/api/generated.ts` 自动跟随）：

- `ErrorResponse` 从 `{ error: string }` 变为 `{ code: string; message: string }`。
- `make web-types-gen` 跑完后，所有读 `.error` 的 TS 文件编译报错 —— 作为审计入口。

前端受影响位置（粗扫，phase 1 PR 内审计完）：

- `web/src/api/client.ts`：错误抛出 / 解析。
- `web/src/api/hooks/*.ts`：useXxx hook 的 onError 处理。
- 页面级 `try/catch` 拿到 `ApiError` 后的提示。

版本与发布顺序：

- Phase 1 是**破坏性变更**（前端类型不向下兼容），但前后端在同 PR 同提交，
  部署上不存在跨版本兼容窗口 —— 老前端不会拿到新后端响应（SPA etag 强制刷新即恢复）。
- Phase 2 不破坏前端契约（字段集不变，只是 code 值变得更精细）。

## 9. 测试策略

新增测试：

- `internal/auth/context_test.go`：`TestPrincipalContextRoundTrip`、`TestPrincipalFromContextEmpty`。
- `internal/api/middleware/auth_test.go`：六条用例覆盖 缺 header / 非 Bearer / 空 token / 篡改签名 / 过期 / 合法注入。
- `internal/api/apierror/response_test.go`：`TestNew_FieldMirror`。

改造既有测试：

- 21 个 handler 的 `_test.go`：
  - 所有 `gin.H{"error": "..."}` 断言改为对 `apierror.ErrorResponse{Code, Message}` 的反序列化断言。
  - 删除每个 handler test 各自模拟「缺 token / token 无效」的样例（粗估 ~34 条），统一收口到 middleware test。
  - 测 handler 时在 ctx 里预注入 `auth.WithPrincipal(...)`，不挂中间件，纯测 handler 行为。
- 新增 `internal/api/handlers/testhelper_test.go`：导出 `withPrincipal(req, p)` 测试辅助函数。

集成 / 端到端：

- `cmd/seed-e2e` 现有 smoke 路径不变。
- 增加用例：用过期 access token 调任意接口，断言 401 + `Code=UNAUTHENTICATED`。
- 必须包含 runtime-agent enroll + heartbeat 用例（防 agent 路由误挂中间件）。
- `make smoke-v102` 跑全量回归。

前端测试：

- `web/src/api/client.test.ts` 增加用例：mock 返回 `{code, message}`，断言 `ApiError.code` 与 `.message`。
- `make web-test` 跑全量。

覆盖率：不强制提指标，目标是「测试集中度提升 / 重复用例减少」。

浏览器手动验证（按项目交付前检查规约）：

- Phase 1：登录平台管理员账号 + 组织管理员账号，跑通：登录、列表查询、创建组织、创建成员、应用初始化、查看运行节点、退出登录。验证错误提示文案正常、network tab 看到的响应体是 `{code, message}` 结构。
- Phase 2：重复上述路径，重点验证错误场景（删不存在的资源、无权操作、节点不足时建应用）的 code 与状态码与 spec §6 表一致。

## 10. 风险与缓解

| 风险 | 影响 | 概率 | 缓解 |
|---|---|---|---|
| `RequireUserAuth` 误挂到 agent 路由组 | agent enroll/heartbeat 全 401，节点全部离线 | 中 | router 分组在同一 PR 内完成；middleware test 加「未挂中间件的 group 通过」对照用例；smoke-v102 必须包含 agent heartbeat |
| 前端字段名直切破坏老 SPA | 部署窗口里用户看到 `undefined` 文案 | 中 | 前后端同 PR 提交 + SPA etag 强制刷新 |
| §6 表中 26 条 sentinel + 3 条非 sentinel 的 code 命名拍得草率 | 后续只能新增不能改名，形成历史包袱 | 低 | 命名表在 spec §6 显式列出，spec 评审阶段就敲定 |
| CSRF 与 auth 顺序错乱 | 写操作被错放 / 错挡 | 低 | 顺序固定为 RequestID → CSRF → Auth；spec 与 router.go 注释明确 |
| handler 删 `*TokenManager` 入参 | cmd/server 装配代码 + 21 个 handler test 装配连锁变更，漏改一处编译过不了 | 低 | 编译期错误（非运行期）；PR 描述对 reviewer 标注 |
| ErrorResponse `Error` 字段被外部脚本读取 | 外部脚本拿到 `undefined` | 低 | manager 是闭环系统；如有运维脚本依赖请运维侧同步审计 |
| 21 handler 一次性改完 diff 大 | review 漏看 | 中 | commit 按文件批次拆（middleware/apierror/auth context 一个 commit；handler 按 5-6 个文件一组）；验收清单里 grep 断言 |

## 11. 验收标准

### Phase 1

- [ ] 21 个 handler 内无 `bearerToken(` 或 `tokens.VerifyAccessToken` 调用（`auth.go` 处理 refresh / login 除外）。
- [ ] `grep -rn "缺少访问令牌\|访问令牌无效" internal/api/handlers` 返回 0 行（中间件接管，handler 不再有此文案）。
- [ ] `grep -rn 'gin.H{"error"' internal/api/handlers` 返回 0 行。
- [ ] `internal/api/handlers/dto.go::ErrorResponse` 已删除，所有引用切到 `apierror.ErrorResponse`。
- [ ] `make test web-test openapi-check` 全绿。
- [ ] 浏览器手动验证 7 条主路径通过。
- [ ] runtime-agent 在改造后能正常 enroll 与 heartbeat。

### Phase 2

- [ ] `baseServiceErrorTable` 至少覆盖 §6 表中所有 26 条 sentinel。
- [ ] 每个 `writeXxxError` 函数体 ≤ 8 行（仅 override + 一次 `WriteServiceErrorWith` 调用）。
- [ ] `writeMappedServiceError` 函数已删除，其规则合入 `baseServiceErrorTable`。
- [ ] 每个 handler test 至少 1 条用例显式断言 base 表 default code（针对 `ErrForbidden` 或 `ErrNotFound`）。
- [ ] `make test web-test openapi-check` 全绿。
- [ ] 浏览器手动验证错误场景的 code 与 §6 表一致。

## 12. Follow-up（本次不做）

- 大 service 拆分：`organization_service.go` (666 行) / `resource_metrics_service.go` (614) / `runtime_node_service.go` (594) / `onboarding_service.go` (单方法 150)。独立 spec。
- 常量提取：`5s` cleanup timeout、`90s` heartbeat grace、`30d` retention、`7d` resource window 等散在 service 的 magic number。可单独小 PR 做，与本次无依赖。
- 仓库根 `agent` 11.1MB 构建产物加入 `.gitignore`。
- agent endpoints（enrollment_secret / agent_token）的认证统一化为 middleware 化。
- 前端 code 驱动的 UX 分支（NO_NODE_AVAILABLE CTA、SESSION_EXPIRED 自动跳登录等）按业务诉求逐个加。
- 限流 / 审计中间件按 `RequireUserAuth` 后挂载点扩展。
