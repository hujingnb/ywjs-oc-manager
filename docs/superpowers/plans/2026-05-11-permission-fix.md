# Manage Permission Fix Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 修复 manage 三类角色在成员、应用、用量、审计和知识库上的权限边界，并完成自动测试与浏览器验收。

**Architecture:** 后端继续以 `internal/auth/authorizer.go` 为唯一权限谓词层，service 在读取资源归属后调用谓词判定。前端只做入口、按钮和空状态收敛，所有安全边界以后端 403/404 为准。

**Tech Stack:** Go service + Gin handler + sqlc store, Vue 3 + TypeScript + Naive UI + TanStack Query, Go `testify`, Vitest, OpenAPI/swag 生成链路。

---

## File Structure

- Modify `internal/auth/authorizer.go`: 增加应用审计目标读取谓词，保持权限判断集中。
- Modify `internal/auth/authorizer_test.go`: 覆盖组织成员只能查看自己应用审计、平台/组织管理员可读目标审计。
- Modify `internal/service/audit_service.go`: 扩展 store 依赖以读取 app 归属，允许组织成员按自己应用查看 target 审计。
- Modify `internal/service/audit_service_test.go`: 用失败用例驱动审计成员视角和跨应用拒绝。
- Modify `internal/api/handlers/audit.go`: 更新 OpenAPI 注释，说明 target 审计支持成员自己的 app。
- Modify frontend app pages: `web/src/pages/apps/AppsPage.vue`, `AppOverviewTab.vue`, `AppRuntimeTab.vue`, `AppChannelsTab.vue`, `AppKnowledgeTab.vue`, `OrgKnowledgePage.vue`, `AuditLogsPage.vue`, `DashboardLayout.vue`, `router.ts` as needed.
- Potentially create `web/src/domain/permissions.ts`: 复用前端角色判断，避免每个页面重复散写。
- Generated if handler annotation changes: `openapi/openapi.yaml`, `web/src/api/generated.ts`.

## Task 1: 后端审计权限测试

**Files:**
- Modify: `internal/auth/authorizer_test.go`
- Modify: `internal/service/audit_service_test.go`

- [ ] **Step 1: Add failing authorizer test**

Add test cases for a new app target audit predicate:

```go
func TestCanViewAppAudit(t *testing.T) {
	cases := []memberCase{
		{"platform_admin 可看任意应用审计", domain.UserRolePlatformAdmin, orgA, userA, orgB, userB, true},
		{"org_admin 可看本组织应用审计", domain.UserRoleOrgAdmin, orgA, userA, orgA, userB, true},
		{"org_admin 不可看跨组织应用审计", domain.UserRoleOrgAdmin, orgA, userA, orgB, userB, false},
		{"org_member 仅可看自己应用审计", domain.UserRoleOrgMember, orgA, userA, orgA, userA, true},
		{"org_member 不可看同组织他人应用审计", domain.UserRoleOrgMember, orgA, userA, orgA, userB, false},
	}
	runAppCases(t, CanViewAppAudit, cases)
}
```

- [ ] **Step 2: Run authorizer test and verify RED**

Run: `rtk go test ./internal/auth -run TestCanViewAppAudit -count=1`

Expected: FAIL because `CanViewAppAudit` is undefined.

- [ ] **Step 3: Add failing audit service tests**

Add tests that call `ListByTarget` as `org_member` for own app and other app. Use an `auditStoreStub` that implements `GetApp`.

```go
func TestAuditServiceListByTargetAllowsMemberOwnApp(t *testing.T) {
	store := &auditStoreStub{
		apps: map[string]sqlc.App{
			testAppID: {ID: mustUUID(t, testAppID), OrgID: mustUUID(t, testOrgID), OwnerUserID: mustUUID(t, testUserID)},
		},
		byTarget: []sqlc.AuditLog{
			{TargetType: "app", TargetID: testAppID, OrgID: mustOptionalUUID(t, testOrgID)},
		},
	}
	svc := NewAuditService(store)

	results, err := svc.ListByTarget(context.Background(), auth.Principal{
		UserID: testUserID,
		OrgID:  testOrgID,
		Role:   domain.UserRoleOrgMember,
	}, "app", testAppID, 0, 0)

	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, testAppID, results[0].TargetID)
}
```

