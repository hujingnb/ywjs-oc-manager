# 成员无实例检测与补建设计

- 状态：草稿
- 日期：2026-05-15
- 作者：hujing + AI 协作

## 背景

组织管理员当前有两条新增成员的路径：

1. **「创建并初始化」**（`/members/new` → `OnboardMember`）：在同一事务里创建用户、应用、渠道绑定、初始化 job。
2. **「新增成员」**（`MembersPage.vue` 内联表单 → `CreateMember`）：仅创建用户，不创建任何应用资源。

此外，组织管理员可以通过 `DeleteMember` 联动软删该成员名下的活跃实例；运维或后续业务也可能从 `AppsPage` 直接软删某个实例。在这些路径之后，会出现「成员存在但名下没有未软删实例」的悬挂状态。

后端为这种状态准备了补建入口 `MemberOnboardingService.CreateAppForMember`，但当前前端的能力曝光有两个缺口：

- 成员列表上**看不出**某成员是否有活跃实例，组织管理员无从知道谁需要补建。
- `MembersPage.vue` 已有的「创建新实例」按钮 **hidden 条件错误**，仅对 `platform_admin` 可见，而后端 `CanCreateAppForMember` 同时允许本组织 `org_admin`。组织管理员看不到这个入口。

本设计在尽量小的改动面下补齐这两个缺口。

## 当前能力盘点

后端能力**完全就绪**：

- `service.MemberOnboardingService.CreateAppForMember`（`internal/service/onboarding_service.go:305`）在事务内完成：
  - `GetActiveAppByOwner` 校验「成员当前无活跃实例」，否则返回 `ErrMemberCreateInvalid`；
  - 走和 `OnboardMember` 一致的节点选择、模型 allowlist 校验、应用 + 渠道绑定 + 审计 + 初始化 job 写入。
- HTTP 路由 `POST /organizations/:orgId/members/:userId/apps`（`internal/api/handlers/members.go:68`、handler 实现 `:317`）。
- 授权 `auth.CanCreateAppForMember`（`internal/auth/authorizer.go:183`）：`platform_admin` 跨组织 + `org_admin` 仅本组织。
- 前端 `useCreateMemberApp` hook（`web/src/api/hooks/useMembers.ts`）已存在并被 `MembersPage` 使用。

前端**仅**缺：

1. 列表上没有「实例状态」可见性；
2. 「创建新实例」按钮的 `hidden` 条件与后端权限不一致；
3. 默认值填充体验差（手填实例名、模型每次都要选）。

## 设计

### 一、架构与数据流

```
[MembersPage.vue]                [Backend]
  ┌──────────────────────────┐
  │ 成员列表（含「实例」列）  │ ──GET── /organizations/:orgId/members
  │  ├ 有实例: 实例名(链接)   │           └→ MemberService.ListMembers
  │  └ 无实例: 警告 tag       │              └→ Store.ListUsersByOrgWithActiveApp
  │     + 「为该成员创建实例」 │                 (LEFT JOIN apps WHERE deleted_at IS NULL)
  └──────────────────────────┘

  ┌──────────────────────────┐
  │ 复用现有创建实例表单       │ ──POST─→ /organizations/:orgId/members/:userId/apps
  │ （默认值预填）             │           └→ MemberOnboardingService.CreateAppForMember
  └──────────────────────────┘             (现有事务，不动)
```

- 后端列表查询：用 `LEFT JOIN apps ... AND deleted_at IS NULL`。`apps` 表上 `apps_owner_active` 唯一约束保证每个 owner 最多一个未软删实例，JOIN 不会产生重复行。
- 「软删除」的语义沿用现有约定：`apps.deleted_at IS NULL` 即活跃。`users.deleted_at` 是下线时间戳语义（见 `AGENTS.md`），与本设计无关。

### 二、后端改动

#### 2.1 新增 sqlc 查询 `ListUsersByOrgWithActiveApp`

在 `internal/store/queries/users.sql` 新增：

```sql
-- name: ListUsersByOrgWithActiveApp :many
-- 列出组织内成员及其当前关联的活跃实例（LEFT JOIN，无实例的成员仍返回）。
-- apps 表上 apps_owner_active 唯一约束保证每个 owner 最多一个未软删实例，
-- LEFT JOIN 不会产生重复行。
SELECT u.*, a.id AS active_app_id, a.name AS active_app_name
FROM users u
LEFT JOIN apps a
  ON a.owner_user_id = u.id AND a.deleted_at IS NULL
WHERE u.org_id = $1
ORDER BY u.created_at DESC, u.id DESC
LIMIT $2 OFFSET $3;
```

