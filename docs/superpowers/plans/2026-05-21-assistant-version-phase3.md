# 助手版本 Phase 3 实施计划：组织 allowlist 与实例绑定版本

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让平台管理员在创建/编辑组织时为组织配置「可用助手版本」allowlist，让组织管理员创建实例时从本组织 allowlist 选一个版本绑定到实例。

**Architecture:** 复用 Phase 1 已建好的 DB 列（`organizations.assistant_version_ids` jsonb、`apps.version_id`）。组织服务新增版本 allowlist 的校验与读写；onboarding 服务在创建实例时校验所选 `version_id` 落在组织 allowlist 内并写 `apps.version_id`。前端组织表单加版本多选、实例创建表单加版本单选。本阶段是 additive——不删任何旧列旧代码（旧 persona/model 的清理在 Phase 5）。

**Tech Stack:** Go、pgx/v5、sqlc、gin、testify；前端 Vue 3 + naive-ui + @tanstack/vue-query。

**关联文档：** 设计 spec `docs/superpowers/specs/2026-05-21-assistant-version-design.md` §3.2 / §3.3 / §7.3 / §7.4 / §8.2 / §8.3。

---

## 关键设计与排序决策

1. **organizations.model_id 的过渡处理。** 设计终态是「版本完全接管模型选择」，组织不再选模型。本阶段：组织创建/编辑**不再校验、不再要求** `model_id`——前端组织表单移除模型 select，后端 `CreateOrganization` / `UpdateOrganization` 去掉 `ValidateModelIDs` 调用与「modelValidator 未配置」的硬性拒绝，`model_id` 列按 `''` 默认值写入、不再使用。`model_id` 列与 `OrganizationResult.ModelID` 字段**保留**（Phase 5 才删），只是不再被填有意义的值。

2. **实例创建期的过渡。** 本阶段实例创建新增 `version_id`（必填，校验 ∈ 组织 allowlist）。`apps` 表的 `model_id` / `persona_mode` / `app_prompt` 列**保留**：onboarding 仍按现状写入（`model_id` = `org.ModelID`，此时为 `''`；`persona_mode` 默认 `org_inherited`；`app_prompt` 空）。这些旧列直到 Phase 4 把版本数据接入 manifest、Phase 5 删列才退役。过渡期内 app_initialize 仍写 v1 manifest，`app.model` 为空时由 `BuildAppInputData` 兜底到配置默认模型——功能可用，属已知过渡降级。

3. **apps.version_id 仍可空。** Phase 1 迁移把 `apps.version_id` 建为可空。本阶段不加迁移收紧为 NOT NULL（存量 app 为 NULL）；由 onboarding 服务在创建时强制必填。

4. **版本存在性。** 组织 allowlist 里的版本 id 在写入组织时逐个校验存在且未删除。实例绑定只需校验 `version_id ∈ org.assistant_version_ids`——因为 Phase 1 的严格保护已保证「被组织 allowlist 引用的版本不可删除」，allowlist 内的版本必然存在。

---

## 文件结构

| 文件 | 职责 | 动作 |
|---|---|---|
| `internal/store/queries/organizations.sql` | Create/Update 组织查询加 `assistant_version_ids` | 修改 |
| `internal/store/queries/apps.sql` | `CreateApp` 加 `version_id` | 修改 |
| `internal/store/sqlc/*` | sqlc 生成产物 | 重新生成 |
| `internal/service/assistant_version_service.go` | 新增 `ValidateAssistantVersionIDs` | 修改 |
| `internal/service/organization_service.go` | allowlist 校验/读写；去掉 model_id 校验 | 修改 |
| `internal/service/organization_service_test.go` | 组织服务测试 | 修改 |
| `internal/service/onboarding_service.go` | 实例创建绑定 `version_id` | 修改 |
| `internal/service/onboarding_service_test.go` | onboarding 测试 | 修改 |
| `internal/api/handlers/dto.go` | 组织/成员请求体加版本字段 | 修改 |
| `internal/api/handlers/organizations.go` | 组织 handler 透传 allowlist | 修改 |
| `internal/api/handlers/members.go` | onboarding handler 透传 version_id | 修改 |
| `cmd/server/main.go` | 给 OrganizationService 注入版本校验器 | 修改 |
| `openapi/openapi.yaml`、`web/src/api/generated.ts` | OpenAPI 同步 | 重新生成 |
| `web/src/api/hooks/useOrganizations.ts` | 组织 hook 加版本字段 | 修改 |
| `web/src/pages/platform/OrganizationsPage.vue` | 组织表单：版本多选，移除模型 select | 修改 |
| `web/src/pages/org/CreateMemberPage.vue` | 实例创建表单：版本单选，移除人设 | 修改 |
| 对应 `.spec.ts` | 前端测试 | 修改 |

