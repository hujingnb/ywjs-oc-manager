# Instance Model Governance Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add manager-managed organization model allowlists and per-instance model selection, with model changes requiring an instance restart.

**Architecture:** new-api remains the realtime source for available models, but does not enforce model permissions. Manager persists `organizations.enabled_models` and `apps.model_id`, validates all create/update paths against the organization allowlist, and injects `app.model_id` into OpenClaw during initialization. Instance model updates are saved through a service transaction that also writes audit and queues a restart job when a container exists.

**Tech Stack:** Go, Gin, sqlc, PostgreSQL migrations, pgx, Vue 3, Naive UI, TanStack Query, OpenAPI generation.

---

## File Structure

- Create `internal/migrations/000015_app_model_governance.up.sql`: add model columns and clear legacy local app data.
- Create `internal/migrations/000015_app_model_governance.down.sql`: remove model columns and index.
- Modify `internal/store/queries/organizations.sql`: persist `enabled_models` and count apps using removed models.
- Modify `internal/store/queries/apps.sql`: persist `model_id`, update model, and include it in app results.
- Regenerate `internal/store/sqlc/*.go`: sqlc models and query params gain `EnabledModels` and `ModelID`.
- Modify `internal/integrations/newapi/client.go` and `client_test.go`: add realtime model-list methods for `/api/models` and `/v1/models`.
- Create `internal/service/model_service.go` and `model_service_test.go`: expose model catalog retrieval and validation helpers.
- Modify `internal/service/organization_service.go` and tests: store organization allowlist and block removals in use.
- Modify `internal/service/onboarding_service.go` and tests: require model on app creation.
- Modify `internal/service/app_service.go` and tests: return model and implement model update with restart job.
- Modify `internal/worker/handlers/app_initialize.go` and tests: inject `app.model_id`.
- Modify `internal/api/handlers/dto.go`: add request/response fields for models.
- Modify `internal/api/handlers/apps.go` and tests: register `PATCH /apps/{appId}/model`.
- Create `internal/api/handlers/models.go` and `models_test.go`: register `GET /models`.
- Modify `internal/api/router.go`, `cmd/server/main.go`: wire model service and extended app service.
- Modify `web/src/api/hooks/useOrganizations.ts`, `useMembers.ts`, `useApps.ts`: add model payloads and hooks.
- Modify `web/src/api/index.ts`: mark new generated fields as required where runtime requires them.
- Modify `web/src/pages/platform/OrganizationsPage.vue`: model multiselect in organization form.
- Modify `web/src/pages/org/CreateMemberPage.vue` and `MembersPage.vue`: model select on instance creation.
- Modify `web/src/pages/apps/AppOverviewTab.vue`: model display and update form.
- Update page specs under `web/src/pages/**`: cover new model interactions.
- Regenerate `openapi/openapi.yaml` and `web/src/api/generated.ts`.

## Task 1: Database And sqlc Model Columns

**Files:**
- Create: `internal/migrations/000015_app_model_governance.up.sql`
- Create: `internal/migrations/000015_app_model_governance.down.sql`
- Modify: `internal/store/queries/organizations.sql`
- Modify: `internal/store/queries/apps.sql`
- Generated: `internal/store/sqlc/*.go`

- [ ] **Step 1: Add the migration**

Create `internal/migrations/000015_app_model_governance.up.sql`:

```sql
-- 实例模型治理迁移。
-- 本地历史 app 数据均为测试数据，清理后避免为旧实例猜测错误模型。
DELETE FROM channel_bindings
WHERE app_id IN (SELECT id FROM apps);

DELETE FROM jobs
WHERE payload_json->>'app_id' IN (SELECT id::text FROM apps);

DELETE FROM audit_logs
WHERE target_type = 'app'
  AND target_id IN (SELECT id::text FROM apps);

DELETE FROM apps;

ALTER TABLE organizations
ADD COLUMN enabled_models jsonb NOT NULL DEFAULT '[]'::jsonb;

ALTER TABLE organizations
ADD CONSTRAINT organizations_enabled_models_array_check
CHECK (jsonb_typeof(enabled_models) = 'array');

COMMENT ON COLUMN organizations.enabled_models IS 'manager 层组织可用模型列表；new-api 不用该字段做权限控制。';

ALTER TABLE apps
ADD COLUMN model_id text NOT NULL;

COMMENT ON COLUMN apps.model_id IS '实例当前使用的模型 ID，由 manager 注入 OpenClaw 配置。';

CREATE INDEX apps_org_model_active_idx
ON apps(org_id, model_id)
WHERE deleted_at IS NULL;
```

Create `internal/migrations/000015_app_model_governance.down.sql`:

```sql
DROP INDEX IF EXISTS apps_org_model_active_idx;

ALTER TABLE apps
DROP COLUMN IF EXISTS model_id;

ALTER TABLE organizations
DROP CONSTRAINT IF EXISTS organizations_enabled_models_array_check;

ALTER TABLE organizations
DROP COLUMN IF EXISTS enabled_models;
```

- [ ] **Step 2: Update organization queries**

In `internal/store/queries/organizations.sql`, change `CreateOrganization` to insert `enabled_models`:

```sql
-- name: CreateOrganization :one
INSERT INTO organizations (
    name,
    code,
    status,
    contact_name,
    contact_phone,
    remark,
    credit_warning_threshold,
    enabled_models
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8
)
RETURNING *;
```

Change `UpdateOrganizationProfile` to also update model allowlist:

```sql
-- name: UpdateOrganizationProfile :one
UPDATE organizations
SET
    name = $2,
    contact_name = $3,
    contact_phone = $4,
    remark = $5,
    credit_warning_threshold = $6,
    enabled_models = $7,
    updated_at = now()
WHERE id = $1
RETURNING *;
```

Append this query:

```sql
-- name: CountActiveAppsByOrgAndModels :many
SELECT model_id, count(*)::bigint AS app_count
FROM apps
WHERE org_id = $1
  AND deleted_at IS NULL
  AND model_id = ANY($2::text[])
GROUP BY model_id
ORDER BY model_id;
```

- [ ] **Step 3: Update app queries**

In `internal/store/queries/apps.sql`, add `model_id` to `CreateApp`:

```sql
-- name: CreateApp :one
INSERT INTO apps (
    org_id,
    owner_user_id,
    runtime_node_id,
    name,
    description,
    status,
    persona_mode,
    app_prompt,
    api_key_status,
    model_id
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10
)
RETURNING *;
```

Append this query:

```sql
-- name: SetAppModel :one
UPDATE apps
SET model_id = $2,
    updated_at = now()
WHERE id = $1
  AND deleted_at IS NULL
RETURNING *;
```

- [ ] **Step 4: Regenerate sqlc and run migration tests**

Run:

```bash
rtk make sqlc-generate
rtk go test ./internal/migrations ./internal/store -count=1
```

Expected: both packages pass, and generated sqlc code includes `Organization.EnabledModels []byte` and `App.ModelID string`.

- [ ] **Step 5: Commit database and sqlc changes**

```bash
rtk git add internal/migrations/000015_app_model_governance.up.sql \
  internal/migrations/000015_app_model_governance.down.sql \
  internal/store/queries/organizations.sql \
  internal/store/queries/apps.sql \
  internal/store/sqlc
rtk git commit -m "feat(model): 增加组织和实例模型字段" \
  -m "新增模型治理迁移，清理本地旧实例测试数据，并为组织可用模型和实例当前模型生成 sqlc 查询。"
```

## Task 2: new-api Model Catalog Client And Service

**Files:**
- Modify: `internal/integrations/newapi/client.go`
- Modify: `internal/integrations/newapi/client_test.go`
- Create: `internal/service/model_service.go`
- Create: `internal/service/model_service_test.go`
- Create: `internal/api/handlers/models.go`
- Create: `internal/api/handlers/models_test.go`
- Modify: `internal/api/router.go`
- Modify: `cmd/server/main.go`

