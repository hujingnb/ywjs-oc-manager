# 实例 locale 赋值/传播/实时展示 实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 实例语言跟随其所属成员的界面语言；实例详情页实时（查 oc-ops、不读 DB 快照）展示实例当前真正在用的语言；UI 语言变更以「更新 apps.locale 不重启 + 详情页手动重启生效」方式传播。

**Architecture:** Go 后端：新成员 locale 随创建者、UI 语言变更传播到 owner 实例的 apps.locale（不重启）、新增 `GET /apps/:id/locale-status`（current 实时查 oc-ops、desired 来自 DB、needs_restart 比较）。Python oc-ops：新增 `/oc/config` 暴露 live `display.language`。Vue：实例详情加"实例语言"行 + needs_restart 重启入口。

**Tech Stack:** Go（gin、sqlc、testify）、Python（oc-ops aiohttp/http server）、Vue3+TS（vue-i18n、naive-ui）、swag + openapi-gen + web-types-gen。

**设计依据：** `docs/superpowers/specs/2026-06-24-instance-locale-propagation-design.md`

---

## 前置事实（已核对真实代码）

- `internal/service/auth_service.go:348-360`：`UpdateLocale(ctx, userID, locale)` 仅 `store.UpdateUserLocale`，无传播。`SupportedLocales`/`isSupportedLocale` 同文件。
- `internal/service/onboarding_service.go`：`OnboardMember(ctx, principal auth.Principal, orgID, input)`（L105）；tx 内 `store.CreateUser`（L155，**无 locale**）、`store.CreateApp`（L168，`Locale: null.StringFrom(s.defaultLocale)` L177）。`CreateAppForMember`（L291，L350 `appLocale := s.defaultLocale` 再按成员 user.Locale 覆盖）。`s.defaultLocale` 来自 `cmd/server/main.go:143`。
- `OnboardingStore` 接口已含 `GetUser(ctx, id) (sqlc.User, error)`、`GetActiveAppByOwner(ctx, ownerUserID) (sqlc.App, error)`、`CreateUser`、`CreateApp`。
- `internal/store/sqlc`：`GetUser(ctx,id)`、`UpdateUserLocale`、`UpdateAppLocale(ctx, UpdateAppLocaleParams)`（仅改列）、`GetActiveAppByOwner` 均存在。`CreateUserParams` **无 Locale 字段**（users 表有 locale 列，migration 000013）。`CreateUser` 的 SQL 源：`internal/store/queries/users.sql`（`-- name: CreateUser :exec`）。
- `internal/integrations/ocops/`：`client_channel.go` 有 `Info/Doctor/ChannelStatus`，统一 `c.DoJSON(ctx, ep, method, path, body, &out)`，`Endpoint` 寻址 `app-<id>-ocops:8080` + per-app token。**无 Config 方法**。
- oc-ops `runtime/hermes/hermes-v2026.6.5/ocops/server.py`：有 `/oc/info`、`/oc/doctor` 等，**无 `/oc/config`**。`config.yaml` 在 `/opt/data/config.yaml`，含 `display.language`。
- `web/src/pages/apps/AppOverviewTab.vue`：无语言字段；`web/src/stores/locale.ts:49` `setLocale` 调 PATCH `/api/v1/auth/me/locale`。
- 前端类型由 `make web-types-gen` 从 `openapi/openapi.yaml`（`make openapi-gen` 由 swag 注解生成）生成 `web/src/api/generated.ts`，二者入 git，需保持同步（见 AGENTS.md）。

## 文件结构

