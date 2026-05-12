# Organization Code Login Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add an immutable organization login code so organization users log in with `org_code + username + password`, while platform admins continue logging in with username and password only.

**Architecture:** Add `organizations.code` as the tenant login namespace, change `users.username` uniqueness from global to scoped, and route authentication through either platform lookup or organization-scoped lookup. Keep organization display name (`organizations.name`) separate from login code, and update backend DTOs, services, generated contracts, frontend forms, seed data, and docs together.

**Tech Stack:** Go 1.25, Gin, pgx, sqlc, golang-migrate, PostgreSQL, testify, Vue 3, Pinia, Naive UI, TanStack Query, Vitest, OpenAPI/swag.

---

## File Structure

- Create: `internal/migrations/000013_organization_code_login.up.sql` — add organization code and scoped username indexes.
- Create: `internal/migrations/000013_organization_code_login.down.sql` — reverse schema changes when data still allows restoring global username uniqueness.
- Modify: `sqlc.yaml` — include migration 000013 in sqlc schema inputs.
- Modify: `internal/store/queries/organizations.sql` — persist and query organization code.
- Modify: `internal/store/queries/users.sql` — add `(org_id, username)` user lookup.
- Generated: `internal/store/sqlc/*` — regenerate after query and schema changes.
- Modify: `internal/service/auth_service.go` and `internal/service/auth_service_test.go` — add org-code login routing.
- Modify: `internal/service/organization_service.go` and `internal/service/organization_service_test.go` — validate and persist organization code.
- Modify: `internal/api/handlers/dto.go`, `internal/api/handlers/auth.go`, `internal/api/handlers/auth_test.go`, `internal/api/handlers/organizations.go`, `internal/api/handlers/organizations_test.go` — HTTP DTO passthrough and tests.
- Modify: `internal/service/member_service_test.go` and `internal/service/onboarding_service_test.go` — verify same short username is allowed across different organizations at service boundary where stubs can model uniqueness.
- Modify: `cmd/seed-e2e/main.go` — seed a stable organization code.
- Modify: `web/src/stores/auth.ts`, `web/src/pages/login/LoginPage.vue`, `web/src/pages/platform/OrganizationsPage.vue`, `web/src/api/hooks/useOrganizations.ts`, `web/src/api/hooks/useMembers.ts` — frontend payloads and UI.
- Create: `web/src/stores/auth.spec.ts` — verify login requests include `org_code`.
- Test: `web/src/pages/platform/OrganizationsPage.spec.ts` — verify organization code renders and submits.
- Generated: `openapi/openapi.yaml`, `web/src/api/generated.ts` — regenerate after backend DTO changes.
- Modify: `AGENTS.md`, `docs/user-manual.md` — update local login account instructions.

---

### Task 1: Database Schema And sqlc Queries

**Files:**
- Create: `internal/migrations/000013_organization_code_login.up.sql`
- Create: `internal/migrations/000013_organization_code_login.down.sql`
- Modify: `sqlc.yaml`
- Modify: `internal/store/queries/organizations.sql`
- Modify: `internal/store/queries/users.sql`
- Generated: `internal/store/sqlc/*`

- [ ] **Step 1: Write migration up**

Create `internal/migrations/000013_organization_code_login.up.sql`:

```sql
-- organizations.code 是组织登录命名空间，创建后不可通过业务接口修改。
ALTER TABLE organizations ADD COLUMN code text NULL;

-- 历史组织自动生成稳定 code：
-- 1. 英文/数字组织名转小写 slug；
-- 2. 中文或其它无法转出有效 slug 的名称使用 org-<uuid8>；
-- 3. slug 冲突时追加 uuid8，避免唯一约束失败。
WITH raw AS (
    SELECT
        id,
        btrim(lower(regexp_replace(name, '[^a-zA-Z0-9]+', '-', 'g')), '-') AS slug
    FROM organizations
),
base AS (
    SELECT
        id,
        CASE
            WHEN slug ~ '^[a-z0-9]([a-z0-9-]{1,30}[a-z0-9])$'
                THEN slug
            ELSE 'org-' || left(replace(id::text, '-', ''), 8)
        END AS base_code
    FROM raw
),
ranked AS (
    SELECT
        id,
        base_code,
        count(*) OVER (PARTITION BY base_code) AS same_code_count
    FROM base
),
resolved AS (
    SELECT
        id,
        CASE
            WHEN same_code_count = 1 THEN base_code
            ELSE btrim(left(base_code, 23), '-') || '-' || left(replace(id::text, '-', ''), 8)
        END AS code
    FROM ranked
)
UPDATE organizations
SET code = resolved.code
FROM resolved
WHERE organizations.id = resolved.id;

ALTER TABLE organizations ALTER COLUMN code SET NOT NULL;

ALTER TABLE organizations
    ADD CONSTRAINT organizations_code_format_check
    CHECK (code ~ '^[a-z0-9]([a-z0-9-]{1,30}[a-z0-9])$');

ALTER TABLE organizations
    ADD CONSTRAINT organizations_code_key UNIQUE (code);

-- users.username 从全局唯一改为按账号归属范围唯一：
-- 平台管理员无 org_id，平台范围内 username 唯一；
-- 组织用户有 org_id，同组织内 username 唯一，不同组织可重复。
ALTER TABLE users DROP CONSTRAINT IF EXISTS users_username_key;

CREATE UNIQUE INDEX users_org_username_uniq
    ON users(org_id, username)
    WHERE org_id IS NOT NULL;

CREATE UNIQUE INDEX users_platform_username_uniq
    ON users(username)
    WHERE org_id IS NULL;
```

- [ ] **Step 2: Write migration down**

Create `internal/migrations/000013_organization_code_login.down.sql`:

```sql
DROP INDEX IF EXISTS users_platform_username_uniq;
DROP INDEX IF EXISTS users_org_username_uniq;

-- 如果迁移后已经创建了跨组织重复 username，这一步会失败，避免静默破坏数据。
ALTER TABLE users
    ADD CONSTRAINT users_username_key UNIQUE (username);

ALTER TABLE organizations DROP CONSTRAINT IF EXISTS organizations_code_key;
ALTER TABLE organizations DROP CONSTRAINT IF EXISTS organizations_code_format_check;
ALTER TABLE organizations DROP COLUMN IF EXISTS code;
```

- [ ] **Step 3: Add migration to sqlc schema inputs**

Modify `sqlc.yaml` and append migration 000013 after 000012:

```yaml
      - internal/migrations/000012_runtime_nodes_auto_enroll.up.sql
      - internal/migrations/000013_organization_code_login.up.sql
```

- [ ] **Step 4: Update organization queries**

Modify `internal/store/queries/organizations.sql`.

Change `CreateOrganization` to include `code`:

```sql
-- name: CreateOrganization :one
INSERT INTO organizations (
    name,
    code,
    status,
    contact_name,
    contact_phone,
    remark,
    credit_warning_threshold
) VALUES (
    $1, $2, $3, $4, $5, $6, $7
)
RETURNING *;
```

Add a code lookup near `GetOrganizationByName`:

```sql
-- name: GetOrganizationByCode :one
SELECT *
FROM organizations
WHERE code = $1;
```

Leave `UpdateOrganizationProfile` unchanged so it cannot modify `code`.

- [ ] **Step 5: Update user queries**

Modify `internal/store/queries/users.sql` and add scoped lookup after `GetUserByUsername`:

```sql
-- name: GetUserByOrgAndUsername :one
SELECT *
FROM users
WHERE org_id = $1 AND username = $2;
```

Leave `CreateUser` as `org_id, username, ...`; it already receives both columns.

- [ ] **Step 6: Regenerate sqlc**

Run:

```bash
rtk make sqlc-generate
```

Expected: PASS. Generated files under `internal/store/sqlc/` include:

- `Organization.Code`
- `CreateOrganizationParams.Code`
- `GetOrganizationByCode`
- `GetUserByOrgAndUsername`

- [ ] **Step 7: Verify migration SQL is loadable**

Run:

```bash
rtk go test ./internal/migrations -count=1
```

Expected: PASS.

- [ ] **Step 8: Commit database and sqlc changes**

```bash
rtk git add \
  internal/migrations/000013_organization_code_login.up.sql \
  internal/migrations/000013_organization_code_login.down.sql \
  sqlc.yaml \
  internal/store/queries/organizations.sql \
  internal/store/queries/users.sql \
  internal/store/sqlc

rtk git commit -m "feat(db): 增加组织登录标识和组织内用户名唯一约束" -m "新增 organizations.code 作为登录命名空间，并把 users.username 从全局唯一调整为平台范围或组织范围唯一。同步 sqlc 查询，为按 org_code 登录和跨组织重复短用户名提供数据层支持。"
```

---

### Task 2: AuthService Organization-Code Login

**Files:**
- Modify: `internal/service/auth_service.go`
- Modify: `internal/service/auth_service_test.go`

- [ ] **Step 1: Write failing AuthService tests**

In `internal/service/auth_service_test.go`, replace the current single-user login setup with tests that distinguish platform and organization users. Add these tests near existing login tests:

```go
func TestAuthServiceLoginPlatformAdminWithoutOrgCode(t *testing.T) {
	store := newAuthStoreStub(t)
	svc := newTestAuthService(t, store)

	result, err := svc.Login(context.Background(), LoginInput{
		Username: "admin",
		Password: "correct-password",
	})

	require.NoError(t, err)
	require.Equal(t, domain.UserRolePlatformAdmin, result.User.Role)
	require.Equal(t, "admin", result.User.Username)
	require.Empty(t, result.User.OrgID)
}

func TestAuthServiceLoginRejectsPlatformAdminWithOrgCode(t *testing.T) {
	store := newAuthStoreStub(t)
	svc := newTestAuthService(t, store)

	_, err := svc.Login(context.Background(), LoginInput{
		OrgCode:  "test-org",
		Username: "admin",
		Password: "correct-password",
	})

	require.ErrorIs(t, err, ErrInvalidCredentials)
}

func TestAuthServiceLoginOrgUserWithOrgCode(t *testing.T) {
	store := newAuthStoreStub(t)
	svc := newTestAuthService(t, store)

	result, err := svc.Login(context.Background(), LoginInput{
		OrgCode:  "test-org",
		Username: "admin",
		Password: "correct-password",
	})

	require.NoError(t, err)
	require.Equal(t, domain.UserRoleOrgAdmin, result.User.Role)
	require.Equal(t, "admin", result.User.Username)
	require.Equal(t, testOrgID, result.User.OrgID)
}

func TestAuthServiceLoginRejectsOrgUserWithoutOrgCode(t *testing.T) {
	store := newAuthStoreStub(t)
	svc := newTestAuthService(t, store)

	_, err := svc.Login(context.Background(), LoginInput{
		Username: "member",
		Password: "correct-password",
	})

	require.ErrorIs(t, err, ErrInvalidCredentials)
}

func TestAuthServiceLoginRejectsUnknownOrgCode(t *testing.T) {
	store := newAuthStoreStub(t)
	svc := newTestAuthService(t, store)

	_, err := svc.Login(context.Background(), LoginInput{
		OrgCode:  "missing-org",
		Username: "admin",
		Password: "correct-password",
	})

	require.ErrorIs(t, err, ErrInvalidCredentials)
}
```

Update the existing disabled-org test so it uses organization login:

```go
_, err := svc.Login(context.Background(), LoginInput{
	OrgCode:  "test-org",
	Username: "admin",
	Password: "correct-password",
})
```

Update the existing token-issuing and refresh tests to log in as `OrgCode: "test-org", Username: "admin"` or as platform `Username: "admin"` consistently. Do not keep a login test that depends on `member@example.com` unless the stub contains that exact organization user.