构建/测试：`make vet`、`go test ./internal/... ./cmd/...`、`make web-typecheck`、`make web-test`、`make openapi-gen` + `make web-types-gen`。

---

## Task 1：sqlc 查询——组织 allowlist 与实例 version_id

**Files:**
- Modify: `internal/store/queries/organizations.sql`
- Modify: `internal/store/queries/apps.sql`
- Modify: `internal/store/sqlc/*`（生成）

- [ ] **Step 1：改 `organizations.sql`**

`CreateOrganization` 的 INSERT 列加 `assistant_version_ids`，`UpdateOrganizationProfile` 的 SET 加 `assistant_version_ids`。先读现有文件确认参数顺序，然后：
- `CreateOrganization`：在 INSERT 列表与 `VALUES` 末尾加 `assistant_version_ids` / 对应 `$N`。
- `UpdateOrganizationProfile`：在 `SET` 末尾加 `assistant_version_ids = $N`（N 取下一个序号）。

- [ ] **Step 2：改 `apps.sql`**

`CreateApp` 的 INSERT 列表与 `VALUES` 末尾加 `version_id` / 对应 `$N`。先读现有 `CreateApp` 确认列序。

- [ ] **Step 3：生成 sqlc** — Run: `make sqlc-generate` — Expected: `sqlc.CreateOrganizationParams` / `UpdateOrganizationProfileParams` 多出 `AssistantVersionIds []byte`；`sqlc.CreateAppParams` 多出 `VersionID pgtype.UUID`。

- [ ] **Step 4：验证编译** — Run: `go build ./...` — Expected: 编译失败（service 调用处还没传新参数）——这是预期的，下一个任务修。**先不要**改 service；本任务只确认 sqlc 生成成功。若想让 `go build` 暂时通过，可跳过此步，留待 Task 3/4 一起验证。

- [ ] **Step 5：提交**

```bash
git add internal/store/queries/organizations.sql internal/store/queries/apps.sql internal/store/sqlc/
git commit -m "feat(assistant-version): sqlc 支持组织 allowlist 与实例 version_id"
```
提交信息：Conventional Commits、中文摘要，正文空一行后以 `Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>` 结尾。

> 注：Task 1 提交后 `go build` 会暂时失败（service 未更新）。Task 2-4 在同一连续推进中修复；执行计划时把 Task 1-4 视作一组、最后统一验证编译。若严格要求每个提交可编译，把 Task 1 的提交推迟到 Task 4 一起提交——执行者按 subagent-driven 流程决定，但 spec/quality 审查应在 Task 4 后确认整体编译通过。

---

## Task 2：版本服务——ValidateAssistantVersionIDs

**Files:**
- Modify: `internal/service/assistant_version_service.go`
- Modify: `internal/service/assistant_version_service_test.go`

组织服务需要一个「校验一组版本 id 都存在且未删除」的能力。在版本服务上加此方法。

- [ ] **Step 1：写失败测试** — 在 `assistant_version_service_test.go` 末尾追加：

```go
// TestAssistantVersionValidateIDsOK 验证全部 id 存在时返回去重后的列表。
func TestAssistantVersionValidateIDsOK(t *testing.T) {
	store := newFakeAVStore()
	id := seedVersion(store, "标准版", 1)
	svc := newTestAVService(t, store)
	out, err := svc.ValidateAssistantVersionIDs(context.Background(), []string{id, id})
	require.NoError(t, err)
	assert.Equal(t, []string{id}, out)
}

// TestAssistantVersionValidateIDsRejectsUnknown 验证含不存在 id 时报 Invalid。
func TestAssistantVersionValidateIDsRejectsUnknown(t *testing.T) {
	svc := newTestAVService(t, newFakeAVStore())
	_, err := svc.ValidateAssistantVersionIDs(context.Background(), []string{"00000000-0000-0000-0000-0000000000e1"})
	require.ErrorIs(t, err, ErrAssistantVersionInvalid)
}

// TestAssistantVersionValidateIDsEmpty 验证空列表合法（组织可不配版本）。
func TestAssistantVersionValidateIDsEmpty(t *testing.T) {
	svc := newTestAVService(t, newFakeAVStore())
	out, err := svc.ValidateAssistantVersionIDs(context.Background(), nil)
	require.NoError(t, err)
	assert.Empty(t, out)
}
```