- [ ] **Step 1: Write new-api client tests**

Add these tests to `internal/integrations/newapi/client_test.go`:

```go
// TestListModelsPrefersDashboardEndpoint 验证模型列表优先解析 new-api Dashboard 模型映射接口。
func TestListModelsPrefersDashboardEndpoint(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/models", r.URL.Path)
		require.Equal(t, "Bearer admin-token", r.Header.Get("Authorization"))
		require.Equal(t, "1", r.Header.Get("New-Api-User"))
		_, _ = w.Write([]byte(`{"success":true,"data":{"1":["qwen2.5:7b","deepseek-r1:14b"],"2":["qwen2.5:7b"]}}`))
	}))
	t.Cleanup(server.Close)

	client := NewClient(server.URL, "admin-token", 1)
	models, err := client.ListModels(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []Model{{ID: "deepseek-r1:14b", Name: "deepseek-r1:14b"}, {ID: "qwen2.5:7b", Name: "qwen2.5:7b"}}, models)
}

// TestListModelsFallsBackToOpenAIEndpoint 验证 Dashboard 模型接口不可用时兼容 OpenAI 模型列表。
func TestListModelsFallsBackToOpenAIEndpoint(t *testing.T) {
	t.Parallel()
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if r.URL.Path == "/api/models" {
			http.NotFound(w, r)
			return
		}
		require.Equal(t, "/v1/models", r.URL.Path)
		_, _ = w.Write([]byte(`{"data":[{"id":"b-model"},{"id":"a-model"}]}`))
	}))
	t.Cleanup(server.Close)

	client := NewClient(server.URL, "admin-token", 1)
	models, err := client.ListModels(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []string{"/api/models", "/v1/models"}, paths)
	assert.Equal(t, []Model{{ID: "a-model", Name: "a-model"}, {ID: "b-model", Name: "b-model"}}, models)
}
```

- [ ] **Step 2: Implement new-api client model listing**

Add to `internal/integrations/newapi/client.go`:

```go
// Model 描述 new-api 当前暴露的模型。
type Model struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// ListModels 实时查询 new-api 当前可用模型列表。
func (c *Client) ListModels(ctx context.Context) ([]Model, error) {
	models, err := c.listDashboardModels(ctx)
	if err == nil {
		return models, nil
	}
	if !errors.Is(err, ErrNotFound) && !errors.Is(err, ErrUnauthorized) {
		return nil, err
	}
	return c.listOpenAIModels(ctx)
}

func (c *Client) listDashboardModels(ctx context.Context) ([]Model, error) {
	var response struct {
		Success bool                `json:"success"`
		Message string              `json:"message"`
		Data    map[string][]string `json:"data"`
	}
	if err := c.do(ctx, http.MethodGet, "/api/models", nil, &response); err != nil {
		return nil, err
	}
	if !response.Success {
		return nil, fmt.Errorf("%w: %s", ErrUpstream, response.Message)
	}
	set := make(map[string]struct{})
	for _, names := range response.Data {
		for _, name := range names {
			name = strings.TrimSpace(name)
			if name != "" {
				set[name] = struct{}{}
			}
		}
	}
	return sortedModels(set), nil
}

func (c *Client) listOpenAIModels(ctx context.Context) ([]Model, error) {
	var response struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := c.do(ctx, http.MethodGet, "/v1/models", nil, &response); err != nil {
		return nil, err
	}
	set := make(map[string]struct{})
	for _, item := range response.Data {
		id := strings.TrimSpace(item.ID)
		if id != "" {
			set[id] = struct{}{}
		}
	}
	return sortedModels(set), nil
}

func sortedModels(set map[string]struct{}) []Model {
	names := make([]string, 0, len(set))
	for name := range set {
		names = append(names, name)
	}
	sort.Strings(names)
	models := make([]Model, 0, len(names))
	for _, name := range names {
		models = append(models, Model{ID: name, Name: name})
	}
	return models
}
```

Add `sort` to the imports.

- [ ] **Step 3: Write model service tests**

Create `internal/service/model_service_test.go`:

```go
package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"oc-manager/internal/auth"
	"oc-manager/internal/integrations/newapi"
)

type fakeModelCatalog struct {
	models []newapi.Model
	err    error
}

func (f fakeModelCatalog) ListModels(context.Context) ([]newapi.Model, error) {
	return f.models, f.err
}

// TestModelCatalogServiceListRequiresPlatformAdmin 验证模型列表管理接口只允许平台管理员读取。
func TestModelCatalogServiceListRequiresPlatformAdmin(t *testing.T) {
	t.Parallel()
	svc := NewModelCatalogService(fakeModelCatalog{models: []newapi.Model{{ID: "qwen", Name: "qwen"}}})
	_, err := svc.List(context.Background(), auth.Principal{Role: "org_admin"})
	require.ErrorIs(t, err, ErrForbidden)
}

// TestModelCatalogServiceListReturnsModels 验证平台管理员可以读取实时模型列表。
func TestModelCatalogServiceListReturnsModels(t *testing.T) {
	t.Parallel()
	svc := NewModelCatalogService(fakeModelCatalog{models: []newapi.Model{{ID: "qwen", Name: "qwen"}}})
	got, err := svc.List(context.Background(), auth.Principal{Role: "platform_admin"})
	require.NoError(t, err)
	assert.Equal(t, []ModelResult{{ID: "qwen", Name: "qwen"}}, got)
}

// TestValidateModelIDsRejectsMissingModel 验证组织提交的模型必须来自实时模型列表。
func TestValidateModelIDsRejectsMissingModel(t *testing.T) {
	t.Parallel()
	svc := NewModelCatalogService(fakeModelCatalog{models: []newapi.Model{{ID: "qwen", Name: "qwen"}}})
	_, err := svc.ValidateModelIDs(context.Background(), []string{"missing"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "模型 missing 不存在")
}

// TestModelCatalogServiceSurfacesUpstreamFailure 验证 new-api 不可用时阻止上层继续提交。
func TestModelCatalogServiceSurfacesUpstreamFailure(t *testing.T) {
	t.Parallel()
	svc := NewModelCatalogService(fakeModelCatalog{err: errors.New("upstream down")})
	_, err := svc.ValidateModelIDs(context.Background(), []string{"qwen"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "查询模型列表失败")
}
```

- [ ] **Step 4: Implement model service**

Create `internal/service/model_service.go`:

```go
package service

import (
	"context"
	"fmt"
	"strings"

	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/integrations/newapi"
)

// ModelCatalog 抽象 new-api 实时模型列表，便于 service 单测注入。
type ModelCatalog interface {
	ListModels(ctx context.Context) ([]newapi.Model, error)
}

// ModelResult 是 manager API 返回给前端的模型视图。
type ModelResult struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// ModelCatalogService 负责读取和校验 new-api 实时模型列表。
type ModelCatalogService struct {
	catalog ModelCatalog
}

// NewModelCatalogService 创建模型目录服务。
func NewModelCatalogService(catalog ModelCatalog) *ModelCatalogService {
	return &ModelCatalogService{catalog: catalog}
}

// List 返回当前 new-api 可用模型，仅平台管理员可读。
func (s *ModelCatalogService) List(ctx context.Context, principal auth.Principal) ([]ModelResult, error) {
	if principal.Role != domain.UserRolePlatformAdmin {
		return nil, ErrForbidden
	}
	return s.list(ctx)
}

// ValidateModelIDs 校验组织模型 allowlist 非空、去重并且全部存在于实时模型列表。
func (s *ModelCatalogService) ValidateModelIDs(ctx context.Context, input []string) ([]string, error) {
	models, err := s.list(ctx)
	if err != nil {
		return nil, err
	}
	available := make(map[string]struct{}, len(models))
	for _, model := range models {
		available[model.ID] = struct{}{}
	}
	seen := make(map[string]struct{}, len(input))
	out := make([]string, 0, len(input))
	for _, raw := range input {
		id := strings.TrimSpace(raw)
		if id == "" {
			continue
		}
		if _, ok := available[id]; !ok {
			return nil, fmt.Errorf("%w: 模型 %s 不存在", ErrMemberCreateInvalid, id)
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%w: 至少选择一个可用模型", ErrMemberCreateInvalid)
	}
	return out, nil
}

func (s *ModelCatalogService) list(ctx context.Context) ([]ModelResult, error) {
	if s.catalog == nil {
		return nil, fmt.Errorf("模型目录未配置")
	}
	models, err := s.catalog.ListModels(ctx)
	if err != nil {
		return nil, fmt.Errorf("查询模型列表失败: %w", err)
	}
	out := make([]ModelResult, 0, len(models))
	for _, model := range models {
		id := strings.TrimSpace(model.ID)
		if id == "" {
			continue
		}
		name := strings.TrimSpace(model.Name)
		if name == "" {
			name = id
		}
		out = append(out, ModelResult{ID: id, Name: name})
	}
	return out, nil
}
```

