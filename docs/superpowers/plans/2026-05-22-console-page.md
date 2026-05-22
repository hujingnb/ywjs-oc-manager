# 控制台页面 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 将 platform_admin 的「总览」和「平台」两个入口合并为单一「控制台」页面（`/console`），含实时统计条 + 三个 Tab 图表（Token 趋势、各组织用量、实例状态）。

**Architecture:** 后端新增 `GET /api/v1/platform/usage/org-breakdown` 接口（在 `UsageService`/`UsageHandler` 中实现），前端新建 `ConsolePage.vue` 复用 `echarts/core`（仿 `ResourceTrendChart.vue` 模式），并替换路由/菜单中的两个旧入口。

**Tech Stack:** Go 1.25 / gin / sqlc / errgroup；Vue 3 / Naive UI / echarts 6（`echarts/core` 直接使用，无需 vue-echarts）

---

## Task 1：SQL — 新增 `ListAllActiveOrganizations`

**Files:**
- Modify: `internal/store/queries/organizations.sql`
- Auto-generated (by sqlc): `internal/store/sqlc/organizations.sql.go`, `internal/store/sqlc/querier.go`

- [ ] **Step 1：追加 SQL 查询**

在 `internal/store/queries/organizations.sql` 末尾追加：

```sql
-- name: ListAllActiveOrganizations :many
-- 全量返回活跃组织（deleted_at IS NULL），不分页；
-- 仅供平台内部聚合使用（如 GetOrgUsageBreakdown），请勿用于用户可见的列表接口。
SELECT *
FROM organizations
WHERE deleted_at IS NULL
ORDER BY created_at DESC, id DESC;
```

- [ ] **Step 2：运行 sqlc 生成**

```bash
make sqlc-generate
```

预期：无报错，`internal/store/sqlc/organizations.sql.go` 出现新函数 `ListAllActiveOrganizations`，`querier.go` 中出现对应接口方法。

- [ ] **Step 3：确认生成结果**

```bash
grep -n "ListAllActiveOrganizations" internal/store/sqlc/organizations.sql.go internal/store/sqlc/querier.go
```

预期输出（行号可能不同）：
```
internal/store/sqlc/organizations.sql.go:NN:func (q *Queries) ListAllActiveOrganizations(ctx context.Context) ([]Organization, error) {
internal/store/sqlc/querier.go:NN:    ListAllActiveOrganizations(ctx context.Context) ([]Organization, error)
```

- [ ] **Step 4：提交**

```bash
git add internal/store/queries/organizations.sql internal/store/sqlc/
git commit -m "feat(store): 新增 ListAllActiveOrganizations 全量查询

用于平台控制台的组织用量分布聚合，不分页、过滤软删除记录。"
```

---

## Task 2：UsageService — 新增类型 + `GetOrgUsageBreakdown` 方法

**Files:**
- Modify: `internal/service/usage_service.go`

- [ ] **Step 1：在 `UsageStore` 接口新增方法**

在 `internal/service/usage_service.go` 的 `UsageStore` 接口中追加：

```go
// ListAllActiveOrganizations 全量返回未软删除的组织，供 GetOrgUsageBreakdown 批量查询用量。
ListAllActiveOrganizations(ctx context.Context) ([]sqlc.Organization, error)
```

- [ ] **Step 2：新增响应类型**

在 `internal/service/usage_service.go` 的 `QuotaSeries` 类型定义之后追加：

```go
// OrgUsageItem 是单个组织在指定时间窗内的 quota 消耗汇总。
type OrgUsageItem struct {
	// OrgID 是组织 UUID。
	OrgID string `json:"org_id"`
	// OrgName 是组织显示名。
	OrgName string `json:"org_name"`
	// TotalQuota 是 [since, until] 内各日 QuotaDate.Quota 的累加值。
	TotalQuota int64 `json:"total_quota"`
}

// OrgUsageBreakdown 是 GET /api/v1/platform/usage/org-breakdown 的响应视图。
type OrgUsageBreakdown struct {
	// Items 按 TotalQuota 降序排列，最多 10 条。
	Items []OrgUsageItem `json:"items"`
	// UpdatedAt 是 manager 完成聚合的时刻。
	UpdatedAt time.Time `json:"updated_at"`
}
```

- [ ] **Step 3：实现 `GetOrgUsageBreakdown` 方法**

在 `internal/service/usage_service.go` 的 `GetPlatformUsage` 方法之后追加。同时在文件顶部 import 块中补充 `"sort"`、`"sync"` 和 `"golang.org/x/sync/errgroup"`，完整 import 块如下：

```go
import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"golang.org/x/sync/errgroup"

	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/integrations/newapi"
	"oc-manager/internal/store/sqlc"
)
```

