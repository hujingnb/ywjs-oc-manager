# 企业实例数量上限 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 平台管理员可在创建 / 编辑企业时设置「最多创建 n 个实例」上限，企业达到上限后无法再新建实例。

**Architecture:** 「实例」= `apps` 表中 `deleted_at IS NULL` 的应用。企业新增可空列 `max_instance_count`（NULL = 不限制）。校验放在两个建实例入口（`OnboardMember` / `CreateAppForMember`）的已有事务内，事务内 `COUNT(*)` 现存未删除实例数与上限比较；接受并发轻微越限的 race。超限返回新 sentinel `ErrInstanceLimitReached`，handler 映射为 409。

**Tech Stack:** Go + gin + sqlc（MySQL）+ golang-migrate；前端 Vue 3 + naive-ui + TanStack Query；OpenAPI 由 swag 注解生成、前端类型由 yaml 生成。

设计文档：`docs/superpowers/specs/2026-06-02-org-instance-limit-design.md`

---

### Task 1: 数据库 migration + sqlc query + 重新生成

**Files:**
- Create: `internal/migrations/000004_org_max_instance_count.up.sql`
- Create: `internal/migrations/000004_org_max_instance_count.down.sql`
- Modify: `sqlc.yaml`（schema 列表追加 000004）
- Modify: `internal/store/queries/organizations.sql`（Create / UpdateProfile 加列）
- Modify: `internal/store/queries/apps.sql`（新增 CountActiveAppsByOrg）
- 生成产物：`internal/store/sqlc/*`（由 `make sqlc-generate` 重写，勿手改）

- [ ] **Step 1: 写 up migration**

文件 `internal/migrations/000004_org_max_instance_count.up.sql`：

```sql
-- 企业实例数量上限：max_instance_count 为 NULL 表示不限制，正整数为上限。
-- 「实例」指 apps 表中 deleted_at IS NULL 的应用；上限校验在 service 层完成
-- （事务内 COUNT 现存未删除实例数），本列仅持久化上限值。存量企业默认 NULL，不影响现有行为。
ALTER TABLE organizations
    ADD COLUMN max_instance_count INT NULL,
    ADD CONSTRAINT organizations_max_instance_count_check
        CHECK (max_instance_count IS NULL OR max_instance_count > 0);
```

- [ ] **Step 2: 写 down migration**

文件 `internal/migrations/000004_org_max_instance_count.down.sql`：

```sql
-- 回滚企业实例数量上限：先删 CHECK 约束再删列（MySQL 8 支持 DROP CONSTRAINT）。
ALTER TABLE organizations
    DROP CONSTRAINT organizations_max_instance_count_check,
    DROP COLUMN max_instance_count;
```

- [ ] **Step 3: sqlc.yaml 追加 schema**

在 `sqlc.yaml` 的 `schema:` 列表末尾（`000003` 那行之后）追加一行：

```yaml
      - internal/migrations/000004_org_max_instance_count.up.sql
```

- [ ] **Step 4: organizations.sql 两处 query 加列**

`internal/store/queries/organizations.sql` 的 `CreateOrganization`，列清单加 `max_instance_count`、VALUES 加一个 `?`（位置放在 `credit_warning_threshold` 之后、`assistant_version_ids` 之前）：

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
    assistant_version_ids
) VALUES (
    ?, ?, ?, ?, ?, ?, ?, ?, ?, ?
);
```

同文件 `UpdateOrganizationProfile` 加 `max_instance_count = ?`（同样放在 `credit_warning_threshold` 之后）：

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
    assistant_version_ids = ?,
    updated_at = now()
WHERE id = ?;
```

> 注意：sqlc 按 query 中列出现顺序生成 Params 字段，service 层 Step 改动需与此顺序一致（Params 是命名字段，按字段名赋值即可，不依赖位置）。

- [ ] **Step 5: apps.sql 新增计数 query**

在 `internal/store/queries/apps.sql` 末尾追加：

```sql
-- name: CountActiveAppsByOrg :one
-- 统计企业当前未删除实例数（apps.deleted_at IS NULL），用于企业实例数量上限校验。
SELECT COUNT(*) FROM apps WHERE org_id = ? AND deleted_at IS NULL;
```

