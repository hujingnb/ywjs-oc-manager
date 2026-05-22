# 成员无实例检测与补建 实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让组织管理员在成员列表上识别「成员名下无活跃实例」的状态，并允许其直接为该成员补建实例。

**Architecture:** 后端用 LEFT JOIN 在 `ListMembers` 查询里附带成员当前未软删的实例 id/name；前端列表新增「实例」列，无实例时显示警告色 tag 并暴露「为该成员创建实例」按钮（复用现有 `CreateAppForMember` 表单 + 默认值预填）。后端 `CreateAppForMember` 事务、权限规则、删除按钮等保持不变。

**Tech Stack:** Go (sqlc, pgx/v5, testify, swag), Vue 3 + TypeScript + naive-ui + Vitest，Makefile (`make openapi-gen` / `make web-types-gen`)。

**Spec:** `docs/superpowers/specs/2026-05-15-member-without-instance-rebuild-design.md`

---

## 文件影响范围

| 路径 | 动作 | 责任 |
| --- | --- | --- |
| `internal/store/queries/users.sql` | 修改 | 新增 `ListUsersByOrgWithActiveApp :many` 查询 |
| `internal/store/sqlc/users.sql.go` | 生成 | sqlc 自动生成的查询代码与 Row 类型 |
| `internal/store/sqlc/querier.go` | 生成 | sqlc 自动同步接口签名 |
| `internal/service/member_service.go` | 修改 | 扩展 `MemberStore` 接口、`MemberResult` 字段；新增 `toMemberResultsWithApp`；`ListMembers` 切到新查询 |
| `internal/service/member_service_test.go` | 修改 | 内存桩补 `ListUsersByOrgWithActiveApp`；新增 ListMembers 在有/无活跃实例两类场景下的断言 |
| `openapi/openapi.yaml` | 生成 | swag 重新扫描产物 |
| `web/src/api/generated.ts` | 生成 | OpenAPI → TS 类型同步 |
| `web/src/pages/org/MembersPage.vue` | 修改 | 新增「实例」列；改 `canCreateAppForMember` 计算与按钮 `hidden`；表单默认值预填；按钮文案改为「为该成员创建实例」 |
| `web/src/pages/org/MembersPage.spec.ts` | 修改 | mock 数据加 `active_app_id/active_app_name`；用新按钮文案；补按钮可见性、默认值与跳转链接断言 |

---

## Task 1: 新增 sqlc 查询并生成代码

**Files:**
- Modify: `internal/store/queries/users.sql`（末尾追加）
- Generate: `internal/store/sqlc/users.sql.go`、`internal/store/sqlc/querier.go`

- [ ] **Step 1: 在 `internal/store/queries/users.sql` 末尾追加新查询**

```sql
-- name: ListUsersByOrgWithActiveApp :many
-- 列出组织内成员及其当前关联的活跃实例（LEFT JOIN，无实例的成员仍返回）。
-- apps 表上 apps_owner_active 唯一约束保证每个 owner 最多一个未软删实例，
-- LEFT JOIN 不会产生重复行；ORDER BY 保持与 ListUsersByOrg 一致。
SELECT u.*, a.id AS active_app_id, a.name AS active_app_name
FROM users u
LEFT JOIN apps a
  ON a.owner_user_id = u.id AND a.deleted_at IS NULL
WHERE u.org_id = $1
ORDER BY u.created_at DESC, u.id DESC
LIMIT $2 OFFSET $3;
```

- [ ] **Step 2: 重新生成 sqlc 代码**

Run:
```bash
make sqlc-gen 2>/dev/null || sqlc generate
```

Expected：
- 终端无报错。
- `internal/store/sqlc/users.sql.go` 出现 `const listUsersByOrgWithActiveApp = ` 常量、`type ListUsersByOrgWithActiveAppParams struct` 和 `type ListUsersByOrgWithActiveAppRow struct`（含 11 个 users 列 + `ActiveAppID pgtype.UUID` + `ActiveAppName pgtype.Text`）。
- `internal/store/sqlc/querier.go` `Querier` 接口出现 `ListUsersByOrgWithActiveApp(ctx context.Context, arg ListUsersByOrgWithActiveAppParams) ([]ListUsersByOrgWithActiveAppRow, error)`。

