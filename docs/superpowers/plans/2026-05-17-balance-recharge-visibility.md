# 余额与充值记录可见性 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让组织管理员能查看自己的余额与充值记录；让平台管理员在组织列表页看到余额列和充值记录弹窗。

**Architecture:** 后端扩展 `BalanceView` 新增 `TotalRecharged`（来自本地 DB 聚合），放开 `ListRecharges` 给 org_admin；前端在组织列表页批量拉取余额、新增充值记录弹窗，并为 org_admin 新增"账户余额"顶级页。

**Tech Stack:** Go (gin + sqlc + errgroup), Vue 3 + Naive UI + TanStack Query v5, swag/openapi-typescript

---

## 文件变更清单

| 文件 | 新增/修改 | 说明 |
|------|-----------|------|
| `internal/store/queries/recharge_records.sql` | 修改 | 新增 `SumRechargeAmountByOrg` 查询 |
| `internal/store/sqlc/recharge_records.sql.go` | 生成 | `make sqlc-generate` 覆盖，勿手改 |
| `internal/service/recharge_service.go` | 修改 | `BalanceView` 增字段、`GetBalance` 并发、`ListRecharges` 权限 |
| `internal/service/recharge_service_test.go` | 修改 | stub 实现新接口方法、更新/新增测试 |
| `internal/auth/authorizer.go` | 修改 | 新增 `CanViewRecharges` 谓词 |
| `openapi/openapi.yaml` | 生成 | `make openapi-gen` 覆盖 |
| `web/src/api/generated.ts` | 生成 | `make web-types-gen` 覆盖 |
| `web/src/api/hooks/useRecharge.ts` | 修改 | `BalanceDTO` 增加 `total_recharged` |
| `web/src/pages/platform/OrganizationsPage.vue` | 修改 | 批量余额列 + 充值记录 Modal |
| `web/src/pages/org/OrgBalancePage.vue` | 新增 | org_admin 账户余额页 |
| `web/src/app/router.ts` | 修改 | 注册 `/balance` 路由 |
| `web/src/layouts/DashboardLayout.vue` | 修改 | org_admin 导航新增"账户余额" |

---

## Task 1: SQL — 新增聚合查询并重新生成 sqlc 代码

**Files:**
- Modify: `internal/store/queries/recharge_records.sql`
- Generated: `internal/store/sqlc/recharge_records.sql.go`（`make sqlc-generate` 覆盖）

- [ ] **Step 1: 在 SQL 查询文件末尾追加新查询**

  编辑 `internal/store/queries/recharge_records.sql`，在文件末尾添加：

  ```sql
  -- name: SumRechargeAmountByOrg :one
  -- SumRechargeAmountByOrg 聚合指定组织所有成功充值记录的总额。
  -- 仅统计 status='succeeded' 的记录，failed 记录不计入累计金额。
  SELECT COALESCE(SUM(credit_amount), 0)::bigint AS total_recharged
  FROM recharge_records
  WHERE org_id = $1 AND status = 'succeeded';
  ```

- [ ] **Step 2: 运行 sqlc 代码生成**

  ```bash
  make sqlc-generate
  ```

  预期：无报错，`internal/store/sqlc/recharge_records.sql.go` 更新，新增：

  ```go
  const sumRechargeAmountByOrg = `-- name: SumRechargeAmountByOrg :one
  ...`

  func (q *Queries) SumRechargeAmountByOrg(ctx context.Context, orgID pgtype.UUID) (int64, error) {
      row := q.db.QueryRow(ctx, sumRechargeAmountByOrg, orgID)
      var totalRecharged int64
      err := row.Scan(&totalRecharged)
      return totalRecharged, err
  }
  ```

- [ ] **Step 3: 确认生成代码存在**

  ```bash
  grep -n "SumRechargeAmountByOrg" internal/store/sqlc/recharge_records.sql.go
  ```

  预期：输出函数定义所在行号。

- [ ] **Step 4: Commit**

  ```bash
  git add internal/store/queries/recharge_records.sql internal/store/sqlc/recharge_records.sql.go
  git commit -m "$(cat <<'EOF'
  feat(store): 新增 SumRechargeAmountByOrg 聚合查询

  统计指定组织所有 status='succeeded' 充值记录的 credit_amount 之和，
  用于 GetBalance 接口返回累计充值金额。
  EOF
  )"
  ```

---

## Task 2: 后端 Service — 扩展接口与 GetBalance 并发查询

**Files:**
- Modify: `internal/service/recharge_service.go`

