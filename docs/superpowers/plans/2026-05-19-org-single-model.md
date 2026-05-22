# 组织单模型绑定 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 将模型管理从"组织多模型白名单 + 实例自选"简化为"组织绑定单一模型，实例自动继承"，组织/普通用户完全无法感知模型。

**Architecture:** 数据库层将 `organizations.enabled_models` (jsonb) 替换为 `model_id` (text NOT NULL)；apps 表新增 `model_synced` (bool) 标记重启状态。后端移除实例模型更新接口，创建实例时自动从组织读取模型。前端移除所有非平台管理员的模型展示。

**Tech Stack:** Go (gin, sqlc, pgx), PostgreSQL, Vue 3 (Naive UI, TanStack Query)

---

## File Structure

### 新建文件
- `internal/migrations/000022_org_single_model.up.sql` — schema 迁移
- `internal/migrations/000022_org_single_model.down.sql` — 回滚迁移

### 修改文件（后端）
- `internal/store/queries/organizations.sql` — 改 enabled_models → model_id
- `internal/store/queries/apps.sql` — 新增 SetAppModelSynced、UpdateAppModelsByOrg 查询
- `internal/store/sqlc/` — sqlc 重新生成（models.go, querier.go, *.sql.go）
- `internal/service/organization_service.go` — 适配单模型字段，更新时同步实例
- `internal/service/onboarding_service.go` — 移除 ModelID 入参，自动从组织读取
- `internal/service/app_service.go` — 移除 UpdateModel 方法，AppResult 增加 ModelSynced
- `internal/api/handlers/dto.go` — 请求/响应 DTO 适配
- `internal/api/handlers/apps.go` — 移除 UpdateModel handler 和路由
- `internal/api/handlers/organizations.go` — 响应适配
- `internal/worker/handlers/app_runtime_ops.go` — 重启完成后置 model_synced = true

### 修改文件（前端）
- `web/src/pages/platform/OrganizationsPage.vue` — 多选改单选
- `web/src/pages/org/CreateMemberPage.vue` — 移除模型选择
- `web/src/pages/apps/AppOverviewTab.vue` — 移除模型更换，按角色显隐 model_id
- `web/src/pages/apps/AppsPage.vue` — 增加 model_synced 提示
- `web/src/pages/usage/UsageSummary.vue` — 隐藏 model_name 列
- `web/src/api/hooks/useApps.ts` — 移除 useUpdateAppModel
- `web/src/api/hooks/useOrganizations.ts` — payload 适配

---

### Task 1: 数据库迁移

**Files:**
- Create: `internal/migrations/000022_org_single_model.up.sql`
- Create: `internal/migrations/000022_org_single_model.down.sql`

- [ ] **Step 1: 编写 up 迁移**

```sql
-- 000022_org_single_model.up.sql

-- 1. organizations: enabled_models → model_id
ALTER TABLE organizations DROP COLUMN IF EXISTS enabled_models;
ALTER TABLE organizations ADD COLUMN model_id text NOT NULL DEFAULT '';

-- 2. apps: 新增 model_synced 标记
ALTER TABLE apps ADD COLUMN model_synced boolean NOT NULL DEFAULT true;

-- 3. 移除旧索引（不再需要按 org+model 统计）
DROP INDEX IF EXISTS apps_org_model_active_idx;
```

- [ ] **Step 2: 编写 down 迁移**

```sql
-- 000022_org_single_model.down.sql

ALTER TABLE apps DROP COLUMN IF EXISTS model_synced;
ALTER TABLE organizations DROP COLUMN IF EXISTS model_id;
ALTER TABLE organizations ADD COLUMN enabled_models jsonb NOT NULL DEFAULT '[]';
CREATE INDEX IF NOT EXISTS apps_org_model_active_idx ON apps(org_id, model_id) WHERE deleted_at IS NULL;
```

- [ ] **Step 3: 本地验证迁移**

Run: `go run ./cmd/manager migrate`
Expected: 迁移成功，无报错

- [ ] **Step 4: Commit**

```bash
git add internal/migrations/000022_org_single_model.up.sql internal/migrations/000022_org_single_model.down.sql
git commit -m "feat(db): 组织单模型迁移，enabled_models → model_id，apps 增加 model_synced"
```

---

### Task 2: sqlc 查询文件适配

**Files:**
- Modify: `internal/store/queries/organizations.sql`
- Modify: `internal/store/queries/apps.sql`

- [ ] **Step 1: 修改 organizations.sql**