- [ ] **Step 6: 重新生成 sqlc 代码**

Run: `make sqlc-generate`
Expected: 命令成功；`git status` 显示 `internal/store/sqlc/` 下 `models.go`、`organizations.sql.go`、`apps.sql.go` 有改动。验证生成结果包含新字段与新方法：

Run: `grep -rn "MaxInstanceCount\|CountActiveAppsByOrg" internal/store/sqlc/`
Expected: 至少出现 `Organization.MaxInstanceCount null.Int`、`CreateOrganizationParams.MaxInstanceCount`、`UpdateOrganizationProfileParams.MaxInstanceCount`、`CountActiveAppsByOrg(ctx, ...) (int64, error)`。

- [ ] **Step 7: 确认编译通过**

Run: `make build`
Expected: 编译成功（此时 service 尚未引用新字段，仅验证生成代码合法）。

- [ ] **Step 8: 提交**

```bash
git add internal/migrations/000004_org_max_instance_count.up.sql \
        internal/migrations/000004_org_max_instance_count.down.sql \
        sqlc.yaml internal/store/queries/organizations.sql \
        internal/store/queries/apps.sql internal/store/sqlc/
git commit -m "feat(org): 新增企业实例数量上限列与计数 query

organizations 增可空列 max_instance_count（NULL=不限制，CHECK >0），
apps.sql 增 CountActiveAppsByOrg 统计未删除实例数，重新生成 sqlc 代码。"
```

---

### Task 2: service sentinel error + 企业读写字段透传

**Files:**
- Modify: `internal/service/errors.go`（新增 sentinel）
- Modify: `internal/service/organization_service.go`（Input/Result 加字段、Create/Update/Result 透传）
- Test: `internal/service/organization_service_test.go`

- [ ] **Step 1: 新增 sentinel error**

`internal/service/errors.go`，在「成员」段落（`ErrMemberCreateInvalid` 定义）之后追加：

```go
// ErrInstanceLimitReached 表示企业已达实例数量上限（organizations.max_instance_count），
// 不能再新建实例（app）。handler 层据此映射为 409 Conflict。
var ErrInstanceLimitReached = errors.New("已达企业实例数量上限")
```

- [ ] **Step 2: 写失败测试（create/update 透传 max_instance_count）**

`internal/service/organization_service_test.go` 末尾追加两个测试。先确认文件已 import `"github.com/guregu/null/v5"`（已 import）。

```go
// TestCreateOrganization_PersistsMaxInstanceCount 验证创建企业时实例上限透传到 CreateOrganizationParams。
// 覆盖正常路径：平台管理员传入正整数上限，service 应原样写库。
func TestCreateOrganization_PersistsMaxInstanceCount(t *testing.T) {
	store := &organizationStoreStub{}
	prov := &fakeProvisioner{}
	svc := newTestOrganizationService(t, store, prov)

	limit := int32(5)
	_, err := svc.CreateOrganization(context.Background(), platformAdmin(), OrganizationInput{
		Name: "限额企业", Code: "limited-org",
		AdminUsername: "admin", AdminDisplayName: "管理员", AdminPassword: "secret-123",
		MaxInstanceCount: &limit,
	})

	require.NoError(t, err)
	require.True(t, store.created.MaxInstanceCount.Valid) // 上限有效值应写库
	assert.Equal(t, int64(5), store.created.MaxInstanceCount.Int64)
}

// TestUpdateOrganization_PersistsMaxInstanceCount 验证编辑企业时实例上限透传到 UpdateOrganizationProfileParams。
// 同时覆盖「上限可低于当前实例数」语义：service 编辑路径不校验当前实例数，原样保存。
func TestUpdateOrganization_PersistsMaxInstanceCount(t *testing.T) {
	store := &organizationStoreStub{}
	store.mustSeedOrganization(t, "limited-org")
	prov := &fakeProvisioner{}
	svc := newTestOrganizationService(t, store, prov)

	limit := int32(3)
	_, err := svc.UpdateOrganization(context.Background(), platformAdmin(), store.org.ID, OrganizationInput{
		Name: "限额企业", MaxInstanceCount: &limit,
	})

	require.NoError(t, err)
	require.True(t, store.updatedProfile.MaxInstanceCount.Valid)
	assert.Equal(t, int64(3), store.updatedProfile.MaxInstanceCount.Int64)
}
```