- [ ] **Step 1: 在 `RechargeStore` 接口中添加新方法**

  找到 `recharge_service.go` 第 18-23 行的 `RechargeStore` 接口，添加 `SumRechargeAmountByOrg` 方法：

  ```go
  // RechargeStore 抽象 service 需要的存储能力。
  type RechargeStore interface {
      GetOrganization(ctx context.Context, id pgtype.UUID) (sqlc.Organization, error)
      CreateRechargeRecord(ctx context.Context, arg sqlc.CreateRechargeRecordParams) (sqlc.RechargeRecord, error)
      ListRechargeRecordsByOrg(ctx context.Context, arg sqlc.ListRechargeRecordsByOrgParams) ([]sqlc.RechargeRecord, error)
      CreateAuditLog(ctx context.Context, arg sqlc.CreateAuditLogParams) (sqlc.AuditLog, error)
      // SumRechargeAmountByOrg 聚合指定组织 succeeded 充值记录总额；无记录时返回 0。
      SumRechargeAmountByOrg(ctx context.Context, orgID pgtype.UUID) (int64, error)
  }
  ```

- [ ] **Step 2: 在 `BalanceView` 中添加 `TotalRecharged` 字段**

  找到第 47-51 行的 `BalanceView`，替换为：

  ```go
  // BalanceView 是 GET /organizations/:id/balance 接口的响应。
  type BalanceView struct {
      NewAPIUserID   int64 `json:"newapi_user_id"`
      RemainQuota    int64 `json:"remain_quota"`
      UsedQuota      int64 `json:"used_quota"`
      // TotalRecharged 是该组织历史累计充值额度之和（仅计 succeeded 记录）。
      // 数据来源于 manager 自身 recharge_records 表，不从 new-api 获取。
      TotalRecharged int64 `json:"total_recharged"`
  }
  ```

- [ ] **Step 3: 在文件顶部 import 块添加 errgroup**

  找到当前 import 块，添加 `"golang.org/x/sync/errgroup"`：

  ```go
  import (
      "context"
      "errors"
      "fmt"

      "github.com/jackc/pgx/v5"
      "github.com/jackc/pgx/v5/pgtype"
      "golang.org/x/sync/errgroup"

      "oc-manager/internal/auth"
      "oc-manager/internal/domain"
      "oc-manager/internal/integrations/newapi"
      "oc-manager/internal/store/sqlc"
  )
  ```

- [ ] **Step 4: 重写 `GetBalance` 为并发版本**

  将第 193-227 行的 `GetBalance` 替换为：

  ```go
  // GetBalance 查询组织当前余额（透传 new-api）及累计充值金额（本地聚合）。
  // 两个数据源并发查询：① new-api 取 RemainQuota/UsedQuota；② 本地 DB 聚合 TotalRecharged。
  func (s *RechargeService) GetBalance(ctx context.Context, principal auth.Principal, orgID string) (BalanceView, error) {
      if principal.Role != domain.UserRolePlatformAdmin && principal.Role != domain.UserRoleOrgAdmin {
          return BalanceView{}, ErrForbidden
      }
      id, err := parseUUID(orgID)
      if err != nil {
          return BalanceView{}, ErrNotFound
      }
      org, err := s.store.GetOrganization(ctx, id)
      if errors.Is(err, pgx.ErrNoRows) {
          return BalanceView{}, ErrNotFound
      }
      if err != nil {
          return BalanceView{}, fmt.Errorf("查询组织失败: %w", err)
      }
      if principal.Role == domain.UserRoleOrgAdmin && principal.OrgID != uuidToString(org.ID) {
          return BalanceView{}, ErrForbidden
      }
      if !org.NewapiUserID.Valid || org.NewapiUserID.String == "" {
          return BalanceView{}, ErrOrgMissingNewAPIUserID
      }
      newapiUserID, err := parseInt64(org.NewapiUserID.String)
      if err != nil {
          return BalanceView{}, fmt.Errorf("非法 newapi_user_id: %w", err)
      }

      // 并发执行：① new-api 余额查询（实时，不缓存）；② 本地 DB 累计充值聚合。
      var balance newapi.BalanceResult
      var totalRecharged int64
      g, gctx := errgroup.WithContext(ctx)
      g.Go(func() error {
          var e error
          balance, e = s.client.GetUserBalance(gctx, newapiUserID)
          return e
      })
      g.Go(func() error {
          var e error
          totalRecharged, e = s.store.SumRechargeAmountByOrg(gctx, id)
          return e
      })
      if err := g.Wait(); err != nil {
          return BalanceView{}, fmt.Errorf("查询余额失败: %w", err)
      }
      return BalanceView{
          NewAPIUserID:   balance.NewAPIUserID,
          RemainQuota:    balance.RemainQuota,
          UsedQuota:      balance.UsedQuota,
          TotalRecharged: totalRecharged,
      }, nil
  }
  ```

- [ ] **Step 5: 编译验证**

  ```bash
  go build ./internal/service/...
  ```

  预期：无报错（此时测试会 fail，因为 stub 未实现新方法，下一个 Task 修复）。

---

## Task 3: 后端权限 — CanViewRecharges 谓词 + ListRecharges 放权

**Files:**
- Modify: `internal/auth/authorizer.go`
- Modify: `internal/service/recharge_service.go`