将 `CreateOrganization` 中 `enabled_models` 替换为 `model_id`：

```sql
-- name: CreateOrganization :one
INSERT INTO organizations (
    name,
    code,
    status,
    contact_name,
    contact_phone,
    remark,
    credit_warning_threshold,
    model_id
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8
)
RETURNING *;
```

将 `UpdateOrganizationProfile` 中 `enabled_models` 替换为 `model_id`：

```sql
-- name: UpdateOrganizationProfile :one
UPDATE organizations
SET
    name = $2,
    contact_name = $3,
    contact_phone = $4,
    remark = $5,
    credit_warning_threshold = $6,
    model_id = $7,
    updated_at = now()
WHERE id = $1
RETURNING *;
```

移除 `CountActiveAppsByOrgAndModels` 查询（不再需要）。

- [ ] **Step 2: 修改 apps.sql**

移除 `SetAppModel` 查询。

新增两个查询：

```sql
-- name: UpdateAppModelsByOrg :exec
-- 组织模型变更时批量同步所有活跃实例的 model_id 并标记需重启。
UPDATE apps
SET model_id = $2,
    model_synced = false,
    updated_at = now()
WHERE org_id = $1
  AND deleted_at IS NULL;

-- name: SetAppModelSynced :one
-- 实例重启完成后标记模型已同步。
UPDATE apps
SET model_synced = true,
    updated_at = now()
WHERE id = $1
RETURNING *;
```

- [ ] **Step 3: 重新生成 sqlc**

Run: `make sqlc-gen`（或项目中等效命令 `sqlc generate`）
Expected: `internal/store/sqlc/` 下文件重新生成，Organization 结构体有 `ModelID string`，App 结构体有 `ModelSynced bool`

- [ ] **Step 4: 验证编译**

Run: `go build ./...`
Expected: 编译失败（service 层引用旧字段），确认 sqlc 生成正确后继续

- [ ] **Step 5: Commit**

```bash
git add internal/store/queries/organizations.sql internal/store/queries/apps.sql internal/store/sqlc/
git commit -m "feat(store): sqlc 查询适配单模型字段，新增 UpdateAppModelsByOrg 和 SetAppModelSynced"
```

---

### Task 3: 后端 service 层适配 — organization_service.go

**Files:**
- Modify: `internal/service/organization_service.go`

- [ ] **Step 1: 修改 OrganizationInput 结构体**

将 `EnabledModels []string` 和 `EnabledModelsSet bool` 替换为：

```go
// ModelID 是该组织所有实例统一使用的模型 ID，由平台管理员指定。
ModelID    string
// ModelIDSet 标记更新请求中是否显式传入了 model_id（用于区分"不修改"和"修改为某值"）。
ModelIDSet bool
```

- [ ] **Step 2: 修改 OrganizationResult 结构体**

将 `EnabledModels []string` 替换为：

```go
ModelID string `json:"model_id"`
```

- [ ] **Step 3: 修改 CreateOrganization 方法**

移除 `validateEnabledModels` 调用和 `modelListJSON` 序列化。替换为：

```go
if err := s.modelValidator.ValidateModelIDs(ctx, []string{input.ModelID}); err != nil {
    return OrganizationResult{}, fmt.Errorf("%w: %v", ErrValidation, err)
}
```

修改 `store.CreateOrganization` 调用，将 `EnabledModels: enabledModelsJSON` 替换为 `ModelID: input.ModelID`。

- [ ] **Step 4: 修改 UpdateOrganization 方法**

移除 `ensureRemovedModelsUnused` 调用。当 `input.ModelIDSet` 为 true 时：
1. 验证新 model_id 有效
2. 调用 `store.UpdateOrganizationProfile`（传入 `ModelID`）
3. 如果 model_id 与旧值不同，调用 `store.UpdateAppModelsByOrg(ctx, org.ID, input.ModelID)` 批量同步实例

```go
if input.ModelIDSet && input.ModelID != current.ModelID {
    if err := s.modelValidator.ValidateModelIDs(ctx, []string{input.ModelID}); err != nil {
        return OrganizationResult{}, fmt.Errorf("%w: %v", ErrValidation, err)
    }
}
// ... UpdateOrganizationProfile ...
if input.ModelIDSet && input.ModelID != current.ModelID {
    if err := s.store.UpdateAppModelsByOrg(ctx, sqlc.UpdateAppModelsByOrgParams{
        OrgID:   org.ID,
        ModelID: input.ModelID,
    }); err != nil {
        return OrganizationResult{}, fmt.Errorf("同步实例模型失败: %w", err)
    }
}
```

