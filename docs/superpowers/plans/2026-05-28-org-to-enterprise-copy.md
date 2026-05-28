# 企业文案改名 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 将用户可见中文租户术语从“组织”统一调整为“企业”，同时保持 `org/organization` 内部英文标识、API 路径、数据库结构和角色枚举不变。

**Architecture:** 这是一组展示文案和文档改动，不新增业务抽象。前端先更新用户可见断言和页面文案；后端只改错误消息、审计展示标签和 Swagger/DTO 中文注释；OpenAPI 与前端生成类型只通过生成命令更新。

**Tech Stack:** Vue 3、Vitest、Go/Gin、swag OpenAPI、openapi-typescript、Markdown 文档、真实浏览器验收。

---

## File Structure

- Modify: `web/src/domain/status.ts`
  - 中文角色展示从“组织管理员/组织成员”改为“企业管理员/企业成员”。
- Modify: `web/src/domain/status.test.ts`
  - 更新 `formatMemberRole` 断言。
- Modify: `web/src/stores/auth.spec.ts`
  - 登录测试名称和 `orgCode` 语义断言改为企业文案。
- Modify: `web/src/pages/login/LoginPage.vue`
  - 登录表单 label、aria-label 和 placeholder 使用“企业标识”。
- Modify: `web/src/layouts/DashboardLayout.vue`
  - 平台侧导航“组织”改为“企业”。
- Modify: `web/src/pages/dashboard/RoleAwareHome.vue`
  - 角色首页入口标题和 subtitle 改为企业语义。
- Modify: `web/src/pages/platform/OrganizationsPage.vue`
  - 企业列表、创建/编辑企业表单、企业充值弹窗、企业标识列和复制信息文案。
- Modify: `web/src/pages/platform/OrganizationsPage.spec.ts`
  - 企业列表、企业标识、复制信息、创建/编辑企业测试断言。
- Modify: `web/src/pages/platform/ConsolePage.vue`
  - 平台控制台统计和图表 tab 中“组织”展示改为“企业”。
- Modify: `web/src/pages/platform/PermissionsPage.vue`
  - 权限说明页中的操作项、条件项、角色列名和说明改为企业语义。
- Modify: `web/src/pages/audit/AuditLogsPage.vue`
  - 企业审计选择器、空态、eyebrow。
- Modify: `web/src/pages/audit/AuditLogsPage.spec.ts`
  - 审计角色展示断言改为企业角色。
- Modify: `web/src/pages/knowledge/OrgKnowledgePage.vue`
  - 企业知识库页面标题、选择器、空态、eyebrow。
- Modify: `web/src/pages/knowledge/OrgKnowledgePage.spec.ts`
  - 企业知识库上传、只读下载测试名称和断言。
- Modify: `web/src/pages/org/CreateMemberPage.vue`
  - 企业管理员创建成员页面中的空态、eyebrow、角色选项。
- Modify: `web/src/pages/org/CreateMemberPage.spec.ts`
  - 测试数据名称和角色展示文案。
- Modify: `web/src/pages/org/MembersPage.vue`
  - 成员列表选择器、eyebrow、空态、角色选项和默认实例名展示。
- Modify: `web/src/pages/org/MembersPage.spec.ts`
  - 成员页企业名称、角色、补建实例默认名断言。
- Modify: `web/src/pages/org/OrgBalancePage.vue`
  - 企业余额页面文案。
- Modify: `web/src/pages/org/OrgConsolePage.vue`
  - 企业管理员控制台文案。
- Modify: `web/src/pages/apps/AppsPage.vue`
  - 实例列表企业选择器、空态、eyebrow。
- Modify: `web/src/pages/apps/AppsPage.spec.ts`
  - 企业实例和空态断言。
- Modify: `web/src/pages/apps/AppOverviewTab.vue`
  - “所属组织/未知组织/组织管理员”展示改为企业语义。
- Modify: `web/src/pages/apps/AppOverviewTab.spec.ts`
  - 所属企业、企业名称、企业管理员断言。
- Modify: `web/src/pages/apps/AppAuditTab.spec.ts`
  - 审计角色标签断言改为企业成员。
- Modify: `web/src/pages/apps/AppChannelsTab.vue`
  - 钉钉描述“组织通讯”改为“企业通讯”。
