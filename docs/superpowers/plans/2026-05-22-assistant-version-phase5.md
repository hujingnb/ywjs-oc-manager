# 助手版本 Phase 5：死代码与死 schema 清理

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax.

**Goal:** 助手版本特性（Phase 1–4）上线后，旧的「组织级模型 / 组织级人设 / 实例级人设」机制已被「版本完全接管」取代。Phase 5 删除这批死代码与死 schema，让仓库不再保留误导性的过期字段。

**Architecture:** 纯删除。无新功能。逐个「可删除单元」推进：先删 Go / 前端对字段的所有引用（此时 sqlc 生成结构体仍含该字段，只是无人用，编译保持绿），再 drop 列 + 重新生成 sqlc（无人引用，编译仍绿）。人设特性自上而下删除（前端 → 路由/handler → service → store → auth/errors → 表），每层删完无悬挂调用方。每个任务结束 `go build ./...` + `go test ./...` 全绿；前端任务额外跑 `make web-typecheck` + `make web-test`。迁移只追加，从 `000025` 起。

**Tech Stack:** Go、pgx/v5、sqlc、golang-migrate、Vue 3 + naive-ui。

## 调研结论（执行前须知）

- `organizations.enabled_models` 与 `apps_org_model_active_idx` **已被 `000022` 删除**，无需再处理 schema；但 `cmd/seed-e2e/main.go` 仍 INSERT `enabled_models` / TRUNCATE `organization_personas` / 写 `apps.persona_mode`/`model_id` —— seed-e2e 当前已运行期损坏，Phase 5 顺带修复（Task 8）。
- `OrganizationModelValidator` / `modelValidator` / `SetModelValidator` / `OrganizationStore.UpdateAppModelsByOrg` / `ModelCatalogService.ValidateModelIDs` 是「声明但从不调用」的死代码（`UpdateOrganization` 直接透传 `current.ModelID`）。`cmd/server/main.go` 仍调 `SetModelValidator`。
- **`ModelCatalogService` / `/api/v1/models` / `HasModelInCatalog` 仍在用**（助手版本页的主模型 / 路由 select），**不删**。只删 `ValidateModelIDs`。
- **hermes 包的 `PersonaText` / `RenderPersonaText` / `resources/persona.md` / manifest `Persona` 字段不删** —— 它们承载「版本内置提示词」（Phase 4）。Phase 5 只删 `organization_personas` 表那套「组织人设」特性。`app_initialize.go` 有一处提到 `app.persona / org.persona` 的过期注释，需改。
- `apps.persona_mode` / `app_prompt` 已是纯遗留列：仅沿 request DTO → onboarding → `CreateAppParams` → `AppResult` → 前端展示流动，worker / manifest 不再读；前端 `CreateMemberPage.vue` 创建表单根本不发这两个字段。
- `apps.model_synced` 仍被 restart handler 调用（`app_runtime_ops.go` 的 `SetAppModelSynced`，Phase 4 故意保留）。Phase 5 删列时一并删调用点、`AppRuntimeStore` 接口方法、3 个测试桩、sqlc query。
- `cfg.Hermes.RuntimeImage` 的所有读取点都是 fallback（`ResolveRuntimeImage` 为 nil 时才用，生产恒注入），确认为死代码。
- 最新迁移号 `000024`，Phase 5 迁移用 `000025`–`000028`。

---

### Task 1：前端删除组织 AI 人设页

**Files:** `web/src/pages/org/PersonaPage.vue`、`web/src/api/hooks/usePersona.ts`、`web/src/router.ts`（`org/persona` 路由）、`web/src/layouts/DashboardLayout.vue`（导航项）、`web/src/pages/dashboard/RoleAwareHome.vue`（入口卡片）

- [ ] 删除 `PersonaPage.vue` 与 `usePersona.ts`（及其 `.spec.ts` 若有）。
- [ ] 删除 `router.ts` 中 `org/persona` 路由、`DashboardLayout.vue` 与 `RoleAwareHome.vue` 中「AI 人设」导航 / 入口。
- [ ] 验证：`make web-typecheck`、`make web-test` 全绿。
- [ ] Commit：`refactor(assistant-version): 前端删除组织 AI 人设页`

### Task 2：后端删除组织人设特性 + 删表