- [ ] **Step 2：运行测试确认失败** — Run: `go test ./internal/service/ -run AssistantVersionValidateIDs -v` — Expected: 编译失败（方法未定义）。

- [ ] **Step 3：实现** — 在 `assistant_version_service.go` 末尾追加：

```go
// ValidateAssistantVersionIDs 校验一组版本 id 全部存在且未删除，返回去重后的列表。
// 供组织 allowlist 写入前校验；空列表合法（组织可暂不配置任何版本）。
func (s *AssistantVersionService) ValidateAssistantVersionIDs(ctx context.Context, ids []string) ([]string, error) {
	seen := make(map[string]struct{}, len(ids))
	out := make([]string, 0, len(ids))
	for _, raw := range ids {
		id := trimSpace(raw)
		if id == "" {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		if _, err := s.loadVersion(ctx, id); err != nil {
			if errors.Is(err, ErrAssistantVersionNotFound) {
				return nil, fmt.Errorf("%w: 版本 %s 不存在", ErrAssistantVersionInvalid, id)
			}
			return nil, err
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out, nil
}
```

- [ ] **Step 4：运行测试确认通过** — Run: `go test ./internal/service/ -run AssistantVersion -v` — Expected: 全部 PASS。

- [ ] **Step 5：提交**

```bash
git add internal/service/assistant_version_service.go internal/service/assistant_version_service_test.go
git commit -m "feat(assistant-version): 版本服务新增 allowlist 校验方法"
```
提交信息规则同 Task 1。

---

## Task 3：组织服务——allowlist 校验与读写

**Files:**
- Modify: `internal/service/organization_service.go`
- Modify: `internal/service/organization_service_test.go`

- [ ] **Step 1：读现有文件与测试** — 通读 `organization_service.go`（`OrganizationInput` / `OrganizationResult` / `OrganizationStore` / `CreateOrganization` / `UpdateOrganization` / `toOrganizationResult`）与 `organization_service_test.go`，掌握现有 model_id 校验与测试装配。

- [ ] **Step 2：改 service**

(a) 新增依赖接口与字段。在文件内新增：

```go
// OrganizationVersionValidator 抽象「校验一组助手版本 id 都存在」的能力。
type OrganizationVersionValidator interface {
	ValidateAssistantVersionIDs(ctx context.Context, ids []string) ([]string, error)
}
```

`OrganizationService` 结构体加字段 `versionValidator OrganizationVersionValidator`，并加注入方法：

```go
// SetVersionValidator 注入助手版本 allowlist 校验器。
func (s *OrganizationService) SetVersionValidator(v OrganizationVersionValidator) {
	s.versionValidator = v
}
```

(b) `OrganizationInput` 加字段：

```go
	// AssistantVersionIDs 是该组织可用的助手版本 id 列表（allowlist）。
	AssistantVersionIDs []string
	// AssistantVersionIDsSet 标记更新请求是否显式传入了 allowlist。
	AssistantVersionIDsSet bool
```

(c) `OrganizationResult` 加字段 `AssistantVersionIDs []string \`json:"assistant_version_ids"\``。

(d) `toOrganizationResult` 解析 `org.AssistantVersionIds`（jsonb `[]byte`）为 `[]string`：

```go
	versionIDs := []string{}
	if len(org.AssistantVersionIds) > 0 {
		_ = json.Unmarshal(org.AssistantVersionIds, &versionIDs)
	}
	// ...result.AssistantVersionIDs = versionIDs
```

(e) `CreateOrganization`：
- **删除** model_id 的两段——`if s.modelValidator == nil { ... }` 与 `if _, err := s.modelValidator.ValidateModelIDs(...)`。`model_id` 不再校验，`CreateOrganizationParams.ModelID` 传 `input.ModelID`（前端将传空串）。
- 校验 allowlist：`versionValidator` 必须已注入；调 `ValidateAssistantVersionIDs(ctx, input.AssistantVersionIDs)` 得到去重列表，`json.Marshal` 为 `[]byte` 传入 `CreateOrganizationParams.AssistantVersionIds`。
- 用法示例：

