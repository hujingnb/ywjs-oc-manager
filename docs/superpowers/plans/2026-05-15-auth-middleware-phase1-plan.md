# 认证中间件与统一错误响应 — Phase 1 实施计划

> **For agentic workers:** Use `superpowers:executing-plans` 来按任务推进。每个步骤都用 checkbox 跟踪。

**Goal:** 按 `docs/superpowers/specs/2026-05-15-auth-middleware-and-unified-errors-design.md` 完成 phase 1：把 `RequireUserAuth` 中间件挂上、把 `ErrorResponse` 切到 `{Code, Message}` 双字段、前端字段同步切。phase 2（base 表收口）在另一份 plan 里。

**Architecture:** 直接在 `master` 分支工作，按 5 个 commit 顺序推进。前 3 个 commit 是"加新能力但不接线"（apierror / auth context / middleware 各自独立可编译可测），第 4 个 commit 是"接线"（21 个 handler 改造 + router 分组 + cmd/server 装配同步），第 5 个 commit 是"换契约"（ErrorResponse 字段直切 + openapi 同步 + 前端跟改）。每个 commit 自身可编译可测，最后一个 commit 完整跑浏览器验证。

**Tech Stack:** Go 1.25 + Gin + sqlc；前端 Vue 3 + TypeScript；测试用 testify + Vitest；openapi/类型生成用 `make openapi-gen` / `make web-types-gen`。

**约束（全任务通用）：**

- 直接在 `master` 分支工作，不创建 worktree。
- 不动 service 层 sentinel 与业务语义；不改路由 URL / 请求字段名。
- 不在 phase 1 引入 base 表收口（那是 phase 2）。
- 不在 phase 1 引入前端 code 驱动的 UX 分支。
- handler test 改造时**删掉「缺 token / token 无效」用例**，统一收口到 `middleware/auth_test.go`，避免重复测试。
- commit message 用 Conventional Commits + 中文摘要 + 中文正文（参考项目 AGENTS.md）。

---

## Commit 1：新增 `apierror` 包统一错误响应结构

**目标：** 独立可编译的新包，本 commit 不影响现有任何代码路径。

**变更范围：**
- 新建 `internal/api/apierror/response.go`
- 新建 `internal/api/apierror/response_test.go`

**步骤：**

- [ ] 1.1 新建 `internal/api/apierror/response.go`，定义 `ErrorResponse{Code, Message string}` 与 `New(code, message string) ErrorResponse` 构造器。结构体字段必须有 swag-compatible `json` tag 和 `example` tag。给类型、字段、构造器都加中文注释（按 AGENTS.md 注释规约）。
- [ ] 1.2 新建 `internal/api/apierror/response_test.go`，覆盖：
  - `TestNew_FieldsMatch`：`New("X","Y").Code == "X"`、`.Message == "Y"`。
  - `TestNew_Marshal`：`json.Marshal` 输出形如 `{"code":"X","message":"Y"}`。
- [ ] 1.3 跑 `go test ./internal/api/apierror/...`，全绿。
- [ ] 1.4 跑 `make vet`，全绿。
- [ ] 1.5 提交 commit。

**Commit message：**

```
feat(api): 新增 apierror 包统一错误响应结构

引入 internal/api/apierror，定义对外契约层面的 ErrorResponse
{Code, Message} 双字段结构，作为后续中间件与 handler 统一返回错误
的基础类型。Code 为 SCREAMING_SNAKE_CASE 机器可读标识，Message
为面向用户展示的中文文案。

本 commit 仅新增包与单元测试，未接入任何调用方。
```

---

## Commit 2：新增 `auth.Principal` context 注入工具

**目标：** 为中间件准备 ctx 读写工具；本 commit 不接入调用方。

**变更范围：**
- 新建 `internal/auth/context.go`
- 新建 `internal/auth/context_test.go`

**步骤：**