```go
// GetOrgUsageBreakdown 聚合全平台各组织在 [since, until] 内的 quota 消耗，
// 按消耗量降序返回 top 10。仅 platform_admin 可调。
//
// 并发上限 5：避免对 new-api 产生瞬时大批请求；无 newapi 账号的组织跳过。
func (s *UsageService) GetOrgUsageBreakdown(ctx context.Context, principal auth.Principal, since, until int64) (OrgUsageBreakdown, error) {
	if s.client == nil {
		return OrgUsageBreakdown{}, ErrUsageUnavailable
	}
	if principal.Role != domain.UserRolePlatformAdmin {
		return OrgUsageBreakdown{}, ErrForbidden
	}

	orgs, err := s.store.ListAllActiveOrganizations(ctx)
	if err != nil {
		return OrgUsageBreakdown{}, fmt.Errorf("查询组织列表失败: %w", err)
	}

	// 并发收集各组织用量；mu 保护 items 切片。
	var mu sync.Mutex
	var items []OrgUsageItem

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(5) // 并发上限，避免 new-api 过载
	for _, org := range orgs {
		// 跳过没有 new-api 账号或 username 的组织（历史数据 / 尚未初始化）。
		if !org.NewapiUserID.Valid || org.NewapiUserID.String == "" ||
			!org.NewapiUsername.Valid || org.NewapiUsername.String == "" {
			continue
		}
		org := org
		g.Go(func() error {
			userID := parseInt64Default(org.NewapiUserID.String, 0)
			if userID == 0 {
				return nil
			}
			dates, err := s.client.GetUserQuotaDates(gctx, userID, org.NewapiUsername.String, since, until)
			if err != nil {
				return fmt.Errorf("查询组织 %s 用量失败: %w", uuidToString(org.ID), err)
			}
			var total int64
			for _, d := range dates {
				total += d.Quota
			}
			mu.Lock()
			items = append(items, OrgUsageItem{
				OrgID:      uuidToString(org.ID),
				OrgName:    org.Name,
				TotalQuota: total,
			})
			mu.Unlock()
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return OrgUsageBreakdown{}, mapUsageError(err)
	}

	// 降序排列，截取前 10 条。
	sort.Slice(items, func(i, j int) bool {
		return items[i].TotalQuota > items[j].TotalQuota
	})
	if len(items) > 10 {
		items = items[:10]
	}
	return OrgUsageBreakdown{Items: items, UpdatedAt: time.Now()}, nil
}
```

- [ ] **Step 4：确认编译通过**

```bash
go build ./internal/service/...
```

预期：无报错。

---

## Task 3：UsageService 测试 — `GetOrgUsageBreakdown`

**Files:**
- Modify: `internal/service/usage_service_test.go`

- [ ] **Step 1：在 `fakeUsageStore` 中新增字段和方法**

在 `internal/service/usage_service_test.go` 的 `fakeUsageStore` 结构体定义中追加字段：

```go
	// allActiveOrgs 是 ListAllActiveOrganizations 的返回值。
	allActiveOrgs []sqlc.Organization
```

在 `fakeUsageStore` 的方法组末尾追加：

```go
func (s *fakeUsageStore) ListAllActiveOrganizations(_ context.Context) ([]sqlc.Organization, error) {
	return s.allActiveOrgs, nil
}
```

- [ ] **Step 2：在测试文件 import 中补充 `"fmt"`**

`usage_service_test.go` 顶部已有 import 块，需在其中添加 `"fmt"`：

```go
import (
	"context"
	"errors"
	"fmt"       // 新增
	"testing"
	...（其余保持不变）
)
```

- [ ] **Step 3：新增四个测试用例**

在 `internal/service/usage_service_test.go` 末尾追加（包含一个辅助 fake client）：

