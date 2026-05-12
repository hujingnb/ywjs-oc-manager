# 用量页面体验优化 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 优化“用量”页面筛选、汇总、折线图和表格体验，并让组织充值展示沿用 new-api 金额 / 额度配置。

**Architecture:** manager 继续作为 new-api 薄代理，不维护 token 单价或本地用量缓存。后端补一个 new-api 状态展示配置接口，前端基于当前筛选返回的 usage items 计算总量、折线图和稳定表格列。

**Tech Stack:** Go 1.25、Gin、testify、Vue 3、Naive UI、TanStack Query、Vitest、Playwright/浏览器验证。

---

## File Structure

- Modify: `internal/integrations/newapi/client.go`
  - 增加 `StatusView` / `GetStatusView`，复用 `/api/status` 返回的展示字段。
- Modify: `internal/service/recharge_service.go`
  - 扩展 `NewAPIRechargeClient`，增加 `GetStatusView`；新增 `GetBillingStatus` 服务方法。
- Modify: `internal/api/handlers/recharge.go`
  - 新增 `/api/v1/billing/status` 路由，前端统一获取 new-api 显示配置。
- Modify: `internal/api/handlers/dto.go`
  - 如需 OpenAPI 注解，补充请求 / 响应注释说明。
- Modify: `internal/api/handlers/recharge_test.go`
  - 覆盖 billing status 路由。
- Modify: `internal/service/recharge_service_test.go`
  - 覆盖 `GetBillingStatus` 透传 new-api 状态配置。
- Modify: `openapi/openapi.yaml`
  - 通过 `make openapi-gen` 生成。
- Modify: `web/src/api/generated.ts`
  - 通过 `make web-types-gen` 生成。
- Modify: `web/src/api/hooks/useRecharge.ts`
  - 增加 `BillingStatusDTO` 与 `useBillingStatusQuery`。
- Create: `web/src/pages/usage/usageMetrics.ts`
  - 纯函数：日期归一化、模型名兜底、Token / quota / count 汇总、折线图点聚合。
- Create: `web/src/pages/usage/usageFormatting.ts`
  - 纯函数：按 new-api 状态配置格式化 quota / 金额 / 额度。
- Create: `web/src/pages/usage/UsageTrendChart.vue`
  - 轻量 SVG 折线图，仅接收聚合点，不直接依赖 API 结构。
- Modify: `web/src/pages/usage/UsageSummary.vue`
  - 从动态列改为稳定列；新增 summary cards 与 chart 插槽或 props。
- Modify: `web/src/pages/usage/UsagePage.vue`
  - 重组为统一筛选栏 + 当前视角数据；成员 / 应用用可搜索 `n-select`。
- Modify: `web/src/pages/platform/OrganizationsPage.vue`
  - 充值弹框文案和余额格式化改成 new-api 口径。
- Create/Modify tests:
  - `web/src/pages/usage/__tests__/usageMetrics.spec.ts`
  - `web/src/pages/usage/__tests__/usageFormatting.spec.ts`
  - `web/src/pages/usage/__tests__/UsagePage.spec.ts`
  - `web/src/pages/platform/OrganizationsPage.spec.ts`

## Task 1: Backend Billing Status

**Files:**
- Modify: `internal/integrations/newapi/client.go`
- Modify: `internal/service/recharge_service.go`
- Modify: `internal/api/handlers/recharge.go`
- Test: `internal/service/recharge_service_test.go`
- Test: `internal/api/handlers/recharge_test.go`

- [ ] **Step 1: Add failing service test**

Add to `internal/service/recharge_service_test.go`:

```go
func TestRechargeServiceGetBillingStatusProxiesNewAPIStatus(t *testing.T) {
	client := &fakeNewAPIRecharge{statusResult: newapi.StatusView{
		QuotaPerUnit:               500000,
		QuotaDisplayType:           "USD",
		DisplayInCurrency:          true,
		CustomCurrencySymbol:       "¤",
		CustomCurrencyExchangeRate: 1,
		USDExchangeRate:            7.3,
		Price:                      7.3,
	}}
	svc := NewRechargeService(newRechargeStub(t, "4"), client)

	view, err := svc.GetBillingStatus(context.Background(), platformAdmin())

	require.NoError(t, err)
	require.Equal(t, int64(500000), view.QuotaPerUnit)
	require.Equal(t, "USD", view.QuotaDisplayType)
	require.True(t, view.DisplayInCurrency)
}
```

Extend `fakeNewAPIRecharge`:

```go
statusResult newapi.StatusView
statusErr    error

func (f *fakeNewAPIRecharge) GetStatusView(_ context.Context) (newapi.StatusView, error) {
	if f.statusErr != nil {
		return newapi.StatusView{}, f.statusErr
	}
	return f.statusResult, nil
}
```