- [ ] **Step 5: Add model handler tests and handler**

Create `internal/api/handlers/models_test.go` with one success and one forbidden case:

```go
// TestModelsListReturnsCatalog 验证平台管理员可通过 manager 读取实时模型列表。
func TestModelsListReturnsCatalog(t *testing.T) {
	t.Parallel()
	svc := &modelServiceStub{models: []service.ModelResult{{ID: "qwen", Name: "qwen"}}}
	router, tokens := newModelsTestRouter(t, svc)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/models", nil)
	req.Header.Set("Authorization", "Bearer "+mustAccessToken(t, tokens, auth.Principal{UserID: "u-1", Role: "platform_admin"}))
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)
	require.Equal(t, http.StatusOK, resp.Code)
	assert.JSONEq(t, `{"models":[{"id":"qwen","name":"qwen"}]}`, resp.Body.String())
}

// TestModelsListMapsForbidden 验证非平台管理员读取模型列表时返回 403。
func TestModelsListMapsForbidden(t *testing.T) {
	t.Parallel()
	svc := &modelServiceStub{err: service.ErrForbidden}
	router, tokens := newModelsTestRouter(t, svc)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/models", nil)
	req.Header.Set("Authorization", "Bearer "+mustAccessToken(t, tokens, auth.Principal{UserID: "u-1", Role: "org_admin"}))
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)
	require.Equal(t, http.StatusForbidden, resp.Code)
}
```

Create `internal/api/handlers/models.go`:

```go
package handlers

import (
	"context"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"oc-manager/internal/auth"
	"oc-manager/internal/service"
)

// ModelsHandler 暴露 manager 代理的实时模型列表。
type ModelsHandler struct {
	service modelService
	tokens  *auth.TokenManager
}

type modelService interface {
	List(ctx context.Context, principal auth.Principal) ([]service.ModelResult, error)
}

func NewModelsHandler(svc modelService, tokens *auth.TokenManager) *ModelsHandler {
	return &ModelsHandler{service: svc, tokens: tokens}
}

func RegisterModelRoutes(router gin.IRouter, handler *ModelsHandler) {
	router.GET("/api/v1/models", handler.List)
}

// List 返回 new-api 当前可用模型列表。
//
// @Summary      模型列表
// @Description  平台管理员实时查询 new-api 当前可用模型，供组织模型 allowlist 使用
// @Tags         models
// @Produce      json
// @Security     BearerAuth
// @Success      200  {object}  map[string][]service.ModelResult
// @Failure      401  {object}  ErrorResponse
// @Failure      403  {object}  ErrorResponse
// @Failure      500  {object}  ErrorResponse
// @Router       /models [get]
func (h *ModelsHandler) List(c *gin.Context) {
	principal, ok := h.principal(c)
	if !ok {
		return
	}
	models, err := h.service.List(c.Request.Context(), principal)
	if err != nil {
		if errors.Is(err, service.ErrForbidden) {
			c.JSON(http.StatusForbidden, gin.H{"error": "无权查看模型列表"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "模型列表暂时不可用"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"models": models})
}

func (h *ModelsHandler) principal(c *gin.Context) (auth.Principal, bool) {
	token, ok := bearerToken(c.GetHeader("Authorization"))
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "缺少访问令牌"})
		return auth.Principal{}, false
	}
	principal, err := h.tokens.VerifyAccessToken(token)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "访问令牌无效"})
		return auth.Principal{}, false
	}
	return principal, true
}
```

- [ ] **Step 6: Wire the route**

In `internal/api/router.go`, add a dependency field:

```go
// ModelCatalogService 提供 new-api 实时模型列表路由。
ModelCatalogService *service.ModelCatalogService
```

Register it before organization routes:

```go
if dep.ModelCatalogService != nil {
	handlers.RegisterModelRoutes(router, handlers.NewModelsHandler(dep.ModelCatalogService, dep.TokenManager))
}
```

In `cmd/server/main.go`, declare `var modelCatalogService *service.ModelCatalogService`, set it when new-api is configured:

```go
modelCatalogService = service.NewModelCatalogService(newapiClient)
```

Pass it into `api.Dependencies{ModelCatalogService: modelCatalogService}`.

- [ ] **Step 7: Run focused tests and commit**

Run:

```bash
rtk go test ./internal/integrations/newapi ./internal/service ./internal/api/handlers -count=1
```

Expected: all packages pass.

Commit:

```bash
rtk git add internal/integrations/newapi/client.go internal/integrations/newapi/client_test.go \
  internal/service/model_service.go internal/service/model_service_test.go \
  internal/api/handlers/models.go internal/api/handlers/models_test.go \
  internal/api/router.go cmd/server/main.go
rtk git commit -m "feat(model): 增加实时模型列表接口" \
  -m "通过 manager 代理 new-api 模型列表，并增加模型目录 service 和 /api/v1/models 路由，供组织模型配置使用。"
```

## Task 3: Organization Allowlist Backend

**Files:**
- Modify: `internal/api/handlers/dto.go`
- Modify: `internal/api/handlers/organizations.go`
- Modify: `internal/api/handlers/organizations_test.go`
- Modify: `internal/service/organization_service.go`
- Modify: `internal/service/organization_service_test.go`

- [ ] **Step 1: Add request fields**

In `internal/api/handlers/dto.go`, add to both `CreateOrganizationRequest` and `OrganizationRequest`:

```go
// EnabledModels 是该组织允许在 manager 内选择的模型列表；new-api token 不使用该字段限权。
EnabledModels []string `json:"enabled_models"`
```

In `internal/api/handlers/organizations.go`, pass `req.EnabledModels` into `service.OrganizationInput`.

- [ ] **Step 2: Extend service types and helpers**

In `internal/service/organization_service.go`, add to `OrganizationInput`:

```go
// EnabledModels 是组织可用模型 allowlist，创建和更新时必须至少包含一个实时存在的模型。
EnabledModels []string
```

Add to `OrganizationResult`:

```go
// EnabledModels 是组织在 manager 层允许创建实例时选择的模型列表。
EnabledModels []string `json:"enabled_models"`
```

Add helpers:

```go
func modelListJSON(models []string) ([]byte, error) {
	data, err := json.Marshal(models)
	if err != nil {
		return nil, fmt.Errorf("序列化组织可用模型失败: %w", err)
	}
	return data, nil
}

func modelListFromJSON(data []byte) []string {
	if len(data) == 0 {
		return nil
	}
	var models []string
	if err := json.Unmarshal(data, &models); err != nil {
		return nil
	}
	return models
}
```

- [ ] **Step 3: Inject model validator into OrganizationService**

Add this interface:

```go
// OrganizationModelValidator 抽象模型列表校验能力，避免 OrganizationService 直接依赖具体实现。
type OrganizationModelValidator interface {
	ValidateModelIDs(ctx context.Context, input []string) ([]string, error)
}
```

Update struct and constructor:

```go
modelValidator OrganizationModelValidator
```

```go
func (s *OrganizationService) SetModelValidator(validator OrganizationModelValidator) {
	s.modelValidator = validator
}
```

Use this method in `cmd/server/main.go` after both services are constructed:

```go
if modelCatalogService != nil {
	organizationService.SetModelValidator(modelCatalogService)
}
```

- [ ] **Step 4: Validate and persist on create**

In `CreateOrganization`, before `CreateOrganizationParams`, add:

```go
enabledModels, err := s.validateEnabledModels(ctx, input.EnabledModels)
if err != nil {
	return OrganizationResult{}, err
}
enabledModelsJSON, err := modelListJSON(enabledModels)
if err != nil {
	return OrganizationResult{}, err
}
```

Set `EnabledModels: enabledModelsJSON` in `sqlc.CreateOrganizationParams`.

Add method:

```go
func (s *OrganizationService) validateEnabledModels(ctx context.Context, input []string) ([]string, error) {
	if s.modelValidator == nil {
		return nil, fmt.Errorf("模型校验器未配置，无法保存组织模型")
	}
	return s.modelValidator.ValidateModelIDs(ctx, input)
}
```

- [ ] **Step 5: Block removal of models in use**

Extend `OrganizationStore` with:

```go
CountActiveAppsByOrgAndModels(ctx context.Context, arg sqlc.CountActiveAppsByOrgAndModelsParams) ([]sqlc.CountActiveAppsByOrgAndModelsRow, error)
```

In `UpdateOrganization`, load the current org before update:

```go
current, err := s.store.GetOrganization(ctx, id)
if errors.Is(err, pgx.ErrNoRows) {
	return OrganizationResult{}, ErrNotFound
}
if err != nil {
	return OrganizationResult{}, fmt.Errorf("查询组织失败: %w", err)
}
enabledModels, err := s.validateEnabledModels(ctx, input.EnabledModels)
if err != nil {
	return OrganizationResult{}, err
}
if err := s.ensureRemovedModelsUnused(ctx, id, modelListFromJSON(current.EnabledModels), enabledModels); err != nil {
	return OrganizationResult{}, err
}
enabledModelsJSON, err := modelListJSON(enabledModels)
if err != nil {
	return OrganizationResult{}, err
}
```

Add:

```go
func (s *OrganizationService) ensureRemovedModelsUnused(ctx context.Context, orgID pgtype.UUID, oldModels, newModels []string) error {
	newSet := make(map[string]struct{}, len(newModels))
	for _, model := range newModels {
		newSet[model] = struct{}{}
	}
	removed := make([]string, 0)
	for _, model := range oldModels {
		if _, ok := newSet[model]; !ok {
			removed = append(removed, model)
		}
	}
	if len(removed) == 0 {
		return nil
	}
	rows, err := s.store.CountActiveAppsByOrgAndModels(ctx, sqlc.CountActiveAppsByOrgAndModelsParams{
		OrgID: orgID,
		ModelID: removed,
	})
	if err != nil {
		return fmt.Errorf("查询模型使用情况失败: %w", err)
	}
	if len(rows) == 0 {
		return nil
	}
	return fmt.Errorf("%w: 模型 %s 已被 %d 个实例使用，请先切换实例模型", ErrConflict, rows[0].ModelID, rows[0].AppCount)
}
```

Use `EnabledModels: enabledModelsJSON` in `UpdateOrganizationProfileParams`.

- [ ] **Step 6: Return enabled models**

In `toOrganizationResult`, add:

```go
EnabledModels: modelListFromJSON(org.EnabledModels),
```

- [ ] **Step 7: Add service tests**

Update `internal/service/organization_service_test.go` with this fake validator:

```go
type orgModelValidatorStub struct {
	models []string
	err    error
}

func (s orgModelValidatorStub) ValidateModelIDs(context.Context, []string) ([]string, error) {
	return s.models, s.err
}
```

Add `TestCreateOrganizationRequiresEnabledModels` with this test body:

```go
// TestCreateOrganizationRequiresEnabledModels 验证创建组织必须通过实时模型列表校验。
func TestCreateOrganizationRequiresEnabledModels(t *testing.T) {
	t.Parallel()
	store := newOrganizationStoreStub()
	svc := NewOrganizationService(store, &newAPIProvisionerStub{}, testCipher(t), nil)
	svc.SetModelValidator(orgModelValidatorStub{err: fmt.Errorf("%w: 至少选择一个可用模型", ErrMemberCreateInvalid)})

	_, err := svc.CreateOrganization(context.Background(), platformAdminPrincipal(), OrganizationInput{
		Name: "测试组织", Code: "test-org", AdminUsername: "admin", AdminDisplayName: "管理员", AdminPassword: "admin123",
	})

	require.ErrorIs(t, err, ErrMemberCreateInvalid)
	assert.Empty(t, store.organizations)
}
```

Add `TestUpdateOrganizationRejectsRemovingModelInUse` with this test body:

```go
// TestUpdateOrganizationRejectsRemovingModelInUse 验证不能移除仍被未删除实例使用的模型。
func TestUpdateOrganizationRejectsRemovingModelInUse(t *testing.T) {
	t.Parallel()
	store := newOrganizationStoreStub()
	org := store.mustSeedOrganization(t, "test-org", []string{"qwen2.5:7b", "deepseek-r1:14b"})
	store.modelUsage = []sqlc.CountActiveAppsByOrgAndModelsRow{{ModelID: "qwen2.5:7b", AppCount: 1}}
	svc := NewOrganizationService(store, &newAPIProvisionerStub{}, testCipher(t), nil)
	svc.SetModelValidator(orgModelValidatorStub{models: []string{"deepseek-r1:14b"}})

	_, err := svc.UpdateOrganization(context.Background(), platformAdminPrincipal(), uuidToString(org.ID), OrganizationInput{
		Name: "测试组织", EnabledModels: []string{"deepseek-r1:14b"},
	})

	require.ErrorIs(t, err, ErrConflict)
	assert.Contains(t, err.Error(), "qwen2.5:7b")
}
```

If the existing store stub uses different helper names, add `mustSeedOrganization` and `modelUsage` fields directly to that stub in the same test file.

- [ ] **Step 8: Run focused tests and commit**

Run:

```bash
rtk go test ./internal/service ./internal/api/handlers ./cmd/server -count=1
```

Expected: all packages pass.

Commit:

```bash
rtk git add internal/api/handlers/dto.go internal/api/handlers/organizations.go \
  internal/api/handlers/organizations_test.go internal/service/organization_service.go \
  internal/service/organization_service_test.go cmd/server/main.go
rtk git commit -m "feat(model): 支持组织可用模型配置" \
  -m "组织创建和更新保存 enabled_models，并通过实时模型列表校验，阻止移除正在被实例使用的模型。"
```

## Task 4: Instance Creation And Initialization Model

**Files:**
- Modify: `internal/api/handlers/dto.go`
- Modify: `internal/api/handlers/members.go`
- Modify: `internal/api/handlers/members_test.go`
- Modify: `internal/service/onboarding_service.go`
- Modify: `internal/service/onboarding_service_test.go`
- Modify: `internal/service/app_service.go`
- Modify: `internal/worker/handlers/app_initialize.go`
- Modify: `internal/worker/handlers/app_initialize_test.go`

- [ ] **Step 1: Add request model fields**

In `OnboardMemberRequest` and `CreateMemberAppRequest`, add:

```go
// ModelID 是新实例使用的模型，必须属于组织 enabled_models。
ModelID string `json:"model_id" binding:"required"`
```