- [ ] **Step 1: 在 `authorizer.go` 末尾添加 `CanViewRecharges`**

  在 `authorizer.go` 最后的 `CanViewOwnAudit` 函数之后追加：

  ```go
  // CanViewRecharges 判断主体是否可查看指定组织的充值记录。
  // 平台管理员可查任意组织；组织管理员仅可查自己所属组织的充值记录。
  func CanViewRecharges(p Principal, orgID string) bool {
      return p.Role == domain.UserRolePlatformAdmin ||
          (p.Role == domain.UserRoleOrgAdmin && p.OrgID == orgID)
  }
  ```

- [ ] **Step 2: 修改 `ListRecharges` 权限检查**

  将 `recharge_service.go` 中 `ListRecharges` 函数头部的权限检查：

  ```go
  // 改前
  if principal.Role != domain.UserRolePlatformAdmin {
      return nil, ErrRechargeDenied
  }
  ```

  替换为：

  ```go
  // 改后：平台管理员可查任意组织，组织管理员仅可查自己组织
  if !auth.CanViewRecharges(principal, orgID) {
      return nil, ErrForbidden
  }
  ```

  同时删除 `ListRecharges` 函数注释中"仅平台管理员可访问"的旧说明，更新为：

  ```go
  // ListRecharges 列出组织充值历史。平台管理员可查任意组织；org_admin 仅可查自己所属组织。
  ```

- [ ] **Step 3: 编译验证**

  ```bash
  go build ./internal/...
  ```

  预期：无报错。

- [ ] **Step 4: Commit（task 2 + task 3 合并提交，属同一功能边界）**

  ```bash
  git add internal/service/recharge_service.go internal/auth/authorizer.go
  git commit -m "$(cat <<'EOF'
  feat(recharge): GetBalance 增加 TotalRecharged 字段，放开 ListRecharges 给 org_admin

  BalanceView 新增 total_recharged，并发查询 new-api 余额和本地 recharge_records 聚合。

  ListRecharges 改用 auth.CanViewRecharges 权限谓词，org_admin 现在可以查看自己组织的
  充值记录；authorizer.go 新增 CanViewRecharges 函数，与 CanViewOrgUsage 语义对齐。
  EOF
  )"
  ```

---

## Task 4: 后端单元测试 — 更新 stub 并补充新用例

**Files:**
- Modify: `internal/service/recharge_service_test.go`

- [ ] **Step 1: 在 `rechargeStub` 中实现 `SumRechargeAmountByOrg`**

  找到 `recharge_service_test.go` 中 `rechargeStub` 结构体定义（约第 144 行），添加字段和方法：

  在结构体中添加 `totalRecharged int64` 字段：

  ```go
  type rechargeStub struct {
      t                *testing.T
      org              sqlc.Organization
      records          []sqlc.RechargeRecord
      recordWritten    bool
      lastRecordStatus string
      auditWritten     bool
      totalRecharged   int64 // SumRechargeAmountByOrg 的桩返回值
  }
  ```

  在文件中添加 `SumRechargeAmountByOrg` 方法（紧接现有方法之后）：

  ```go
  func (s *rechargeStub) SumRechargeAmountByOrg(_ context.Context, _ pgtype.UUID) (int64, error) {
      return s.totalRecharged, nil
  }
  ```

- [ ] **Step 2: 更新 `TestListRecharges_DeniesNonPlatformAdmin` 以匹配新错误**

  旧测试检查 `ErrRechargeDenied`，现在 `ListRecharges` 返回 `ErrForbidden`。将：

  ```go
  // TestListRecharges_DeniesNonPlatformAdmin 验证列表充值记录Denies非平台管理员的预期行为场景。
  func TestListRecharges_DeniesNonPlatformAdmin(t *testing.T) {
      store := newRechargeStub(t, "1234")
      svc := NewRechargeService(store, &fakeNewAPIRecharge{})
      _, err := svc.ListRecharges(context.Background(), auth.Principal{Role: domain.UserRoleOrgAdmin}, testRechargeOrgID, 0, 0)
      require.ErrorIs(t, err, ErrRechargeDenied)
  }
  ```

  替换为：

  ```go
  // TestListRecharges_DeniesOrgMember 验证普通成员无权查看充值记录。
  func TestListRecharges_DeniesOrgMember(t *testing.T) {
      store := newRechargeStub(t, "1234")
      svc := NewRechargeService(store, &fakeNewAPIRecharge{})
      // org_member 不在 CanViewRecharges 允许范围内，返回 ErrForbidden。
      _, err := svc.ListRecharges(context.Background(), auth.Principal{Role: domain.UserRoleOrgMember, OrgID: testRechargeOrgID}, testRechargeOrgID, 0, 0)
      require.ErrorIs(t, err, ErrForbidden)
  }
  ```