如果 Makefile 没有 `sqlc-gen` target，直接 `sqlc generate`。

- [ ] **Step 3: 编译确认**

Run: `go build ./...`
Expected：通过。新生成的方法不破坏 `*sqlc.Queries`，Querier 接口扩展不影响调用方（service 还没改）。

- [ ] **Step 4: 提交**

```bash
git add internal/store/queries/users.sql internal/store/sqlc/users.sql.go internal/store/sqlc/querier.go
git commit -m "$(cat <<'EOF'
feat(store): 新增 ListUsersByOrgWithActiveApp 查询

为成员列表展示「成员当前未软删实例」做基础查询。LEFT JOIN
apps 表并以 deleted_at IS NULL 过滤活跃实例，apps_owner_active
唯一约束保证不产生重复行，没有活跃实例的成员仍以 active_app_id
为 NULL 的形式返回。

仅生成 sqlc 代码，service 层切换在下一个提交完成。
EOF
)"
```

---

## Task 2: MemberStore 接口与桩补 `ListUsersByOrgWithActiveApp`

**Files:**
- Modify: `internal/service/member_service.go`（仅 `MemberStore` 接口）
- Modify: `internal/service/member_service_test.go`（仅 stub）

- [ ] **Step 1: 在 `MemberStore` 接口加方法**

打开 `internal/service/member_service.go`，把现有：

```go
type MemberStore interface {
	GetOrganization(ctx context.Context, id pgtype.UUID) (sqlc.Organization, error)
	CreateUser(ctx context.Context, arg sqlc.CreateUserParams) (sqlc.User, error)
	GetUser(ctx context.Context, id pgtype.UUID) (sqlc.User, error)
	GetUserByUsername(ctx context.Context, username string) (sqlc.User, error)
	ListUsersByOrg(ctx context.Context, arg sqlc.ListUsersByOrgParams) ([]sqlc.User, error)
```

改为：

```go
type MemberStore interface {
	GetOrganization(ctx context.Context, id pgtype.UUID) (sqlc.Organization, error)
	CreateUser(ctx context.Context, arg sqlc.CreateUserParams) (sqlc.User, error)
	GetUser(ctx context.Context, id pgtype.UUID) (sqlc.User, error)
	GetUserByUsername(ctx context.Context, username string) (sqlc.User, error)
	ListUsersByOrg(ctx context.Context, arg sqlc.ListUsersByOrgParams) ([]sqlc.User, error)
	// ListUsersByOrgWithActiveApp 列出成员及其当前未软删实例的 id/name，
	// 用于成员列表上区分「需要补建」与「已绑定」两种状态。
	ListUsersByOrgWithActiveApp(ctx context.Context, arg sqlc.ListUsersByOrgWithActiveAppParams) ([]sqlc.ListUsersByOrgWithActiveAppRow, error)
```

`ListUsersByOrg` 保留不动，避免破坏其它调用方接口契约。

- [ ] **Step 2: 在 `memberStoreStub` 加字段与方法**

打开 `internal/service/member_service_test.go`，在 `memberStoreStub` 结构（约 `:347`）的字段列表里追加一个用于回放参数的字段：

```go
	lastListWithApp    sqlc.ListUsersByOrgWithActiveAppParams
```

在 `func (s *memberStoreStub) ListUsersByOrg(...)`（约 `:420`）之后追加新方法：

```go
// ListUsersByOrgWithActiveApp 模拟 sqlc 的 LEFT JOIN：先取本组织全部 users，
// 再为每个 user 查找 apps 表中未软删的实例。apps_owner_active 约束保证最多一个。
func (s *memberStoreStub) ListUsersByOrgWithActiveApp(_ context.Context, arg sqlc.ListUsersByOrgWithActiveAppParams) ([]sqlc.ListUsersByOrgWithActiveAppRow, error) {
	s.lastListWithApp = arg
	rows := make([]sqlc.ListUsersByOrgWithActiveAppRow, 0, len(s.users))
	for _, user := range s.users {
		if user.OrgID != arg.OrgID {
			continue
		}
		row := sqlc.ListUsersByOrgWithActiveAppRow{
			ID:           user.ID,
			OrgID:        user.OrgID,
			Username:     user.Username,
			PasswordHash: user.PasswordHash,
			DisplayName:  user.DisplayName,
			Role:         user.Role,
			Status:       user.Status,
			LastLoginAt:  user.LastLoginAt,
			CreatedAt:    user.CreatedAt,
			UpdatedAt:    user.UpdatedAt,
			DeletedAt:    user.DeletedAt,
		}
		for _, app := range s.apps {
			if app.OwnerUserID == user.ID && !app.DeletedAt.Valid {
				row.ActiveAppID = app.ID
				row.ActiveAppName = pgtype.Text{String: app.Name, Valid: true}
				break
			}
		}
		rows = append(rows, row)
	}
	return rows, nil
}
```