- Modify: `web/src/pages/apps/AppDetailPage.vue`, `web/src/pages/apps/AppDetailPage.spec.ts`, `web/src/pages/apps/AppRuntimeTab.vue`, `web/src/pages/apps/cron/CronJobFormModal.spec.ts`
  - 用户可见或测试场景名中的组织语义改为企业语义；保留内部权限注释中必须指向 `org` 的英文代码。
- Modify: `web/src/pages/usage/UsagePage.vue`, `web/src/pages/usage/__tests__/UsagePage.spec.ts`
  - 用量 tab、选择器、测试名改为企业语义。
- Modify: `web/src/api/hooks/useApps.ts`, `web/src/api/hooks/useKnowledge.ts`, `web/src/api/hooks/useMembers.ts`, `web/src/api/hooks/useOrganizations.ts`, `web/src/api/hooks/usePlatform.ts`, `web/src/api/hooks/useRecharge.ts`, `web/src/api/hooks/useUsage.ts`
  - 抛给用户的错误消息和会进入测试断言的中文注释改为企业语义；函数名和类型名不改。
- Modify: `web/src/composables/usePlatformOrgSelection.ts`, `web/src/composables/useMemberApp.ts`, `web/src/composables/__tests__/useMemberApp.spec.ts`, `web/src/domain/permissions.ts`, `web/src/domain/permissions.spec.ts`
  - 用户可见或测试说明中的中文改为企业语义；保留 `orgId`、`org_id`、权限函数名。
- Modify: `internal/service/audit_label.go`, `internal/service/audit_label_test.go`
  - 审计展示标签改为企业角色和企业资源。
- Modify: `internal/service/organization_service.go`, `internal/api/handlers/request_errors_test.go`, `internal/api/handlers/organizations_test.go`
  - 企业标识校验错误文案和断言。
- Modify: `internal/service/onboarding_service.go`, `internal/service/errors.go`, `internal/api/handlers/apps.go`
  - 助手版本 allowlist 错误文案改为“企业允许列表/企业可用范围”。
- Modify: `internal/api/handlers/organizations.go`, `internal/api/handlers/members.go`, `internal/api/handlers/knowledge.go`, `internal/api/handlers/audit.go`, `internal/api/handlers/recharge.go`, `internal/api/handlers/usage.go`, `internal/api/handlers/apps.go`, `internal/api/handlers/app_runtime.go`, `internal/api/handlers/models.go`, `internal/api/handlers/platform_overview.go`, `internal/api/handlers/runtime_knowledge.go`, `internal/api/handlers/jobs.go`, `internal/api/handlers/dto.go`
  - Swagger 注解、DTO 中文说明和用户可见错误消息改为企业语义。
- Modify: `internal/service/auth_service_test.go`, `internal/service/organization_service_test.go`
  - 测试数据中的中文展示名改为企业角色。
- Modify after generation: `openapi/openapi.yaml`, `web/src/api/generated.ts`
  - 仅通过 `make openapi-gen` 和 `make web-types-gen` 更新。
- Modify: `README.md`, `docs/architecture.md`, `docs/hermes-container.md`, `docs/knowledge-base.md`, `docs/local-development.md`, `docs/product-design.md`, `docs/technical-design.md`, `docs/user-manual.md`
  - 正式产品和使用文档改为企业语义。
- Do not modify for this task: `docs/superpowers/specs/**`, `docs/superpowers/plans/**` except this plan file, `docs/reports/**`, SQL migrations, sqlc generated files, API paths, DB names, role enum values.

---

### Task 1: Frontend User-Visible Copy

