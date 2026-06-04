# Industry Knowledge Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build platform-managed industry knowledge bases, external file upload, assistant-version associations, and runtime search across app, org, and selected industry knowledge bases.

**Architecture:** Add a platform-level `industry_knowledge_bases` resource and extend the existing RAGFlow dataset/document mapping model with `scope_type='industry'`. Reuse `KnowledgeService` and the RAGFlow client for dataset creation, file upload, parse, download, delete, reparse, and status refresh. Assistant versions store industry-base associations in a join table; runtime search reads the app's current version live, so industry selection changes apply without restart or revision bump.

**Tech Stack:** Go, Gin, sqlc, MySQL 8, RAGFlow HTTP API, Vue 3, TypeScript, TanStack Query, Naive UI, Vitest, Playwright/browser verification.

---

## File Structure

Backend schema and queries:

- Create `internal/migrations/000007_industry_knowledge.up.sql`: add industry knowledge tables and extend RAGFlow scope constraints.
- Create `internal/migrations/000007_industry_knowledge.down.sql`: reverse the migration.
- Modify `internal/store/queries/ragflow_knowledge.sql`: add industry dataset/document queries.
- Create `internal/store/queries/industry_knowledge.sql`: add industry base CRUD and assistant-version association queries.
- Regenerate `internal/store/sqlc/*.go` with `make sqlc-generate`.

Backend service and handlers:

- Modify `internal/config/config.go`, `internal/config/loader.go`, `config/manager.example.yaml`: add external upload token config.
- Modify `internal/auth/authorizer.go` and `internal/auth/authorizer_test.go`: add `CanManageIndustryKnowledge`.
- Modify `internal/service/errors.go`: add industry-specific sentinel errors.
- Create `internal/service/industry_knowledge_service.go`: industry library CRUD and file lifecycle methods on `KnowledgeService`.
- Modify `internal/service/knowledge_service.go`: add industry store methods, dataset helpers, document-source fields, and runtime search expansion.
- Modify `internal/service/ragflow_parse_status_refresher.go`: ensure queued/running industry documents are refreshed by the existing parse-status reconciler.
- Create `internal/api/handlers/industry_knowledge.go`: platform and external upload routes.
- Modify `internal/api/handlers/dto.go`: add industry knowledge DTOs and assistant-version industry IDs.
- Modify `cmd/server/main.go`: register industry knowledge routes and pass configured upload token.

Assistant versions:

- Modify `internal/store/queries/assistant_versions.sql`: keep version CRUD unchanged; industry association queries live in `industry_knowledge.sql`.
- Modify `internal/service/assistant_version_service.go`: include industry associations in inputs/results and persist them without bumping revision.
- Modify `internal/api/handlers/assistant_versions.go`: pass `industry_knowledge_base_ids`.
- Modify `internal/api/handlers/assistant_versions_test.go` and `internal/service/assistant_version_service_test.go`: cover association behavior and no revision bump.

Frontend:

- Create `web/src/api/hooks/useIndustryKnowledge.ts`: industry list/file hooks.
- Create `web/src/pages/platform/IndustryKnowledgePage.vue`: platform management page.
- Create `web/src/pages/platform/IndustryKnowledgePage.spec.ts`: page tests.
- Modify `web/src/api/hooks/useAssistantVersions.ts`: include industry IDs and returned refs.
- Modify `web/src/pages/platform/AssistantVersionsPage.vue`: add searchable multi-select and warning.
- Modify `web/src/pages/platform/AssistantVersionsPage.spec.ts`: cover selection and warning.
- Modify `web/src/app/router.ts` and `web/src/layouts/DashboardLayout.vue`: add platform-only route/menu.

Generated artifacts and docs:

- Regenerate `openapi/openapi.yaml` and `web/src/api/generated.ts`.
- Update `docs/knowledge-base.md`, `docs/user-manual.md`, and `docs/technical-design.md`.
- Update `runtime/hermes/hermes-v2026.5.16/renderer/render_skills.py` and `runtime/hermes/hermes-v2026.5.16/renderer/render_soul_md.py` copy so Hermes knows search results can include industry knowledge.

---

### Task 1: Schema And sqlc Queries

**Files:**
- Create: `internal/migrations/000007_industry_knowledge.up.sql`
- Create: `internal/migrations/000007_industry_knowledge.down.sql`
- Create: `internal/store/queries/industry_knowledge.sql`
- Modify: `internal/store/queries/ragflow_knowledge.sql`
- Modify: `internal/migrations/migrations_test.go`
- Generated: `internal/store/sqlc/*.go`

- [ ] **Step 1: Write migration tests**

Add this migration discovery test with adjacent Chinese comments in `internal/migrations/migrations_test.go`:

```go
// TestMigrationsIncludeIndustryKnowledge 验证行业知识库迁移已进入嵌入迁移集合，避免新增 SQL 文件遗漏到发布包。
func TestMigrationsIncludeIndustryKnowledge(t *testing.T) {
	// 版本 7 是行业知识库迁移；First/Last 能防止迁移文件命名或嵌入路径缺失。
	first, err := iofs.First(migrationsFS, ".")
	require.NoError(t, err)
	last, err := iofs.Last(migrationsFS, ".")
	require.NoError(t, err)

	assert.Equal(t, uint(1), first)
	assert.GreaterOrEqual(t, last, uint(7))
}
```

- [ ] **Step 2: Run migration test and verify it fails**

Run:

```bash
rtk go test ./internal/migrations -run TestMigrationsIncludeIndustryKnowledge -count=1
```

Expected: fail because migration version 7 does not exist yet.

- [ ] **Step 3: Add migration up SQL**

Create `internal/migrations/000007_industry_knowledge.up.sql`:

```sql
-- 行业知识库是平台级全局资源，名称只在未删除记录中唯一。
CREATE TABLE industry_knowledge_bases (
    id CHAR(36) PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    created_by VARCHAR(255) NOT NULL,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    deleted_at DATETIME(6) NULL,
    name_active_key VARCHAR(255) GENERATED ALWAYS AS (CASE WHEN deleted_at IS NULL THEN name END) VIRTUAL,
    UNIQUE KEY uk_industry_knowledge_bases_name_active (name_active_key)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- 行业库不归属企业，因此 org_id 需要允许 NULL；org/app 的原有约束通过新 CHECK 保留。
ALTER TABLE ragflow_datasets
    DROP CHECK ragflow_datasets_scope_app_check,
    MODIFY org_id CHAR(36) NULL,
    ADD COLUMN industry_knowledge_base_id CHAR(36) NULL AFTER app_id,
    ADD COLUMN industry_scope_key CHAR(36)
        GENERATED ALWAYS AS (CASE WHEN scope_type = 'industry' THEN industry_knowledge_base_id END) VIRTUAL,
    ADD CONSTRAINT ragflow_datasets_scope_target_check CHECK (
        (scope_type = 'org' AND org_id IS NOT NULL AND app_id IS NULL AND industry_knowledge_base_id IS NULL)
        OR (scope_type = 'app' AND org_id IS NOT NULL AND app_id IS NOT NULL AND industry_knowledge_base_id IS NULL)
        OR (scope_type = 'industry' AND org_id IS NULL AND app_id IS NULL AND industry_knowledge_base_id IS NOT NULL)
    ),
    ADD CONSTRAINT fk_ragflow_datasets_industry_id
        FOREIGN KEY (industry_knowledge_base_id) REFERENCES industry_knowledge_bases(id),
    ADD UNIQUE KEY uk_ragflow_datasets_industry_unique (industry_scope_key);

ALTER TABLE ragflow_documents
    DROP CHECK ragflow_documents_scope_type_check,
    DROP CHECK ragflow_documents_scope_app_check,
    MODIFY org_id CHAR(36) NULL,
    ADD COLUMN industry_knowledge_base_id CHAR(36) NULL AFTER app_id,
    ADD COLUMN industry_document_base_key CHAR(36)
        GENERATED ALWAYS AS (CASE WHEN scope_type = 'industry' THEN industry_knowledge_base_id END) VIRTUAL,
    ADD COLUMN industry_document_name_key VARCHAR(255)
        GENERATED ALWAYS AS (CASE WHEN scope_type = 'industry' THEN name END) VIRTUAL,
    ADD CONSTRAINT ragflow_documents_scope_type_check CHECK (scope_type IN ('org','app','industry')),
    ADD CONSTRAINT ragflow_documents_scope_target_check CHECK (
        (scope_type = 'org' AND org_id IS NOT NULL AND app_id IS NULL AND industry_knowledge_base_id IS NULL)
        OR (scope_type = 'app' AND org_id IS NOT NULL AND app_id IS NOT NULL AND industry_knowledge_base_id IS NULL)
        OR (scope_type = 'industry' AND org_id IS NULL AND app_id IS NULL AND industry_knowledge_base_id IS NOT NULL)
    ),
    ADD CONSTRAINT fk_ragflow_documents_industry_id
        FOREIGN KEY (industry_knowledge_base_id) REFERENCES industry_knowledge_bases(id),
    ADD UNIQUE KEY uk_ragflow_documents_industry_name (industry_document_base_key, industry_document_name_key);

CREATE TABLE assistant_version_industry_knowledge_bases (
    version_id CHAR(36) NOT NULL,
    industry_knowledge_base_id CHAR(36) NOT NULL,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY (version_id, industry_knowledge_base_id),
    CONSTRAINT fk_av_industry_version
        FOREIGN KEY (version_id) REFERENCES assistant_versions(id) ON DELETE CASCADE,
    CONSTRAINT fk_av_industry_base
        FOREIGN KEY (industry_knowledge_base_id) REFERENCES industry_knowledge_bases(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
```