**Files:** `internal/api/handlers/persona.go`(+`persona_test.go`)、`internal/api/router.go`、`internal/service/persona_service.go`(+test)、`internal/store/persona_store.go`、`internal/store/queries/organization_personas.sql`、`internal/auth/authorizer.go`(+test)、`internal/service/errors.go`、`cmd/server/main.go`、新增迁移 `000025`

- [ ] 删 `persona.go` + 路由注册（`router.go` 的 `PersonaService` 依赖字段与 `RegisterPersonaRoutes`）。
- [ ] 删 `persona_service.go`、`persona_store.go`、`organization_personas.sql`。
- [ ] 删 `auth.CanViewOrgPersona` / `CanManageOrgPersona`（及 authorizer 测试用例）、`service.ErrPersonaNotFound` / `ErrPersonaDenied`、`cmd/server/main.go` 的 persona 装配。
- [ ] `make sqlc-generate` 移除 `organization_personas.sql.go`。
- [ ] 迁移 `000025` drop `organization_personas` 表。
- [ ] 验证：`go build ./...`、`go test ./...`、`go vet ./...` 全绿。
- [ ] Commit：`refactor(assistant-version): 删除组织 AI 人设特性`

### Task 3：移除 `apps.persona_mode` / `app_prompt` 死列

**Files:** `internal/service/app_service.go`、`internal/service/onboarding_service.go`、`internal/api/handlers/dto.go`、`internal/api/handlers/members.go`、`internal/domain/enums.go`、`internal/store/queries/apps.sql`、相关 service/handler 测试、新增迁移 `000026`

- [ ] 从 `AppResult` + `toAppResult` 删 `PersonaMode` / `AppPrompt`。
- [ ] 从 `OnboardMemberInput` / `CreateAppForMemberInput` 与 `CreateAppParams` 调用删这两字段；删请求 DTO（`OnboardMemberRequest` / `CreateMemberAppRequest` / `CreateAppRequest`）对应字段与 `members.go` 装配；删 `domain.PersonaModeOrgInherited` / `PersonaModeAppOverride` 及 onboarding 的 personaMode 默认值逻辑。
- [ ] `apps.sql` 的 `CreateApp` 去掉这两列，`make sqlc-generate`。
- [ ] 迁移 `000026` drop `apps.persona_mode` + `apps.app_prompt`。
- [ ] 更新受影响测试。验证：`go build`/`go test`/`go vet` 全绿。
- [ ] Commit：`refactor(assistant-version): 移除 apps.persona_mode/app_prompt 死列`

### Task 4：移除 `apps.model_id` / `model_synced` 死列与死查询

**Files:** `internal/service/app_service.go`、`internal/service/onboarding_service.go`、`internal/worker/handlers/app_runtime_ops.go`、worker 测试桩（`app_runtime_ops_test.go`、`runtime_refresh_status_test.go`、`app_health_check_test.go`）、`internal/store/queries/apps.sql`、新增迁移 `000027`

- [ ] 从 `AppResult` / `toAppResult` / `filterAppResultByRole` 删 `ModelID` / `ModelSynced`；`onboarding_service.go` 删 `modelID := org.ModelID` 与 `CreateAppParams.ModelID`。
- [ ] restart handler 删 `SetAppModelSynced` 调用、`AppRuntimeStore` 接口删该方法、3 个测试桩删对应实现。
- [ ] `apps.sql` 删 `UpdateAppModelsByOrg` + `SetAppModelSynced` 查询，`CreateApp` 去掉 `model_id`，`make sqlc-generate`。
- [ ] 迁移 `000027` drop `apps.model_id` + `apps.model_synced`。
- [ ] 更新测试。验证：`go build`/`go test`/`go vet` 全绿。
- [ ] Commit：`refactor(assistant-version): 移除 apps.model_id/model_synced 死列与查询`

### Task 5：移除 `organizations.model_id` 死列

**Files:** `internal/service/organization_service.go`、`internal/service/onboarding_service.go`、`internal/api/handlers/dto.go`、`internal/api/handlers/organizations.go`、`internal/store/queries/organizations.sql`、org service/handler 测试、新增迁移 `000028`