**Files:**
- Modify: `web/src/domain/status.ts`
- Modify: `web/src/domain/status.test.ts`
- Modify: `web/src/stores/auth.spec.ts`
- Modify: `web/src/pages/login/LoginPage.vue`
- Modify: `web/src/layouts/DashboardLayout.vue`
- Modify: `web/src/pages/dashboard/RoleAwareHome.vue`
- Modify: `web/src/pages/platform/OrganizationsPage.vue`
- Modify: `web/src/pages/platform/OrganizationsPage.spec.ts`
- Modify: `web/src/pages/platform/ConsolePage.vue`
- Modify: `web/src/pages/platform/PermissionsPage.vue`
- Modify: `web/src/pages/audit/AuditLogsPage.vue`
- Modify: `web/src/pages/audit/AuditLogsPage.spec.ts`
- Modify: `web/src/pages/knowledge/OrgKnowledgePage.vue`
- Modify: `web/src/pages/knowledge/OrgKnowledgePage.spec.ts`
- Modify: `web/src/pages/org/CreateMemberPage.vue`
- Modify: `web/src/pages/org/CreateMemberPage.spec.ts`
- Modify: `web/src/pages/org/MembersPage.vue`
- Modify: `web/src/pages/org/MembersPage.spec.ts`
- Modify: `web/src/pages/org/OrgBalancePage.vue`
- Modify: `web/src/pages/org/OrgConsolePage.vue`
- Modify: `web/src/pages/apps/AppsPage.vue`
- Modify: `web/src/pages/apps/AppsPage.spec.ts`
- Modify: `web/src/pages/apps/AppOverviewTab.vue`
- Modify: `web/src/pages/apps/AppOverviewTab.spec.ts`
- Modify: `web/src/pages/apps/AppAuditTab.spec.ts`
- Modify: `web/src/pages/apps/AppChannelsTab.vue`
- Modify: `web/src/pages/apps/AppDetailPage.vue`
- Modify: `web/src/pages/apps/AppDetailPage.spec.ts`
- Modify: `web/src/pages/apps/AppRuntimeTab.vue`
- Modify: `web/src/pages/apps/cron/CronJobFormModal.spec.ts`
- Modify: `web/src/pages/usage/UsagePage.vue`
- Modify: `web/src/pages/usage/__tests__/UsagePage.spec.ts`
- Modify: `web/src/api/hooks/useApps.ts`
- Modify: `web/src/api/hooks/useKnowledge.ts`
- Modify: `web/src/api/hooks/useKnowledge.spec.ts`
- Modify: `web/src/api/hooks/useMembers.ts`
- Modify: `web/src/api/hooks/useOrganizations.ts`
- Modify: `web/src/api/hooks/usePlatform.ts`
- Modify: `web/src/api/hooks/useRecharge.ts`
- Modify: `web/src/api/hooks/useUsage.ts`
- Modify: `web/src/composables/usePlatformOrgSelection.ts`
- Modify: `web/src/composables/useMemberApp.ts`
- Modify: `web/src/composables/__tests__/useMemberApp.spec.ts`
- Modify: `web/src/domain/permissions.ts`
- Modify: `web/src/domain/permissions.spec.ts`

- [ ] **Step 1: Capture the frontend baseline**

Run:

```bash
rtk rg -n "组织|本组织|跨组织|组织标识|组织管理员|组织成员|组织级|所属组织|未知组织|暂无可查看组织|当前账号未关联组织" web/src --glob '!api/generated.ts'
```

Expected: output lists frontend pages, hooks, tests, and comments that still contain the old Chinese tenant term. Do not edit `web/src/api/generated.ts` in this task.

- [ ] **Step 2: Update frontend tests to expect enterprise copy**

Edit tests first so the UI copy change has failing assertions. Apply these exact user-visible expectation changes:

```text
组织管理员 -> 企业管理员
组织成员 -> 企业成员
组织标识 -> 企业标识
组织列表 -> 企业列表
新增组织 -> 新增企业
创建组织 -> 创建企业
编辑组织 -> 编辑企业
组织知识库 -> 企业知识库
组织实例 -> 企业实例
所属组织 -> 所属企业
未知组织 -> 未知企业
暂无可查看组织 -> 暂无可查看企业
当前账号未关联组织 -> 当前账号未关联企业
本组织 -> 本企业
跨组织 -> 跨企业
组织内 -> 企业内
组织用量 -> 企业用量
组织审计 -> 企业审计
组织充值 -> 企业充值
```

Concrete test edits:

```ts
// web/src/domain/status.test.ts
expect(formatMemberRole('org_admin')).toBe('企业管理员')
expect(formatMemberRole('org_member')).toBe('企业成员')
```

```ts
// web/src/pages/platform/OrganizationsPage.spec.ts
expect(wrapper.text()).toContain('企业标识')
const openButton = wrapper.findAll('button').find(button => button.text().includes('新增企业'))
await inputs[3].setValue('企业管理员')
expect(createOrganization).toHaveBeenCalledWith(expect.objectContaining({
  admin_display_name: '企业管理员',
}))
```

```ts
// web/src/pages/apps/AppOverviewTab.spec.ts
it('所属企业展示企业名称而不是企业 UUID', () => {
  organizationName.value = '测试企业'
  const wrapper = mountComponent()
  expect(wrapper.text()).toContain('测试企业')
})

it('企业名称缺失时展示友好兜底文案', () => {
  organizationName.value = undefined
  const wrapper = mountComponent()
  expect(wrapper.text()).toContain('未知企业')
})
```