```go
// TestGetOrgUsageBreakdownForbidsNonPlatformAdmin 校验非 platform_admin 拿不到分组用量。
func TestGetOrgUsageBreakdownForbidsNonPlatformAdmin(t *testing.T) {
	svc := NewUsageService(&fakeUsageStore{}, &fakeUsageClient{}, nil)
	_, err := svc.GetOrgUsageBreakdown(context.Background(), auth.Principal{Role: domain.UserRoleOrgAdmin}, 0, 0)
	require.ErrorIs(t, err, ErrForbidden)
}

// TestGetOrgUsageBreakdownSkipsOrgsWithoutNewAPIUser 校验无 newapi_user_id 的组织
// 不触发 new-api 调用、也不报错，仅在结果中静默跳过。
func TestGetOrgUsageBreakdownSkipsOrgsWithoutNewAPIUser(t *testing.T) {
	// org1 有 newapi 账号，org2 没有；期望结果只有 org1。
	orgWithUser := sqlc.Organization{
		ID:             mustUUID(t, "00000000-0000-0000-0000-000000000a01"),
		Name:           "org-with-user",
		NewapiUserID:   pgtype.Text{String: "10", Valid: true},
		NewapiUsername: pgtype.Text{String: "org-10-user", Valid: true},
	}
	orgWithout := sqlc.Organization{
		ID:   mustUUID(t, "00000000-0000-0000-0000-000000000a02"),
		Name: "org-without-user",
		// NewapiUserID / NewapiUsername 均为零值（Invalid）
	}
	store := &fakeUsageStore{allActiveOrgs: []sqlc.Organization{orgWithUser, orgWithout}}
	client := &fakeUsageClient{userQuota: []newapi.QuotaDate{{Date: "2026-05-22", Quota: 500}}}
	svc := NewUsageService(store, client, nil)

	result, err := svc.GetOrgUsageBreakdown(context.Background(), platformAdmin(), 0, 0)
	require.NoError(t, err)
	// 仅 orgWithUser 出现在结果中。
	require.Len(t, result.Items, 1)
	assert.Equal(t, "org-with-user", result.Items[0].OrgName)
	assert.Equal(t, int64(500), result.Items[0].TotalQuota)
}

// TestGetOrgUsageBreakdownSortsAndCapsAt10 校验结果按 TotalQuota 降序并截取前 10 条。
func TestGetOrgUsageBreakdownSortsAndCapsAt10(t *testing.T) {
	// 构建 12 个组织，quota 依次为 1200, 1100, ..., 100（步长 100）。
	orgs := make([]sqlc.Organization, 12)
	for i := range orgs {
		idStr := fmt.Sprintf("00000000-0000-0000-0000-%012d", i+1)
		orgs[i] = sqlc.Organization{
			ID:             mustUUID(t, idStr),
			Name:           fmt.Sprintf("org-%02d", i+1),
			NewapiUserID:   pgtype.Text{String: fmt.Sprintf("%d", i+1), Valid: true},
			NewapiUsername: pgtype.Text{String: fmt.Sprintf("user-%d", i+1), Valid: true},
		}
	}
	// fakeUsageClient.GetUserQuotaDates 对所有 userID 返回固定 quota = userID * 100。
	client := &fakeUsageClientWithPerUserQuota{
		quotaByUserID: func(id int64) int64 { return id * 100 },
	}
	store := &fakeUsageStore{allActiveOrgs: orgs}
	svc := NewUsageService(store, client, nil)

	result, err := svc.GetOrgUsageBreakdown(context.Background(), platformAdmin(), 0, 0)
	require.NoError(t, err)
	// 最多返回 10 条。
	require.Len(t, result.Items, 10)
	// 第一条应是 quota 最大的（org-12, quota=1200）。
	assert.Equal(t, int64(1200), result.Items[0].TotalQuota)
	// 结果降序排列。
	for i := 1; i < len(result.Items); i++ {
		assert.GreaterOrEqual(t, result.Items[i-1].TotalQuota, result.Items[i].TotalQuota)
	}
}

// fakeUsageClientWithPerUserQuota 是支持按 userID 返回不同 quota 的 UsageNewAPIClient 实现，
// 专用于 TestGetOrgUsageBreakdownSortsAndCapsAt10。
type fakeUsageClientWithPerUserQuota struct {
	quotaByUserID func(id int64) int64
}

func (c *fakeUsageClientWithPerUserQuota) GetTokenLogs(_ context.Context, _ newapi.LogsQuery) (newapi.LogsPage, error) {
	return newapi.LogsPage{}, nil
}
func (c *fakeUsageClientWithPerUserQuota) GetUserQuotaDates(_ context.Context, userID int64, _ string, _, _ int64) ([]newapi.QuotaDate, error) {
	return []newapi.QuotaDate{{Date: "2026-05-22", Quota: c.quotaByUserID(userID)}}, nil
}
func (c *fakeUsageClientWithPerUserQuota) GetAllQuotaDates(_ context.Context, _, _ int64) ([]newapi.QuotaDate, error) {
	return nil, nil
}
```

- [ ] **Step 4：运行测试确认通过**

```bash
go test ./internal/service/... -run "TestGetOrgUsageBreakdown" -v
```

预期：3 个 `PASS`。

- [ ] **Step 4：确认全部 service 测试仍通过**

```bash
go test ./internal/service/...
```

预期：`ok  oc-manager/internal/service`（无失败）。

- [ ] **Step 5：提交**

```bash
git add internal/service/usage_service.go internal/service/usage_service_test.go
git commit -m "feat(usage): 新增 GetOrgUsageBreakdown 聚合各组织用量

并发上限 5，跳过无 newapi 账号的组织，结果按 quota 降序截取 top 10。
同步扩展 UsageStore 接口新增 ListAllActiveOrganizations 方法。"
```

---

## Task 4：UsageHandler — 新增 `GetOrgBreakdown` 接口

**Files:**
- Modify: `internal/api/handlers/usage.go`

- [ ] **Step 1：在 handler 接口中新增方法**