| 文件 | 动作 | 职责 |
|---|---|---|
| `internal/store/queries/users.sql` | 修改 | CreateUser INSERT 增 locale 列 |
| `internal/store/sqlc/users.sql.go` | 重生成 | CreateUserParams 增 Locale（sqlc 产物） |
| `internal/service/onboarding_service.go` | 修改 | OnboardMember 用创建者 locale 设 member+app |
| `internal/service/auth_service.go` | 修改 | UpdateLocale 传播到 owner 活跃实例 apps.locale（不重启） |
| `runtime/hermes/hermes-v2026.6.5/ocops/server.py` | 修改 | 新增 `/oc/config` 返回 display_language |
| `runtime/hermes/hermes-v2026.6.5/tests/test_ocops_*.py` | 新增/改 | /oc/config 端点测试 |
| `internal/integrations/ocops/client_channel.go`（或新 client_config.go） | 修改/新增 | `Config(ctx, ep)` + `OcConfig` 类型 |
| `internal/service/app_service.go` | 修改 | `AppLocaleStatus(ctx, principal, appID)` |
| `internal/api/handlers/apps.go` | 修改 | `GET /api/v1/apps/:appId/locale-status` handler + swag |
| `internal/api/handlers/dto.go` | 修改 | `AppLocaleStatusResponse` DTO |
| `openapi/openapi.yaml` + `web/src/api/generated.ts` | 重生成 | openapi-gen + web-types-gen 同步 |
| `web/src/pages/apps/AppOverviewTab.vue` | 修改 | 实例语言行 + needs_restart + 重启 |
| `web/src/i18n/locales/{en,zh}/*.ts` | 修改 | 语言行/状态文案 |

## 命令
- Go：`go test ./...`、`go build ./...`、`go vet ./...`。
- sqlc 重生成：`make sqlc-generate`（看 Makefile 实际 target）。
- openapi：`make openapi-gen` + `make web-types-gen`；`make openapi-check`（跑 openapi-gen 后工作区应干净）。
- 前端：`make web-typecheck`、`make web-test`（若有）。
- oc-ops 测试：`cd runtime/hermes/hermes-v2026.6.5 && python3 -m pytest tests/`。

---

## Task 1：CreateUser 支持 locale（sqlc）

**Files:** Modify `internal/store/queries/users.sql`；重生成 `internal/store/sqlc/users.sql.go`

- [ ] **Step 1: 改 CreateUser SQL 加 locale 列**
打开 `internal/store/queries/users.sql`，找到 `-- name: CreateUser :exec` 的 INSERT，把列与占位符加上 `locale`。例如（按实际列顺序调整）：
```sql
-- name: CreateUser :exec
INSERT INTO users (id, org_id, username, password_hash, display_name, role, status, locale)
VALUES (?, ?, ?, ?, ?, ?, ?, ?);
```
（locale 为可空，传 null.String。）

- [ ] **Step 2: 重生成 sqlc**
Run: `make sqlc-generate`（先 `grep -n sqlc Makefile` 确认 target 名）
Expected: `internal/store/sqlc/users.sql.go` 的 `CreateUserParams` 出现 `Locale null.String` 字段、`CreateUser` 绑定该列。

- [ ] **Step 3: 编译确认**
Run: `go build ./...`
Expected: 成功（OnboardMember 现有 CreateUser 调用未传 Locale 仍可编译——零值 null.String 即 NULL，行为不变）。

- [ ] **Step 4: 提交**
```bash
git add internal/store/queries/users.sql internal/store/sqlc/users.sql.go
git commit -m "feat(store): CreateUser 支持 locale 列

为新成员创建时写入界面语言偏好做准备;sqlc 重生成 CreateUserParams 增 Locale 字段。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 2：OnboardMember 新成员 locale 随创建者

**Files:** Modify `internal/service/onboarding_service.go`；Test `internal/service/onboarding_service_test.go`

- [ ] **Step 1: 写失败测试**
在 onboarding 测试中加用例：创建者（principal.UserID）的 users.locale 为 "zh" 时，OnboardMember 建的新成员 users.locale 与新实例 apps.locale 都为 "zh"；创建者 locale 为空时回落 `s.defaultLocale`。用现有测试的 mock/fake OnboardingStore（按现有测试风格），断言传入 CreateUser 的 `Locale` 与 CreateApp 的 `Locale`。
```go
// 场景：创建者 locale=zh → 新成员与新实例 locale 均为 zh
func TestOnboardMemberInheritsCreatorLocale(t *testing.T) {
	// ... 构造 fake store：GetUser(creatorID) 返回 {Locale: null.StringFrom("zh")}
	// 调 OnboardMember(ctx, principal{UserID: creatorID}, orgID, input)
	// require.NoError；assert 捕获的 CreateUserParams.Locale == null.StringFrom("zh")
	//   且 CreateAppParams.Locale == null.StringFrom("zh")
}