```ts
// web/src/pages/org/MembersPage.spec.ts
const members = [
  { display_name: '企业管理员', role: 'org_admin' },
  { display_name: '企业成员', role: 'org_member' },
]
expect((appNameInput.element as HTMLInputElement).value).toBe('企业成员 的实例')
```

- [ ] **Step 3: Run frontend tests and verify they fail before implementation**

Run:

```bash
rtk npm --prefix web test -- --run \
  src/domain/status.test.ts \
  src/stores/auth.spec.ts \
  src/pages/platform/OrganizationsPage.spec.ts \
  src/pages/apps/AppOverviewTab.spec.ts \
  src/pages/org/MembersPage.spec.ts \
  src/pages/knowledge/OrgKnowledgePage.spec.ts \
  src/pages/audit/AuditLogsPage.spec.ts \
  src/pages/usage/__tests__/UsagePage.spec.ts
```

Expected: FAIL with assertions expecting enterprise copy while components still render organization copy.

- [ ] **Step 4: Update frontend source copy**

Edit the Vue and TypeScript source files listed in this task using the same mapping from Step 2. Preserve all identifiers and paths:

```text
Keep: org_id, orgId, org_code, org_admin, org_member, organization, organizations, OrganizationsPage, OrgKnowledgePage, /organizations
Change only Chinese display copy and Chinese test descriptions that describe user-visible behavior.
```

Key source snippets after the edit:

```ts
// web/src/domain/status.ts
const memberRoleLabels: Record<string, string> = {
  platform_admin: '平台管理员',
  org_admin: '企业管理员',
  org_member: '企业成员',
}
```

```vue
<!-- web/src/pages/login/LoginPage.vue -->
<n-form-item label="企业标识" path="orgCode">
  <n-input
    v-model:value="form.orgCode"
    placeholder="企业用户填写，平台管理员留空"
    :input-props="{ id: 'org-code', 'aria-label': '企业标识' }"
  />
</n-form-item>
```

```ts
// web/src/layouts/DashboardLayout.vue
items.push({ key: '/organizations', label: '企业', icon: () => h(Building2, { size: 18 }) })
```

```vue
<!-- web/src/pages/knowledge/OrgKnowledgePage.vue -->
<h2 style="margin: 0">企业知识库</h2>
<n-select placeholder="选择企业" />
```

```ts
// web/src/pages/org/MembersPage.vue
const memberRoleOptions = [
  { label: '企业成员', value: 'org_member' },
  { label: '企业管理员', value: 'org_admin' },
]
```

- [ ] **Step 5: Run frontend copy tests and verify pass**

Run:

```bash
rtk npm --prefix web test -- --run \
  src/domain/status.test.ts \
  src/stores/auth.spec.ts \
  src/pages/platform/OrganizationsPage.spec.ts \
  src/pages/apps/AppOverviewTab.spec.ts \
  src/pages/org/MembersPage.spec.ts \
  src/pages/knowledge/OrgKnowledgePage.spec.ts \
  src/pages/audit/AuditLogsPage.spec.ts \
  src/pages/usage/__tests__/UsagePage.spec.ts
```

Expected: PASS.

- [ ] **Step 6: Audit frontend leftovers**

Run:

```bash
rtk rg -n "组织|本组织|跨组织|组织标识|组织管理员|组织成员|组织级|所属组织|未知组织|暂无可查看组织|当前账号未关联组织" web/src --glob '!api/generated.ts'
```

Expected: Remaining matches are only developer-facing comments that explicitly mention internal `org` semantics. If any rendered string, form label, placeholder, error message, aria-label, table title, tab title, test assertion, or user-facing mock data remains, edit it to enterprise copy and rerun Step 5.

- [ ] **Step 7: Commit frontend copy changes**

Run:

```bash
rtk git add web/src
rtk git commit -m "feat(web): 将组织展示文案改为企业" -m "更新前端菜单、页面标题、表单、空态、角色标签和相关 Vitest 断言。\n\n内部 org/organization 标识、路由和 API 字段保持不变。"
```

Expected: commit succeeds and includes only frontend source/test files, not `web/src/api/generated.ts`.

---

### Task 2: Backend User-Facing Copy and OpenAPI Sources