在 `internal/api/handlers/usage.go` 的 `usageService` 接口中追加：

```go
	GetOrgUsageBreakdown(ctx context.Context, principal auth.Principal, since, until int64) (service.OrgUsageBreakdown, error)
```

- [ ] **Step 2：新增 handler 方法**

在 `GetPlatform` 方法之后追加：

```go
// GetOrgBreakdown 返回各组织近期 quota 消耗的 top 10 汇总，供平台控制台图表使用。
// 仅 platform_admin 可调；service 层再做一次角色校验。
//
// @Summary      各组织用量分布
// @Description  平台维度各组织在时间窗口内的 quota 消耗 top 10，仅平台管理员可调
// @Tags         platform
// @Produce      json
// @Security     BearerAuth
// @Param        since  query     int     false  "起始时间（Unix 秒）"
// @Param        until  query     int     false  "结束时间（Unix 秒）"
// @Success      200    {object}  map[string]service.OrgUsageBreakdown
// @Failure      401    {object}  ErrorResponse
// @Failure      403    {object}  ErrorResponse
// @Failure      500    {object}  ErrorResponse
// @Failure      503    {object}  ErrorResponse
// @Router       /platform/usage/org-breakdown [get]
func (h *UsageHandler) GetOrgBreakdown(c *gin.Context) {
	principal := principalFromCtx(c)
	since, until := parseUsageStatsWindow(c)
	view, err := h.service.GetOrgUsageBreakdown(c.Request.Context(), principal, since, until)
	if err != nil {
		writeUsageError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"breakdown": view})
}
```

- [ ] **Step 3：在路由注册中新增路由**

在 `RegisterUsageRoutes` 函数中追加：

```go
	router.GET("/api/v1/platform/usage/org-breakdown", handler.GetOrgBreakdown)
```

- [ ] **Step 4：确认编译**

```bash
go build ./internal/api/...
```

预期：无报错。

- [ ] **Step 5：提交**

```bash
git add internal/api/handlers/usage.go
git commit -m "feat(handler): 新增 GET /api/v1/platform/usage/org-breakdown 接口

返回各组织在时间窗内的 quota 消耗 top 10，供平台控制台组织用量分布图使用。"
```

---

## Task 5：OpenAPI 同步

**Files:**
- Auto-generated: `openapi/openapi.yaml`, `web/src/api/generated.ts`

- [ ] **Step 1：重新生成 OpenAPI spec**

```bash
make openapi-gen
```

预期：无报错，`openapi/openapi.yaml` 包含 `/platform/usage/org-breakdown` 新路由。

- [ ] **Step 2：重新生成前端类型**

```bash
make web-types-gen
```

预期：无报错，`web/src/api/generated.ts` 更新。

- [ ] **Step 3：确认工作区干净（仅 openapi 文件有变化）**

```bash
git diff --name-only
```

预期：只有 `openapi/openapi.yaml` 和 `web/src/api/generated.ts` 显示变更。

- [ ] **Step 4：提交**

```bash
git add openapi/openapi.yaml web/src/api/generated.ts
git commit -m "chore(openapi): 同步 org-breakdown 接口契约

新增 GET /platform/usage/org-breakdown 路由的 swag 注解和前端生成类型。"
```

---

## Task 6：前端 hook — `usePlatformOrgBreakdownQuery`

**Files:**
- Modify: `web/src/api/hooks/usePlatform.ts`

- [ ] **Step 1：新增类型和 hook**

在 `web/src/api/hooks/usePlatform.ts` 末尾追加：

```typescript
// OrgUsageBreakdownItem 与后端 service.OrgUsageItem 字段一一对应。
export interface OrgUsageBreakdownItem {
  // 组织 UUID。
  org_id: string
  // 组织显示名。
  org_name: string
  // [since, until] 内各日 quota 累计值（new-api 原始单位）。
  total_quota: number
}

// usePlatformOrgBreakdownQuery 拉各组织近 7 天 quota 消耗汇总，仅 platform_admin 可调。
// 60 秒刷新，图表数据变化频率低于统计卡片。
export function usePlatformOrgBreakdownQuery(enabled: Ref<boolean>) {
  return useQuery<OrgUsageBreakdownItem[]>({
    queryKey: ['platform', 'usage', 'org-breakdown'],
    enabled: () => enabled.value,
    refetchInterval: 60000,
    queryFn: async () => {
      const now = Math.floor(Date.now() / 1000)
      const since = now - 7 * 24 * 60 * 60
      const resp = await apiRequest<{ breakdown: { items: OrgUsageBreakdownItem[] } }>(
        '/api/v1/platform/usage/org-breakdown',
        { query: { since: String(since), until: String(now) } },
      )
      return resp.breakdown.items ?? []
    },
  })
}
```

- [ ] **Step 2：确认类型检查通过**

```bash
make web-typecheck
```

预期：无报错。