```go
	if s.versionValidator == nil {
		return OrganizationResult{}, fmt.Errorf("版本校验器未配置，无法保存组织可用版本")
	}
	cleanVersionIDs, err := s.versionValidator.ValidateAssistantVersionIDs(ctx, input.AssistantVersionIDs)
	if err != nil {
		return OrganizationResult{}, err
	}
	versionIDsJSON, err := json.Marshal(cleanVersionIDs)
	if err != nil {
		return OrganizationResult{}, fmt.Errorf("序列化组织可用版本失败: %w", err)
	}
```

把 `versionIDsJSON` 作为 `CreateOrganizationParams.AssistantVersionIds`。

(f) `UpdateOrganization`：
- **删除** model_id 校验段（`input.ModelIDSet` 那一整块），改为始终保留 `current.ModelID`（`UpdateOrganizationProfileParams.ModelID: current.ModelID`），并移除随之产生的 `modelChanged` / `UpdateAppModelsByOrg` 调用。
- allowlist：仅当 `input.AssistantVersionIDsSet` 为 true 时校验并更新；否则保留 `current.AssistantVersionIds`。把结果传 `UpdateOrganizationProfileParams.AssistantVersionIds`。

(g) `OrganizationStore` 接口：现在不再用 `UpdateAppModelsByOrg`（model 同步逻辑随 model_id 校验一起移除）。**保留** `UpdateAppModelsByOrg` 在接口里也可以（不调用即可），但更干净是删除该接口方法——执行时按「只删本任务确实不再用到的」处理：若删除会牵连其它调用方则保留。优先保留，避免连带改动；在 PR 说明里注明 model 同步已不再触发。

> 设计取舍说明：`s.modelValidator` 字段与 `SetModelValidator` 方法本任务暂不删除（Phase 5 统一清理 model 相关代码），只是 Create/Update 不再调用它。

- [ ] **Step 3：改测试** — `organization_service_test.go`：
- 现有依赖 model_id 校验的用例（如「模型不存在时拒绝创建」）：改为不再校验 model_id——要么删除该用例，要么改为断言 model_id 不影响创建。
- 测试装配补一个 `fakeVersionValidator`（实现 `ValidateAssistantVersionIDs`：已知 id 通过、未知报 `ErrAssistantVersionInvalid`），并 `SetVersionValidator` 注入。
- 新增用例：创建组织带合法 `AssistantVersionIDs` → 成功且 `OrganizationResult.AssistantVersionIDs` 正确；带非法版本 id → 报错；`UpdateOrganization` 带 `AssistantVersionIDsSet=true` 更新 allowlist。
- 每个新测试方法/子测试加相邻中文注释。

- [ ] **Step 4：运行测试** — Run: `go test ./internal/service/ -run Organization -v` — Expected: PASS。

- [ ] **Step 5：提交**

```bash
git add internal/service/organization_service.go internal/service/organization_service_test.go
git commit -m "feat(assistant-version): 组织服务支持助手版本 allowlist"
```
提交信息规则同 Task 1。

---

## Task 4：onboarding 服务——实例绑定 version_id

**Files:**
- Modify: `internal/service/onboarding_service.go`
- Modify: `internal/service/onboarding_service_test.go`

- [ ] **Step 1：读现有文件与测试**

- [ ] **Step 2：改 service**

(a) `OnboardMemberInput` 与 `CreateAppForMemberInput` 各加字段 `VersionID string`。

(b) 新增一个 helper，校验 version_id 落在组织 allowlist 内：

```go
// versionInOrgAllowlist 判断 version_id 是否在组织 assistant_version_ids allowlist 内。
func versionInOrgAllowlist(org sqlc.Organization, versionID string) bool {
	if versionID == "" {
		return false
	}
	ids := []string{}
	if len(org.AssistantVersionIds) > 0 {
		if err := json.Unmarshal(org.AssistantVersionIds, &ids); err != nil {
			return false
		}
	}
	for _, id := range ids {
		if id == versionID {
			return true
		}
	}
	return false
}
```

(c) `OnboardMember` 与 `CreateAppForMember`：在 tx 内取到 `org` 后、`CreateApp` 之前，校验：