- [ ] **Step 2: Run AuthService tests and verify failure**

Run:

```bash
rtk go test ./internal/service -count=1 -run 'TestAuthServiceLogin'
```

Expected: FAIL because `LoginInput.OrgCode`, `AuthStore.GetOrganizationByCode`, and `AuthStore.GetUserByOrgAndUsername` are not implemented yet.

- [ ] **Step 3: Extend AuthStore and LoginInput**

Modify `internal/service/auth_service.go`:

```go
type AuthStore interface {
	GetUserByUsername(ctx context.Context, username string) (sqlc.User, error)
	GetUserByOrgAndUsername(ctx context.Context, arg sqlc.GetUserByOrgAndUsernameParams) (sqlc.User, error)
	GetUser(ctx context.Context, id pgtype.UUID) (sqlc.User, error)
	MarkUserLoggedIn(ctx context.Context, id pgtype.UUID) (sqlc.User, error)
	GetOrganization(ctx context.Context, id pgtype.UUID) (sqlc.Organization, error)
	GetOrganizationByCode(ctx context.Context, code string) (sqlc.Organization, error)
	CreateRefreshToken(ctx context.Context, arg sqlc.CreateRefreshTokenParams) (sqlc.RefreshToken, error)
	GetRefreshTokenByHash(ctx context.Context, tokenHash string) (sqlc.RefreshToken, error)
	RevokeRefreshToken(ctx context.Context, id pgtype.UUID) (sqlc.RefreshToken, error)
}
```

```go
type LoginInput struct {
	OrgCode  string
	Username string
	Password string
}
```

- [ ] **Step 4: Implement scoped login lookup**

Replace the beginning of `Login` in `internal/service/auth_service.go` with:

```go
func (s *AuthService) Login(ctx context.Context, input LoginInput) (LoginResult, error) {
	input.OrgCode = strings.ToLower(strings.TrimSpace(input.OrgCode))
	input.Username = strings.TrimSpace(input.Username)

	user, err := s.lookupLoginUser(ctx, input)
	if err != nil {
		return LoginResult{}, err
	}
	if !s.passwordHash(input.Password, user.PasswordHash) {
		return LoginResult{}, ErrInvalidCredentials
	}
	// 登录前重新检查用户和组织状态，避免已禁用账号继续拿到新令牌。
	if err := s.ensureUserEnabled(ctx, user); err != nil {
		return LoginResult{}, err
	}

	if _, err := s.store.MarkUserLoggedIn(ctx, user.ID); err != nil {
		return LoginResult{}, fmt.Errorf("更新登录时间失败: %w", err)
	}
	return s.issueTokenPair(ctx, user)
}
```

Add this helper below `Login`:

```go
// lookupLoginUser 根据 org_code 是否为空选择平台登录或组织登录路径。
// 账号不存在、组织标识不存在和角色不匹配统一返回 ErrInvalidCredentials，
// 避免登录接口泄露租户或用户名枚举信息。
func (s *AuthService) lookupLoginUser(ctx context.Context, input LoginInput) (sqlc.User, error) {
	if input.OrgCode == "" {
		user, err := s.store.GetUserByUsername(ctx, input.Username)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return sqlc.User{}, ErrInvalidCredentials
			}
			return sqlc.User{}, fmt.Errorf("查询用户失败: %w", err)
		}
		if user.Role != domain.UserRolePlatformAdmin || user.OrgID.Valid {
			return sqlc.User{}, ErrInvalidCredentials
		}
		return user, nil
	}

	org, err := s.store.GetOrganizationByCode(ctx, input.OrgCode)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return sqlc.User{}, ErrInvalidCredentials
		}
		return sqlc.User{}, fmt.Errorf("查询组织标识失败: %w", err)
	}
	user, err := s.store.GetUserByOrgAndUsername(ctx, sqlc.GetUserByOrgAndUsernameParams{
		OrgID:    org.ID,
		Username: input.Username,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return sqlc.User{}, ErrInvalidCredentials
		}
		return sqlc.User{}, fmt.Errorf("查询组织用户失败: %w", err)
	}
	if user.Role == domain.UserRolePlatformAdmin || !user.OrgID.Valid {
		return sqlc.User{}, ErrInvalidCredentials
	}
	return user, nil
}
```

Add `strings` to the import list.

- [ ] **Step 5: Update auth test stub**

In `internal/service/auth_service_test.go`, define stable IDs at package scope:

```go
const (
	testOrgID           = "00000000-0000-0000-0000-000000000101"
	testPlatformAdminID = "00000000-0000-0000-0000-000000000200"
	testOrgAdminID      = "00000000-0000-0000-0000-000000000201"
	testOrgMemberID     = "00000000-0000-0000-0000-000000000202"
)
```

Replace `authStoreStub` user fields with maps:

```go
type authStoreStub struct {
	usersByID       map[string]sqlc.User
	platformByName  map[string]sqlc.User
	orgUsersByKey   map[string]sqlc.User
	orgsByID        map[string]sqlc.Organization
	orgsByCode      map[string]sqlc.Organization
	nextRefreshID   pgtype.UUID
	idCounter       byte
	loggedIn        bool
	lastIssuedRole  string
	refreshTokens   map[string]sqlc.RefreshToken
	revoked         []pgtype.UUID
}
```

Add helper keys:

```go
func orgUserKey(orgID pgtype.UUID, username string) string {
	return uuidToString(orgID) + "/" + username
}
```

Update `newAuthStoreStub` so it creates both `admin` users:

```go
orgID := mustUUID(t, testOrgID)
platformID := mustUUID(t, testPlatformAdminID)
orgAdminID := mustUUID(t, testOrgAdminID)
orgMemberID := mustUUID(t, testOrgMemberID)
refreshID := mustUUID(t, "00000000-0000-0000-0000-000000000301")
hash, err := auth.HashPassword("correct-password", auth.PasswordParams{
	Memory:      32,
	Iterations:  1,
	Parallelism: 1,
	SaltLength:  8,
	KeyLength:   16,
})
require.NoError(t, err)
org := sqlc.Organization{
	ID:     orgID,
	Code:   "test-org",
	Name:   "测试组织",
	Status: domain.StatusActive,
}
platformAdmin := sqlc.User{
	ID:           platformID,
	Username:     "admin",
	PasswordHash: hash,
	DisplayName:  "平台管理员",
	Role:         domain.UserRolePlatformAdmin,
	Status:       domain.StatusActive,
}
orgAdmin := sqlc.User{
	ID:           orgAdminID,
	OrgID:        orgID,
	Username:     "admin",
	PasswordHash: hash,
	DisplayName:  "组织管理员",
	Role:         domain.UserRoleOrgAdmin,
	Status:       domain.StatusActive,
}
orgMember := sqlc.User{
	ID:           orgMemberID,
	OrgID:        orgID,
	Username:     "member",
	PasswordHash: hash,
	DisplayName:  "组织成员",
	Role:         domain.UserRoleOrgMember,
	Status:       domain.StatusActive,
}
return &authStoreStub{
	usersByID: map[string]sqlc.User{
		uuidToString(platformAdmin.ID): platformAdmin,
		uuidToString(orgAdmin.ID):      orgAdmin,
		uuidToString(orgMember.ID):     orgMember,
	},
	platformByName: map[string]sqlc.User{
		platformAdmin.Username: platformAdmin,
	},
	orgUsersByKey: map[string]sqlc.User{
		orgUserKey(org.ID, orgAdmin.Username): orgAdmin,
		orgUserKey(org.ID, orgMember.Username): orgMember,
	},
	orgsByID: map[string]sqlc.Organization{
		uuidToString(org.ID): org,
	},
	orgsByCode: map[string]sqlc.Organization{
		org.Code: org,
	},
	nextRefreshID: refreshID,
	refreshTokens: map[string]sqlc.RefreshToken{},
}
```

Implement the new store methods:

```go
func (s *authStoreStub) GetUserByUsername(_ context.Context, username string) (sqlc.User, error) {
	user, ok := s.platformByName[username]
	if !ok {
		return sqlc.User{}, pgx.ErrNoRows
	}
	return user, nil
}

func (s *authStoreStub) GetUserByOrgAndUsername(_ context.Context, arg sqlc.GetUserByOrgAndUsernameParams) (sqlc.User, error) {
	user, ok := s.orgUsersByKey[orgUserKey(arg.OrgID, arg.Username)]
	if !ok {
		return sqlc.User{}, pgx.ErrNoRows
	}
	return user, nil
}

func (s *authStoreStub) GetUser(_ context.Context, id pgtype.UUID) (sqlc.User, error) {
	user, ok := s.usersByID[uuidToString(id)]
	if !ok {
		return sqlc.User{}, pgx.ErrNoRows
	}
	return user, nil
}

func (s *authStoreStub) MarkUserLoggedIn(_ context.Context, id pgtype.UUID) (sqlc.User, error) {
	user, ok := s.usersByID[uuidToString(id)]
	if !ok {
		return sqlc.User{}, pgx.ErrNoRows
	}
	s.loggedIn = true
	s.lastIssuedRole = user.Role
	return user, nil
}

func (s *authStoreStub) GetOrganization(_ context.Context, id pgtype.UUID) (sqlc.Organization, error) {
	org, ok := s.orgsByID[uuidToString(id)]
	if !ok {
		return sqlc.Organization{}, pgx.ErrNoRows
	}
	return org, nil
}

func (s *authStoreStub) GetOrganizationByCode(_ context.Context, code string) (sqlc.Organization, error) {
	org, ok := s.orgsByCode[code]
	if !ok {
		return sqlc.Organization{}, pgx.ErrNoRows
	}
	return org, nil
}
```

- [ ] **Step 6: Run auth tests**

Run:

```bash
rtk go test ./internal/service -count=1 -run 'TestAuthService'
```

Expected: PASS.

- [ ] **Step 7: Commit AuthService changes**

```bash
rtk git add internal/service/auth_service.go internal/service/auth_service_test.go
rtk git commit -m "feat(auth): 支持组织标识登录" -m "登录入口按 org_code 分流：空组织标识仅允许平台管理员登录，非空组织标识按组织和短用户名查询组织用户。补充平台管理员、组织用户和错误组织标识的认证测试。"
```

---

### Task 3: Organization Code In Service And HTTP DTOs

**Files:**
- Modify: `internal/service/organization_service.go`
- Modify: `internal/service/organization_service_test.go`
- Modify: `internal/api/handlers/dto.go`
- Modify: `internal/api/handlers/auth.go`
- Modify: `internal/api/handlers/auth_test.go`
- Modify: `internal/api/handlers/organizations.go`
- Modify: `internal/api/handlers/organizations_test.go`

- [ ] **Step 1: Write failing organization service tests**

In `internal/service/organization_service_test.go`, update successful create tests to pass `Code: "test-org"` and assert the store received it:

```go
result, err := svc.CreateOrganization(context.Background(), auth.Principal{Role: domain.UserRolePlatformAdmin}, OrganizationInput{
	Name:                   "测试组织",
	Code:                   "test-org",
	AdminUsername:          "admin",
	AdminDisplayName:       "管理员",
	AdminPassword:          "secret-password",
	CreditWarningThreshold: ptrInt32(20),
})
require.NoError(t, err)
assert.Equal(t, "test-org", result.Code)
assert.Equal(t, "test-org", store.created.Code)
```

Add validation tests:

```go
func TestCreateOrganizationRequiresValidCode(t *testing.T) {
	store := newOrganizationStoreStub(t)
	svc := newTestOrganizationService(store, newAPIProvisionerStub{}, nil)

	for _, code := range []string{"", "ab", "-bad", "bad-", "Bad Org", "bad_org"} {
		_, err := svc.CreateOrganization(context.Background(), auth.Principal{Role: domain.UserRolePlatformAdmin}, OrganizationInput{
			Name:             "测试组织",
			Code:             code,
			AdminUsername:    "admin",
			AdminDisplayName: "管理员",
			AdminPassword:    "secret-password",
		})
		require.ErrorIs(t, err, ErrMemberCreateInvalid, "code=%q", code)
	}
}
```

- [ ] **Step 2: Run tests and verify failure**

Run:

```bash
rtk go test ./internal/service -count=1 -run 'TestOrganizationServiceCreate|TestCreateOrganization'
```

Expected: FAIL because `OrganizationInput.Code`, `OrganizationResult.Code`, and `CreateOrganizationParams.Code` wiring are not implemented.

- [ ] **Step 3: Implement organization code validation and persistence**

Modify `internal/service/organization_service.go`.

Add `regexp` import and a package-level pattern:

```go
var organizationCodePattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{1,30}[a-z0-9])$`)
```

Extend `OrganizationInput`:

```go
// Code 是组织登录标识，创建后不可修改；仅允许小写字母、数字和短横线。
Code string
```

Extend `OrganizationResult`:

```go
// Code 是组织登录标识，用于组织用户登录时定位租户。
Code string `json:"code"`
```

Add helper:

```go
// normalizeOrganizationCode 统一组织标识格式，避免大小写或空白导致同一标识多种写法。
func normalizeOrganizationCode(value string) (string, error) {
	code := strings.ToLower(strings.TrimSpace(value))
	if !organizationCodePattern.MatchString(code) {
		return "", fmt.Errorf("%w: 组织标识必须为 3-32 位小写字母、数字或短横线，且不能以短横线开头或结尾", ErrMemberCreateInvalid)
	}
	return code, nil
}
```

In `CreateOrganization`, validate before hashing:

```go
code, err := normalizeOrganizationCode(input.Code)
if err != nil {
	return OrganizationResult{}, err
}
```

Pass code into sqlc params:

```go
org, err := s.store.CreateOrganization(ctx, sqlc.CreateOrganizationParams{
	Name:                   input.Name,
	Code:                   code,
	Status:                 domain.StatusActive,
	ContactName:            textValue(input.ContactName),
	ContactPhone:           textValue(input.ContactPhone),
	Remark:                 textValue(input.Remark),
	CreditWarningThreshold: int4Ptr(input.CreditWarningThreshold),
})
```

Update `toOrganizationResult` to include code:

```go
Code: org.Code,
```

- [ ] **Step 4: Update DTOs and handler mapping**

Modify `internal/api/handlers/dto.go`:

```go
// LoginRequest 用户名密码登录的请求体。
type LoginRequest struct {
	// OrgCode 是组织用户登录时填写的组织标识；平台管理员登录时留空。
	OrgCode string `json:"org_code"`
	// Username 是 manager 账号名，登录失败时不区分账号不存在和密码错误。
	Username string `json:"username" binding:"required"`
	// Password 是明文登录密码，仅用于本次校验，handler 不写日志。
	Password string `json:"password" binding:"required"`
}
```

Add `Code` to `CreateOrganizationRequest`:

```go
// Code 是组织登录标识，创建后不可修改。
Code string `json:"code" binding:"required"`
```

Modify `internal/api/handlers/auth.go` login mapping:

```go
result, err := h.service.Login(c.Request.Context(), service.LoginInput{
	OrgCode:  req.OrgCode,
	Username: req.Username,
	Password: req.Password,
})
```

Modify `internal/api/handlers/organizations.go` in `toCreateOrganizationInput`:

```go
Code: req.Code,
```

Do not add `Code` to `toOrganizationInput`.

- [ ] **Step 5: Update handler tests**

In `internal/api/handlers/auth_test.go`, update the login request body and assert `OrgCode` was passed through if the stub records it:

```go
request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewBufferString(`{"org_code":"test-org","username":"admin","password":"secret"}`))
```

In `internal/api/handlers/organizations_test.go`, update create request bodies:

```go
`{"name":"测试组织","code":"test-org","admin_username":"admin","admin_display_name":"管理员","admin_password":"secret-password"}`
```

Add or update assertion:

```go
require.Equal(t, "test-org", svc.lastCreateInput.Code)
```

- [ ] **Step 6: Run service and handler tests**

Run:

```bash
rtk go test ./internal/service ./internal/api/handlers -count=1 -run 'TestCreateOrganization|TestOrganization|TestAuth|TestLogin'
```

Expected: PASS.

- [ ] **Step 7: Commit organization service and handler changes**

```bash
rtk git add \
  internal/service/organization_service.go \
  internal/service/organization_service_test.go \
  internal/api/handlers/dto.go \
  internal/api/handlers/auth.go \
  internal/api/handlers/auth_test.go \
  internal/api/handlers/organizations.go \
  internal/api/handlers/organizations_test.go

rtk git commit -m "feat(org): 创建组织时保存登录标识" -m "组织创建请求新增 code 字段，后端校验格式并写入 organizations.code。登录请求同步透传 org_code，组织响应返回登录标识但组织资料更新不允许修改该字段。"
```

---

### Task 4: Member Creation Boundary Tests

**Files:**
- Modify: `internal/service/member_service_test.go`
- Modify: `internal/service/onboarding_service_test.go`

- [ ] **Step 1: Add member service test for duplicate username across organizations**

In `internal/service/member_service_test.go`, add a test that uses a stub with two organizations and creates `admin` in each:

```go
const testAdmin2UID = "00000000-0000-0000-0000-0000000000b3"

