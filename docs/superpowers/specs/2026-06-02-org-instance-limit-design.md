# 企业实例数量上限 — 设计文档

- 日期：2026-06-02
- 状态：已确认，待实现
- 作者：hujing + Claude

## 背景与目标

平台管理员希望在创建 / 编辑企业（organization）时，限制该企业最多可创建的「实例」数量。
超过上限后，企业内不得再新建实例。

## 概念定义

- **实例 = `apps` 表中 `deleted_at IS NULL` 的应用记录。**
  每个应用归属一个成员（`owner_user_id`），且受唯一约束 `uk_apps_owner_active` 限制——
  每个成员同一时间最多一个未删除应用。
- **企业当前实例数** = 该企业下 `deleted_at IS NULL` 的应用数：
  `SELECT COUNT(*) FROM apps WHERE org_id = ? AND deleted_at IS NULL`。
- 软删除应用会**释放名额**（因为只统计未删除应用）。

## 语义决策（已确认）

1. **留空 = 不限制。** `organizations.max_instance_count` 为 `NULL` 表示无上限；
   正整数表示上限。存量企业默认 `NULL`，不影响现有行为。
2. **上限可低于当前实例数。** 平台管理员把上限改到比当前实例数还低时**允许保存**，
   已有实例不受影响，仅阻止后续新建，直到实例数降到上限以下。
3. **并发：接受轻微 race。** 两个并发建实例事务理论上可能都读到 `count=N` 各插一条而
   轻微越限。鉴于这是平台 / 企业管理员的手动低频操作，接受该 race，不加行锁。
4. **错误码：超限返回 409 Conflict。**

## 创建实例的两个入口（都需校验）

1. `POST /api/v1/organizations/:orgId/members/onboard` —— `OnboardMember`：同时创建成员 + 应用。
2. `POST /api/v1/organizations/:orgId/members/:userId/apps` —— `CreateAppForMember`：给已有成员建新应用。

两个入口都在已有 store 事务内执行，校验逻辑放在事务内。

## 分层实现

### 1. 数据库 migration（新增 `000004`）

文件：`internal/migrations/000004_org_max_instance_count.up.sql` / `.down.sql`

- `up`：`organizations` 增列
  - `max_instance_count INT NULL`
  - CHECK 约束：`max_instance_count IS NULL OR max_instance_count > 0`
- `down`：删列。
- 同步把该 schema 文件追加进 `sqlc.yaml` 的 `schema` 列表。

### 2. sqlc query（`internal/store/queries/`）

- `organizations.sql`：`CreateOrganization` / `UpdateOrganization` 的 INSERT/UPDATE 带上 `max_instance_count`。
- `apps.sql`：新增
  ```sql
  -- name: CountActiveAppsByOrg :one
  SELECT COUNT(*) FROM apps WHERE org_id = ? AND deleted_at IS NULL;
  ```
- 查询 / 列表用 `SELECT *`，自动带出新列。
- 改完跑 `sqlc generate`（或 `make generate`）重新生成 `internal/store/sqlc/`。

### 3. Service 层（`internal/service/organization_service.go` + `onboarding_service.go`）

- `OrganizationInput` / `OrganizationResult` 增 `MaxInstanceCount *int32`，沿用
  `CreditWarningThreshold` 的指针模式（`nil` = 不限制）。
- 创建 / 更新企业时把 `MaxInstanceCount` 写入 / 读出。
- 新增 sentinel error：`ErrInstanceLimitReached`（文案如「已达企业实例上限 (n)」）。
- `OnboardMember` 与 `CreateAppForMember` 在创建应用前，事务内：
  ```text
  if org.MaxInstanceCount != nil {
      count := store.CountActiveAppsByOrg(ctx, org.ID)
      if count >= *org.MaxInstanceCount {
          return ErrInstanceLimitReached
      }
  }
  ```

### 4. Handler DTO + 错误映射（`internal/api/handlers/`）

- `dto.go`：`CreateOrganizationRequest`、`OrganizationRequest`（更新）增
  `MaxInstanceCount *int32 \`json:"max_instance_count"\``。
- `organizations.go`：创建 / 更新时透传该字段。
- 错误映射：`ErrInstanceLimitReached` → **409 Conflict**，沿用现有
  `ErrMemberCreateInvalid` 的映射写法，实现时核对具体位置。

### 5. 前端（`web/src/`）

- `pages/platform/OrganizationsPage.vue`：创建 / 编辑表单各加一个数字输入框
  「最多实例数（留空 = 不限制）」。
- `api/hooks/useOrganizations.ts`：`OrganizationFormPayload` /
  `OrganizationUpdatePayload` 增 `max_instance_count` 字段。
- 建实例被拒（409）时，前端展示后端文案，提示已达上限。
- 跑 `make openapi-gen` + `make web-types-gen` 同步 `openapi/openapi.yaml`
  与 `web/src/api/generated.ts`。

## 测试

### Service 单元测试
- 当前实例数 = limit-1 时建实例：成功。
- 当前实例数 = limit 时建实例：被拒，返回 `ErrInstanceLimitReached`。
- `MaxInstanceCount == nil`（不限制）时建实例：成功。
- 编辑企业把上限改到低于当前实例数：保存成功，已有实例不动。
- 覆盖 `OnboardMember` 与 `CreateAppForMember` 两个入口。

### 浏览器验证（交付前必做）
平台管理员设上限 → 建实例到上限 → 第 n+1 个被拦截并提示已达上限。

## 影响范围

- 新增 migration `000004`，破坏性低（仅加可空列）。
- `organizations` 读写路径、两个建实例入口、企业管理前端表单。
- OpenAPI 契约与前端生成类型需同步提交。

## 不做（YAGNI）

- 不做按实例类型 / 渠道类型分别限额。
- 不做上限变更的历史审计（沿用企业更新已有审计即可）。
- 不为强一致加行锁（已确认接受 race）。