- [ ] 2.1 新建 `internal/auth/context.go`，定义：
  - 未导出 `type principalContextKey struct{}` 作为 ctx key。
  - `WithPrincipal(ctx context.Context, p Principal) context.Context`。
  - `PrincipalFromContext(ctx context.Context) (Principal, bool)`。
  - 每个函数加中文注释，说明：为什么用 struct key（避免跨包字符串冲突）、中间件挂载后 `bool=true` 是恒定保证、调用方不需要做防御。
- [ ] 2.2 新建 `internal/auth/context_test.go`，覆盖：
  - `TestPrincipalContextRoundTrip`：注入后取出，三个字段（UserID/OrgID/Role）相等。
  - `TestPrincipalFromContext_Empty`：从 `context.Background()` 取，返回 `(Principal{}, false)`。
  - `TestPrincipalFromContext_WrongType`：人工 `context.WithValue(ctx, principalContextKey{}, "wrong")` 后取，返回 `false`（防类型擦除事故）。
- [ ] 2.3 跑 `go test ./internal/auth/...`，全绿。
- [ ] 2.4 提交 commit。

**Commit message：**

```
feat(auth): 新增 Principal context 注入工具

引入 WithPrincipal / PrincipalFromContext 让中间件把认证主体写入
请求 context，下游 handler 与 service 均通过 context 取用，不再
依赖 gin.Context 局部存储。ctx key 使用未导出 struct 类型，避免
跨包字符串键冲突。

本 commit 仅新增工具与单元测试，未接入中间件。
```

---

## Commit 3：新增 `RequireUserAuth` 中间件

**目标：** 实现中间件本体；本 commit 仍不挂载到任何路由。

**变更范围：**
- 新建 `internal/api/middleware/auth.go`
- 新建 `internal/api/middleware/auth_test.go`

**步骤：**

- [ ] 3.1 新建 `internal/api/middleware/auth.go`，实现：
  - `RequireUserAuth(tokens *auth.TokenManager) gin.HandlerFunc`：解析 `Authorization: Bearer <token>`，校验失败 abort 401，成功则 `auth.WithPrincipal` 后 `c.Request = c.Request.WithContext(ctx)`。
  - 私有 `parseBearer(header string) (token string, ok bool)`。
  - 私有 `abortUnauthenticated(c *gin.Context, msg string)`：返回 `apierror.New("UNAUTHENTICATED", msg)`。
  - 顶部中文注释说明设计取舍（不做角色/资源权限、统一 `UNAUTHENTICATED` 防探测、CSRF 在前 auth 在后的链路位置）。
- [ ] 3.2 新建 `internal/api/middleware/auth_test.go`，覆盖六条用例：
  - `TestRequireUserAuth_MissingHeader`：无 Authorization → 401 `UNAUTHENTICATED` `"缺少访问令牌"`。
  - `TestRequireUserAuth_NonBearerScheme`：`Basic xxx` → 401 `UNAUTHENTICATED` `"缺少访问令牌"`。
  - `TestRequireUserAuth_EmptyBearerToken`：`Bearer ` → 401 `"缺少访问令牌"`。
  - `TestRequireUserAuth_InvalidSignature`：token 篡改 → 401 `"访问令牌无效"`。
  - `TestRequireUserAuth_ExpiredToken`：token 已过期 → 401 `"访问令牌无效"`。
  - `TestRequireUserAuth_HappyPath`：合法 access token → 200，下游 handler 从 ctx 拿到 principal 且字段正确。
  - 每条用例添加中文注释说明覆盖的业务场景与边界条件。
- [ ] 3.3 跑 `go test ./internal/api/middleware/...`，全绿。
- [ ] 3.4 提交 commit。

**Commit message：**