- [ ] **Step 2: Add failing handler test**

Add to `internal/api/handlers/recharge_test.go`:

```go
func TestBillingStatusHappy(t *testing.T) {
	stub := &rechargeServiceStub{billingStatusResult: service.BillingStatusView{
		QuotaPerUnit:      500000,
		QuotaDisplayType:  "USD",
		DisplayInCurrency: true,
	}}
	router, tokens := newRechargeTestRouter(t, stub)
	token := mustSignAccess(t, tokens, auth.Principal{UserID: "u1", Role: domain.UserRolePlatformAdmin})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/billing/status", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "quota_per_unit")
}
```

Extend `rechargeServiceStub`:

```go
billingStatusResult service.BillingStatusView
billingStatusErr    error

func (s *rechargeServiceStub) GetBillingStatus(_ context.Context, _ auth.Principal) (service.BillingStatusView, error) {
	return s.billingStatusResult, s.billingStatusErr
}
```

- [ ] **Step 3: Run tests and confirm failure**

Run:

```bash
rtk go test ./internal/service ./internal/api/handlers -run 'TestRechargeServiceGetBillingStatus|TestBillingStatusHappy'
```

Expected: FAIL because `StatusView`, `GetBillingStatus`, and route do not exist yet.

- [ ] **Step 4: Implement new-api status view**

Add to `internal/integrations/newapi/client.go` near `GetQuotaPerUnit`:

```go
// StatusView 描述 manager 前端展示 new-api 金额 / 额度所需的状态配置。
type StatusView struct {
	QuotaPerUnit               int64   `json:"quota_per_unit"`
	QuotaDisplayType           string  `json:"quota_display_type"`
	DisplayInCurrency          bool    `json:"display_in_currency"`
	CustomCurrencySymbol       string  `json:"custom_currency_symbol"`
	CustomCurrencyExchangeRate float64 `json:"custom_currency_exchange_rate"`
	USDExchangeRate            float64 `json:"usd_exchange_rate"`
	Price                      float64 `json:"price"`
}

// GetStatusView 读取 new-api 展示配置；manager 只透传展示字段，不在本地维护单价。
func (c *Client) GetStatusView(ctx context.Context) (StatusView, error) {
	var response struct {
		Success bool       `json:"success"`
		Message string     `json:"message"`
		Data    StatusView `json:"data"`
	}
	if err := c.do(ctx, http.MethodGet, "/api/status", nil, &response); err != nil {
		return StatusView{}, err
	}
	if !response.Success {
		return StatusView{}, fmt.Errorf("%w: %s", ErrUpstream, response.Message)
	}
	if response.Data.QuotaPerUnit <= 0 {
		return StatusView{}, fmt.Errorf("%w: quota_per_unit 必须为正", ErrPayloadInvalid)
	}
	return response.Data, nil
}
```

Change `GetQuotaPerUnit` to call `GetStatusView` and return `status.QuotaPerUnit`.

- [ ] **Step 5: Implement service and handler**

In `internal/service/recharge_service.go`, update `NewAPIRechargeClient`:

```go
GetStatusView(ctx context.Context) (newapi.StatusView, error)
```

Add:

```go
// BillingStatusView 是 manager 前端格式化余额 / 用量所需的 new-api 展示配置。
type BillingStatusView struct {
	QuotaPerUnit               int64   `json:"quota_per_unit"`
	QuotaDisplayType           string  `json:"quota_display_type"`
	DisplayInCurrency          bool    `json:"display_in_currency"`
	CustomCurrencySymbol       string  `json:"custom_currency_symbol"`
	CustomCurrencyExchangeRate float64 `json:"custom_currency_exchange_rate"`
	USDExchangeRate            float64 `json:"usd_exchange_rate"`
	Price                      float64 `json:"price"`
}

// GetBillingStatus 透传 new-api 展示配置；manager 不维护 token 单价。
func (s *RechargeService) GetBillingStatus(ctx context.Context, principal auth.Principal) (BillingStatusView, error) {
	if principal.Role != domain.UserRolePlatformAdmin && principal.Role != domain.UserRoleOrgAdmin && principal.Role != domain.UserRoleOrgMember {
		return BillingStatusView{}, ErrForbidden
	}
	status, err := s.client.GetStatusView(ctx)
	if err != nil {
		return BillingStatusView{}, fmt.Errorf("查询 new-api 展示配置失败: %w", err)
	}
	return BillingStatusView(status), nil
}
```

In `internal/api/handlers/recharge.go`, extend `rechargeService`, register route:

```go
router.GET("/api/v1/billing/status", handler.BillingStatus)
```

Add `BillingStatus` handler returning `gin.H{"billing_status": view}`.