```go
func TestAuditServiceListByTargetRejectsMemberOtherApp(t *testing.T) {
	store := &auditStoreStub{
		apps: map[string]sqlc.App{
			testAppID: {ID: mustUUID(t, testAppID), OrgID: mustUUID(t, testOrgID), OwnerUserID: mustUUID(t, testUser2ID)},
		},
	}
	svc := NewAuditService(store)

	_, err := svc.ListByTarget(context.Background(), auth.Principal{
		UserID: testUserID,
		OrgID:  testOrgID,
		Role:   domain.UserRoleOrgMember,
	}, "app", testAppID, 0, 0)

	require.ErrorIs(t, err, ErrForbidden)
}
```

- [ ] **Step 4: Run audit tests and verify RED**

Run: `rtk go test ./internal/service -run 'TestAuditServiceListByTarget(AllowsMemberOwnApp|RejectsMemberOtherApp)' -count=1`

Expected: FAIL because `auditStoreStub` lacks new fields/methods or service rejects org_member.

## Task 2: 后端审计权限实现

**Files:**
- Modify: `internal/auth/authorizer.go`
- Modify: `internal/service/audit_service.go`
- Modify: `internal/service/audit_service_test.go`
- Modify: `internal/api/handlers/audit.go`

- [ ] **Step 1: Implement authorizer predicate**

Add:

```go
// CanViewAppAudit 判断主体是否可查看指定应用的审计记录。
// 审计读取是观察能力：平台管理员可跨组织查看，组织管理员可查看本组织应用，
// 组织成员只能查看自己拥有的应用，不能通过 target 审计窥探同组织其他成员。
func CanViewAppAudit(p Principal, appOrgID, appOwnerUserID string) bool {
	return CanViewApp(p, appOrgID, appOwnerUserID)
}
```

- [ ] **Step 2: Extend AuditStore**

Change `AuditStore` to include:

```go
GetApp(ctx context.Context, id pgtype.UUID) (sqlc.App, error)
```

- [ ] **Step 3: Gate ListByTarget by target ownership**

For `targetType == "app"`, parse `targetID`, load app, call `auth.CanViewAppAudit`. Return `ErrNotFound` for invalid/missing app and `ErrForbidden` for permission mismatch. For non-app targets, keep platform/org-admin behavior and continue rejecting members.

- [ ] **Step 4: Update auditStoreStub**

Add fields:

```go
apps map[string]sqlc.App
```

Add method:

```go
func (s *auditStoreStub) GetApp(_ context.Context, id pgtype.UUID) (sqlc.App, error) {
	app, ok := s.apps[uuidToString(id)]
	if !ok {
		return sqlc.App{}, pgx.ErrNoRows
	}
	return app, nil
}
```

- [ ] **Step 5: Run backend audit tests and verify GREEN**

Run: `rtk go test ./internal/auth ./internal/service -run 'TestCanViewAppAudit|TestAuditServiceListByTarget' -count=1`

Expected: PASS.

- [ ] **Step 6: Update handler docs**

Update `/audit-logs` description to say platform/admin can query allowed target audit and org members can query their own app target audit. If annotation changes generated YAML, run generation in Task 5.

## Task 3: 前端权限工具和 UI 收敛测试

**Files:**
- Create: `web/src/domain/permissions.ts`
- Create or modify: `web/src/domain/permissions.test.ts`

- [ ] **Step 1: Add failing frontend permission tests**

Create tests:

```ts
import { describe, expect, it } from 'vitest'

import {
  canManageApp,
  canManageOrgKnowledge,
  canViewOrgAudit,
  canViewOwnAppAudit,
  canCreateAppForOrg,
} from './permissions'

describe('role permissions', () => {
  it('keeps platform admin read-only for organization-side app and knowledge writes', () => {
    const user = { id: 'platform-user', role: 'platform_admin' as const }
    const app = { org_id: 'org-1', owner_user_id: 'member-1' }

    expect(canCreateAppForOrg(user, 'org-1')).toBe(false)
    expect(canManageOrgKnowledge(user, 'org-1')).toBe(false)
    expect(canManageApp(user, app)).toBe(false)
    expect(canViewOrgAudit(user, 'org-1')).toBe(true)
  })

  it('allows org members to view only their own app audit', () => {
    const user = { id: 'member-1', org_id: 'org-1', role: 'org_member' as const }

    expect(canViewOwnAppAudit(user, { org_id: 'org-1', owner_user_id: 'member-1' })).toBe(true)
    expect(canViewOwnAppAudit(user, { org_id: 'org-1', owner_user_id: 'member-2' })).toBe(false)
    expect(canViewOrgAudit(user, 'org-1')).toBe(false)
  })
})
```

- [ ] **Step 2: Run frontend test and verify RED**

Run: `rtk npm --prefix web test -- permissions.test.ts --run`

Expected: FAIL because `permissions.ts` does not exist.

- [ ] **Step 3: Implement permissions.ts**

Export minimal role helpers using structural types, not generated API types:

```ts
export type Role = 'platform_admin' | 'org_admin' | 'org_member'

export interface PermissionUser {
  id?: string
  org_id?: string
  role?: Role | string
}

export interface PermissionApp {
  org_id?: string
  owner_user_id?: string
}

export function canCreateAppForOrg(user: PermissionUser | null | undefined, orgId?: string): boolean {
  return user?.role === 'org_admin' && Boolean(orgId) && user.org_id === orgId
}

export function canManageOrgKnowledge(user: PermissionUser | null | undefined, orgId?: string): boolean {
  return user?.role === 'org_admin' && Boolean(orgId) && user.org_id === orgId
}

export function canManageApp(user: PermissionUser | null | undefined, app: PermissionApp | null | undefined): boolean {
  if (!user || !app) return false
  if (user.role === 'org_admin') return user.org_id === app.org_id
  if (user.role === 'org_member') return user.id === app.owner_user_id
  return false
}

export function canViewOrgAudit(user: PermissionUser | null | undefined, orgId?: string): boolean {
  if (!user || !orgId) return false
  return user.role === 'platform_admin' || (user.role === 'org_admin' && user.org_id === orgId)
}

export function canViewOwnAppAudit(user: PermissionUser | null | undefined, app: PermissionApp | null | undefined): boolean {
  if (!user || !app) return false
  if (user.role === 'platform_admin') return true
  if (user.role === 'org_admin') return user.org_id === app.org_id
  if (user.role === 'org_member') return user.id === app.owner_user_id
  return false
}
```

- [ ] **Step 4: Run frontend permission tests and verify GREEN**

Run: `rtk npm --prefix web test -- permissions.test.ts --run`

Expected: PASS.

## Task 4: 前端页面接入权限工具

**Files:**
- Modify: `web/src/pages/apps/AppsPage.vue`
- Modify: `web/src/pages/apps/AppOverviewTab.vue`
- Modify: `web/src/pages/apps/AppRuntimeTab.vue`
- Modify: `web/src/pages/apps/AppChannelsTab.vue`
- Modify: `web/src/pages/apps/AppKnowledgeTab.vue`
- Modify: `web/src/pages/knowledge/OrgKnowledgePage.vue`
- Modify: `web/src/pages/audit/AuditLogsPage.vue`
- Modify: `web/src/layouts/DashboardLayout.vue`
- Modify: `web/src/app/router.ts`
- Modify: `web/src/api/hooks/useAuditLogs.ts` if app target audit hook is missing.