```
feat(api): 新增 RequireUserAuth 中间件

实现统一的用户态认证中间件：解析 Authorization Bearer header、
校验 access token、把 auth.Principal 注入 request context；校验
失败统一返回 401 UNAUTHENTICATED，不区分缺失/格式错/签名错/过期
原因，避免向探测者泄露细节。

本 commit 仅引入中间件本体与六条单元用例（缺 header / 非
Bearer / 空 token / 篡改签名 / 已过期 / 正常注入），尚未挂载
到 router；下一个 commit 完成路由分组与 handler 改造。
```

---

## Commit 4：handler 从 ctx 取 principal、router 分组挂中间件

**目标：** 21 个 handler 改造 + router 分组 + cmd/server 装配同步，构造器签名变化必须原子提交。本 commit 后所有需要登录的接口都走中间件，但**错误响应仍是旧的 `gin.H{"error":...}`**（下一个 commit 才换）。

**变更范围：**
- 改 `internal/api/router.go`（分组 + 挂 middleware）
- 改 `internal/api/handlers/*.go`（21 个 handler 源文件，构造器签名、principal 取法）
- 改 `internal/api/handlers/*_test.go`（21 个 handler test，装配方式 + 删「缺 token」用例）
- 改 `cmd/server/main.go` 或装配处（构造器调用同步）
- 删 `internal/api/handlers/auth.go::bearerToken`（不再有调用方）—— 注意：`auth.go::Login/Refresh` 不通过中间件，但它们也不调 `bearerToken`，所以可以删。
- 新建 `internal/api/handlers/testhelper_test.go`（包内辅助，导出 `withPrincipal(req, p)`）

**步骤：**

- [ ] 4.1 新建 `internal/api/handlers/testhelper_test.go`：
  - 包内 `withPrincipal(req *http.Request, p auth.Principal) *http.Request`：用 `auth.WithPrincipal` 封装并 `req.WithContext` 返回。
  - 加中文注释说明这是给 21 个 handler test 共用的注入辅助。
- [ ] 4.2 改 `internal/api/router.go`：
  - 删 `dep.TokenManager == nil` 的早返；改为：若 nil 则三组都不创建（保留 health 在 public）。
  - 新建三个 `*gin.RouterGroup`：`public := router.Group("")`、`agent := router.Group("")`、`user := router.Group("")`。
  - `user.Use(middleware.RequireUserAuth(dep.TokenManager))`。
  - `agent` 与 `public` 不挂 RequireUserAuth。
  - 把 17 个用户级 `RegisterXxxRoutes(router, ...)` 调用全部改为 `RegisterXxxRoutes(user, ...)`。
  - 把 agent 相关的 `RegisterAgentRoutes(router, ...)` 改为 `RegisterAgentRoutes(agent, ...)`。
  - 把 `RegisterHealthRoutes(router)` 改为 `RegisterHealthRoutes(public)`。
  - 把 `AuthHandler` 拆为 `RegisterPublicAuthRoutes(public, ...)`（login/refresh）和 `RegisterAuthMeRoutes(user, ...)`（logout/me）。**注意**：这一步需要在 `auth.go` handler 文件里把现有 `RegisterAuthRoutes` 拆成两个 Register 函数，对应改造同步在本 commit 内。
  - 每个 handler 的 `Handler` 构造器去掉 `*auth.TokenManager` 入参（除 `AuthHandler`，它仍需 TokenManager 用于签发）。
  - 给三组顶部加中文注释，说明每组的认证语义（public/agent/user）。
- [ ] 4.3 改 21 个 `internal/api/handlers/*.go`（除 `auth.go` 部分逻辑保留 TokenManager）：
  - 每个 handler 函数体里：删 `token, ok := bearerToken(...)` 与 `principal, err := h.tokens.VerifyAccessToken(...)` 两段；改为 `principal, _ := auth.PrincipalFromContext(c.Request.Context())`。
  - 构造器签名删 `tokens *auth.TokenManager`；struct 字段删 `tokens`。
  - **不动 `c.JSON(http.StatusXxx, gin.H{"error": ...})` 调用**——下一个 commit 才换。
  - **不动 `writeXxxError` 函数体**——下一个 commit 才换。
  - 给改动位置加 1 行中文注释说明"principal 由 RequireUserAuth 注入到 ctx"。