- [ ] **Step 3: 编译确认接口 + 桩可用**

Run: `go build ./...`
Expected：通过。`*sqlc.Queries` 已经由 Task 1 满足新方法，stub 也补齐。

- [ ] **Step 4: 跑现有成员测试不退化**

Run: `go test ./internal/service/ -run MemberService -v`
Expected：现有用例（限制 scope、默认分页、最大分页）全部 PASS，没有副作用。

- [ ] **Step 5: 提交（与 Task 3 合并提交，本步骤先暂存）**

无独立 commit，留到 Task 3 一起。

---

## Task 3: `MemberResult` 字段 + `ListMembers` 切换查询（TDD）

**Files:**
- Modify: `internal/service/member_service_test.go`（先加测试）
- Modify: `internal/service/member_service.go`（后实现）

- [ ] **Step 1: 写失败的 ListMembers 活跃实例测试**

在 `internal/service/member_service_test.go` 既有 `TestMemberServiceListClampsMaxPageSize` 之后追加：

```go
// TestMemberServiceListExposesActiveApp 验证 ListMembers 返回每个成员当前关联的活跃实例。
// 三类场景必须同时覆盖：有活跃实例、无活跃实例、实例被软删的成员都应正确还原。
func TestMemberServiceListExposesActiveApp(t *testing.T) {
	// withApp：拥有未软删 app 的成员；列表应返回 active_app_id/name 指针。
	// noApp：组织成员只创建了用户，未来需要补建；返回值两字段为 nil。
	// deletedApp：成员名下唯一的 app 已被软删，等同于「无实例」状态。
	store := newMemberStoreStub(t)
	orgUUID := store.orgs[testOrgID].ID

	withAppID := mustUUID(t, "00000000-0000-0000-0000-0000000000a1")
	noAppID := mustUUID(t, "00000000-0000-0000-0000-0000000000a2")
	deletedID := mustUUID(t, "00000000-0000-0000-0000-0000000000a3")
	store.users[uuidToString(withAppID)] = sqlc.User{ID: withAppID, OrgID: orgUUID, Username: "with-app", DisplayName: "有实例的成员", Role: domain.UserRoleOrgMember, Status: domain.StatusActive}
	store.users[uuidToString(noAppID)] = sqlc.User{ID: noAppID, OrgID: orgUUID, Username: "no-app", DisplayName: "无实例的成员", Role: domain.UserRoleOrgMember, Status: domain.StatusActive}
	store.users[uuidToString(deletedID)] = sqlc.User{ID: deletedID, OrgID: orgUUID, Username: "deleted-app", DisplayName: "实例被删的成员", Role: domain.UserRoleOrgMember, Status: domain.StatusActive}

	activeAppID := mustUUID(t, "00000000-0000-0000-0000-0000000000b1")
	deletedAppID := mustUUID(t, "00000000-0000-0000-0000-0000000000b2")
	store.apps[uuidToString(activeAppID)] = sqlc.App{ID: activeAppID, OrgID: orgUUID, OwnerUserID: withAppID, Name: "现役实例"}
	store.apps[uuidToString(deletedAppID)] = sqlc.App{ID: deletedAppID, OrgID: orgUUID, OwnerUserID: deletedID, Name: "已删实例", DeletedAt: pgtype.Timestamptz{Valid: true}}

	svc := NewMemberService(store, fakeHash)
	results, err := svc.ListMembers(context.Background(), platformAdmin(), testOrgID, 0, 0)
	require.NoError(t, err)
	require.Len(t, results, 3)

	byUsername := map[string]MemberResult{}
	for _, r := range results {
		byUsername[r.Username] = r
	}

	// 有活跃实例：active_app_id 指向 activeAppID 字符串，active_app_name 为应用名。
	withAppResult := byUsername["with-app"]
	require.NotNil(t, withAppResult.ActiveAppID)
	assert.Equal(t, uuidToString(activeAppID), *withAppResult.ActiveAppID)
	require.NotNil(t, withAppResult.ActiveAppName)
	assert.Equal(t, "现役实例", *withAppResult.ActiveAppName)

	// 没创建实例：两字段为 nil 指针，前端据此显示「无实例」+ 补建按钮。
	noAppResult := byUsername["no-app"]
	assert.Nil(t, noAppResult.ActiveAppID)
	assert.Nil(t, noAppResult.ActiveAppName)

	// 实例被软删：active_app_id/name 也为 nil；与「从未创建」语义一致。
	deletedAppResult := byUsername["deleted-app"]
	assert.Nil(t, deletedAppResult.ActiveAppID)
	assert.Nil(t, deletedAppResult.ActiveAppName)
}
```

