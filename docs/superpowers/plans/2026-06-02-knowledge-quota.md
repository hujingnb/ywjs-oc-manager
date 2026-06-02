# Knowledge Quota Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add mandatory cumulative capacity limits for enterprise knowledge bases and per-instance knowledge bases.

**Architecture:** Store quota bytes on the business owners (`organizations` for enterprise knowledge, `apps` for instance knowledge), compute current usage from `ragflow_documents.size_bytes`, and enforce the quota in `KnowledgeService` before any RAGFlow upload. Organization quota editing follows the existing organization profile flow; instance quota editing uses a small app knowledge quota endpoint because there is no general app profile edit API.

**Tech Stack:** Go 1.22+, Gin, sqlc v1.30.0, MySQL 8 migrations, Vue 3 + TypeScript + TanStack Query + Naive UI, OpenAPI via swag + openapi-typescript.

---

## File Structure

- Create: `internal/migrations/000005_knowledge_quota.up.sql` — add required quota columns with 1GB defaults.
- Create: `internal/migrations/000005_knowledge_quota.down.sql` — rollback quota columns and constraints.
- Modify: `sqlc.yaml` — include the new migration in sqlc schema order.
- Modify: `internal/store/queries/organizations.sql` — persist organization knowledge quota in create/update.
- Modify: `internal/store/queries/apps.sql` — add `SetAppKnowledgeQuota`.
- Modify: `internal/store/queries/ragflow_knowledge.sql` — add usage sum query.
- Generated: `internal/store/sqlc/*.go` — regenerate via `make sqlc-generate`.
- Modify: `internal/auth/authorizer.go` and tests — add quota edit predicates.
- Modify: `internal/service/errors.go` — add `ErrKnowledgeQuotaExceeded`.
- Modify: `internal/service/organization_service.go` and tests — add organization quota field and validation.
- Modify: `internal/service/app_service.go` and tests — expose app quota and add instance quota update service method.
- Modify: `internal/api/handlers/dto.go` — add quota request fields.
- Modify: `internal/api/handlers/organizations.go` and tests — forward organization quota bytes.
- Modify: `internal/api/handlers/apps.go` and tests — register `PATCH /api/v1/apps/{appId}/knowledge/quota`.
- Modify: `internal/api/handlers/knowledge.go` and tests — reject unknown upload size and map quota errors to 409.
- Modify: `internal/service/knowledge_service.go` and tests — add list quota fields and enforce upload quota.
- Modify: `web/src/api/hooks/useOrganizations.ts` — include organization quota payload fields.
- Modify: `web/src/api/hooks/useApps.ts` — include app quota field and update mutation.
- Modify: `web/src/api/hooks/useKnowledge.ts` and tests — expose listing usage fields and remaining-size helpers.
- Modify: `web/src/domain/permissions.ts` and tests — add app quota edit helper.
- Modify: `web/src/pages/platform/OrganizationsPage.vue` and tests — add enterprise knowledge quota form/list field.
- Modify: `web/src/pages/knowledge/OrgKnowledgePage.vue` and tests — show quota summary and block files larger than remaining quota.
- Modify: `web/src/pages/apps/AppKnowledgeTab.vue` and tests — show quota summary, add edit modal, and block files larger than remaining quota.
- Generated: `openapi/openapi.yaml`, `web/src/api/generated.ts` — regenerate after handler changes.

---

### Task 1: Database Schema and sqlc

**Files:**
- Create: `internal/migrations/000005_knowledge_quota.up.sql`
- Create: `internal/migrations/000005_knowledge_quota.down.sql`
- Modify: `sqlc.yaml`
- Modify: `internal/store/queries/organizations.sql`
- Modify: `internal/store/queries/apps.sql`
- Modify: `internal/store/queries/ragflow_knowledge.sql`
- Generated: `internal/store/sqlc/*.go`

- [ ] **Step 1: Write migration up/down files**

Create `internal/migrations/000005_knowledge_quota.up.sql`:

```sql
-- 知识库空间上限：所有企业与实例都必须有累计容量限制，默认 1GB。
-- 线上若存在历史空值，先统一补 1GB，再启用 NOT NULL 约束；新增列在 MySQL 侧默认填充 1GB。
ALTER TABLE organizations
    ADD COLUMN knowledge_quota_bytes BIGINT NOT NULL DEFAULT 1073741824,
    ADD CONSTRAINT organizations_knowledge_quota_bytes_check
        CHECK (knowledge_quota_bytes > 0);

ALTER TABLE apps
    ADD COLUMN knowledge_quota_bytes BIGINT NOT NULL DEFAULT 1073741824,
    ADD CONSTRAINT apps_knowledge_quota_bytes_check
        CHECK (knowledge_quota_bytes > 0);
```

Create `internal/migrations/000005_knowledge_quota.down.sql`:

```sql
-- 回滚知识库空间上限：先删 CHECK 约束再删列（MySQL 8 支持 DROP CONSTRAINT）。
ALTER TABLE apps
    DROP CONSTRAINT apps_knowledge_quota_bytes_check,
    DROP COLUMN knowledge_quota_bytes;

ALTER TABLE organizations
    DROP CONSTRAINT organizations_knowledge_quota_bytes_check,
    DROP COLUMN knowledge_quota_bytes;
```

- [ ] **Step 2: Add schema file to `sqlc.yaml`**

Append the migration to the `schema` list after `000004_org_max_instance_count.up.sql`:

```yaml
      - internal/migrations/000005_knowledge_quota.up.sql
```

- [ ] **Step 3: Update organization queries**

In `internal/store/queries/organizations.sql`, add `knowledge_quota_bytes` to `CreateOrganization` and `UpdateOrganizationProfile`.

`CreateOrganization` column and values list should include:

```sql
    max_instance_count,
    knowledge_quota_bytes,
    assistant_version_ids
) VALUES (
    ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?
);
```

`UpdateOrganizationProfile` should set:

```sql
    credit_warning_threshold = ?,
    max_instance_count = ?,
    knowledge_quota_bytes = ?,
    assistant_version_ids = ?,
    updated_at = now()
WHERE id = ?;
```

- [ ] **Step 4: Add app quota update query**

Append this to `internal/store/queries/apps.sql`:

```sql
-- name: SetAppKnowledgeQuota :exec
-- 更新单个实例知识库累计容量上限；允许低于当前已用，后续上传由 KnowledgeService 拒绝。
UPDATE apps
SET knowledge_quota_bytes = ?,
    updated_at = now()
WHERE id = ? AND deleted_at IS NULL;
```

- [ ] **Step 5: Add knowledge usage sum query**

Append this to `internal/store/queries/ragflow_knowledge.sql`:

```sql
-- name: SumRAGFlowDocumentsSizeByScope :one
-- 汇总知识库当前累计占用；失败/停止文件仍占用 RAGFlow 原文件存储，因此全部状态都计入。
SELECT COALESCE(SUM(size_bytes), 0)
FROM ragflow_documents
WHERE scope_type = ?
  AND org_id = ?
  AND (sqlc.narg(app_id) IS NULL OR app_id = sqlc.narg(app_id));
```

- [ ] **Step 6: Regenerate sqlc output**

Run:

```bash
make sqlc-generate
```

Expected: command exits 0 and generated structs now include:

```go
KnowledgeQuotaBytes int64 `json:"knowledge_quota_bytes" db:"knowledge_quota_bytes"`
```

on both `sqlc.Organization` and `sqlc.App`, plus generated params for:

```go
SetAppKnowledgeQuota(ctx context.Context, arg SetAppKnowledgeQuotaParams) error
SumRAGFlowDocumentsSizeByScope(ctx context.Context, arg SumRAGFlowDocumentsSizeByScopeParams) (int64, error)
```

- [ ] **Step 7: Verify schema generation**

Run:

```bash
go test ./internal/migrations ./cmd/migrate
go test ./internal/store/sqlc
```

Expected: PASS.

- [ ] **Step 8: Commit database/sqlc changes**

```bash
git add sqlc.yaml internal/migrations/000005_knowledge_quota.up.sql internal/migrations/000005_knowledge_quota.down.sql internal/store/queries/organizations.sql internal/store/queries/apps.sql internal/store/queries/ragflow_knowledge.sql internal/store/sqlc
git commit -m "feat(knowledge): 增加知识库容量字段" -m "为企业和实例增加必填知识库容量上限字段，默认 1GB。同步 sqlc 查询与生成代码，为后续容量校验提供数据基础。"
```

---

### Task 2: Shared Quota Validation and Authorization

**Files:**
- Modify: `internal/auth/authorizer.go`
- Modify: `internal/auth/authorizer_test.go`
- Modify: `internal/service/errors.go`
- Create or modify: `internal/service/knowledge_quota.go`
- Test: `internal/service/knowledge_quota_test.go`

- [ ] **Step 1: Add authorization tests**

In `internal/auth/authorizer_test.go`, add tests:

```go
// TestCanUpdateOrgKnowledgeQuota 验证企业知识库容量只能由平台管理员修改。
func TestCanUpdateOrgKnowledgeQuota(t *testing.T) {
	assert.True(t, CanUpdateOrgKnowledgeQuota(Principal{Role: domain.UserRolePlatformAdmin}))
	assert.False(t, CanUpdateOrgKnowledgeQuota(Principal{Role: domain.UserRoleOrgAdmin, OrgID: "org-1"}))
	assert.False(t, CanUpdateOrgKnowledgeQuota(Principal{Role: domain.UserRoleOrgMember, OrgID: "org-1"}))
}

// TestCanUpdateAppKnowledgeQuota 验证实例知识库容量允许平台管理员和本企业管理员修改。
func TestCanUpdateAppKnowledgeQuota(t *testing.T) {
	assert.True(t, CanUpdateAppKnowledgeQuota(Principal{Role: domain.UserRolePlatformAdmin}, "org-1"))
	assert.True(t, CanUpdateAppKnowledgeQuota(Principal{Role: domain.UserRoleOrgAdmin, OrgID: "org-1"}, "org-1"))
	assert.False(t, CanUpdateAppKnowledgeQuota(Principal{Role: domain.UserRoleOrgAdmin, OrgID: "org-2"}, "org-1"))
	assert.False(t, CanUpdateAppKnowledgeQuota(Principal{Role: domain.UserRoleOrgMember, OrgID: "org-1"}, "org-1"))
}
```

- [ ] **Step 2: Run auth tests and verify they fail**

Run:

```bash
go test ./internal/auth -run 'TestCanUpdateOrgKnowledgeQuota|TestCanUpdateAppKnowledgeQuota' -v
```

Expected: FAIL because the two predicate functions do not exist.

- [ ] **Step 3: Add predicates in `authorizer.go`**

Add under the knowledge permissions section:

```go
// CanUpdateOrgKnowledgeQuota 判断主体是否可编辑企业知识库容量。
// 容量属于平台侧租户配置，只允许平台管理员修改。
func CanUpdateOrgKnowledgeQuota(p Principal) bool {
	return p.Role == domain.UserRolePlatformAdmin
}

// CanUpdateAppKnowledgeQuota 判断主体是否可编辑实例知识库容量。
// 平台管理员可运维兜底；企业管理员仅能调整本企业实例；普通成员不可修改容量。
func CanUpdateAppKnowledgeQuota(p Principal, appOrgID string) bool {
	switch p.Role {
	case domain.UserRolePlatformAdmin:
		return true
	case domain.UserRoleOrgAdmin:
		return p.OrgID == appOrgID
	default:
		return false
	}
}
```

- [ ] **Step 4: Add service quota helpers and tests**

Create `internal/service/knowledge_quota_test.go`:

```go
package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNormalizeKnowledgeQuotaBytes 验证知识库容量默认值与正数校验。
func TestNormalizeKnowledgeQuotaBytes(t *testing.T) {
	oneGB := KnowledgeQuotaDefaultBytes

	got, err := normalizeKnowledgeQuotaBytes(nil)
	require.NoError(t, err)
	assert.Equal(t, oneGB, got)

	custom := int64(2 * 1024 * 1024 * 1024)
	got, err = normalizeKnowledgeQuotaBytes(&custom)
	require.NoError(t, err)
	assert.Equal(t, custom, got)

	zero := int64(0)
	_, err = normalizeKnowledgeQuotaBytes(&zero)
	require.ErrorIs(t, err, ErrMemberCreateInvalid)
}

// TestKnowledgeQuotaRemainingBytes 验证剩余空间小于 0 时展示为 0。
func TestKnowledgeQuotaRemainingBytes(t *testing.T) {
	assert.Equal(t, int64(20), knowledgeQuotaRemainingBytes(100, 80))
	assert.Equal(t, int64(0), knowledgeQuotaRemainingBytes(100, 120))
}
```

- [ ] **Step 5: Create quota helper implementation**

Create `internal/service/knowledge_quota.go`:

```go
package service

import "fmt"

const (
	// KnowledgeQuotaDefaultBytes 是企业和实例知识库的默认累计容量上限（1GB）。
	KnowledgeQuotaDefaultBytes int64 = 1024 * 1024 * 1024
)

// normalizeKnowledgeQuotaBytes 将可选请求值归一为必填正数容量。
// nil 表示调用方未提交容量，创建场景使用 1GB 默认值；更新场景可在调用前选择保留旧值。
func normalizeKnowledgeQuotaBytes(value *int64) (int64, error) {
	if value == nil {
		return KnowledgeQuotaDefaultBytes, nil
	}
	if *value <= 0 {
		return 0, fmt.Errorf("%w: 知识库空间必须大于 0", ErrMemberCreateInvalid)
	}
	return *value, nil
}

// validateKnowledgeQuotaBytes 校验显式提交的容量值。
func validateKnowledgeQuotaBytes(value int64) error {
	if value <= 0 {
		return fmt.Errorf("%w: 知识库空间必须大于 0", ErrMemberCreateInvalid)
	}
	return nil
}

// knowledgeQuotaRemainingBytes 计算前端展示用剩余空间，已超用时按 0 展示。
func knowledgeQuotaRemainingBytes(quotaBytes, usedBytes int64) int64 {
	remaining := quotaBytes - usedBytes
	if remaining < 0 {
		return 0
	}
	return remaining
}
```

- [ ] **Step 6: Add knowledge quota exceeded sentinel**

In `internal/service/errors.go`, add under knowledge errors:

```go
// ErrKnowledgeQuotaExceeded 表示知识库累计空间不足，handler 层据此映射为 409 Conflict。
var ErrKnowledgeQuotaExceeded = errors.New("知识库空间不足")
```

- [ ] **Step 7: Verify shared helpers**

Run:

```bash
go test ./internal/auth ./internal/service -run 'TestCanUpdate|TestNormalizeKnowledgeQuotaBytes|TestKnowledgeQuotaRemainingBytes' -v
```

Expected: PASS.

- [ ] **Step 8: Commit shared helper changes**

```bash
git add internal/auth/authorizer.go internal/auth/authorizer_test.go internal/service/errors.go internal/service/knowledge_quota.go internal/service/knowledge_quota_test.go
git commit -m "feat(knowledge): 增加容量权限与校验 helper" -m "集中新增知识库容量编辑权限、默认 1GB 容量校验和容量不足 sentinel error。后续组织、实例和上传路径复用这些 helper。"
```

---

### Task 3: Organization Quota Backend

**Files:**
- Modify: `internal/service/organization_service.go`
- Modify: `internal/service/organization_service_test.go`
- Modify: `internal/api/handlers/dto.go`
- Modify: `internal/api/handlers/organizations.go`
- Modify: `internal/api/handlers/organizations_test.go`

- [ ] **Step 1: Add handler tests for forwarding quota**

In `internal/api/handlers/organizations_test.go`, add:

```go
// TestOrganizationsCreateForwardsKnowledgeQuotaBytes 验证创建企业时透传企业知识库容量上限。
func TestOrganizationsCreateForwardsKnowledgeQuotaBytes(t *testing.T) {
	svc := &organizationServiceStub{
		createResult: service.OrganizationResult{ID: "org-1", Name: "测试组织", Status: domain.StatusActive},
	}
	router := newOrganizationsTestRouter(t, svc)

	recorder := httptest.NewRecorder()
	body := `{"name":"测试组织","code":"test-org","knowledge_quota_bytes":2147483648,"admin_username":"admin","admin_display_name":"管理员","admin_password":"secret-password"}`
	request := httptest.NewRequest(http.MethodPost, "/api/v1/organizations", bytes.NewBufferString(body))
	request.Header.Set("Content-Type", "application/json")
	request = withPrincipal(request, auth.Principal{UserID: "user-1", Role: domain.UserRolePlatformAdmin})
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusCreated, recorder.Code)
	require.NotNil(t, svc.lastCreateInput.KnowledgeQuotaBytes)
	assert.Equal(t, int64(2147483648), *svc.lastCreateInput.KnowledgeQuotaBytes)
}

// TestOrganizationsUpdateForwardsKnowledgeQuotaBytes 验证编辑企业时透传企业知识库容量上限。
func TestOrganizationsUpdateForwardsKnowledgeQuotaBytes(t *testing.T) {
	svc := &organizationServiceStub{
		createResult: service.OrganizationResult{ID: "org-1", Name: "测试组织", Status: domain.StatusActive},
	}
	router := newOrganizationsTestRouter(t, svc)

	recorder := httptest.NewRecorder()
	body := `{"name":"测试组织","knowledge_quota_bytes":3221225472}`
	request := httptest.NewRequest(http.MethodPatch, "/api/v1/organizations/org-1", bytes.NewBufferString(body))
	request.Header.Set("Content-Type", "application/json")
	request = withPrincipal(request, auth.Principal{UserID: "user-1", Role: domain.UserRolePlatformAdmin})
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.NotNil(t, svc.lastUpdateInput.KnowledgeQuotaBytes)
	assert.Equal(t, int64(3221225472), *svc.lastUpdateInput.KnowledgeQuotaBytes)
}
```

- [ ] **Step 2: Run organization handler tests and verify they fail**

Run:

```bash
go test ./internal/api/handlers -run 'TestOrganizations(Create|Update)ForwardsKnowledgeQuotaBytes' -v
```

Expected: FAIL because `KnowledgeQuotaBytes` is not in DTO/input yet.

- [ ] **Step 3: Add organization DTO fields**

In `internal/api/handlers/dto.go`, add to both organization request structs:

```go
// KnowledgeQuotaBytes 是企业知识库累计容量上限，单位字节；nil 表示创建时使用默认值、更新时保留旧值。
KnowledgeQuotaBytes *int64 `json:"knowledge_quota_bytes"`
```

- [ ] **Step 4: Forward quota in handler input conversion**

In `internal/api/handlers/organizations.go`, add:

```go
KnowledgeQuotaBytes: req.KnowledgeQuotaBytes,
```

to both `toOrganizationInput` and `toCreateOrganizationInput`.

- [ ] **Step 5: Add service fields and persistence tests**

In `internal/service/organization_service_test.go`, add:

```go
// TestCreateOrganization_PersistsKnowledgeQuotaBytes 验证创建企业时知识库容量写入 CreateOrganizationParams。
func TestCreateOrganization_PersistsKnowledgeQuotaBytes(t *testing.T) {
	store := &organizationStoreStub{}
	prov := &fakeProvisioner{user: newapi.User{ID: 42}, accessToken: "access-tok-xyz"}
	svc := NewOrganizationService(store, prov, mustCipher(t), nil)
	svc.SetVersionValidator(fakeVersionValidator{known: map[string]bool{}})
	svc.hashPassword = fakeHash
	quota := int64(2 * 1024 * 1024 * 1024)

	_, err := svc.CreateOrganization(context.Background(), auth.Principal{Role: domain.UserRolePlatformAdmin}, OrganizationInput{
		Name:                "测试组织",
		Code:                "test-org",
		KnowledgeQuotaBytes: &quota,
		AdminUsername:       "org-admin",
		AdminDisplayName:    "企业管理员",
		AdminPassword:       "secret-password",
	})
	require.NoError(t, err)
	assert.Equal(t, quota, store.created.KnowledgeQuotaBytes)
}

// TestCreateOrganization_DefaultsKnowledgeQuotaBytes 验证创建企业未传容量时默认 1GB。
func TestCreateOrganization_DefaultsKnowledgeQuotaBytes(t *testing.T) {
	store := &organizationStoreStub{}
	prov := &fakeProvisioner{user: newapi.User{ID: 42}, accessToken: "access-tok-xyz"}
	svc := NewOrganizationService(store, prov, mustCipher(t), nil)
	svc.SetVersionValidator(fakeVersionValidator{known: map[string]bool{}})
	svc.hashPassword = fakeHash

	_, err := svc.CreateOrganization(context.Background(), auth.Principal{Role: domain.UserRolePlatformAdmin}, OrganizationInput{
		Name:             "测试组织",
		Code:             "test-org",
		AdminUsername:    "org-admin",
		AdminDisplayName: "企业管理员",
		AdminPassword:    "secret-password",
	})
	require.NoError(t, err)
	assert.Equal(t, KnowledgeQuotaDefaultBytes, store.created.KnowledgeQuotaBytes)
}

// TestUpdateOrganization_PreservesKnowledgeQuotaWhenOmitted 验证编辑企业未传容量时保留原值。
func TestUpdateOrganization_PreservesKnowledgeQuotaWhenOmitted(t *testing.T) {
	store := &organizationStoreStub{}
	store.org.KnowledgeQuotaBytes = 3 * 1024 * 1024 * 1024
	svc := NewOrganizationService(store, &fakeProvisioner{}, mustCipher(t), nil)
	svc.SetVersionValidator(fakeVersionValidator{known: map[string]bool{}})

	_, err := svc.UpdateOrganization(context.Background(), auth.Principal{Role: domain.UserRolePlatformAdmin}, store.org.ID, OrganizationInput{
		Name:                   store.org.Name,
		AssistantVersionIDsSet: false,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(3*1024*1024*1024), store.updatedProfile.KnowledgeQuotaBytes)
}
```

- [ ] **Step 6: Update organization service structs and mapping**

In `internal/service/organization_service.go`:

Add to `OrganizationInput`:

```go
// KnowledgeQuotaBytes 是企业知识库累计容量上限，单位字节；nil 表示创建时默认 1GB，更新时保留旧值。
KnowledgeQuotaBytes *int64
```

Add to `OrganizationResult`:

```go
// KnowledgeQuotaBytes 是企业知识库累计容量上限，单位字节。
KnowledgeQuotaBytes int64 `json:"knowledge_quota_bytes"`
```

In `CreateOrganization`, before `CreateOrganizationParams`, compute:

```go
knowledgeQuotaBytes, err := normalizeKnowledgeQuotaBytes(input.KnowledgeQuotaBytes)
if err != nil {
	return OrganizationResult{}, err
}
```

Pass:

```go
KnowledgeQuotaBytes: knowledgeQuotaBytes,
```

In `UpdateOrganization`, compute after `current` is loaded:

```go
knowledgeQuotaBytes := current.KnowledgeQuotaBytes
if input.KnowledgeQuotaBytes != nil {
	if err := validateKnowledgeQuotaBytes(*input.KnowledgeQuotaBytes); err != nil {
		return OrganizationResult{}, err
	}
	knowledgeQuotaBytes = *input.KnowledgeQuotaBytes
}
```

Pass:

```go
KnowledgeQuotaBytes: knowledgeQuotaBytes,
```

In `toOrganizationResult`, set:

```go
KnowledgeQuotaBytes: org.KnowledgeQuotaBytes,
```

- [ ] **Step 7: Run organization backend tests**

Run:

```bash
go test ./internal/service -run 'Test(Create|Update)Organization_.*KnowledgeQuota|TestOrganizationServiceCreateProvisionsNewAPIUser' -v
go test ./internal/api/handlers -run 'TestOrganizations(Create|Update)ForwardsKnowledgeQuotaBytes|TestOrganizationsCreateReturnsCreatedOrganization' -v
```

Expected: PASS.

- [ ] **Step 8: Commit organization backend changes**

```bash
git add internal/service/organization_service.go internal/service/organization_service_test.go internal/api/handlers/dto.go internal/api/handlers/organizations.go internal/api/handlers/organizations_test.go
git commit -m "feat(organization): 支持配置企业知识库容量" -m "组织创建和编辑接口增加 knowledge_quota_bytes，创建默认 1GB，编辑未传值时保留原容量。"
```

---

### Task 4: App Knowledge Quota Backend Endpoint

**Files:**
- Modify: `internal/service/app_service.go`
- Modify: `internal/service/app_service_test.go`
- Modify: `internal/api/handlers/dto.go`
- Modify: `internal/api/handlers/apps.go`
- Modify: `internal/api/handlers/apps_test.go`

- [ ] **Step 1: Add app service tests**

In `internal/service/app_service_test.go`, add:

```go
// TestUpdateAppKnowledgeQuotaAllowsOrgAdmin 验证企业管理员可修改本企业实例知识库容量。
func TestUpdateAppKnowledgeQuotaAllowsOrgAdmin(t *testing.T) {
	svc, store := newAppServiceWithStore(t)
	app := store.mustSeedApp(t)
	app.KnowledgeQuotaBytes = KnowledgeQuotaDefaultBytes
	store.app = app

	result, err := svc.UpdateAppKnowledgeQuota(context.Background(), appOrgAdminPrincipal(store.organization), testAppServiceAppID, 2*1024*1024*1024)
	require.NoError(t, err)

	assert.Equal(t, int64(2*1024*1024*1024), store.app.KnowledgeQuotaBytes)
	assert.Equal(t, int64(2*1024*1024*1024), result.KnowledgeQuotaBytes)
}

// TestUpdateAppKnowledgeQuotaRejectsMember 验证普通成员不能修改实例知识库容量。
func TestUpdateAppKnowledgeQuotaRejectsMember(t *testing.T) {
	svc, store := newAppServiceWithStore(t)
	store.mustSeedApp(t)

	_, err := svc.UpdateAppKnowledgeQuota(context.Background(), auth.Principal{
		Role:   domain.UserRoleOrgMember,
		OrgID:  store.organization.ID,
		UserID: testMemUID,
	}, testAppServiceAppID, 2*1024*1024*1024)

	require.ErrorIs(t, err, ErrForbidden)
	assert.Equal(t, int64(0), store.app.KnowledgeQuotaBytes)
}

// TestUpdateAppKnowledgeQuotaRejectsInvalidQuota 验证实例知识库容量必须为正数。
func TestUpdateAppKnowledgeQuotaRejectsInvalidQuota(t *testing.T) {
	svc, store := newAppServiceWithStore(t)
	store.mustSeedApp(t)

	_, err := svc.UpdateAppKnowledgeQuota(context.Background(), appOrgAdminPrincipal(store.organization), testAppServiceAppID, 0)

	require.ErrorIs(t, err, ErrMemberCreateInvalid)
}
```

- [ ] **Step 2: Run app service tests and verify they fail**