- [ ] 4.4 删 `internal/api/handlers/auth.go::bearerToken` 函数（不再有调用方）。如果 `auth.go` 内部还有 logout/me 用到，先迁到从 ctx 取 principal。
- [ ] 4.5 改 21 个 `internal/api/handlers/*_test.go`：
  - 删每个 test 文件中模拟「缺 token / token 无效」的用例（粗估 34 条）；这些场景由 `middleware/auth_test.go` 覆盖。
  - 改装配方式：构造 handler 时去掉 `tokens` 入参；构造 request 时用 `testhelper_test.go::withPrincipal` 注入主体。
  - **不动响应断言**（仍断言 `gin.H{"error":...}`）——下一个 commit 才换。
- [ ] 4.6 改 `cmd/server/main.go` 或装配处：构造器调用对齐新签名（去掉 TokenManager 入参，除 `NewAuthHandler`）。
- [ ] 4.7 跑 `go build ./...`，全绿。
- [ ] 4.8 跑 `make vet`、`make test`，全绿。
- [ ] 4.9 验证：`grep -rn "bearerToken(\|tokens.VerifyAccessToken" internal/api/handlers/ | grep -v _test.go` 返回 0 行（auth.go 的 RefreshToken/SignAccessToken 等不计）。
- [ ] 4.10 验证：`grep -rn "缺少访问令牌\|访问令牌无效" internal/api/handlers/` 返回 0 行。
- [ ] 4.11 提交 commit。

**Commit message：**

```
refactor(api): handler 改为从 ctx 取 principal 并按路由分组挂中间件

把 17 处重复的 bearer token 解析 + VerifyAccessToken 样板下沉到
RequireUserAuth 中间件，handler 一律通过 auth.PrincipalFromContext
从 request context 取认证主体；handler 构造器去掉 TokenManager 入参
（AuthHandler 保留，仍负责签发与撤销）。

router 重组为 public / agent / user 三组：public 挂健康检查与
login/refresh，agent 保留 enrollment_secret / agent_token 自校验
不挂中间件，user 组挂 RequireUserAuth 覆盖其余全部接口。

handler 测试同步改造：删除每个文件里模拟「缺 token / token 无效」
的样例（统一由 middleware/auth_test.go 覆盖），改用包内
testhelper_test.go::withPrincipal 注入主体。本 commit 不动错误响
应字段，仍是 gin.H{"error":...}；下一个 commit 切到 apierror.
ErrorResponse。
```

---

## Commit 5：ErrorResponse 切到 {Code, Message} 双字段（含 openapi 与前端）

**目标：** 完成 phase 1 最后一步：错误响应契约切换 + openapi 同步 + 前端跟改。本 commit 后前端拿到的错误体一律是 `{code, message}`。

**变更范围：**
- 改 `internal/api/handlers/dto.go`（删旧 `ErrorResponse`）
- 改 21 个 `internal/api/handlers/*.go`（所有 `c.JSON(status, gin.H{"error": ...})` → `c.JSON(status, apierror.New(code, msg))`）
- 改 21 个 `internal/api/handlers/*_test.go`（响应断言改为对 `apierror.ErrorResponse{Code, Message}` 反序列化）
- 改 `internal/api/handlers/members.go::NO_NODE_AVAILABLE`（去掉单独发明的 `{code,message}` 风格，统一走 `apierror.New`）
- 跑 `make openapi-gen`，提交 `openapi/openapi.yaml`
- 跑 `make web-types-gen`，提交 `web/src/api/generated.ts`
- 改 `web/src/api/client.ts`（解析 `code` 与 `message`）
- 改 `web/src/api/index.ts` 或 `ApiError` 定义处（新增 `code` 字段）
- 改前端读 `.error` 的位置（编译错误处）