func TestCreateMemberAllowsSameUsernameAcrossDifferentOrganizations(t *testing.T) {
	store := newMemberStoreStub(t)
	store.orgs[testOrg2ID] = sqlc.Organization{ID: mustUUID(t, testOrg2ID), Name: "另一个组织", Status: domain.StatusActive}
	svc := NewMemberService(store, fastHash)

	first, err := svc.CreateMember(context.Background(), orgAdminPrincipal(), testOrgID, MemberInput{
		Username:    "admin",
		DisplayName: "组织一管理员",
		Password:    "password-123",
		Role:        domain.UserRoleOrgAdmin,
	})
	require.NoError(t, err)
	require.Equal(t, "admin", first.Username)

	secondPrincipal := auth.Principal{Role: domain.UserRoleOrgAdmin, OrgID: testOrg2ID, UserID: testAdmin2UID}
	second, err := svc.CreateMember(context.Background(), secondPrincipal, testOrg2ID, MemberInput{
		Username:    "admin",
		DisplayName: "组织二管理员",
		Password:    "password-123",
		Role:        domain.UserRoleOrgAdmin,
	})
	require.NoError(t, err)
	require.Equal(t, "admin", second.Username)
}
```

This test intentionally refers to `store.orgs`, which is added in Step 3.

- [ ] **Step 2: Run member test and verify failure**

Run:

```bash
rtk go test ./internal/service -count=1 -run TestCreateMemberAllowsSameUsernameAcrossDifferentOrganizations
```

Expected: FAIL if the stub still models username as globally unique.

- [ ] **Step 3: Update member test stub to model scoped uniqueness**

In `internal/service/member_service_test.go`, update `memberStoreStub` to keep organizations by ID and enforce username uniqueness by `(org_id, username)`.

Change the struct fields:

```go
orgs               map[string]sqlc.Organization
users              map[string]sqlc.User
usersByOrgUsername map[string]sqlc.User
```

Change `newMemberStoreStub`:

```go
org := sqlc.Organization{ID: mustUUID(t, testOrgID), Status: domain.StatusActive, Name: "测试组织"}
return &memberStoreStub{
	t:                  t,
	orgs:               map[string]sqlc.Organization{testOrgID: org},
	users:              map[string]sqlc.User{},
	usersByOrgUsername: map[string]sqlc.User{},
	apps:               map[string]sqlc.App{},
}
```

Change `GetOrganization`:

```go
func (s *memberStoreStub) GetOrganization(_ context.Context, id pgtype.UUID) (sqlc.Organization, error) {
	org, ok := s.orgs[uuidToString(id)]
	if !ok {
		return sqlc.Organization{}, pgx.ErrNoRows
	}
	return org, nil
}
```

Update tests that set `store.org.Status` to mutate the map entry:

```go
org := store.orgs[testOrgID]
org.Status = domain.StatusDisabled
store.orgs[testOrgID] = org
```

In `memberStoreStub.CreateUser`, store created users by scoped key:

```go
key := uuidToString(arg.OrgID) + "/" + arg.Username
if _, exists := s.usersByOrgUsername[key]; exists {
	return sqlc.User{}, errors.New("duplicate username in organization")
}
id := mustUUID(s.t, "00000000-0000-0000-0000-0000000000ff")
id.Bytes[15] = byte(len(s.users) + 1)
user := sqlc.User{
	ID:           id,
	OrgID:        arg.OrgID,
	Username:     arg.Username,
	PasswordHash: arg.PasswordHash,
	DisplayName:  arg.DisplayName,
	Role:         arg.Role,
	Status:       arg.Status,
}
s.usersByOrgUsername[key] = user
s.users[uuidToString(user.ID)] = user
return user, nil
```

Keep `GetUserByUsername` behavior only if an existing test still needs it. If retained, make it search all users and return `pgx.ErrNoRows` when no match exists; do not use it to enforce global uniqueness in member creation tests.

- [ ] **Step 4: Add onboarding service test for short username**

In `internal/service/onboarding_service_test.go`, add an assertion to the successful onboarding test:

```go
assert.Equal(t, "alice", result.Member.Username)
assert.NotContains(t, result.Member.Username, "test-org-")
```

The existing successful onboarding input uses `Username: "alice"`, so this assertion proves onboarding does not prepend organization code.

- [ ] **Step 5: Run member and onboarding tests**

Run:

```bash
rtk go test ./internal/service -count=1 -run 'TestCreateMemberAllowsSameUsernameAcrossDifferentOrganizations|TestOnboardMember'
```

Expected: PASS.

- [ ] **Step 6: Commit member boundary tests**

```bash
rtk git add internal/service/member_service_test.go internal/service/onboarding_service_test.go
rtk git commit -m "test(member): 覆盖跨组织重复短用户名" -m "成员创建和一键开户保持短用户名语义，不再通过全局用户名或组织标识前缀表达租户边界。测试 stub 按 org_id 和 username 建模唯一性。"
```

---

### Task 5: Seed Data And Local Account Documentation

**Files:**
- Modify: `cmd/seed-e2e/main.go`
- Modify: `AGENTS.md`
- Modify: `docs/user-manual.md`

- [ ] **Step 1: Update e2e seed SQL for organization code**

Modify `cmd/seed-e2e/main.go`.

Set fixture org code:

```go
fx.OrgName = "e2e-org"
fx.OrgCode = "test-org"
```

If the `fixture` struct has no `OrgCode`, add:

```go
OrgCode string `json:"org_code"`
```

Update organization insert:

```go
`INSERT INTO organizations (name, code, status) VALUES ($1, $2, 'active') RETURNING id`,
fx.OrgName,
fx.OrgCode,
```

Keep organization usernames short. If fixture currently uses `e2e-org-admin`, leave it unchanged unless e2e tests expect `test-org`; the login request will carry `org_code` separately.

- [ ] **Step 2: Update platform admin seed conflict clause**

In `cmd/seed-e2e/main.go`, `ensurePlatformAdmin` currently uses `ON CONFLICT (username)`. After Task 1 the global constraint is gone. Replace it with an explicit partial-index conflict target:

```sql
ON CONFLICT (username) WHERE org_id IS NULL DO UPDATE
    SET password_hash = EXCLUDED.password_hash,
        role = 'platform_admin',
        status = 'active',
        updated_at = now()