- [ ] **Step 4: Add migration down SQL**

Create `internal/migrations/000007_industry_knowledge.down.sql`:

```sql
DROP TABLE assistant_version_industry_knowledge_bases;

ALTER TABLE ragflow_documents
    DROP INDEX uk_ragflow_documents_industry_name,
    DROP FOREIGN KEY fk_ragflow_documents_industry_id,
    DROP CHECK ragflow_documents_scope_target_check,
    DROP CHECK ragflow_documents_scope_type_check,
    DROP COLUMN industry_document_name_key,
    DROP COLUMN industry_document_base_key,
    DROP COLUMN industry_knowledge_base_id,
    MODIFY org_id CHAR(36) NOT NULL,
    ADD CONSTRAINT ragflow_documents_scope_type_check CHECK (scope_type IN ('org','app')),
    ADD CONSTRAINT ragflow_documents_scope_app_check CHECK (
        (scope_type = 'org' AND app_id IS NULL) OR (scope_type = 'app' AND app_id IS NOT NULL));

ALTER TABLE ragflow_datasets
    DROP INDEX uk_ragflow_datasets_industry_unique,
    DROP FOREIGN KEY fk_ragflow_datasets_industry_id,
    DROP CHECK ragflow_datasets_scope_target_check,
    DROP COLUMN industry_scope_key,
    DROP COLUMN industry_knowledge_base_id,
    MODIFY org_id CHAR(36) NOT NULL,
    ADD CONSTRAINT ragflow_datasets_scope_app_check CHECK (
        (scope_type = 'org' AND app_id IS NULL) OR (scope_type = 'app' AND app_id IS NOT NULL));

DROP TABLE industry_knowledge_bases;
```

- [ ] **Step 5: Add sqlc industry queries**

Create `internal/store/queries/industry_knowledge.sql`:

```sql
-- name: CreateIndustryKnowledgeBase :exec
INSERT INTO industry_knowledge_bases (id, name, created_by)
VALUES (?, ?, ?);

-- name: GetIndustryKnowledgeBase :one
SELECT *
FROM industry_knowledge_bases
WHERE id = ? AND deleted_at IS NULL;

-- name: GetIndustryKnowledgeBaseByName :one
SELECT *
FROM industry_knowledge_bases
WHERE name = ? AND deleted_at IS NULL;

-- name: ListIndustryKnowledgeBases :many
SELECT ikb.*,
       CAST(COALESCE(COUNT(rd.id), 0) AS SIGNED) AS document_count
FROM industry_knowledge_bases ikb
LEFT JOIN ragflow_documents rd
  ON rd.scope_type = 'industry'
 AND rd.industry_knowledge_base_id = ikb.id
WHERE ikb.deleted_at IS NULL
  AND (sqlc.narg(keyword) IS NULL OR ikb.name LIKE CONCAT('%', sqlc.narg(keyword), '%'))
GROUP BY ikb.id
ORDER BY ikb.updated_at DESC, ikb.id DESC
LIMIT ? OFFSET ?;

-- name: CountIndustryKnowledgeBases :one
SELECT count(*)
FROM industry_knowledge_bases
WHERE deleted_at IS NULL
  AND (sqlc.narg(keyword) IS NULL OR name LIKE CONCAT('%', sqlc.narg(keyword), '%'));

-- name: RenameIndustryKnowledgeBase :exec
UPDATE industry_knowledge_bases
SET name = ?, updated_at = now()
WHERE id = ? AND deleted_at IS NULL;

-- name: SoftDeleteIndustryKnowledgeBase :exec
UPDATE industry_knowledge_bases
SET deleted_at = now(), updated_at = now()
WHERE id = ? AND deleted_at IS NULL;

-- name: CountAssistantVersionsUsingIndustryKnowledgeBase :one
SELECT count(*)
FROM assistant_version_industry_knowledge_bases avikb
JOIN assistant_versions av ON av.id = avikb.version_id
WHERE av.deleted_at IS NULL
  AND avikb.industry_knowledge_base_id = ?;

-- name: ReplaceAssistantVersionIndustryKnowledgeBases :exec
DELETE FROM assistant_version_industry_knowledge_bases
WHERE version_id = ?;

-- name: AddAssistantVersionIndustryKnowledgeBase :exec
INSERT INTO assistant_version_industry_knowledge_bases (version_id, industry_knowledge_base_id)
VALUES (?, ?);

-- name: ListIndustryKnowledgeBasesByAssistantVersion :many
SELECT ikb.*
FROM assistant_version_industry_knowledge_bases avikb
JOIN industry_knowledge_bases ikb ON ikb.id = avikb.industry_knowledge_base_id
WHERE avikb.version_id = ?
  AND ikb.deleted_at IS NULL
ORDER BY ikb.name ASC, ikb.id ASC;
```

- [ ] **Step 6: Extend RAGFlow knowledge queries**

Append to `internal/store/queries/ragflow_knowledge.sql`:

```sql
-- name: CreateRAGFlowIndustryDatasetMapping :exec
INSERT IGNORE INTO ragflow_datasets (
    id, scope_type, org_id, app_id, industry_knowledge_base_id,
    ragflow_dataset_id, name, status, last_error, create_claim_token
) VALUES (
    sqlc.arg(id), 'industry', NULL, NULL, sqlc.arg(industry_knowledge_base_id),
    NULL, sqlc.arg(name), 'creating', NULL, sqlc.arg(create_claim_token)
);

-- name: GetRAGFlowIndustryDataset :one
SELECT *
FROM ragflow_datasets
WHERE scope_type = 'industry' AND industry_knowledge_base_id = ?;

-- name: ListRAGFlowIndustryDocuments :many
SELECT *
FROM ragflow_documents
WHERE scope_type = 'industry'
  AND industry_knowledge_base_id = ?
  AND (sqlc.narg(parse_status) IS NULL OR parse_status = sqlc.narg(parse_status))
  AND (sqlc.narg(keywords) IS NULL OR name LIKE CONCAT('%', sqlc.narg(keywords), '%'))
ORDER BY created_at DESC, id DESC
LIMIT ? OFFSET ?;

-- name: CountRAGFlowIndustryDocuments :one
SELECT count(*)
FROM ragflow_documents
WHERE scope_type = 'industry'
  AND industry_knowledge_base_id = ?
  AND (sqlc.narg(parse_status) IS NULL OR parse_status = sqlc.narg(parse_status))
  AND (sqlc.narg(keywords) IS NULL OR name LIKE CONCAT('%', sqlc.narg(keywords), '%'));

-- name: GetRAGFlowIndustryDocumentByName :one
SELECT *
FROM ragflow_documents
WHERE scope_type = 'industry'
  AND industry_knowledge_base_id = ?
  AND name = ?;
```

- [ ] **Step 7: Generate sqlc code**

Run:

```bash
rtk make sqlc-generate
```

Expected: sqlc regenerates `internal/store/sqlc/*.go` with new query methods and models.

- [ ] **Step 8: Run migration and sqlc tests**

Run:

```bash
rtk go test ./internal/migrations ./internal/store -count=1
```

Expected: PASS.

- [ ] **Step 9: Commit schema and query work**

Run:

```bash
rtk git add internal/migrations internal/store/queries internal/store/sqlc
rtk git commit -m "feat(knowledge): 增加行业知识库数据模型" -m "新增平台级行业知识库表、行业 scope 的 RAGFlow 映射和助手版本关联表。"
```

---

### Task 2: Config, Permissions, And Errors

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/loader.go`
- Modify: `internal/config/loader_test.go`
- Modify: `config/manager.example.yaml`
- Modify: `internal/auth/authorizer.go`
- Modify: `internal/auth/authorizer_test.go`
- Modify: `internal/service/errors.go`

- [ ] **Step 1: Write config tests**

Add to `internal/config/loader_test.go`:

```go
// TestIndustryKnowledgeUploadTokenAllowsEmptyConfig 验证外部行业库上传 token 可为空，空配置表示禁用外部上传接口。
func TestIndustryKnowledgeUploadTokenAllowsEmptyConfig(t *testing.T) {
	yaml := fullValidYAML() + `
industry_knowledge:
  upload_token: ""
`
	cfg, err := LoadFile(writeTempConfig(t, yaml))

	require.NoError(t, err)
	assert.Empty(t, cfg.IndustryKnowledge.UploadToken)
}