- [ ] **Step 3: 新增 org_admin 访问充值记录的测试用例**

  在文件末尾（`type rechargeStub struct` 之前）追加：

  ```go
  // TestListRecharges_OrgAdminCanViewOwnOrg 验证 org_admin 可以查看自己组织的充值记录。
  func TestListRecharges_OrgAdminCanViewOwnOrg(t *testing.T) {
      store := newRechargeStub(t, "1234")
      store.records = []sqlc.RechargeRecord{
          {ID: mustUUID(t, "00000000-0000-0000-0000-000000002201"), OrgID: mustUUID(t, testRechargeOrgID), CreditAmount: 100, Status: "succeeded"}, // 场景：org_admin 查询自己组织，应正常返回记录。
      }
      svc := NewRechargeService(store, &fakeNewAPIRecharge{})
      results, err := svc.ListRecharges(context.Background(),
          auth.Principal{Role: domain.UserRoleOrgAdmin, OrgID: testRechargeOrgID}, testRechargeOrgID, 50, 0)
      require.NoError(t, err)
      require.Len(t, results, 1)
  }

  // TestListRecharges_OrgAdminCannotViewOtherOrg 验证 org_admin 无权查看其他组织的充值记录。
  func TestListRecharges_OrgAdminCannotViewOtherOrg(t *testing.T) {
      store := newRechargeStub(t, "1234")
      svc := NewRechargeService(store, &fakeNewAPIRecharge{})
      // org_admin 尝试访问非自己组织，orgID 不匹配，应返回 ErrForbidden。
      _, err := svc.ListRecharges(context.Background(),
          auth.Principal{Role: domain.UserRoleOrgAdmin, OrgID: "other-org-id"}, testRechargeOrgID, 50, 0)
      require.ErrorIs(t, err, ErrForbidden)
  }

  // TestGetBalance_IncludesTotalRecharged 验证 GetBalance 正确聚合并返回累计充值金额。
  func TestGetBalance_IncludesTotalRecharged(t *testing.T) {
      store := newRechargeStub(t, "1234")
      store.totalRecharged = 3000 // 桩返回固定聚合值
      client := &fakeNewAPIRecharge{balanceResult: newapi.BalanceResult{NewAPIUserID: 1234, RemainQuota: 2000}}
      svc := NewRechargeService(store, client)
      view, err := svc.GetBalance(context.Background(), platformAdmin(), testRechargeOrgID)
      require.NoError(t, err)
      // 累计充值金额来自 recharge_records 聚合，不依赖 new-api。
      require.Equal(t, int64(3000), view.TotalRecharged)
      require.Equal(t, int64(2000), view.RemainQuota)
  }
  ```

- [ ] **Step 4: 运行 recharge 相关测试**

  ```bash
  go test ./internal/service/... -run "Recharge|GetBalance|ListRecharge" -v 2>&1 | tail -30
  ```

  预期：所有测试 PASS，无 FAIL。

- [ ] **Step 5: Commit**

  ```bash
  git add internal/service/recharge_service_test.go
  git commit -m "$(cat <<'EOF'
  test(recharge): 更新测试 stub 并补充 org_admin 权限和 TotalRecharged 用例

  rechargeStub 实现 SumRechargeAmountByOrg 接口方法；将原
  TestListRecharges_DeniesNonPlatformAdmin 更新为 TestListRecharges_DeniesOrgMember
  以匹配新的错误类型；新增三个测试覆盖 org_admin 访问自己组织、越权访问以及
  GetBalance 正确返回 TotalRecharged。
  EOF
  )"
  ```

---

## Task 5: OpenAPI + 前端类型同步

**Files:**
- Generated: `openapi/openapi.yaml`
- Generated: `web/src/api/generated.ts`

- [ ] **Step 1: 重新生成 OpenAPI yaml**

  ```bash
  make openapi-gen
  ```

  预期：`openapi/openapi.yaml` 中 `service.BalanceView` schema 新增 `total_recharged` 字段。

- [ ] **Step 2: 校验 yaml 更新正确**

  ```bash
  grep -A5 "total_recharged" openapi/openapi.yaml
  ```

  预期：输出 `total_recharged: { type: integer, format: int64 }` 或类似定义。

- [ ] **Step 3: 生成前端 TypeScript 类型**

  ```bash
  make web-types-gen
  ```

  预期：`web/src/api/generated.ts` 中 `BalanceView` 接口包含 `total_recharged` 字段。

- [ ] **Step 4: 校验前端类型更新**

  ```bash
  grep -A5 "total_recharged" web/src/api/generated.ts
  ```

  预期：输出字段定义。

- [ ] **Step 5: Commit**

  ```bash
  git add openapi/openapi.yaml web/src/api/generated.ts
  git commit -m "$(cat <<'EOF'
  chore(openapi): 同步 BalanceView 新增 total_recharged 字段到 API 文档和前端类型
  EOF
  )"
  ```

---

## Task 6: 前端 — 更新 BalanceDTO 和 useRechargesQuery

**Files:**
- Modify: `web/src/api/hooks/useRecharge.ts`