> 校验前置：先用 `grep -n "func newTestOrganizationService\|func platformAdmin\|type fakeProvisioner" internal/service/organization_service_test.go` 确认这些 helper 名称。若实际 helper 名不同（例如直接 `NewOrganizationService(...)` 构造），按文件内既有测试的构造方式替换 `newTestOrganizationService(t, store, prov)` 与 `fakeProvisioner`，其余断言不变。

- [ ] **Step 3: 跑测试确认失败**

Run: `go test ./internal/service/ -run 'TestCreateOrganization_PersistsMaxInstanceCount|TestUpdateOrganization_PersistsMaxInstanceCount' -v`
Expected: 编译失败 `unknown field MaxInstanceCount in struct literal of type OrganizationInput`（字段尚未加）。

- [ ] **Step 4: OrganizationInput / OrganizationResult 加字段**

`internal/service/organization_service.go`，`OrganizationInput` 结构体内（`CreditWarningThreshold` 字段之后）加：

```go
	// MaxInstanceCount 是企业最多可创建的实例（应用）数；nil 写入 NULL，表示不限制。
	MaxInstanceCount *int32
```

`OrganizationResult` 结构体内（`CreditWarningThreshold` 字段之后）加：

```go
	// MaxInstanceCount 是企业实例数量上限；nil 表示不限制。
	MaxInstanceCount *int32 `json:"max_instance_count,omitempty"`
```

- [ ] **Step 5: Create / Update / Result 透传字段**

`CreateOrganization` 中 `sqlc.CreateOrganizationParams{...}`，在 `CreditWarningThreshold:` 那行之后加：

```go
		MaxInstanceCount:       nullIntFromInt32Ptr(input.MaxInstanceCount),
```

`UpdateOrganization` 中 `sqlc.UpdateOrganizationProfileParams{...}`，在 `CreditWarningThreshold:` 那行之后加：

```go
		MaxInstanceCount:       nullIntFromInt32Ptr(input.MaxInstanceCount),
```

`toOrganizationResult` 中返回的 `OrganizationResult{...}`，在 `CreditWarningThreshold:` 那行之后加：

```go
		MaxInstanceCount:       int32PtrFromNullInt(org.MaxInstanceCount),
```

> 复用现有 helper `nullIntFromInt32Ptr` / `int32PtrFromNullInt`（与 `CreditWarningThreshold` 完全同型），无需新增转换函数。

- [ ] **Step 6: 跑测试确认通过**

Run: `go test ./internal/service/ -run 'TestCreateOrganization_PersistsMaxInstanceCount|TestUpdateOrganization_PersistsMaxInstanceCount' -v`
Expected: PASS。

- [ ] **Step 7: 提交**

```bash
git add internal/service/errors.go internal/service/organization_service.go internal/service/organization_service_test.go
git commit -m "feat(org): 企业创建/编辑读写实例数量上限字段

OrganizationInput/Result 增 MaxInstanceCount，Create/Update 透传到
sqlc 参数，toOrganizationResult 读回；新增 ErrInstanceLimitReached sentinel。
补充 create/update 透传单测，覆盖上限可低于当前实例数的编辑语义。"
```

---

### Task 3: 两个建实例入口的上限校验（核心）

**Files:**
- Modify: `internal/service/onboarding_service.go`（接口加方法、helper、两处调用）
- Test: `internal/service/onboarding_service_test.go`（stub 加方法 + 新测试）

- [ ] **Step 1: stub 加 CountActiveAppsByOrg + 写失败测试**

`internal/service/onboarding_service_test.go`：

(a) `onboardingStub` 结构体加字段（在 `activeApp *sqlc.App` 之后）：

```go
	activeAppCount   int64 // 模拟 CountActiveAppsByOrg 返回的企业未删除实例数。
```