**步骤：**

- [ ] 5.1 改 `internal/api/handlers/dto.go`：删除旧 `ErrorResponse{Error string}` 类型定义。
- [ ] 5.2 在 21 个 handler 源文件中，把所有 `c.JSON(http.StatusXxx, gin.H{"error": "..."})` 替换为 `c.JSON(http.StatusXxx, apierror.New("CODE", "..."))`，code 用 inline 字符串（phase 2 才迁到 base 表）。code 命名严格按 spec §6 表格——例如：
  - `auth.go::writeAuthError` 中 `ErrInvalidCredentials` → `INVALID_CREDENTIALS`；`ErrInvalidToken` → `INVALID_TOKEN`；disabled 分支 → `USER_DISABLED` / `ORG_DISABLED`。
  - `app_runtime.go::writeAppRuntimeError` 中 `ErrForbidden / ErrRuntimeOperationDenied` 分支用 `RUNTIME_OP_FORBIDDEN`；`ErrNotFound` → `APP_NOT_FOUND`；`ErrAppNotReinitializable` → `APP_NOT_REINIT`。
  - 完整命名映射见 spec §6 表；本 commit 必须 grep-able 地达到 §6 全集。
- [ ] 5.3 改 `members.go::NO_NODE_AVAILABLE` 那段：用 `apierror.New("NO_NODE_AVAILABLE", "暂无可用 Runtime Node...")` 一行替换原 `gin.H{"code":..., "message":...}` 块。
- [ ] 5.4 验证：`grep -rn 'gin.H{"error"' internal/api/handlers/` 返回 0 行。
- [ ] 5.5 验证：`grep -rn '"code":' internal/api/handlers/ | grep -v _test.go` 返回 0 行（除生成代码外，code 必须通过 apierror.New 出口）。
- [ ] 5.6 改 21 个 handler test 文件：
  - 响应断言改为对 `apierror.ErrorResponse{Code, Message}` 反序列化（用 `json.Unmarshal` 后断言两个字段）。
  - 至少每个 handler 的 1 条主错误用例显式断言 `Code` 字段值，以防 phase 2 时回归。
  - 每条改动用例加中文注释说明覆盖场景。
- [ ] 5.7 跑 `make test`，全绿。
- [ ] 5.8 跑 `make openapi-gen`，确认 `openapi/openapi.yaml` 中 `ErrorResponse` schema 变为 `{code, message}` 两字段必填，git diff 检查无意外字段动。
- [ ] 5.9 跑 `make web-types-gen`，确认 `web/src/api/generated.ts` 中 `ErrorResponse` 类型同步。
- [ ] 5.10 改 `web/src/api/client.ts`：
  - 响应非 2xx 时把 body 解析为 `{code, message}`，构造 `ApiError` 时把 `code` 传入。
  - 删除 / 改造任何依赖旧 `body.error` 的代码。
- [ ] 5.11 改 `ApiError` 类（在 `client.ts` 或 `index.ts` 内）：增加 `code: string` 字段；默认值 `'UNKNOWN'` 用于网络层错误。
- [ ] 5.12 跑 `make web-typecheck`，根据编译错误清单逐个修复读 `.error` 的位置——改为 `.message`，并按需读 `.code` 做兜底分支。
- [ ] 5.13 跑 `make web-test`、`make web-build`，全绿。
- [ ] 5.14 验证：`grep -rn "\.error" web/src/ --include='*.ts' --include='*.vue' | grep -v "// " | grep -i "ApiError\|response"`，检查没有遗漏的 `.error` 读取（人工判断）。
- [ ] 5.15 跑 `make openapi-check`，确认工作区干净（说明 yaml 与代码同步）。
- [ ] 5.16 浏览器手动验证（必须，按 AGENTS.md 规约）：
  - 启动 `make dev-up`。
  - 平台管理员账号（`admin` / `admin123`）登录、列出组织、创建一个组织、退出。
  - 组织管理员账号（`test-org` / `test-org` / `test-org123`）登录、列出成员、创建一个成员、给成员创建一个应用、查看 runtime 节点、退出。
  - 浏览器 devtools network tab 检查：所有 4xx/5xx 响应体都是 `{code, message}`；至少触发一次错误场景（如删不存在的资源），确认前端提示展示 `message` 字段、`code` 在 console 可见。
  - 任意接口故意带过期 token，确认 401 + `code=UNAUTHENTICATED`。