确保文件顶部已 `import "github.com/stretchr/testify/assert"`（若没有，加上）。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/service/ -run TestMemberServiceListExposesActiveApp -v`
Expected：FAIL，原因是 `MemberResult` 没有 `ActiveAppID` / `ActiveAppName` 字段（compile error 也算 fail）。

- [ ] **Step 3: 在 `MemberResult` 加字段**

打开 `internal/service/member_service.go`，把 `MemberResult` 改为：

```go
// MemberResult 是对外返回的成员视图，剥离了密码等敏感字段。
type MemberResult struct {
	// ID 是成员用户 UUID。
	ID string `json:"id"`
	// OrgID 是成员所属组织 UUID；platform_admin 可能为空。
	OrgID string `json:"org_id,omitempty"`
	// Username 是登录账号名。
	Username string `json:"username"`
	// DisplayName 是前端展示名。
	DisplayName string `json:"display_name"`
	// Role 是成员角色，限定为 org_admin 或 org_member。
	Role string `json:"role"`
	// Status 是成员状态；disabled 会阻止登录并设置 users.deleted_at。
	Status string `json:"status"`
	// ActiveAppID 是该成员当前未软删实例的 UUID；nil 表示成员名下没有活跃实例。
	// 仅在 ListMembers 列表返回里有值，单条 GetMember 等接口保持 nil。
	ActiveAppID *string `json:"active_app_id,omitempty"`
	// ActiveAppName 是该成员当前活跃实例的展示名；nil 与 ActiveAppID 同步。
	ActiveAppName *string `json:"active_app_name,omitempty"`
}
```

- [ ] **Step 4: 新增 `toMemberResultsWithApp` 工具函数**

在 `toMemberResult` 函数末尾追加：

```go
// toMemberResultsWithApp 把 sqlc 的 LEFT JOIN 行映射为 MemberResult。
// 仅当 active_app_id 在数据库层有值时才把指针填上，避免误判「无实例」为空字符串。
func toMemberResultsWithApp(rows []sqlc.ListUsersByOrgWithActiveAppRow) []MemberResult {
	results := make([]MemberResult, 0, len(rows))
	for _, row := range rows {
		result := MemberResult{
			ID:          uuidToString(row.ID),
			OrgID:       uuidToOptionalString(row.OrgID),
			Username:    row.Username,
			DisplayName: row.DisplayName,
			Role:        row.Role,
			Status:      row.Status,
		}
		if row.ActiveAppID.Valid {
			id := uuidToString(row.ActiveAppID)
			result.ActiveAppID = &id
		}
		if row.ActiveAppName.Valid {
			name := row.ActiveAppName.String
			result.ActiveAppName = &name
		}
		results = append(results, result)
	}
	return results
}
```

- [ ] **Step 5: 让 `ListMembers` 切到新查询**

在 `member_service.go` 找到 `ListMembers` 的查询调用（约 `:164`）：

```go
	users, err := s.store.ListUsersByOrg(ctx, sqlc.ListUsersByOrgParams{
		OrgID:  id,
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		return nil, fmt.Errorf("查询成员列表失败: %w", err)
	}
	return toMemberResults(users), nil
```

改为：

```go
	rows, err := s.store.ListUsersByOrgWithActiveApp(ctx, sqlc.ListUsersByOrgWithActiveAppParams{
		OrgID:  id,
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		return nil, fmt.Errorf("查询成员列表失败: %w", err)
	}
	return toMemberResultsWithApp(rows), nil
```

- [ ] **Step 6: 更新历史测试中读 `store.lastList` 的断言**

`TestMemberServiceListAppliesDefaultPageSize`（约 `:136`）和 `TestMemberServiceListClampsMaxPageSize`（约 `:146`）当前断言 `store.lastList.Limit`。`ListMembers` 切到新查询后这两处必须改成读 `lastListWithApp`：

```go
	require.Equal(t, int32(50), store.lastListWithApp.Limit)
```

```go
	require.Equal(t, int32(200), store.lastListWithApp.Limit)
```

- [ ] **Step 7: 跑测试确认全部通过**

Run: `go test ./internal/service/ -run "TestMemberService" -v`
Expected：原有用例 + `TestMemberServiceListExposesActiveApp` 全部 PASS。

- [ ] **Step 8: 跑全部 service 测试避免其它包被波及**

Run: `go test ./internal/service/...`
Expected：全绿。如果 `onboarding_service_test.go` 出现编译/运行错误，说明遗漏接口实现，回到 Task 2 Step 2 补齐对应桩。

- [ ] **Step 9: 提交**

```bash
git add internal/service/member_service.go internal/service/member_service_test.go
git commit -m "$(cat <<'EOF'
feat(member): ListMembers 返回成员关联活跃实例信息

MemberResult 新增可选的 active_app_id / active_app_name，
ListMembers 切到新的 ListUsersByOrgWithActiveApp 查询，
单条 GetMember 等接口保持原返回结构，避免范围扩散。

单元测试覆盖三种活跃实例状态：拥有实例、未创建实例、
实例已软删，确保「无实例」语义对前端补建按钮可靠。
EOF
)"
```

---

## Task 4: 同步 OpenAPI 与前端类型生成产物

**Files:**
- Generate: `openapi/openapi.yaml`
- Generate: `web/src/api/generated.ts`

- [ ] **Step 1: 生成 OpenAPI**

Run: `make openapi-gen`
Expected：终端输出 swag generation 完成，无报错。

- [ ] **Step 2: 校验生成产物与代码同步**

Run: `make openapi-check`
Expected：git 工作区里只有 `openapi/openapi.yaml` 出现新增字段，且 `make openapi-gen` 再次运行不会产生新 diff。

```bash
git diff openapi/openapi.yaml | grep -A2 "active_app"
```

应能看到：

```
+        active_app_id:
+          type: string
...
+        active_app_name:
+          type: string
```

如果未看到，回到 Task 3 检查字段 JSON 标签和注释格式。

- [ ] **Step 3: 生成前端类型**

Run: `make web-types-gen`
Expected：`web/src/api/generated.ts` 中 `service.MemberResult` 接口出现两个可选字段 `active_app_id?: string` 和 `active_app_name?: string`。

```bash
grep -A2 "MemberResult" web/src/api/generated.ts | head -20
```

- [ ] **Step 4: 编译前端确认类型无破坏**

Run: `cd web && pnpm typecheck 2>&1 | tail`
Expected：无类型错误（`Member` 类型自动从 generated.ts 继承新字段）。

- [ ] **Step 5: 提交**

```bash
git add openapi/openapi.yaml web/src/api/generated.ts
git commit -m "$(cat <<'EOF'
chore(api): 同步 active_app_id/name 到 openapi 与前端类型

MemberResult 的两个可选字段经 swag 与 openapi-to-ts 同步进生成
产物，前端可直接读取 Member.active_app_id / active_app_name。
EOF
)"
```

---

## Task 5: 前端 MembersPage 单测先行（TDD）

**Files:**
- Modify: `web/src/pages/org/MembersPage.spec.ts`

- [ ] **Step 1: 在测试 mock 数据上加 `active_app_id` / `active_app_name`**

打开 `web/src/pages/org/MembersPage.spec.ts`，把 `useMembersQuery` mock 中的 `data` 改为：

```ts
  useMembersQuery: () => ({
    data: ref<Member[]>([
      {
        id: 'admin-1',
        org_id: 'org-1',
        username: 'org-admin',
        display_name: '组织管理员',
        role: 'org_admin',
        status: 'active',
        active_app_id: 'app-admin-1',
        active_app_name: '管理员的实例',
      },
      {
        id: 'member-1',
        org_id: 'org-1',
        username: 'member',
        display_name: '组织成员',
        role: 'org_member',
        status: 'active',
      },
    ]),
    isLoading: ref(false),
  }),