Run:

```bash
go test ./internal/service -run 'TestUpdateAppKnowledgeQuota' -v
```

Expected: FAIL because `UpdateAppKnowledgeQuota` and generated query wiring do not exist yet.

- [ ] **Step 3: Implement app service quota update**

In `internal/service/app_service.go`:

Add to `AppStore`:

```go
// SetAppKnowledgeQuota 更新单个实例知识库容量上限。
SetAppKnowledgeQuota(ctx context.Context, arg sqlc.SetAppKnowledgeQuotaParams) error
```

Add to `AppResult`:

```go
// KnowledgeQuotaBytes 是实例知识库累计容量上限，单位字节。
KnowledgeQuotaBytes int64 `json:"knowledge_quota_bytes"`
```

In `toAppResult`, set:

```go
KnowledgeQuotaBytes: app.KnowledgeQuotaBytes,
```

Add service method:

```go
// UpdateAppKnowledgeQuota 更新单个实例的知识库累计容量上限。
func (s *AppService) UpdateAppKnowledgeQuota(ctx context.Context, principal auth.Principal, appID string, quotaBytes int64) (AppResult, error) {
	if err := validateKnowledgeQuotaBytes(quotaBytes); err != nil {
		return AppResult{}, err
	}
	row, err := s.store.GetAppWithVersion(ctx, appID)
	if errors.Is(err, sql.ErrNoRows) {
		return AppResult{}, ErrNotFound
	}
	if err != nil {
		return AppResult{}, fmt.Errorf("查询应用失败: %w", err)
	}
	if !auth.CanUpdateAppKnowledgeQuota(principal, row.App.OrgID) {
		return AppResult{}, ErrForbidden
	}
	if err := s.store.SetAppKnowledgeQuota(ctx, sqlc.SetAppKnowledgeQuotaParams{
		ID:                  row.App.ID,
		KnowledgeQuotaBytes: quotaBytes,
	}); err != nil {
		return AppResult{}, fmt.Errorf("更新实例知识库容量失败: %w", err)
	}
	newRow, err := s.store.GetAppWithVersion(ctx, appID)
	if err != nil {
		return AppResult{}, fmt.Errorf("重新查询应用失败: %w", err)
	}
	result := toAppResult(newRow.App)
	result.VersionSynced = computeVersionSynced(newRow.App, newRow.VersionRevision, newRow.VersionImageID, s.imageResolver)
	if principal.Role == domain.UserRolePlatformAdmin {
		result.RuntimeImageRef = newRow.App.RuntimeImageRef
		result.RuntimeImageSha256 = newRow.App.RuntimeImageSha256
	}
	return result, nil
}
```

Update `appServiceStoreStub` in `app_service_test.go`:

```go
func (s *appServiceStoreStub) SetAppKnowledgeQuota(_ context.Context, arg sqlc.SetAppKnowledgeQuotaParams) error {
	if s.app.ID != arg.ID {
		return sql.ErrNoRows
	}
	s.app.KnowledgeQuotaBytes = arg.KnowledgeQuotaBytes
	return nil
}
```

- [ ] **Step 4: Add app handler tests**

In `internal/api/handlers/apps_test.go`, add:

```go
// TestUpdateKnowledgeQuotaHappy 验证实例知识库容量更新接口返回更新后的 app。
func TestUpdateKnowledgeQuotaHappy(t *testing.T) {
	stub := &appsStub{updateKnowledgeQuotaResult: service.AppResult{ID: "app-1", KnowledgeQuotaBytes: 2147483648}}
	router := newAppsTestRouter(t, stub)

	body := strings.NewReader(`{"quota_bytes":2147483648}`)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/apps/app-1/knowledge/quota", body)
	req.Header.Set("Content-Type", "application/json")
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgAdmin, OrgID: "org-1"})
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"knowledge_quota_bytes":2147483648`)
	assert.Equal(t, int64(2147483648), stub.lastQuotaBytes)
}

// TestUpdateKnowledgeQuotaBadRequest 验证缺少 quota_bytes 时返回 400。
func TestUpdateKnowledgeQuotaBadRequest(t *testing.T) {
	stub := &appsStub{}
	router := newAppsTestRouter(t, stub)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/apps/app-1/knowledge/quota", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgAdmin, OrgID: "org-1"})
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "INVALID_REQUEST")
}
```

- [ ] **Step 5: Add DTO and handler route**

In `internal/api/handlers/dto.go`, add:

```go
// UpdateAppKnowledgeQuotaRequest 更新实例知识库累计容量上限的请求体。
type UpdateAppKnowledgeQuotaRequest struct {
	// QuotaBytes 是实例知识库累计容量上限，单位字节，必须大于 0。
	QuotaBytes int64 `json:"quota_bytes" binding:"required"`
}
```

In `internal/api/handlers/apps.go`:

Add to `appService`:

```go
UpdateAppKnowledgeQuota(ctx context.Context, principal auth.Principal, appID string, quotaBytes int64) (service.AppResult, error)
```

Register route:

```go
router.PATCH("/api/v1/apps/:appId/knowledge/quota", handler.UpdateKnowledgeQuota)
```

Add handler:

```go
// UpdateKnowledgeQuota 更新实例知识库容量上限。
//
// @Summary      更新实例知识库容量
// @Description  更新单个实例知识库累计容量上限，允许低于当前已用，后续上传会被拦截
// @Tags         apps
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        appId  path      string                         true  "应用 ID"
// @Param        body   body      UpdateAppKnowledgeQuotaRequest true  "容量上限"
// @Success      200    {object}  map[string]service.AppResult
// @Failure      400    {object}  ErrorResponse
// @Failure      401    {object}  ErrorResponse
// @Failure      403    {object}  ErrorResponse
// @Failure      404    {object}  ErrorResponse
// @Failure      500    {object}  ErrorResponse
// @Router       /apps/{appId}/knowledge/quota [patch]
func (h *AppsHandler) UpdateKnowledgeQuota(c *gin.Context) {
	var req UpdateAppKnowledgeQuotaRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, apierror.New("INVALID_REQUEST", "请求体格式错误"))
		return
	}
	result, err := h.service.UpdateAppKnowledgeQuota(c.Request.Context(), principalFromCtx(c), c.Param("appId"), req.QuotaBytes)
	if err != nil {
		writeAppsError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"app": result})
}
```

Update `writeAppsError` so invalid quota maps to 400:

```go
case errors.Is(err, service.ErrMemberCreateInvalid):
	c.JSON(http.StatusBadRequest, apierror.New("MEMBER_INVALID", validationServiceMessage(err, service.ErrMemberCreateInvalid)))
```

This case already exists; keep it.

Update `appsStub` fields/method:

```go
updateKnowledgeQuotaResult service.AppResult
updateKnowledgeQuotaErr    error
lastQuotaBytes             int64

func (s *appsStub) UpdateAppKnowledgeQuota(_ context.Context, _ auth.Principal, _ string, quotaBytes int64) (service.AppResult, error) {
	s.lastQuotaBytes = quotaBytes
	if s.updateKnowledgeQuotaErr != nil {
		return service.AppResult{}, s.updateKnowledgeQuotaErr
	}
	return s.updateKnowledgeQuotaResult, nil
}
```

- [ ] **Step 6: Verify app backend**

Run:

```bash
go test ./internal/service -run 'TestUpdateAppKnowledgeQuota|TestGetAppExposeRuntimeImageOnlyToPlatformAdmin' -v
go test ./internal/api/handlers -run 'TestUpdateKnowledgeQuota|TestAppsGetHappy' -v
```

Expected: PASS.

- [ ] **Step 7: Commit app quota endpoint**

```bash
git add internal/service/app_service.go internal/service/app_service_test.go internal/api/handlers/dto.go internal/api/handlers/apps.go internal/api/handlers/apps_test.go
git commit -m "feat(app): 增加实例知识库容量编辑接口" -m "新增 PATCH /apps/{appId}/knowledge/quota，允许平台管理员和本企业管理员更新单个实例的知识库容量上限。"
```

---

### Task 5: Knowledge Service Usage and Upload Enforcement

**Files:**
- Modify: `internal/service/knowledge_service.go`
- Modify: `internal/service/knowledge_service_test.go`
- Modify: `internal/api/handlers/knowledge.go`
- Modify: `internal/api/handlers/knowledge_test.go`

- [ ] **Step 1: Add knowledge service tests**

In `internal/service/knowledge_service_test.go`, add:

```go
// TestRAGFlowKnowledgeListOrgIncludesQuota 验证企业知识库列表返回已用、上限和剩余空间。
func TestRAGFlowKnowledgeListOrgIncludesQuota(t *testing.T) {
	svc, store, _ := newRAGFlowKnowledgeTestService(t)
	store.org.KnowledgeQuotaBytes = 100
	store.docs["doc-a"] = testDocument(t, "org", "a.md", store.orgDataset.ID)
	doc := store.docs["doc-a"]
	doc.SizeBytes = 40
	store.docs["doc-a"] = doc

	result, err := svc.ListOrg(context.Background(), orgKnowledgeAdmin(), testKnowledgeOrg, 1, 50, "", "")
	require.NoError(t, err)

	assert.Equal(t, int64(40), result.UsedBytes)
	assert.Equal(t, int64(100), result.QuotaBytes)
	assert.Equal(t, int64(60), result.RemainingBytes)
}

