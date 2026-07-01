# 企业级「个人知识库空间」默认配额 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在企业配置中新增一个「个人知识库空间 (GB)」字段，作为该企业**新建实例**（成员个人知识库）的默认知识库配额，替代当前写死的 1GB。

**Architecture:** 复用现有 `organizations.knowledge_quota_bytes`（企业知识库）的字节存储 + `CHECK (> 0)` + GB↔Bytes 前端换算模式，新增平行字段 `organizations.default_app_knowledge_quota_bytes`。实例创建（onboarding）时读取所属企业该默认值写入新实例 `apps.knowledge_quota_bytes`；已有实例、实例单独配额编辑入口完全不动。后端校验沿用 `normalizeKnowledgeQuotaBytes`（nil→1GB 默认、>0 校验），"必填"由前端表单把关。

**Tech Stack:** Go + sqlc + golang-migrate（MySQL 8）、Gin handler + swag/OpenAPI、Vue 3 + Naive UI + vue-i18n。

**设计决策记录（相对 spec 的收敛，已与用户确认）：**
- 后端**不**在 service 层强制 non-nil：复用 `normalizeKnowledgeQuotaBytes`，传了校验 > 0、没传回落 1GB。避免破坏现有一批未传该字段的 `CreateOrganization` 单测，并与相邻「企业知识库空间」字段行为对称。
- "不允许留空"由前端 `n-input-number`（`:min="1"` + 非 clearable）保证，用户无法提交空值。

参考 spec：`docs/superpowers/specs/2026-07-01-org-default-instance-kb-quota-design.md`

---

## 文件结构

| 文件 | 责任 | 动作 |
|------|------|------|
| `internal/migrations/000026_org_default_app_kb_quota.up.sql` | organizations 加列 + CHECK 约束 | 创建 |
| `internal/migrations/000026_org_default_app_kb_quota.down.sql` | 回滚（删约束 + 列） | 创建 |
| `internal/store/queries/organizations.sql` | Create/Update 带上新列 | 修改 |
| `internal/store/queries/apps.sql` | `CreateApp` INSERT 增加 `knowledge_quota_bytes` 列 | 修改 |
| `internal/store/sqlc/*.go` | sqlc 生成产物 | 重新生成 |
| `internal/service/organization_service.go` | Input/Result 字段 + create/update 透传 | 修改 |
| `internal/service/onboarding_service.go` | 两处 `CreateApp` 传入企业默认配额 | 修改 |
| `internal/service/organization_service_test.go` | 新字段 create/update 单测 | 修改 |
| `internal/service/onboarding_service_test.go` | 新实例继承企业默认配额单测 + stub 捕获 | 修改 |
| `internal/api/handlers/dto.go` | 两个组织请求体加字段 | 修改 |
| `internal/api/handlers/organizations.go` | 两个 `to*Input` 透传字段 | 修改 |
| `openapi/openapi.yaml` / `web/src/api/generated.ts` | 生成产物 | 重新生成 |
| `web/src/api/hooks/useOrganizations.ts` | 两个 payload 加字段 | 修改 |
| `web/src/pages/platform/OrganizationsPage.vue` | 表单字段 + 换算 + 说明文案 | 修改 |
| `web/src/i18n/locales/{zh,en}/platform.ts` | 标签 + 说明文案 | 修改 |

---

## Task 1: 数据库 migration

**Files:**
- Create: `internal/migrations/000026_org_default_app_kb_quota.up.sql`
- Create: `internal/migrations/000026_org_default_app_kb_quota.down.sql`

- [ ] **Step 1: 写 up migration**

创建 `internal/migrations/000026_org_default_app_kb_quota.up.sql`，完全对齐 000005 的字节存储 + CHECK 模式，并带中文 COMMENT（项目规范要求新增列带 COMMENT）：

```sql
-- 企业级「个人知识库空间」默认配额：作为该企业新建实例（成员个人知识库）的默认知识库容量上限。
-- 默认 1GB，与实例创建历史默认值一致，存量企业行为不变；不影响已有实例，实例仍可单独调整。
ALTER TABLE organizations
    ADD COLUMN default_app_knowledge_quota_bytes BIGINT NOT NULL DEFAULT 1073741824
        COMMENT '该企业新建实例的默认个人知识库空间上限（字节），默认 1GB',
    ADD CONSTRAINT organizations_default_app_kb_quota_check
        CHECK (default_app_knowledge_quota_bytes > 0);
```

- [ ] **Step 2: 写 down migration**

创建 `internal/migrations/000026_org_default_app_kb_quota.down.sql`，对齐 000005 down 的"先删约束再删列"：

```sql
-- 回滚企业级个人知识库默认配额：先删 CHECK 约束再删列（MySQL 8 支持 DROP CONSTRAINT）。
ALTER TABLE organizations
    DROP CONSTRAINT organizations_default_app_kb_quota_check,
    DROP COLUMN default_app_knowledge_quota_bytes;
```

- [ ] **Step 3: 校验 migration 文件命名与序号**

Run: `ls internal/migrations/ | grep 000026`
Expected: 输出两个文件 `000026_org_default_app_kb_quota.up.sql` 与 `000026_org_default_app_kb_quota.down.sql`。当前最大序号为 `000025_apps_web_publish_applied`，故 000026 为下一个可用序号，无冲突。