**Files:**
- Modify: `internal/service/audit_label.go`
- Modify: `internal/service/audit_label_test.go`
- Modify: `internal/service/organization_service.go`
- Modify: `internal/service/onboarding_service.go`
- Modify: `internal/service/errors.go`
- Modify: `internal/api/handlers/request_errors_test.go`
- Modify: `internal/api/handlers/organizations_test.go`
- Modify: `internal/api/handlers/apps.go`
- Modify: `internal/api/handlers/organizations.go`
- Modify: `internal/api/handlers/members.go`
- Modify: `internal/api/handlers/knowledge.go`
- Modify: `internal/api/handlers/audit.go`
- Modify: `internal/api/handlers/recharge.go`
- Modify: `internal/api/handlers/usage.go`
- Modify: `internal/api/handlers/app_runtime.go`
- Modify: `internal/api/handlers/models.go`
- Modify: `internal/api/handlers/platform_overview.go`
- Modify: `internal/api/handlers/runtime_knowledge.go`
- Modify: `internal/api/handlers/jobs.go`
- Modify: `internal/api/handlers/dto.go`
- Modify: `internal/service/auth_service_test.go`
- Modify: `internal/service/organization_service_test.go`

- [ ] **Step 1: Capture backend user-facing baseline**

Run:

```bash
rtk rg -n "@Summary.*组织|@Description.*组织|@Param.*组织|组织标识必须|组织不存在|组织未关联|助手版本不在组织|\"组织管理员\"|\"组织成员\"|\"组织\"|\"组织充值\"|\"加入组织" internal/api internal/service internal/domain --glob '!store/sqlc/**'
```

Expected: output lists Swagger comments, DTO comments, API error strings, audit labels, and tests using old Chinese user-facing copy.

- [ ] **Step 2: Update backend tests first**

Change test expectations to enterprise terms:

```go
// internal/service/audit_label_test.go
{"org_admin", "企业管理员"},              // 企业管理员
{"org_member", "企业成员"},              // 普通企业成员
{"organization", "企业"},                // 企业资源
{"member", "create_with_app", "加入企业（含应用创建）"}, // onboarding 新成员
{"organization", "recharge", "企业充值"}, // 企业余额充值
```

```go
// internal/api/handlers/request_errors_test.go
err := fmt.Errorf("%w: 企业标识必须为 3-32 位小写字母、数字或短横线", service.ErrMemberCreateInvalid)
require.Contains(t, recorder.Body.String(), "企业标识必须")
```

```go
// internal/api/handlers/organizations_test.go
createErr: fmt.Errorf("%w: 企业标识必须为 3-32 位小写字母、数字或短横线，且不能以短横线开头或结尾", service.ErrMemberCreateInvalid),
require.Contains(t, recorder.Body.String(), "企业标识必须")
```

```go
// internal/service/organization_service_test.go
AdminDisplayName: "企业管理员"
assert.Equal(t, "企业管理员", store.createdUser.DisplayName)
```

- [ ] **Step 3: Run backend tests and verify they fail before implementation**

Run:

```bash
rtk go test ./internal/service ./internal/api/handlers
```

Expected: FAIL with old organization copy still returned from source.

- [ ] **Step 4: Update backend user-facing source copy**

Apply these exact source changes:

```go
// internal/service/audit_label.go
var roleLabels = map[string]string{
	"platform_admin": "平台管理员",
	"org_admin":      "企业管理员",
	"org_member":     "企业成员",
}

var targetTypeLabels = map[string]string{
	"organization": "企业",
	"member":       "成员",
	"app":          "实例",
}

var actionLabels = map[[2]string]string{
	{"member", "create_with_app"}: "加入企业（含应用创建）",
	{"organization", "recharge"}:  "企业充值",
}
```

```go
// internal/service/organization_service.go
return "", fmt.Errorf("%w: 企业标识必须为 3-32 位小写字母、数字或短横线，且不能以短横线开头或结尾", ErrMemberCreateInvalid)
```

```go
// internal/service/onboarding_service.go
return fmt.Errorf("%w: 所选助手版本不在企业可用范围内", ErrMemberCreateInvalid)
```

```go
// internal/service/errors.go
var ErrVersionNotInAllowlist = errors.New("助手版本不在企业允许列表内")
```

```go
// internal/api/handlers/apps.go
c.JSON(http.StatusBadRequest, apierror.New("VERSION_NOT_ALLOWED", "助手版本不在企业允许列表内"))
```