- [ ] **Step 5: 修改 toOrganizationResult**

将 `EnabledModels: modelListFromJSON(org.EnabledModels)` 替换为 `ModelID: org.ModelID`。

- [ ] **Step 6: 移除辅助函数**

删除 `validateEnabledModels`、`ensureRemovedModelsUnused`、`modelListJSON`、`modelListFromJSON`（如果仅在此文件使用）。

- [ ] **Step 7: 验证编译**

Run: `go build ./internal/service/...`
Expected: 可能还有 onboarding_service 和 app_service 报错，下一步处理

- [ ] **Step 8: Commit**

```bash
git add internal/service/organization_service.go
git commit -m "feat(service): 组织服务适配单模型，更新时批量同步实例"
```

---

### Task 4: 后端 service 层适配 — onboarding_service.go

**Files:**
- Modify: `internal/service/onboarding_service.go`

- [ ] **Step 1: 修改 OnboardMemberInput 和 CreateAppForMemberInput**

从两个结构体中移除 `ModelID string` 字段及其注释。

- [ ] **Step 2: 修改 OnboardMember 方法**

移除 `ensureModelAllowed(org, input.ModelID)` 调用。直接使用 `org.ModelID`：

```go
// 实例模型直接继承组织配置，无需用户指定。
modelID := org.ModelID
```

将 `CreateApp` 调用中的 `ModelID: modelID` 保持不变（变量来源改了）。

- [ ] **Step 3: 修改 CreateAppForMember 方法**

同样移除 `ensureModelAllowed` 调用，直接使用 `org.ModelID`。

- [ ] **Step 4: 移除 ensureModelAllowed 函数**

删除整个 `ensureModelAllowed` 函数（约 lines 450–461）。

- [ ] **Step 5: 验证编译**

Run: `go build ./internal/service/...`
Expected: 可能 app_service 还有引用 ensureModelAllowed 的报错

- [ ] **Step 6: Commit**

```bash
git add internal/service/onboarding_service.go
git commit -m "feat(service): onboarding 移除模型选择，实例自动继承组织模型"
```

---

### Task 5: 后端 service 层适配 — app_service.go

**Files:**
- Modify: `internal/service/app_service.go`

- [ ] **Step 1: 移除 UpdateModel 方法**

删除整个 `UpdateModel` 方法（约 lines 163–257）。

- [ ] **Step 2: 移除 AppModelUpdateResult 结构体**

删除 `AppModelUpdateResult` 结构体（约 lines 95–101）。

- [ ] **Step 3: AppResult 增加 ModelSynced 字段**

```go
// ModelSynced 标记实例运行中的模型是否与数据库记录一致；false 表示需重启生效。
ModelSynced bool `json:"model_synced"`
```

- [ ] **Step 4: 修改 toAppResult 转换函数**

在构建 AppResult 时增加 `ModelSynced: app.ModelSynced`。

- [ ] **Step 5: model_id 按角色过滤**

在返回 AppResult 的地方（List、Get），如果调用者不是 platform_admin，将 `ModelID` 置空：

```go
func (s *AppService) filterAppResult(result AppResult, principal auth.Principal) AppResult {
    if principal.Role != auth.RolePlatformAdmin {
        result.ModelID = ""
    }
    return result
}
```

在 List 和 Get 方法返回前调用此过滤。

- [ ] **Step 6: 验证编译**

Run: `go build ./internal/...`
Expected: handlers/apps.go 报错（引用了已删除的 UpdateModel），下一步处理

- [ ] **Step 7: Commit**

```bash
git add internal/service/app_service.go
git commit -m "feat(service): 移除实例模型更新，AppResult 增加 model_synced 和角色过滤"
```

---

### Task 6: 后端 handler 和路由适配

**Files:**
- Modify: `internal/api/handlers/dto.go`
- Modify: `internal/api/handlers/apps.go`
- Modify: `internal/api/handlers/organizations.go`

- [ ] **Step 1: 修改 dto.go**

`CreateOrganizationRequest`：将 `EnabledModels []string` 替换为 `ModelID string \`json:"model_id" binding:"required"\``。

`OrganizationRequest`（update）：将 `EnabledModels *[]string` 替换为 `ModelID *string \`json:"model_id"\``。

`OnboardMemberRequest`：移除 `ModelID string \`json:"model_id" binding:"required"\`` 字段。

`CreateMemberAppRequest`：移除 `ModelID string \`json:"model_id" binding:"required"\`` 字段。