- [ ] 5.17 跑 `make smoke-v102`，runtime-agent enroll + heartbeat 必须通过（验证 agent 路由没被 middleware 误挡）。
- [ ] 5.18 提交 commit。

**Commit message：**

```
refactor(api): ErrorResponse 切到 {Code, Message} 双字段并同步前端

把 ErrorResponse 从 {Error string} 直接换为 {Code, Message}：
Code 为 SCREAMING_SNAKE_CASE 机器可读标识（命名表见 spec §6），
Message 为面向用户的中文文案。21 个 handler 中所有
gin.H{"error":...} 调用替换为 apierror.New(code, message)；
members.go 中单独发明的 {code, message} 响应归一回标准路径。

openapi/openapi.yaml 与 web/src/api/generated.ts 跟随重新生成；
前端 web/src/api/client.ts 与 ApiError 类同步切换字段，编译期
保证所有 .error 读取位置都已迁到 .message。

这是 phase 1 的破坏性变更收尾；phase 2 将引入 base 表把 13 个
writeXxxError 函数薄化为 override-only 入口。
```

---

## 收尾验证（所有 commit 完成后）

- [ ] V.1 `grep -rn "bearerToken(\|tokens\.VerifyAccessToken" internal/api/handlers/ | grep -v _test.go` 返回 0 行（auth 包内部签发函数不计）。
- [ ] V.2 `grep -rn "缺少访问令牌\|访问令牌无效" internal/api/handlers/` 返回 0 行。
- [ ] V.3 `grep -rn 'gin.H{"error"' internal/api/handlers/` 返回 0 行。
- [ ] V.4 `grep -rn "type ErrorResponse" internal/api/handlers/` 返回 0 行（已移到 apierror 包）。
- [ ] V.5 `make test web-test openapi-check vet` 全绿。
- [ ] V.6 浏览器手动验证 7 条主路径全通（见 5.16）。
- [ ] V.7 `make smoke-v102` 通过。
- [ ] V.8 `git log --oneline master ^origin/master` 输出 5 个 commit，顺序与本 plan 一致。

## 风险点（实施时重点防）

- **Commit 4 的 diff 大**：21 个 handler + 21 个 test + router + 装配，建议先在 1-2 个 handler 上跑通完整改造（包含构造器、test 装配）再批量铺开，避免一次性改完才发现 testhelper 设计有问题导致整批回退。
- **agent 路由分组**：实施 commit 4 时优先验证 agent enroll / heartbeat 仍能通；不要等到 5.17 smoke 才发现 agent 被中间件拦截。建议在 commit 4 完成后立即跑一次本地 agent enroll（`make smoke-v102` 的子集）。
- **commit 5 前端字段切**：`make web-typecheck` 是审计入口；如果 TypeScript 给出大量 `.error` 错误，每一处都要判断是改读 `.message` 还是删除（有些可能是历史死代码）。不要无脑批量 sed。
- **openapi diff 范围**：`make openapi-gen` 跑完如果 yaml 改动超出 `ErrorResponse` schema + handler `@Failure` 引用范围，说明 swag 扫描出了意外，停下来排查；不要直接 commit。
- **回滚成本**：commit 4 一旦合入，回滚需要同时回退 21 个 handler 与 router 改造，成本极高。所以 commit 4 必须在本地完整通过 `make test` 与至少一次 dev 环境登录验证（不一定要等到 commit 5 的 7 条主路径），再 push。