// 场景：创建者 locale 为空 → 回落平台默认
func TestOnboardMemberFallsBackToDefaultLocaleWhenCreatorEmpty(t *testing.T) {
	// GetUser 返回 {Locale: null.String{}}；defaultLocale="en"
	// 断言 CreateUser/CreateApp 的 Locale == null.StringFrom("en")
}
```
（按 onboarding_service_test.go 现有 fake store 结构补 GetUser 返回值与对 CreateUser/CreateApp 入参的捕获；若现有 fake 未捕获 CreateUser 入参，扩展之。）

- [ ] **Step 2: 跑确认失败**
Run: `go test ./internal/service/ -run TestOnboardMemberInheritsCreatorLocale -v`
Expected: FAIL（当前 CreateUser 不设 locale、CreateApp 用 defaultLocale）。

- [ ] **Step 3: 实现**
在 `OnboardMember` 的 tx 闭包内、`CreateUser` 之前读创建者 locale 并复用：
```go
		// 新成员语言随创建者：读操作者(创建该成员的管理员)的 locale，缺省回落平台默认。
		memberLocale := s.defaultLocale
		if creator, err := store.GetUser(ctx, principal.UserID); err == nil && creator.Locale.Valid && creator.Locale.String != "" {
			memberLocale = creator.Locale.String
		}
```
`CreateUser` 入参加 `Locale: null.StringFrom(memberLocale)`；`CreateApp` 的 `Locale` 从 `null.StringFrom(s.defaultLocale)` 改为 `null.StringFrom(memberLocale)`。更新 L167 注释为「随创建者 locale」。

- [ ] **Step 4: 跑确认通过**
Run: `go test ./internal/service/ -run 'TestOnboardMember' -v`
Expected: PASS。

- [ ] **Step 5: 提交**
```bash
git add internal/service/onboarding_service.go internal/service/onboarding_service_test.go
git commit -m "feat(onboarding): 新成员与其实例 locale 随创建者

OnboardMember 读创建者(操作 principal)的 users.locale 设为新成员 locale 与新实例
apps.locale,缺省回落平台默认;CreateAppForMember 已随成员 locale 不变。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 3：UpdateLocale 传播到 owner 实例（不重启）

**Files:** Modify `internal/service/auth_service.go`；Test `internal/service/auth_service_test.go`

- [ ] **Step 1: 写失败测试**
```go
// 场景：用户改界面语言后,其 owner 活跃实例的 apps.locale 同步更新,且不入队重启 job
func TestUpdateLocalePropagatesToOwnerApp(t *testing.T) {
	// fake store：UpdateUserLocale 记录;GetActiveAppByOwner(userID) 返回 {ID:"app1"};
	//   UpdateAppLocale 捕获入参;断言无 CreateJob/restart 调用
	// 调 UpdateLocale(ctx, "user1", "en")
	// require.NoError;assert UpdateAppLocale 收到 {ID:"app1", Locale: null.StringFrom("en")}
}

// 场景：用户无活跃实例(GetActiveAppByOwner 返回 ErrNoRows)→ 不报错,只更新 users.locale
func TestUpdateLocaleNoAppIsOK(t *testing.T) {
	// GetActiveAppByOwner 返回 sql.ErrNoRows;调用应成功且不调 UpdateAppLocale
}
```
（按 auth_service_test.go 现有 fake/mock store 风格；AuthService 的 store 需能 GetActiveAppByOwner + UpdateAppLocale——确认 AuthService.store 接口含这两个方法，缺则扩展其接口。）