// TestIndustryKnowledgeUploadTokenRejectsWhitespace 验证只包含空白字符的固定鉴权字符串会被拒绝，避免启动后所有请求都无法通过。
func TestIndustryKnowledgeUploadTokenRejectsWhitespace(t *testing.T) {
	yaml := fullValidYAML() + `
industry_knowledge:
  upload_token: "   "
`
	_, err := LoadFile(writeTempConfig(t, yaml))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "industry_knowledge.upload_token")
}
```

These tests use `fullValidYAML()` and `LoadFile(writeTempConfig(t, yaml))`, matching the existing tests in `internal/config/loader_test.go`.

- [ ] **Step 2: Add config struct and validation**

In `internal/config/config.go`, add the field to `Config`:

```go
// IndustryKnowledge 描述外部商业知识库上传行业库文件的固定鉴权配置。
IndustryKnowledge IndustryKnowledgeConfig `yaml:"industry_knowledge"`
```

Add the struct:

```go
// IndustryKnowledgeConfig 描述行业知识库外部上传入口配置。
type IndustryKnowledgeConfig struct {
	// UploadToken 是外部商业知识库上传接口要求的固定鉴权字符串；为空表示禁用外部上传入口。
	UploadToken string `yaml:"upload_token"`
}
```

In `internal/config/loader.go`, call validation from `Config.validate()`:

```go
if err := c.IndustryKnowledge.validate(); err != nil {
	return err
}
```

Add the validator:

```go
// validate 校验行业知识库外部上传配置；空字符串表示禁用，只包含空白字符是配置错误。
func (c IndustryKnowledgeConfig) validate() error {
	if c.UploadToken == "" {
		return nil
	}
	if strings.TrimSpace(c.UploadToken) == "" {
		return fmt.Errorf("industry_knowledge.upload_token 不能只包含空白字符")
	}
	return nil
}
```

- [ ] **Step 3: Update example config**

In `config/manager.example.yaml`, add:

```yaml
industry_knowledge:
  # 外部商业知识库上传行业文件的固定鉴权字符串；留空表示禁用外部上传入口。
  upload_token: ""
```

- [ ] **Step 4: Write permission tests**

Add to `internal/auth/authorizer_test.go`:

```go
// TestCanManageIndustryKnowledge 验证行业知识库是平台级资源，仅平台管理员可管理。
func TestCanManageIndustryKnowledge(t *testing.T) {
	assert.True(t, CanManageIndustryKnowledge(Principal{Role: domain.UserRolePlatformAdmin}))
	assert.False(t, CanManageIndustryKnowledge(Principal{Role: domain.UserRoleOrgAdmin, OrgID: "org-1"}))
	assert.False(t, CanManageIndustryKnowledge(Principal{Role: domain.UserRoleOrgMember, OrgID: "org-1"}))
	assert.False(t, CanManageIndustryKnowledge(Principal{}))
}
```

- [ ] **Step 5: Add permission predicate**

In `internal/auth/authorizer.go`:

```go
// CanManageIndustryKnowledge 判断主体是否可管理平台级行业知识库。
// 行业库是全局平台资源，不归属企业，只允许平台管理员创建、编辑、删除和管理文件。
func CanManageIndustryKnowledge(p Principal) bool {
	return p.Role == domain.UserRolePlatformAdmin
}
```

- [ ] **Step 6: Add service errors**

In `internal/service/errors.go`, add sentinel errors:

```go
var (
	// ErrIndustryKnowledgeNotFound 表示行业知识库不存在或已删除。
	ErrIndustryKnowledgeNotFound = errors.New("行业知识库不存在")
	// ErrIndustryKnowledgeNameTaken 表示未删除行业库中已存在同名记录。
	ErrIndustryKnowledgeNameTaken = errors.New("行业知识库名称已存在")
	// ErrIndustryKnowledgeInUse 表示行业库仍被未删除助手版本引用，不能删除。
	ErrIndustryKnowledgeInUse = errors.New("行业知识库正在被助手版本引用")
	// ErrIndustryKnowledgeUploadTokenInvalid 表示外部上传固定鉴权字符串缺失或错误。
	ErrIndustryKnowledgeUploadTokenInvalid = errors.New("行业知识库上传鉴权失败")
)
```

- [ ] **Step 7: Run tests**

Run:

```bash
rtk go test ./internal/config ./internal/auth -count=1
```

Expected: PASS.

- [ ] **Step 8: Commit config and permission work**

Run:

```bash
rtk git add internal/config config/manager.example.yaml internal/auth internal/service/errors.go
rtk git commit -m "feat(knowledge): 增加行业库上传配置和权限谓词" -m "新增外部上传固定 token 配置，并将行业知识库管理权限收敛到平台管理员。"
```

---

### Task 3: Industry Knowledge Service

**Files:**
- Create: `internal/service/industry_knowledge_service.go`
- Modify: `internal/service/knowledge_service.go`
- Modify: `internal/service/knowledge_service_test.go`

- [ ] **Step 1: Extend fake store for industry tests**

In `internal/service/knowledge_service_test.go`, extend fake constants:

```go
const (
	testIndustryKnowledgeBaseID = "00000000-0000-0000-0000-000000000f01"
	testRemoteIndustryDatasetID = "industry-ds"
)
```

Extend `fakeKnowledgeStore` with:

```go
industryBases       map[string]sqlc.IndustryKnowledgeBasis
industryDataset     sqlc.RagflowDataset
missingIndustryDataset bool
deletedIndustryBaseID string
```

Initialize it in `newFakeKnowledgeStore`:

```go
industryBase := sqlc.IndustryKnowledgeBasis{
	ID:        testIndustryKnowledgeBaseID,
	Name:      "保险",
	CreatedBy: "u-platform",
}
industryDataset := sqlc.RagflowDataset{
	ID:                      mustParseUUID("00000000-0000-0000-0000-000000000d03"),
	ScopeType:               "industry",
	IndustryKnowledgeBaseID: null.StringFrom(testIndustryKnowledgeBaseID),
	RagflowDatasetID:        null.StringFrom(testRemoteIndustryDatasetID),
	Name:                    "oc-industry",
	Status:                  "active",
	UpdatedAt:               time.Now(),
}
```

The plan uses sqlc's MySQL singularization name `sqlc.IndustryKnowledgeBasis` for the `industry_knowledge_bases` model.

- [ ] **Step 2: Add failing service tests**

Add tests with adjacent Chinese comments:

```go
// TestCreateIndustryKnowledgeBasePlatformOnly 验证行业库创建只允许平台管理员，且名称会去除首尾空白。
func TestCreateIndustryKnowledgeBasePlatformOnly(t *testing.T) {
	svc, store, _ := newRAGFlowKnowledgeTestService(t)

	created, err := svc.CreateIndustryKnowledgeBase(context.Background(), auth.Principal{UserID: "u1", Role: domain.UserRolePlatformAdmin}, "  保险  ")
	require.NoError(t, err)
	assert.Equal(t, "保险", created.Name)
	assert.Contains(t, store.industryBases, created.ID)

	_, err = svc.CreateIndustryKnowledgeBase(context.Background(), auth.Principal{Role: domain.UserRoleOrgAdmin, OrgID: testKnowledgeOrg}, "金融")
	require.ErrorIs(t, err, ErrKnowledgeForbidden)
}

// TestExternalUploadCreatesIndustryAndOverwritesSameName 验证外部上传会按行业名称自动创建行业库，并用同名覆盖语义替换旧文件。
func TestExternalUploadCreatesIndustryAndOverwritesSameName(t *testing.T) {
	svc, store, rf := newRAGFlowKnowledgeTestService(t)
	store.industryBases = map[string]sqlc.IndustryKnowledgeBasis{}
	store.missingIndustryDataset = true
	rf.createDatasetResult = ragflow.Dataset{ID: testRemoteIndustryDatasetID, Name: "保险"}
	oldDoc := testDocument(t, "industry", "policy.pdf", store.industryDataset.ID)
	oldDoc.IndustryKnowledgeBaseID = null.StringFrom(testIndustryKnowledgeBaseID)
	store.docs["old-doc"] = oldDoc

	doc, err := svc.ExternalUploadIndustryFile(context.Background(), "保险", "policy.pdf", strings.NewReader("new"), 3)
	require.NoError(t, err)

	assert.Equal(t, "policy.pdf", doc.Name)
	require.Len(t, rf.deleteCalls, 1)
	assert.Equal(t, []string{"remote-doc-1"}, rf.deleteCalls[0].documentIDs)
	require.Len(t, rf.uploadCalls, 1)
	assert.Equal(t, testRemoteIndustryDatasetID, rf.uploadCalls[0].datasetID)
}