- [ ] **Step 1: 在 `BalanceDTO` 中添加 `total_recharged` 字段**

  找到第 30-38 行的 `BalanceDTO` 接口，替换为：

  ```typescript
  // BalanceDTO 是组织在 new-api 中的余额快照，附带本地聚合的累计充值金额。
  export interface BalanceDTO {
    // new-api 用户 ID。
    newapi_user_id: number
    // 剩余额度（实时从 new-api 查询）。
    remain_quota: number
    // 已用额度。
    used_quota: number
    // 累计充值金额（来自 manager recharge_records 聚合，仅计 succeeded 记录）。
    total_recharged: number
  }
  ```

- [ ] **Step 2: 前端类型检查**

  ```bash
  make web-typecheck
  ```

  预期：无报错。

- [ ] **Step 3: Commit**

  ```bash
  git add web/src/api/hooks/useRecharge.ts
  git commit -m "$(cat <<'EOF'
  feat(web): BalanceDTO 新增 total_recharged 字段

  对应后端 BalanceView 的 total_recharged，来源为 manager 本地 recharge_records 聚合。
  EOF
  )"
  ```

---

## Task 7: 前端 — 组织列表页：余额列 + 充值记录弹窗

**Files:**
- Modify: `web/src/pages/platform/OrganizationsPage.vue`

- [ ] **Step 1: 添加 useQueries import 和批量余额查询**

  在 `<script setup>` 的 import 块中添加 `useQueries`：

  ```typescript
  import { useMutation, useQueries, useQuery, useQueryClient } from '@tanstack/vue-query'
  ```

  在 `OrganizationsPage` 现有组织数据查询之后、columns 定义之前，添加批量余额和充值记录查询逻辑：

  ```typescript
  // orgBalanceQueries 对列表中每个组织并发查询余额，orgId 变化时自动重建查询集合。
  const orgBalanceQueries = useQueries({
    queries: computed(() =>
      (organizations.value ?? []).map(org => ({
        queryKey: ['org-balance', org.id] as const,
        queryFn: async () => {
          const res = await apiRequest<{ balance: BalanceDTO }>(`/api/v1/organizations/${org.id}/balance`)
          return res.balance
        },
      }))
    ),
  })

  // balanceByOrgId 把 useQueries 的数组结果转成 orgId → BalanceDTO 映射，供列渲染器使用。
  const balanceByOrgId = computed(() => {
    const map: Record<string, BalanceDTO | undefined> = {}
    ;(organizations.value ?? []).forEach((org, i) => {
      map[org.id] = orgBalanceQueries.value[i]?.data ?? undefined
    })
    return map
  })

  // rechargeHistoryVisible 控制充值记录弹窗（与已有充值弹框 rechargeVisible 独立）。
  const rechargeHistoryVisible = ref(false)
  const rechargeHistoryOrg = ref<Organization | null>(null)
  const rechargeHistoryOrgId = computed(() => rechargeHistoryOrg.value?.id)
  const rechargeHistoryBalanceQuery = useOrgBalanceQuery(rechargeHistoryOrgId)
  const rechargeHistoryBalance = computed(() => rechargeHistoryBalanceQuery.data.value ?? null)
  const { data: rechargeHistoryRecords, isLoading: rechargeHistoryLoading } = useRechargesQuery(rechargeHistoryOrgId)

  function openRechargeHistory(org: Organization) {
    rechargeHistoryOrg.value = org
    rechargeHistoryVisible.value = true
  }
  ```

  还需要补充 `apiRequest`、`BalanceDTO`、`useRechargesQuery` 的 import：

  ```typescript
  import { apiRequest } from '@/api/client'
  import { useBillingStatusQuery, useOrgBalanceQuery, useRechargeMutation, useRechargesQuery } from '@/api/hooks/useRecharge'
  import type { BalanceDTO } from '@/api/hooks/useRecharge'
  ```

  （原有的 `useOrgBalanceQuery` / `useRechargeMutation` / `useBillingStatusQuery` 已在现有 import 中，上面合并后整体替换原 import 行即可）

- [ ] **Step 2: 将 columns 改为 computed 并添加余额列**

  将原来的 `const columns = [...]` 改为 `const columns = computed(() => [...])` 并添加两列：

  ```typescript
  const columns = computed(() => [
    {
      title: '名称',
      key: 'name',
      render: (row: Organization) => [
        h('strong', row.name),
        row.remark
          ? h('small', { class: 'data-table-subtitle' }, row.remark)
          : null,
      ],
    },
    { title: '组织标识', key: 'code', render: (row: Organization) => row.code || '—' },
    statusColumn<Organization>('状态', r => formatOrgStatus(r.status)),
    { title: '联系人', key: 'contact_name', render: (row: Organization) => row.contact_name || '—' },
    { title: '电话', key: 'contact_phone', render: (row: Organization) => row.contact_phone || '—' },
    {
      title: '预警阈值',
      key: 'credit_warning_threshold',
      render: (row: Organization) => typeof row.credit_warning_threshold === 'number'
        ? `${row.credit_warning_threshold}%` : '—',
    },
    // 当前余额列：从并发查询结果映射到对应行，未加载时显示省略号。
    {
      title: '当前余额',
      key: 'remain_quota',
      render: (row: Organization) => {
        const b = balanceByOrgId.value[row.id]
        if (!b) return '…'
        return formatQuotaValue(b.remain_quota, billingStatus.value)
      },
    },
    actionColumn<Organization>([
      { label: '复制信息', onClick: r => { void copyOrganizationInfo(r) } },
      { label: '充值记录', onClick: openRechargeHistory },
      { label: '充值', type: 'primary', onClick: openRecharge },
      { label: '禁用', onClick: r => onToggle(r, 'disable'), hidden: r => r.status !== 'active' },
      { label: '启用', type: 'primary', onClick: r => onToggle(r, 'enable'), hidden: r => r.status === 'active' },
    ]),
  ])
  ```

  注意：DataTableList 接收 `:columns` 时如果已经是 computed，直接传 `columns` 即可（Naive UI 的 column prop 是响应式的）。