(b) 给 `onboardingStub` 加方法（放在 `GetActiveAppByOwner` 方法之后）：

```go
// CountActiveAppsByOrg 返回 stub 预置的企业未删除实例数，供实例上限校验测试。
func (s *onboardingStub) CountActiveAppsByOrg(_ context.Context, _ string) (int64, error) {
	return s.activeAppCount, nil
}
```

(c) 文件末尾追加 4 个测试：

```go
// TestCreateAppForMember_RejectsWhenInstanceLimitReached 验证企业已达实例上限时补建实例被拒。
// 边界：当前未删除实例数 == 上限（3），应返回 ErrInstanceLimitReached 且事务回滚。
func TestCreateAppForMember_RejectsWhenInstanceLimitReached(t *testing.T) {
	store := newOnboardingStub(t)
	store.org.MaxInstanceCount = null.IntFrom(3) // 企业上限 3
	store.activeAppCount = 3                      // 当前已 3 个未删除实例，达到上限
	tx := &txRunnerStub{store: store}
	svc := NewMemberOnboardingService(tx, fakeHash)

	_, err := svc.CreateAppForMember(context.Background(), platformAdmin(), testOrgID, store.user.ID, CreateAppForMemberInput{
		AppName: "alice-new-bot", VersionID: testVersionID,
	})

	require.ErrorIs(t, err, ErrInstanceLimitReached)
	require.False(t, tx.committed)
}

// TestCreateAppForMember_AllowsBelowInstanceLimit 验证未达上限时可正常补建实例。
// 边界：当前 2 个 < 上限 3，应创建成功。
func TestCreateAppForMember_AllowsBelowInstanceLimit(t *testing.T) {
	store := newOnboardingStub(t)
	store.org.MaxInstanceCount = null.IntFrom(3)
	store.activeAppCount = 2
	tx := &txRunnerStub{store: store}
	svc := NewMemberOnboardingService(tx, fakeHash)

	result, err := svc.CreateAppForMember(context.Background(), platformAdmin(), testOrgID, store.user.ID, CreateAppForMemberInput{
		AppName: "alice-new-bot", VersionID: testVersionID,
	})

	require.NoError(t, err)
	require.True(t, tx.committed)
	assert.Equal(t, "alice-new-bot", result.App.Name)
}

// TestCreateAppForMember_AllowsWhenLimitUnset 验证上限为 NULL（不限制）时即便实例数很大也可创建。
func TestCreateAppForMember_AllowsWhenLimitUnset(t *testing.T) {
	store := newOnboardingStub(t)
	store.org.MaxInstanceCount = null.Int{} // NULL = 不限制
	store.activeAppCount = 999
	tx := &txRunnerStub{store: store}
	svc := NewMemberOnboardingService(tx, fakeHash)

	_, err := svc.CreateAppForMember(context.Background(), platformAdmin(), testOrgID, store.user.ID, CreateAppForMemberInput{
		AppName: "alice-new-bot", VersionID: testVersionID,
	})

	require.NoError(t, err)
	require.True(t, tx.committed)
}

// TestOnboardMember_RejectsWhenInstanceLimitReached 验证 onboard 成员（建成员+实例）入口同样受上限约束。
func TestOnboardMember_RejectsWhenInstanceLimitReached(t *testing.T) {
	store := newOnboardingStub(t)
	store.org.MaxInstanceCount = null.IntFrom(2)
	store.activeAppCount = 2
	tx := &txRunnerStub{store: store}
	svc := NewMemberOnboardingService(tx, fakeHash)

	_, err := svc.OnboardMember(context.Background(), orgAdmin(testOrgID), testOrgID, OnboardMemberInput{
		Username: "bob", DisplayName: "Bob", Password: "secret-123",
		AppName: "bob-bot", VersionID: testVersionID,
	})

	require.ErrorIs(t, err, ErrInstanceLimitReached)
	require.False(t, tx.committed)
}
```