// TestRuntimeSearchRetrievesEachIndustryWithTopK 验证 runtime 检索会按 app、org、每个行业库分别调用 RAGFlow，行业库各自使用 top_k。
func TestRuntimeSearchRetrievesEachIndustryWithTopK(t *testing.T) {
	svc, store, rf := newRAGFlowKnowledgeTestService(t)
	app := store.apps[testKnowledgeApp]
	app.VersionID = null.StringFrom("ver-1")
	store.apps[testKnowledgeApp] = app
	store.appsByToken[HashAppRuntimeToken(testRuntimeToken)] = app
	store.versionIndustryBases["ver-1"] = []sqlc.IndustryKnowledgeBasis{
		{ID: "industry-a", Name: "保险"},
		{ID: "industry-b", Name: "银行"},
	}
	store.industryDatasets["industry-a"] = industryDataset("industry-a", "remote-a")
	store.industryDatasets["industry-b"] = industryDataset("industry-b", "remote-b")

	_, err := svc.RuntimeSearch(context.Background(), testRuntimeToken, "理赔", 6)
	require.NoError(t, err)

	require.Len(t, rf.retrieveCalls, 4)
	assert.Equal(t, []string{testRemoteAppDatasetID}, rf.retrieveCalls[0].datasetIDs)
	assert.Equal(t, []string{testRemoteOrgDatasetID}, rf.retrieveCalls[1].datasetIDs)
	assert.Equal(t, []string{"remote-a"}, rf.retrieveCalls[2].datasetIDs)
	assert.Equal(t, []string{"remote-b"}, rf.retrieveCalls[3].datasetIDs)
	assert.Equal(t, int32(6), rf.retrieveCalls[2].topK)
	assert.Equal(t, int32(6), rf.retrieveCalls[3].topK)
}
```

Add helper:

```go
// industryDataset 构造行业库 dataset 测试行，避免每个 runtime 检索用例重复拼 null 字段。
func industryDataset(industryID, remoteID string) sqlc.RagflowDataset {
	return sqlc.RagflowDataset{
		ID:                      newUUID(),
		ScopeType:               "industry",
		IndustryKnowledgeBaseID: null.StringFrom(industryID),
		RagflowDatasetID:        null.StringFrom(remoteID),
		Name:                    "industry-" + industryID,
		Status:                  "active",
		UpdatedAt:               time.Now(),
	}
}
```

- [ ] **Step 3: Run tests and verify failure**

Run:

```bash
rtk go test ./internal/service -run 'Test(CreateIndustryKnowledgeBasePlatformOnly|ExternalUploadCreatesIndustryAndOverwritesSameName|RuntimeSearchRetrievesEachIndustryWithTopK)' -count=1
```

Expected: fail because service methods and fake store methods do not exist yet.

- [ ] **Step 4: Add industry service result types and store methods**

In `internal/service/knowledge_service.go`, extend `KnowledgeStore` with generated query methods:

```go
CreateIndustryKnowledgeBase(ctx context.Context, arg sqlc.CreateIndustryKnowledgeBaseParams) error
GetIndustryKnowledgeBase(ctx context.Context, id string) (sqlc.IndustryKnowledgeBasis, error)
GetIndustryKnowledgeBaseByName(ctx context.Context, name string) (sqlc.IndustryKnowledgeBasis, error)
ListIndustryKnowledgeBases(ctx context.Context, arg sqlc.ListIndustryKnowledgeBasesParams) ([]sqlc.ListIndustryKnowledgeBasesRow, error)
CountIndustryKnowledgeBases(ctx context.Context, keyword interface{}) (int64, error)
RenameIndustryKnowledgeBase(ctx context.Context, arg sqlc.RenameIndustryKnowledgeBaseParams) error
SoftDeleteIndustryKnowledgeBase(ctx context.Context, id string) error
CountAssistantVersionsUsingIndustryKnowledgeBase(ctx context.Context, industryKnowledgeBaseID string) (int64, error)
CreateRAGFlowIndustryDatasetMapping(ctx context.Context, arg sqlc.CreateRAGFlowIndustryDatasetMappingParams) error
GetRAGFlowIndustryDataset(ctx context.Context, industryKnowledgeBaseID null.String) (sqlc.RagflowDataset, error)
ListRAGFlowIndustryDocuments(ctx context.Context, arg sqlc.ListRAGFlowIndustryDocumentsParams) ([]sqlc.RagflowDocument, error)
CountRAGFlowIndustryDocuments(ctx context.Context, arg sqlc.CountRAGFlowIndustryDocumentsParams) (int64, error)
GetRAGFlowIndustryDocumentByName(ctx context.Context, arg sqlc.GetRAGFlowIndustryDocumentByNameParams) (sqlc.RagflowDocument, error)
ListIndustryKnowledgeBasesByAssistantVersion(ctx context.Context, versionID string) ([]sqlc.IndustryKnowledgeBasis, error)
```

The query parameter names used by service code are `sqlc.CreateRAGFlowIndustryDatasetMappingParams`, `sqlc.ListRAGFlowIndustryDocumentsParams`, `sqlc.CountRAGFlowIndustryDocumentsParams`, and `sqlc.GetRAGFlowIndustryDocumentByNameParams`.

In `internal/service/industry_knowledge_service.go`, add:

```go
package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"

	"github.com/guregu/null/v5"

	"oc-manager/internal/auth"
	"oc-manager/internal/store/sqlc"
)

// IndustryKnowledgeBaseResult 是平台管理面展示的行业知识库摘要。
type IndustryKnowledgeBaseResult struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	DocumentCount int64  `json:"document_count"`
	CreatedAt     string `json:"created_at"`
	UpdatedAt     string `json:"updated_at"`
}

// IndustryKnowledgeBaseRef 是助手版本和检索命中的行业库来源引用。
type IndustryKnowledgeBaseRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
```

- [ ] **Step 5: Implement industry CRUD methods**

Add methods in `industry_knowledge_service.go`:

```go
// CreateIndustryKnowledgeBase 创建平台级行业知识库，名称在未删除记录内唯一。
func (s *KnowledgeService) CreateIndustryKnowledgeBase(ctx context.Context, principal auth.Principal, name string) (IndustryKnowledgeBaseResult, error) {
	if !auth.CanManageIndustryKnowledge(principal) {
		return IndustryKnowledgeBaseResult{}, ErrKnowledgeForbidden
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return IndustryKnowledgeBaseResult{}, fmt.Errorf("行业知识库名称不能为空")
	}
	if _, err := s.store.GetIndustryKnowledgeBaseByName(ctx, name); err == nil {
		return IndustryKnowledgeBaseResult{}, ErrIndustryKnowledgeNameTaken
	} else if !errors.Is(err, sql.ErrNoRows) {
		return IndustryKnowledgeBaseResult{}, fmt.Errorf("查询行业知识库名称失败: %w", err)
	}
	id := newUUID()
	if err := s.store.CreateIndustryKnowledgeBase(ctx, sqlc.CreateIndustryKnowledgeBaseParams{
		ID: id, Name: name, CreatedBy: principal.UserID,
	}); err != nil {
		return IndustryKnowledgeBaseResult{}, fmt.Errorf("创建行业知识库失败: %w", err)
	}
	row, err := s.store.GetIndustryKnowledgeBase(ctx, id)
	if err != nil {
		return IndustryKnowledgeBaseResult{}, fmt.Errorf("读取新建行业知识库失败: %w", err)
	}
	return toIndustryKnowledgeBaseResult(row, 0), nil
}
```

Also implement `ListIndustryKnowledgeBases`, `RenameIndustryKnowledgeBase`, and `DeleteIndustryKnowledgeBase`. `DeleteIndustryKnowledgeBase` must call `CountAssistantVersionsUsingIndustryKnowledgeBase`, then `GetRAGFlowIndustryDataset`, `DeleteDatasets`, `DeleteRAGFlowDatasetMapping`, and `SoftDeleteIndustryKnowledgeBase`.

- [ ] **Step 6: Implement industry dataset and file methods**

Add methods:

```go
// ExternalUploadIndustryFile 用外部上传身份按行业名称自动创建行业库并上传文件。
func (s *KnowledgeService) ExternalUploadIndustryFile(ctx context.Context, industryName, filename string, content io.Reader, size int64) (KnowledgeDocumentResult, error) {
	base, err := s.ensureIndustryKnowledgeBaseByName(ctx, strings.TrimSpace(industryName), "external:industry-knowledge")
	if err != nil {
		return KnowledgeDocumentResult{}, err
	}
	return s.saveIndustryFile(ctx, base, filename, content, size, "external:industry-knowledge")
}