- [ ] **Step 3: 在模板中添加充值记录弹窗**

  在组织充值弹框 `</n-modal>` 之后、`</div>` 之前，添加新的充值记录弹窗：

  ```html
  <!-- 充值记录弹窗 -->
  <n-modal
    v-model:show="rechargeHistoryVisible"
    preset="card"
    style="max-width: 720px"
    :title="rechargeHistoryOrg ? `充值记录 · ${rechargeHistoryOrg.name}` : '充值记录'"
  >
    <div v-if="rechargeHistoryOrg" style="display: grid; gap: 16px">
      <!-- 概况卡片 -->
      <n-grid :cols="2" :x-gap="14">
        <n-grid-item>
          <n-statistic label="累计充值金额">
            <template v-if="rechargeHistoryBalanceQuery.isLoading.value">—</template>
            <template v-else-if="rechargeHistoryBalance">
              {{ formatQuotaValue(rechargeHistoryBalance.total_recharged, billingStatus) }}
            </template>
            <template v-else>查询失败</template>
          </n-statistic>
        </n-grid-item>
        <n-grid-item>
          <n-statistic label="当前剩余金额">
            <template v-if="rechargeHistoryBalanceQuery.isLoading.value">—</template>
            <template v-else-if="rechargeHistoryBalance">
              {{ formatQuotaValue(rechargeHistoryBalance.remain_quota, billingStatus) }}
            </template>
            <template v-else>查询失败</template>
          </n-statistic>
        </n-grid-item>
      </n-grid>
      <!-- 充值记录表格 -->
      <div v-if="rechargeHistoryLoading" class="state-text">加载中…</div>
      <n-data-table
        v-else
        size="small"
        :columns="rechargeHistoryColumns"
        :data="rechargeHistoryRecords ?? []"
        :pagination="{ pageSize: 10 }"
      />
    </div>
  </n-modal>
  ```

- [ ] **Step 4: 定义充值记录表格列（平台管理员视角，含操作人）**

  在 script setup 末尾，`columns` computed 后面添加：

  ```typescript
  // rechargeHistoryColumns 是充值记录弹窗的表格列定义；含操作人 ID（平台管理员可见）。
  const rechargeHistoryColumns = [
    { title: '时间', key: 'created_at', render: (r: { created_at: string }) => r.created_at.replace('T', ' ').slice(0, 19) },
    {
      title: '金额',
      key: 'credit_amount',
      render: (r: { credit_amount: number }) => formatDisplayAmount(r.credit_amount, billingStatus.value),
    },
    { title: '备注', key: 'remark', render: (r: { remark?: string }) => r.remark || '—' },
    {
      title: '状态',
      key: 'status',
      render: (r: { status: string }) => r.status === 'succeeded' ? '成功' : '失败',
    },
    { title: '操作人', key: 'operator_id', render: (r: { operator_id?: string }) => r.operator_id ? r.operator_id.slice(0, 8) + '…' : '—' },
  ]
  ```

- [ ] **Step 5: 补充 Naive UI 组件引入**

  确保 `NDataTable`、`NStatistic` 已加入 import：

  ```typescript
  import {
    NButton, NCard, NDataTable, NForm, NFormItem, NGrid, NGridItem,
    NInput, NInputNumber, NModal, NSelect, NSpace, NStatistic,
  } from 'naive-ui'
  ```

- [ ] **Step 6: 前端类型检查**

  ```bash
  make web-typecheck
  ```

  预期：无报错。

- [ ] **Step 7: Commit**

  ```bash
  git add web/src/pages/platform/OrganizationsPage.vue
  git commit -m "$(cat <<'EOF'
  feat(web): 组织列表新增余额列和充值记录弹窗

  使用 useQueries 对所有组织并发加载余额，表格新增"当前余额"列；
  操作栏新增"充值记录"按钮，点击弹出 Modal 展示累计充值金额、当前余额和分页充值记录表格。
  EOF
  )"
  ```

---

## Task 8: 前端 — 新增 org_admin 账户余额页、路由和导航