- [ ] 从 `OrganizationInput` / `OrganizationResult` / `toOrganizationResult` 删 `ModelID` / `ModelIDSet`，`CreateOrganization` / `UpdateOrganization` 删该参数装配。
- [ ] 删 `CreateOrganizationRequest.ModelID` / `OrganizationRequest.ModelID` 及 `organizations.go` 装配。
- [ ] `organizations.sql` 的 `CreateOrganization` / `UpdateOrganizationProfile` 去掉 `model_id`，`make sqlc-generate`。
- [ ] 迁移 `000028` drop `organizations.model_id`。
- [ ] 更新测试。验证：`go build`/`go test`/`go vet` 全绿。
- [ ] Commit：`refactor(assistant-version): 移除 organizations.model_id 死列`

### Task 6：删除组织模型校验器死代码

**Files:** `internal/service/organization_service.go`、`internal/service/model_service.go`(+test)、`cmd/server/main.go`、org service 测试桩

- [ ] 删 `OrganizationModelValidator` 接口、`modelValidator` 字段、`SetModelValidator`、`OrganizationStore.UpdateAppModelsByOrg`、`cmd/server/main.go` 的 `SetModelValidator` 调用。
- [ ] 删 `ModelCatalogService.ValidateModelIDs` 及其测试、org service 测试里的 `orgModelValidatorStub` / `recordingOrgModelValidator`。
- [ ] **保留** `ModelCatalogService` / `List` / `HasModelInCatalog` / `/api/v1/models`。
- [ ] 验证：`go build`/`go test`/`go vet` 全绿。
- [ ] Commit：`refactor(assistant-version): 删除组织模型校验器死代码`

### Task 7：移除 `cfg.Hermes.RuntimeImage` 单值配置

**Files:** `internal/config/*.go`、`config/manager.yaml`、`config/manager.example.yaml`、`internal/worker/handlers/app_initialize.go`、`cmd/server/main.go`、相关测试（`loader_test.go` / `main_test.go` / `app_initialize_test.go` / `cmd/migrate` test）

- [ ] 删 `config.HermesConfig.RuntimeImage` 字段、loader 中 `runtime_image` 的必填校验与 `validateHermesRuntimeImage*`。
- [ ] 删 `config/manager.yaml` + `manager.example.yaml` 的 `runtime_image:` 键。
- [ ] 删 `app_initialize.go` 的 `RuntimeImage` / `defaultHermesRuntimeImage` 字段与各 fallback、`cmd/server/main.go` 的 `RuntimeImage` 装配。
- [ ] 受影响测试改为注入 `ResolveRuntimeImage` 桩、去掉 `runtime_image` 配置。
- [ ] 验证：`go build`/`go test`/`go vet` 全绿。
- [ ] Commit：`refactor(config): 移除 hermes.runtime_image 单值字段`

### Task 8：修复 `cmd/seed-e2e` 适配清理后的 schema

**Files:** `cmd/seed-e2e/main.go`

- [ ] 删 `enabled_models` INSERT、`organization_personas` TRUNCATE、apps INSERT 的 `persona_mode` / `model_id`、fixture 的 `ModelIDs` 字段，使其对齐 Phase 5 后 schema。
- [ ] 验证：`go build ./cmd/seed-e2e`、`go build ./...`。
- [ ] Commit：`fix(seed-e2e): 适配 Phase 5 schema 清理`

### Task 9：同步 OpenAPI 与前端类型

**Files:** `openapi/openapi.yaml`、`web/src/api/generated.ts`、`web/src/api/hooks/useApps.ts`、`web/src/api/hooks/useMembers.ts`、`web/src/api/index.ts`、`web/src/pages/apps/AppOverviewTab.vue`、受影响 `.spec.ts`

- [ ] `make openapi-gen` + `make web-types-gen` + `make openapi-check`（须干净）。
- [ ] `useApps.ts` 删 `persona_mode` / `app_prompt` / `model_id` / `model_synced`；`useMembers.ts` 删 `persona_mode`；`api/index.ts` 修对应 `WithRequired` 键。
- [ ] `AppOverviewTab.vue` 删「人设模式」「模型」两行。
- [ ] 更新受影响 `.spec.ts` fixture。验证：`make web-typecheck` + `make web-test`。
- [ ] Commit：`chore(assistant-version): 同步 OpenAPI 与前端类型`

---

## 验证清单
- `go build ./...`、`go vet ./...`、`go test ./...`
- `make openapi-check`
- `make web-typecheck`、`make web-test`
- `internal/migrations/migrations_test.go` 在新增 `000025`–`000028` 后仍通过