// SaveIndustryFile 供平台管理员手动上传行业库文件；同名文件覆盖。
func (s *KnowledgeService) SaveIndustryFile(ctx context.Context, principal auth.Principal, industryID, filename string, content io.Reader, size int64) (KnowledgeDocumentResult, error) {
	if !auth.CanManageIndustryKnowledge(principal) {
		return KnowledgeDocumentResult{}, ErrKnowledgeForbidden
	}
	base, err := s.getIndustryKnowledgeBase(ctx, industryID)
	if err != nil {
		return KnowledgeDocumentResult{}, err
	}
	return s.saveIndustryFile(ctx, base, filename, content, size, principal.UserID)
}
```

`saveIndustryFile` must:

1. Normalize filename with `path.Base(strings.TrimSpace(filename))`.
2. Ensure industry dataset.
3. Look up existing document by industry ID and normalized filename.
4. Delete the old RAGFlow document and mapping before upload.
5. Call `uploadToDataset` with `scope_type='industry'` support.

Update `uploadToDataset` signature so it accepts a struct target:

```go
type knowledgeUploadTarget struct {
	Dataset sqlc.RagflowDataset
	AppID string
	IndustryKnowledgeBaseID string
	CreatedBy string
}
```

This avoids adding positional parameters that are easy to swap.

- [ ] **Step 7: Extend runtime search**

In `RuntimeSearch`, after app and org retrieval, add:

```go
if app.VersionID.Valid {
	industryBases, err := s.store.ListIndustryKnowledgeBasesByAssistantVersion(ctx, app.VersionID.String)
	if err != nil {
		return KnowledgeSearchResult{}, fmt.Errorf("查询助手版本行业知识库失败: %w", err)
	}
	for _, base := range industryBases {
		dataset, err := s.getIndustryDataset(ctx, base.ID)
		if err != nil {
			return KnowledgeSearchResult{}, err
		}
		remoteID, err := requireRemoteDatasetID(dataset)
		if err != nil {
			return KnowledgeSearchResult{}, err
		}
		chunks, err := s.ragflowClient().Retrieve(ctx, []string{remoteID}, question, topK)
		if err != nil {
			return KnowledgeSearchResult{}, fmt.Errorf("RAGFlow 检索行业知识库 %s 失败: %w", base.Name, err)
		}
		hits = append(hits, searchHitsFromChunksWithIndustry("industry", chunks, base.ID, base.Name)...)
	}
}
```

Extend `KnowledgeSearchHit`:

```go
IndustryKnowledgeBaseID   string `json:"industry_knowledge_base_id,omitempty"`
IndustryKnowledgeBaseName string `json:"industry_knowledge_base_name,omitempty"`
```

- [ ] **Step 8: Update fake store and run service tests**

Implement fake store methods matching the new `KnowledgeStore` interface. Run:

```bash
rtk go test ./internal/service -count=1
```

Expected: PASS.

- [ ] **Step 9: Commit service work**

Run:

```bash
rtk git add internal/service
rtk git commit -m "feat(knowledge): 增加行业知识库服务能力" -m "复用 RAGFlow 知识库服务实现行业库管理、同名覆盖上传和 runtime 行业库检索。"
```

---

### Task 4: HTTP Handlers And Routes

**Files:**
- Create: `internal/api/handlers/industry_knowledge.go`
- Create: `internal/api/handlers/industry_knowledge_test.go`
- Modify: `internal/api/handlers/dto.go`
- Modify: `cmd/server/main.go`

- [ ] **Step 1: Write handler tests**

Create `internal/api/handlers/industry_knowledge_test.go` with tests:

```go
// TestExternalIndustryUploadRejectsMissingToken 验证外部上传缺少固定鉴权字符串时返回 401 且不调用 service。
func TestExternalIndustryUploadRejectsMissingToken(t *testing.T) {
	stub := &industryKnowledgeServiceStub{}
	router := newIndustryKnowledgeTestRouter(t, stub, "secret-token")

	body, contentType := multipartIndustryUploadBody(t, "保险", "policy.pdf", "content")
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/external/industry-knowledge/files", body)
	req.Header.Set("Content-Type", contentType)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Equal(t, 0, stub.externalUploadCalls)
}

// TestExternalIndustryUploadAcceptsConfiguredToken 验证外部上传携带配置 token 时透传 industry_name 和文件给 service。
func TestExternalIndustryUploadAcceptsConfiguredToken(t *testing.T) {
	stub := &industryKnowledgeServiceStub{saveResult: service.KnowledgeDocumentResult{ID: "doc-1", Name: "policy.pdf", ParseStatus: "queued"}}
	router := newIndustryKnowledgeTestRouter(t, stub, "secret-token")

	body, contentType := multipartIndustryUploadBody(t, "保险", "policy.pdf", "content")
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/external/industry-knowledge/files", body)
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("X-OC-Industry-Knowledge-Token", "secret-token")
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusAccepted, w.Code)
	assert.Equal(t, "保险", stub.externalIndustryName)
	assert.Equal(t, "policy.pdf", stub.externalFilename)
}

// TestIndustryKnowledgePlatformRoutesRequirePlatformAdmin 验证平台行业库管理接口拒绝非平台管理员。
func TestIndustryKnowledgePlatformRoutesRequirePlatformAdmin(t *testing.T) {
	stub := &industryKnowledgeServiceStub{listErr: service.ErrKnowledgeForbidden}
	router := newIndustryKnowledgeTestRouter(t, stub, "secret-token")

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/industry-knowledge-bases", nil)
	req = withPrincipal(req, auth.Principal{Role: domain.UserRoleOrgAdmin, OrgID: "org-1"})
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}
```

Add helper `multipartIndustryUploadBody` using `multipart.NewWriter`, with Chinese comments for each test helper.

- [ ] **Step 2: Run handler tests and verify failure**

Run:

```bash
rtk go test ./internal/api/handlers -run 'Test(ExternalIndustryUpload|IndustryKnowledge)' -count=1
```

Expected: fail because handler and routes do not exist.

- [ ] **Step 3: Add DTOs**

In `internal/api/handlers/dto.go`:

```go
// CreateIndustryKnowledgeBaseRequest 是创建行业知识库请求体。
type CreateIndustryKnowledgeBaseRequest struct {
	Name string `json:"name" binding:"required"`
}

// UpdateIndustryKnowledgeBaseRequest 是重命名行业知识库请求体。
type UpdateIndustryKnowledgeBaseRequest struct {
	Name string `json:"name" binding:"required"`
}
```

- [ ] **Step 4: Implement handler**

Create `internal/api/handlers/industry_knowledge.go`:

```go
package handlers

import (
	"context"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"oc-manager/internal/api/apierror"
	"oc-manager/internal/auth"
	"oc-manager/internal/service"
)

const industryKnowledgeTokenHeader = "X-OC-Industry-Knowledge-Token"

type industryKnowledgeService interface {
	ListIndustryKnowledgeBases(ctx context.Context, principal auth.Principal, page, pageSize int32, keyword string) (service.IndustryKnowledgeBaseListResult, error)
	CreateIndustryKnowledgeBase(ctx context.Context, principal auth.Principal, name string) (service.IndustryKnowledgeBaseResult, error)
	RenameIndustryKnowledgeBase(ctx context.Context, principal auth.Principal, id, name string) (service.IndustryKnowledgeBaseResult, error)
	DeleteIndustryKnowledgeBase(ctx context.Context, principal auth.Principal, id string) error
	ListIndustryFiles(ctx context.Context, principal auth.Principal, industryID string, page, pageSize int32, keyword, status string) (service.KnowledgeListResult, error)
	SaveIndustryFile(ctx context.Context, principal auth.Principal, industryID, filename string, content io.Reader, size int64) (service.KnowledgeDocumentResult, error)
	OpenIndustryFile(ctx context.Context, principal auth.Principal, industryID, documentID string) (io.ReadCloser, int64, string, error)
	DeleteIndustryFile(ctx context.Context, principal auth.Principal, industryID, documentID string) error
	ReparseIndustryFile(ctx context.Context, principal auth.Principal, industryID, documentID string) (service.KnowledgeDocumentResult, error)
	ExternalUploadIndustryFile(ctx context.Context, industryName, filename string, content io.Reader, size int64) (service.KnowledgeDocumentResult, error)
}
```

Implement platform CRUD, file routes, and external upload. For external upload, compare:

```go
if h.uploadToken == "" || token != h.uploadToken {
	c.JSON(http.StatusUnauthorized, apierror.New("INDUSTRY_KNOWLEDGE_TOKEN_INVALID", "行业知识库上传鉴权失败"))
	return
}
```

Reference existing `maxKnowledgeUploadBytes`, `maxKnowledgeMultipartOverheadBytes`, and `maxKnowledgeUploadMessage` constants.

- [ ] **Step 5: Register routes**

In `internal/api/router.go`, extend `Dependencies`:

```go
// IndustryKnowledgeUploadToken 是外部商业知识库上传行业文件的固定鉴权字符串；为空时外部上传入口返回 401。
IndustryKnowledgeUploadToken string
```

Register external routes outside the user auth group and platform routes inside the user auth group:

```go
if dep.KnowledgeService != nil {
	industryHandler := handlers.NewIndustryKnowledgeHandler(dep.KnowledgeService, dep.IndustryKnowledgeUploadToken)
	handlers.RegisterExternalIndustryKnowledgeRoutes(router, industryHandler)
}
```

```go
if dep.KnowledgeService != nil {
	handlers.RegisterKnowledgeRoutes(user, handlers.NewKnowledgeHandler(dep.KnowledgeService))
	industryHandler := handlers.NewIndustryKnowledgeHandler(dep.KnowledgeService, dep.IndustryKnowledgeUploadToken)
	handlers.RegisterIndustryKnowledgeRoutes(user, industryHandler)
}
```

In `cmd/server/main.go`, pass the config value into `api.Dependencies`:

```go
IndustryKnowledgeUploadToken: cfg.IndustryKnowledge.UploadToken,
```

- [ ] **Step 6: Run handler tests**

Run:

```bash
rtk go test ./internal/api/handlers -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit handler work**