Pass `req.ModelID` into `service.OnboardMemberInput` and `service.CreateAppForMemberInput`.

- [ ] **Step 2: Extend onboarding inputs**

In `internal/service/onboarding_service.go`, add `ModelID string` to both input structs:

```go
// ModelID 是实例当前使用的模型，必须属于组织模型 allowlist。
ModelID string
```

Add helper:

```go
func ensureModelAllowed(org sqlc.Organization, modelID string) error {
	modelID = strings.TrimSpace(modelID)
	if modelID == "" {
		return fmt.Errorf("%w: 模型不能为空", ErrMemberCreateInvalid)
	}
	for _, allowed := range modelListFromJSON(org.EnabledModels) {
		if allowed == modelID {
			return nil
		}
	}
	return fmt.Errorf("%w: 模型 %s 不在组织可用模型列表中", ErrMemberCreateInvalid, modelID)
}
```

Add `strings` to imports.

- [ ] **Step 3: Validate and persist app model**

In both `OnboardMember` and `CreateAppForMember`, after loading `org` and checking status, add:

```go
if err := ensureModelAllowed(org, input.ModelID); err != nil {
	return err
}
```

Set `ModelID: input.ModelID` in each `sqlc.CreateAppParams`.

- [ ] **Step 4: Return model in AppResult**

In `internal/service/app_service.go`, add:

```go
ModelID string `json:"model_id"`
```

Set in `toAppResult`:

```go
ModelID: app.ModelID,
```

- [ ] **Step 5: Use app model during initialization**

In `internal/worker/handlers/app_initialize.go`, replace `h.cfg.LLM.DefaultModel` usage when configuring OpenClaw:

```go
llm := h.cfg.LLM
llm.DefaultModel = app.ModelID
if execer, ok := h.starter.(ContainerExecer); ok && llm.DefaultModel != "" {
	if err := configureOpenClawDefaultModel(ctx, execer, payload.RuntimeNodeID, info.ID, llm); err != nil {
		slog.WarnContext(ctx, "app_initialize: 配置 openclaw 默认 model 失败", "app_id", uuidToString(app.ID), "model_id", app.ModelID, "error", err)
	}
}
```

Keep `CreateAPIKey` unchanged:

```go
Models: []string{},
```

- [ ] **Step 6: Add tests**

Update onboarding tests with concrete assertions:

```go
// TestOnboardMemberRejectsModelOutsideOrgAllowlist 验证创建实例时模型必须属于组织 allowlist。
func TestOnboardMemberRejectsModelOutsideOrgAllowlist(t *testing.T) {
	t.Parallel()
	svc, store := newOnboardingServiceWithStore(t)
	store.organization.EnabledModels = []byte(`["qwen2.5:7b"]`)

	_, err := svc.OnboardMember(context.Background(), orgAdminPrincipal(store.organization), uuidToString(store.organization.ID), OnboardMemberInput{
		Username: "alice", DisplayName: "Alice", Password: "password123", AppName: "alice-bot", ModelID: "deepseek-r1:14b",
	})

	require.ErrorIs(t, err, ErrMemberCreateInvalid)
	assert.Empty(t, store.apps)
}

// TestCreateAppForMemberStoresModelID 验证为已有成员创建实例时保存选定模型。
func TestCreateAppForMemberStoresModelID(t *testing.T) {
	t.Parallel()
	svc, store := newOnboardingServiceWithStore(t)
	store.organization.EnabledModels = []byte(`["qwen2.5:7b"]`)
	member := store.mustSeedMember(t, "alice")

	result, err := svc.CreateAppForMember(context.Background(), platformAdminPrincipal(), uuidToString(store.organization.ID), uuidToString(member.ID), CreateAppForMemberInput{
		AppName: "alice-bot", ModelID: "qwen2.5:7b",
	})

	require.NoError(t, err)
	require.NotEmpty(t, result.App.ID)
	assert.Equal(t, "qwen2.5:7b", store.apps[0].ModelID)
}
```

Update app initialize tests with concrete assertions:

```go
// TestHandleConfiguresAppModelID 验证初始化使用 app.model_id 而不是全局默认模型。
func TestHandleConfiguresAppModelID(t *testing.T) {
	t.Parallel()
	store, containers, starter := newAppInitializeHarness(t)
	store.app.ModelID = "deepseek-r1:14b"
	handler := newTestAppInitializeHandler(store, containers, starter)
	handler.cfg.LLM.DefaultModel = "qwen2.5:7b"

	err := handler.Handle(context.Background(), appInitializeJobFor(store.app.ID))

	require.NoError(t, err)
	assert.Contains(t, starter.lastExecCommand, "deepseek-r1:14b")
	assert.NotContains(t, starter.lastExecCommand, "qwen2.5:7b")
}

// TestEnsureAPIKeyKeepsNewAPITokenModelsUnrestricted 验证 new-api token 创建仍不限制模型。
func TestEnsureAPIKeyKeepsNewAPITokenModelsUnrestricted(t *testing.T) {
	t.Parallel()
	handler, api := newEnsureAPIKeyHarness(t)
	handler.store.app.ModelID = "deepseek-r1:14b"

	_, err := handler.ensureAPIKey(context.Background(), &handler.store.app)

	require.NoError(t, err)
	assert.Empty(t, api.lastCreateInput.Models)
}
```

If the existing test harness names differ, add the shown helper behavior inside the same test files so each assertion remains exactly the same.

- [ ] **Step 7: Run focused tests and commit**

Run:

```bash
rtk go test ./internal/service ./internal/api/handlers ./internal/worker/handlers -count=1
```

Expected: all packages pass.

Commit:

```bash
rtk git add internal/api/handlers/dto.go internal/api/handlers/members.go \
  internal/api/handlers/members_test.go internal/service/onboarding_service.go \
  internal/service/onboarding_service_test.go internal/service/app_service.go \
  internal/worker/handlers/app_initialize.go internal/worker/handlers/app_initialize_test.go
rtk git commit -m "feat(model): 创建实例时保存模型" \
  -m "成员开户和实例复建请求增加 model_id 校验，应用初始化改为按 app.model_id 注入 OpenClaw 配置。"
```

## Task 5: App Model Update API And Restart Job

**Files:**
- Modify: `internal/service/app_service.go`
- Modify: `internal/service/app_service_test.go`
- Modify: `internal/api/handlers/dto.go`
- Modify: `internal/api/handlers/apps.go`
- Modify: `internal/api/handlers/apps_test.go`
- Modify: `internal/api/router.go`

- [ ] **Step 1: Add DTO**

In `internal/api/handlers/dto.go`, add:

```go
// UpdateAppModelRequest 修改实例模型的请求体。
type UpdateAppModelRequest struct {
	// ModelID 是目标模型，必须属于实例所属组织的 enabled_models。
	ModelID string `json:"model_id" binding:"required"`
}
```

- [ ] **Step 2: Extend AppStore and AppService**

In `internal/service/app_service.go`, extend `AppStore`:

```go
GetOrganization(ctx context.Context, id pgtype.UUID) (sqlc.Organization, error)
SetAppModel(ctx context.Context, arg sqlc.SetAppModelParams) (sqlc.App, error)
CreateJob(ctx context.Context, arg sqlc.CreateJobParams) (sqlc.Job, error)
CreateAuditLog(ctx context.Context, arg sqlc.CreateAuditLogParams) (sqlc.AuditLog, error)
```

Add notifier support:

```go
notifier JobNotifier

func (s *AppService) SetJobNotifier(notifier JobNotifier) {
	s.notifier = notifier
}
```

Add result type:

```go
// AppModelUpdateResult 是修改实例模型后的响应。
type AppModelUpdateResult struct {
	App             AppResult `json:"app"`
	RestartJobID    string    `json:"restart_job_id,omitempty"`
	RequiresRestart bool      `json:"requires_restart"`
}
```