- [ ] **Step 3：提交**

```bash
git add web/src/api/hooks/usePlatform.ts
git commit -m "feat(web/hooks): 新增 usePlatformOrgBreakdownQuery

拉各组织近 7 天 quota 消耗 top 10，60 秒轮询，用于控制台组织用量分布图。"
```

---

## Task 7：前端 — 新建 `ConsolePage.vue`

**Files:**
- Create: `web/src/pages/platform/ConsolePage.vue`

- [ ] **Step 1：创建组件文件**

创建 `web/src/pages/platform/ConsolePage.vue`，内容如下（完整文件）：

```vue
<template>
  <div style="display: grid; gap: 18px">
    <!-- 统计条 -->
    <n-grid :cols="6" :x-gap="14" :y-gap="14" responsive="screen" :item-responsive="true">
      <n-grid-item v-for="stat in stats" :key="stat.label" :span="1" :xs="3" :sm="2" :md="1">
        <n-card size="small" :bordered="true">
          <n-statistic :label="stat.label" :value="stat.value">
            <template v-if="stat.suffix" #suffix>
              <span style="font-size: 11px; color: #8A94C6">{{ stat.suffix }}</span>
            </template>
          </n-statistic>
          <div v-if="stat.note" style="font-size: 11px; margin-top: 4px" :style="{ color: stat.noteColor ?? '#8A94C6' }">
            {{ stat.note }}
          </div>
        </n-card>
      </n-grid-item>
    </n-grid>

    <!-- 图表区 Tab -->
    <n-card :bordered="true" style="flex: 1">
      <n-tabs v-model:value="activeTab" type="line" animated @update:value="onTabChange">
        <!-- Tab 1：Token 趋势 -->
        <n-tab-pane name="token" tab="Token 趋势">
          <div v-if="platformUsageLoading" class="chart-state">加载中…</div>
          <div v-else-if="platformUsageError" class="chart-state danger">用量服务不可用</div>
          <div v-else ref="tokenChartEl" class="chart-container" />
        </n-tab-pane>

        <!-- Tab 2：各组织用量 -->
        <n-tab-pane name="orgs" tab="各组织用量">
          <div v-if="orgBreakdownLoading" class="chart-state">加载中…</div>
          <div v-else-if="orgBreakdownError" class="chart-state danger">用量服务不可用</div>
          <div v-else-if="!orgBreakdownData?.length" class="chart-state">暂无数据</div>
          <div v-else ref="orgChartEl" class="chart-container" />
        </n-tab-pane>

        <!-- Tab 3：实例状态 -->
        <n-tab-pane name="status" tab="实例状态">
          <div v-if="overviewLoading" class="chart-state">加载中…</div>
          <div v-else ref="statusChartEl" class="chart-container" />
        </n-tab-pane>
      </n-tabs>
    </n-card>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { NCard, NGrid, NGridItem, NStatistic, NTabPane, NTabs } from 'naive-ui'
import { init, use } from 'echarts/core'
import { LineChart, BarChart, PieChart } from 'echarts/charts'
import {
  GridComponent, TooltipComponent, LegendComponent,
} from 'echarts/components'
import { CanvasRenderer } from 'echarts/renderers'
import type { EChartsType } from 'echarts/core'

import { usePlatformOverviewQuery, usePlatformOrgBreakdownQuery } from '@/api/hooks/usePlatform'
import { useAuthStore } from '@/stores/auth'
import { apiRequest } from '@/api/client'
import { useQuery } from '@tanstack/vue-query'

use([CanvasRenderer, LineChart, BarChart, PieChart, GridComponent, TooltipComponent, LegendComponent])

// ConsolePage 是平台管理员专属的控制台首页：统计条 + Token 趋势/组织用量/实例状态三图。
const auth = useAuthStore()
const isPlatformAdmin = computed(() => auth.isPlatformAdmin)

// ── 数据查询 ──────────────────────────────────────────────
const { data: overview, isLoading: overviewLoading } = usePlatformOverviewQuery(isPlatformAdmin)
const { data: orgBreakdownData, isLoading: orgBreakdownLoading, error: orgBreakdownError } = usePlatformOrgBreakdownQuery(isPlatformAdmin)

// 平台近 7 天 quota 序列：用于 Token 趋势折线图和「今日 Token」统计卡片。
const { data: platformUsageItems, isLoading: platformUsageLoading, error: platformUsageError } = useQuery<
  { date: string; quota: number }[]
>({
  queryKey: ['usage', 'platform', '7days'],
  enabled: () => isPlatformAdmin.value,
  refetchInterval: 60000,
  queryFn: async () => {
    const now = Math.floor(Date.now() / 1000)
    const since = now - 7 * 24 * 60 * 60
    const resp = await apiRequest<{ usage: { items: { date: string; quota: number }[] } }>(
      '/api/v1/usage/platform',
      { query: { since: String(since), until: String(now) } },
    )
    return resp.usage?.items ?? []
  },
})

// ── 统计卡片 ──────────────────────────────────────────────
// todayTokenTotal 把今天（本地日期）在 platformUsageItems 中的 quota 求和。
const todayTokenTotal = computed(() => {
  if (!platformUsageItems.value?.length) return null
  const today = new Date().toISOString().slice(0, 10) // YYYY-MM-DD
  return platformUsageItems.value
    .filter(item => item.date === today)
    .reduce((acc, item) => acc + item.quota, 0)
})

// stats 将 overview + today token 转为统计卡片数组。
const stats = computed(() => {
  const o = overview.value
  return [
    { label: '组织数', value: String(o?.organization_count ?? '—'), note: '', noteColor: undefined, suffix: undefined },
    { label: '成员数', value: String(o?.member_count ?? '—'), note: '不含平台管理员', noteColor: undefined, suffix: undefined },
    { label: '实例数', value: String(o?.app_count ?? '—'), note: '', noteColor: undefined, suffix: undefined },
    { label: '运行中', value: String(o?.running_app_count ?? '—'), note: '', noteColor: '#18a058', suffix: undefined },
    { label: '异常', value: String(o?.error_app_count ?? '—'), note: '', noteColor: '#d03050', suffix: undefined },
    {
      label: '今日 Token',
      value: todayTokenTotal.value !== null ? String(todayTokenTotal.value.toLocaleString('en-US')) : '—',
      note: todayTokenTotal.value !== null ? 'new-api 实时' : platformUsageLoading.value ? '加载中…' : '不可用',
      noteColor: undefined,
      suffix: undefined,
    },
  ]
})

// ── 图表 ──────────────────────────────────────────────────
const activeTab = ref<'token' | 'orgs' | 'status'>('token')
const tokenChartEl = ref<HTMLElement | null>(null)
const orgChartEl = ref<HTMLElement | null>(null)
const statusChartEl = ref<HTMLElement | null>(null)

let tokenChart: EChartsType | null = null
let orgChart: EChartsType | null = null
let statusChart: EChartsType | null = null

// formatQuota 把 new-api quota（1 token ≈ 5×10⁻³ 单位，但按原始数字显示）格式化为万/千。
function formatQuota(v: number): string {
  if (v >= 1_000_000) return `${(v / 1_000_000).toFixed(1)}M`
  if (v >= 10_000) return `${(v / 10_000).toFixed(1)}W`
  if (v >= 1_000) return `${(v / 1_000).toFixed(1)}k`
  return String(v)
}

// ── Token 趋势图（折线） ──
function buildTokenChart() {
  if (!tokenChartEl.value || !platformUsageItems.value?.length) return
  if (!tokenChart) tokenChart = init(tokenChartEl.value)

  // 按日聚合：同一天可能有多个 model 条目。
  const byDate = new Map<string, number>()
  for (const item of platformUsageItems.value) {
    byDate.set(item.date, (byDate.get(item.date) ?? 0) + item.quota)
  }
  const sorted = [...byDate.entries()].sort(([a], [b]) => a.localeCompare(b))
  const dates = sorted.map(([d]) => d.slice(5)) // MM-DD
  const values = sorted.map(([, v]) => v)

  tokenChart.setOption({
    animation: false,
    grid: { top: 14, right: 16, bottom: 28, left: 60, containLabel: false },
    tooltip: { trigger: 'axis', formatter: (params: { value: number }[]) => formatQuota(params[0]?.value ?? 0) },
    xAxis: {
      type: 'category',
      data: dates,
      axisLabel: { color: '#8A94C6', fontSize: 11 },
      axisLine: { lineStyle: { color: '#30363d' } },
      axisTick: { show: false },
    },
    yAxis: {
      type: 'value',
      axisLabel: { color: '#8A94C6', fontSize: 11, formatter: (v: number) => formatQuota(v) },
      splitLine: { lineStyle: { color: '#2d3139' } },
    },
    series: [{
      type: 'line',
      data: values,
      smooth: true,
      showSymbol: true,
      symbolSize: 5,
      lineStyle: { width: 2, color: '#1f6feb' },
      itemStyle: { color: '#1f6feb' },
      areaStyle: { color: 'rgba(31,111,235,0.08)' },
    }],
  })
}

// ── 各组织用量图（横向柱状） ──
function buildOrgChart() {
  if (!orgChartEl.value || !orgBreakdownData.value?.length) return
  if (!orgChart) orgChart = init(orgChartEl.value)

  const items = [...orgBreakdownData.value].reverse() // echarts bar 从底到顶，反转让最高的在上
  orgChart.setOption({
    animation: false,
    grid: { top: 8, right: 80, bottom: 8, left: 120, containLabel: false },
    tooltip: {
      trigger: 'axis',
      axisPointer: { type: 'shadow' },
      formatter: (params: { value: number }[]) => formatQuota(params[0]?.value ?? 0),
    },
    xAxis: {
      type: 'value',
      axisLabel: { color: '#8A94C6', fontSize: 11, formatter: (v: number) => formatQuota(v) },
      splitLine: { lineStyle: { color: '#2d3139' } },
    },
    yAxis: {
      type: 'category',
      data: items.map(i => i.org_name),
      axisLabel: { color: '#8A94C6', fontSize: 11, width: 110, overflow: 'truncate' },
      axisLine: { show: false },
      axisTick: { show: false },
    },
    series: [{
      type: 'bar',
      data: items.map(i => i.total_quota),
      itemStyle: { color: '#1f6feb', borderRadius: [0, 3, 3, 0] },
      label: { show: true, position: 'right', color: '#8A94C6', fontSize: 11, formatter: (p: { value: number }) => formatQuota(p.value) },
    }],
  })
}

// ── 实例状态图（饼图） ──
function buildStatusChart() {
  if (!statusChartEl.value || !overview.value) return
  if (!statusChart) statusChart = init(statusChartEl.value)

  const o = overview.value
  const stopped = (o.app_count ?? 0) - (o.running_app_count ?? 0) - (o.error_app_count ?? 0)
  statusChart.setOption({
    animation: false,
    tooltip: { trigger: 'item', formatter: '{b}: {c} ({d}%)' },
    legend: { bottom: 0, textStyle: { color: '#8A94C6', fontSize: 12 } },
    series: [{
      type: 'pie',
      radius: ['40%', '68%'],
      center: ['50%', '44%'],
      itemStyle: { borderWidth: 2, borderColor: '#0d1117' },
      label: { show: false },
      data: [
        { name: '运行中', value: o.running_app_count ?? 0, itemStyle: { color: '#18a058' } },
        { name: '停止', value: stopped < 0 ? 0 : stopped, itemStyle: { color: '#63748a' } },
        { name: '异常', value: o.error_app_count ?? 0, itemStyle: { color: '#d03050' } },
      ],
    }],
  })
}

// 切 Tab 时等 DOM 渲染后再初始化/resize 图表。
function onTabChange(tab: string) {
  nextTick(() => {
    if (tab === 'token') { tokenChart ? tokenChart.resize() : buildTokenChart() }
    if (tab === 'orgs') { orgChart ? orgChart.resize() : buildOrgChart() }
    if (tab === 'status') { statusChart ? statusChart.resize() : buildStatusChart() }
  })
}

// 数据就绪后自动重绘；watch 保证初始加载完成也触发。
watch(platformUsageItems, () => { if (activeTab.value === 'token') nextTick(buildTokenChart) })
watch(orgBreakdownData, () => { if (activeTab.value === 'orgs') nextTick(buildOrgChart) })
watch(overview, () => { if (activeTab.value === 'status') nextTick(buildStatusChart) })

onMounted(() => { nextTick(buildTokenChart) })

onBeforeUnmount(() => {
  tokenChart?.dispose()
  orgChart?.dispose()
  statusChart?.dispose()
})
</script>

<style scoped>
.chart-container {
  width: 100%;
  height: 320px;
}

.chart-state {
  display: flex;
  align-items: center;
  justify-content: center;
  height: 320px;
  color: #8a94c6;
  font-size: 13px;
}

.chart-state.danger { color: #d03050; }
</style>
```