```

- [ ] **Step 2: 调整既有「平台管理员可看到每个成员行的创建新实例入口」用例文案与语义**

按设计，按钮文案统一改为「为该成员创建实例」，且只在无实例的成员行显示。把原用例：

```ts
// 平台管理员可在每个成员行看到创建新实例入口，包括与当前平台管理员同 ID 的成员行。
it('平台管理员可看到每个成员行的创建新实例入口', () => {
  authUser.current = { id: 'admin-1', role: 'platform_admin' }

  const wrapper = mountPage()

  const createAppButtons = wrapper.findAll('button').filter(button => button.text() === '创建新实例')
  expect(createAppButtons).toHaveLength(2)
})
```

改为：

```ts
// 平台管理员仅在没有活跃实例的成员行看到补建入口；与新版 hidden 条件保持一致。
it('平台管理员只在无实例成员行看到补建入口', () => {
  authUser.current = { id: 'admin-1', role: 'platform_admin' }

  const wrapper = mountPage()

  const createAppButtons = wrapper.findAll('button').filter(button => button.text() === '为该成员创建实例')
  expect(createAppButtons).toHaveLength(1)
})
```

同时把同一文件下其它出现 `'创建新实例'` 字面量的位置（about line 223 / 246）改为 `'为该成员创建实例'`。

把「组织管理员看不到平台复建实例入口」用例改为：

```ts
// 组织管理员可以看到「为该成员创建实例」入口，但只对没有活跃实例的成员行显示。
it('组织管理员可见无实例成员的补建入口', () => {
  authUser.current = { id: 'admin-1', role: 'org_admin', org_id: 'org-1' }

  const wrapper = mountPage()

  const buttons = wrapper.findAll('button').filter(button => button.text() === '为该成员创建实例')
  expect(buttons).toHaveLength(1)
})
```

把「平台管理员提交创建新实例时带上默认模型并展示结果」用例中 click 那一行：

```ts
await wrapper.findAll('button').filter(button => button.text() === '创建新实例')[1].trigger('click')
```

改为（mock 里 member-1 是唯一无实例，第 0 个就是目标）：

```ts
await wrapper.findAll('button').filter(button => button.text() === '为该成员创建实例')[0].trigger('click')
```

并把 input value 的注入路径改为「确认默认值已填入再覆盖」：

```ts
// 默认 app_name 预填为「{显示名} 的实例」，测试覆盖默认值后再改名走表单提交。
const appNameInput = wrapper.find('input')
expect((appNameInput.element as HTMLInputElement).value).toBe('组织成员 的实例')
await appNameInput.setValue('新实例')
```

「平台管理员切换组织时关闭创建新实例表单」用例同样把字面量改为 `'为该成员创建实例'`。

- [ ] **Step 3: 新增「实例列展示已绑定实例链接」用例**

在 `describe('MembersPage', ...)` 内追加：

```ts
// 列表「实例」列在有活跃实例时渲染可点击链接，跳转到 /apps/:appId/overview。
it('已绑定实例的成员行展示可点击实例链接', () => {
  authUser.current = { id: 'admin-1', role: 'org_admin', org_id: 'org-1' }

  const wrapper = mountPage()

  const link = wrapper.find('a[href="/apps/app-admin-1/overview"]')
  expect(link.exists()).toBe(true)
  expect(link.text()).toBe('管理员的实例')
})