```go
// internal/api/handlers/recharge.go
c.JSON(http.StatusNotFound, apierror.New("NOT_FOUND", "企业不存在"))
c.JSON(http.StatusConflict, apierror.New("ORG_MISSING_NEWAPI_USER", "企业未关联 new-api 账户"))
```

- [ ] **Step 5: Update Swagger and DTO Chinese annotations**

In the handler and DTO files listed in this task, update only Chinese `@Summary`, `@Description`, `@Param`, and DTO field comments that generate OpenAPI descriptions. Keep route paths, path variable names, JSON field names, Go type names, and `orgId` parameter names unchanged.

Use this replacement map:

```text
创建组织 -> 创建企业
组织列表 -> 企业列表
组织详情 -> 企业详情
更新组织 -> 更新企业
禁用组织 -> 禁用企业
启用组织 -> 启用企业
组织 ID -> 企业 ID
组织成员 -> 企业成员
组织管理员 -> 企业管理员
组织级知识库 -> 企业级知识库
组织知识库 -> 企业知识库
组织审计日志列表 -> 企业审计日志列表
组织充值 -> 企业充值
组织充值历史列表 -> 企业充值历史列表
查询组织余额 -> 查询企业余额
组织用量统计 -> 企业用量统计
各组织用量分布 -> 各企业用量分布
跨所有组织 -> 跨所有企业
本组织 -> 本企业
跨组织 -> 跨企业
组织标识 -> 企业标识
组织用户 -> 企业用户
组织模型 allowlist -> 企业模型 allowlist
组织 allowlist -> 企业 allowlist
所属组织 -> 所属企业
```

Example expected annotations:

```go
// @Summary      创建企业
// @Description  平台管理员创建新企业，并同步在 new-api 侧完成账户 provisioning
// @Param        body  body      CreateOrganizationRequest     true  "创建企业请求"
```

```go
// @Summary      列出企业级知识库文件
// @Description  以扁平 RAGFlow document 列表返回企业知识库文件
// @Param        orgId       path      string  true   "企业 ID"
```

```go
// @Description  按企业 ID 分页列出成员；org_member 只能看到自己
// @Param        orgId   path      string  true   "企业 ID"
```

- [ ] **Step 6: Run backend tests and verify pass**

Run:

```bash
rtk go test ./internal/service ./internal/api/handlers
```

Expected: PASS.

- [ ] **Step 7: Audit backend leftovers**

Run:

```bash
rtk rg -n "@Summary.*组织|@Description.*组织|@Param.*组织|组织标识必须|组织不存在|组织未关联|助手版本不在组织|\"组织管理员\"|\"组织成员\"|\"组织\"|\"组织充值\"|\"加入组织" internal/api internal/service internal/domain --glob '!store/sqlc/**'
```

Expected: no matches. If matches remain in user-facing strings, Swagger comments, DTO comments, or test assertions, update them and rerun Step 6. Developer-only comments that explain internal `org` semantics are allowed only when they do not match this user-facing pattern command.

- [ ] **Step 8: Commit backend copy and OpenAPI-source changes**

Run:

```bash
rtk git add internal/api internal/service internal/domain
rtk git commit -m "feat(api): 将后端展示文案改为企业" -m "更新 API 错误消息、审计标签、Swagger 注解和 DTO 中文说明。\n\n保留 org/organization 英文标识、API 路径和角色枚举值不变。"
```

Expected: commit succeeds and does not include generated OpenAPI files yet.

---

### Task 3: Formal Documentation Copy

**Files:**
- Modify: `README.md`
- Modify: `docs/architecture.md`
- Modify: `docs/hermes-container.md`
- Modify: `docs/knowledge-base.md`
- Modify: `docs/local-development.md`
- Modify: `docs/product-design.md`
- Modify: `docs/technical-design.md`
- Modify: `docs/user-manual.md`

- [ ] **Step 1: Capture formal documentation baseline**

Run:

```bash
rtk rg -n "组织|本组织|跨组织|组织标识|组织管理员|组织成员|组织级" README.md docs --glob '!superpowers/**' --glob '!reports/**'
```

Expected: output only includes formal docs. It must not include `docs/superpowers/**` or `docs/reports/**`.

- [ ] **Step 2: Update docs copy**

Use the same term map as the spec:

```text
组织 -> 企业
组织标识 -> 企业标识
组织管理员 -> 企业管理员
组织成员 -> 企业成员
本组织 -> 本企业
跨组织 -> 跨企业
组织级知识库 -> 企业级知识库
组织级 -> 企业级
组织维度 -> 企业维度
组织数 -> 企业数
组织用量 -> 企业用量
组织充值 -> 企业充值
组织审计 -> 企业审计
```

Handle non-tenant Chinese usages explicitly:

```text
按角色组织章节 -> 按角色分章节
项目文件组织 -> 项目文件结构
组织语言 / 组织内容 -> 调整语言 / 编排内容
```

Keep code identifiers unchanged inside docs:

```text
Keep: org_id, org_code, org_admin, org_member, organization, organizations, Organization, /api/v1/organizations, CanManageOrg, CanViewOrg, org dataset
Change nearby Chinese explanation to say 企业 when describing product-facing concepts.
```

- [ ] **Step 3: Audit formal docs leftovers**

Run:

```bash
rtk rg -n "组织|本组织|跨组织|组织标识|组织管理员|组织成员|组织级" README.md docs --glob '!superpowers/**' --glob '!reports/**'
```

Expected: any remaining matches are either code identifiers containing `org/organization` with no Chinese replacement needed, or non-tenant Chinese verbs that were intentionally reworded. If a tenant-facing Chinese term remains, edit it and rerun this command.

- [ ] **Step 4: Commit documentation copy changes**

Run:

```bash
rtk git add README.md docs/architecture.md docs/hermes-container.md docs/knowledge-base.md docs/local-development.md docs/product-design.md docs/technical-design.md docs/user-manual.md
rtk git commit -m "docs: 将租户说明文案改为企业" -m "更新 README 和正式产品文档中的用户可见术语。\n\n历史 specs、plans 和 reports 保持不变，避免改写既有设计和排查记录。"
```

Expected: commit succeeds and does not include `docs/superpowers/**` or `docs/reports/**`.

---

### Task 4: OpenAPI and Generated Frontend Types

**Files:**
- Modify: `openapi/openapi.yaml`
- Modify: `web/src/api/generated.ts`

- [ ] **Step 1: Generate OpenAPI from Swagger annotations**

Run:

```bash
rtk make openapi-gen
```

Expected: command exits 0 and updates `openapi/openapi.yaml` if Swagger/DTO source comments changed.

- [ ] **Step 2: Generate frontend API types**

Run:

```bash
rtk make web-types-gen
```

Expected: command exits 0 and updates `web/src/api/generated.ts` from `openapi/openapi.yaml`.

- [ ] **Step 3: Verify OpenAPI is in sync**

Run:

```bash
rtk make openapi-check
```

Expected: command exits 0. If it reports generated files differ, inspect the generated diff, fix source comments rather than hand-editing generated files, then rerun Steps 1-3.

- [ ] **Step 4: Audit generated files for old user-facing copy**

Run:

```bash
rtk rg -n "组织|本组织|跨组织|组织标识|组织管理员|组织成员|组织级|所属组织" openapi/openapi.yaml web/src/api/generated.ts
```

Expected: no user-facing old Chinese tenant copy remains. If matches remain, identify the source comment in `internal/api/handlers` or `internal/api/handlers/dto.go`, update the source, rerun Steps 1-4, and do not hand-edit the generated files.

- [ ] **Step 5: Commit generated files**

Run:

```bash
rtk git add openapi/openapi.yaml web/src/api/generated.ts
rtk git commit -m "chore(openapi): 同步企业文案生成产物" -m "根据 Swagger 和 DTO 中文说明重新生成 OpenAPI 契约与前端 API 类型。\n\n仅中文 description/summary 文案变化，路径、schema 字段和英文标识保持不变。"
```

Expected: commit succeeds and contains only generated OpenAPI/type files.

---

### Task 5: Final Verification and Browser QA

**Files:**
- No planned source edits. If QA finds a missed user-facing string, edit the owning source file and repeat the relevant earlier task tests before finalizing.

- [ ] **Step 1: Run focused frontend tests**

Run:

```bash
rtk npm --prefix web test -- --run \
  src/domain/status.test.ts \
  src/stores/auth.spec.ts \
  src/pages/platform/OrganizationsPage.spec.ts \
  src/pages/apps/AppOverviewTab.spec.ts \
  src/pages/apps/AppAuditTab.spec.ts \
  src/pages/org/CreateMemberPage.spec.ts \
  src/pages/org/MembersPage.spec.ts \
  src/pages/knowledge/OrgKnowledgePage.spec.ts \
  src/pages/audit/AuditLogsPage.spec.ts \
  src/pages/usage/__tests__/UsagePage.spec.ts
```