Run:

```bash
rtk git add internal/api/handlers cmd/server/main.go
rtk git commit -m "feat(knowledge): 增加行业知识库 HTTP 接口" -m "新增平台管理接口和外部 multipart 上传入口，并使用配置 token 做固定鉴权。"
```

---

### Task 5: Assistant Version Industry Associations

**Files:**
- Modify: `internal/service/assistant_version_service.go`
- Modify: `internal/service/assistant_version_service_test.go`
- Modify: `internal/api/handlers/assistant_versions.go`
- Modify: `internal/api/handlers/assistant_versions_test.go`
- Modify: `internal/api/handlers/dto.go`
- Modify: `web/src/api/hooks/useAssistantVersions.ts`

- [ ] **Step 1: Write service tests**

Add to `internal/service/assistant_version_service_test.go`:

```go
// TestAssistantVersionUpdateIndustryKnowledgeDoesNotBumpRevision 验证只修改行业知识库关联立即生效但不递增版本 revision。
func TestAssistantVersionUpdateIndustryKnowledgeDoesNotBumpRevision(t *testing.T) {
	svc, store := newAssistantVersionTestService(t)
	row := store.versions["ver-1"]
	row.Revision = 3
	store.versions["ver-1"] = row
	store.industryBases["industry-1"] = sqlc.IndustryKnowledgeBasis{ID: "industry-1", Name: "保险"}

	result, err := svc.Update(context.Background(), platformAdminPrincipal(), "ver-1", AssistantVersionInput{
		Name: row.Name, Description: row.Description, SystemPrompt: row.SystemPrompt,
		ImageID: row.ImageID, MainModel: row.MainModel, Routing: map[string]string{},
		IndustryKnowledgeBaseIDs: []string{"industry-1"},
	})
	require.NoError(t, err)

	assert.Equal(t, int32(3), result.Revision)
	require.Len(t, result.IndustryKnowledgeBases, 1)
	assert.Equal(t, "保险", result.IndustryKnowledgeBases[0].Name)
}
```

- [ ] **Step 2: Run service test and verify failure**

Run:

```bash
rtk go test ./internal/service -run TestAssistantVersionUpdateIndustryKnowledgeDoesNotBumpRevision -count=1
```

Expected: fail because input/result association fields do not exist.

- [ ] **Step 3: Extend assistant version service store and DTOs**

In `AssistantVersionStore`, add:

```go
GetIndustryKnowledgeBase(ctx context.Context, id string) (sqlc.IndustryKnowledgeBasis, error)
ReplaceAssistantVersionIndustryKnowledgeBases(ctx context.Context, versionID string) error
AddAssistantVersionIndustryKnowledgeBase(ctx context.Context, arg sqlc.AddAssistantVersionIndustryKnowledgeBaseParams) error
ListIndustryKnowledgeBasesByAssistantVersion(ctx context.Context, versionID string) ([]sqlc.IndustryKnowledgeBasis, error)
```

Extend service structs:

```go
type AssistantVersionInput struct {
	Name string
	Description string
	SystemPrompt string
	ImageID string
	MainModel string
	Routing map[string]string
	// IndustryKnowledgeBaseIDs 是该版本运行时额外检索的行业库 ID 列表，保存后立即生效。
	IndustryKnowledgeBaseIDs []string
}

type AssistantVersionResult struct {
	// existing fields...
	IndustryKnowledgeBases []IndustryKnowledgeBaseRef `json:"industry_knowledge_bases"`
}
```

- [ ] **Step 4: Persist associations without bumping revision**

Refactor result conversion from standalone `toAssistantVersionResult(row)` into method:

```go
func (s *AssistantVersionService) toAssistantVersionResult(ctx context.Context, row sqlc.AssistantVersion) (AssistantVersionResult, error) {
	result, err := assistantVersionBaseResult(row)
	if err != nil {
		return AssistantVersionResult{}, err
	}
	bases, err := s.store.ListIndustryKnowledgeBasesByAssistantVersion(ctx, row.ID)
	if err != nil {
		return AssistantVersionResult{}, fmt.Errorf("查询版本行业知识库失败: %w", err)
	}
	for _, base := range bases {
		result.IndustryKnowledgeBases = append(result.IndustryKnowledgeBases, IndustryKnowledgeBaseRef{ID: base.ID, Name: base.Name})
	}
	return result, nil
}
```

In `Update`, keep existing `revision` calculation unchanged, then persist associations after `UpdateAssistantVersion`:

```go
if err := s.replaceIndustryKnowledgeBases(ctx, current.ID, in.IndustryKnowledgeBaseIDs); err != nil {
	return AssistantVersionResult{}, err
}
```

`replaceIndustryKnowledgeBases` must validate every ID exists and dedupe IDs before delete/insert.

- [ ] **Step 5: Extend handler DTOs**

In `dto.go`, add to create/update requests:

```go
// IndustryKnowledgeBaseIDs 是该助手版本运行时额外检索的行业知识库 ID 列表。
IndustryKnowledgeBaseIDs []string `json:"industry_knowledge_base_ids"`
```

In `assistant_versions.go`, pass it into `AssistantVersionInput`.

- [ ] **Step 6: Write handler test for payload**

In `internal/api/handlers/assistant_versions_test.go`:

```go
// TestAssistantVersionUpdatePassesIndustryKnowledgeBaseIDs 验证 handler 将行业库 ID 列表透传给 service。
func TestAssistantVersionUpdatePassesIndustryKnowledgeBaseIDs(t *testing.T) {
	stub := &assistantVersionServiceStub{updateResult: service.AssistantVersionResult{ID: "ver-1", Name: "版本"}}
	router := newAssistantVersionTestRouter(t, stub)

	body := strings.NewReader(`{"name":"版本","system_prompt":"prompt","image_id":"img","main_model":"gpt","routing":{},"industry_knowledge_base_ids":["industry-1"]}`)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/assistant-versions/ver-1", body)
	req = withPrincipal(req, auth.Principal{Role: domain.UserRolePlatformAdmin, UserID: "u1"})
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, []string{"industry-1"}, stub.updateInput.IndustryKnowledgeBaseIDs)
}
```

- [ ] **Step 7: Update frontend API type**

In `web/src/api/hooks/useAssistantVersions.ts`:

```ts
export interface AssistantVersionIndustryKnowledgeBaseDTO {
  id: string
  name: string
}

export interface AssistantVersionDTO {
  // existing fields...
  industry_knowledge_bases?: AssistantVersionIndustryKnowledgeBaseDTO[]
}

export interface AssistantVersionFormPayload {
  // existing fields...
  industry_knowledge_base_ids: string[]
}
```

Update `buildPayload` later in Task 7 to set this field.

- [ ] **Step 8: Run tests**

Run:

```bash
rtk go test ./internal/service ./internal/api/handlers -run 'AssistantVersion' -count=1
rtk npm --prefix web test -- useAssistantVersions
```

Expected: Go tests PASS; frontend hook tests PASS.

- [ ] **Step 9: Commit assistant version associations**

Run:

```bash
rtk git add internal/service internal/api/handlers web/src/api/hooks/useAssistantVersions.ts
rtk git commit -m "feat(version): 支持助手版本关联行业知识库" -m "保存行业库关联时不递增 revision，使运行时检索范围立即按版本配置生效。"
```

---

### Task 6: Frontend Industry Knowledge Management

**Files:**
- Create: `web/src/api/hooks/useIndustryKnowledge.ts`
- Create: `web/src/pages/platform/IndustryKnowledgePage.vue`
- Create: `web/src/pages/platform/IndustryKnowledgePage.spec.ts`
- Modify: `web/src/app/router.ts`
- Modify: `web/src/layouts/DashboardLayout.vue`

- [ ] **Step 1: Add frontend API hook**

Create `web/src/api/hooks/useIndustryKnowledge.ts`:

```ts
import { useMutation, useQuery, useQueryClient } from '@tanstack/vue-query'
import { computed } from 'vue'
import type { Ref } from 'vue'

import { apiRequest } from '@/api/client'
import { xhrUpload } from '@/api/xhrUpload'
import { normalizeKnowledgeListing, type KnowledgeListing } from '@/api/hooks/useKnowledge'

export interface IndustryKnowledgeBaseDTO {
  id: string
  name: string
  document_count: number
  created_at: string
  updated_at: string
}

const listKey = ['industry-knowledge-bases'] as const
const fileKey = (id: string | undefined) => ['industry-knowledge-bases', id, 'knowledge'] as const
```

Implement `useIndustryKnowledgeBasesQuery`, `useCreateIndustryKnowledgeBase`, `useRenameIndustryKnowledgeBase`, `useDeleteIndustryKnowledgeBase`, `useIndustryKnowledgeFilesQuery`, `useUploadIndustryKnowledgeFile`, `useDeleteIndustryKnowledgeFile`, and `useReparseIndustryKnowledgeFile`.