- [ ] **Step 1: Apps list write actions**

Use `canCreateAppForOrg` to show “创建成员并初始化” only to org admins. Use `canManageApp` to show runtime action column only for apps the user can manage.

- [ ] **Step 2: App overview write actions**

Use `canManageApp(auth.user, app?.value)` for init retry and API key toggle. Platform admin should see data but not write buttons.

- [ ] **Step 3: Runtime and channel write actions**

Use `canManageApp(auth.user, app?.value)` in `AppRuntimeTab.vue` and `AppChannelsTab.vue`. Disable or hide start/stop/restart/delete/login/unbind when false.

- [ ] **Step 4: Knowledge write actions**

Use `canManageOrgKnowledge` for organization knowledge upload/delete/sync cards. Use `canManageApp` for app knowledge upload/delete. Platform admin can read lists but cannot mutate.

- [ ] **Step 5: Member audit view**

Expose member-safe app audit through `/audit-logs?target_type=app&target_id=<appId>` on app detail or audit page. Do not give org members access to `/organizations/:orgId/audit-logs`.

- [ ] **Step 6: Router and menu alignment**

Keep `/audit-logs` route restricted to platform/admin for organization audit. Add app audit access inside app detail rather than making org audit page available to members.

- [ ] **Step 7: Run frontend tests**

Run: `rtk npm --prefix web test -- --run`

Expected: PASS.

## Task 5: Generated contracts and backend verification

**Files:**
- Potentially modify generated: `openapi/openapi.yaml`, `web/src/api/generated.ts`

- [ ] **Step 1: Run Go focused tests**

Run: `rtk go test ./internal/auth ./internal/service ./internal/api/handlers -count=1`

Expected: PASS.

- [ ] **Step 2: Run OpenAPI generation if handler annotations changed**

Run: `rtk make openapi-gen`

Expected: command succeeds.

Run: `rtk make web-types-gen`

Expected: command succeeds.

- [ ] **Step 3: Run OpenAPI check**

Run: `rtk make openapi-check`

Expected: PASS or no generated drift after committed/generated files are included.

- [ ] **Step 4: Run frontend build/test**

Run: `rtk npm --prefix web test -- --run`

Expected: PASS.

Run: `rtk npm --prefix web run build`

Expected: PASS.

## Task 6: Browser validation and fix loop

**Files:**
- Modify any files identified by browser validation failures, staying within this permission scope.

- [ ] **Step 1: Start local app using existing project scripts**

Inspect README/Makefile for dev commands. Start backend/frontend as needed without destructive commands.

- [ ] **Step 2: Validate platform admin**

Login with `admin` / `admin123`. Confirm platform admin can view organizations, members, apps, usage, audit, runtime nodes. Confirm it cannot create app/member onboarding from app list, upload/delete org/app knowledge, trigger runtime, channel auth, API key toggle.

- [ ] **Step 3: Validate org admin**

Login with `test-org` / `test-org123`. Confirm org admin can view/manage current org members, apps, usage, audit, org knowledge.

- [ ] **Step 4: Validate org member**

Login with `test-org-user1` / `test-org-user1`. Confirm org member can view own app, own usage, own app audit, and cannot view members or organization audit.

- [ ] **Step 5: Fix and repeat**

If any browser step fails, add or update a focused test where feasible, fix the issue, rerun relevant automated tests, and repeat browser validation.

## Task 7: Final review

**Files:**
- All changed files.

- [ ] **Step 1: Check diff scope**

Run: `rtk git status --short`

Expected: only files directly related to permission fix, plan/spec, generated contracts if needed.

- [ ] **Step 2: Check diff**

Run: `rtk git diff --stat && rtk git diff --check`

Expected: no whitespace errors; diff aligns with design.

- [ ] **Step 3: Report verification**

Final response must list automated test commands run, browser roles validated, and any known limitation.