Expected: PASS. `web/src/pages/platform/PermissionsPage.vue` has no dedicated spec in the current tree, so it is verified in browser QA Step 5.

- [ ] **Step 2: Run focused backend tests**

Run:

```bash
rtk go test ./internal/service ./internal/api/handlers
```

Expected: PASS.

- [ ] **Step 3: Run full generation check**

Run:

```bash
rtk make openapi-check
```

Expected: PASS and no generated diff.

- [ ] **Step 4: Run final source audit**

Run:

```bash
rtk rg -n "组织|本组织|跨组织|组织标识|组织管理员|组织成员|组织级|所属组织|未知组织|暂无可查看组织|当前账号未关联组织" \
  web/src internal/api/handlers internal/service README.md docs \
  --glob '!web/src/api/generated.ts' \
  --glob '!docs/superpowers/**' \
  --glob '!docs/reports/**'
```

Expected: remaining matches are developer-only comments or code-adjacent explanations of internal `org` semantics. There must be no rendered frontend string, API error message, Swagger/DTO generated description source, test assertion, README user-facing sentence, or formal docs user-facing tenant term using the old copy.

- [ ] **Step 5: Run browser QA**

Start or reuse the local dev environment. If the app is already running on `http://localhost:5173`, use it. If not running, start the existing project stack:

```bash
rtk make dev-up
```

Open a real browser at:

```text
http://localhost:5173
```

Verify with platform admin credentials:

```text
企业标识：留空
用户名：admin
密码：admin123
```

Routes to inspect:

```text
/login
/organizations
/knowledge
/members
/usage
/audit-logs
/platform/permissions
```

Expected visual results:

```text
登录页显示“企业标识”。
侧边栏显示“企业”。
/organizations 页面标题、按钮、表格列、充值弹窗使用企业文案。
/knowledge 页面显示“企业知识库”和“选择企业”。
/members 页面显示企业成员/企业管理员角色文案。
/usage 页面显示企业维度用量。
/audit-logs 页面显示企业审计空态或列表。
/platform/permissions 页面角色列显示“企业管理员”“企业成员”，条件说明显示“本企业/跨企业”。
```

Verify with enterprise admin credentials if local data contains the default account:

```text
企业标识：test-org
用户名：test-org
密码：test-org123
```

Expected visual results:

```text
企业管理员登录后进入企业视角首页。
菜单和首页卡片不显示“组织”作为租户展示术语。
成员、知识库、用量、账户余额、审计入口使用企业文案。
```

- [ ] **Step 6: Commit QA fixes if any**

If browser QA or final audit finds missed copy, edit the owning source file, rerun the related task tests, regenerate OpenAPI if the missed copy came from Swagger/DTO comments, then commit:

```bash
rtk git add web/src internal/api internal/service internal/domain README.md docs/architecture.md docs/hermes-container.md docs/knowledge-base.md docs/local-development.md docs/product-design.md docs/technical-design.md docs/user-manual.md openapi/openapi.yaml web/src/api/generated.ts
rtk git commit -m "fix(copy): 补齐企业展示文案" -m "修正最终验收中发现的遗漏用户可见文案。\n\n未修改内部 org/organization 标识和 API 契约字段。"
```

Expected: commit succeeds only if QA fixes were needed. If no fixes were needed, skip this step and report that no QA fix commit was created.

---

## Self-Review Checklist

- Spec coverage:
  - Frontend user-visible copy is covered by Task 1.
  - Backend API errors, audit labels, Swagger and DTO comments are covered by Task 2.
  - Formal docs are covered by Task 3.
  - OpenAPI and generated TypeScript are covered by Task 4.
  - Browser QA and final audits are covered by Task 5.
- Scope guard:
  - No task renames `org_id`, `org_admin`, `org_member`, `organization`, `/api/v1/organizations`, SQL tables, or generated sqlc code.
  - Historical `docs/superpowers/**` and `docs/reports/**` are excluded except for this new plan file.
- Verification:
  - Frontend Vitest, backend Go tests, `make openapi-check`, `rg` source audits, and real browser QA all have explicit commands or routes.