- [ ] **Step 4: Commit**

```bash
git add internal/migrations/000026_org_default_app_kb_quota.up.sql internal/migrations/000026_org_default_app_kb_quota.down.sql
git commit -m "feat(db): 企业新增个人知识库默认配额列

organizations 表新增 default_app_knowledge_quota_bytes（默认 1GB，
CHECK > 0），作为该企业新建实例的默认知识库容量上限。存量企业默认
1GB，与实例历史默认值一致，行为不变。"
```

---

## Task 2: sqlc 查询与生成代码

**Files:**
- Modify: `internal/store/queries/organizations.sql:1-18`（CreateOrganization）、`:51-63`（UpdateOrganizationProfile）
- Modify: `internal/store/queries/apps.sql:4-18`（CreateApp）
- 重新生成: `internal/store/sqlc/models.go`、`internal/store/sqlc/organizations.sql.go`、`internal/store/sqlc/apps.sql.go`

- [ ] **Step 1: 改 CreateOrganization query**

编辑 `internal/store/queries/organizations.sql`，`CreateOrganization`（第 1-18 行）在 `knowledge_quota_bytes` 之后加入新列，沿用相同的 `COALESCE(NULLIF(...),1073741824)` 兜底（service 已保证传入 > 0，这里只是与既有列风格一致）。改后完整 query：

```sql
-- name: CreateOrganization :exec
INSERT INTO organizations (
    id,
    name,
    code,
    status,
    contact_name,
    contact_phone,
    remark,
    credit_warning_threshold,
    max_instance_count,
    knowledge_quota_bytes,
    default_app_knowledge_quota_bytes,
    assistant_version_ids
) VALUES (
    ?, ?, ?, ?, ?, ?, ?, ?, ?,
    COALESCE(NULLIF(CAST(sqlc.arg(knowledge_quota_bytes) AS SIGNED), 0), 1073741824),
    COALESCE(NULLIF(CAST(sqlc.arg(default_app_knowledge_quota_bytes) AS SIGNED), 0), 1073741824),
    ?
);
```

- [ ] **Step 2: 改 UpdateOrganizationProfile query**

编辑同文件 `UpdateOrganizationProfile`（第 51-63 行），在 `knowledge_quota_bytes` 行之后加入新列，沿用"未提交（0）时保留旧值"的 `COALESCE(NULLIF(...), 旧列)` 模式。改后完整 query：

```sql
-- name: UpdateOrganizationProfile :exec
UPDATE organizations
SET
    name = ?,
    contact_name = ?,
    contact_phone = ?,
    remark = ?,
    credit_warning_threshold = ?,
    max_instance_count = ?,
    knowledge_quota_bytes = COALESCE(NULLIF(CAST(sqlc.arg(knowledge_quota_bytes) AS SIGNED), 0), knowledge_quota_bytes),
    default_app_knowledge_quota_bytes = COALESCE(NULLIF(CAST(sqlc.arg(default_app_knowledge_quota_bytes) AS SIGNED), 0), default_app_knowledge_quota_bytes),
    assistant_version_ids = ?,
    updated_at = now()
WHERE id = ?;
```

- [ ] **Step 3: 改 CreateApp query 增加 knowledge_quota_bytes 列**

编辑 `internal/store/queries/apps.sql`，`CreateApp`（第 4-17 行）在列清单末尾加入 `knowledge_quota_bytes`，由参数显式传入（不再依赖 DB 默认）。改后完整 query：

```sql
-- name: CreateApp :exec
-- k8s 模型下 app 对应 Deployment，pod 落点由调度器决定，不再写 runtime_node_id。
-- locale 在创建时快照 owner 的用户语言偏好（NULL=平台回退默认）。
-- knowledge_quota_bytes 由 service 传入所属企业的默认配额，替代 DB 默认 1GB。
INSERT INTO apps (
    id,
    org_id,
    owner_user_id,
    name,
    description,
    status,
    api_key_status,
    version_id,
    locale,
    knowledge_quota_bytes
) VALUES (
    ?, ?, ?, ?, ?, ?, ?, ?, ?, ?
);
```

- [ ] **Step 4: 重新生成 sqlc 代码**

Run: `make sqlc-generate`
Expected: 命令成功；`git status` 显示 `internal/store/sqlc/models.go`（`Organization` 结构体新增 `DefaultAppKnowledgeQuotaBytes int64`）、`organizations.sql.go`（`CreateOrganizationParams`/`UpdateOrganizationProfileParams` 新增字段）、`apps.sql.go`（`CreateAppParams` 新增 `KnowledgeQuotaBytes int64`）被更新。

- [ ] **Step 5: 校验编译**

此时 `internal/service` 尚未更新会编译失败是预期的；仅校验 sqlc 包本身可编译：
Run: `go build ./internal/store/...`
Expected: 成功，无报错。

- [ ] **Step 6: Commit**