// TestRAGFlowKnowledgeUploadOrgRejectsQuotaExceeded 验证企业知识库累计空间不足时不调用 RAGFlow 上传。
func TestRAGFlowKnowledgeUploadOrgRejectsQuotaExceeded(t *testing.T) {
	svc, store, rf := newRAGFlowKnowledgeTestService(t)
	store.org.KnowledgeQuotaBytes = 10
	store.docs["doc-a"] = testDocument(t, "org", "a.md", store.orgDataset.ID)
	doc := store.docs["doc-a"]
	doc.SizeBytes = 8
	store.docs["doc-a"] = doc

	_, err := svc.SaveOrgFile(context.Background(), orgKnowledgeAdmin(), testKnowledgeOrg, "b.md", strings.NewReader("bbb"), 3)

	require.ErrorIs(t, err, ErrKnowledgeQuotaExceeded)
	assert.Empty(t, rf.uploadCalls)
}

// TestRAGFlowKnowledgeUploadAppAllowsExactQuota 验证实例知识库刚好达到容量上限时允许上传。
func TestRAGFlowKnowledgeUploadAppAllowsExactQuota(t *testing.T) {
	svc, store, rf := newRAGFlowKnowledgeTestService(t)
	app := store.apps[testKnowledgeApp]
	app.KnowledgeQuotaBytes = 10
	store.apps[testKnowledgeApp] = app
	store.docs["doc-a"] = testDocument(t, "app", "a.md", store.appDataset.ID)
	doc := store.docs["doc-a"]
	doc.AppID = null.StringFrom(testKnowledgeApp)
	doc.SizeBytes = 8
	store.docs["doc-a"] = doc

	_, err := svc.SaveAppFile(context.Background(), appOwnerPrincipal(), testKnowledgeApp, "b.md", strings.NewReader("bb"), 2)

	require.NoError(t, err)
	require.Len(t, rf.uploadCalls, 1)
}

// TestRuntimeAddRejectsQuotaExceeded 验证 runtime token 写入实例知识库也不能绕过容量限制。
func TestRuntimeAddRejectsQuotaExceeded(t *testing.T) {
	svc, store, rf := newRAGFlowKnowledgeTestService(t)
	app := store.apps[testKnowledgeApp]
	app.KnowledgeQuotaBytes = 5
	store.apps[testKnowledgeApp] = app
	store.appsByToken[HashAppRuntimeToken(testRuntimeToken)] = app

	_, err := svc.RuntimeAddFile(context.Background(), testRuntimeToken, "research.md", strings.NewReader("report"), 6)

	require.ErrorIs(t, err, ErrKnowledgeQuotaExceeded)
	assert.Empty(t, rf.uploadCalls)
}
```

- [ ] **Step 2: Run knowledge service tests and verify they fail**

Run:

```bash
go test ./internal/service -run 'TestRAGFlowKnowledge.*Quota|TestRuntimeAddRejectsQuotaExceeded' -v
```

Expected: FAIL because list result fields and quota checks are missing.

- [ ] **Step 3: Extend knowledge store interface and result**

In `internal/service/knowledge_service.go`, add to `KnowledgeStore`:

```go
// SumRAGFlowDocumentsSizeByScope 统计当前知识库累计占用，包含所有解析状态。
SumRAGFlowDocumentsSizeByScope(ctx context.Context, arg sqlc.SumRAGFlowDocumentsSizeByScopeParams) (int64, error)
```

Extend `KnowledgeListResult`:

```go
UsedBytes      int64 `json:"used_bytes"`
QuotaBytes     int64 `json:"quota_bytes"`
RemainingBytes int64 `json:"remaining_bytes"`
```

- [ ] **Step 4: Add quota helpers to knowledge service**

Add functions near `listDocuments`:

```go
func (s *KnowledgeService) knowledgeUsedBytes(ctx context.Context, scope, orgID, appID string) (int64, error) {
	appIDNull := null.String{}
	if appID != "" {
		appIDNull = null.StringFrom(appID)
	}
	used, err := s.store.SumRAGFlowDocumentsSizeByScope(ctx, sqlc.SumRAGFlowDocumentsSizeByScopeParams{
		ScopeType: scope,
		OrgID:     orgID,
		AppID:     appIDNull,
	})
	if err != nil {
		return 0, fmt.Errorf("统计知识库空间失败: %w", err)
	}
	return used, nil
}

func (s *KnowledgeService) ensureKnowledgeQuotaAvailable(ctx context.Context, scope, orgID, appID string, quotaBytes, uploadBytes int64) error {
	if uploadBytes < 0 {
		return fmt.Errorf("知识库文件大小不能为负数")
	}
	used, err := s.knowledgeUsedBytes(ctx, scope, orgID, appID)
	if err != nil {
		return err
	}
	remaining := quotaBytes - used
	if remaining < uploadBytes {
		return fmt.Errorf("%w: 知识库空间不足，剩余 %s", ErrKnowledgeQuotaExceeded, formatKnowledgeBytes(knowledgeQuotaRemainingBytes(quotaBytes, used)))
	}
	return nil
}

func formatKnowledgeBytes(value int64) string {
	const mb = 1024 * 1024
	const gb = 1024 * mb
	if value >= gb && value%gb == 0 {
		return fmt.Sprintf("%dGB", value/gb)
	}
	if value >= mb {
		return fmt.Sprintf("%dMB", value/mb)
	}
	return fmt.Sprintf("%dB", value)
}
```

- [ ] **Step 5: Add quota data to list paths**

Change `listDocuments` signature to accept `quotaBytes int64` and set result fields:

```go
func (s *KnowledgeService) listDocuments(ctx context.Context, dataset sqlc.RagflowDataset, scope string, orgID, appID string, quotaBytes int64, page, pageSize int32, keyword, status string) (KnowledgeListResult, error) {
```

Before returning, add:

```go
usedBytes, err := s.knowledgeUsedBytes(ctx, scope, orgID, appID)
if err != nil {
	return KnowledgeListResult{}, err
}
return KnowledgeListResult{
	Items:          results,
	Total:          total,
	UsedBytes:      usedBytes,
	QuotaBytes:     quotaBytes,
	RemainingBytes: knowledgeQuotaRemainingBytes(quotaBytes, usedBytes),
}, nil
```

Update callers:

```go
org, err := s.store.GetOrganization(ctx, orgID)
...
return s.listDocuments(ctx, dataset, "org", dataset.OrgID, "", org.KnowledgeQuotaBytes, page, pageSize, keyword, status)
```

and:

```go
return s.listDocuments(ctx, dataset, "app", app.OrgID, strOrEmpty(dataset.AppID), app.KnowledgeQuotaBytes, page, pageSize, keyword, status)
```

- [ ] **Step 6: Enforce quota on upload paths**

In `SaveOrgFile`, load org and call:

```go
org, err := s.store.GetOrganization(ctx, orgID)
if errors.Is(err, sql.ErrNoRows) {
	return KnowledgeDocumentResult{}, ErrNotFound
}
if err != nil {
	return KnowledgeDocumentResult{}, fmt.Errorf("查询企业失败: %w", err)
}
if err := s.ensureKnowledgeQuotaAvailable(ctx, "org", orgID, "", org.KnowledgeQuotaBytes, size); err != nil {
	return KnowledgeDocumentResult{}, err
}
```

In `SaveAppFile`, after app permission check and before RAGFlow upload:

```go
if err := s.ensureKnowledgeQuotaAvailable(ctx, "app", app.OrgID, app.ID, app.KnowledgeQuotaBytes, size); err != nil {
	return KnowledgeDocumentResult{}, err
}
```

In `RuntimeAddFile`, after app lookup:

```go
if err := s.ensureKnowledgeQuotaAvailable(ctx, "app", app.OrgID, app.ID, app.KnowledgeQuotaBytes, size); err != nil {
	return KnowledgeDocumentResult{}, err
}
```

- [ ] **Step 7: Update fake knowledge store**

In `newFakeKnowledgeStore`, set defaults:

```go
app.KnowledgeQuotaBytes = KnowledgeQuotaDefaultBytes
org.KnowledgeQuotaBytes = KnowledgeQuotaDefaultBytes
```

Add method:

```go
func (s *fakeKnowledgeStore) SumRAGFlowDocumentsSizeByScope(_ context.Context, arg sqlc.SumRAGFlowDocumentsSizeByScopeParams) (int64, error) {
	var total int64
	for _, doc := range s.docs {
		if doc.ScopeType != arg.ScopeType || doc.OrgID != arg.OrgID {
			continue
		}
		if arg.AppID.Valid && doc.AppID.String != arg.AppID.String {
			continue
		}
		if !arg.AppID.Valid && doc.AppID.Valid {
			continue
		}
		total += doc.SizeBytes
	}
	return total, nil
}
```

- [ ] **Step 8: Add handler tests for unknown size and quota error**

In `internal/api/handlers/knowledge_test.go`, add:

```go
// TestKnowledgeUploadOrgRejectsUnknownContentLength 验证未知请求体大小时不允许上传，避免 RAGFlow 上传后才发现超限。
func TestKnowledgeUploadOrgRejectsUnknownContentLength(t *testing.T) {
	stub := &knowledgeServiceStub{}
	router := newKnowledgeTestRouter(t, stub)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/organizations/org-1/knowledge?filename=stream.md", io.NopCloser(bytes.NewBufferString("content")))
	req.ContentLength = -1
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgAdmin, OrgID: "org-1"})
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Equal(t, 0, stub.saveOrgCalls)
}