- [ ] **Step 6: Run backend tests**

Run:

```bash
rtk go test ./internal/service ./internal/api/handlers -run 'TestRechargeServiceGetBillingStatus|TestBillingStatusHappy|TestRecharge'
```

Expected: PASS.

## Task 2: Frontend Usage Utilities

**Files:**
- Create: `web/src/pages/usage/usageMetrics.ts`
- Create: `web/src/pages/usage/usageFormatting.ts`
- Test: `web/src/pages/usage/__tests__/usageMetrics.spec.ts`
- Test: `web/src/pages/usage/__tests__/usageFormatting.spec.ts`

- [ ] **Step 1: Add utility tests**

Create `usageMetrics.spec.ts` with tests for:

```ts
expect(normalizeModelName('')).toBe('未知模型')
expect(normalizeUsageDate({ created_at: 1778562000 })).toMatch(/2026/)
expect(summarizeUsage({ scope: 'platform', items: [{ token_used: 10, quota: 5, count: 2, model_name: '' }] as any, updated_at: '2026-05-12T00:00:00Z' })).toMatchObject({
  totalTokens: 10,
  totalQuota: 5,
  totalCount: 2,
  modelCount: 1,
})
expect(summarizeUsage({ scope: 'member', items: [{ prompt_tokens: 3, completion_tokens: 7, quota: 4, model_name: 'm' }] as any, total: 1, updated_at: '2026-05-12T00:00:00Z' })).toMatchObject({
  totalTokens: 10,
  totalQuota: 4,
  totalCount: 1,
})
```

Create `usageFormatting.spec.ts` with tests for:

```ts
const status = { quota_per_unit: 500000, quota_display_type: 'USD', display_in_currency: true, custom_currency_symbol: '¤', custom_currency_exchange_rate: 1, usd_exchange_rate: 7.3, price: 7.3 }
expect(formatQuotaValue(500000, status)).toContain('USD')
expect(formatQuotaValue(500000, undefined)).toContain('500,000')
```

- [ ] **Step 2: Run tests and confirm failure**

Run:

```bash
cd web && rtk npm test -- --run src/pages/usage/__tests__/usageMetrics.spec.ts src/pages/usage/__tests__/usageFormatting.spec.ts
```

Expected: FAIL because utility files do not exist yet.

- [ ] **Step 3: Implement utilities**

`usageMetrics.ts` exports:

```ts
export function normalizeModelName(value: unknown): string
export function normalizeUsageDate(row: Record<string, unknown>): string
export function getRowTokens(scope: AggregatedUsage['scope'], row: Record<string, unknown>): number
export function getRowQuota(row: Record<string, unknown>): number
export function summarizeUsage(view?: AggregatedUsage): UsageTotals
export function buildTrendPoints(view?: AggregatedUsage): UsageTrendPoint[]
```

`usageFormatting.ts` exports:

```ts
export interface BillingStatusDTO { ... }
export function formatNumber(value: number): string
export function formatQuotaValue(value: number, status?: BillingStatusDTO | null): string
```

The formatter must degrade to raw quota formatting when billing status is unavailable.

- [ ] **Step 4: Run utility tests**

Run the same Vitest command.

Expected: PASS.

## Task 3: Usage UI Components

**Files:**
- Create: `web/src/pages/usage/UsageTrendChart.vue`
- Modify: `web/src/pages/usage/UsageSummary.vue`
- Test: `web/src/pages/usage/__tests__/UsageSummary.spec.ts`

- [ ] **Step 1: Add component tests**

Create tests that mount `UsageSummary` with platform data containing `created_at` and empty `model_name`, expecting:

```ts
expect(wrapper.text()).toContain('Token 总量')
expect(wrapper.text()).toContain('未知模型')
expect(wrapper.text()).not.toContain('DATE')
```

- [ ] **Step 2: Implement SVG chart**

Create `UsageTrendChart.vue` with props:

```ts
const props = defineProps<{ points: UsageTrendPoint[]; quotaLabel: string }>()
```

Render an SVG polyline for tokens and quota. If `points.length === 0`, render `暂无趋势数据`.

- [ ] **Step 3: Update UsageSummary**

Props:

```ts
const props = defineProps<{ view?: AggregatedUsage; emptyText: string; billingStatus?: BillingStatusDTO | null }>()
```

Use `summarizeUsage`, `buildTrendPoints`, and `formatQuotaValue`. Replace dynamic table columns with stable columns by scope.

- [ ] **Step 4: Run component tests**

Run:

```bash
cd web && rtk npm test -- --run src/pages/usage/__tests__/UsageSummary.spec.ts
```

Expected: PASS.

## Task 4: Usage Page Filter Selects