```go
		if !versionInOrgAllowlist(org, input.VersionID) {
			return fmt.Errorf("%w: 所选助手版本不在组织可用范围内", ErrMemberCreateInvalid)
		}
		versionUUID, err := parseUUID(input.VersionID)
		if err != nil {
			return fmt.Errorf("%w: 非法助手版本 id", ErrMemberCreateInvalid)
		}
```

并在 `CreateApp` 的 `sqlc.CreateAppParams{...}` 里加 `VersionID: versionUUID`。其余字段（`ModelID: org.ModelID`、`PersonaMode`、`AppPrompt`）保持现状不动。

(d) `OnboardMember` 顶部的入参非空校验（`if input.Username == "" || ...`）加上 `input.VersionID == ""` 的检查，缺版本即报 `ErrMemberCreateInvalid`。`CreateAppForMember` 顶部同样加 `input.VersionID == ""` 检查。

- [ ] **Step 3：改测试** — `onboarding_service_test.go`：
- 现有测试构造 `OnboardMemberInput` / `CreateAppForMemberInput` 的地方补 `VersionID`；测试用的 fake 组织要带 `AssistantVersionIds`（含该 version id 的 jsonb）。
- 新增用例：`version_id` 不在组织 allowlist → 报错；缺 `version_id` → 报错；合法 `version_id` → `app.version_id` 被写入。
- 每个新测试加相邻中文注释。

- [ ] **Step 4：运行测试 + 全量编译** — Run: `go test ./internal/service/ -v` 然后 `go build ./...` — Expected: service 包测试 PASS；`go build ./...` 此时应通过（Task 1 的 sqlc 新参数已被 Task 3/4 的 service 调用补齐）。若 `go build` 仍报某处缺参数，定位并修复该调用点。

- [ ] **Step 5：提交**

```bash
git add internal/service/onboarding_service.go internal/service/onboarding_service_test.go
git commit -m "feat(assistant-version): 实例创建绑定助手版本"
```
提交信息规则同 Task 1。

---

## Task 5：HTTP handler 与 DTO

**Files:**
- Modify: `internal/api/handlers/dto.go`
- Modify: `internal/api/handlers/organizations.go`
- Modify: `internal/api/handlers/members.go`
- Modify: 对应 handler 测试文件

- [ ] **Step 1：读现有 handler 与 dto**

通读 `dto.go` 里组织与成员相关请求体、`organizations.go`、`members.go`，掌握请求体如何解析为 service 入参。

- [ ] **Step 2：改 dto.go**

组织创建/更新请求体加字段 `AssistantVersionIDs []string \`json:"assistant_version_ids"\``。成员 onboarding / 为已有成员创建实例的请求体加 `VersionID string \`json:"version_id" binding:"required"\``。（先确认这些请求体当前的确切类型名，与 `dto.go` 现状对齐。）

- [ ] **Step 3：改 handler**

- `organizations.go`：创建/更新组织时把请求体的 `AssistantVersionIDs` 透传到 `OrganizationInput.AssistantVersionIDs`；更新接口设置 `AssistantVersionIDsSet = true`（当请求显式带了该字段时——若用指针或「字段出现即视为 set」按现有 `ModelIDSet` 的处理方式对齐）。
- `members.go`：onboarding / 创建实例接口把请求体 `VersionID` 透传到 `OnboardMemberInput.VersionID` / `CreateAppForMemberInput.VersionID`。

- [ ] **Step 4：改 handler 测试** — 对应 `organizations_test.go` / `members_test.go`：构造请求体处补版本字段；新增覆盖「带 allowlist 创建组织」「带 version_id 创建实例」的用例。

- [ ] **Step 5：运行测试** — Run: `go test ./internal/api/handlers/ -v` — Expected: PASS。

- [ ] **Step 6：提交**

```bash
git add internal/api/handlers/
git commit -m "feat(assistant-version): 组织与实例 HTTP 接口透传版本字段"
```
提交信息规则同 Task 1。

---

## Task 6：cmd/server 接线

**Files:**
- Modify: `cmd/server/main.go`

- [ ] **Step 1：注入版本校验器**

在 `cmd/server/main.go` 构造 `organizationService` 与 `assistantVersionService` 之后，把版本服务作为校验器注入组织服务：

```go
	if assistantVersionService != nil {
		organizationService.SetVersionValidator(assistantVersionService)
	}
```