// TestKnowledgeUploadOrgMapsQuotaExceeded 验证知识库空间不足映射为 409。
func TestKnowledgeUploadOrgMapsQuotaExceeded(t *testing.T) {
	stub := &knowledgeServiceStub{saveOrgErr: fmt.Errorf("%w: 知识库空间不足，剩余 1MB", service.ErrKnowledgeQuotaExceeded)}
	router := newKnowledgeTestRouter(t, stub)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/organizations/org-1/knowledge?filename=big.md", bytes.NewBufferString("content"))
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgAdmin, OrgID: "org-1"})
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusConflict, w.Code)
	assert.Contains(t, w.Body.String(), "KNOWLEDGE_QUOTA_EXCEEDED")
	assert.Contains(t, w.Body.String(), "剩余 1MB")
}
```

Add missing imports if needed:

```go
import "fmt"
```

- [ ] **Step 9: Update handler upload size parsing and error mapping**

In `internal/api/handlers/knowledge.go`, change `requestContentLength` to:

```go
func requestContentLength(c *gin.Context) (int64, bool) {
	if raw := c.GetHeader("Content-Length"); raw != "" {
		size, err := strconv.ParseInt(raw, 10, 64)
		return size, err == nil && size >= 0
	}
	if c.Request.ContentLength >= 0 {
		return c.Request.ContentLength, true
	}
	return 0, false
}
```

Change `prepareKnowledgeOctetStreamUpload`:

```go
func prepareKnowledgeOctetStreamUpload(c *gin.Context) (int64, bool) {
	size, ok := requestContentLength(c)
	if !ok {
		c.JSON(http.StatusBadRequest, apierror.New("BAD_REQUEST", "缺少有效的文件大小信息"))
		return 0, false
	}
	if size > maxKnowledgeUploadBytes {
		c.JSON(http.StatusBadRequest, apierror.New("BAD_REQUEST", maxKnowledgeUploadMessage))
		return size, false
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxKnowledgeUploadBytes)
	return size, true
}
```

In `writeKnowledgeError`, add before default:

```go
case errors.Is(err, service.ErrKnowledgeQuotaExceeded):
	c.JSON(http.StatusConflict, apierror.New("KNOWLEDGE_QUOTA_EXCEEDED", validationServiceMessage(err, service.ErrKnowledgeQuotaExceeded)))
```

- [ ] **Step 10: Verify knowledge backend**

Run:

```bash
go test ./internal/service -run 'TestRAGFlowKnowledge.*Quota|TestRuntimeAddRejectsQuotaExceeded|TestRAGFlowKnowledgeUploadOrgTriggersParse|TestRuntimeAddWritesOnlyCurrentAppDataset' -v
go test ./internal/api/handlers -run 'TestKnowledgeUploadOrg(RejectsUnknownContentLength|MapsQuotaExceeded|ReturnsDocument|RejectsOversizedBody)' -v
```

Expected: PASS.

- [ ] **Step 11: Commit knowledge quota enforcement**

```bash
git add internal/service/knowledge_service.go internal/service/knowledge_service_test.go internal/api/handlers/knowledge.go internal/api/handlers/knowledge_test.go
git commit -m "feat(knowledge): 按累计容量限制知识库上传" -m "知识库列表返回已用、上限和剩余空间，并在企业、实例和 runtime 写入路径统一执行容量校验。"
```

---

### Task 6: Frontend API Hooks and Permissions

**Files:**
- Modify: `web/src/api/hooks/useOrganizations.ts`
- Modify: `web/src/api/hooks/useApps.ts`
- Modify: `web/src/api/hooks/useKnowledge.ts`
- Modify: `web/src/api/hooks/useKnowledge.spec.ts`
- Modify: `web/src/domain/permissions.ts`
- Modify: `web/src/domain/permissions.spec.ts`

- [ ] **Step 1: Add frontend permission tests**

In `web/src/domain/permissions.spec.ts`, add:

```ts
describe('canUpdateAppKnowledgeQuota', () => {
  // 覆盖平台管理员可作为运维兜底编辑任意实例知识库容量。
  it('allows platform admin', () => {
    expect(canUpdateAppKnowledgeQuota({ role: 'platform_admin' }, { org_id: 'org-1' })).toBe(true)
  })

  // 覆盖企业管理员只能编辑本企业实例知识库容量。
  it('allows only same-org org admin', () => {
    expect(canUpdateAppKnowledgeQuota({ role: 'org_admin', org_id: 'org-1' }, { org_id: 'org-1' })).toBe(true)
    expect(canUpdateAppKnowledgeQuota({ role: 'org_admin', org_id: 'org-2' }, { org_id: 'org-1' })).toBe(false)
  })

  // 覆盖普通成员不可编辑容量，即使是自己的实例也不允许。
  it('rejects org member', () => {
    expect(canUpdateAppKnowledgeQuota({ role: 'org_member', id: 'u1', org_id: 'org-1' }, { org_id: 'org-1', owner_user_id: 'u1' })).toBe(false)
  })
})
```

- [ ] **Step 2: Implement frontend permission helper**

In `web/src/domain/permissions.ts`, add:

```ts
// canUpdateAppKnowledgeQuota：实例知识库容量由企业管理员设置，平台管理员可运维兜底。
// 普通成员不能编辑容量，即使该实例属于自己。
export function canUpdateAppKnowledgeQuota(
  user: PermissionUser | null | undefined,
  app: PermissionApp | null | undefined,
): boolean {
  if (!user || !app) return false
  if (user.role === 'platform_admin') return true
  if (user.role === 'org_admin') return user.org_id === app.org_id
  return false
}
```

- [ ] **Step 3: Update API hook types**

In `web/src/api/hooks/useOrganizations.ts`, add to both payload interfaces:

```ts
// 企业知识库累计容量上限，单位字节；未传时后端创建默认 1GB、更新保留旧值。
knowledge_quota_bytes?: number
```

In `web/src/api/hooks/useApps.ts`, add to `AppDTO`:

```ts
// knowledge_quota_bytes 是实例知识库累计容量上限，单位字节。
knowledge_quota_bytes: number
```

Add mutation:

```ts
// useUpdateAppKnowledgeQuota 更新单个实例知识库容量，并刷新实例详情与知识库列表。
export function useUpdateAppKnowledgeQuota(appId: Ref<string | undefined>) {
  const client = useQueryClient()
  return useMutation({
    mutationFn: async (quotaBytes: number) => {
      if (!appId.value) throw new Error('缺少实例 ID')
      const response = await apiRequest<{ app: AppDTO }>(
        `/api/v1/apps/${appId.value}/knowledge/quota`,
        { method: 'PATCH', body: { quota_bytes: quotaBytes } },
      )
      return response.app
    },
    onSuccess: () => {
      void client.invalidateQueries({ queryKey: appKey(appId.value) })
      void client.invalidateQueries({ queryKey: ['knowledge', 'app', appId.value] })
    },
  })
}
```

- [ ] **Step 4: Update knowledge listing type and helpers**

In `web/src/api/hooks/useKnowledge.ts`, extend `KnowledgeListing`:

```ts
// KnowledgeListing 是扁平文件列表响应，附带当前知识库容量信息。
export interface KnowledgeListing {
  items: KnowledgeDocument[]
  total: number
  used_bytes: number
  quota_bytes: number
  remaining_bytes: number
}
```

Add helpers:

```ts
// formatKnowledgeBytes 统一前端知识库容量展示。
export function formatKnowledgeBytes(value: number): string {
  if (value < 1024) return `${value} B`
  if (value < 1024 * 1024) return `${(value / 1024).toFixed(1)} KB`
  if (value < 1024 * 1024 * 1024) return `${(value / 1024 / 1024).toFixed(1)} MB`
  return `${(value / 1024 / 1024 / 1024).toFixed(2)} GB`
}