- [ ] **Step 2：确认 TypeScript 编译**

```bash
make web-typecheck
```

预期：无报错。

- [ ] **Step 3：提交**

```bash
git add web/src/pages/platform/ConsolePage.vue
git commit -m "feat(web): 新增 ConsolePage 平台控制台页面

含 6 张统计卡片（overview + 今日 Token）和三个图表 Tab：
Token 趋势折线图、各组织用量横向柱状图、实例状态饼图。"
```

---

## Task 8：前端路由 + 菜单 + 首页重定向

**Files:**
- Modify: `web/src/app/router.ts`
- Modify: `web/src/layouts/DashboardLayout.vue`
- Modify: `web/src/pages/dashboard/RoleAwareHome.vue`

- [ ] **Step 1：更新 `router.ts`**

在 `web/src/app/router.ts` 中：

1. 在 import 块末尾新增：
```typescript
import ConsolePage from '@/pages/platform/ConsolePage.vue'
```

2. 删除：
```typescript
import DashboardHome from '@/pages/dashboard/DashboardHome.vue'
import PlatformDashboardPage from '@/pages/platform/PlatformDashboardPage.vue'
```

3. 在路由数组中，替换以下两条路由：
```typescript
// 删除这两条：
{ path: 'dashboard', component: DashboardHome },
{ path: 'platform/dashboard', component: PlatformDashboardPage, meta: { allowedRoles: PLATFORM_ONLY } },
```
替换为：
```typescript
// 新增控制台路由
{ path: 'console', component: ConsolePage, meta: { allowedRoles: PLATFORM_ONLY } },
// 旧路径重定向，保留向后兼容
{ path: 'platform/dashboard', redirect: '/console' },
{ path: 'dashboard', redirect: '/console' },
```