- [ ] **Step 2: Write page tests**

Create `web/src/pages/platform/IndustryKnowledgePage.spec.ts`:

```ts
// 创建行业库：验证页面提交名称并刷新列表。
it('creates an industry knowledge base', async () => {
  const createIndustryKnowledgeBase = vi.fn().mockResolvedValue({ id: 'industry-1', name: '保险' })
  createIndustryKnowledgeBase.mockResolvedValue({ id: 'industry-1', name: '保险' })
  const wrapper = mountPage()

  await wrapper.get('[data-test="create-industry"]').trigger('click')
  await wrapper.get('[data-test="industry-name-input"]').setValue('保险')
  await wrapper.get('[data-test="industry-submit"]').trigger('click')

  expect(createIndustryKnowledgeBase).toHaveBeenCalledWith(expect.objectContaining({ name: '保险' }))
})

// 上传提示：验证行业库页面明确告知同名文件会覆盖。
it('shows overwrite warning for file upload', () => {
  const wrapper = mountPage()

  expect(wrapper.text()).toContain('同名文件会覆盖')
})
```

Follow the existing `mountPage()` pattern from `web/src/pages/platform/AssistantVersionsPage.spec.ts`: stub Naive UI components locally, call `mount(IndustryKnowledgePage, { global: { stubs: { ... } } })`, and keep the Chinese comments.

At the top of `IndustryKnowledgePage.spec.ts`, define the hook mocks used by these tests:

```ts
const createIndustryKnowledgeBase = vi.hoisted(() => vi.fn())

vi.mock('@/api/hooks/useIndustryKnowledge', () => ({
  useIndustryKnowledgeBasesQuery: () => ({
    data: ref([{ id: 'industry-1', name: '保险', document_count: 0, created_at: '', updated_at: '' }]),
    isLoading: ref(false),
    error: ref(null),
  }),
  useCreateIndustryKnowledgeBase: () => ({ mutateAsync: createIndustryKnowledgeBase }),
  useRenameIndustryKnowledgeBase: () => ({ mutateAsync: vi.fn() }),
  useDeleteIndustryKnowledgeBase: () => ({ mutateAsync: vi.fn() }),
  useIndustryKnowledgeFilesQuery: () => ({
    data: ref({ items: [], total: 0, used_bytes: 0, quota_bytes: 0, remaining_bytes: 0 }),
    isLoading: ref(false),
    error: ref(null),
  }),
  useUploadIndustryKnowledgeFile: () => ({ mutateAsync: vi.fn() }),
  useDeleteIndustryKnowledgeFile: () => ({ mutateAsync: vi.fn() }),
  useReparseIndustryKnowledgeFile: () => ({ mutateAsync: vi.fn() }),
}))
```

- [ ] **Step 3: Implement `IndustryKnowledgePage.vue`**

Create a platform page with:

- Left/top library list.
- Create/rename/delete actions.
- File upload area using existing `knowledgeUploadBatch` helpers.
- File table columns matching org/app knowledge pages.
- No quota summary.
- Visible text `同名文件会覆盖` near upload controls.

Add stable test selectors:

```vue
<n-button data-test="create-industry" type="primary" @click="openCreate">新增行业库</n-button>
<n-input data-test="industry-name-input" v-model:value="form.name" />
<n-button data-test="industry-submit" type="primary" @click="submitIndustry">保存</n-button>
```

- [ ] **Step 4: Add router and menu**

In `web/src/app/router.ts`, import the page and add:

```ts
import IndustryKnowledgePage from '@/pages/platform/IndustryKnowledgePage.vue'
```

Under platform-only routes:

```ts
{ path: 'platform/industry-knowledge', component: IndustryKnowledgePage, meta: { allowedRoles: PLATFORM_ONLY } },
```

In `web/src/layouts/DashboardLayout.vue`, add a platform menu item near platform skills:

```ts
items.push({ key: '/platform/industry-knowledge', label: '行业知识库', icon: () => h(BookOpen, { size: 18 }) })
```

Import `BookOpen` from `lucide-vue-next` when `DashboardLayout.vue` does not already import it.

- [ ] **Step 5: Run frontend tests**

Run:

```bash
rtk npm --prefix web test -- IndustryKnowledgePage
rtk npm --prefix web run typecheck
```

Expected: PASS.

- [ ] **Step 6: Commit frontend industry page**

Run:

```bash
rtk git add web/src/api/hooks/useIndustryKnowledge.ts web/src/pages/platform/IndustryKnowledgePage.vue web/src/pages/platform/IndustryKnowledgePage.spec.ts web/src/app/router.ts web/src/layouts/DashboardLayout.vue
rtk git commit -m "feat(web): 增加行业知识库管理页" -m "平台管理员可创建行业库、管理文件，并在上传区看到同名覆盖提示。"
```

---

### Task 7: Assistant Version UI Selector

**Files:**
- Modify: `web/src/pages/platform/AssistantVersionsPage.vue`
- Modify: `web/src/pages/platform/AssistantVersionsPage.spec.ts`
- Modify: `web/src/api/hooks/useAssistantVersions.ts`
- Modify: `web/src/api/hooks/useIndustryKnowledge.ts`

- [ ] **Step 1: Write UI test**

In `web/src/pages/platform/AssistantVersionsPage.spec.ts`, add:

```ts
// 编辑版本：展示行业知识库多选和上下文膨胀提示，保存时提交行业库 ID。
it('edits industry knowledge bases and shows context warning', async () => {
  updateVersion.mockResolvedValue({ ...sampleVersion, industry_knowledge_bases: [{ id: 'industry-1', name: '保险' }] })
  const wrapper = mountPage()

  await wrapper.findAll('button').find(b => b.text() === '编辑')!.trigger('click')
  await nextTick()
  expect(wrapper.text()).toContain('每个行业知识库都会单独召回最多 top_k 条结果')
  const selects = wrapper.findAll('select')
  await selects.at(-1)!.setValue('industry-1')
  await wrapper.find('form').trigger('submit')

  expect(updateVersion.mock.calls.at(-1)?.[0].payload.industry_knowledge_base_ids).toEqual(['industry-1'])
})
```

At the top of `AssistantVersionsPage.spec.ts`, add a mock for the industry options hook:

```ts
vi.mock('@/api/hooks/useIndustryKnowledge', () => ({
  useIndustryKnowledgeBaseOptionsQuery: () => ({
    data: ref([{ id: 'industry-1', name: '保险', document_count: 0, created_at: '', updated_at: '' }]),
    isLoading: ref(false),
    isError: ref(false),
  }),
}))
```

Extend the local `NSelect`, `n-select`, and `Select` stubs in `mountPage()` so a `multiple` select emits `string[]`:

```ts
props: { value: [String, Array], options: Array, disabled: Boolean, multiple: Boolean },
emits: ['update:value'],
setup(p, { emit }) {
  return () => h('select', {
    disabled: p.disabled,
    value: Array.isArray(p.value) ? p.value[0] : p.value,
    onChange: (e: Event) => {
      const value = (e.target as HTMLSelectElement).value
      emit('update:value', p.multiple ? [value] : value)
    },
  }, ((p.options ?? []) as Array<{ label: string; value: string }>).map(o =>
    h('option', { value: o.value }, o.label)))
}
```

- [ ] **Step 2: Run UI test and verify failure**

Run:

```bash
rtk npm --prefix web test -- AssistantVersionsPage
```

Expected: fail because selector is not implemented.

- [ ] **Step 3: Add query support**

In `useIndustryKnowledge.ts`, export a lightweight query for select options:

```ts
export function useIndustryKnowledgeBaseOptionsQuery(enabled?: () => boolean) {
  return useIndustryKnowledgeBasesQuery(enabled)
}
```

- [ ] **Step 4: Extend assistant version form state**

In `AssistantVersionsPage.vue`, add to `form`:

```ts
industry_knowledge_base_ids: [] as string[],
```

In `resetForm`:

```ts
form.industry_knowledge_base_ids = []
```

In `openEdit`:

```ts
form.industry_knowledge_base_ids = (version.industry_knowledge_bases ?? []).map(item => item.id)
```

In `buildPayload`:

```ts
industry_knowledge_base_ids: [...form.industry_knowledge_base_ids],
```

- [ ] **Step 5: Add selector UI**

Inside the assistant-version form, near Skill list or before it:

```vue
<n-grid-item :span="2">
  <n-form-item label="行业知识库">
    <div style="display: grid; gap: 8px; width: 100%">
      <n-select
        v-model:value="form.industry_knowledge_base_ids"
        multiple
        filterable
        clearable
        :loading="industryKnowledgeQuery.isLoading.value"
        :options="industryKnowledgeOptions"
        placeholder="选择该版本要额外检索的行业知识库"
      />
      <p class="state-text">
        每个行业知识库都会单独召回最多 top_k 条结果。选择过多会显著增加上下文长度和响应成本，请只选择该版本确实需要的行业库。
      </p>
      <p class="state-text">已选择 {{ form.industry_knowledge_base_ids.length }} 个行业知识库</p>
    </div>
  </n-form-item>
</n-grid-item>
```