**Files:**
- Modify: `web/src/pages/usage/UsagePage.vue`
- Modify: `web/src/pages/usage/__tests__/UsagePage.spec.ts`
- Modify: `web/src/api/hooks/useRecharge.ts`

- [ ] **Step 1: Add billing status hook**

In `useRecharge.ts`, add `BillingStatusDTO` and:

```ts
export function useBillingStatusQuery() {
  return useQuery<BillingStatusDTO | null>({
    queryKey: ['billing-status'],
    queryFn: async () => {
      const response = await apiRequest<{ billing_status: BillingStatusDTO }>('/api/v1/billing/status')
      return response.billing_status
    },
  })
}
```

- [ ] **Step 2: Update UsagePage tests**

Extend existing tests to assert org member still sends member query with org id, and add org admin app select path:

```ts
expect(wrapper.text()).toContain('应用')
expect(usageRefs.appContext?.value?.orgId).toBe('org-1')
```

Mock `useMembersQuery`, `useAppsByOrgQuery`, and `useBillingStatusQuery`.

- [ ] **Step 3: Update UsagePage implementation**

Replace raw `n-input` for member/app with filterable `n-select`:

```vue
<n-select v-model:value="selectedMemberId" filterable clearable :options="memberOptions" placeholder="搜索成员" />
<n-select v-model:value="selectedAppId" filterable clearable :options="appOptions" placeholder="搜索应用" />
```

Use `useMembersQuery(memberOrgRef)` and `useAppsByOrgQuery(memberOrgRef)`.

For app usage, use existing `useAppUsageQuery` with context from selected app:

```ts
const selectedApp = computed(() => apps.value?.find((a) => a.id === selectedAppId.value))
const appUsageContext = computed(() => selectedApp.value ? {
  orgId: selectedApp.value.org_id,
  ownerUserId: selectedApp.value.owner_user_id,
  newapiKeyId: selectedApp.value.newapi_key_id ?? 0,
} : undefined)
```

Show `UsageSummary` for app view instead of “前往应用列表” placeholder.

- [ ] **Step 4: Run UsagePage tests**

Run:

```bash
cd web && rtk npm test -- --run src/pages/usage/__tests__/UsagePage.spec.ts
```

Expected: PASS.

## Task 5: Recharge Dialog Display

**Files:**
- Modify: `web/src/pages/platform/OrganizationsPage.vue`
- Modify: `web/src/pages/platform/OrganizationsPage.spec.ts`

- [ ] **Step 1: Update tests**

Add expectations:

```ts
expect(wrapper.text()).toContain('充值额度')
expect(wrapper.text()).toContain('manager 不维护单价')
```

Mock `useBillingStatusQuery` in the spec.

- [ ] **Step 2: Update OrganizationsPage**

Import `useBillingStatusQuery` and `formatQuotaValue`.

Change balance display:

```vue
剩余 {{ formatQuotaValue(balance.remain_quota, billingStatus) }} ｜ 已用 {{ formatQuotaValue(balance.used_quota, billingStatus) }}
```

Change form label to `充值额度` and success feedback to:

```ts
rechargeFeedback.value = `已充值 ${formatQuotaValue(result.credit_amount, billingStatus.value)}`
```

- [ ] **Step 3: Run OrganizationsPage test**

Run:

```bash
cd web && rtk npm test -- --run src/pages/platform/OrganizationsPage.spec.ts
```

Expected: PASS.

## Task 6: Generated API Sync and Verification

**Files:**
- Modify generated: `openapi/openapi.yaml`
- Modify generated: `web/src/api/generated.ts`

- [ ] **Step 1: Generate API artifacts**

Run:

```bash
rtk make openapi-gen
rtk make web-types-gen
```

Expected: generated files update for `/api/v1/billing/status`.

- [ ] **Step 2: Run focused tests**

Run:

```bash
rtk go test ./internal/api/handlers ./internal/service ./internal/integrations/newapi -run 'TestRecharge|TestBilling|TestUsage'
cd web && rtk npm test -- --run src/pages/usage src/pages/platform/OrganizationsPage.spec.ts
cd web && rtk npm run typecheck
```

Expected: all pass.

- [ ] **Step 3: Browser verification**

Use browser automation:

1. Log in as platform admin, open `/usage`.
2. Verify platform view shows Token 总量、金额 / 额度、折线图 and rows with non-empty date.
3. Switch to organization, member, and app view using searchable selects.
4. Verify totals and chart change after selection.
5. Log in as org admin, verify platform view hidden and member/app selects work.
6. Log in as org member, verify only self/member and app views available.
7. Open platform organizations page, open recharge dialog, verify balance/used quota formatted from new-api status and text says manager does not maintain price.

Expected: no console errors, no unexpected 4xx/5xx usage or billing requests.