- [ ] **Step 2：更新 `DashboardLayout.vue` 菜单**

在 `web/src/layouts/DashboardLayout.vue` 中：

1. **保留** `LayoutDashboard` import（非 platform_admin 的"总览"入口仍需它），只把 `Gauge` 继续用于"控制台"入口。无需修改 lucide-vue-next import 行。

2. 在 `menuOptions` computed 中，替换：
```typescript
// 删除：
{ key: '/', label: '总览', icon: () => h(LayoutDashboard, { size: 18 }) },
// 以及 if (isPlatformAdmin.value) 块中的：
items.push({ key: '/platform/dashboard', label: '平台', icon: () => h(Gauge, { size: 18 }) })
```
替换为：
```typescript
// platform_admin 看"控制台"，其他角色保留"总览"（/ 路由）
if (isPlatformAdmin.value) {
  items.push({ key: '/console', label: '控制台', icon: () => h(Gauge, { size: 18 }) })
} else {
  items.push({ key: '/', label: '总览', icon: () => h(LayoutDashboard, { size: 18 }) })
}
```

3. 在 `activeKey` computed 的 `prefixes` 数组中，替换 `'/platform/dashboard'` 为 `'/console'`。

- [ ] **Step 3：更新 `RoleAwareHome.vue` 重定向**