- [ ] **Step 2: 跑确认失败**
Run: `go test ./internal/service/ -run TestUpdateLocalePropagates -v`
Expected: FAIL（当前无传播）。

- [ ] **Step 3: 实现**
`UpdateLocale` 在 `UpdateUserLocale` 成功后追加传播（同事务最佳；若 AuthService 无事务封装则顺序调用 + 失败记日志不阻断）：
```go
	// 传播：把该用户拥有的活跃实例的 apps.locale 同步为新语言（不重启，详情页提供手动重启入口）。
	app, err := s.store.GetActiveAppByOwner(ctx, userID)
	if err == nil {
		if err := s.store.UpdateAppLocale(ctx, sqlc.UpdateAppLocaleParams{ID: app.ID, Locale: null.StringFrom(locale)}); err != nil {
			return fmt.Errorf("传播实例语言失败: %w", err)
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("查询用户实例失败: %w", err)
	}
	return nil
```
（确认 `UpdateAppLocaleParams` 字段名 ID/Locale 与 sqlc 一致；若 AuthService.store 接口未声明 GetActiveAppByOwner/UpdateAppLocale，在其 store 接口加这两方法。import errors / database/sql。）

- [ ] **Step 4: 跑确认通过**
Run: `go test ./internal/service/ -run 'TestUpdateLocale' -v`
Expected: PASS。

- [ ] **Step 5: 提交**
```bash
git add internal/service/auth_service.go internal/service/auth_service_test.go
git commit -m "feat(auth): 改界面语言时传播到 owner 实例 apps.locale(不重启)

UpdateLocale 更新 users.locale 后同步更新该用户活跃实例的 apps.locale,不入队重启;
无实例时静默跳过。重启生效由实例详情页手动触发。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 4：oc-ops `/oc/config` 端点

**Files:** Modify `runtime/hermes/hermes-v2026.6.5/ocops/server.py`；Test `runtime/hermes/hermes-v2026.6.5/tests/`

- [ ] **Step 1: 读现有端点风格**
Run: `grep -n 'def \|/oc/info\|/oc/doctor\|routes\|add_get\|self\.path ==' runtime/hermes/hermes-v2026.6.5/ocops/server.py | head -40`
看清路由注册方式（aiohttp router 还是 BaseHTTPRequestHandler path 分发）与 `/oc/info` 处理函数怎么写响应 JSON、怎么读文件。

- [ ] **Step 2: 写失败测试**
在 tests/ 下（参考 test_ocops_info_doctor.py）加 `/oc/config` 测试：给定一个含 `display:\n  language: zh` 的临时 config.yaml（指向 oc-ops 读取路径），GET /oc/config 返回 `{"display_language":"zh"}`；config 缺 display.language 时返回默认（"en"）或合理空值。按现有 ocops server 测试夹具（conftest）构造。

- [ ] **Step 3: 跑确认失败**
Run: `cd runtime/hermes/hermes-v2026.6.5 && python3 -m pytest tests/ -k config -v`
Expected: FAIL（端点不存在 → 404）。

- [ ] **Step 4: 实现 /oc/config**
仿 `/oc/info` 加 `GET /oc/config`：读 `/opt/data/config.yaml`（用 server 现有的 config 路径常量/读法），解析 `display.language`，返回 `{"display_language": <值或 "en">}`。需 Bearer token 鉴权（同其它 /oc/* 端点）。中文注释说明用途（manager 实时查实例当前语言）。

- [ ] **Step 5: 跑确认通过**
Run: `cd runtime/hermes/hermes-v2026.6.5 && python3 -m pytest tests/ -k config -v`
Expected: PASS。同步 ocops-contract（若该端点需登记契约 schema，按 contract 目录现有格式补；否则跳过）。

- [ ] **Step 6: 提交**
```bash
git add runtime/hermes/hermes-v2026.6.5/ocops/server.py runtime/hermes/hermes-v2026.6.5/tests/
git commit -m "feat(ocops): 新增 /oc/config 暴露实例当前 display.language

