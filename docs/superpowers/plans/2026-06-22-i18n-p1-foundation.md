# 国际化 P1 地基 实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 打通多语言切换地基——前端引入 vue-i18n + naive-ui 语言联动 + `useLocaleStore`，后端落地 `users.locale`、`/me` 返回、持久化接口与公开配置端点，登录页与顶栏各一个语言选择器；做到「登录页可选语言、登录后跟随用户 DB、切换实时生效、刷新/换设备保持」。

**Architecture:** 翻译统一在前端用一套 vue-i18n 目录（en 默认 + zh）。语言来源优先级：登录页 `localStorage.locale` → 平台默认（`GET /api/v1/config`）→ `en`；登录后 `user.locale`（DB，经 `/me`）为准并回写 localStorage；切换时即时应用到 vue-i18n + naive-ui，写 localStorage，已登录则 `PATCH /api/v1/auth/me/locale` 持久化。后端本期只存取 locale、不建 Go i18n 目录（无直出文案场景）。

**Tech Stack:** 前端 Vue 3 + vue-i18n + naive-ui + Pinia；后端 Go + Gin + sqlc + golang-migrate；契约 swag → `openapi/openapi.yaml` → openapi-typescript。

**约定（贯穿全计划）：**
- locale 取值仅 `"en"` | `"zh"`，`en` 为默认与兜底。
- localStorage key：`ocm.locale`（与现有 `ocm.access_token` 同前缀）。
- 持久化接口：`PATCH /api/v1/auth/me/locale`，body `{ "locale": "en" }`，成功返回 204。
- 公开配置端点：`GET /api/v1/config`，返回 `{ "default_locale": "en", "supported_locales": ["en","zh"] }`。
- 提交遵循 AGENTS.md Conventional Commits（中文摘要 + 正文），committer 为 `Claude <noreply@anthropic.com>`。

---

## 文件结构

**后端（新建/修改）：**
- Create: `internal/migrations/000013_users_locale.up.sql` / `.down.sql` — users 表加 `locale` 列
- Modify: `sqlc.yaml` — schema 列表追加新迁移
- Modify: `internal/store/queries/users.sql` — 新增 `UpdateUserLocale`
- Modify: `internal/store/sqlc/*`（生成产物，勿手改）
- Modify: `internal/service/auth_service.go` — `AuthUser.Locale`、`toAuthUser`、`UpdateLocale`
- Modify: `internal/api/handlers/auth.go` — `UpdateLocale` handler + 路由注册
- Modify: `internal/api/handlers/dto.go` — `UpdateLocaleRequest`、`PublicConfigResponse`
- Create: `internal/api/handlers/config.go` — 公开配置 handler
- Modify: `internal/config/config.go` + `internal/config/loader.go` — `I18nConfig` + 默认值
- Modify: `internal/api/router.go` + `cmd/server/main.go` — 装配 `DefaultLocale`、注册公开配置路由
- Modify: `openapi/openapi.yaml`、`web/src/api/generated.ts`（生成产物）

**前端（新建/修改）：**
- Create: `web/src/i18n/index.ts` — 创建 i18n 实例 + 类型
- Create: `web/src/i18n/locales/en.ts` / `zh.ts` — 文案目录（P1 仅放地基所需 key）
- Create: `web/src/stores/locale.ts` + `web/src/stores/locale.spec.ts` — locale store
- Modify: `web/src/api/client.ts` — `Accept-Language` 头 + locale provider 注入
- Modify: `web/src/main.ts` — 装配 i18n、init locale
- Modify: `web/src/App.vue` — `NConfigProvider` 语言/日期联动
- Modify: `web/src/stores/auth.ts` — 登录/拉 /me 后同步 locale
- Create: `web/src/components/LocaleSwitcher.vue` + `.spec.ts` — 语言选择器组件
- Modify: `web/src/pages/login/LoginPage.vue` — 登录页挂选择器
- Modify: `web/src/layouts/DashboardLayout.vue` — 顶栏挂选择器
- Create: `web/tests/e2e/locale.spec.ts` — E2E 主链路

---

## Task 1：users 表新增 locale 列（migration + sqlc）

**Files:**
- Create: `internal/migrations/000013_users_locale.up.sql`
- Create: `internal/migrations/000013_users_locale.down.sql`
- Modify: `sqlc.yaml`（schema 列表）
- Modify: `internal/store/queries/users.sql`

- [ ] **Step 1: 写 up 迁移**

Create `internal/migrations/000013_users_locale.up.sql`:

```sql
-- users 新增 locale：用户界面语言偏好。NULL 表示「未显式选择」，由应用层回退到平台默认语言。
-- 不设 DEFAULT，避免把「未选择」与「显式选了某语言」混淆；CHECK 约束限定取值集合，新增语言时一并扩展。
ALTER TABLE users
    ADD COLUMN locale VARCHAR(10) NULL COMMENT '用户界面语言偏好（en/zh）；NULL=未选择，回退平台默认',
    ADD CONSTRAINT users_locale_check CHECK (locale IS NULL OR locale IN ('en','zh'));
```

- [ ] **Step 2: 写 down 迁移**

Create `internal/migrations/000013_users_locale.down.sql`:

```sql
ALTER TABLE users
    DROP CONSTRAINT users_locale_check,
    DROP COLUMN locale;
```

- [ ] **Step 3: 把新迁移加入 sqlc schema 列表**

Modify `sqlc.yaml`，在 schema 列表末尾（`000012_skill_ticket_title_only.up.sql` 之后）追加一行：

```yaml
      - internal/migrations/000012_skill_ticket_title_only.up.sql
      - internal/migrations/000013_users_locale.up.sql
```

- [ ] **Step 4: 新增 UpdateUserLocale 查询**

Modify `internal/store/queries/users.sql`，在文件末尾追加：

```sql
-- name: UpdateUserLocale :exec
-- 更新用户界面语言偏好。locale 由 handler 校验取值集合后传入；NULL 表示重置为「未选择」。
UPDATE users
SET locale = sqlc.arg(locale), updated_at = now()
WHERE id = sqlc.arg(id);
```