生成 sqlc 代码后会得到一个新的行类型，含 `ActiveAppID pgtype.UUID` 与 `ActiveAppName pgtype.Text`，均为可空字段（`LEFT JOIN` 未命中时为 `Valid=false`）。

原有 `ListUsersByOrg` **保留不动**，避免影响其它调用方（如 cmd 启动校验或后续可能的精简查询）。

#### 2.2 `MemberService` 改造

`internal/service/member_service.go`：

- `MemberStore` 接口新增：

  ```go
  ListUsersByOrgWithActiveApp(ctx context.Context, arg sqlc.ListUsersByOrgWithActiveAppParams) ([]sqlc.ListUsersByOrgWithActiveAppRow, error)
  ```

  原 `ListUsersByOrg` 仍保留在接口中（兼容历史调用）。

- `MemberResult` 增加两个可选字段：

  ```go
  // ActiveAppID 是该成员当前未软删实例的 UUID；nil 表示成员名下没有活跃实例。
  // 前端据此在列表上区分「需要补建」与「已绑定」两种状态。
  ActiveAppID *string `json:"active_app_id,omitempty"`
  // ActiveAppName 是该成员当前活跃实例的展示名；nil 与 ActiveAppID 同步。
  // 前端用它在列表上渲染可点击的实例名链接。
  ActiveAppName *string `json:"active_app_name,omitempty"`
  ```

- `ListMembers` 调用新 query，并新增工具函数：

  ```go
  // toMemberResultsWithApp 把 sqlc 行映射为 MemberResult，附带活跃实例的可选信息。
  // 该函数只服务于成员列表场景；单条 GetMember 仍走 toMemberResult，避免单条接口范围扩散。
  func toMemberResultsWithApp(rows []sqlc.ListUsersByOrgWithActiveAppRow) []MemberResult { ... }
  ```

- 单条 `GetMember`、`UpdateMemberProfile`、`SetMemberStatus`、`ResetMemberPassword` 等接口**不变**，返回的 `MemberResult.ActiveAppID/Name` 保持为 nil（JSON 中 `omitempty` 隐藏）。这是有意的范围控制：本期只在列表场景暴露该状态，避免接口大面积变化。

#### 2.3 装配与生成产物

- `cmd/server` 中 `MemberStore` 的实现是 `*sqlc.Queries`，新增方法由 sqlc 自动生成，无需手工实现。
- `make openapi-gen`：swag 扫描会自动把 `MemberResult` 的两个新字段同步进 `openapi/openapi.yaml`。
- `make web-types-gen`：把 yaml 同步进 `web/src/api/generated.ts`。
- 单元测试中的内存 `MemberStore` 桩（`member_service_test.go`、`organization_service_test.go` 等）需要补一个 `ListUsersByOrgWithActiveApp` 桩实现。

### 三、前端改动（`web/src/pages/org/MembersPage.vue`）

#### 3.1 新增「实例」列

```ts
import { h } from 'vue'
import { RouterLink } from 'vue-router'
import { NTag } from 'naive-ui'

{
  title: '实例',
  key: 'active_app_name',
  render: (row: Member) =>
    row.active_app_id
      ? h(RouterLink, { to: `/apps/${row.active_app_id}/overview` }, () => row.active_app_name)
      : h(NTag, { type: 'warning', size: 'small' }, () => '无实例'),
}
```

- 「无实例」用 `n-tag type="warning"`（黄色语义），让组织管理员一眼识别需要补建的行。
- 有实例时展示可点击的实例名，跳转到现有 `apps/:appId/overview` 深链（router 已支持，见 `web/src/app/router.ts:66`）。

#### 3.2 修复「创建新实例」按钮可见性

新增组合权限计算：

```ts
// canCreateAppForMember 与后端 auth.CanCreateAppForMember 对齐：
// platform_admin 跨组织可补建；org_admin 仅在本组织可补建；普通成员不可。
const canCreateAppForMember = computed(() =>
  auth.user?.role === 'platform_admin' ||
  (auth.user?.role === 'org_admin' && auth.user?.org_id === effectiveOrgId.value))
```

替换 `MembersPage.vue:281` 的 action 定义：

```ts
{
  label: '为该成员创建实例',
  type: 'primary',
  // 仅在「可补建」且「行无活跃实例」时显示，避免误点导致 ErrMemberCreateInvalid 兜底。
  hidden: r => !canCreateAppForMember.value || Boolean(r.active_app_id),
  onClick: r => openCreateAppForm(r),
},
```

按钮标签从「创建新实例」改为「为该成员创建实例」，文案更直白。

#### 3.3 表单默认值填充

`openCreateAppForm(member)` 改为：