> 校验前置：用 `grep -n "func orgAdmin\|func platformAdmin\|func fakeHash\|fakeHash =" internal/service/*_test.go` 确认 `orgAdmin(...)`、`platformAdmin()`、`fakeHash` helper 名称。OnboardMember 要求 `auth.CanCreateAppForOrg` 通过（org_admin 本组织），若已有 onboard 测试用的是别的 helper（如 `orgAdminPrincipal`），替换成实际名称。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/service/ -run 'InstanceLimit' -v`
Expected: 编译失败 `store (variable of type *onboardingStub) does not implement OnboardingStore`（接口尚未声明该方法）或断言失败 —— 因 service 尚未做校验，limit-reached 测试会因创建成功而 `require.ErrorIs` 失败。

- [ ] **Step 3: OnboardingStore 接口加方法**

`internal/service/onboarding_service.go`，`OnboardingStore` 接口内（`GetActiveAppByOwner` 那行之后）加：

```go
	// CountActiveAppsByOrg 统计企业当前未删除实例数（apps.deleted_at IS NULL），用于实例上限校验。
	CountActiveAppsByOrg(ctx context.Context, orgID string) (int64, error)
```

- [ ] **Step 4: 新增 ensureInstanceQuota helper**

`internal/service/onboarding_service.go`，在 `versionInOrgAllowlist` 函数之前（或之后）加：

```go
// ensureInstanceQuota 校验企业未达实例数量上限。
// org.MaxInstanceCount 无效（NULL）表示不限制，直接放行；否则统计企业未删除实例数，
// 达到或超过上限即返回 ErrInstanceLimitReached。
//
// 并发说明：计数与随后的 CreateApp 在同一事务内但不加行锁，两个并发事务理论上可能
// 都读到相同计数而各插一条、轻微越限。鉴于建实例是平台/企业管理员的手动低频操作，
// 此 race 可接受（见设计文档「语义决策」）。
func ensureInstanceQuota(ctx context.Context, store OnboardingStore, org sqlc.Organization) error {
	if !org.MaxInstanceCount.Valid {
		return nil
	}
	count, err := store.CountActiveAppsByOrg(ctx, org.ID)
	if err != nil {
		return fmt.Errorf("统计企业实例数失败: %w", err)
	}
	if count >= org.MaxInstanceCount.Int64 {
		return fmt.Errorf("%w (%d)", ErrInstanceLimitReached, org.MaxInstanceCount.Int64)
	}
	return nil
}
```

- [ ] **Step 5: OnboardMember 事务内加校验**

`OnboardMember` 的事务函数内，紧跟「校验所选助手版本在组织 allowlist 内」的 `if !versionInOrgAllowlist(...) {...}` 块之后、`store.CreateUser(...)` 之前，加：

```go
		// 校验企业未达实例数量上限（max_instance_count）。
		if err := ensureInstanceQuota(ctx, store, org); err != nil {
			return err
		}
```

- [ ] **Step 6: CreateAppForMember 事务内加校验**

`CreateAppForMember` 的事务函数内，紧跟其「校验所选助手版本在组织 allowlist 内」的 `if !versionInOrgAllowlist(...) {...}` 块之后、`store.GetUser(...)` 之前，加同样的块：

```go
		// 校验企业未达实例数量上限（max_instance_count）。
		if err := ensureInstanceQuota(ctx, store, org); err != nil {
			return err
		}
```

- [ ] **Step 7: 跑新测试 + 全量 service 测试确认通过**

Run: `go test ./internal/service/ -run 'InstanceLimit' -v`
Expected: 4 个新测试全部 PASS。

Run: `go test ./internal/service/`
Expected: 全部 PASS（既有 onboard / app 测试不受影响：默认 stub `MaxInstanceCount` 为 NULL，`activeAppCount` 为 0，quota 始终放行）。

- [ ] **Step 8: 提交**

```bash
git add internal/service/onboarding_service.go internal/service/onboarding_service_test.go
git commit -m "feat(org): onboard/补建实例入口校验企业实例数量上限