```bash
git add internal/store/queries/organizations.sql internal/store/queries/apps.sql internal/store/sqlc/
git commit -m "feat(store): 组织/实例查询接入个人知识库默认配额

CreateOrganization / UpdateOrganizationProfile 带上
default_app_knowledge_quota_bytes；CreateApp 增加 knowledge_quota_bytes
入参以承接企业默认值。重新生成 sqlc 代码。"
```

---

## Task 3: Service 层（组织 Input/Result 与 create/update）

**Files:**
- Modify: `internal/service/organization_service.go`（`OrganizationInput` ~152、`OrganizationResult` ~188、`CreateOrganization` ~234/241、`UpdateOrganization` ~548/574、`toOrganizationResultWithAdminUsername` ~706）
- Test: `internal/service/organization_service_test.go`

- [ ] **Step 1: 写失败测试**

在 `internal/service/organization_service_test.go` 末尾追加三个测试（紧邻现有 `TestCreateOrganization_PersistsKnowledgeQuotaBytes` 的风格）：

```go
// TestCreateOrganization_PersistsDefaultAppKnowledgeQuota 验证创建企业时"个人知识库默认配额"写入 CreateOrganizationParams。
// 覆盖正常路径：平台管理员显式传入正整数默认配额，service 应原样写库。
func TestCreateOrganization_PersistsDefaultAppKnowledgeQuota(t *testing.T) {
	store := &organizationStoreStub{}
	prov := &fakeProvisioner{user: newapi.User{ID: 42}, accessToken: "access-tok-xyz"}
	svc := NewOrganizationService(store, prov, mustCipher(t), nil)
	svc.SetVersionValidator(fakeVersionValidator{known: map[string]bool{}})
	svc.hashPassword = fakeHash
	quota := int64(5 * 1024 * 1024 * 1024) // 5GB，区别于 1GB 默认以确认确实来自入参

	_, err := svc.CreateOrganization(context.Background(), auth.Principal{Role: domain.UserRolePlatformAdmin}, OrganizationInput{
		Name:                         "测试组织",
		Code:                         "test-org",
		DefaultAppKnowledgeQuotaBytes: &quota,
		AdminUsername:                "org-admin",
		AdminDisplayName:             "企业管理员",
		AdminPassword:                "secret-password",
	})
	require.NoError(t, err)
	assert.Equal(t, quota, store.created.DefaultAppKnowledgeQuotaBytes)
}

// TestCreateOrganization_DefaultsAppKnowledgeQuota 验证创建企业未传"个人知识库默认配额"时回落 1GB 默认。
// 覆盖边界：nil 入参走 normalizeKnowledgeQuotaBytes，写入 KnowledgeQuotaDefaultBytes。
func TestCreateOrganization_DefaultsAppKnowledgeQuota(t *testing.T) {
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
	assert.Equal(t, KnowledgeQuotaDefaultBytes, store.created.DefaultAppKnowledgeQuotaBytes)
}

// TestCreateOrganization_RejectsNonPositiveDefaultAppKnowledgeQuota 验证显式传入非正数默认配额时返回参数错误。
// 覆盖异常路径：0 值经 normalizeKnowledgeQuotaBytes 校验应被拒绝。
func TestCreateOrganization_RejectsNonPositiveDefaultAppKnowledgeQuota(t *testing.T) {
	store := &organizationStoreStub{}
	prov := &fakeProvisioner{user: newapi.User{ID: 42}, accessToken: "access-tok-xyz"}
	svc := NewOrganizationService(store, prov, mustCipher(t), nil)
	svc.SetVersionValidator(fakeVersionValidator{known: map[string]bool{}})
	svc.hashPassword = fakeHash
	invalid := int64(0)

	_, err := svc.CreateOrganization(context.Background(), auth.Principal{Role: domain.UserRolePlatformAdmin}, OrganizationInput{
		Name:                         "测试组织",
		Code:                         "test-org",
		DefaultAppKnowledgeQuotaBytes: &invalid,
		AdminUsername:                "org-admin",
		AdminDisplayName:             "企业管理员",
		AdminPassword:                "secret-password",
	})
	require.ErrorIs(t, err, ErrMemberCreateInvalid)
}

// TestUpdateOrganization_PreservesDefaultAppKnowledgeQuotaWhenOmitted 验证编辑企业未传默认配额时保留原值。
// 覆盖边界：nil 入参不覆盖数据库既有 default_app_knowledge_quota_bytes。
func TestUpdateOrganization_PreservesDefaultAppKnowledgeQuotaWhenOmitted(t *testing.T) {
	store := &organizationStoreStub{}
	org := store.mustSeedOrganization(t, "test-org")
	store.org.DefaultAppKnowledgeQuotaBytes = 7 * 1024 * 1024 * 1024
	svc := NewOrganizationService(store, &fakeProvisioner{}, mustCipher(t), nil)
	svc.SetVersionValidator(fakeVersionValidator{known: map[string]bool{}})

	_, err := svc.UpdateOrganization(context.Background(), auth.Principal{Role: domain.UserRolePlatformAdmin}, org.ID, OrganizationInput{
		Name:                   store.org.Name,
		AssistantVersionIDsSet: false,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(7*1024*1024*1024), store.updatedProfile.DefaultAppKnowledgeQuotaBytes)
}
```