**Files:**
- Create: `web/src/pages/org/OrgBalancePage.vue`
- Modify: `web/src/app/router.ts`
- Modify: `web/src/layouts/DashboardLayout.vue`

- [ ] **Step 1: 创建 `OrgBalancePage.vue`**

  新建文件 `web/src/pages/org/OrgBalancePage.vue`，内容如下：

  ```vue
  <template>
    <n-card :bordered="true">
      <template #header>
        <div>
          <p class="eyebrow">Billing · 账户余额</p>
          <h2 style="margin: 0">账户余额</h2>
        </div>
      </template>

      <!-- 概况卡片 -->
      <n-grid :cols="2" :x-gap="14" style="margin-bottom: 24px">
        <n-grid-item>
          <n-statistic label="累计充值金额">
            <template v-if="balanceLoading">—</template>
            <template v-else-if="balance">
              {{ formatQuotaValue(balance.total_recharged, billingStatus) }}
            </template>
            <template v-else class="state-text danger">查询失败</template>
          </n-statistic>
        </n-grid-item>
        <n-grid-item>
          <n-statistic label="当前剩余金额">
            <template v-if="balanceLoading">—</template>
            <template v-else-if="balance">
              {{ formatQuotaValue(balance.remain_quota, billingStatus) }}
            </template>
            <template v-else class="state-text danger">查询失败</template>
          </n-statistic>
        </n-grid-item>
      </n-grid>

      <!-- 充值记录列表 -->
      <div v-if="rechargesLoading" class="state-text">加载中…</div>
      <div v-else-if="rechargesError" class="state-text danger">加载失败：{{ rechargesError.message }}</div>
      <n-data-table
        v-else
        :columns="columns"
        :data="recharges ?? []"
        :pagination="{ pageSize: 15 }"
      />
    </n-card>
  </template>

  <script setup lang="ts">
  import { computed, ref } from 'vue'
  import { NCard, NDataTable, NGrid, NGridItem, NStatistic } from 'naive-ui'

  import { useAuthStore } from '@/stores/auth'
  import { useBillingStatusQuery, useOrgBalanceQuery, useRechargesQuery } from '@/api/hooks/useRecharge'
  import { formatDisplayAmount, formatQuotaValue } from '@/pages/usage/usageFormatting'
  import type { RechargeRecordDTO } from '@/api/hooks/useRecharge'

  // OrgBalancePage 供 org_admin 查看自己组织的余额概况和充值流水，只读，不含充值入口。
  const auth = useAuthStore()

  // orgId 从当前登录用户的 org_id 取得，org_admin 登录后必定有 org_id。
  const orgId = computed(() => auth.user?.org_id ?? undefined)

  const { data: balance, isLoading: balanceLoading } = useOrgBalanceQuery(orgId)
  const { data: billingStatus } = useBillingStatusQuery()
  const { data: recharges, isLoading: rechargesLoading, error: rechargesError } = useRechargesQuery(orgId)

  // columns 是充值记录表格列；org_admin 视角不显示操作人（操作人始终是平台管理员，对 org_admin 无意义）。
  const columns = [
    {
      title: '时间',
      key: 'created_at',
      render: (r: RechargeRecordDTO) => r.created_at.replace('T', ' ').slice(0, 19),
    },
    {
      title: '金额',
      key: 'credit_amount',
      render: (r: RechargeRecordDTO) => formatDisplayAmount(r.credit_amount, billingStatus.value),
    },
    { title: '备注', key: 'remark', render: (r: RechargeRecordDTO) => r.remark || '—' },
    {
      title: '状态',
      key: 'status',
      render: (r: RechargeRecordDTO) => r.status === 'succeeded' ? '成功' : '失败',
    },
  ]
  </script>
  ```

  > **注意：** `auth.user?.org_id` 要求 `AuthUser` 类型有 `org_id` 字段。请先检查 `web/src/api/index.ts` 中 `AuthUser` 的定义以及 `web/src/api/generated.ts` 中对应 schema 字段名。如果字段名不同（如 `orgId` 或 `organization_id`），替换即可。

- [ ] **Step 2: 确认 AuthUser 的 org_id 字段名**

  ```bash
  grep -n "org_id\|orgId\|organization_id" web/src/api/generated.ts | head -10
  grep -n "org_id\|orgId\|organization_id" web/src/api/index.ts | head -10
  ```

  如果字段名与 `org_id` 不一致，相应修改 `OrgBalancePage.vue` 中的 `auth.user?.org_id`。

- [ ] **Step 3: 在 `router.ts` 注册新路由**

  在 `router.ts` 文件顶部 import 块中添加：

  ```typescript
  import OrgBalancePage from '@/pages/org/OrgBalancePage.vue'
  ```

  在路由数组的 `{ path: 'usage', component: UsagePage }` 之后添加：

  ```typescript
  { path: 'balance', component: OrgBalancePage, meta: { allowedRoles: ORG_ADMIN_ONLY } },
  ```