OnboardingStore 增 CountActiveAppsByOrg，新增 ensureInstanceQuota 在两个
建实例入口的事务内比较未删除实例数与上限，超限返回 ErrInstanceLimitReached。
接受并发轻微越限的 race（手动低频操作）。补充 4 个边界单测。"
```

---

### Task 4: handler DTO + 转换 + 错误映射（409）

**Files:**
- Modify: `internal/api/handlers/dto.go`（两个请求体加字段）
- Modify: `internal/api/handlers/organizations.go`（两个转换函数透传）
- Modify: `internal/api/handlers/members.go`（writeMemberError 加 409 分支）
- Test: `internal/api/handlers/members_test.go`（可选断言 409，见 Step 4）

- [ ] **Step 1: dto.go 两个请求体加字段**

`internal/api/handlers/dto.go`，`CreateOrganizationRequest` 内（`CreditWarningThreshold` 之后）加：

```go
	// MaxInstanceCount 是企业最多可创建的实例（应用）数；nil 表示不限制。
	MaxInstanceCount *int32 `json:"max_instance_count"`
```

`OrganizationRequest` 内（`CreditWarningThreshold` 之后）加同样字段：

```go
	// MaxInstanceCount 是企业最多可创建的实例（应用）数；nil 表示不限制。
	MaxInstanceCount *int32 `json:"max_instance_count"`
```

- [ ] **Step 2: organizations.go 两个转换函数透传**

`internal/api/handlers/organizations.go`，`toOrganizationInput` 的 `service.OrganizationInput{...}` 内加：

```go
		MaxInstanceCount:       req.MaxInstanceCount,
```

`toCreateOrganizationInput` 的 `service.OrganizationInput{...}` 内加：

```go
		MaxInstanceCount:       req.MaxInstanceCount,
```

- [ ] **Step 3: members.go 错误映射加 409 分支**

`internal/api/handlers/members.go` 的 `writeMemberError`，在 `case errors.Is(err, service.ErrNotFound):` 之后、`case errors.Is(err, service.ErrMemberCreateInvalid):` 之前加：

```go
	case errors.Is(err, service.ErrInstanceLimitReached):
		c.JSON(http.StatusConflict, apierror.New("INSTANCE_LIMIT_REACHED", validationServiceMessage(err, service.ErrInstanceLimitReached)))
```

> 说明：上限错误只可能来自 onboard / 补建实例两个入口，二者都走 `writeMemberError`；组织 CRUD 的 `writeServiceError` 不会收到该错误，无需改动。

- [ ] **Step 4: 编译并跑 handler 测试**

Run: `go test ./internal/api/handlers/`
Expected: PASS（既有测试不受影响）。

> 可选：若想覆盖 409 映射，可在 `members_test.go` 新增一个测试，让 members service stub 的 `CreateAppForMember` 返回 `service.ErrInstanceLimitReached`，断言 HTTP 409 与 body `code == "INSTANCE_LIMIT_REACHED"`。沿用文件内既有 handler 测试的 stub 构造方式（先 `grep -n "func Test.*CreateAppForMember\|membersServiceStub\|type .*[Ss]tub" internal/api/handlers/members_test.go` 确认 stub 名称与注入方式）。若 stub 模式不直观则跳过，核心校验已在 Task 3 service 层覆盖。

- [ ] **Step 5: 提交**

```bash
git add internal/api/handlers/dto.go internal/api/handlers/organizations.go internal/api/handlers/members.go internal/api/handlers/members_test.go
git commit -m "feat(org): 企业请求体增实例上限字段并映射超限为 409