同时更新 stub 让新列在 create/update 往返可见：编辑 `organizationStoreStub.CreateOrganization`（~434 行 `created := sqlc.Organization{...}`）加入 `DefaultAppKnowledgeQuotaBytes: arg.DefaultAppKnowledgeQuotaBytes,`；编辑 `UpdateOrganizationProfile`（~492 行）加入 `s.org.DefaultAppKnowledgeQuotaBytes = arg.DefaultAppKnowledgeQuotaBytes`。

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/service/ -run 'DefaultAppKnowledgeQuota' 2>&1 | head -30`
Expected: 编译失败——`OrganizationInput` / `CreateOrganizationParams` 无 `DefaultAppKnowledgeQuotaBytes` 字段（字段将在 Step 3 添加；sqlc 侧已在 Task 2 生成）。

- [ ] **Step 3: 在 OrganizationInput / OrganizationResult 加字段**

编辑 `internal/service/organization_service.go`，在 `OrganizationInput` 的 `KnowledgeQuotaBytes *int64`（~153 行）之后加：

```go
	// DefaultAppKnowledgeQuotaBytes 是该企业新建实例的默认知识库容量上限，单位字节；
	// nil 表示创建时默认 1GB、更新时保留旧值（前端负责必填校验）。
	DefaultAppKnowledgeQuotaBytes *int64
```

在 `OrganizationResult` 的 `KnowledgeQuotaBytes int64`（~189 行）之后加：

```go
	// DefaultAppKnowledgeQuotaBytes 是该企业新建实例的默认知识库容量上限，单位字节。
	DefaultAppKnowledgeQuotaBytes int64 `json:"default_app_knowledge_quota_bytes"`
```

- [ ] **Step 4: CreateOrganization 透传字段**

在 `CreateOrganization` 中，`knowledgeQuotaBytes, err := normalizeKnowledgeQuotaBytes(input.KnowledgeQuotaBytes)`（~234 行）之后加一段归一：

```go
	// 个人知识库默认配额：与企业知识库同样走 normalize（nil→1GB、>0 校验），前端保证必填。
	defaultAppKnowledgeQuotaBytes, err := normalizeKnowledgeQuotaBytes(input.DefaultAppKnowledgeQuotaBytes)
	if err != nil {
		return OrganizationResult{}, err
	}
```

在 `sqlc.CreateOrganizationParams{...}`（~241 行）的 `KnowledgeQuotaBytes: knowledgeQuotaBytes,` 之后加：

```go
		DefaultAppKnowledgeQuotaBytes: defaultAppKnowledgeQuotaBytes,
```

- [ ] **Step 5: UpdateOrganization 透传字段**

在 `UpdateOrganization` 中，现有 `knowledgeQuotaBytes := current.KnowledgeQuotaBytes; if input.KnowledgeQuotaBytes != nil {...}`（~548-554 行）块之后加平行处理：

```go
	// 个人知识库默认配额：未提交时保留数据库原值；显式提交时只校验正数。
	defaultAppKnowledgeQuotaBytes := current.DefaultAppKnowledgeQuotaBytes
	if input.DefaultAppKnowledgeQuotaBytes != nil {
		if err := validateKnowledgeQuotaBytes(*input.DefaultAppKnowledgeQuotaBytes); err != nil {
			return OrganizationResult{}, err
		}
		defaultAppKnowledgeQuotaBytes = *input.DefaultAppKnowledgeQuotaBytes
	}
```

在 `sqlc.UpdateOrganizationProfileParams{...}`（~574 行）的 `KnowledgeQuotaBytes: knowledgeQuotaBytes,` 之后加：

```go
		DefaultAppKnowledgeQuotaBytes: defaultAppKnowledgeQuotaBytes,
```

- [ ] **Step 6: toOrganizationResult 回填字段**

编辑 `toOrganizationResultWithAdminUsername`（~706 行 `KnowledgeQuotaBytes: org.KnowledgeQuotaBytes,` 处）加：

```go
		DefaultAppKnowledgeQuotaBytes: org.DefaultAppKnowledgeQuotaBytes,
```

- [ ] **Step 7: 运行测试确认通过**

Run: `go test ./internal/service/ -run 'KnowledgeQuota' -v 2>&1 | tail -30`
Expected: 新增 4 个 `DefaultAppKnowledgeQuota` 用例与既有 `KnowledgeQuota` 用例全部 PASS。

- [ ] **Step 8: Commit**

```bash
git add internal/service/organization_service.go internal/service/organization_service_test.go
git commit -m "feat(service): 组织创建/更新支持个人知识库默认配额

OrganizationInput/Result 新增 DefaultAppKnowledgeQuotaBytes；创建走
normalizeKnowledgeQuotaBytes（nil→1GB、>0 校验），更新未传时保留原值。
补充 create/update/默认/非法值单测。"
```

---

## Task 4: 实例创建继承企业默认配额（onboarding）

**Files:**
- Modify: `internal/service/onboarding_service.go:175`（OnboardMember 的 CreateApp）、`:361`（CreateAppForMember 的 CreateApp）
- Test: `internal/service/onboarding_service_test.go`（stub 捕获 ~390、happy-path 断言）

- [ ] **Step 1: 写失败测试**

在 `internal/service/onboarding_service_test.go` 的 `onboardingStub` 结构体字段区（~302 `lastAppLocale` 附近）加一个捕获字段：

```go
	lastAppKnowledgeQuotaBytes int64 // 记录最近一次 CreateApp 使用的知识库配额，供断言校验继承企业默认值。