放在 `organizationService` 已构造、`assistantVersionService` 已构造之后；注意两者构造顺序——若 `assistantVersionService` 在 `organizationService` 之后构造，把这行放到两者都就绪的位置。`*service.AssistantVersionService` 已有 `ValidateAssistantVersionIDs` 方法，结构上满足 `OrganizationVersionValidator`。

- [ ] **Step 2：构建与全量测试** — Run: `go build ./... && make vet && go test ./internal/... ./cmd/...` — Expected: 全绿。

- [ ] **Step 3：提交**

```bash
git add cmd/server/main.go
git commit -m "feat(assistant-version): 组织服务接线版本校验器"
```
提交信息规则同 Task 1。

---

## Task 7：OpenAPI 与前端类型同步

**Files:** `openapi/openapi.yaml`、`web/src/api/generated.ts`（生成）

- [ ] **Step 1：生成** — Run: `make openapi-gen && make web-types-gen`
- [ ] **Step 2：校验** — Run: `make openapi-check` — Expected: 工作区干净。
- [ ] **Step 3：提交**

```bash
git add openapi/openapi.yaml web/src/api/generated.ts
git commit -m "chore(assistant-version): 同步 OpenAPI 与前端类型"
```

---

## Task 8：前端——组织表单版本多选

**Files:**
- Modify: `web/src/api/hooks/useOrganizations.ts`
- Modify: `web/src/pages/platform/OrganizationsPage.vue`
- Modify: `web/src/pages/platform/OrganizationsPage.spec.ts`

- [ ] **Step 1：hook** — `useOrganizations.ts` 的 `OrganizationFormPayload` 去掉 `model_id`、加 `assistant_version_ids: string[]`；`Organization` 类型（来自 `@/api`）已含 `assistant_version_ids`（Task 7 生成）。新增/复用一个查询助手版本列表的 hook——可直接 import Phase 1b 的 `useAssistantVersionsQuery`（`@/api/hooks/useAssistantVersions`），供组织表单的版本多选选项使用。

- [ ] **Step 2：页面** — `OrganizationsPage.vue` 创建组织表单：
- 移除「模型」`n-select` 及其 `useModelsQuery` / `modelOptions` / `canSubmitOrganization` 里的 model 判断。
- 新增「可用助手版本」`n-select`（`multiple` 多选），选项来自 `useAssistantVersionsQuery`（label = 版本 name，value = 版本 id），`v-model` 绑 `form.assistant_version_ids`。
- `toPayload` 把 `model_id` 换成 `assistant_version_ids`。
- 表单初始值 `assistant_version_ids: [] as string[]`。

- [ ] **Step 3：测试** — `OrganizationsPage.spec.ts`：把 model 相关的 mock/断言换成版本多选；mock `useAssistantVersionsQuery`；更新「创建组织」用例断言提交体含 `assistant_version_ids`。

- [ ] **Step 4：校验** — Run: `make web-typecheck && make web-test` — Expected: 通过（注意：`OrganizationsPage.spec.ts` 此前有 5 个因 `useQueries` 缺 QueryClient 的预存在失败——本任务若顺带让这些用例可跑则更好，但不强制；至少不得新增失败）。

- [ ] **Step 5：提交**

```bash
git add web/src/api/hooks/useOrganizations.ts web/src/pages/platform/OrganizationsPage.vue web/src/pages/platform/OrganizationsPage.spec.ts
git commit -m "feat(assistant-version): 组织表单改用助手版本多选"
```
提交信息规则同 Task 1。

---

## Task 9：前端——实例创建表单版本单选

**Files:**
- Modify: `web/src/pages/org/CreateMemberPage.vue`
- Modify: `web/src/api/hooks/useMembers.ts`（如 onboarding 提交体在此定义）
- Modify: `web/src/pages/org/CreateMemberPage.spec.ts`

- [ ] **Step 1：读现有页面** — 通读 `CreateMemberPage.vue` 与其提交 hook，掌握现有人设（persona）相关字段与提交体结构。

- [ ] **Step 2：页面与 hook**
- onboarding 提交体加 `version_id: string`。
- `CreateMemberPage.vue`：新增「助手版本」`n-select`（单选），选项为**当前组织 allowlist 内**的版本。组织管理员的 `orgID` 可从 auth store 取；版本列表——可用 `useAssistantVersionsQuery`（org_admin 有读权限）后按本组织 `organization.assistant_version_ids` 过滤，或后端另给一个「本组织可用版本」接口。**本阶段实现**：前端取组织详情拿 `assistant_version_ids`，再取版本列表，前端做交集过滤展示。
- 移除人设（persona_mode / app_prompt）相关输入项（设计 §8.3）。
- 表单校验：`version_id` 必填才能提交。