供 manager 实时查实例真正在用的语言(不读 DB 快照);读 /opt/data/config.yaml 返回
{display_language}。新 variant 复制时需带走。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 5：ocops client Config 方法

**Files:** Create `internal/integrations/ocops/client_config.go`；Test `internal/integrations/ocops/client_config_test.go`

- [ ] **Step 1: 写失败测试**
参考现有 ocops client 测试（mock HTTP server 返回 JSON），断言 `Config(ctx, ep)` 调 `GET /oc/config` 并解析出 `DisplayLanguage`。
```go
func TestClientConfigParsesDisplayLanguage(t *testing.T) {
	// 起 httptest server，对 /oc/config 返回 {"display_language":"zh"}
	// 构造 Client + Endpoint 指向它；调 Config(ctx, ep)
	// require.NoError；assert out.DisplayLanguage == "zh"
}
```

- [ ] **Step 2: 跑确认失败**
Run: `go test ./internal/integrations/ocops/ -run TestClientConfig -v`
Expected: FAIL（Config/OcConfig 未定义）。

- [ ] **Step 3: 实现**
```go
// client_config.go — ocops 实例运行配置查询。
package ocops

import (
	"context"
	"net/http"
)

// OcConfig 是 /oc/config 的响应：实例当前运行配置中与 manager 相关的字段。
type OcConfig struct {
	DisplayLanguage string `json:"display_language"`
}

// Config 查询实例当前运行的 display.language（实时，不依赖 manager DB 快照）。
// GET /oc/config
func (c *Client) Config(ctx context.Context, ep Endpoint) (OcConfig, error) {
	var out OcConfig
	err := c.DoJSON(ctx, ep, http.MethodGet, "/oc/config", nil, &out)
	return out, err
}
```

- [ ] **Step 4: 跑确认通过**
Run: `go test ./internal/integrations/ocops/ -run TestClientConfig -v`
Expected: PASS。

- [ ] **Step 5: 提交**
```bash
git add internal/integrations/ocops/client_config.go internal/integrations/ocops/client_config_test.go
git commit -m "feat(ocops-client): 新增 Config 方法查 /oc/config 当前语言

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 6：AppLocaleStatus service + locale-status 端点 + openapi 同步

**Files:** Modify `internal/service/app_service.go`、`internal/api/handlers/apps.go`、`internal/api/handlers/dto.go`；重生成 openapi/generated.ts；Test 对应 service/handler test

- [ ] **Step 1: 写 service 失败测试**
```go
// 场景：实例运行中,oc-ops 返回 current=zh,apps.locale(desired)=en → needs_restart=true
func TestAppLocaleStatusNeedsRestart(t *testing.T) {
	// fake：GetApp 返回 {Locale: null.StringFrom("en")};ocops.Config 返回 {DisplayLanguage:"zh"}
	// 断言 current=="zh"、desired=="en"、needsRestart==true
}
// 场景：oc-ops 不可达 → current=nil、needs_restart=false、desired 仍返回
func TestAppLocaleStatusInstanceUnreachable(t *testing.T) {
	// ocops.Config 返回 error → current 为 nil(空指针)、needsRestart=false
}
```
（按 app_service 现有测试的 fake ocops/channelOps 与 store 风格；为 AppService 注入的 ocops 接口加 Config 方法 mock。）

- [ ] **Step 2: 跑确认失败**
Run: `go test ./internal/service/ -run TestAppLocaleStatus -v`
Expected: FAIL（方法未定义）。

- [ ] **Step 3: 实现 service**
在 app_service.go 加（结构按现有 AppService 依赖：store 取 apps.locale + 鉴权、ocops 客户端取 current）：
```go
// AppLocaleStatusResult 是实例语言状态：current 为实时实例语言(实例不可达时为 nil),
// desired 为期望(apps.locale)配置值,needsRestart 表示运行中实例当前语言与期望不一致需重启生效。
type AppLocaleStatusResult struct {
	CurrentLanguage *string
	DesiredLanguage string
	NeedsRestart    bool
}