```ts
function openCreateAppForm(member: Member) {
  createAppTarget.value = member
  createAppResult.value = null
  createAppError.value = ''
  createAppForm.value = {
    app_name: `${member.display_name} 的实例`,
    persona_mode: 'org_inherited',
    channel_type: 'wechat',
    model_id: String(modelOptions.value[0]?.value ?? ''),
  }
}
```

- `app_name` 默认填「{显示名} 的实例」，管理员可手动改。
- `model_id` 默认取组织 `enabled_models` 的第一个。

#### 3.4 删除按钮不动

`MembersPage.vue:280` 的删除按钮 hidden 条件**保持原状**，与新逻辑相互独立。已无实例的成员仍允许删除（即下线用户账号），避免范围扩散到删除流程。

### 四、影响范围

- **代码改动**：
  - 后端：`internal/store/queries/users.sql`、`internal/service/member_service.go` 及测试、各处内存桩补方法。
  - 生成产物：`internal/store/sqlc/users.sql.go`、`openapi/openapi.yaml`、`web/src/api/generated.ts`。
  - 前端：`web/src/pages/org/MembersPage.vue`、`web/src/pages/org/MembersPage.spec.ts`。
- **不动**：
  - `CreateAppForMember` 后端事务、权限规则、`OnboardMember` 路径、`DeleteMember` 联动逻辑。
  - 单条成员查询接口（`GetMember` 等）的返回结构。
  - `MembersPage` 删除按钮的可见性逻辑。

## 测试

### 后端单元测试 (`internal/service/member_service_test.go`)

补充 `TestListMembers_WithActiveApp` 子测试，覆盖三种场景：

1. **成员有活跃实例**：桩返回 `ActiveAppID.Valid=true`，断言 `MemberResult.ActiveAppID/Name` 为对应字符串指针。
2. **成员无活跃实例**：桩返回 `ActiveAppID.Valid=false`，断言两字段为 nil。
3. **混合**：同一组织内一些成员有实例、一些没有，断言各行字段独立、不串行。

每条 table-driven 用例 + 子测试都按 `AGENTS.md` 要求加中文注释说明业务场景。

### 前端单元测试 (`web/src/pages/org/MembersPage.spec.ts`)

补充：

- `org_admin` 登录、列表中存在无实例成员行时，「为该成员创建实例」按钮可见；
- 同样登录、有实例的成员行该按钮 hidden；
- 点击按钮后 `createAppForm.app_name` 默认值为 `${显示名} 的实例`，`model_id` 取组织第一个模型；
- 实例已绑定的行显示 `RouterLink` 而非 `NTag`。

### 浏览器手工验收（必走）

按 `AGENTS.md` 要求，全功能验证：

1. 用 `test-org` / `test-org123` 登录组织管理员。
2. 在成员页用「新增成员」入口创建一个无实例的新成员。
3. 列表中该行显示警告色「无实例」tag 和「为该成员创建实例」按钮。
4. 点击按钮，表单 `app_name` 预填、模型已选中；填充 prompt（可选）后提交。
5. 列表刷新后该行变成可点击的实例名链接；点击跳转 AppsPage 详情。
6. 在 AppsPage 软删该实例，回到成员列表，该行恢复「无实例」+ 按钮可见。
7. 重复一次 5–6，验证幂等。
8. 用 `platform_admin`（`admin` / `admin123`，组织标识留空）登录后切换到 `test-org` 组织，验证同样的按钮与列展示，原 platform_admin 跨组织能力不退化。

## 交付检查

- 跑 `make openapi-check`，确认 yaml 与代码同步。
- 跑相关单元测试：`go test ./internal/service/...` 及前端 `pnpm test` 对应 spec。
- 浏览器全功能验收通过后再交付。
- 提交按 `AGENTS.md` 拆分，建议提交节奏：
  1. `feat(member): 成员列表暴露活跃实例信息`（含 sqlc query、service、生成产物、单测）。
  2. `feat(web/member): 列表显示实例状态并允许组织管理员补建`（前端 UI + spec）。

## 风险与权衡

- **范围控制**：单条 `GetMember` 不改，前端只在列表用到这两字段，避免一次扩散到所有成员接口。如未来 `MemberDetail` 也需要展示活跃实例，再独立扩展。
- **`apps_owner_active` 唯一约束**：本设计依赖该约束保证 LEFT JOIN 不重复。若未来允许「同一成员多实例」，需要重新审视列表展示策略（届时改为 `MAX(created_at)` 或返回数组）。
- **跳转一致性**：列表里点击实例名跳的是 `/apps/:appId/overview`，与 `AppsPage` 主入口一致。如果未来 overview tab 路径变化，需要随之调整。
- **删除按钮独立**：删除按钮保持原可见性，与「无实例」状态相互独立，避免功能耦合。组织管理员可以对无实例成员先补建实例再使用、也可以直接下线该用户。