删除 `UpdateAppModelRequest` 结构体。

- [ ] **Step 2: 修改 apps.go**

移除 `UpdateModel` handler 方法。

修改 `RegisterAppRoutes`，移除 `router.PATCH("/api/v1/apps/:appId/model", handler.UpdateModel)` 行。

- [ ] **Step 3: 修改 organizations.go**

在 handler 中将 `EnabledModels` 映射改为 `ModelID` 映射。创建组织时：

```go
input := service.OrganizationInput{
    // ...
    ModelID: req.ModelID,
}
```

更新组织时：

```go
if req.ModelID != nil {
    input.ModelID = *req.ModelID
    input.ModelIDSet = true
}
```

- [ ] **Step 4: 修改 onboarding handler**

在 `OnboardMember` 和 `CreateMemberApp` handler 中，移除从请求体读取 `ModelID` 并传入 input 的代码。

- [ ] **Step 5: 验证编译**

Run: `go build ./...`
Expected: 编译通过

- [ ] **Step 6: Commit**

```bash
git add internal/api/handlers/dto.go internal/api/handlers/apps.go internal/api/handlers/organizations.go
git commit -m "feat(api): handler 适配单模型，移除实例模型更新接口"
```

---

### Task 7: Worker 重启完成后置 model_synced

**Files:**
- Modify: `internal/worker/handlers/app_runtime_ops.go`

- [ ] **Step 1: 在 AppRestartContainerHandler.Handle 末尾增加 SetAppModelSynced 调用**

在 `SetAppStatus` 成功后、`return nil` 前：

```go
if _, err := h.store.SetAppModelSynced(ctx, sqlc.SetAppModelSyncedParams{ID: app.ID}); err != nil {
    return fmt.Errorf("标记模型同步状态失败: %w", err)
}
```

- [ ] **Step 2: 确认 store 接口包含新方法**

检查 `AppRuntimeStore` 接口（该文件顶部），确保包含 `SetAppModelSynced` 方法签名。如果接口定义在别处，需要在对应接口中添加。

- [ ] **Step 3: 验证编译**

Run: `go build ./internal/worker/...`
Expected: 编译通过

- [ ] **Step 4: Commit**

```bash
git add internal/worker/handlers/app_runtime_ops.go
git commit -m "feat(worker): 实例重启完成后标记 model_synced = true"
```

---

### Task 8: OpenAPI 和前端类型重新生成

**Files:**
- Regenerate: `openapi/openapi.yaml`
- Regenerate: `web/src/api/generated.ts`

- [ ] **Step 1: 更新 swag 注解**

确认 handler 函数的 swag 注解与新 DTO 一致（CreateOrganizationRequest 的 model_id 字段、移除 UpdateAppModelRequest 等）。

- [ ] **Step 2: 生成 OpenAPI**

Run: `make openapi-gen`
Expected: `openapi/openapi.yaml` 更新，反映新字段

- [ ] **Step 3: 生成前端类型**

Run: `make web-types-gen`
Expected: `web/src/api/generated.ts` 更新

- [ ] **Step 4: Commit**

```bash
git add openapi/openapi.yaml web/src/api/generated.ts
git commit -m "chore(openapi): 重新生成 API 契约和前端类型"
```

---

### Task 9: 前端 — 创建组织表单改单选

**Files:**
- Modify: `web/src/pages/platform/OrganizationsPage.vue`
- Modify: `web/src/api/hooks/useOrganizations.ts`

- [ ] **Step 1: 修改 OrganizationsPage.vue**

将模型 `<n-select>` 从 `multiple` 改为单选：

```vue
<n-select
  v-model:value="form.model_id"
  :options="modelOptions"
  placeholder="选择模型"
/>
```

表单数据中 `enabled_models: []` 改为 `model_id: ''`。

提交校验中 `enabled_models.length === 0` 改为 `!form.model_id`。

- [ ] **Step 2: 修改 useOrganizations.ts**

`OrganizationFormPayload` 中 `enabled_models: string[]` 改为 `model_id: string`。

- [ ] **Step 3: 验证**

Run: `npm run type-check`（在 web/ 目录）
Expected: 类型检查通过

- [ ] **Step 4: Commit**

```bash
git add web/src/pages/platform/OrganizationsPage.vue web/src/api/hooks/useOrganizations.ts
git commit -m "feat(web): 创建组织表单模型选择改为单选"
```

---

### Task 10: 前端 — 创建成员页面移除模型选择