CreateOrganizationRequest/OrganizationRequest 增 max_instance_count 并
透传到 service 入参；members handler 将 ErrInstanceLimitReached 映射为
409 INSTANCE_LIMIT_REACHED。"
```

---

### Task 5: 重新生成 OpenAPI 契约与前端类型

**Files:**
- 生成产物：`openapi/openapi.yaml`、`web/src/api/generated.ts`（勿手改）

- [ ] **Step 1: 生成 OpenAPI**

Run: `make openapi-gen`
Expected: 命令成功；`openapi/openapi.yaml` 中 `CreateOrganizationRequest`、`OrganizationRequest`、`OrganizationResult`（service 响应）新增 `max_instance_count` 字段。

- [ ] **Step 2: 生成前端类型**

Run: `make web-types-gen`
Expected: `web/src/api/generated.ts` 中 `Organization` / 相关请求类型出现 `max_instance_count?: number`。

Run: `grep -n "max_instance_count" web/src/api/generated.ts openapi/openapi.yaml`
Expected: 两个文件都包含该字段。

- [ ] **Step 3: 校验 openapi 同步**

Run: `make openapi-check`
Expected: 通过（git 工作区干净，说明 yaml 已跟随代码）。

- [ ] **Step 4: 提交**

```bash
git add openapi/openapi.yaml web/src/api/generated.ts
git commit -m "chore(openapi): 同步企业实例上限字段到 openapi 与前端类型

make openapi-gen + web-types-gen 生成 max_instance_count 字段。"
```

---

### Task 6: 前端表单字段 + payload + 列表展示

**Files:**
- Modify: `web/src/api/hooks/useOrganizations.ts`（两个 payload 加字段）
- Modify: `web/src/pages/platform/OrganizationsPage.vue`（创建/编辑表单、payload、列表列）

- [ ] **Step 1: payload 类型加字段**

`web/src/api/hooks/useOrganizations.ts`，`OrganizationFormPayload` 内（`credit_warning_threshold` 之后）加：

```ts
  // 实例数量上限；null/undefined 表示不限制。
  max_instance_count?: number | null
```

`OrganizationUpdatePayload` 内（`credit_warning_threshold` 之后）加同样字段：

```ts
  // 实例数量上限；null/undefined 表示不限制。
  max_instance_count?: number | null
```

- [ ] **Step 2: 创建表单状态 + payload**

`web/src/pages/platform/OrganizationsPage.vue` 的 `useFormModal({ initial: {...} })`，在 `credit_warning_threshold: undefined as number | undefined,` 之后加：

```ts
    max_instance_count: undefined as number | undefined,
```

同一 `useFormModal` 的 `toPayload: (f) => ({...})`，在 `credit_warning_threshold:` 那行之后加：

```ts
    max_instance_count: typeof f.max_instance_count === 'number' ? f.max_instance_count : undefined,
```

- [ ] **Step 3: 编辑表单状态 + 预填 + payload**

同文件 `editForm` reactive 对象，在 `credit_warning_threshold: undefined as number | undefined,` 之后加：

```ts
  max_instance_count: undefined as number | undefined,
```

`openEditForm(org)` 内，在 `editForm.credit_warning_threshold = ...` 那行之后加：

```ts
  editForm.max_instance_count = typeof org.max_instance_count === 'number'
    ? org.max_instance_count : undefined
```

`submitEditOrganization` 内 `updateMutation.mutateAsync({ id, payload: {...} })` 的 payload，在 `credit_warning_threshold:` 那行之后加：

```ts
        max_instance_count: typeof editForm.max_instance_count === 'number'
          ? editForm.max_instance_count : undefined,
```

- [ ] **Step 4: 表单加输入框（创建 + 编辑两套，镜像余额预警阈值）**

在「余额预警阈值」那个 `<n-grid-item>...</n-grid-item>`（含 `form.credit_warning_threshold` / `editForm.credit_warning_threshold` 的 n-input-number）之后，新增一个 grid item：

```vue
          <n-grid-item>
            <n-form-item label="实例数量上限（留空 = 不限制）">
              <n-input-number
                v-if="modalMode === 'create'"
                v-model:value="form.max_instance_count"
                :min="1" :precision="0" clearable style="width: 100%"
                placeholder="留空表示不限制"
              />
              <n-input-number
                v-else
                v-model:value="editForm.max_instance_count"
                :min="1" :precision="0" clearable style="width: 100%"
                placeholder="留空表示不限制"
              />
            </n-form-item>
          </n-grid-item>
```

- [ ] **Step 5: 列表加「实例上限」列**

`columns` computed 内，在「预警阈值」列对象之后加一列：

```ts
  {
    title: '实例上限',
    key: 'max_instance_count',
    render: (row: Organization) => typeof row.max_instance_count === 'number'
      ? String(row.max_instance_count) : '不限',
  },