In script:

```ts
const industryKnowledgeQuery = useIndustryKnowledgeBaseOptionsQuery(() => formVisible.value)
const industryKnowledgeOptions = computed(() => (industryKnowledgeQuery.data.value ?? []).map(item => ({
  label: item.name,
  value: item.id,
})))
```

- [ ] **Step 6: Run frontend tests**

Run:

```bash
rtk npm --prefix web test -- AssistantVersionsPage
rtk npm --prefix web run typecheck
```

Expected: PASS.

- [ ] **Step 7: Commit selector**

Run:

```bash
rtk git add web/src/pages/platform/AssistantVersionsPage.vue web/src/pages/platform/AssistantVersionsPage.spec.ts web/src/api/hooks/useAssistantVersions.ts web/src/api/hooks/useIndustryKnowledge.ts
rtk git commit -m "feat(web): 助手版本可选择行业知识库" -m "在版本编辑页增加行业库多选和上下文膨胀提示，保存后立即影响运行时检索。"
```

---

### Task 8: OpenAPI, Runtime Copy, And Docs

**Files:**
- Modify/generated: `openapi/openapi.yaml`
- Modify/generated: `web/src/api/generated.ts`
- Modify: `runtime/hermes/hermes-v2026.5.16/renderer/render_skills.py`
- Modify: `runtime/hermes/hermes-v2026.5.16/renderer/render_soul_md.py`
- Modify tests: `runtime/hermes/hermes-v2026.5.16/tests/test_render_skills.py`
- Modify tests: `runtime/hermes/hermes-v2026.5.16/tests/test_render_soul_md.py`
- Modify: `docs/knowledge-base.md`
- Modify: `docs/user-manual.md`
- Modify: `docs/technical-design.md`

- [ ] **Step 1: Add runtime copy tests**

In `runtime/hermes/hermes-v2026.5.16/tests/test_render_skills.py`, update the existing oc-kb skill assertion:

```python
def test_render_runtime_knowledge_skill_mentions_industry_scope(tmp_input: Path, tmp_data: Path) -> None:
    # manifest.knowledge 存在时，oc-kb skill 说明必须提到行业知识库可能参与检索，避免模型忽略 industry 来源。
    outputs = render(_manifest(knowledge=True), tmp_input, tmp_data)

    body = (tmp_data / "skills" / "oc-kb" / "SKILL.md").read_text()
    assert "industry knowledge" in body
    assert "scope" in body
    assert "skills/oc-kb/SKILL.md" in outputs
```

In `test_render_soul_md.py`:

```python
def test_knowledge_guide_mentions_industry_results_when_configured(tmp_input: Path, tmp_data: Path) -> None:
    # SOUL.md 的知识库指引需要告知结果可能包含行业知识库，帮助模型引用来源。
    render(_manifest_with_knowledge(), tmp_input, tmp_data)

    soul = (tmp_data / "SOUL.md").read_text()
    assert "行业知识库" in soul
```

- [ ] **Step 2: Run runtime tests and verify failure**

Run:

```bash
rtk pytest runtime/hermes/hermes-v2026.5.16/tests/test_render_skills.py runtime/hermes/hermes-v2026.5.16/tests/test_render_soul_md.py -q
```

Expected: fail because current copy does not mention industry knowledge.

- [ ] **Step 3: Update runtime copy**

In `render_skills.py`, update oc-kb generated skill content so the search bullet says:

```text
`oc-kb search "<question>" --top-k 8` searches the current app knowledge base, the organization knowledge base, and any industry knowledge bases selected by the app's assistant version. App results are returned first, organization results second, and each industry knowledge base can contribute up to top-k results with scope="industry".
```

In `render_soul_md.py`, add Chinese guidance:

```text
检索结果可能包含 scope=industry 的行业知识库命中。行业知识库是助手版本选择的通用行业资料，引用时需要区分它与实例知识库、企业知识库的来源。
```

- [ ] **Step 4: Regenerate OpenAPI and frontend types**

Run:

```bash
rtk make openapi-gen
rtk make web-types-gen
```

Expected: `openapi/openapi.yaml` and `web/src/api/generated.ts` are updated.

- [ ] **Step 5: Update docs**

In `docs/knowledge-base.md`, add a third scope row:

```markdown
| 行业知识库（`industry`）| 平台 | 助手版本选择的通用行业资料 | `/platform/industry-knowledge` |
```

Update Hermes search section:

```markdown
manager 用 runtime token 解析当前 app 与所属 org，再读取 app 绑定助手版本选择的行业库。检索结果顺序为 app → org → industry；每个行业库单独返回最多 top_k 条。
```

In `docs/user-manual.md`, add platform administrator steps for industry knowledge page and assistant-version selection.

In `docs/technical-design.md`, update `KnowledgeService` and permission sections with `industry` scope and `CanManageIndustryKnowledge`.

- [ ] **Step 6: Run docs/runtime/generated checks**

Run:

```bash
rtk pytest runtime/hermes/hermes-v2026.5.16/tests/test_render_skills.py runtime/hermes/hermes-v2026.5.16/tests/test_render_soul_md.py -q
rtk make openapi-check
```

Expected: PASS.

- [ ] **Step 7: Commit generated artifacts and docs**

Run:

```bash
rtk git add openapi/openapi.yaml web/src/api/generated.ts runtime/hermes/hermes-v2026.5.16 docs/knowledge-base.md docs/user-manual.md docs/technical-design.md
rtk git commit -m "docs(knowledge): 同步行业知识库契约和说明" -m "更新 OpenAPI、前端类型、Hermes 知识库指引和用户文档，说明 industry scope 检索行为。"
```

---

### Task 9: End-To-End Verification

**Files:**
- Source changes happen only when a verification command exposes a defect; apply the fix in the smallest file touched by that defect.

- [ ] **Step 1: Run backend tests**

Run:

```bash
rtk go test ./internal/... ./cmd/... -count=1
```

Expected: PASS.

- [ ] **Step 2: Run frontend tests and build checks**

Run:

```bash
rtk npm --prefix web test
rtk npm --prefix web run typecheck
rtk npm --prefix web run build
```

Expected: PASS.

- [ ] **Step 3: Start local dev server**

Run:

```bash
rtk npm --prefix web run dev -- --host 0.0.0.0
```

Expected: Vite prints a local URL, typically `http://localhost:5173/`. Keep the session running for browser verification.

- [ ] **Step 4: Browser verification**

Use the real browser:

1. Log in as platform admin using the local account from `AGENTS.md`.
2. Open `/platform/industry-knowledge`.
3. Create industry library `保险`.
4. Upload a small `.md` file and confirm the page shows queued/running/completed parse status.
5. Upload another file with the same filename and confirm the UI warning is visible and the list shows one effective file with the new upload result.
6. Open `/assistant-versions`, edit a version, select `保险`, confirm the warning text is visible, save, and confirm no “需重启” UI appears because this association does not bump revision.

- [ ] **Step 5: External upload API verification**

With local manager API running and `industry_knowledge.upload_token` configured:

```bash
rtk curl -i \
  -H "X-OC-Industry-Knowledge-Token: $INDUSTRY_KNOWLEDGE_UPLOAD_TOKEN" \
  -F "industry_name=保险" \
  -F "file=@/tmp/policy.md" \
  http://ocm.localhost/api/v1/external/industry-knowledge/files
```

Expected: HTTP `202 Accepted` with a document JSON body.

Run the same request without the header:

```bash
rtk curl -i \
  -F "industry_name=保险" \
  -F "file=@/tmp/policy.md" \
  http://ocm.localhost/api/v1/external/industry-knowledge/files
```

Expected: HTTP `401` with `INDUSTRY_KNOWLEDGE_TOKEN_INVALID`.

- [ ] **Step 6: Runtime search verification**

Use a running app bound to the edited assistant version. From the Hermes container or runtime shell, run:

```bash
oc-kb search "保险理赔规则" --top-k 3
```

Expected: JSON result contains existing `app`/`org` results when present and `scope: "industry"` hits with `industry_knowledge_base_name`.

- [ ] **Step 7: Final status check**

Run:

```bash
rtk git status --short
```

Expected: only intentional implementation files are modified or the tree is clean after final commits. No unrelated user changes are staged.

---

## Self-Review Checklist

- Spec coverage: Tasks cover schema, external upload token, platform CRUD, file overwrite, assistant-version association, no revision bump, per-industry `top_k`, frontend warning, generated OpenAPI/types, docs, and browser verification.
- Placeholder scan: No task uses `TBD`, `TODO`, “similar to”, or unspecified “add validation”.
- Type consistency: `industry_knowledge_base_ids`, `industry_knowledge_bases`, `IndustryKnowledgeBaseRef`, and `scope='industry'` are named consistently across backend, frontend, and docs.