- [ ] **Step 5: 生成 sqlc 代码**

Run: `make sqlc-generate`
Expected: 命令成功；`git status` 显示 `internal/store/sqlc/` 下 `User` struct 多出 `Locale` 字段、新增 `UpdateUserLocale` 方法。

- [ ] **Step 6: 验证迁移可被加载（编译 + 迁移测试）**

Run: `go build ./... && go test ./internal/migrations/...`
Expected: PASS（迁移文件命名/编号连续，embed 正常）。

- [ ] **Step 7: 提交**

```bash
git add internal/migrations/000013_users_locale.up.sql internal/migrations/000013_users_locale.down.sql sqlc.yaml internal/store/queries/users.sql internal/store/sqlc/
git commit -m "feat(i18n): users 表新增 locale 列与更新查询

新增 000013 迁移给 users 加 locale(en/zh) 列，NULL 表示未选择、由应用层回退平台默认；
新增 UpdateUserLocale 查询并重新生成 sqlc 代码，为用户语言持久化提供存储层支持。"
```

---

## Task 2：AuthUser 暴露 locale（service 层）

**Files:**
- Modify: `internal/service/auth_service.go`
- Test: `internal/service/auth_service_test.go`（若不存在则在同目录新建针对 `toAuthUser` 的测试）

- [ ] **Step 1: 写失败测试（toAuthUser 透传 locale）**

在 `internal/service/auth_service_test.go` 追加（若文件不存在则创建，包名 `service`）：

```go
// TestToAuthUser_LocaleMapping 覆盖 toAuthUser 把 users.locale(null.String) 正确映射到 AuthUser.Locale：
// 已选语言透传，NULL（未选择）映射为空字符串，交由前端回退平台默认。
func TestToAuthUser_LocaleMapping(t *testing.T) {
	// 已显式选择 zh：应原样透传
	got := toAuthUser(sqlc.User{ID: "u1", Username: "a", DisplayName: "A", Role: "org_member", Status: "active", Locale: null.StringFrom("zh")})
	assert.Equal(t, "zh", got.Locale)

	// locale 为 NULL（未选择）：应映射为空字符串
	got = toAuthUser(sqlc.User{ID: "u2", Username: "b", DisplayName: "B", Role: "org_member", Status: "active"})
	assert.Equal(t, "", got.Locale)
}
```