- [ ] **Step 4: 在 `DashboardLayout.vue` 导航中添加"账户余额"菜单项**

  在 `DashboardLayout.vue` 顶部 import 块中，添加 `Wallet` 图标：

  ```typescript
  import {
    BarChart3, BookOpen, Bot, Building2, FileSearch, Gauge,
    LayoutDashboard, LogOut, RefreshCw, Server, Users, Wallet,
  } from 'lucide-vue-next'
  ```

  在 `activeKey` computed 的 `prefixes` 数组中添加 `'/balance'`：

  ```typescript
  const prefixes = [
    '/platform/dashboard',
    '/organizations',
    '/members',
    '/apps',
    '/knowledge',
    '/usage',
    '/balance',
    '/audit-logs',
    '/runtime-nodes',
    '/org/persona',
  ]
  ```

  在 `menuOptions` computed 中，在 `'/usage'` 菜单项之后、`'/audit-logs'` 之前，添加 org_admin 专属的"账户余额"菜单项：

  ```typescript
  items.push(
    { key: '/apps', label: '实例', icon: () => h(Bot, { size: 18 }) },
    { key: '/knowledge', label: '知识库', icon: () => h(BookOpen, { size: 18 }) },
    { key: '/usage', label: '用量', icon: () => h(BarChart3, { size: 18 }) },
  )
  // 账户余额仅对 org_admin 显示；org_member 和 platform_admin 无此入口。
  if (isOrgAdmin.value) {
    items.push({ key: '/balance', label: '账户余额', icon: () => h(Wallet, { size: 18 }) })
  }
  if (!isOrgMember.value) {
    items.push({ key: '/audit-logs', label: '审计', icon: () => h(FileSearch, { size: 18 }) })
  }
  ```

  `isOrgAdmin` 已在 `DashboardLayout.vue` 的 auth store 引入里定义：`const isOrgAdmin = computed(() => auth.isOrgAdmin)`

- [ ] **Step 5: 前端类型检查**

  ```bash
  make web-typecheck
  ```

  预期：无报错。

- [ ] **Step 6: Commit**

  ```bash
  git add web/src/pages/org/OrgBalancePage.vue web/src/app/router.ts web/src/layouts/DashboardLayout.vue
  git commit -m "$(cat <<'EOF'
  feat(web): 新增 org_admin 账户余额页，注册路由并配置导航

  新建 OrgBalancePage.vue，展示累计充值金额、当前剩余金额和分页充值记录；
  router.ts 注册 /balance 路由（仅限 org_admin）；
  DashboardLayout 导航为 org_admin 新增"账户余额"菜单项。
  EOF
  )"
  ```

---

## Task 9: 浏览器验证（含问题修复）

**目标：** 全面验证功能在浏览器中正常工作，发现问题立即修复再验证。

- [ ] **Step 1: 启动本地服务**

  ```bash
  docker compose up -d
  ```

  等待服务就绪（通常 10-30 秒）。

- [ ] **Step 2: 以平台管理员登录验证余额列和充值记录弹窗**

  浏览器打开 `http://localhost:3000`，以 `admin` / `admin123`（组织标识留空）登录。

  验证清单：
  - [ ] 进入"组织"页面，表格含"当前余额"列，显示各组织余额（加载中时显示"…"）
  - [ ] 点击"充值记录"按钮，弹窗出现，展示累计充值金额 + 当前余额 + 充值记录列表
  - [ ] 充值记录弹窗分页正常（超过 10 条时显示分页器）
  - [ ] 充值记录中时间、金额、状态展示正确

- [ ] **Step 3: 以组织管理员登录验证"账户余额"页**

  退出登录，以 `test-org` / `test-org` / `test-org123` 登录（组织标识 `test-org`）。

  验证清单：
  - [ ] 左侧导航出现"账户余额"菜单项
  - [ ] 点击进入页面，显示累计充值金额和当前剩余金额两张卡片
  - [ ] 页面下方显示充值记录列表（无操作人列）
  - [ ] 以组织成员账号 `test-org-user1` / `test-org-user1` 登录，左侧导航**无**"账户余额"入口

- [ ] **Step 4: 验证 API 权限边界（手动或用浏览器开发者工具）**

  以 org_admin 登录后，在浏览器开发者工具 Console 中执行：

  ```javascript
  // 尝试访问其他组织的余额（应返回 403）
  fetch('/api/v1/organizations/00000000-0000-0000-0000-000000000000/recharges', {
    headers: { Authorization: 'Bearer ' + JSON.parse(localStorage.getItem('manager_auth') ?? '{}').accessToken }
  }).then(r => r.json()).then(console.log)
  ```

  预期：返回 HTTP 403，响应体含 `RECHARGE_FORBIDDEN`。

- [ ] **Step 5: 修复发现的问题**

  如上述验证中发现任何 UI 或接口问题，立即修复并重新验证直到所有项通过。提交修复时使用 `fix(...)` commit 类型。