在 `web/src/pages/dashboard/RoleAwareHome.vue` 的 `<script setup>` 中：

1. 新增 `useRouter` 导入（已有则跳过）：
```typescript
import { useRouter } from 'vue-router'
```

2. 在 `auth` 声明之后新增：
```typescript
const router = useRouter()

// platform_admin 访问首页直接跳转到控制台，不展示欢迎卡片。
if (auth.user?.role === 'platform_admin') {
  router.replace('/console')
}
```

- [ ] **Step 4：确认 TypeScript 编译**

```bash
make web-typecheck
```

预期：无报错。

- [ ] **Step 5：提交**

```bash
git add web/src/app/router.ts web/src/layouts/DashboardLayout.vue web/src/pages/dashboard/RoleAwareHome.vue
git commit -m "feat(web/router): 接入控制台路由，替换总览+平台双入口

platform_admin 菜单从「总览+平台」改为单一「控制台」（/console）；
访问旧路径 /platform/dashboard 和 /dashboard 自动重定向；
RoleAwareHome 对 platform_admin 直接跳转 /console。"
```

---

## Task 9：删除废弃文件

**Files:**
- Delete: `web/src/pages/platform/PlatformDashboardPage.vue`
- Delete: `web/src/pages/dashboard/DashboardHome.vue`

- [ ] **Step 1：删除文件**

```bash
rm web/src/pages/platform/PlatformDashboardPage.vue
rm web/src/pages/dashboard/DashboardHome.vue
```

- [ ] **Step 2：确认无残留引用**

```bash
grep -r "PlatformDashboardPage\|DashboardHome" web/src --include="*.ts" --include="*.vue"
```

预期：无输出（已在 Task 8 中清理 import）。

- [ ] **Step 3：确认编译**

```bash
make web-typecheck
```

预期：无报错。

- [ ] **Step 4：提交**

```bash
git add -A web/src/pages/platform/PlatformDashboardPage.vue web/src/pages/dashboard/DashboardHome.vue
git commit -m "chore(web): 删除废弃的 PlatformDashboardPage 和 DashboardHome

两文件已被 ConsolePage 替代，路由已清理，无残留引用。"
```

---

## Task 10：验证

- [ ] **Step 1：运行后端全量测试**

```bash
go test ./...
```

预期：无失败。

- [ ] **Step 2：运行前端类型检查**

```bash
make web-typecheck
```

预期：无报错。

- [ ] **Step 3：浏览器验证（需本地启动服务）**

使用 `admin` / `admin123` 以 platform_admin 身份登录：

1. 访问 `http://localhost:8080`（具体端口以本地配置为准），确认自动重定向到 `/console`
2. 统计条 6 张卡片正常显示，数字非 `—`（需 new-api 在线）
3. 切换「Token 趋势」Tab：折线图显示近 7 天数据，无报错
4. 切换「各组织用量」Tab：横向柱状图显示 top 10 组织，数据非空
5. 切换「实例状态」Tab：饼图显示运行中/停止/异常分布
6. 直接访问 `/platform/dashboard` 和 `/dashboard`，确认均重定向到 `/console`
7. 以 org_admin 身份登录，确认首页仍见「总览」欢迎卡片（`/` 路由正常），菜单无「控制台」
