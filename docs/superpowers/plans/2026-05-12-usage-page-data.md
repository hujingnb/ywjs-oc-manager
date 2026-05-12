# 用量统计页面数据修复 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 修复用量统计页面在平台管理员、组织管理员和组织成员身份下没有数据展示的问题，并通过浏览器验证权限裁剪。

**Architecture:** 保持现有 Vue + TanStack Query 前端和 Go `UsageService` 薄代理架构。前端修复角色相关查询启用条件，后端为组织/平台用量提供默认时间窗口，测试覆盖成员自查和时间窗口参数。

**Tech Stack:** Go 1.25、Gin、testify、Vue 3、Pinia、TanStack Query、Vitest、Playwright/Chrome DevTools。

---

## File Structure

- Modify: `web/src/pages/usage/UsagePage.vue`
  - 负责按角色计算可查询组织 ID，确保成员身份会发起“我的用量”请求。
- Modify: `internal/api/handlers/usage.go`
  - 负责解析 usage 时间窗口，并在组织/平台统计未传时间时补最近 30 天默认窗口。
- Modify: `internal/api/handlers/usage_test.go`
  - 覆盖 handler 传给 service 的默认时间窗口和成员请求参数。
- Optional Modify: `web/src/pages/usage/UsagePage.test.ts`
  - 如果现有测试环境能快速挂载页面，则覆盖成员身份查询启用行为。

## Task 1: Browser Reproduction

**Files:**
- Read only: browser state and Network/Console

- [ ] **Step 1: Start or verify local services**

Run:

```bash
rtk docker compose ps
```

Expected: manager API, web, database and new-api containers are running. If they are not running, start them:

```bash
rtk docker compose up -d
```

- [ ] **Step 2: Open the usage page as platform admin**

Use browser tools to open the app, log in with empty org code, `admin` / `admin123`, and navigate to `/usage`.

Expected: record visible tabs, empty/data state, Console errors, and Network status for `/api/v1/usage/platform` and `/api/v1/usage/organizations/:orgId`.

- [ ] **Step 3: Open the usage page as org admin**

Log out, then log in with org code `test-org`, username `test-org`, password `test-org123`, and navigate to `/usage`.

Expected: platform tab is hidden; organization/member/app tabs are visible; record usage request status and whether data appears.

- [ ] **Step 4: Open the usage page as org member**

Log out, then log in with org code `test-org`, username `test-org-user1`, password `test-org-user1`, and navigate to `/usage`.

Expected: only “我的用量” and app entry are visible; member usage request should be checked in Network. Before the fix, this request may be absent because `orgRef` is undefined.

## Task 2: Backend Default Window Test

**Files:**
- Modify: `internal/api/handlers/usage_test.go`

- [ ] **Step 1: Add assertions to the usage service stub**

Add fields to `usageServiceStub`:

```go
lastMemberOrgID string
lastMemberUserID string
lastOrgSince int64
lastOrgUntil int64
lastPlatformSince int64
lastPlatformUntil int64
```

Update stub methods to capture arguments:

```go
func (s *usageServiceStub) GetMemberUsage(_ context.Context, _ auth.Principal, orgID, userID string, _ service.LogsQueryOptions) (service.LogsPage, error) {
	s.lastMemberOrgID = orgID
	s.lastMemberUserID = userID
	return s.memberResult, s.memberErr
}

func (s *usageServiceStub) GetOrgUsage(_ context.Context, _ auth.Principal, _ string, since, until int64) (service.QuotaSeries, error) {
	s.lastOrgSince = since
	s.lastOrgUntil = until
	return s.orgResult, s.orgErr
}

func (s *usageServiceStub) GetPlatformUsage(_ context.Context, _ auth.Principal, since, until int64) (service.QuotaSeries, error) {
	s.lastPlatformSince = since
	s.lastPlatformUntil = until
	return s.platformResult, s.platformErr
}
```

- [ ] **Step 2: Add default window tests**

Add tests:

```go
func TestUsageGetOrgAppliesDefaultWindow(t *testing.T) {
	stub := &usageServiceStub{orgResult: service.QuotaSeries{}}
	router, tokens := newUsageTestRouter(t, stub)
	token := mustToken(t, tokens, auth.Principal{UserID: "u1", Role: domain.UserRolePlatformAdmin})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/usage/organizations/org-1", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	require.Greater(t, stub.lastOrgSince, int64(0))
	require.Greater(t, stub.lastOrgUntil, stub.lastOrgSince)
	require.InDelta(t, int64(30*24*60*60), stub.lastOrgUntil-stub.lastOrgSince, 5)
}

func TestUsageGetPlatformAppliesDefaultWindow(t *testing.T) {
	stub := &usageServiceStub{platformResult: service.QuotaSeries{}}
	router, tokens := newUsageTestRouter(t, stub)
	token := mustToken(t, tokens, auth.Principal{UserID: "u1", Role: domain.UserRolePlatformAdmin})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/usage/platform", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	require.Greater(t, stub.lastPlatformSince, int64(0))
	require.Greater(t, stub.lastPlatformUntil, stub.lastPlatformSince)
	require.InDelta(t, int64(30*24*60*60), stub.lastPlatformUntil-stub.lastPlatformSince, 5)
}
```

- [ ] **Step 3: Run tests and confirm failure**

Run:

```bash
rtk go test ./internal/api/handlers -run 'TestUsageGet(Org|Platform)AppliesDefaultWindow'
```

Expected: FAIL because `parseTimeWindow` currently returns `0,0`.

## Task 3: Backend Default Window Implementation

**Files:**
- Modify: `internal/api/handlers/usage.go`

- [ ] **Step 1: Add the default window helper**

Add a constant and helper near `parseTimeWindow`:

```go
const usageDefaultWindowSeconds int64 = 30 * 24 * 60 * 60

// parseUsageStatsWindow 解析组织 / 平台统计时间窗；未显式传参时默认查最近 30 天，
// 避免上游 new-api 在空时间窗语义下返回空统计。
func parseUsageStatsWindow(c *gin.Context) (int64, int64) {
	since, until := parseTimeWindow(c)
	if since > 0 || until > 0 {
		return since, until
	}
	now := time.Now().Unix()
	return now - usageDefaultWindowSeconds, now
}
```

Add `time` to imports.

- [ ] **Step 2: Use the helper for org and platform handlers**

Change `GetOrg` and `GetPlatform` from:

```go
since, until := parseTimeWindow(c)
```

to:

```go
since, until := parseUsageStatsWindow(c)
```

- [ ] **Step 3: Run backend handler tests**

Run:

```bash
rtk go test ./internal/api/handlers -run 'TestUsage'
```

Expected: PASS.

## Task 4: Frontend Member Query Fix

**Files:**
- Modify: `web/src/pages/usage/UsagePage.vue`

- [ ] **Step 1: Split organization statistics org ID from member query org ID**

Replace:

```ts
const orgRef = computed(() => (isOrgMember.value ? undefined : effectiveOrgId.value))
const { data: orgView, isLoading: orgLoading, error: orgError } = useOrgUsageQuery(orgRef)
```

with:

```ts
const orgUsageRef = computed(() => (isOrgMember.value ? undefined : effectiveOrgId.value))
const memberOrgRef = computed(() => effectiveOrgId.value)
const { data: orgView, isLoading: orgLoading, error: orgError } = useOrgUsageQuery(orgUsageRef)
```

Then replace the member query call:

```ts
const { data: memberView, isLoading: memberLoading, error: memberError } = useMemberUsageQuery(orgRef, memberRef)
```

with:

```ts
const { data: memberView, isLoading: memberLoading, error: memberError } = useMemberUsageQuery(memberOrgRef, memberRef)
```

- [ ] **Step 2: Run frontend typecheck**

Run:

```bash
cd web && rtk npm run typecheck
```

Expected: PASS.

## Task 5: Verification

**Files:**
- Read: browser state, Network, Console

- [ ] **Step 1: Run focused backend tests**

Run:

```bash
rtk go test ./internal/api/handlers ./internal/service -run 'TestUsage'
```

Expected: PASS.

- [ ] **Step 2: Run frontend build or typecheck**

Run:

```bash
cd web && rtk npm run typecheck
```

Expected: PASS.

- [ ] **Step 3: Browser verify platform admin**

Log in with empty org code, `admin` / `admin123`, navigate to `/usage`.

Expected: platform tab visible; organization/member/app entries available; Network usage requests do not fail with unexpected 400/403/500.

- [ ] **Step 4: Browser verify org admin**

Log in with org code `test-org`, `test-org` / `test-org123`, navigate to `/usage`.

Expected: platform tab hidden; organization/member/app entries available; organization request uses a non-zero default time window.

- [ ] **Step 5: Browser verify org member**

Log in with org code `test-org`, `test-org-user1` / `test-org-user1`, navigate to `/usage`.

Expected: “我的用量” is visible; organization/platform tabs are hidden; member usage request is sent with `org_id=<current org id>` and does not fail because of a missing `org_id`.