```

`cmd/seed-admin/main.go` uses `ON CONFLICT (username)` too. Modify it the same way:

```sql
ON CONFLICT (username) WHERE org_id IS NULL DO NOTHING
```

If `seed-admin` currently has no conflict clause, leave it unchanged.

- [ ] **Step 3: Update AGENTS local debug accounts**

Modify `AGENTS.md` local account section:

```markdown
- new-api 管理员：`admin` / `admin123`
- manager 平台管理员：组织标识留空，`admin` / `admin123`
- manager 测试组织：组织标识 `test-org`
- manager 测试组织管理员：组织标识 `test-org`，`test-org` / `test-org123`
- manager 组织成员：组织标识 `test-org`，`test-org-user1` / `test-org-user1`
```

- [ ] **Step 4: Update user manual login text**

Modify `docs/user-manual.md` login section so it explains:

```markdown
组织用户登录需要填写组织标识、用户名和密码。平台管理员登录时组织标识留空。
组织名称是展示名称，可以由平台管理员修改；组织标识是登录命名空间，创建后不修改。
```

- [ ] **Step 5: Run seed command tests**

Run:

```bash
rtk go test ./cmd/seed-e2e ./cmd/seed-admin -count=1
```

Expected: PASS. If `cmd/seed-admin` has no tests, Go reports `? ... [no test files]`.

- [ ] **Step 6: Commit seed and docs changes**

```bash
rtk git add cmd/seed-e2e/main.go cmd/seed-admin/main.go AGENTS.md docs/user-manual.md
rtk git commit -m "docs(auth): 更新组织标识登录调试账号" -m "e2e fixture 写入稳定组织标识，并更新本地调试账号和用户手册，明确平台管理员组织标识留空、组织用户通过 org_code 登录。"
```

---

### Task 6: Frontend Login And Organization Management UI

**Files:**
- Modify: `web/src/stores/auth.ts`
- Create: `web/src/stores/auth.spec.ts`
- Modify: `web/src/pages/login/LoginPage.vue`
- Modify: `web/src/api/hooks/useOrganizations.ts`
- Modify: `web/src/pages/platform/OrganizationsPage.vue`
- Modify: `web/src/pages/platform/OrganizationsPage.spec.ts`
- Modify: `web/src/api/hooks/useMembers.ts`

- [ ] **Step 1: Update auth store API**

Modify `web/src/stores/auth.ts`:

```ts
async function login(username: string, password: string, orgCode = ''): Promise<LoginResult> {
  loading.value = true
  error.value = null
  try {
    const result = await apiRequest<LoginResult>('/api/v1/auth/login', {
      method: 'POST',
      body: {
        org_code: orgCode.trim() || undefined,
        username,
        password,
      },
      withAuth: false,
    })
    setStoredTokens({
      accessToken: result.tokens.access_token,
      refreshToken: result.tokens.refresh_token,
    })
    user.value = result.user
    return result
  } catch (err) {
    error.value = err instanceof Error ? err.message : '登录失败'
    throw err
  } finally {
    loading.value = false
  }
}
```

- [ ] **Step 2: Update login page**

Modify `web/src/pages/login/LoginPage.vue`.

Add field before username:

```vue
<n-form-item label="组织标识" path="orgCode">
  <n-input
    v-model:value="orgCode"
    autocomplete="organization"
    :input-props="{ id: 'org-code', 'aria-label': '组织标识' }"
    placeholder="平台管理员可留空"
  />
</n-form-item>
```

Add state:

```ts
const orgCode = ref('')
```

Change submit call:

```ts
await auth.login(username.value, password.value, orgCode.value)
```

- [ ] **Step 3: Add auth store request test**

Create `web/src/stores/auth.spec.ts`:

```ts
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'

import { useAuthStore } from '@/stores/auth'
import { apiRequest, setStoredTokens } from '@/api/client'

vi.mock('@/api/client', () => ({
  apiRequest: vi.fn(),
  clearStoredTokens: vi.fn(),
  getStoredAccessToken: vi.fn(() => ''),
  getStoredRefreshToken: vi.fn(() => ''),
  setStoredTokens: vi.fn(),
}))