// isKnowledgeUploadOverRemaining 判断文件是否超过知识库当前剩余容量。
export function isKnowledgeUploadOverRemaining(file: Pick<File, 'size'>, listing: Pick<KnowledgeListing, 'remaining_bytes'> | null | undefined): boolean {
  if (!listing) return false
  return file.size > listing.remaining_bytes
}
```

- [ ] **Step 5: Add hook tests**

In `web/src/api/hooks/useKnowledge.spec.ts`, add imports and tests:

```ts
import {
  formatKnowledgeBytes,
  isKnowledgeUploadOverRemaining,
} from './useKnowledge'
```

Add:

```ts
describe('知识库累计容量展示', () => {
  // 覆盖容量格式化：GB、MB 和字节按固定精度展示。
  it('格式化知识库容量字节数', () => {
    expect(formatKnowledgeBytes(1024 * 1024 * 1024)).toBe('1.00 GB')
    expect(formatKnowledgeBytes(512 * 1024 * 1024)).toBe('512.0 MB')
    expect(formatKnowledgeBytes(512)).toBe('512 B')
  })

  // 覆盖剩余空间本地拦截：超过 remaining_bytes 时阻止上传。
  it('判断文件是否超过剩余空间', () => {
    expect(isKnowledgeUploadOverRemaining({ size: 11 }, { remaining_bytes: 10 })).toBe(true)
    expect(isKnowledgeUploadOverRemaining({ size: 10 }, { remaining_bytes: 10 })).toBe(false)
    expect(isKnowledgeUploadOverRemaining({ size: 10 }, null)).toBe(false)
  })
})
```

- [ ] **Step 6: Verify frontend hooks and permissions**

Run:

```bash
cd web && npm test -- --run src/api/hooks/useKnowledge.spec.ts src/domain/permissions.spec.ts
```

Expected: PASS.

- [ ] **Step 7: Commit frontend hooks**

```bash
git add web/src/api/hooks/useOrganizations.ts web/src/api/hooks/useApps.ts web/src/api/hooks/useKnowledge.ts web/src/api/hooks/useKnowledge.spec.ts web/src/domain/permissions.ts web/src/domain/permissions.spec.ts
git commit -m "feat(web): 增加知识库容量前端类型与权限" -m "前端 API hooks 暴露知识库容量字段和实例容量更新 mutation，并补充实例容量编辑权限 helper。"
```

---

### Task 7: Organization Management Frontend

**Files:**
- Modify: `web/src/pages/platform/OrganizationsPage.vue`
- Modify: `web/src/pages/platform/OrganizationsPage.spec.ts`

- [ ] **Step 1: Add organization page tests**

In `web/src/pages/platform/OrganizationsPage.spec.ts`, update mocked organization to include:

```ts
knowledge_quota_bytes: 1073741824,
```

Add test:

```ts
// 企业知识库容量由平台管理员在企业表单中设置，提交给后端时使用 bytes。
it('创建企业时提交企业知识库容量 bytes', async () => {
  createOrganization.mockResolvedValue({ id: 'org-2', name: '新企业', code: 'new-org', status: 'active' })
  const wrapper = mountPage()

  const openButton = wrapper.findAll('button').find(button => button.text().includes('新增企业'))
  expect(openButton).toBeTruthy()
  await openButton!.trigger('click')
  await nextTick()

  const inputs = wrapper.findAll('input')
  await inputs[0].setValue('新企业')
  await inputs[1].setValue('new-org')
  await inputs[2].setValue('org-admin')
  await inputs[3].setValue('企业管理员')
  await inputs[4].setValue('secret-password')

  const quotaInput = inputs.find(input => (input.element as HTMLInputElement).value === '1')
  expect(quotaInput).toBeTruthy()
  await quotaInput!.setValue('2')
  await wrapper.find('form').trigger('submit')

  expect(createOrganization).toHaveBeenCalledWith(expect.objectContaining({
    knowledge_quota_bytes: 2 * 1024 * 1024 * 1024,
  }))
})
```

- [ ] **Step 2: Add GB/bytes helpers in page**

In `OrganizationsPage.vue`, add constants near form state:

```ts
const knowledgeQuotaGBDefault = 1
const bytesPerGB = 1024 * 1024 * 1024

// quotaBytesToGB 将后端字节容量转为企业表单中的 GB 数字。
function quotaBytesToGB(bytes?: number): number {
  if (!bytes || bytes <= 0) return knowledgeQuotaGBDefault
  return Math.round(bytes / bytesPerGB)
}

// quotaGBToBytes 将企业表单中的 GB 数字转为后端 bytes；空值回落为 1GB。
function quotaGBToBytes(gb?: number): number {
  return Math.max(1, Math.round(gb ?? knowledgeQuotaGBDefault)) * bytesPerGB
}
```

- [ ] **Step 3: Add create/edit form fields**

Add to `editForm`:

```ts
knowledge_quota_gb: knowledgeQuotaGBDefault,
```

Add to `initial` form:

```ts
knowledge_quota_gb: knowledgeQuotaGBDefault,
```

In `openEditForm`, set:

```ts
editForm.knowledge_quota_gb = quotaBytesToGB(org.knowledge_quota_bytes)
```

In create `toPayload`, include:

```ts
knowledge_quota_bytes: quotaGBToBytes(f.knowledge_quota_gb),
```

In `submitEditOrganization`, include:

```ts
knowledge_quota_bytes: quotaGBToBytes(editForm.knowledge_quota_gb),
```

- [ ] **Step 4: Add form input and list column**

In the form grid, add after instance limit:

```vue
<n-grid-item>
  <n-form-item label="企业知识库空间 (GB)">
    <n-input-number
      v-if="modalMode === 'create'"
      v-model:value="form.knowledge_quota_gb"
      :min="1" :precision="0" style="width: 100%"
    />
    <n-input-number
      v-else
      v-model:value="editForm.knowledge_quota_gb"
      :min="1" :precision="0" style="width: 100%"
    />
  </n-form-item>
</n-grid-item>
```

In `columns`, add:

```ts
{
  title: '知识库空间',
  key: 'knowledge_quota_bytes',
  render: (row: Organization) => typeof row.knowledge_quota_bytes === 'number'
    ? `${Math.round(row.knowledge_quota_bytes / bytesPerGB)}GB` : '1GB',
},
```

- [ ] **Step 5: Keep existing required-field tests stable**

The new quota input is inserted after the existing instance-count input, so the first five required inputs in existing create-organization tests remain:

```text
0 名称
1 企业标识
2 管理员用户名
3 管理员姓名
4 管理员密码
```

Do not change those indexes. The new quota-specific test above finds the quota input by its default value and asserts the submitted `knowledge_quota_bytes`.

- [ ] **Step 6: Verify organization page**

Run:

```bash
cd web && npm test -- --run src/pages/platform/OrganizationsPage.spec.ts
```

Expected: PASS.

- [ ] **Step 7: Commit organization frontend**

```bash
git add web/src/pages/platform/OrganizationsPage.vue web/src/pages/platform/OrganizationsPage.spec.ts
git commit -m "feat(web): 企业表单支持知识库空间配置" -m "平台企业创建和编辑表单增加企业知识库空间输入，并以 bytes 提交给后端。"
```

---

### Task 8: Knowledge Page Capacity UI and App Quota Modal

**Files:**
- Modify: `web/src/pages/knowledge/OrgKnowledgePage.vue`
- Modify: `web/src/pages/knowledge/OrgKnowledgePage.spec.ts`
- Modify: `web/src/pages/apps/AppKnowledgeTab.vue`
- Modify: `web/src/pages/apps/AppKnowledgeTab.spec.ts`

- [ ] **Step 1: Update page test mocks**

In both knowledge page specs, update mocked `KnowledgeListing`:

```ts
used_bytes: 5,
quota_bytes: 100,
remaining_bytes: 95,
```

- [ ] **Step 2: Add org page tests**

In `OrgKnowledgePage.spec.ts`, add:

```ts
// 覆盖企业知识库容量展示：页面应显示已用和上限。
it('展示企业知识库容量信息', () => {
  const wrapper = mountPage()

  expect(wrapper.text()).toContain('已用')
  expect(wrapper.text()).toContain('剩余')
})

// 覆盖企业知识库剩余容量拦截：超过 remaining_bytes 时不创建上传会话。
it('拒绝超过企业知识库剩余空间的文件', async () => {
  const wrapper = mountPage()
  const input = wrapper.find('input[type="file"]')
  const file = new File(['x'], 'too-large.md')
  Object.defineProperty(file, 'size', { value: 96 })

  Object.defineProperty(input.element, 'files', { value: [file], configurable: true })
  await input.trigger('change')

  expect(mocks.warning).toHaveBeenCalledWith(expect.stringContaining('知识库空间不足'))
  expect(mocks.run).not.toHaveBeenCalled()
})
```

- [ ] **Step 3: Add app page tests**

In `AppKnowledgeTab.spec.ts`, add to provided app:

```ts
knowledge_quota_bytes: 100,
```

Add test:

```ts
// 覆盖实例知识库容量编辑入口：企业管理员可看到编辑空间按钮。
it('企业管理员可看到实例知识库空间编辑入口', () => {
  const wrapper = mountTab()

  expect(wrapper.text()).toContain('编辑空间')
})