**Files:**
- Modify: `web/src/pages/org/CreateMemberPage.vue`

- [ ] **Step 1: 移除模型相关 UI 和逻辑**

删除：
- 模型 `<n-select>` 组件及其 `<n-form-item>` 包裹
- `modelOptions` computed
- `modelSelectError` computed
- `watch(modelOptions, ...)` 自动选择逻辑
- 表单数据中的 `model_id` 字段
- 提交 payload 中的 `model_id`

- [ ] **Step 2: 验证**

Run: `npm run type-check`
Expected: 类型检查通过

- [ ] **Step 3: Commit**

```bash
git add web/src/pages/org/CreateMemberPage.vue
git commit -m "feat(web): 创建成员页面移除模型选择"
```

---

### Task 11: 前端 — 实例详情移除模型更换，按角色显隐

**Files:**
- Modify: `web/src/pages/apps/AppOverviewTab.vue`
- Modify: `web/src/api/hooks/useApps.ts`

- [ ] **Step 1: 移除 AppOverviewTab.vue 中模型更换功能**

删除：
- 模型更换的 `<n-select>` 和提交按钮
- `modelOptions` computed
- `modelValue` ref
- `useUpdateAppModel` 调用
- `watch(() => app?.value?.model_id, ...)` 同步逻辑

保留 model_id 的只读展示，但用 `v-if="isPlatformAdmin"` 包裹（仅平台管理员可见）：

```vue
<n-descriptions-item v-if="isPlatformAdmin" label="模型">
  {{ app.model_id }}
</n-descriptions-item>
```

- [ ] **Step 2: 移除 useApps.ts 中 useUpdateAppModel**

删除 `useUpdateAppModel` 函数和 `AppModelUpdateResult` 类型。

- [ ] **Step 3: 验证**

Run: `npm run type-check`
Expected: 类型检查通过

- [ ] **Step 4: Commit**

```bash
git add web/src/pages/apps/AppOverviewTab.vue web/src/api/hooks/useApps.ts
git commit -m "feat(web): 实例详情移除模型更换，model_id 仅平台管理员可见"
```

---

### Task 12: 前端 — 实例列表增加"需重启"提示

**Files:**
- Modify: `web/src/pages/apps/AppsPage.vue`

- [ ] **Step 1: 在状态列后增加 model_synced 提示**

在 `columns` 定义中，状态列的 render 函数里或新增一列，当 `model_synced === false` 时显示警告标签：

```ts
statusColumn<AppDTO>('状态', r => {
  const status = formatAppStatus(r.status)
  if (!r.model_synced) return `${status} (模型已变更，需重启)`
  return status
}),
```

或者用 Naive UI 的 `NTag` 组件在状态旁显示橙色标签。具体实现取决于 `statusColumn` helper 是否支持自定义 render——如果不支持，改为普通列定义。

- [ ] **Step 2: 验证**

Run: `npm run type-check`
Expected: 类型检查通过

- [ ] **Step 3: Commit**

```bash
git add web/src/pages/apps/AppsPage.vue
git commit -m "feat(web): 实例列表 model_synced=false 时显示需重启提示"
```

---

### Task 13: 前端 — 用量页面隐藏模型列

**Files:**
- Modify: `web/src/pages/usage/UsageSummary.vue`

- [ ] **Step 1: 移除 model_name 列**

在 `tableColumns` computed 中删除 `{ title: 'model_name', key: 'model_name' }` 行。

- [ ] **Step 2: 验证**

Run: `npm run type-check`
Expected: 类型检查通过

- [ ] **Step 3: Commit**

```bash
git add web/src/pages/usage/UsageSummary.vue
git commit -m "feat(web): 用量页面隐藏模型列"
```

---

### Task 14: 端到端验证

- [ ] **Step 1: 后端编译和测试**

Run: `go build ./... && go test ./internal/...`
Expected: 编译通过，测试通过（部分测试可能需要适配）

- [ ] **Step 2: 前端类型检查**

Run: `cd web && npm run type-check`
Expected: 无类型错误

- [ ] **Step 3: 启动服务并浏览器验证**

启动 dev server，验证：
1. 创建组织时可选择单个模型
2. 创建成员时无模型选择
3. 实例详情中平台管理员可见 model_id，组织管理员不可见
4. 用量页面无模型列
5. 修改组织模型后实例列表显示"需重启"提示

- [ ] **Step 4: OpenAPI 一致性检查**

Run: `make openapi-check`
Expected: 工作区干净，openapi.yaml 与代码同步