describe('auth store login', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.mocked(apiRequest).mockReset()
    vi.mocked(setStoredTokens).mockReset()
  })

  it('sends organization code for organization login', async () => {
    vi.mocked(apiRequest).mockResolvedValue({
      user: {
        id: 'user-1',
        org_id: 'org-1',
        username: 'admin',
        display_name: '组织管理员',
        role: 'org_admin',
        status: 'active',
      },
      tokens: { access_token: 'access', refresh_token: 'refresh' },
    })
    const store = useAuthStore()

    await store.login('admin', 'secret-password', 'test-org')

    expect(apiRequest).toHaveBeenCalledWith('/api/v1/auth/login', {
      method: 'POST',
      body: { org_code: 'test-org', username: 'admin', password: 'secret-password' },
      withAuth: false,
    })
  })

  it('omits organization code for platform login', async () => {
    vi.mocked(apiRequest).mockResolvedValue({
      user: {
        id: 'admin-1',
        username: 'admin',
        display_name: '平台管理员',
        role: 'platform_admin',
        status: 'active',
      },
      tokens: { access_token: 'access', refresh_token: 'refresh' },
    })
    const store = useAuthStore()

    await store.login('admin', 'secret-password')

    expect(apiRequest).toHaveBeenCalledWith('/api/v1/auth/login', {
      method: 'POST',
      body: { org_code: undefined, username: 'admin', password: 'secret-password' },
      withAuth: false,
    })
  })
})
```

- [ ] **Step 4: Update organization payload type**

Modify `web/src/api/hooks/useOrganizations.ts`:

```ts
export interface OrganizationFormPayload {
  // 组织名称。
  name: string
  // 组织登录标识，创建后不可修改。
  code: string
  // 联系人姓名。
  contact_name?: string
```

- [ ] **Step 5: Update organization create form**

Modify `web/src/pages/platform/OrganizationsPage.vue`.

Add form field next to organization name:

```vue
<n-grid-item>
  <n-form-item label="组织标识 *">
    <n-input v-model:value="form.code" placeholder="test-org" />
  </n-form-item>
</n-grid-item>
```

Update initial form:

```ts
initial: {
  name: '',
  code: '',
  contact_name: '',
  contact_phone: '',
  remark: '',
  credit_warning_threshold: undefined as number | undefined,
  admin_username: '',
  admin_display_name: '',
  admin_password: '',
},
```

Update payload:

```ts
toPayload: (f) => ({
  name: f.name,
  code: f.code,
  contact_name: f.contact_name || undefined,
  contact_phone: f.contact_phone || undefined,
  remark: f.remark || undefined,
  credit_warning_threshold: typeof f.credit_warning_threshold === 'number'
    ? f.credit_warning_threshold : undefined,
  admin_username: f.admin_username,
  admin_display_name: f.admin_display_name,
  admin_password: f.admin_password,
}),
```

Add organization code column after name:

```ts
{ title: '组织标识', key: 'code', render: (row: Organization) => row.code || '—' },
```

- [ ] **Step 6: Update member hook comment**

Modify `web/src/api/hooks/useMembers.ts` comment for username:

```ts
// 登录用户名，后端会校验同一组织内唯一性。
username: string
```

Do not prepend org code in member payloads.

- [ ] **Step 7: Update organization page test**

Modify `web/src/pages/platform/OrganizationsPage.spec.ts` so test organization rows include `code`:

```ts
{ id: 'org-1', name: '测试组织', code: 'test-org', status: 'active' }
```

Add an assertion:

```ts
expect(wrapper.text()).toContain('test-org')
```

If the spec currently mocks `useCreateOrganization`, assert the submitted payload includes `code: 'test-org'` after filling the field.

- [ ] **Step 8: Run frontend targeted tests**

Run:

```bash
rtk npm --prefix web run test:unit -- --run OrganizationsPage
rtk npm --prefix web run test:unit -- --run auth
```

Expected: PASS.

- [ ] **Step 9: Commit frontend UI changes**

```bash
rtk git add \
  web/src/stores/auth.ts \
  web/src/stores/auth.spec.ts \
  web/src/pages/login/LoginPage.vue \
  web/src/api/hooks/useOrganizations.ts \
  web/src/pages/platform/OrganizationsPage.vue \
  web/src/pages/platform/OrganizationsPage.spec.ts \
  web/src/api/hooks/useMembers.ts

rtk git commit -m "feat(web): 登录和组织创建支持组织标识" -m "登录页增加组织标识输入，平台管理员可留空；组织创建页新增组织标识字段并在组织列表展示。成员创建继续提交短用户名，不拼接组织前缀。"
```

---

### Task 7: OpenAPI And Generated Types

**Files:**
- Generated: `openapi/openapi.yaml`
- Generated: `web/src/api/generated.ts`

- [ ] **Step 1: Generate OpenAPI**

Run:

```bash
rtk make openapi-gen
```

Expected: PASS and `openapi/openapi.yaml` changes include:

- `handlers.LoginRequest.properties.org_code`
- `handlers.CreateOrganizationRequest.required` includes `code`
- `service.OrganizationResult.properties.code`

- [ ] **Step 2: Generate frontend API types**

Run:

```bash
rtk make web-types-gen
```

Expected: PASS and `web/src/api/generated.ts` includes:

- `handlers.LoginRequest.org_code?: string`
- `handlers.CreateOrganizationRequest.code: string`
- `service.OrganizationResult.code?: string` or `code: string` depending on swag output

- [ ] **Step 3: Run OpenAPI sync check**

Run:

```bash
rtk make openapi-check
```

Expected: PASS.

- [ ] **Step 4: Commit generated contract changes**

```bash
rtk git add openapi/openapi.yaml web/src/api/generated.ts
rtk git commit -m "chore(openapi): 同步组织标识登录契约" -m "根据登录请求、组织创建请求和组织响应字段变化重新生成 OpenAPI 与前端类型，保持后端 DTO 和前端 API 类型一致。"
```

---

### Task 8: Full Verification

**Files:**
- No code changes unless verification exposes a defect in prior tasks.

- [ ] **Step 1: Run backend focused tests**

Run:

```bash
rtk go test ./internal/service ./internal/api/handlers ./cmd/seed-admin ./cmd/seed-e2e -count=1
```

Expected: PASS.

- [ ] **Step 2: Run full backend tests if focused tests pass**

Run:

```bash
rtk go test ./internal/... ./cmd/... -count=1
```

Expected: PASS.

- [ ] **Step 3: Run frontend unit tests**

Run:

```bash
rtk npm --prefix web run test:unit -- --run
```

Expected: PASS.

- [ ] **Step 4: Run frontend typecheck**

Run:

```bash
rtk npm --prefix web run type-check
```

Expected: PASS.

- [ ] **Step 5: Check working tree**

Run:

```bash
rtk git status --short
```

Expected: no uncommitted changes. If generated files or fixes remain, commit them with a focused Conventional Commit message in Chinese.

---

## Self-Review

- Spec coverage: data model, login semantics, organization creation, member creation, seed data, docs, OpenAPI, frontend, and tests all map to tasks above.
- Placeholder scan: no banned placeholders remain.
- Type consistency: `org_code` is the JSON field, `OrgCode` is the Go service field, `code` is the organization JSON/database field, and `organizations.code` is never added to update DTOs.