// 列表「实例」列在无活跃实例时展示「无实例」警告 tag。
it('无实例的成员行展示无实例 tag', () => {
  authUser.current = { id: 'admin-1', role: 'org_admin', org_id: 'org-1' }

  const wrapper = mountPage()

  expect(wrapper.text()).toContain('无实例')
})
```

mountPage stubs 中已经把 `DataTableList` 替换为简单 table；为支持 `RouterLink` 渲染为 `<a>`，需要把 `vue-router` mock 与 `NTag` stub 也补上。打开 `vi.mock('vue-router', ...)` 改为：

```ts
vi.mock('vue-router', async () => {
  const actual = await import('vue-router')
  return {
    ...actual,
    useRouter: () => ({ push: vi.fn() }),
    RouterLink: defineComponent({
      props: ['to'],
      setup(props, { slots }) {
        return () => h('a', { href: typeof props.to === 'string' ? props.to : props.to?.path ?? '' }, slots.default?.())
      },
    }),
  }
})
```

在 `global.stubs` 中追加：

```ts
NTag: defineComponent({
  setup(_, { slots }) {
    return () => h('span', slots.default?.())
  },
}),
```

> 注意：`defineComponent` 在 mock 工厂里要从 `vi.hoisted` 之外的 `vue` 引入；文件头部已有 `import { ..., defineComponent, h, ref ... } from 'vue'`，无需新增。

- [ ] **Step 4: 跑测试确认失败**

Run: `cd web && pnpm test -- MembersPage.spec`
Expected：FAIL，错误信息包括「找不到 `a[href="/apps/app-admin-1/overview"]`」、「找不到「为该成员创建实例」按钮」等，因为页面尚未实现。

- [ ] **Step 5: 暂不提交**

留到 Task 6 实现后一起提交。

---

## Task 6: 前端 MembersPage 实现实例列与按钮逻辑

**Files:**
- Modify: `web/src/pages/org/MembersPage.vue`

- [ ] **Step 1: 引入 `RouterLink` 与 `NTag`**

打开 `web/src/pages/org/MembersPage.vue`，在 `<script setup lang="ts">` 顶部 import 段：

```ts
import { computed, h, ref, watch } from 'vue'
import { useRouter, RouterLink } from 'vue-router'
import {
  NButton, NCard, NForm, NFormItem, NGrid, NGridItem,
  NInput, NSelect, NSpace, NTag, type SelectOption,
} from 'naive-ui'
```

（`h` 已在依赖列表里就保留；`NTag` 是 naive-ui 内置组件，确认 `pnpm list naive-ui` 有即可，无需额外安装。）

- [ ] **Step 2: 新增 `canCreateAppForMember` 计算**

在已有 `canManageMembers` 之后追加：

```ts
// canCreateAppForMember 与后端 auth.CanCreateAppForMember 对齐：
// platform_admin 跨组织可补建；org_admin 仅在本组织可补建；普通成员不可。
const canCreateAppForMember = computed(() =>
  auth.user?.role === 'platform_admin' ||
  (auth.user?.role === 'org_admin' && auth.user?.org_id === effectiveOrgId.value))