// AppLocaleStatus 返回实例语言状态;current 实时查 oc-ops(铁律:不读 DB 快照),
// desired 取 apps.locale,实例不可达时 current=nil、needsRestart=false。
func (s *AppService) AppLocaleStatus(ctx context.Context, principal auth.Principal, appID string) (AppLocaleStatusResult, error) {
	app, err := s.store.GetApp(ctx, appID)
	if err != nil { /* 映射 ErrNotFound */ }
	if !s.authz.CanAccessApp(principal, app) { return AppLocaleStatusResult{}, ErrForbidden } // 按现有鉴权谓词
	res := AppLocaleStatusResult{DesiredLanguage: app.Locale.String}
	if cfg, err := s.ocops.Config(ctx, s.endpointFor(app)); err == nil && cfg.DisplayLanguage != "" {
		cur := cfg.DisplayLanguage
		res.CurrentLanguage = &cur
		res.NeedsRestart = cur != res.DesiredLanguage
	}
	return res, nil
}
```
（实际依赖名/鉴权谓词/endpoint 构造按 app_service.go 现状对齐：复用其调 oc-ops 的现有路径与 authorizer。ocops 调用用**短超时** context，避免实例未运行时详情页卡顿。）

- [ ] **Step 4: DTO + handler + swag**
`dto.go` 加：
```go
// AppLocaleStatusResponse 是 GET /apps/:appId/locale-status 的响应。
type AppLocaleStatusResponse struct {
	CurrentLanguage *string `json:"current_language"` // 实例实时语言;未运行/不可达为 null
	DesiredLanguage string  `json:"desired_language"` // 期望语言(apps.locale)
	NeedsRestart    bool    `json:"needs_restart"`    // 运行中实例当前语言≠期望,需重启生效
}
```
`apps.go` 注册 `router.GET("/api/v1/apps/:appId/locale-status", handler.LocaleStatus)`，handler 调 service、按现有 handler 错误风格用 `apierror.JSON`（Spec B 的 msgKey）、成功 `c.JSON(200, AppLocaleStatusResponse{...})`。加 swag 注解（@Summary/@Tags/@Produce/@Success 200 {object} handlers.AppLocaleStatusResponse/@Router）。

- [ ] **Step 5: 跑 service/handler 测试**
Run: `go test ./internal/service/ ./internal/api/... -run 'AppLocaleStatus|LocaleStatus' -v`
Expected: PASS。

- [ ] **Step 6: 同步 openapi + 前端类型**
Run: `make openapi-gen && make web-types-gen`
Expected: `openapi/openapi.yaml` 含新端点、`web/src/api/generated.ts` 含 AppLocaleStatusResponse 类型。`make openapi-check` 干净。

- [ ] **Step 7: 提交**
```bash
git add internal/service/app_service.go internal/api/handlers/apps.go internal/api/handlers/dto.go \
        internal/service/*_test.go internal/api/handlers/*_test.go openapi/openapi.yaml web/src/api/generated.ts
git commit -m "feat(api): 新增 GET /apps/:id/locale-status 实时实例语言状态

current 实时查 oc-ops(不读 DB)、desired 取 apps.locale、needs_restart 比较;实例
不可达 current 为 null。同步 openapi 与前端生成类型。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 7：前端实例详情"实例语言"行 + 重启入口

**Files:** Modify `web/src/pages/apps/AppOverviewTab.vue`、`web/src/i18n/locales/{en,zh}/`（apps 相关文件）

- [ ] **Step 1: i18n 文案**
在 apps locale 文件加 key（en/zh 对齐）：`apps.overview.language.label`（"实例语言"/"Instance language"）、`.notRunning`（"实例未运行"/"Instance not running"）、`.needsRestart`（"切换语言后需重启生效为 {lang}"/"Restart required to apply {lang}"）、`.restart`（"重启应用"/"Restart to apply"）；语言名 `common.locale.zh`/`.en`（"中文"/"English"）若无则加。

- [ ] **Step 2: 加"实例语言"行**
在 AppOverviewTab.vue 用生成的 typed client 调 `GET /api/v1/apps/:appId/locale-status`（参考该页其它 query 写法/useQuery）。渲染：
- `current_language` 有值 → 显示 `t('common.locale.'+current)`。
- `current_language` 为 null → 显示 `t('apps.overview.language.notRunning')`。
- `needs_restart` 为 true → 显示提示 `t('apps.overview.language.needsRestart', { lang: t('common.locale.'+desired) })` + 「重启应用」按钮：复用该实例现有的重启调用（运行时 tab 的 restart 用的同一 API/mutation），点击后重启并刷新本状态。

- [ ] **Step 3: 校验**
Run: `make web-typecheck`（及 `make web-test` 若有）
Expected: 通过，无类型错误（generated.ts 已含响应类型）。

- [ ] **Step 4: 提交**
```bash
git add web/src/pages/apps/AppOverviewTab.vue web/src/i18n/locales/
git commit -m "feat(web): 实例详情展示当前语言(实时)+需重启提示与重启入口

调 /apps/:id/locale-status 实时显示实例当前语言;未运行显示提示;当前≠期望时显示
需重启徽标与重启按钮(复用现有重启)。文案走 vue-i18n。

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 8：全量校验

- [ ] **Step 1:** `go build ./... && go vet ./... && go test ./...` 全绿。
- [ ] **Step 2:** `make openapi-check` 干净（openapi 与代码同步）。
- [ ] **Step 3:** `make web-typecheck` 通过；`cd runtime/hermes/hermes-v2026.6.5 && python3 -m pytest tests/` 全绿。

---

## Task 9：真机三角色验证（CLAUDE.md 强制）

> 需重连 chrome-devtools MCP（/mcp）。本地 k3d 部署改后 manager-api/web + 新 hermes 镜像（含 /oc/config）。

- [ ] **Step 1:** 部署：`make local-build`（api/web）；hermes 需 `make build-hermes-runtime` 重建并按 Spec A 的本地换镜像流程让实例用上含 /oc/config 的新镜像。
- [ ] **Step 2: 新成员随创建者**：以 org_admin（界面语言设为某值）新建成员 → 查该成员 users.locale 与其实例 apps.locale 等于 org_admin 的语言（DB 或详情页）。
- [ ] **Step 3: 传播**：以成员登录切换界面语言 → 确认其实例 apps.locale 更新（详情页"当前语言"仍显示旧 live + "需重启"提示出现）。
- [ ] **Step 4: 实时展示 + 重启生效**：详情页"实例语言"显示实例真实 live 语言；点「重启应用」→ 重启后该行变为新语言、提示消失。停止实例 → 显示"实例未运行"。
- [ ] **Step 5: 三角色**（platform_admin/org_admin/org_member）覆盖可见性与权限；产出逐场景矩阵存 `docs/reports/`。
- [ ] **Step 6:** 发现问题先修再验，闭环到全部正常。

---

## 完成判据
- `go test ./...`、`make openapi-check`、`make web-typecheck`、oc-ops pytest 全绿。
- 真机：新成员随创建者 locale；切语言传播 apps.locale 不重启；详情页实时显示实例语言 + 未运行态 + needs_restart 重启生效；三角色矩阵归档。
- 各层改动按业务边界分提交；openapi.yaml 与 generated.ts 同步入 git。

## 风险与回退
- **locale-status 实时往返**：service 调 oc-ops 用短超时，实例未运行快速返回 current=nil，避免详情页卡。
- **AuthService.store 接口扩展**：若其 store 接口未含 GetActiveAppByOwner/UpdateAppLocale，需扩接口 + mock，注意不要破坏现有 auth 测试。
- **CreateUser 加列**：sqlc 重生成产物随源提交；旧调用零值 NULL 行为不变。
- **oc-ops /oc/config 跨 variant**：进 6.5；5.16 按需带走（见 [[project-hermes-i18n-zh]] variant 带走清单）。
- 各 task 独立提交，单点出错可独立 revert。