// 覆盖实例知识库剩余容量拦截：超过 remaining_bytes 时不创建上传会话。
it('拒绝超过实例知识库剩余空间的文件', async () => {
  const wrapper = mountTab()
  const input = wrapper.find('input[type="file"]')
  const file = new File(['x'], 'too-large.md')
  Object.defineProperty(file, 'size', { value: 96 })

  Object.defineProperty(input.element, 'files', { value: [file], configurable: true })
  await input.trigger('change')

  expect(mocks.warning).toHaveBeenCalledWith(expect.stringContaining('知识库空间不足'))
  expect(mocks.run).not.toHaveBeenCalled()
})
```

- [ ] **Step 4: Implement quota summary in org page**

In `OrgKnowledgePage.vue`, import:

```ts
formatKnowledgeBytes,
isKnowledgeUploadOverRemaining,
```

Add computed:

```ts
const quotaSummary = computed(() => listing.value
  ? `已用 ${formatKnowledgeBytes(listing.value.used_bytes)} / 上限 ${formatKnowledgeBytes(listing.value.quota_bytes)}，剩余 ${formatKnowledgeBytes(listing.value.remaining_bytes)}`
  : '')
```

Render under organization selector:

```vue
<p v-if="quotaSummary" class="state-text">{{ quotaSummary }}</p>
```

In `onUpload`, after single-file max check:

```ts
if (isKnowledgeUploadOverRemaining(file, listing.value)) {
  message.warning(`知识库空间不足，剩余 ${formatKnowledgeBytes(listing.value?.remaining_bytes ?? 0)}`)
  return
}
```

- [ ] **Step 5: Implement app quota summary and modal**

In `AppKnowledgeTab.vue`, import:

```ts
NModal, NInputNumber, NSpace
```

Import hooks/helpers:

```ts
useUpdateAppKnowledgeQuota,
formatKnowledgeBytes,
isKnowledgeUploadOverRemaining,
```

Import permission:

```ts
canUpdateAppKnowledgeQuota
```

Add state:

```ts
const bytesPerGB = 1024 * 1024 * 1024
const showQuotaModal = ref(false)
const quotaGB = ref<number>(1)
const quotaFeedback = ref('')
const quotaError = ref(false)
const updateQuotaMutation = useUpdateAppKnowledgeQuota(appIdRef)
const canEditQuota = computed(() => canUpdateAppKnowledgeQuota(auth.user, app?.value))
const quotaSummary = computed(() => listing.data.value
  ? `已用 ${formatKnowledgeBytes(listing.data.value.used_bytes)} / 上限 ${formatKnowledgeBytes(listing.data.value.quota_bytes)}，剩余 ${formatKnowledgeBytes(listing.data.value.remaining_bytes)}`
  : '')

function openQuotaModal() {
  quotaGB.value = Math.max(1, Math.round((app?.value?.knowledge_quota_bytes ?? bytesPerGB) / bytesPerGB))
  quotaFeedback.value = ''
  quotaError.value = false
  showQuotaModal.value = true
}

async function submitQuota() {
  quotaFeedback.value = ''
  quotaError.value = false
  try {
    await updateQuotaMutation.mutateAsync(Math.max(1, Math.round(quotaGB.value)) * bytesPerGB)
    showQuotaModal.value = false
  } catch (err) {
    quotaError.value = true
    quotaFeedback.value = err instanceof Error ? err.message : '更新空间失败'
  }
}
```

Render under the loading/error block before the table:

```vue
<p v-if="quotaSummary" class="state-text">{{ quotaSummary }}</p>
```

Add header button near upload actions:

```vue
<n-button v-if="canEditQuota" size="small" @click="openQuotaModal">编辑空间</n-button>
```

Add modal:

```vue
<n-modal v-model:show="showQuotaModal" preset="card" title="编辑实例知识库空间" style="width: 420px">
  <n-form label-placement="top" @submit.prevent="submitQuota">
    <n-form-item label="空间大小 (GB)">
      <n-input-number v-model:value="quotaGB" :min="1" :precision="0" style="width: 100%" />
    </n-form-item>
    <n-space justify="end">
      <n-button @click="showQuotaModal = false">取消</n-button>
      <n-button type="primary" attr-type="submit" :loading="updateQuotaMutation.isPending.value">保存</n-button>
    </n-space>
    <p v-if="quotaFeedback" class="state-text" :class="{ danger: quotaError }">{{ quotaFeedback }}</p>
  </n-form>
</n-modal>
```

In `onUploadFile`, after single-file max check:

```ts
if (isKnowledgeUploadOverRemaining(file, listing.data.value)) {
  message.warning(`知识库空间不足，剩余 ${formatKnowledgeBytes(listing.data.value?.remaining_bytes ?? 0)}`)
  return
}
```

- [ ] **Step 6: Verify knowledge pages**

Run:

```bash
cd web && npm test -- --run src/pages/knowledge/OrgKnowledgePage.spec.ts src/pages/apps/AppKnowledgeTab.spec.ts
```

Expected: PASS.

- [ ] **Step 7: Commit knowledge page UI**

```bash
git add web/src/pages/knowledge/OrgKnowledgePage.vue web/src/pages/knowledge/OrgKnowledgePage.spec.ts web/src/pages/apps/AppKnowledgeTab.vue web/src/pages/apps/AppKnowledgeTab.spec.ts
git commit -m "feat(web): 展示并编辑知识库容量" -m "企业和实例知识库页面展示已用、上限和剩余空间；实例知识库页增加容量编辑入口并在上传前按剩余空间拦截。"
```

---

### Task 9: OpenAPI, Generated Types, and Verification

**Files:**
- Generated: `openapi/openapi.yaml`
- Generated: `web/src/api/generated.ts`
- Modify only files that fail the verification commands in this task.

- [ ] **Step 1: Regenerate OpenAPI and frontend types**

Run:

```bash
make openapi-gen
make web-types-gen
```

Expected: both commands exit 0 and generated files contain:

```text
knowledge_quota_bytes
used_bytes
quota_bytes
remaining_bytes
/apps/{appId}/knowledge/quota
```

- [ ] **Step 2: Run focused backend tests**

Run:

```bash
go test ./internal/auth ./internal/service ./internal/api/handlers
```

Expected: PASS.

- [ ] **Step 3: Run frontend tests and typecheck**

Run:

```bash
cd web && npm test -- --run src/api/hooks/useKnowledge.spec.ts src/domain/permissions.spec.ts src/pages/platform/OrganizationsPage.spec.ts src/pages/knowledge/OrgKnowledgePage.spec.ts src/pages/apps/AppKnowledgeTab.spec.ts
cd web && npm run typecheck
```

Expected: PASS.

- [ ] **Step 4: Run OpenAPI check**

Run:

```bash
make openapi-check
```

Expected: PASS with no generated drift.

- [ ] **Step 5: Browser verification**

Start local dev server if not already running:

```bash
cd web && npm run dev -- --host 0.0.0.0
```

Use a real browser and the local account from `AGENTS.md`:

1. Log in as platform admin at `http://ocm.localhost` with username `admin`, password `admin123`, org code empty.
2. Open enterprise management and create or edit an enterprise with enterprise knowledge quota `1GB`.
3. Open enterprise knowledge page and confirm the page shows used, quota, and remaining capacity.
4. Try selecting a file larger than the remaining capacity and confirm the UI shows "知识库空间不足" without starting upload progress.
5. Log in or switch to an org admin, open an instance knowledge page, click "编辑空间", set quota to `1GB`, and save.
6. Confirm the instance knowledge page shows capacity info and rejects files larger than remaining capacity.
7. Delete a knowledge file and confirm remaining capacity increases after list refresh.

If local RAGFlow is not available, still complete browser checks for form rendering, quota modal, and frontend pre-upload rejection. Record that actual successful upload/delete against RAGFlow was not verified.

- [ ] **Step 6: Commit generated contracts and verification fixes**

```bash
git add openapi/openapi.yaml web/src/api/generated.ts
git add -u
git commit -m "chore(openapi): 同步知识库容量接口契约" -m "同步组织、应用和知识库容量字段的 OpenAPI 与前端生成类型，并完成相关测试验证。"
```

---

## Final Verification Checklist

- [ ] `go test ./internal/auth ./internal/service ./internal/api/handlers`
- [ ] `cd web && npm test -- --run src/api/hooks/useKnowledge.spec.ts src/domain/permissions.spec.ts src/pages/platform/OrganizationsPage.spec.ts src/pages/knowledge/OrgKnowledgePage.spec.ts src/pages/apps/AppKnowledgeTab.spec.ts`
- [ ] `cd web && npm run typecheck`
- [ ] `make openapi-check`
- [ ] Real browser verification completed or explicitly documented if RAGFlow/local stack is unavailable.

## Spec Coverage Review

- Enterprise quota by platform admin: Task 3 and Task 7.
- Per-instance quota by org admin and editable later: Task 4, Task 6, and Task 8.
- Mandatory 1GB default and no unlimited state: Task 1, Task 2, and Task 3.
- Failed/stopped files count toward usage: Task 1 sum query and Task 5 service tests.
- Delete releases capacity through live sum: Task 5 uses current `ragflow_documents` rows, no cache.
- Runtime write path cannot bypass quota: Task 5 includes `RuntimeAddFile`.
- 409 quota error: Task 5 handler tests and mapping.
- OpenAPI/frontend generated types: Task 9.
- Browser validation: Task 9.