- [ ] **Step 3: Implement UpdateModel**

Add to `AppService`:

```go
// UpdateModel 修改实例模型；运行中实例会提交重启任务让模型生效。
func (s *AppService) UpdateModel(ctx context.Context, principal auth.Principal, appID, modelID string) (AppModelUpdateResult, error) {
	id, err := parseUUID(appID)
	if err != nil {
		return AppModelUpdateResult{}, ErrNotFound
	}
	app, err := s.store.GetApp(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) || app.DeletedAt.Valid {
		return AppModelUpdateResult{}, ErrNotFound
	}
	if err != nil {
		return AppModelUpdateResult{}, fmt.Errorf("查询应用失败: %w", err)
	}
	if !auth.CanTriggerRuntimeOperation(principal, uuidToString(app.OrgID), uuidToString(app.OwnerUserID)) {
		return AppModelUpdateResult{}, ErrForbidden
	}
	org, err := s.store.GetOrganization(ctx, app.OrgID)
	if err != nil {
		return AppModelUpdateResult{}, fmt.Errorf("查询组织失败: %w", err)
	}
	if err := ensureModelAllowed(org, modelID); err != nil {
		return AppModelUpdateResult{}, err
	}
	updated, err := s.store.SetAppModel(ctx, sqlc.SetAppModelParams{ID: app.ID, ModelID: modelID})
	if err != nil {
		return AppModelUpdateResult{}, fmt.Errorf("更新实例模型失败: %w", err)
	}
	result := AppModelUpdateResult{App: toAppResult(updated)}
	if !app.ContainerID.Valid || app.ContainerID.String == "" {
		return result, nil
	}
	payload, err := json.Marshal(map[string]any{
		"app_id":       uuidToString(app.ID),
		"operation":    string(RuntimeOperationRestart),
		"runtime_node": uuidToOptionalString(app.RuntimeNodeID),
		"requested_by": principal.UserID,
	})
	if err != nil {
		return AppModelUpdateResult{}, fmt.Errorf("序列化重启任务 payload 失败: %w", err)
	}
	job, err := s.store.CreateJob(ctx, sqlc.CreateJobParams{
		Type:        domain.JobTypeAppRestartContainer,
		Priority:    100,
		RunAfter:    pgtype.Timestamptz{Time: time.Now(), Valid: true},
		MaxAttempts: 3,
		PayloadJson: payload,
	})
	if err != nil {
		return AppModelUpdateResult{}, fmt.Errorf("创建模型生效重启任务失败: %w", err)
	}
	actorUUID, _ := optionalUUID(principal.UserID)
	metadata, _ := json.Marshal(map[string]any{"old_model_id": app.ModelID, "new_model_id": modelID, "restart_job_id": uuidToString(job.ID)})
	if _, err := s.store.CreateAuditLog(ctx, sqlc.CreateAuditLogParams{
		ActorID:      actorUUID,
		ActorRole:    principal.Role,
		OrgID:        app.OrgID,
		TargetType:   "app",
		TargetID:     uuidToString(app.ID),
		Action:       "update_model",
		Result:       "succeeded",
		MetadataJson: metadata,
	}); err != nil {
		return AppModelUpdateResult{}, fmt.Errorf("写入模型修改审计日志失败: %w", err)
	}
	result.RestartJobID = uuidToString(job.ID)
	result.RequiresRestart = true
	if s.notifier != nil {
		_ = s.notifier.Enqueue(ctx, result.RestartJobID)
	}
	return result, nil
}
```

If `RuntimeOperationRestart` is not in `service` package scope for `app_service.go`, use string literal `"restart"` in payload and add a local comment explaining it mirrors the runtime operation payload.

- [ ] **Step 4: Add handler route**

Extend `appService` interface in `internal/api/handlers/apps.go`:

```go
UpdateModel(ctx context.Context, principal auth.Principal, appID, modelID string) (service.AppModelUpdateResult, error)
```

Register:

```go
router.PATCH("/api/v1/apps/:appId/model", handler.UpdateModel)
```

Add handler method:

```go
// UpdateModel 修改实例模型并在需要时提交重启任务。
//
// @Summary      修改实例模型
// @Description  更新实例模型；已有容器的实例会提交重启任务让新模型生效
// @Tags         apps
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        appId  path      string                 true  "应用 ID"
// @Param        body   body      UpdateAppModelRequest  true  "修改模型请求"
// @Success      200    {object}  service.AppModelUpdateResult
// @Failure      400    {object}  ErrorResponse
// @Failure      401    {object}  ErrorResponse
// @Failure      403    {object}  ErrorResponse
// @Failure      404    {object}  ErrorResponse
// @Failure      500    {object}  ErrorResponse
// @Router       /apps/{appId}/model [patch]
func (h *AppsHandler) UpdateModel(c *gin.Context) {
	principal, ok := h.principal(c)
	if !ok {
		return
	}
	var req UpdateAppModelRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeBindError(c, err)
		return
	}
	result, err := h.service.UpdateModel(c.Request.Context(), principal, c.Param("appId"), req.ModelID)
	if err != nil {
		writeAppsError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}
```

Update `writeAppsError` to map `ErrMemberCreateInvalid` to 400:

```go
case errors.Is(err, service.ErrMemberCreateInvalid):
	c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
```

- [ ] **Step 5: Wire notifier**

In `cmd/server/main.go`, after creating `appService`:

```go
appService.SetJobNotifier(redisQueue)
```

- [ ] **Step 6: Add tests**

Add app service tests with these bodies:

```go
// TestUpdateModelRejectsOutsideAllowlist 验证实例模型必须属于组织 allowlist。
func TestUpdateModelRejectsOutsideAllowlist(t *testing.T) {
	t.Parallel()
	svc, store := newAppServiceWithStore(t)
	store.organization.EnabledModels = []byte(`["qwen2.5:7b"]`)
	app := store.mustSeedApp(t, "qwen2.5:7b")

	_, err := svc.UpdateModel(context.Background(), orgAdminPrincipal(store.organization), uuidToString(app.ID), "deepseek-r1:14b")

	require.ErrorIs(t, err, ErrMemberCreateInvalid)
	assert.Empty(t, store.jobs)
}

// TestUpdateModelCreatesRestartJobForContainer 验证有容器实例修改模型后提交重启任务。
func TestUpdateModelCreatesRestartJobForContainer(t *testing.T) {
	t.Parallel()
	svc, store := newAppServiceWithStore(t)
	store.organization.EnabledModels = []byte(`["qwen2.5:7b","deepseek-r1:14b"]`)
	app := store.mustSeedApp(t, "qwen2.5:7b")
	app.ContainerID = pgtype.Text{String: "container-1", Valid: true}
	store.app = app

	result, err := svc.UpdateModel(context.Background(), orgAdminPrincipal(store.organization), uuidToString(app.ID), "deepseek-r1:14b")

	require.NoError(t, err)
	assert.Equal(t, "deepseek-r1:14b", result.App.ModelID)
	assert.True(t, result.RequiresRestart)
	assert.NotEmpty(t, result.RestartJobID)
	require.Len(t, store.jobs, 1)
	assert.Equal(t, domain.JobTypeAppRestartContainer, store.jobs[0].Type)
}

// TestUpdateModelWithoutContainerOnlySavesModel 验证未创建容器的实例只保存模型不提交重启任务。
func TestUpdateModelWithoutContainerOnlySavesModel(t *testing.T) {
	t.Parallel()
	svc, store := newAppServiceWithStore(t)
	store.organization.EnabledModels = []byte(`["qwen2.5:7b","deepseek-r1:14b"]`)
	app := store.mustSeedApp(t, "qwen2.5:7b")

	result, err := svc.UpdateModel(context.Background(), orgAdminPrincipal(store.organization), uuidToString(app.ID), "deepseek-r1:14b")

	require.NoError(t, err)
	assert.Equal(t, "deepseek-r1:14b", result.App.ModelID)
	assert.False(t, result.RequiresRestart)
	assert.Empty(t, result.RestartJobID)
	assert.Empty(t, store.jobs)
}
```