确保测试文件 import 了 `sqlc`（`internal/store/sqlc`）、`null`（`github.com/guregu/null/v5`）、`testify/assert`。

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/service/ -run TestToAuthUser_LocaleMapping`
Expected: FAIL（`AuthUser` 无 `Locale` 字段，编译错误）。

- [ ] **Step 3: 给 AuthUser 加字段并在 toAuthUser 映射**

Modify `internal/service/auth_service.go`，`AuthUser` struct 末尾加字段：

```go
type AuthUser struct {
	ID          string `json:"id"`
	OrgID       string `json:"org_id,omitempty"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	Role        string `json:"role"`
	Status      string `json:"status"`
	// Locale 是用户界面语言偏好（en/zh）；空字符串表示未显式选择，前端回退平台默认。
	Locale string `json:"locale,omitempty"`
}
```

`toAuthUser` 末尾补一行（`Status` 之后）：

```go
func toAuthUser(user sqlc.User) AuthUser {
	return AuthUser{
		ID:          user.ID,
		OrgID:       strOrEmpty(user.OrgID),
		Username:    user.Username,
		DisplayName: user.DisplayName,
		Role:        user.Role,
		Status:      user.Status,
		Locale:      user.Locale.String, // null.String：未选择时 .String 为 ""
	}
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/service/ -run TestToAuthUser_LocaleMapping`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add internal/service/auth_service.go internal/service/auth_service_test.go
git commit -m "feat(i18n): AuthUser 暴露 locale 字段

toAuthUser 把 users.locale 透传到 AuthUser.Locale，NULL 映射为空字符串；
/me 与登录响应随之返回用户语言偏好，前端据此初始化界面语言。"
```

---

## Task 3：语言持久化接口 PATCH /api/v1/auth/me/locale

**Files:**
- Modify: `internal/service/auth_service.go`（`UpdateLocale`）
- Modify: `internal/api/handlers/dto.go`（`UpdateLocaleRequest`）
- Modify: `internal/api/handlers/auth.go`（handler + 路由）
- Test: `internal/service/auth_service_test.go`

- [ ] **Step 1: 写 service 失败测试**

在 `internal/service/auth_service_test.go` 追加：

```go
// fakeLocaleStore 仅实现 UpdateLocale 测试所需的存储方法，记录最后一次写入参数。
type fakeLocaleStore struct {
	gotID, gotLocale string
	err              error
}

func (f *fakeLocaleStore) UpdateUserLocale(_ context.Context, arg sqlc.UpdateUserLocaleParams) error {
	f.gotID, f.gotLocale = arg.ID, arg.Locale.String
	return f.err
}

// TestAuthService_UpdateLocale 覆盖语言持久化：合法 locale 写库、非法 locale 被拒。
func TestAuthService_UpdateLocale(t *testing.T) {
	// 合法 zh：应写入对应用户行
	store := &fakeLocaleStore{}
	svc := &AuthService{store: store}
	require.NoError(t, svc.UpdateLocale(context.Background(), "u1", "zh"))
	assert.Equal(t, "u1", store.gotID)
	assert.Equal(t, "zh", store.gotLocale)

	// 非法 fr：应返回 ErrInvalidLocale，不写库
	store2 := &fakeLocaleStore{}
	svc2 := &AuthService{store: store2}
	require.ErrorIs(t, svc2.UpdateLocale(context.Background(), "u1", "fr"), ErrInvalidLocale)
	assert.Equal(t, "", store2.gotID)
}
```

> 注：`AuthService.store` 是接口类型。若现有 store 接口未含 `UpdateUserLocale`，本任务需把该方法加入接口定义（与 `GetUser` 等并列）。`fakeLocaleStore` 仅用于本测试，若接口方法多导致无法只实现一个方法，改为嵌入现有测试桩或在桩上补 `UpdateUserLocale`。先查 `auth_service.go` 中 store 接口定义确认方法集。

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/service/ -run TestAuthService_UpdateLocale`
Expected: FAIL（`UpdateLocale`、`ErrInvalidLocale` 未定义）。

- [ ] **Step 3: 实现 UpdateLocale 与校验**

Modify `internal/service/auth_service.go`：

在错误哨兵区（其它 `ErrXxx` 旁）加：

```go
// ErrInvalidLocale 表示请求的语言不在受支持集合内。
var ErrInvalidLocale = errors.New("不支持的语言")

// SupportedLocales 是平台受支持的界面语言集合；新增语言时在此扩展并同步前端 locale 目录与迁移 CHECK 约束。
var SupportedLocales = []string{"en", "zh"}

// isSupportedLocale 判断 locale 是否受支持。
func isSupportedLocale(locale string) bool {
	for _, l := range SupportedLocales {
		if l == locale {
			return true
		}
	}
	return false
}
```

确认 store 接口含 `UpdateUserLocale(ctx, sqlc.UpdateUserLocaleParams) error`（不含则补上）。新增方法：

```go
// UpdateLocale 持久化用户界面语言偏好。locale 必须属于 SupportedLocales，否则返回 ErrInvalidLocale。
func (s *AuthService) UpdateLocale(ctx context.Context, userID, locale string) error {
	if !isSupportedLocale(locale) {
		return ErrInvalidLocale
	}
	if err := s.store.UpdateUserLocale(ctx, sqlc.UpdateUserLocaleParams{
		ID:     userID,
		Locale: null.StringFrom(locale),
	}); err != nil {
		return fmt.Errorf("更新用户语言失败: %w", err)
	}
	return nil
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/service/ -run TestAuthService_UpdateLocale`
Expected: PASS

- [ ] **Step 5: 加请求 DTO**

Modify `internal/api/handlers/dto.go`，追加：

```go
// UpdateLocaleRequest 是 PATCH /auth/me/locale 的请求体；locale 取值由 service 校验。
type UpdateLocaleRequest struct {
	// Locale 是目标界面语言，例如 en / zh。
	Locale string `json:"locale" binding:"required"`
}
```

- [ ] **Step 6: 加 handler 与路由 + swag 注解**

Modify `internal/api/handlers/auth.go`：

`RegisterAuthMeRoutes` 内追加一行：

```go
func RegisterAuthMeRoutes(router gin.IRouter, handler *AuthHandler) {
	group := router.Group("/api/v1/auth")
	group.GET("/me", handler.Me)
	group.POST("/password", handler.ChangePassword)
	group.PATCH("/me/locale", handler.UpdateLocale)
}
```

文件内新增 handler（放在 `Me` 附近）：

```go
// UpdateLocale 持久化当前登录用户的界面语言偏好。
//
// @Summary      更新界面语言
// @Description  保存当前用户的界面语言偏好（en/zh），登录后跟随用户
// @Tags         auth
// @Accept       json
// @Produce      json
// @Param        body  body      UpdateLocaleRequest  true  "语言请求"
// @Success      204
// @Failure      400   {object}  ErrorResponse
// @Failure      401   {object}  ErrorResponse
// @Router       /auth/me/locale [patch]
func (h *AuthHandler) UpdateLocale(c *gin.Context) {
	var req UpdateLocaleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeBindError(c, err)
		return
	}
	principal := principalFromCtx(c)
	if err := h.service.UpdateLocale(c.Request.Context(), principal.UserID, req.Locale); err != nil {
		if errors.Is(err, service.ErrInvalidLocale) {
			c.AbortWithStatusJSON(http.StatusBadRequest, apierror.New("INVALID_LOCALE", "不支持的语言"))
			return
		}
		writeAuthError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}
```

> 注：确认 `AuthService` 接口（handler 侧 `h.service` 的类型）已声明 `UpdateLocale`。`auth.go` 顶部若已 import `service`/`apierror`/`errors`/`net/http` 则复用，否则补 import。查 `RegisterAuthRoutes` 处的 `AuthService` interface 定义并加方法。

- [ ] **Step 7: 编译并跑 service + handler 测试**

Run: `go build ./... && go test ./internal/service/... ./internal/api/...`
Expected: PASS

- [ ] **Step 8: 提交**

```bash
git add internal/service/auth_service.go internal/service/auth_service_test.go internal/api/handlers/auth.go internal/api/handlers/dto.go
git commit -m "feat(i18n): 新增用户语言持久化接口

新增 PATCH /api/v1/auth/me/locale，校验 locale 属于受支持集合后写入 users.locale；
非法语言返回 400 INVALID_LOCALE。登录后用户切换语言即持久化到 DB，跨设备跟随。"
```

---

## Task 4：公开配置端点 GET /api/v1/config + 平台默认语言配置

**Files:**
- Modify: `internal/config/config.go`（`I18nConfig`）
- Modify: `internal/config/loader.go`（默认值）
- Modify: `internal/api/handlers/dto.go`（`PublicConfigResponse`）
- Create: `internal/api/handlers/config.go`
- Modify: `internal/api/router.go`（`Dependencies.DefaultLocale` + 注册路由）
- Modify: `cmd/server/main.go`（装配 `DefaultLocale`）
- Test: `internal/config/loader_test.go`、`internal/api/handlers/config_test.go`

- [ ] **Step 1: 写 config 默认值失败测试**

在 `internal/config/loader_test.go` 追加：

```go
// TestLoad_DefaultsI18nLocale 覆盖：未配置 i18n.default_locale 时回退 en，
// 显式配置 zh 时保留，保证平台默认语言可由配置文件控制。
func TestLoad_DefaultsI18nLocale(t *testing.T) {
	var c Config            // 未配置
	c.applyDefaults()
	assert.Equal(t, "en", c.I18n.DefaultLocale) // 缺省回退 en

	c2 := Config{I18n: I18nConfig{DefaultLocale: "zh"}} // 显式 zh
	c2.applyDefaults()
	assert.Equal(t, "zh", c2.I18n.DefaultLocale)
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/config/ -run TestLoad_DefaultsI18nLocale`
Expected: FAIL（`Config.I18n` / `I18nConfig` 未定义）。

- [ ] **Step 3: 加 I18nConfig + 默认值**

Modify `internal/config/config.go`：`Config` struct 末尾加字段：

```go
	// I18n 控制平台默认界面语言；整段可缺省，applyDefaults 回填 en。
	I18n I18nConfig `yaml:"i18n"`
```

并在文件内新增类型（放 `LoggingConfig` 附近）：

```go
// I18nConfig 描述平台国际化默认行为。
// DefaultLocale 是用户未显式选择语言时的回退（也下发给登录页）；缺省 en。
type I18nConfig struct {
	// DefaultLocale 是平台默认界面语言（en/zh）；空时由 applyDefaults 回填 en。
	DefaultLocale string `yaml:"default_locale"`
}
```

Modify `internal/config/loader.go` 的 `applyDefaults()`，函数体末尾加：

```go
	// i18n 默认语言：未配置时回退 en（平台默认英文）。
	if strings.TrimSpace(c.I18n.DefaultLocale) == "" {
		c.I18n.DefaultLocale = "en"
	}
```

- [ ] **Step 4: 运行 config 测试确认通过**

Run: `go test ./internal/config/ -run TestLoad_DefaultsI18nLocale`
Expected: PASS

- [ ] **Step 5: 加响应 DTO**

Modify `internal/api/handlers/dto.go`，追加：

```go
// PublicConfigResponse 是 GET /api/v1/config 的响应：登录前可读的平台级前端配置。
type PublicConfigResponse struct {
	// DefaultLocale 是平台默认界面语言（en/zh），登录页 localStorage 为空时采用。
	DefaultLocale string `json:"default_locale"`
	// SupportedLocales 是平台受支持的界面语言集合，供前端渲染语言选择器。
	SupportedLocales []string `json:"supported_locales"`
}
```

- [ ] **Step 6: 写 handler 测试**

Create `internal/api/handlers/config_test.go`:

```go
package handlers_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hujingnb/ywjs-oc-manager/internal/api/handlers"
)

// TestConfigHandler_Public 覆盖公开配置端点：返回注入的默认语言与受支持语言集合，无需鉴权。
func TestConfigHandler_Public(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	handlers.RegisterPublicConfigRoutes(r, handlers.NewConfigHandler("en", []string{"en", "zh"}))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/config", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var body handlers.PublicConfigResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "en", body.DefaultLocale)               // 默认语言透传
	assert.Equal(t, []string{"en", "zh"}, body.SupportedLocales) // 受支持集合透传
}
```

> 注：module path `github.com/hujingnb/ywjs-oc-manager` 以 `go.mod` 第一行为准，若不同则替换。

- [ ] **Step 7: 运行确认失败**

Run: `go test ./internal/api/handlers/ -run TestConfigHandler_Public`
Expected: FAIL（`NewConfigHandler` 等未定义）。

- [ ] **Step 8: 实现 config handler**

Create `internal/api/handlers/config.go`:

```go
package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// ConfigHandler 提供登录前可读的平台级前端配置（无需鉴权）。
// 当前仅下发国际化默认语言与受支持语言集合，供前端登录页初始化界面语言。
type ConfigHandler struct {
	defaultLocale    string
	supportedLocales []string
}

// NewConfigHandler 创建公开配置 handler。defaultLocale 来自 manager 配置文件 i18n.default_locale。
func NewConfigHandler(defaultLocale string, supportedLocales []string) *ConfigHandler {
	return &ConfigHandler{defaultLocale: defaultLocale, supportedLocales: supportedLocales}
}

// RegisterPublicConfigRoutes 注册公开配置路由（public 分组，无 Bearer token）。
func RegisterPublicConfigRoutes(router gin.IRouter, handler *ConfigHandler) {
	router.GET("/api/v1/config", handler.Get)
}

// Get 返回平台公开前端配置。
//
// @Summary      公开前端配置
// @Description  登录前可读的平台级配置：默认界面语言与受支持语言集合
// @Tags         config
// @Produce      json
// @Success      200  {object}  PublicConfigResponse
// @Router       /config [get]
func (h *ConfigHandler) Get(c *gin.Context) {
	c.JSON(http.StatusOK, PublicConfigResponse{
		DefaultLocale:    h.defaultLocale,
		SupportedLocales: h.supportedLocales,
	})
}
```

- [ ] **Step 9: 装配依赖与注册路由**

Modify `internal/api/router.go`：`Dependencies` struct 末尾加字段：

```go
	// DefaultLocale 是平台默认界面语言（来自 config i18n.default_locale），经公开配置端点下发给前端。
	DefaultLocale string
```

在 public 区（`RegisterHealthRoutes(router)` 之后、`if len(deps) == 0` 之前需用到 deps，故放在 `dep := deps[0]` 之后、user 组之前）注册：

```go
	// 公开前端配置：无需鉴权，登录页据此初始化界面语言。
	defaultLocale := dep.DefaultLocale
	if defaultLocale == "" {
		defaultLocale = "en"
	}
	handlers.RegisterPublicConfigRoutes(router, handlers.NewConfigHandler(defaultLocale, service.SupportedLocales))
```

> 注：`service` 包已在 router.go import；`service.SupportedLocales` 来自 Task 3。

Modify `cmd/server/main.go`：`api.Dependencies{...}` 字面量内加一行：

```go
			DefaultLocale:                cfg.I18n.DefaultLocale,
```

- [ ] **Step 10: 编译并跑测试**

Run: `go build ./... && go test ./internal/api/handlers/ ./internal/config/`
Expected: PASS

- [ ] **Step 11: 提交**

```bash
git add internal/config/config.go internal/config/loader.go internal/config/loader_test.go internal/api/handlers/config.go internal/api/handlers/config_test.go internal/api/handlers/dto.go internal/api/router.go cmd/server/main.go
git commit -m "feat(i18n): 新增公开配置端点与平台默认语言配置

新增 config.i18n.default_locale（缺省 en）与公开端点 GET /api/v1/config，
下发默认语言与受支持语言集合；登录页 localStorage 为空时据此初始化界面语言。"
```

---

## Task 5：同步 OpenAPI 契约与前端类型

**Files:**
- Modify: `openapi/openapi.yaml`、`web/src/api/generated.ts`（均为生成产物）

- [ ] **Step 1: 生成契约与前端类型**

Run: `make openapi-gen && make web-types-gen`
Expected: 命令成功；`git status` 显示 `openapi/openapi.yaml` 与 `web/src/api/generated.ts` 有变更（新增 `/config`、`/auth/me/locale`、`AuthUser.locale`、`PublicConfigResponse` 等）。

- [ ] **Step 2: 校验同步**

Run: `make openapi-check`
Expected: PASS（再次生成后 git 工作区干净）。

- [ ] **Step 3: 提交**

```bash
git add openapi/openapi.yaml web/src/api/generated.ts
git commit -m "chore(i18n): 同步 OpenAPI 契约与前端生成类型

随语言持久化接口与公开配置端点重新生成 openapi.yaml 与 generated.ts，保持契约同步。"
```

---

## Task 6：前端引入 vue-i18n 与文案目录骨架

**Files:**
- Modify: `web/package.json`（依赖）
- Create: `web/src/i18n/locales/en.ts`
- Create: `web/src/i18n/locales/zh.ts`
- Create: `web/src/i18n/index.ts`

- [ ] **Step 1: 安装 vue-i18n**

Run: `cd web && npm install vue-i18n@^11`
Expected: `package.json` dependencies 出现 `vue-i18n`。

- [ ] **Step 2: 写英文文案目录（P1 地基所需 key）**

Create `web/src/i18n/locales/en.ts`:

```ts
// en 是平台默认与兜底语言目录。key 按页面/领域分组，P1 仅放地基所需文案；
// 后续 P2 按模块批量补全。新增语言只需复制本结构并整体翻译。
export default {
  common: {
    languageName: 'English',
  },
  locale: {
    switcherLabel: 'Language',
  },
} as const
```

- [ ] **Step 3: 写中文文案目录**

Create `web/src/i18n/locales/zh.ts`:

```ts
// zh 简体中文目录，结构必须与 en 完全对齐（同样的 key 路径）。
export default {
  common: {
    languageName: '简体中文',
  },
  locale: {
    switcherLabel: '语言',
  },
}
```

- [ ] **Step 4: 创建 i18n 实例**

Create `web/src/i18n/index.ts`:

```ts
// i18n 单例：Composition API 模式（legacy:false），默认与兜底语言均为 en。
// locale 初值占位 'en'，真实初值由 useLocaleStore 在应用启动时按优先级解析后通过 setLocale 设置。
import { createI18n } from 'vue-i18n'

import en from './locales/en.ts'
import zh from './locales/zh.ts'

// SupportedLocale 是前端受支持语言联合类型；与后端 service.SupportedLocales 保持一致。
export type SupportedLocale = 'en' | 'zh'

// SUPPORTED_LOCALES 供选择器渲染与校验；顺序即选择器展示顺序。
export const SUPPORTED_LOCALES: SupportedLocale[] = ['en', 'zh']

// DEFAULT_LOCALE 是前端硬兜底（后端公开配置不可达时使用）。
export const DEFAULT_LOCALE: SupportedLocale = 'en'

export const i18n = createI18n({
  legacy: false,
  locale: DEFAULT_LOCALE,
  fallbackLocale: DEFAULT_LOCALE,
  messages: { en, zh },
})
```

- [ ] **Step 5: 类型检查**

Run: `cd web && npm run typecheck`
Expected: PASS（en/zh 结构对齐，无类型错误）。

- [ ] **Step 6: 提交**

```bash
git add web/package.json web/package-lock.json web/src/i18n/
git commit -m "feat(i18n): 前端引入 vue-i18n 与文案目录骨架

新增 vue-i18n 依赖与 i18n 单例（Composition API、默认/兜底 en），建立 en/zh 文案目录结构；
P1 仅放地基所需 key，后续按模块批量补全。"
```

---

## Task 7：useLocaleStore（语言状态与持久化）

**Files:**
- Create: `web/src/stores/locale.ts`
- Test: `web/src/stores/locale.spec.ts`
- Modify: `web/src/api/client.ts`（locale provider，供 Accept-Language 头读取）

- [ ] **Step 1: 在 client.ts 暴露 locale provider**

Modify `web/src/api/client.ts`：在文件靠上位置（`unauthorizedHandler` 定义附近）加：

```ts
// 当前 locale 提供者：由 locale store 在初始化时注入，apiRequest 据此附加 Accept-Language。
// 用函数注入而非直接 import store，避免 client 与 pinia 形成循环依赖。
let currentLocaleProvider: (() => string) | null = null

// setLocaleProvider 注册 locale 读取函数；传 null 可清除（测试用）。
export function setLocaleProvider(provider: (() => string) | null): void {
  currentLocaleProvider = provider
}
```

在 `apiRequest` 内构造 `headers` 后、`fetch` 前，附加语言头：

```ts
  // 附加 Accept-Language：后端本期不消费（翻译在前端），但提前带上便于未来后端直出文案场景。
  const locale = currentLocaleProvider?.()
  if (locale) {
    headers['Accept-Language'] = locale
  }
```

- [ ] **Step 2: 写 locale store 失败测试**

Create `web/src/stores/locale.spec.ts`:

```ts
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'

import { useLocaleStore } from './locale'
import { i18n } from '@/i18n'

// mock api client：拦截 apiRequest，断言持久化调用；保留 setLocaleProvider 真身。
const apiRequest = vi.fn()
vi.mock('@/api/client', async (orig) => {
  const actual = await orig<typeof import('@/api/client')>()
  return { ...actual, apiRequest: (...a: unknown[]) => apiRequest(...a) }
})

describe('useLocaleStore', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    localStorage.clear()
    apiRequest.mockReset().mockResolvedValue(undefined)
    i18n.global.locale.value = 'en'
  })

  // localStorage 有值时 init 采用它并应用到 i18n
  it('init 优先采用 localStorage', async () => {
    localStorage.setItem('ocm.locale', 'zh')
    const store = useLocaleStore()
    await store.init()
    expect(store.locale).toBe('zh')
    expect(i18n.global.locale.value).toBe('zh')
  })

  // localStorage 为空时 init 回退后端默认（fetchDefault 返回 zh）
  it('init 回退后端默认语言', async () => {
    apiRequest.mockResolvedValueOnce({ default_locale: 'zh', supported_locales: ['en', 'zh'] })
    const store = useLocaleStore()
    await store.init()
    expect(store.locale).toBe('zh')
  })

  // setLocale 切换：写 i18n、写 localStorage；已登录时 PATCH 持久化
  it('setLocale 已登录时持久化到后端', async () => {
    const store = useLocaleStore()
    await store.init()
    await store.setLocale('zh', { persist: true })
    expect(localStorage.getItem('ocm.locale')).toBe('zh')
    expect(i18n.global.locale.value).toBe('zh')
    expect(apiRequest).toHaveBeenCalledWith('/api/v1/auth/me/locale', expect.objectContaining({ method: 'PATCH', body: { locale: 'zh' } }))
  })

  // 非法 locale 被规范化为兜底 en，不写入非法值
  it('applyFromUser 非法值回退兜底', () => {
    const store = useLocaleStore()
    store.applyFromUser('fr')
    expect(store.locale).toBe('en')
  })
})
```

- [ ] **Step 3: 运行确认失败**

Run: `cd web && npm test -- --run src/stores/locale.spec.ts`
Expected: FAIL（`useLocaleStore` 未定义）。

- [ ] **Step 4: 实现 locale store**

Create `web/src/stores/locale.ts`:

```ts
// locale store 集中管理界面语言：解析优先级、应用到 vue-i18n、持久化（localStorage + 已登录则后端）。
// 不直接依赖 auth store，是否持久化由调用方通过 persist 选项决定，避免 store 间环依赖。
import { defineStore } from 'pinia'
import { ref } from 'vue'

import { apiRequest, setLocaleProvider } from '@/api/client'
import { DEFAULT_LOCALE, SUPPORTED_LOCALES, i18n, type SupportedLocale } from '@/i18n'

const STORAGE_KEY = 'ocm.locale'

// normalize 把任意输入规范化为受支持 locale，非法/空值回退 DEFAULT_LOCALE。
function normalize(value: string | null | undefined): SupportedLocale {
  return SUPPORTED_LOCALES.includes(value as SupportedLocale) ? (value as SupportedLocale) : DEFAULT_LOCALE
}

export const useLocaleStore = defineStore('locale', () => {
  const locale = ref<SupportedLocale>(DEFAULT_LOCALE)

  // apply 把目标语言写入内存、vue-i18n 与 localStorage（单一出口，保证三者一致）。
  function apply(next: SupportedLocale): void {
    locale.value = next
    i18n.global.locale.value = next
    localStorage.setItem(STORAGE_KEY, next)
  }

  // fetchDefault 读取平台默认语言（登录页 localStorage 为空时使用）；端点不可达时回退 DEFAULT_LOCALE。
  async function fetchDefault(): Promise<SupportedLocale> {
    try {
      const cfg = await apiRequest<{ default_locale: string }>('/api/v1/config', { withAuth: false })
      return normalize(cfg.default_locale)
    } catch {
      return DEFAULT_LOCALE
    }
  }

  // init 在应用启动时解析初值：localStorage → 平台默认 → 兜底；并把 locale provider 注入 api client。
  async function init(): Promise<void> {
    setLocaleProvider(() => locale.value)
    const stored = localStorage.getItem(STORAGE_KEY)
    apply(stored ? normalize(stored) : await fetchDefault())
  }

  // setLocale 用户主动切换：应用语言；persist 为 true（已登录）时持久化到后端。
  async function setLocale(next: SupportedLocale, opts: { persist?: boolean } = {}): Promise<void> {
    apply(normalize(next))
    if (opts.persist) {
      await apiRequest('/api/v1/auth/me/locale', { method: 'PATCH', body: { locale: locale.value } })
    }
  }

  // applyFromUser 登录后用 DB 中的用户语言覆盖（user.locale 为空表示未选择，保持当前值）。
  function applyFromUser(userLocale: string | undefined): void {
    if (userLocale) apply(normalize(userLocale))
  }

  return { locale, init, setLocale, applyFromUser }
})
```

- [ ] **Step 5: 运行确认通过**

Run: `cd web && npm test -- --run src/stores/locale.spec.ts`
Expected: PASS

- [ ] **Step 6: 提交**

```bash
git add web/src/stores/locale.ts web/src/stores/locale.spec.ts web/src/api/client.ts
git commit -m "feat(i18n): 新增 useLocaleStore 管理界面语言

集中管理语言解析优先级（localStorage→平台默认→兜底）、应用到 vue-i18n、
持久化到 localStorage 与已登录用户 DB；apiRequest 附加 Accept-Language 头。"
```

---

## Task 8：装配 i18n 与 naive-ui 语言联动

**Files:**
- Modify: `web/src/main.ts`
- Modify: `web/src/App.vue`
- Modify: `web/src/stores/auth.ts`

- [ ] **Step 1: main.ts 装配 i18n 并初始化 locale**

Modify `web/src/main.ts`：

import 区加：

```ts
import { i18n } from '@/i18n'
import { useLocaleStore } from '@/stores/locale'
```

`app.use(createPinia())` 之后、`app.mount('#app')` 之前：

```ts
app.use(i18n)

// 应用语言初始化：必须在 pinia 装配之后、挂载之前完成，避免首屏闪烁默认语言。
// init 读 localStorage 或拉平台默认，并把 locale provider 注入 api client。
await useLocaleStore().init()
```

> 注：`main.ts` 顶层使用 `await` 需确保模块为 ESM（项目 `type: module`，Vite 支持顶层 await）。若构建报错，改为 `useLocaleStore().init().finally(() => app.mount('#app'))` 并把 `app.mount` 移入回调。

- [ ] **Step 2: App.vue 接入 NConfigProvider 语言/日期**

Modify `web/src/App.vue`：

`<script setup>` import 区加：

```ts
import { computed } from 'vue'
import { enUS, zhCN, dateEnUS, dateZhCN } from 'naive-ui'
import { storeToRefs } from 'pinia'
import { useLocaleStore } from '@/stores/locale'
```

script 内加：

```ts
// naive-ui 内置组件文案与日期组件随当前 locale 切换。
const { locale } = storeToRefs(useLocaleStore())
const naiveLocale = computed(() => (locale.value === 'zh' ? zhCN : enUS))
const naiveDateLocale = computed(() => (locale.value === 'zh' ? dateZhCN : dateEnUS))
```

template 顶层 `<NConfigProvider>` 加绑定：

```html
  <NConfigProvider :theme-overrides="themeOverrides" :locale="naiveLocale" :date-locale="naiveDateLocale">
```

- [ ] **Step 3: 登录/拉 /me 后同步用户语言**

Modify `web/src/stores/auth.ts`：在 `login` 成功写入 `user.value = result.user` 之后、`return result` 之前加：

```ts
      // 登录后用 DB 中的用户语言覆盖界面（user.locale 为空表示未选择，保持登录页所选）。
      useLocaleStore().applyFromUser(result.user.locale)
```

在 `fetchCurrentUser` 成功 `user.value = response.user` 之后加：

```ts
      useLocaleStore().applyFromUser(response.user.locale)
```

并在文件 import 区加 `import { useLocaleStore } from '@/stores/locale'`。

> 注：`AuthUser` 类型经 Task 5 重新生成后已含可选 `locale`，类型检查应通过。

- [ ] **Step 4: 类型检查 + 现有单测回归**

Run: `cd web && npm run typecheck && npm test -- --run`
Expected: PASS（含 auth.spec 等既有用例）。

- [ ] **Step 5: 提交**

```bash
git add web/src/main.ts web/src/App.vue web/src/stores/auth.ts
git commit -m "feat(i18n): 装配 i18n 并联动 naive-ui 语言

main.ts 启动时初始化 locale，App.vue 的 NConfigProvider 随 locale 切换内置文案与日期；
登录与恢复会话后用 DB 用户语言覆盖界面，做到跟随用户。"
```

---

## Task 9：语言选择器组件

**Files:**
- Create: `web/src/components/LocaleSwitcher.vue`
- Test: `web/src/components/__tests__/LocaleSwitcher.spec.ts`

- [ ] **Step 1: 写组件失败测试**

Create `web/src/components/__tests__/LocaleSwitcher.spec.ts`:

```ts
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'

import LocaleSwitcher from '../LocaleSwitcher.vue'
import { i18n } from '@/i18n'

const setLocale = vi.fn()
vi.mock('@/stores/locale', () => ({
  useLocaleStore: () => ({ locale: { value: 'en' }, setLocale }),
}))

describe('LocaleSwitcher', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    setLocale.mockReset()
  })

  // 选择 zh 时按 persist 透传调用 store.setLocale
  it('选择语言时调用 setLocale 并透传 persist', async () => {
    const wrapper = mount(LocaleSwitcher, {
      global: { plugins: [i18n] },
      props: { persist: true },
    })
    await wrapper.vm.onSelect('zh')
    expect(setLocale).toHaveBeenCalledWith('zh', { persist: true })
  })
})
```

- [ ] **Step 2: 运行确认失败**

Run: `cd web && npm test -- --run src/components/__tests__/LocaleSwitcher.spec.ts`
Expected: FAIL（组件不存在）。

- [ ] **Step 3: 实现组件**

Create `web/src/components/LocaleSwitcher.vue`:

```vue
<template>
  <!-- 语言选择器：登录页(persist=false)与顶栏(persist=true)复用同一组件。 -->
  <n-dropdown trigger="click" :options="options" @select="onSelect">
    <n-button quaternary size="small" :aria-label="t('locale.switcherLabel')">
      <template #icon><Languages :size="16" /></template>
      {{ currentLabel }}
    </n-button>
  </n-dropdown>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { NButton, NDropdown } from 'naive-ui'
import { Languages } from 'lucide-vue-next'
import { useI18n } from 'vue-i18n'

import { SUPPORTED_LOCALES, type SupportedLocale } from '@/i18n'
import { useLocaleStore } from '@/stores/locale'

// persist 决定切换后是否持久化到后端：顶栏(已登录)为 true，登录页为 false。
const props = withDefaults(defineProps<{ persist?: boolean }>(), { persist: false })

const { t, messages, locale: i18nLocale } = useI18n()
const localeStore = useLocaleStore()

// options 用各语言自报名（languageName）渲染，保证「该语言的母语者」总能认出自己的语言。
const options = computed(() =>
  SUPPORTED_LOCALES.map((code) => ({
    key: code,
    label: (messages.value[code] as { common: { languageName: string } }).common.languageName,
  })),
)

// currentLabel 展示当前语言的自报名。
const currentLabel = computed(
  () => (messages.value[i18nLocale.value] as { common: { languageName: string } }).common.languageName,
)

// onSelect 切换语言并按 persist 透传给 store；导出以便单测直接调用。
async function onSelect(key: SupportedLocale): Promise<void> {
  await localeStore.setLocale(key, { persist: props.persist })
}

defineExpose({ onSelect })
</script>
```

- [ ] **Step 4: 运行确认通过**

Run: `cd web && npm test -- --run src/components/__tests__/LocaleSwitcher.spec.ts`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add web/src/components/LocaleSwitcher.vue web/src/components/__tests__/LocaleSwitcher.spec.ts
git commit -m "feat(i18n): 新增语言选择器组件

LocaleSwitcher 以各语言自报名渲染下拉，登录页与顶栏复用；
persist 控制切换后是否持久化到后端，默认 false。"
```

---

## Task 10：登录页与顶栏挂载选择器

**Files:**
- Modify: `web/src/pages/login/LoginPage.vue`
- Modify: `web/src/layouts/DashboardLayout.vue`

- [ ] **Step 1: 登录页挂选择器（persist=false）**

Modify `web/src/pages/login/LoginPage.vue`：`<script setup>` import 区加：

```ts
import LocaleSwitcher from '@/components/LocaleSwitcher.vue'
```

template 内登录卡片顶部（`<p class="login-brand">` 之前）加一行右对齐选择器：

```html
    <div class="login-locale-row">
      <LocaleSwitcher :persist="false" />
    </div>
```

在 `<style scoped>` 末尾加：

```css
.login-locale-row {
  display: flex;
  justify-content: flex-end;
  margin-bottom: 8px;
}
```

> 注：登录页未登录，选择器 persist=false，仅写 localStorage + 即时应用；登录成功后由 auth store 的 `applyFromUser` 用 DB 值覆盖。

- [ ] **Step 2: 顶栏挂选择器（persist=true）**

Modify `web/src/layouts/DashboardLayout.vue`：`<script setup>` import 区加：

```ts
import LocaleSwitcher from '@/components/LocaleSwitcher.vue'
```

template 内 `<div class="topbar-actions">` 起始处（`<n-tag ...>API 正常</n-tag>` 之前）加：

```html
          <LocaleSwitcher :persist="true" />
```

- [ ] **Step 3: 类型检查 + 现有单测回归**

Run: `cd web && npm run typecheck && npm test -- --run`
Expected: PASS

- [ ] **Step 4: 提交**

```bash
git add web/src/pages/login/LoginPage.vue web/src/layouts/DashboardLayout.vue
git commit -m "feat(i18n): 登录页与顶栏挂载语言选择器

登录页选择器仅写 localStorage（persist=false），顶栏选择器持久化到用户 DB（persist=true）；
登录前后语言来源切换与设计一致。"
```

---

## Task 11：E2E 主链路 + 全面浏览器验证

**Files:**
- Create: `web/tests/e2e/locale.spec.ts`

- [ ] **Step 1: 看现有 E2E 用例风格**

Run: `ls web/tests` 并阅读现有 `*.spec.ts` 一例，复用其登录辅助与 `baseURL` 约定（`web/playwright.config.ts`）。

- [ ] **Step 2: 写 E2E 用例**

Create `web/tests/e2e/locale.spec.ts`（按现有用例的登录辅助改写，骨架如下）：

```ts
import { expect, test } from '@playwright/test'

// 覆盖语言主链路：登录页切语言 → 登录后跟随 DB → 切换实时变 → 刷新保持。
test('登录页可切语言且登录后跟随用户持久化', async ({ page }) => {
  await page.goto('/login')

  // 登录页切到中文：选择器以自报名渲染
  await page.getByRole('button', { name: /Language|语言|English|简体中文/ }).click()
  await page.getByText('简体中文').click()
  // 断言界面出现中文文案（按实际已迁移文案调整断言目标）
  await expect(page.locator('body')).toContainText('简体中文')

  // 登录（复用现有 e2e 登录步骤/凭据）
  // ... 填写 admin / admin123 并提交 ...

  // 顶栏切英文并刷新：应保持英文（localStorage + DB 双持久化）
  // ... 断言刷新后 localStorage.ocm.locale === 'en' ...
})
```

> 注：断言文案目标取 P1 已存在的可见文案；P1 阶段大量页面文案尚未迁移，E2E 以「选择器存在 + localStorage 持久化 + naive-ui 组件语言切换」为主，不强依赖具体业务文案。

- [ ] **Step 3: 运行 E2E**

Run: `cd web && npm run test:e2e -- locale.spec.ts`
Expected: PASS（若本地无 e2e 后端环境，记录跳过原因并改在浏览器手动验证）。

- [ ] **Step 4: 真实浏览器全面验证（AGENTS.md 强制）**

按 AGENTS.md「交付前检查」，用真实浏览器（本地 k3d，http://ocm.localhost，admin/admin123）验证：
1. 登录页右上角出现语言选择器，切换后页面 naive-ui 组件语言即时变化，localStorage `ocm.locale` 写入。
2. 登录后顶栏选择器切换语言，刷新页面后保持；退出重登仍保持（DB 持久化）。
3. 换浏览器/隐身窗口登录同账号，界面语言跟随该用户 DB 设置。
4. 发现问题先修复并重新验证，直到正常。

- [ ] **Step 5: 提交**

```bash
git add web/tests/e2e/locale.spec.ts
git commit -m "test(i18n): 新增语言切换 E2E 主链路用例

覆盖登录页切语言、登录后跟随用户 DB、切换实时生效与刷新保持；
配合真实浏览器验证 naive-ui 组件语言联动与跨设备跟随。"
```

---

## Self-Review 记录

- **Spec 覆盖（P1 范围）**：vue-i18n 装配（T6/T8）✓；useLocaleStore（T7）✓；naive-ui 联动（T8）✓；users.locale 列（T1）✓；/me 返回 locale（T2）✓；持久化接口（T3）✓；公开配置端点（T4）✓；登录页 + 顶栏选择器（T9/T10）✓；测试与浏览器验证（T11）✓；OpenAPI 同步（T5）✓。
- **跨任务类型一致**：`UpdateUserLocale`(sqlc, T1) / `UpdateLocale`(service, T3) / `UpdateLocale`(handler, T3) 命名贯穿一致；`service.SupportedLocales`(T3) 被 router(T4) 复用；`setLocaleProvider`(T7-client) 与 `i18n`/`SUPPORTED_LOCALES`/`DEFAULT_LOCALE`(T6) 在 store/组件中一致引用；`AuthUser.locale`(T2) 经 T5 生成后被 auth store(T8) 与选择器消费。
- **待执行时确认的现实约束**（计划已就地标注）：① AuthService 的 store 接口与 handler 侧 service 接口需补 `UpdateUserLocale`/`UpdateLocale` 方法声明；② module path 以 go.mod 为准；③ main.ts 顶层 await 若构建不支持则改回调式挂载。