```

在 `onboardingStub.CreateApp`（~390 行）内、`s.lastAppLocale = arg.Locale` 之后加：

```go
	// 记录传入的知识库配额，验证新实例继承所属企业的默认配额。
	s.lastAppKnowledgeQuotaBytes = arg.KnowledgeQuotaBytes
```

在文件末尾追加两个测试（对应两条 CreateApp 路径）：

```go
// TestOnboardMember_InheritsOrgDefaultAppKnowledgeQuota 验证新成员实例继承所属企业的个人知识库默认配额。
// 覆盖正常路径：企业默认配额非 1GB 时，新建实例的 knowledge_quota_bytes 应等于企业设置值而非 DB 默认。
func TestOnboardMember_InheritsOrgDefaultAppKnowledgeQuota(t *testing.T) {
	store := newOnboardingStub(t)
	store.org.DefaultAppKnowledgeQuotaBytes = 8 * 1024 * 1024 * 1024 // 8GB，区别于 1GB 默认
	svc := newOnboardingServiceForTest(t, store)

	_, err := svc.OnboardMember(context.Background(), orgManager(), testOrgID, OnboardMemberInput{
		Username:    "bob",
		DisplayName: "Bob",
		Password:    "secret-password",
		AppName:     "bob-app",
		VersionID:   testVersionID,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(8*1024*1024*1024), store.lastAppKnowledgeQuotaBytes)
}

// TestCreateAppForMember_InheritsOrgDefaultAppKnowledgeQuota 验证为已有成员补建实例时同样继承企业默认配额。
// 覆盖正常路径：CreateAppForMember 路径下 knowledge_quota_bytes 亦来自企业设置值。
func TestCreateAppForMember_InheritsOrgDefaultAppKnowledgeQuota(t *testing.T) {
	store := newOnboardingStub(t)
	store.org.DefaultAppKnowledgeQuotaBytes = 8 * 1024 * 1024 * 1024 // 8GB
	svc := newOnboardingServiceForTest(t, store)

	_, err := svc.CreateAppForMember(context.Background(), platformAdmin(), testOrgID, store.user.ID, CreateAppForMemberInput{
		AppName:   "bob-app",
		VersionID: testVersionID,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(8*1024*1024*1024), store.lastAppKnowledgeQuotaBytes)
}
```

> 注：`orgManager()`、`platformAdmin()`、`newOnboardingServiceForTest(t, store)`、`OnboardMemberInput`、`CreateAppForMemberInput` 的确切构造/签名以文件现有同类测试（如 `TestOnboardMemberCommitsOnSuccess`、`TestCreateAppForMember_PlatformAdminCreatesAfterDelete`）为准；若 helper 名不同则照抄现有用例的构造方式，仅追加 `store.org.DefaultAppKnowledgeQuotaBytes = ...` 与末尾 `assert.Equal(...lastAppKnowledgeQuotaBytes)`。

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/service/ -run 'InheritsOrgDefaultAppKnowledgeQuota' 2>&1 | head -20`
Expected: FAIL——`lastAppKnowledgeQuotaBytes` 为 0（CreateApp 尚未传配额），断言 8GID 不等于 0。

- [ ] **Step 3: OnboardMember 路径传入企业默认配额**

编辑 `internal/service/onboarding_service.go`，`store.CreateApp(ctx, sqlc.CreateAppParams{...})`（~175 行）内 `Locale: null.StringFrom(memberLocale),` 之后加：

```go
				// 新实例知识库配额继承所属企业的默认配额（default_app_knowledge_quota_bytes）。
				KnowledgeQuotaBytes: org.DefaultAppKnowledgeQuotaBytes,
```

- [ ] **Step 4: CreateAppForMember 路径传入企业默认配额**

编辑同文件第二处 `store.CreateApp(ctx, sqlc.CreateAppParams{...})`（~361 行）内 `Locale: null.StringFrom(appLocale),` 之后加：

```go
				// 新实例知识库配额继承所属企业的默认配额（default_app_knowledge_quota_bytes）。
				KnowledgeQuotaBytes: org.DefaultAppKnowledgeQuotaBytes,
```

- [ ] **Step 5: 运行测试确认通过**

Run: `go test ./internal/service/ -run 'InheritsOrgDefaultAppKnowledgeQuota' -v 2>&1 | tail -15`
Expected: 两个用例 PASS。

- [ ] **Step 6: 跑 service 全量回归**

Run: `go test ./internal/service/ 2>&1 | tail -15`
Expected: 全部 PASS（含既有 onboarding/organization 用例，未因新字段回归）。

- [ ] **Step 7: Commit**

```bash
git add internal/service/onboarding_service.go internal/service/onboarding_service_test.go
git commit -m "feat(service): 新建实例继承企业个人知识库默认配额

OnboardMember 与 CreateAppForMember 两条创建路径均将新实例
knowledge_quota_bytes 设为所属企业 default_app_knowledge_quota_bytes，
替代 DB 默认 1GB。补充两条继承路径单测。"
```

---

## Task 5: DTO 与 handler 映射

**Files:**
- Modify: `internal/api/handlers/dto.go`（`CreateOrganizationRequest` ~71、`OrganizationRequest` ~97）
- Modify: `internal/api/handlers/organizations.go`（`toOrganizationInput` ~221、`toCreateOrganizationInput` ~238）

- [ ] **Step 1: dto.go 两个请求体加字段**

编辑 `internal/api/handlers/dto.go`，在 `CreateOrganizationRequest` 的 `KnowledgeQuotaBytes *int64 ...`（~71 行）之后加：

```go
	// DefaultAppKnowledgeQuotaBytes 是该企业新建实例的默认知识库容量上限，单位字节；nil 表示使用默认 1GB。
	DefaultAppKnowledgeQuotaBytes *int64 `json:"default_app_knowledge_quota_bytes"`
```

在 `OrganizationRequest` 的 `KnowledgeQuotaBytes *int64 ...`（~97 行）之后加：

```go
	// DefaultAppKnowledgeQuotaBytes 是该企业新建实例的默认知识库容量上限，单位字节；nil 表示保留旧值。
	DefaultAppKnowledgeQuotaBytes *int64 `json:"default_app_knowledge_quota_bytes"`
```

- [ ] **Step 2: organizations.go 两个转换函数透传**

编辑 `internal/api/handlers/organizations.go`，`toOrganizationInput`（~221 行 `KnowledgeQuotaBytes: req.KnowledgeQuotaBytes,` 处）加：

```go
		DefaultAppKnowledgeQuotaBytes: req.DefaultAppKnowledgeQuotaBytes,
```

`toCreateOrganizationInput`（~238 行同名字段处）加：

```go
		DefaultAppKnowledgeQuotaBytes: req.DefaultAppKnowledgeQuotaBytes,
```

- [ ] **Step 3: 编译校验**

Run: `go build ./... 2>&1 | head`
Expected: 成功，无报错。

- [ ] **Step 4: Commit**

```bash
git add internal/api/handlers/dto.go internal/api/handlers/organizations.go
git commit -m "feat(api): 组织请求体接入个人知识库默认配额字段

CreateOrganizationRequest / OrganizationRequest 新增
default_app_knowledge_quota_bytes，两个 to*Input 透传到 service。"
```

---

## Task 6: OpenAPI 与前端类型同步

**Files:**
- 重新生成: `openapi/openapi.yaml`、`web/src/api/generated.ts`

- [ ] **Step 1: 生成 OpenAPI**

Run: `make openapi-gen`
Expected: 成功；`openapi/openapi.yaml` 中 `CreateOrganizationRequest` / `OrganizationRequest` / `OrganizationResult`（swag 扫描 `service.OrganizationResult`）出现 `default_app_knowledge_quota_bytes` 字段。

- [ ] **Step 2: 生成前端类型**

Run: `make web-types-gen`
Expected: 成功；`web/src/api/generated.ts` 对应类型出现 `default_app_knowledge_quota_bytes`。

- [ ] **Step 3: openapi-check 工作区干净**

Run: `make openapi-check`
Expected: 通过（跑 openapi-gen 后 git 工作区对生成文件无未提交差异）。

- [ ] **Step 4: Commit**

```bash
git add openapi/openapi.yaml web/src/api/generated.ts
git commit -m "chore(openapi): 同步个人知识库默认配额字段

make openapi-gen + web-types-gen 重新生成，organization 请求/响应
类型补充 default_app_knowledge_quota_bytes。"
```

---

## Task 7: 前端表单字段、换算与说明文案

**Files:**
- Modify: `web/src/api/hooks/useOrganizations.ts`（`OrganizationFormPayload` ~36、`OrganizationUpdatePayload` ~123）
- Modify: `web/src/pages/platform/OrganizationsPage.vue`（模板 ~139-152、换算 ~344-372、openEditForm ~391-395、submitEdit ~434、createFormMutation ~511、useFormModal initial/toPayload ~528-549）
- Modify: `web/src/i18n/locales/zh/platform.ts`、`web/src/i18n/locales/en/platform.ts`

- [ ] **Step 1: useOrganizations.ts 两个 payload 加字段**

编辑 `web/src/api/hooks/useOrganizations.ts`，`OrganizationFormPayload` 的 `knowledge_quota_bytes?: number`（~36 行）之后加：

```ts
  // 该企业新建实例的默认知识库容量上限，单位字节；未传时后端回落 1GB。
  default_app_knowledge_quota_bytes?: number
```

`OrganizationUpdatePayload` 的 `knowledge_quota_bytes?: number`（~123 行）之后加同样一行。

- [ ] **Step 2: i18n 加标签与说明文案**

编辑 `web/src/i18n/locales/zh/platform.ts`，在 `labelKnowledgeQuota: '企业知识库空间 (GB)',`（~21 行）之后加：

```ts
      labelPersonalKnowledgeQuota: '个人知识库空间 (GB)',
      personalKnowledgeQuotaHint: '该企业新建实例的默认个人知识库空间上限。仅对之后新建的实例生效，不影响已有实例；平台管理员仍可在实例中单独调整。',
```

编辑 `web/src/i18n/locales/en/platform.ts`，在 `labelKnowledgeQuota: 'Knowledge quota (GB)',`（~21 行）之后加：

```ts
      labelPersonalKnowledgeQuota: 'Personal knowledge base quota (GB)',
      personalKnowledgeQuotaHint: 'Default personal knowledge base quota for new instances in this organization. Applies only to instances created afterward; existing instances are unaffected and can still be adjusted individually.',
```

- [ ] **Step 3: OrganizationsPage.vue 加换算与表单状态**

编辑 `web/src/pages/platform/OrganizationsPage.vue`：

(a) 在 `editQuotaBytesForPayload`（~345-353 行）之后新增一个平行函数（个人知识库默认配额的编辑保值逻辑）：

```ts
// editPersonalQuotaBytesForPayload 在编辑表单未改动个人知识库 GB 输入时保留后端原始 bytes，避免整 GB 展示导致非整 GB 容量被静默舍入。
function editPersonalQuotaBytesForPayload(): number {
  if (
    editForm.personal_knowledge_quota_gb === editForm.personal_knowledge_quota_original_gb
    && typeof editForm.personal_knowledge_quota_original_bytes === 'number'
  ) {
    return editForm.personal_knowledge_quota_original_bytes
  }
  return quotaGBToBytes(editForm.personal_knowledge_quota_gb)
}
```

(b) 在 `OrganizationCreateForm` 接口（~356-359 行）内 `knowledge_quota_gb: number` 之后加：

```ts
  // personal_knowledge_quota_gb 是个人知识库（实例默认）空间 GB 输入，提交前转换为 bytes。
  personal_knowledge_quota_gb: number
```

(c) 在 `editForm` reactive（~362-373 行）内 `knowledge_quota_original_bytes` 之后加三行：

```ts
  personal_knowledge_quota_gb: knowledgeQuotaGBDefault,
  personal_knowledge_quota_original_gb: knowledgeQuotaGBDefault,
  personal_knowledge_quota_original_bytes: undefined as number | undefined,
```

(d) 在 `openEditForm`（~391-396 行）内 `editForm.knowledge_quota_original_bytes = ...` 之后加：

```ts
  const personalQuotaGB = quotaBytesToGB(org.default_app_knowledge_quota_bytes)
  editForm.personal_knowledge_quota_gb = personalQuotaGB
  editForm.personal_knowledge_quota_original_gb = personalQuotaGB
  editForm.personal_knowledge_quota_original_bytes = typeof org.default_app_knowledge_quota_bytes === 'number'
    ? org.default_app_knowledge_quota_bytes : undefined
```

- [ ] **Step 4: OrganizationsPage.vue 提交链路带上新字段**

(a) `submitEditOrganization` 的 `updateMutation.mutateAsync` payload（~434 行 `knowledge_quota_bytes: editQuotaBytesForPayload(),` 处）之后加：

```ts
        default_app_knowledge_quota_bytes: editPersonalQuotaBytesForPayload(),
```

(b) `createFormMutation.mutateAsync`（~511 行 `knowledge_quota_bytes: payload.knowledge_quota_bytes,` 处）之后加：

```ts
    default_app_knowledge_quota_bytes: payload.default_app_knowledge_quota_bytes,
```

(c) `useFormModal` 的 `initial`（~528 行 `knowledge_quota_gb: knowledgeQuotaGBDefault,` 处）之后加：

```ts
    personal_knowledge_quota_gb: knowledgeQuotaGBDefault,
```

(d) `useFormModal` 的 `toPayload`（~544-545 行 `knowledge_quota_gb` / `knowledge_quota_bytes` 处）之后加：

```ts
    personal_knowledge_quota_gb: f.personal_knowledge_quota_gb,
    default_app_knowledge_quota_bytes: quotaGBToBytes(f.personal_knowledge_quota_gb),
```

- [ ] **Step 5: OrganizationsPage.vue 模板加输入框 + 说明文案**

在模板知识库配额 `n-grid-item`（~139-152 行）之后，新增一个平行 `n-grid-item`。用 `feedback` 展示说明文案（Naive UI `n-form-item` 的 `feedback` 插槽/属性在本项目其它表单是否使用需确认；若无先例，用字段下方 `<div class="form-hint">` + 现有样式）。这里采用稳妥的独立说明行：

```vue
          <n-grid-item>
            <n-form-item :label="t('platform.orgs.form.labelPersonalKnowledgeQuota')">
              <n-input-number
                v-if="modalMode === 'create'"
                v-model:value="form.personal_knowledge_quota_gb"
                :min="1" :precision="0" style="width: 100%"
              />
              <n-input-number
                v-else
                v-model:value="editForm.personal_knowledge_quota_gb"
                :min="1" :precision="0" style="width: 100%"
              />
              <template #feedback>
                {{ t('platform.orgs.form.personalKnowledgeQuotaHint') }}
              </template>
            </n-form-item>
          </n-grid-item>
```

> 说明：`n-input-number` 沿用知识库配额同款 `:min="1"`、无 `clearable`，天然阻止空值/非正数提交，即"不允许留空"。`#feedback` 是 Naive UI `n-form-item` 展示辅助/校验文案的标准插槽，用于承载说明文案。若渲染异常，退化为在 `n-form-item` 下方加 `<div style="font-size:12px;color:var(--n-feedback-text-color);margin-top:4px;">{{ t(...) }}</div>`。

- [ ] **Step 6: 前端类型检查与构建**

Run: `make web-typecheck 2>&1 | tail -20`
Expected: 通过，无 TS 报错（新字段在 `Organization`/payload 类型中已由 Task 6 生成）。

Run: `cd web && npm run build 2>&1 | tail -15`（或项目对应 `make web-build`）
Expected: 构建成功。

- [ ] **Step 7: Commit**

```bash
git add web/src/api/hooks/useOrganizations.ts web/src/pages/platform/OrganizationsPage.vue web/src/i18n/locales/zh/platform.ts web/src/i18n/locales/en/platform.ts
git commit -m "feat(web): 企业表单新增个人知识库空间字段与说明

编辑/创建企业页新增「个人知识库空间 (GB)」输入框（必填、最小 1GB），
GB↔Bytes 换算复用现有工具，字段下方展示说明文案（中英）。该值作为
企业新建实例的默认知识库配额。"
```

---

## Task 8: 迁移应用 + 真实浏览器全链路验证

**Files:** 无（验证任务）

- [ ] **Step 1: 应用 migration 到本地 k3d 库**

前置：本地 k3d 环境已 `make local-up`（若开机后 CrashLoop，先 `sudo modprobe br_netfilter` + rollout restart，见项目内存）。manager-api 启动会自动跑迁移；如需手动：
Run: `make migrate-up`
Expected: 000026 迁移成功，无报错。

验证列已存在（经 rtk 绕过，见项目约定）：
Run: `rtk proxy kubectl -n ocm exec deploy/manager-api -- sh -c "echo 'DESCRIBE organizations;' | mysql ..."`（连接串按本地约定），确认 `default_app_knowledge_quota_bytes` 列存在、默认 1073741824。

- [ ] **Step 2: 重新部署本地镜像**

Run: `make local-build && make local-preload`（按项目本地部署约定重建 manager 镜像与前端）
Expected: manager-api 与前端更新为含新字段的版本。

- [ ] **Step 3: 浏览器验证（平台管理员，http://ocm.localhost，admin/admin123）**

用 chrome-devtools 或手动浏览器，逐项确认：
- [ ] 「新建企业」表单出现「个人知识库空间 (GB)」输入框，默认值 1，下方可见中文说明文案；无法清空/提交非正数。
- [ ] 创建一个企业并设个人知识库空间为 3GB，保存成功。
- [ ] 「编辑企业」打开该企业，个人知识库空间回显 3GB；改为 5GB 保存，重新打开回显 5GB。
- [ ] 在该企业下新建一个成员/实例，进入该实例知识库页，确认其配额为 5GB（等于企业设置值，而非 1GB）。
- [ ] 找一个该企业**已存在**的实例，确认其配额未被改变（回归：只影响新建）。
- [ ] 实例知识库页的"单独调整配额"入口仍可用（未被移除/收窄）。
- [ ] 切换到英文，字段标签与说明文案显示英文。

- [ ] **Step 4: 记录验证结果**

在交付说明中给出逐项验证矩阵（截图或结论），任一不通过则回到对应 Task 修复后重验。

---

## Self-Review

**Spec coverage：**
- 数据库列（默认 1GB、CHECK>0、COMMENT）→ Task 1 ✓
- 实例创建继承企业默认（两条路径）→ Task 4 ✓
- service Input/Result/create/update → Task 3 ✓
- DTO/handler → Task 5 ✓
- 前端字段 + 换算 + 必填 + 默认 1GB + 说明文案（中英）→ Task 7 ✓
- OpenAPI/类型同步 → Task 6 ✓
- 测试（必填由前端；后端 create/默认/非法/update 保值 + 继承）→ Task 3、4 ✓
- 浏览器三态验证（新建继承、存量不变、单独入口仍在）→ Task 8 ✓
- 非目标（不动 apps CHECK、不动上传校验、不动实例编辑入口、不引入无限制、不建 users 级字段）→ 计划中均未触碰 ✓

**Placeholder scan：** 无 TBD/TODO；helper 名称不确定处（onboarding 测试 helper、Naive UI feedback 插槽）已给出"以现有用例为准 / 退化方案"的明确指引，非空泛占位。

**Type consistency：** 后端字段统一 `DefaultAppKnowledgeQuotaBytes`（Go）/ `default_app_knowledge_quota_bytes`（json/SQL/TS）；前端表单 GB 字段统一 `personal_knowledge_quota_gb`；换算函数复用既有 `quotaGBToBytes`/`quotaBytesToGB`，新增 `editPersonalQuotaBytesForPayload`。i18n key 统一 `labelPersonalKnowledgeQuota` / `personalKnowledgeQuotaHint`。前后命名一致。