Add handler tests with these bodies:

```go
// TestAppsUpdateModelForwardsRequest 验证模型修改路由转发 appId 和 model_id。
func TestAppsUpdateModelForwardsRequest(t *testing.T) {
	t.Parallel()
	svc := &appServiceStub{modelResult: service.AppModelUpdateResult{App: service.AppResult{ID: "app-1", ModelID: "qwen2.5:7b"}}}
	router, tokens := newAppsTestRouter(t, svc)
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/apps/app-1/model", strings.NewReader(`{"model_id":"qwen2.5:7b"}`))
	req.Header.Set("Authorization", "Bearer "+mustAccessToken(t, tokens, auth.Principal{UserID: "u-1", Role: "org_admin", OrgID: "org-1"}))
	resp := httptest.NewRecorder()

	router.ServeHTTP(resp, req)

	require.Equal(t, http.StatusOK, resp.Code)
	assert.Equal(t, "app-1", svc.lastAppID)
	assert.Equal(t, "qwen2.5:7b", svc.lastModelID)
}

// TestAppsUpdateModelMapsInvalidModel 验证非法模型返回 400。
func TestAppsUpdateModelMapsInvalidModel(t *testing.T) {
	t.Parallel()
	svc := &appServiceStub{err: service.ErrMemberCreateInvalid}
	router, tokens := newAppsTestRouter(t, svc)
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/apps/app-1/model", strings.NewReader(`{"model_id":"missing"}`))
	req.Header.Set("Authorization", "Bearer "+mustAccessToken(t, tokens, auth.Principal{UserID: "u-1", Role: "org_admin", OrgID: "org-1"}))
	resp := httptest.NewRecorder()

	router.ServeHTTP(resp, req)

	require.Equal(t, http.StatusBadRequest, resp.Code)
}
```

- [ ] **Step 7: Run focused tests and commit**

Run:

```bash
rtk go test ./internal/service ./internal/api/handlers ./cmd/server -count=1
```

Expected: all packages pass.

Commit:

```bash
rtk git add internal/service/app_service.go internal/service/app_service_test.go \
  internal/api/handlers/dto.go internal/api/handlers/apps.go internal/api/handlers/apps_test.go \
  internal/api/router.go cmd/server/main.go
rtk git commit -m "feat(model): 支持修改实例模型" \
  -m "应用模型修改接口校验组织 allowlist，保存 app.model_id，并在已有容器时提交重启任务让模型生效。"
```

## Task 6: Frontend Model Selection And Update

**Files:**
- Modify: `web/src/api/hooks/useOrganizations.ts`
- Modify: `web/src/api/hooks/useMembers.ts`
- Modify: `web/src/api/hooks/useApps.ts`
- Modify: `web/src/api/index.ts`
- Modify: `web/src/pages/platform/OrganizationsPage.vue`
- Modify: `web/src/pages/platform/OrganizationsPage.spec.ts`
- Modify: `web/src/pages/org/CreateMemberPage.vue`
- Modify: `web/src/pages/org/MembersPage.vue`
- Modify: `web/src/pages/org/MembersPage.spec.ts`
- Modify: `web/src/pages/apps/AppOverviewTab.vue`
- Modify: `web/src/pages/apps/AppOverviewTab.spec.ts`

- [ ] **Step 1: Add frontend API types and hooks**

In `web/src/api/hooks/useOrganizations.ts`, add:

```ts
export interface ModelOptionDTO {
  id: string
  name: string
}

export function useModelsQuery(enabled?: () => boolean) {
  return useQuery<ModelOptionDTO[]>({
    queryKey: ['models'],
    enabled,
    queryFn: async () => {
      const response = await apiRequest<{ models: ModelOptionDTO[] }>('/api/v1/models')
      return response.models
    },
  })
}
```

Add `enabled_models: string[]` to `OrganizationFormPayload`.

In `web/src/api/hooks/useMembers.ts`, add `model_id: string` to `OnboardMemberPayload` and `CreateMemberAppPayload`.

In `web/src/api/hooks/useApps.ts`, add to `AppDTO`:

```ts
// 实例当前使用的模型 ID。
model_id: string
```

Add:

```ts
export interface AppModelUpdateResult {
  app: AppDTO
  restart_job_id?: string
  requires_restart: boolean
}

export function useUpdateAppModel(appId: Ref<string | undefined>) {
  const client = useQueryClient()
  return useMutation({
    mutationFn: async (modelId: string) => {
      if (!appId.value) throw new Error('缺少实例 ID')
      return apiRequest<AppModelUpdateResult>(`/api/v1/apps/${appId.value}/model`, {
        method: 'PATCH',
        body: { model_id: modelId },
      })
    },
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: appKey(appId.value) })
      void client.invalidateQueries({ queryKey: runtimeKey(appId.value) })
    },
  })
}
```

- [ ] **Step 2: Update Organization alias**

In `web/src/api/index.ts`, require `code` and `enabled_models` on Organization:

```ts
export type Organization = WithRequired<
  Schemas['service.OrganizationResult'],
  'id' | 'name' | 'status' | 'code' | 'enabled_models'
>
```

- [ ] **Step 3: Add model multiselect to OrganizationsPage**

In `web/src/pages/platform/OrganizationsPage.vue`, import `useModelsQuery` and `NSelect` if missing.

Create query and options:

```ts
const modelsQuery = useModelsQuery(() => formVisible.value)
const modelOptions = computed(() => (modelsQuery.data.value ?? []).map(model => ({
  label: model.name,
  value: model.id,
})))
```

Add `enabled_models: [] as string[]` to form initial state and payload:

```ts
enabled_models: f.enabled_models,
```

Add form item:

```vue
<n-grid-item :span="2">
  <n-form-item label="可用模型 *">
    <n-select
      v-model:value="form.enabled_models"
      multiple
      filterable
      :loading="modelsQuery.isLoading.value"
      :disabled="modelsQuery.isError.value"
      :options="modelOptions"
      placeholder="选择组织可使用的模型"
    />
    <p v-if="modelsQuery.isError.value" class="state-text danger">模型列表获取失败，请重试</p>
  </n-form-item>
</n-grid-item>
```

Disable save when no model selected or query failed:

```vue
<n-button type="primary" attr-type="submit" :loading="creating" :disabled="modelsQuery.isError.value || form.enabled_models.length === 0">保存</n-button>
```

- [ ] **Step 4: Add model select to CreateMemberPage**

In `web/src/pages/org/CreateMemberPage.vue`, fetch organization and use its allowlist:

```ts
import { useOrganizationQuery } from '@/api/hooks/useOrganizations'

const organizationQuery = useOrganizationQuery(effectiveOrgId)
const modelOptions = computed(() => (organizationQuery.data.value?.enabled_models ?? []).map(model => ({
  label: model,
  value: model,
})))
```

Initialize `model_id: ''` and watch options:

```ts
watch(modelOptions, (options) => {
  if (!form.model_id && options.length > 0) form.model_id = String(options[0].value)
}, { immediate: true })
```

Add form item in instance info:

```vue
<n-grid-item>
  <n-form-item label="模型 *">
    <n-select v-model:value="form.model_id" :options="modelOptions" placeholder="选择模型" />
  </n-form-item>
</n-grid-item>
```

Disable submit when `!form.model_id`.

- [ ] **Step 5: Add model select to MembersPage rebuild form**

In `web/src/pages/org/MembersPage.vue`, reuse `useOrganizationQuery(effectiveOrgId)` and `modelOptions`.

Initialize `createAppForm.value` with `model_id`:

```ts
createAppForm.value = {
  app_name: '',
  persona_mode: 'org_inherited',
  channel_type: 'wechat',
  model_id: String(modelOptions.value[0]?.value ?? ''),
}
```

Add the same `<n-select>` form item and disable submit when no model is selected.

- [ ] **Step 6: Add model setting to AppOverviewTab**

In `web/src/pages/apps/AppOverviewTab.vue`, import:

```ts
import { NSelect } from 'naive-ui'
import { useUpdateAppModel } from '@/api/hooks/useApps'
```

Add model state:

```ts
const modelValue = ref('')
const modelMutation = useUpdateAppModel(appId)
const modelFeedback = ref('')
const modelError = ref(false)
const modelOptions = computed(() => (organizationQuery.data.value?.enabled_models ?? []).map(model => ({
  label: model,
  value: model,
})))

watch(() => app?.value?.model_id, (value) => {
  modelValue.value = value ?? ''
}, { immediate: true })

async function onUpdateModel() {
  modelFeedback.value = ''
  modelError.value = false
  try {
    const result = await modelMutation.mutateAsync(modelValue.value)
    if (result.restart_job_id) {
      trackingJobId.value = result.restart_job_id
      modelFeedback.value = `已提交模型生效重启任务：${result.restart_job_id}`
    } else {
      modelFeedback.value = '模型已保存，实例启动后生效'
    }
  } catch (err: unknown) {
    modelError.value = true
    modelFeedback.value = err instanceof Error ? err.message : '模型修改失败'
  }
}
```

Add a descriptions item:

```vue
<n-descriptions-item label="模型">{{ app.model_id }}</n-descriptions-item>
```

Add a model form below descriptions:

```vue
<div v-if="app" style="margin-top: 12px; display: grid; grid-template-columns: minmax(180px, 1fr) auto; gap: 12px; align-items: end">
  <n-form-item label="切换模型" style="margin-bottom: 0">
    <n-select v-model:value="modelValue" :options="modelOptions" :disabled="!canToggleKey" />
  </n-form-item>
  <n-button type="primary" :disabled="!modelValue || modelValue === app.model_id || modelMutation.isPending.value" @click="onUpdateModel">
    {{ app.container_id ? '保存并重启实例' : '保存模型' }}
  </n-button>
</div>
<p v-if="modelFeedback" class="state-text" :class="{ danger: modelError }" style="margin-top: 8px">{{ modelFeedback }}</p>
```

- [ ] **Step 7: Update frontend tests**

Add tests:

```ts
// OrganizationsPage: 模型列表加载失败时禁用保存。
expect(wrapper.text()).toContain('模型列表获取失败，请重试')

// CreateMemberPage: 提交体包含 model_id。
expect(onboardSpy).toHaveBeenCalledWith(expect.objectContaining({ model_id: 'qwen2.5:7b' }))

// MembersPage: 平台复建实例提交体包含 model_id。
expect(createAppSpy).toHaveBeenCalledWith(expect.objectContaining({
  payload: expect.objectContaining({ model_id: 'qwen2.5:7b' }),
}))

// AppOverviewTab: 修改模型后展示重启 job。
expect(wrapper.text()).toContain('已提交模型生效重启任务')
```

- [ ] **Step 8: Run frontend tests and commit**

Run:

```bash
rtk npm --prefix web run test -- OrganizationsPage CreateMemberPage MembersPage AppOverviewTab
rtk npm --prefix web run typecheck
```

Expected: targeted tests and typecheck pass.

Commit:

```bash
rtk git add web/src/api/hooks/useOrganizations.ts web/src/api/hooks/useMembers.ts \
  web/src/api/hooks/useApps.ts web/src/api/index.ts \
  web/src/pages/platform/OrganizationsPage.vue web/src/pages/platform/OrganizationsPage.spec.ts \
  web/src/pages/org/CreateMemberPage.vue web/src/pages/org/MembersPage.vue web/src/pages/org/MembersPage.spec.ts \
  web/src/pages/apps/AppOverviewTab.vue web/src/pages/apps/AppOverviewTab.spec.ts
rtk git commit -m "feat(web): 增加实例模型选择和切换入口" \
  -m "组织表单支持模型多选，实例创建和复建支持选择模型，实例概览页支持保存模型并触发重启。"
```

## Task 7: OpenAPI, Full Tests, And Browser Verification

**Files:**
- Generated: `openapi/openapi.yaml`
- Generated: `web/src/api/generated.ts`
- Modify if required: tests touched by generated type changes

- [ ] **Step 1: Generate OpenAPI and web types**

Run:

```bash
rtk make openapi-gen
rtk make web-types-gen
```

Expected: `openapi/openapi.yaml` and `web/src/api/generated.ts` include:

- `enabled_models` on organization create/update/result schemas.
- `model_id` on app result and member app creation schemas.
- `GET /models`.
- `PATCH /apps/{appId}/model`.

- [ ] **Step 2: Run backend and frontend verification**

Run:

```bash
rtk go test ./... -count=1
rtk npm --prefix web run test
rtk npm --prefix web run typecheck
```

Expected: all Go tests, frontend unit tests, and TypeScript checks pass.

- [ ] **Step 3: Start local stack for browser verification**

Run the project’s normal local environment commands:

```bash
rtk make dev-up
```

If the web dev server is not already running, start it:

```bash
rtk npm --prefix web run dev -- --host 0.0.0.0
```

Record the printed local web URL.

- [ ] **Step 4: Browser verify platform model flow**

Use Chrome DevTools MCP to verify:

1. Log in as platform admin with `admin` / `admin123`.
2. Open the organization page.
3. Click “新增组织”.
4. Confirm the “可用模型” multiselect loads model options.
5. Temporarily break new-api config or use a test stub environment to verify model-list failure disables saving.
6. Restore config, create an organization with two models selected.
7. Confirm the organization row can be viewed and copied normally.

Expected: organization create succeeds only when models are selected and model list loads.

- [ ] **Step 5: Browser verify instance model flow**

Use Chrome DevTools MCP:

1. Log in as the new organization admin.
2. Open “创建成员并初始化实例”.
3. Confirm the model select only shows that organization’s selected models.
4. Create a member instance with model A.
5. Open the instance detail page.
6. Confirm overview shows model A.
7. Change to model B and click “保存并重启实例”.
8. Confirm a restart job is shown.
9. After restart succeeds, confirm overview still shows model B.
10. Create or switch to another instance and confirm its model value is independent.

Expected: model selection is per instance, and model changes require restart.

- [ ] **Step 6: Browser verify model use and removal conflict**

Use the app channel or existing actual invocation path:

1. Trigger one actual OpenClaw call for the instance.
2. Confirm response succeeds after the model change.
3. Log in again as platform admin.
4. Try editing the organization and removing the model currently used by the instance.
5. Confirm the API returns 409 and the page shows the conflict message.
6. Switch the instance away from that model.
7. Remove the old model from the organization allowlist.

Expected: model in use cannot be removed; after switching instances away, removal succeeds.

- [ ] **Step 7: Commit generated contracts and verification fixes**

Run:

```bash
rtk git status --short
```

Commit only related files:

```bash
rtk git add openapi/openapi.yaml web/src/api/generated.ts
rtk git commit -m "chore(openapi): 同步实例模型治理契约" \
  -m "根据新增模型列表、组织模型 allowlist、实例模型选择和模型修改接口重新生成 OpenAPI 与前端类型。"
```

If browser verification required a small code fix, commit it separately with the matching conventional commit type.

## Self-Review

- Spec coverage: model list, organization allowlist, app model creation, model update restart, no new-api token permission, legacy app cleanup, OpenAPI, tests, and browser verification all map to tasks above.
- Placeholder scan: no unresolved markers or open-ended implementation instructions remain.
- Type consistency: model field names are `enabled_models`, `model_id`, `restart_job_id`, and `requires_restart` across backend, API, and frontend tasks.