```

- [ ] **Step 6: 前端类型检查 + 单测**

Run: `make web-typecheck`
Expected: 通过（`Organization`、payload 类型已含 `max_instance_count`）。

Run: `make web-test`
Expected: 通过（无新增前端单测；确认未破坏既有）。

- [ ] **Step 7: 提交**

```bash
git add web/src/api/hooks/useOrganizations.ts web/src/pages/platform/OrganizationsPage.vue
git commit -m "feat(web): 企业表单增实例数量上限输入与列表展示

创建/编辑企业表单新增「实例数量上限（留空=不限制）」数字输入并透传 payload，
企业列表新增「实例上限」列。"
```

---

### Task 7: 整体验证（含真实浏览器）

**Files:** 无代码改动，仅验证。

- [ ] **Step 1: 全量后端测试 + 构建**

Run: `make test`
Expected: 全部 PASS。

Run: `make build`
Expected: 编译成功。

- [ ] **Step 2: openapi 同步终检**

Run: `make openapi-check`
Expected: 通过。

- [ ] **Step 3: 本地 k3d 跑迁移**

> 前置：本地 k3d 全栈已起（`make local-up`）。若未起或镜像未含新代码，先 `make local-build` 再继续。

Run: `make local-migrate`
Expected: 000004 迁移成功，`organizations` 出现 `max_instance_count` 列。

- [ ] **Step 4: 真实浏览器验证（平台管理员视角）**

按 AGENTS.md「交付前检查」要求，用真实浏览器（非 curl）验证。登录 http://ocm.localhost （admin / admin123，组织标识留空）：

1. 新建企业时设「实例数量上限 = 1」，保存成功；企业列表「实例上限」列显示 `1`。
2. 进该企业成员管理，onboard 第 1 个成员（含实例）成功。
3. 再 onboard 第 2 个成员（或给已有成员补建实例）→ 前端提示「已达企业实例数量上限 (1)」，HTTP 409，实例未创建。
4. 编辑企业把上限改为 5，保存成功；再 onboard 成员成功（验证上限放宽即时生效）。
5. 编辑企业清空上限（留空）→ 列表显示「不限」；再建实例不受限。
6. 编辑一个已有 2 个实例的企业，把上限改为 1，保存成功（验证「上限可低于当前实例数，仅堵后续」），但再新建实例被 409 拒绝。

每步若发现问题，先修复并重新验证，直到全部正常再交付。

- [ ] **Step 5: 工作区终检**

Run: `git status`
Expected: 干净（无遗漏的生成产物、无临时调试文件）。确认本次改动未混入无关文件。

---

## 自检对照（Self-Review）

- **Spec 覆盖**：migration（Task 1）/ sqlc 计数（Task 1）/ service 字段（Task 2）/ 两入口事务内校验（Task 3）/ sentinel + 409（Task 2、4）/ DTO（Task 4）/ OpenAPI+类型（Task 5）/ 前端表单+payload（Task 6）/ 单测（Task 2、3）/ 浏览器验证（Task 7）—— 全部有对应任务。
- **留空=不限制**：`*int32` nil → `nullIntFromInt32Ptr` → NULL；`ensureInstanceQuota` 对 `!Valid` 直接放行。✅
- **上限可低于当前数**：UpdateOrganization 不校验当前实例数，仅持久化（Task 2 Step 2 测试覆盖）。✅
- **接受 race**：`ensureInstanceQuota` 注释说明，无行锁。✅
- **类型一致性**：service 用 `MaxInstanceCount *int32`，sqlc 列 `null.Int`，转换走既有 `nullIntFromInt32Ptr`/`int32PtrFromNullInt`；接口方法 `CountActiveAppsByOrg(ctx, string) (int64, error)` 在接口、stub、生成代码、helper 调用四处签名一致。✅
- **helper 名称风险**：Task 2/3/4 测试用到的 `newTestOrganizationService`/`fakeProvisioner`/`platformAdmin`/`orgAdmin`/`fakeHash` 均标注了 `grep` 前置校验，避免名称不符。