- [ ] **Step 3：测试** — `CreateMemberPage.spec.ts`：移除人设相关断言；新增版本单选 + 提交体含 `version_id` 的断言；mock 版本列表与组织详情 hook。

- [ ] **Step 4：校验** — Run: `make web-typecheck && make web-test` — Expected: 通过，无新增失败。

- [ ] **Step 5：提交**

```bash
git add web/src/pages/org/CreateMemberPage.vue web/src/api/hooks/useMembers.ts web/src/pages/org/CreateMemberPage.spec.ts
git commit -m "feat(assistant-version): 实例创建表单改用助手版本单选"
```
提交信息规则同 Task 1。

---

## Task 10：真实浏览器功能验证

**Files:** 无（验证任务）

按 AGENTS.md「新功能必须真实浏览器验证」，用 `webapp-testing` 技能（Playwright）走完整流程：

- [ ] **Step 1：环境** — 确认本地 docker 栈与前端 dev server 就绪，`make migrate-up`。
- [ ] **Step 2：平台管理员验证** — 登录平台管理员，创建组织时「可用助手版本」多选可选、保存成功；编辑组织能改 allowlist。组织表单不再出现「模型」select。
- [ ] **Step 3：组织管理员验证** — 用该组织的管理员登录，创建实例时「助手版本」单选只列出本组织 allowlist 内的版本；选一个保存成功；实例创建表单不再有人设输入。绑定的版本可在实例列表/详情体现（若该处有展示）。
- [ ] **Step 4：边界** — allowlist 为空的组织，其管理员创建实例时版本下拉为空且无法提交（必填拦截）。
- [ ] **Step 5：记录结果** — 写入交付说明；发现问题先修再验。

---

## Self-Review

**Spec 覆盖：**
- §3.2 组织 `assistant_version_ids` 读写 → Task 1 + 3 + 8。
- §3.3 实例 `version_id` 绑定 → Task 1 + 4 + 9。
- §7.3 组织服务 allowlist 校验 → Task 2 + 3。
- §7.4 实例创建校验 version_id ∈ allowlist → Task 4。
- §8.2 组织表单版本多选、移除模型 select → Task 8。
- §8.3 实例创建表单版本单选、移除人设 → Task 9。
- OpenAPI 同步（仓库硬性要求）→ Task 7。

**不在本计划：** 旧 `organizations.model_id` / `apps.model_id` / `persona_mode` / `app_prompt` / `organization_personas` 的删除（Phase 5）；version_synced 检测、init/restart 写版本数据、切换版本（Phase 4）；manifest v2 写入侧（Phase 4）。

**排序与编译：** Task 1 改 sqlc 后 `go build` 暂时失败，Task 3/4 补齐 service 调用，Task 4 末尾确认 `go build ./...` 通过。执行时把 Task 1-4 视作连续一组、Task 4 后整体验证编译。

**过渡期已知降级：** 组织不再配模型 → 新建实例 `model_id=''` → Phase 4 之前 app_initialize 走配置默认模型兜底。已在「关键设计与排序决策」§2 说明，符合「不考虑存量」。

**类型一致性：** `AssistantVersionIDs []string`（service Input/Result）、`assistant_version_ids`（json tag、sqlc jsonb 列、前端字段）三处命名一致；`VersionID string`（onboarding Input）/ `version_id`（json、sqlc `VersionID pgtype.UUID`）一致。

---

## 后续

Phase 3 交付后，组织能配版本 allowlist、实例能绑版本。剩余：
- **Phase 4：** Go 侧 `manifest.go` 扩展 + `BuildAppInputData` 写 routing/skills/版本 persona/版本镜像 + 实例初始化/重启写 `applied_version_revision` / `applied_image_ref` + `version_synced` 检测 + 实例列表「需重启」提示 + 切换版本动作。
- **Phase 5：** 删除 `organization_personas` 表、`organizations.model_id`、`apps.model_id` / `persona_mode` / `app_prompt` / `model_synced` 及相关 service / handler / 前端 persona 页。