```

- [ ] **Step 3: columns 加入「实例」列**

在 `const columns = [` 内部，把 `statusColumn<Member>('状态', ...)` 之后插入：

```ts
{
  title: '实例',
  key: 'active_app_name',
  render: (row: Member) =>
    row.active_app_id
      ? h(RouterLink, { to: `/apps/${row.active_app_id}/overview` }, () => row.active_app_name ?? '')
      : h(NTag, { type: 'warning', size: 'small' }, () => '无实例'),
},
```

- [ ] **Step 4: 修复「创建新实例」按钮**

把 `actionColumn<Member>([...])` 里这一行：

```ts
{ label: '创建新实例', type: 'primary', hidden: () => auth.user?.role !== 'platform_admin', onClick: r => openCreateAppForm(r) },
```

改为：

```ts
// 仅在「当前账号有补建权限」且「该行没有活跃实例」时显示，避免点击后被后端 ErrMemberCreateInvalid 兜底。
{ label: '为该成员创建实例', type: 'primary',
  hidden: r => !canCreateAppForMember.value || Boolean(r.active_app_id),
  onClick: r => openCreateAppForm(r) },
```

- [ ] **Step 5: 默认值填充**

把 `openCreateAppForm`（约 `:307`）改为：

```ts
// openCreateAppForm 打开补建实例表单，默认 app_name 取「{显示名} 的实例」，
// 模型默认取组织 enabled_models 首项，减少组织管理员手填项。
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

- [ ] **Step 6: 跑前端单元测试确认通过**

Run: `cd web && pnpm test -- MembersPage.spec`
Expected：全部 PASS。

- [ ] **Step 7: 类型检查**

Run: `cd web && pnpm typecheck`
Expected：无错误。

- [ ] **Step 8: lint（如项目配置）**

Run: `cd web && pnpm lint 2>/dev/null || true`
Expected：通过；若项目未配置 lint，命令退出码非零也忽略，但需手动 review 改动的 `.vue` 文件。

- [ ] **Step 9: 提交**

```bash
git add web/src/pages/org/MembersPage.vue web/src/pages/org/MembersPage.spec.ts
git commit -m "$(cat <<'EOF'
feat(web/member): 成员列表暴露实例状态并允许组织管理员补建

成员列表新增「实例」列：有活跃实例时渲染指向
/apps/:appId/overview 的链接，无实例时显示警告 tag。
「创建新实例」按钮文案改为「为该成员创建实例」，并按后端
CanCreateAppForMember 的规则放开给本组织 org_admin，且仅在
无活跃实例的成员行显示，避免点击后被后端 ErrMemberCreateInvalid 兜底。

补建表单默认填入「{显示名} 的实例」与组织首个可用模型，
减少组织管理员手填项；单元测试覆盖列展示、按钮可见性、
默认值与链接跳转。
EOF
)"
```

---

## Task 7: 浏览器手工验收

按 `AGENTS.md` 要求，所有功能开发完成后必须走全面浏览器验证。

**Files:** 无代码改动；本任务只验证。

- [ ] **Step 1: 启动本地栈**

Run（按项目惯例选择，常见命令）:

```bash
make dev
# 或分两个终端：
#   make api-dev
#   make web-dev
```

确认 manager 前端可访问，后端 :8080（或项目实际端口）健康。

- [ ] **Step 2: 用组织管理员登录**

浏览器登录：组织标识 `test-org`，账号 `test-org`，密码 `test-org123`。

- [ ] **Step 3: 创建一个不带实例的成员**

进入「组织 → 成员」，点击「新增成员」按钮（不是「创建并初始化」）。填写用户名、显示名、密码（≥8 位），角色保留 `org_member`，提交。

预期：列表立刻多出新成员行，「实例」列显示**警告色「无实例」tag**，行右侧有「为该成员创建实例」按钮。

- [ ] **Step 4: 点击「为该成员创建实例」**

弹出表单。预期：
- `实例名` 输入框预填「{显示名} 的实例」。
- `模型` 下拉默认选中组织 enabled_models 的第一项。
- 提交后，列表刷新，该行「实例」列变成可点击的实例名链接。

- [ ] **Step 5: 点击实例名跳转 AppsPage**

预期：跳转到 `/apps/<id>/overview`，进入应用详情概览页。

- [ ] **Step 6: 软删该实例后回到成员列表**

从 AppsPage 删除实例，回到「组织 → 成员」。预期：该成员行恢复「无实例」tag + 补建按钮。

- [ ] **Step 7: 再次补建一次（幂等校验）**

重复 Step 4–5，确认补建可重复。

- [ ] **Step 8: 用平台管理员复核**

退出登录，用 `admin` / `admin123`（组织标识留空）进入平台管理员视角，在组织选择器里切到 `test-org`。

预期：同样能看到无实例成员的「为该成员创建实例」按钮；现有的 platform_admin 跨组织能力不退化。

- [ ] **Step 9: 写交付说明**

把以上验证结果（特别是失败路径如有）写到提交 PR 的描述里，或更新 `docs/superpowers/specs/2026-05-15-member-without-instance-rebuild-design.md` 末尾「验收记录」段（可选）。

> 验收发现任何问题：回到对应 Task 修复并重新跑该 Task 的测试，再回到 Task 7 重验，直到流程全部通过。

---

## 完成判定

- [ ] 后端 `go test ./internal/service/...` 全绿。
- [ ] 前端 `pnpm test -- MembersPage.spec` 全绿。
- [ ] `make openapi-check` 工作区干净。
- [ ] 浏览器走通 Task 7 全部 step。
- [ ] commit 数量与节奏符合 `AGENTS.md`（按 Task 1 / 3 / 4 / 6 分四次提交）。
