# 权限重构与说明页 实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 修正四处权限规则（成员列表禁组织成员、切换版本和启停允许平台管理员、组织成员可查看助手版本），并新增平台管理员专属权限说明页。

**Architecture:** 后端在 `authorizer.go` 集中新增/修改谓词，service 层更新两处调用点；前端在 `permissions.ts` 新增对齐 helper，更新两个页面组件的权限判断，最后新增一个静态权限矩阵说明页。

**Tech Stack:** Go (testify)、Vue 3 (Composition API)、Naive UI、lucide-vue-next

---

## 文件变更清单

| 文件 | 操作 | 说明 |
|---|---|---|
| `internal/auth/authorizer.go` | 修改 + 新增 | 新增 `CanListMembers`、`CanSwitchAppVersion`；修改 `CanTriggerRuntimeOperation`、`CanViewAssistantVersion` |
| `internal/auth/authorizer_test.go` | 修改 + 新增 | 新增两个测试函数；更新两个现有测试函数 |
| `internal/service/member_service.go` | 修改 | `ListMembers` 第 155 行：`CanViewOrg` → `CanListMembers` |
| `internal/service/member_service_test.go` | 新增测试 | 新增 `TestMemberServiceListForbidsMember` |
| `internal/service/app_service.go` | 修改 | `SwitchAppVersion` 第 231 行：`CanManageApp` → `CanSwitchAppVersion` |
| `internal/service/app_service_test.go` | 修改 + 新增 | 移除 forbidden 测试中的 platform_admin 用例；新增 `TestSwitchAppVersionSuccessByPlatformAdmin` |
| `web/src/domain/permissions.ts` | 新增 | 新增 `canSwitchAppVersion`、`canTriggerRuntimeOperation` |
| `web/src/pages/apps/AppOverviewTab.vue` | 修改 | `canViewVersions` 扩展至全登录用户；切换按钮改用 `canSwitchAppVersion` |
| `web/src/pages/apps/AppRuntimeTab.vue` | 修改 | `canManage` 改用 `canTriggerRuntimeOperation` |
| `web/src/pages/platform/PermissionsPage.vue` | 新增 | 静态权限矩阵说明页 |
| `web/src/app/router.ts` | 修改 | 新增 `/platform/permissions` 路由 |
| `web/src/layouts/DashboardLayout.vue` | 修改 | 平台管理员导航新增"权限说明"入口 |

---

## Task 1：authorizer.go — 权限谓词变更（TDD）

**Files:**
- Modify: `internal/auth/authorizer.go`
- Modify: `internal/auth/authorizer_test.go`

- [ ] **Step 1：写失败测试**

在 `internal/auth/authorizer_test.go` 末尾追加以下四段测试代码（`TestCanViewAssistantVersion` 为原地修改，其余三个为新增）：

```go
// TestCanListMembers 验证成员列表读权限：仅平台管理员和组织管理员可访问，普通成员不可。
func TestCanListMembers(t *testing.T) {
	cases := []orgCase{
		{"platform_admin 跨组织可查列表", domain.UserRolePlatformAdmin, orgA, orgB, true},   // 场景：平台管理员跨组织可查成员列表
		{"org_admin 本组织可查列表", domain.UserRoleOrgAdmin, orgA, orgA, true},             // 场景：组织管理员在本组织内可查成员列表
		{"org_admin 跨组织不可查", domain.UserRoleOrgAdmin, orgA, orgB, false},             // 场景：组织管理员不可跨组织查成员列表
		{"org_member 本组织不可查列表", domain.UserRoleOrgMember, orgA, orgA, false},        // 场景：普通成员不可查看本组织成员列表
		{"未知角色不可查", "unknown", orgA, orgA, false},                                    // 场景：未知角色不可查
	}
	runOrgCases(t, CanListMembers, cases)
}

// TestCanSwitchAppVersion 验证应用版本切换权限：平台管理员可跨组织切换，组织管理员限本组织，成员限 owner。
func TestCanSwitchAppVersion(t *testing.T) {
	cases := []memberCase{
		{"platform_admin 任意应用可切换版本", domain.UserRolePlatformAdmin, orgA, userA, orgB, userB, true}, // 场景：平台管理员跨组织可切换版本
		{"org_admin 本组织可切换版本", domain.UserRoleOrgAdmin, orgA, userA, orgA, userB, true},            // 场景：组织管理员可切换本组织应用版本
		{"org_admin 跨组织不可切换", domain.UserRoleOrgAdmin, orgA, userA, orgB, userB, false},            // 场景：组织管理员不可跨组织切换版本
		{"org_member 自己应用可切换", domain.UserRoleOrgMember, orgA, userA, orgA, userA, true},           // 场景：成员可切换自己拥有的应用版本
		{"org_member 他人应用不可切换", domain.UserRoleOrgMember, orgA, userA, orgA, userB, false},        // 场景：成员不可切换他人拥有的应用版本
		{"未知角色不可切换", "unknown", orgA, userA, orgA, userA, false},                                  // 场景：未知角色不可切换
	}
	runAppCases(t, CanSwitchAppVersion, cases)
}
```

将现有 `TestCanTriggerRuntimeOperation` 中的 platform_admin 用例从 `false` 改为 `true`，并更新描述：

```go
// TestCanTriggerRuntimeOperation 验证触发运行时操作的权限边界。
func TestCanTriggerRuntimeOperation(t *testing.T) {
	cases := []memberCase{
		{"platform_admin 可触发任意应用运行操作", domain.UserRolePlatformAdmin, orgA, userA, orgB, userB, true}, // 场景：平台管理员跨组织可触发运行时操作（启停/重启）
		{"org_admin 同组织可触发运行操作", domain.UserRoleOrgAdmin, orgA, userA, orgA, userB, true},             // 场景：org_admin 同组织可触发运行操作
		{"org_admin 跨组织不可触发", domain.UserRoleOrgAdmin, orgA, userA, orgB, userB, false},               // 场景：org_admin 跨组织不可触发
		{"org_member 仅可触发自己应用的运行操作", domain.UserRoleOrgMember, orgA, userA, orgA, userA, true},        // 场景：org_member 仅可触发自己应用的运行操作
		{"org_member 不可触发他人应用的运行操作", domain.UserRoleOrgMember, orgA, userA, orgA, userB, false},       // 场景：org_member 不可触发他人应用的运行操作
	}
	runAppCases(t, CanTriggerRuntimeOperation, cases)
}
```

将现有 `TestCanViewAssistantVersion` 中 org_member 断言从 `False` 改为 `True`，并更新注释：

```go
// TestCanViewAssistantVersion 验证三角色均可查看助手版本，未知角色不可。
func TestCanViewAssistantVersion(t *testing.T) {
	// 平台管理员维护版本目录，可查看。
	assert.True(t, CanViewAssistantVersion(Principal{Role: domain.UserRolePlatformAdmin}))
	// 组织管理员创建实例时需读取版本，可查看。
	assert.True(t, CanViewAssistantVersion(Principal{Role: domain.UserRoleOrgAdmin}))
	// 组织成员需在应用概览中查看绑定版本名称，可查看。
	assert.True(t, CanViewAssistantVersion(Principal{Role: domain.UserRoleOrgMember}))
	// 未知角色无权查看助手版本。
	assert.False(t, CanViewAssistantVersion(Principal{Role: "unknown"}))
}
```

- [ ] **Step 2：运行测试，确认失败**

```bash
cd /home/hujing/dir/software/ywjs/oc-manager
go test ./internal/auth/... -run "TestCanListMembers|TestCanSwitchAppVersion|TestCanTriggerRuntimeOperation|TestCanViewAssistantVersion" -v
```

期望：`TestCanListMembers` 和 `TestCanSwitchAppVersion` 报 "undefined: CanListMembers / CanSwitchAppVersion"；`TestCanTriggerRuntimeOperation` 的 platform_admin 用例 FAIL；`TestCanViewAssistantVersion` 的 org_member 用例 FAIL。

- [ ] **Step 3：实现谓词变更**

在 `internal/auth/authorizer.go` 中：

**（a）在"成员资源"块末尾（第 72 行后）新增 `CanListMembers`：**

```go
// CanListMembers 判断主体能否获取组织成员列表。
// 成员列表属于组织管理视角，普通成员无需访问他人信息，仅管理员可查。
func CanListMembers(p Principal, orgID string) bool {
	switch p.Role {
	case domain.UserRolePlatformAdmin:
		return true
	case domain.UserRoleOrgAdmin:
		return p.OrgID == orgID
	default:
		return false
	}
}
```

**（b）在"应用资源（业务别名）"块末尾（第 113 行后，`CanManageApp` 之后）新增 `CanSwitchAppVersion`：**

```go
// CanSwitchAppVersion 判断主体是否可切换应用绑定的助手版本。
// 版本切换是运维操作，平台管理员需介入版本统一管理；与渠道绑定、知识库写入等
// 纯组织侧操作不同，故单独建谓词而非扩展 CanManageApp。
func CanSwitchAppVersion(p Principal, appOrgID, appOwnerUserID string) bool {
	switch p.Role {
	case domain.UserRolePlatformAdmin:
		return true
	case domain.UserRoleOrgAdmin:
		return p.OrgID == appOrgID
	case domain.UserRoleOrgMember:
		return p.UserID == appOwnerUserID
	default:
		return false
	}
}
```

**（c）将 `CanTriggerRuntimeOperation`（第 153-158 行）改为独立实现：**

```go
// CanTriggerRuntimeOperation 判断主体是否可对应用触发运行时操作（启停/重启等）。
// 平台管理员需要介入实例运维（如强制重启故障实例），故此处与 CanManageApp 分离。
// 注：调用方仍需在此之前额外校验 user.status != disabled，disabled 账号不得触发运行时操作。
func CanTriggerRuntimeOperation(p Principal, appOrgID, appOwnerUserID string) bool {
	switch p.Role {
	case domain.UserRolePlatformAdmin:
		return true
	case domain.UserRoleOrgAdmin:
		return p.OrgID == appOrgID
	case domain.UserRoleOrgMember:
		return p.UserID == appOwnerUserID
	default:
		return false
	}
}
```

**（d）将 `CanViewAssistantVersion`（第 268-277 行）加入 org_member：**

```go
// CanViewAssistantVersion 判断主体能否查看助手版本。
// 平台管理员维护目录；组织管理员创建实例时需要读取版本；
// 组织成员需要在应用概览中查看自己实例绑定的版本名称，故同样开放。
func CanViewAssistantVersion(p Principal) bool {
	switch p.Role {
	case domain.UserRolePlatformAdmin, domain.UserRoleOrgAdmin, domain.UserRoleOrgMember:
		return true
	default:
		return false
	}
}
```

- [ ] **Step 4：运行测试，确认通过**

```bash
go test ./internal/auth/... -v
```

期望：所有测试 PASS，无 FAIL。

- [ ] **Step 5：提交**

```bash
git add internal/auth/authorizer.go internal/auth/authorizer_test.go
git commit -m "$(cat <<'EOF'
feat(auth): 新增 CanListMembers / CanSwitchAppVersion，扩展 CanTriggerRuntimeOperation / CanViewAssistantVersion

- 新增 CanListMembers：成员列表仅 platform_admin 和 org_admin 可访问，org_member 403
- 新增 CanSwitchAppVersion：版本切换允许 platform_admin，与应用配置写权限 CanManageApp 分离
- CanTriggerRuntimeOperation 不再委托 CanManageApp，独立加入 platform_admin 以支持运维介入
- CanViewAssistantVersion 加入 org_member，使其可在应用概览中查看绑定的版本名称

Co-Authored-By: Claude Sonnet 4.6 <noreply@anthropic.com>
EOF
)"
```

---

## Task 2：member_service.go — 调用点更新

**Files:**
- Modify: `internal/service/member_service.go:155`
- Modify: `internal/service/member_service_test.go`

- [ ] **Step 1：写失败测试**

在 `internal/service/member_service_test.go` 中找到 `TestMemberServiceListLimitsOrgScope` 附近，在其后新增：

```go
// TestMemberServiceListForbidsMember 验证 org_member 无权查看成员列表，应返回 ErrForbidden。
// 成员列表属于组织管理视角（CanListMembers），org_member 无需访问他人信息。
func TestMemberServiceListForbidsMember(t *testing.T) {
	store := newMemberStoreStub(t)
	svc := NewMemberService(store, fakeHash)

	// org_member 尝试获取本组织成员列表，应被拒绝。
	_, err := svc.ListMembers(context.Background(),
		auth.Principal{Role: domain.UserRoleOrgMember, OrgID: testOrgID, UserID: testMemUID},
		testOrgID, 0, 0,
	)
	require.ErrorIs(t, err, ErrForbidden, "org_member 调用 ListMembers 应返回 ErrForbidden")
}
```

- [ ] **Step 2：运行测试，确认失败**

```bash
go test ./internal/service/... -run "TestMemberServiceListForbidsMember" -v
```

期望：FAIL —— 当前实现使用 `CanViewOrg`，org_member 同组织可通过，不会返回 `ErrForbidden`。

- [ ] **Step 3：更新调用点**

将 `internal/service/member_service.go` 第 155 行：

```go
if !auth.CanViewOrg(principal, orgID) {
```

改为：

```go
if !auth.CanListMembers(principal, orgID) {
```

- [ ] **Step 4：运行测试，确认通过**

```bash
go test ./internal/service/... -run "TestMemberService" -v
```

期望：全部 PASS，包括新增的 `TestMemberServiceListForbidsMember` 以及原有的 `TestMemberServiceListLimitsOrgScope`。

- [ ] **Step 5：提交**

```bash
git add internal/service/member_service.go internal/service/member_service_test.go
git commit -m "$(cat <<'EOF'
feat(member): 成员列表禁止 org_member 访问

ListMembers 的权限谓词从 CanViewOrg 改为 CanListMembers，
org_member 调用时返回 ErrForbidden（HTTP 403）。

Co-Authored-By: Claude Sonnet 4.6 <noreply@anthropic.com>
EOF
)"
```

---

## Task 3：app_service.go — 调用点更新

**Files:**
- Modify: `internal/service/app_service.go:231`
- Modify: `internal/service/app_service_test.go`

- [ ] **Step 1：更新 forbidden 测试 + 新增 success 测试**

在 `internal/service/app_service_test.go` 中：

**（a）在 `TestSwitchAppVersionForbidden` 的 `tests` slice 中移除 platform_admin 用例**（该用例注释为"平台管理员无应用写权限"），移除后 slice 仅保留两条用例：

```go
// TestSwitchAppVersionForbidden 验证无权管理该实例的调用者被拒绝（返回 ErrForbidden）。
// 测试用例：组织成员尝试管理不属于自己的实例，或错误组织的管理员。
func TestSwitchAppVersionForbidden(t *testing.T) {
	t.Parallel()

	tests := []struct {
		// name 是子测试场景说明。
		name string
		// principal 是没有该实例管理权限的调用者。
		principal auth.Principal
	}{
		{
			// 属于其他组织的组织管理员无权管理本组织的实例。
			name: "其他组织的管理员无权管理",
			principal: auth.Principal{
				Role:   domain.UserRoleOrgAdmin,
				OrgID:  "00000000-0000-0000-0000-000000009999", // 与实例所属组织不同
				UserID: testAdminUID,
			},
		},
		{
			// 组织成员只能管理自己的实例；testMemUID2 不是实例 owner（owner 为 testMemUID）。
			name: "非 owner 组织成员无权管理",
			principal: auth.Principal{
				Role:   domain.UserRoleOrgMember,
				OrgID:  testOrgID,
				UserID: "00000000-0000-0000-0000-000000009998", // 不是实例 owner
			},
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			svc, store := newAppServiceWithStore(t)
			store.mustSeedApp(t)
			store.organization = mustOrgWithAllowlist(t, testSwitchVersionID)

			// 无权调用者尝试切换版本，期望返回 ErrForbidden。
			_, err := svc.SwitchAppVersion(context.Background(), tc.principal, testAppServiceAppID, testSwitchVersionID)
			require.ErrorIs(t, err, ErrForbidden, "无权调用者应返回 ErrForbidden")
		})
	}
}
```

**（b）在 `TestSwitchAppVersionSuccessByOwnerMember` 之后新增：**

```go
// TestSwitchAppVersionSuccessByPlatformAdmin 验证平台管理员可跨组织切换任意实例的助手版本。
// 平台管理员无 OrgID，CanSwitchAppVersion 应仍返回 true，使管理员可统一管理版本。
func TestSwitchAppVersionSuccessByPlatformAdmin(t *testing.T) {
	t.Parallel()
	svc, store := newAppServiceWithStore(t)

	// 预置实例：applied_version_revision=0，模拟切换前状态。
	app := store.mustSeedApp(t)
	app.AppliedVersionRevision = 0
	store.app = app
	// 组织 allowlist 内含目标版本 testSwitchVersionID。
	store.organization = mustOrgWithAllowlist(t, testSwitchVersionID)
	store.versionRevision = 1

	// platformAdmin() 无 OrgID，应通过 CanSwitchAppVersion 的 platform_admin 分支。
	result, err := svc.SwitchAppVersion(context.Background(), platformAdmin(), testAppServiceAppID, testSwitchVersionID)

	// 平台管理员切换成功：无错误，返回的实例 VersionID 为目标版本。
	require.NoError(t, err)
	assert.Equal(t, testSwitchVersionID, result.VersionID, "返回的实例 VersionID 应等于目标版本")
	// applied_version_revision=0 而 versionRevision=1，version_synced 应为 false，提示需重启。
	assert.False(t, result.VersionSynced, "切换后 applied_* 未更新，version_synced 应为 false")
	require.Len(t, store.setVersionCalls, 1, "SetAppVersion 应被调用一次")
}
```

- [ ] **Step 2：运行测试，确认 `TestSwitchAppVersionSuccessByPlatformAdmin` 失败**

```bash
go test ./internal/service/... -run "TestSwitchAppVersion" -v
```

期望：`TestSwitchAppVersionSuccessByPlatformAdmin` FAIL（platform_admin 被 `CanManageApp` 拒绝，返回 `ErrForbidden`）；其余 SwitchAppVersion 测试 PASS。

- [ ] **Step 3：更新调用点**

将 `internal/service/app_service.go` 第 231 行：

```go
if !auth.CanManageApp(principal, uuidToString(row.App.OrgID), uuidToString(row.App.OwnerUserID)) {
```

改为：

```go
if !auth.CanSwitchAppVersion(principal, uuidToString(row.App.OrgID), uuidToString(row.App.OwnerUserID)) {
```

- [ ] **Step 4：运行测试，确认全部通过**

```bash
go test ./internal/service/... -run "TestSwitchAppVersion" -v
```

期望：所有 `TestSwitchAppVersion*` 均 PASS。

- [ ] **Step 5：提交**

```bash
git add internal/service/app_service.go internal/service/app_service_test.go
git commit -m "$(cat <<'EOF'
feat(app): 切换助手版本允许平台管理员操作

SwitchAppVersion 权限谓词从 CanManageApp 改为 CanSwitchAppVersion，
平台管理员可跨组织统一管理实例版本。

Co-Authored-By: Claude Sonnet 4.6 <noreply@anthropic.com>
EOF
)"
```

---

## Task 4：permissions.ts — 新增前端权限 helper

**Files:**
- Modify: `web/src/domain/permissions.ts`

- [ ] **Step 1：追加两个函数**

在 `web/src/domain/permissions.ts` 末尾追加：

```ts
// canSwitchAppVersion：版本切换是运维操作，平台管理员可统一管理；与 canManageApp 分离。
// 与后端 CanSwitchAppVersion 保持一致。
export function canSwitchAppVersion(
  user: PermissionUser | null | undefined,
  app: PermissionApp | null | undefined,
): boolean {
  if (!user || !app) return false
  if (user.role === 'platform_admin') return true
  if (user.role === 'org_admin') return user.org_id === app.org_id
  if (user.role === 'org_member') return user.id === app.owner_user_id
  return false
}

// canTriggerRuntimeOperation：运行时启停/重启，平台管理员需要运维介入能力。
// 与后端 CanTriggerRuntimeOperation 保持一致。
export function canTriggerRuntimeOperation(
  user: PermissionUser | null | undefined,
  app: PermissionApp | null | undefined,
): boolean {
  if (!user || !app) return false
  if (user.role === 'platform_admin') return true
  if (user.role === 'org_admin') return user.org_id === app.org_id
  if (user.role === 'org_member') return user.id === app.owner_user_id
  return false
}
```

- [ ] **Step 2：提交**

```bash
git add web/src/domain/permissions.ts
git commit -m "$(cat <<'EOF'
feat(web/permissions): 新增 canSwitchAppVersion 和 canTriggerRuntimeOperation

与后端 CanSwitchAppVersion / CanTriggerRuntimeOperation 对齐：
平台管理员获得版本切换和运行时启停能力。

Co-Authored-By: Claude Sonnet 4.6 <noreply@anthropic.com>
EOF
)"
```

---

## Task 5：AppOverviewTab.vue — canViewVersions + 切换按钮

**Files:**
- Modify: `web/src/pages/apps/AppOverviewTab.vue`

- [ ] **Step 1：更新 import**

将 `AppOverviewTab.vue` 脚本区的 import 行：

```ts
import { canManageApp } from '@/domain/permissions'
```

改为：

```ts
import { canSwitchAppVersion } from '@/domain/permissions'
```

- [ ] **Step 2：更新 canViewVersions**

将第 180 行：

```ts
const canViewVersions = computed(() => auth.isPlatformAdmin || auth.user?.role === 'org_admin')
```

改为：

```ts
// canViewVersions：三角色均可查看助手版本目录（CanViewAssistantVersion 已扩展至 org_member）。
const canViewVersions = computed(() => !!auth.user)
```

- [ ] **Step 3：更新切换按钮 v-if**

将模板第 79 行：

```html
v-if="canManageApp(auth.user, app) && canViewVersions"
```

改为：

```html
v-if="canSwitchAppVersion(auth.user, app) && canViewVersions"
```

- [ ] **Step 4：提交**

```bash
git add web/src/pages/apps/AppOverviewTab.vue
git commit -m "$(cat <<'EOF'
feat(web/overview): 版本切换按钮对平台管理员可见，组织成员可查看版本名称

- canViewVersions 扩展至全登录用户（对齐后端 CanViewAssistantVersion）
- 切换按钮 v-if 改用 canSwitchAppVersion，平台管理员可见

Co-Authored-By: Claude Sonnet 4.6 <noreply@anthropic.com>
EOF
)"
```

---

## Task 6：AppRuntimeTab.vue — canManage 改用 canTriggerRuntimeOperation

**Files:**
- Modify: `web/src/pages/apps/AppRuntimeTab.vue`

- [ ] **Step 1：更新 import**

将 `AppRuntimeTab.vue` 脚本区：

```ts
import { canManageApp } from '@/domain/permissions'
```

改为：

```ts
import { canTriggerRuntimeOperation } from '@/domain/permissions'
```

- [ ] **Step 2：更新 canManage computed**

将第 178 行：

```ts
const canManage = computed(() => canManageApp(auth.user, app?.value))
```

改为：

```ts
// canManage：运行时启停/重启需平台管理员运维介入能力，使用 canTriggerRuntimeOperation。
const canManage = computed(() => canTriggerRuntimeOperation(auth.user, app?.value))
```

- [ ] **Step 3：提交**

```bash
git add web/src/pages/apps/AppRuntimeTab.vue
git commit -m "$(cat <<'EOF'
feat(web/runtime): 启停/重启按钮对平台管理员可见

canManage 改用 canTriggerRuntimeOperation，
平台管理员可在运行时 Tab 触发应用启动/停止/重启。

Co-Authored-By: Claude Sonnet 4.6 <noreply@anthropic.com>
EOF
)"
```

---

## Task 7：权限说明页（PermissionsPage.vue + router + nav）

**Files:**
- Create: `web/src/pages/platform/PermissionsPage.vue`
- Modify: `web/src/app/router.ts`
- Modify: `web/src/layouts/DashboardLayout.vue`

- [ ] **Step 1：创建 PermissionsPage.vue**

新建 `web/src/pages/platform/PermissionsPage.vue`：

```vue
<script setup lang="ts">
// PermissionsPage 展示平台权限矩阵，仅平台管理员可访问。
// 内容为静态数据，不调用任何 API；与 docs/superpowers/specs/2026-05-22-permission-refactor-design.md 保持一致。

interface PermRow {
  // op 是操作名称。
  op: string
  // admin / orgAdmin / member 分别是三角色的权限描述。
  admin: string
  orgAdmin: string
  member: string
}

interface PermSection {
  title: string
  rows: PermRow[]
}

// sections 包含全量权限矩阵，按功能模块分组。
const sections: PermSection[] = [
  {
    title: '组织管理',
    rows: [
      { op: '创建组织', admin: '✅', orgAdmin: '❌', member: '❌' },
      { op: '组织列表', admin: '✅', orgAdmin: '❌', member: '❌' },
      { op: '查看组织详情', admin: '✅', orgAdmin: '🟡 本组织', member: '🟡 本组织' },
      { op: '修改组织信息', admin: '✅', orgAdmin: '❌', member: '❌' },
      { op: '启用 / 禁用组织', admin: '✅', orgAdmin: '❌', member: '❌' },
    ],
  },
  {
    title: '成员管理',
    rows: [
      { op: '成员列表', admin: '✅', orgAdmin: '🟡 本组织', member: '❌' },
      { op: '查看成员详情', admin: '✅', orgAdmin: '🟡 本组织', member: '🟡 仅自己' },
      { op: '创建成员', admin: '❌', orgAdmin: '🟡 本组织', member: '❌' },
      { op: '修改成员资料', admin: '🟡 仅自己', orgAdmin: '🟡 本组织', member: '🟡 仅自己' },
      { op: '启用 / 禁用成员', admin: '❌', orgAdmin: '🟡 本组织', member: '❌' },
      { op: '删除成员', admin: '❌', orgAdmin: '🟡 本组织', member: '❌' },
      { op: '重置成员密码', admin: '❌', orgAdmin: '🟡 本组织', member: '❌' },
      { op: 'Onboard（初始建实例）', admin: '❌', orgAdmin: '🟡 本组织', member: '❌' },
      { op: '为成员复建实例', admin: '✅', orgAdmin: '🟡 本组织', member: '❌' },
    ],
  },
  {
    title: '应用实例',
    rows: [
      { op: '应用列表', admin: '✅', orgAdmin: '🟡 本组织全部', member: '🟡 仅自己' },
      { op: '查看应用详情', admin: '✅', orgAdmin: '🟡 本组织', member: '🟡 仅自己' },
      { op: '切换助手版本', admin: '✅', orgAdmin: '🟡 本组织', member: '🟡 仅自己' },
    ],
  },
  {
    title: '运行时操作',
    rows: [
      { op: '启动 / 停止 / 重启', admin: '✅', orgAdmin: '🟡 本组织', member: '🟡 仅自己' },
    ],
  },
  {
    title: '渠道（Channel）',
    rows: [
      { op: '查看渠道信息', admin: '✅', orgAdmin: '🟡 本组织', member: '🟡 仅自己' },
      { op: '绑定渠道', admin: '❌', orgAdmin: '🟡 本组织', member: '🟡 仅自己' },
    ],
  },
  {
    title: '知识库',
    rows: [
      { op: '读取组织知识库', admin: '✅', orgAdmin: '🟡 本组织', member: '🟡 本组织' },
      { op: '写入组织知识库', admin: '❌', orgAdmin: '🟡 本组织', member: '❌' },
      { op: '查看组织知识库同步状态', admin: '❌', orgAdmin: '🟡 本组织', member: '❌' },
      { op: '触发组织知识库同步重试', admin: '❌', orgAdmin: '🟡 本组织', member: '❌' },
      { op: '读取应用知识库', admin: '✅', orgAdmin: '🟡 本组织', member: '🟡 仅自己' },
      { op: '写入应用知识库', admin: '❌', orgAdmin: '🟡 本组织', member: '🟡 仅自己' },
    ],
  },
  {
    title: '助手版本',
    rows: [
      { op: '查看助手版本列表 / 详情', admin: '✅', orgAdmin: '✅', member: '✅' },
      { op: '创建 / 修改 / 删除助手版本', admin: '✅', orgAdmin: '❌', member: '❌' },
      { op: '上传技能包', admin: '✅', orgAdmin: '❌', member: '❌' },
    ],
  },
  {
    title: '任务看板（Kanban）',
    rows: [
      { op: '查看任务看板', admin: '✅', orgAdmin: '🟡 本组织', member: '🟡 仅自己' },
      { op: '写操作（评论 / 完成 / 阻塞）', admin: '✅', orgAdmin: '🟡 本组织', member: '🟡 仅自己' },
    ],
  },
  {
    title: 'Cron 任务',
    rows: [
      { op: '查看 Cron 列表 / 详情', admin: '✅', orgAdmin: '🟡 本组织', member: '🟡 仅自己' },
      { op: '创建 / 修改 / 启停 / 删除 Cron', admin: '✅', orgAdmin: '🟡 本组织', member: '🟡 仅自己' },
    ],
  },
  {
    title: '用量',
    rows: [
      { op: '查看组织聚合用量', admin: '✅', orgAdmin: '🟡 本组织', member: '❌' },
      { op: '查看成员用量', admin: '✅', orgAdmin: '🟡 本组织', member: '🟡 仅自己' },
      { op: '查看应用用量', admin: '✅', orgAdmin: '🟡 本组织', member: '🟡 仅自己' },
    ],
  },
  {
    title: '审计日志',
    rows: [
      { op: '查看组织审计', admin: '✅', orgAdmin: '🟡 本组织', member: '❌' },
      { op: '查看应用审计', admin: '✅', orgAdmin: '🟡 本组织', member: '🟡 仅自己' },
      { op: '查看"我的审计"', admin: '✅', orgAdmin: '✅', member: '✅' },
    ],
  },
  {
    title: '充值记录',
    rows: [
      { op: '查看充值记录', admin: '✅', orgAdmin: '🟡 本组织', member: '❌' },
      { op: '查看余额', admin: '✅', orgAdmin: '🟡 本组织', member: '❌' },
    ],
  },
  {
    title: '运行时节点',
    rows: [
      { op: '节点列表 / 详情', admin: '✅', orgAdmin: '❌', member: '❌' },
      { op: '启用 / 禁用节点', admin: '✅', orgAdmin: '❌', member: '❌' },
    ],
  },
  {
    title: '平台总览',
    rows: [
      { op: '平台总览统计', admin: '✅', orgAdmin: '❌', member: '❌' },
    ],
  },
  {
    title: '模型列表',
    rows: [
      { op: '查看可用模型列表', admin: '✅', orgAdmin: '❌', member: '❌' },
    ],
  },
  {
    title: '后台任务（Jobs）',
    rows: [
      { op: '查看后台任务列表', admin: '✅', orgAdmin: '❌', member: '❌' },
    ],
  },
  {
    title: '工作区',
    rows: [
      { op: '查看 / 下载 / 打包工作区文件', admin: '✅', orgAdmin: '🟡 本组织', member: '🟡 仅自己' },
    ],
  },
  {
    title: '资源指标',
    rows: [
      { op: '查看应用资源指标', admin: '✅', orgAdmin: '🟡 本组织', member: '🟡 仅自己' },
    ],
  },
]
</script>

<template>
  <div style="padding: 24px; max-width: 900px;">
    <n-h2 style="margin-bottom: 4px;">权限说明</n-h2>
    <n-p depth="3" style="margin-bottom: 24px;">各角色可见 / 可操作范围一览</n-p>

    <!-- 图例 -->
    <n-space style="margin-bottom: 24px;">
      <n-tag type="success" :bordered="false">✅ 可操作（无条件）</n-tag>
      <n-tag type="error" :bordered="false">❌ 无权限</n-tag>
      <n-tag type="warning" :bordered="false">🟡 有条件（本组织 / 仅自己）</n-tag>
    </n-space>

    <!-- 每个功能模块一个表格 -->
    <div
      v-for="section in sections"
      :key="section.title"
      style="margin-bottom: 32px;"
    >
      <n-h4 style="margin-bottom: 8px;">{{ section.title }}</n-h4>
      <n-table size="small" :bordered="true" :single-line="false">
        <thead>
          <tr>
            <th style="width: 40%;">操作</th>
            <th style="width: 20%; text-align: center;">平台管理员</th>
            <th style="width: 20%; text-align: center;">组织管理员</th>
            <th style="width: 20%; text-align: center;">组织成员</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="row in section.rows" :key="row.op">
            <td>{{ row.op }}</td>
            <td style="text-align: center;">{{ row.admin }}</td>
            <td style="text-align: center;">{{ row.orgAdmin }}</td>
            <td style="text-align: center;">{{ row.member }}</td>
          </tr>
        </tbody>
      </n-table>
    </div>
  </div>
</template>
```

- [ ] **Step 2：在 router.ts 新增路由**

在 `web/src/app/router.ts` 中：

在 import 块末尾追加：

```ts
import PermissionsPage from '@/pages/platform/PermissionsPage.vue'
```

在 routes 的 `{ path: 'platform/organizations/:orgId/recharge', ... }` 后追加：

```ts
{ path: 'platform/permissions', component: PermissionsPage, meta: { allowedRoles: PLATFORM_ONLY } },
```

- [ ] **Step 3：在 DashboardLayout.vue 新增导航项**

在 `web/src/layouts/DashboardLayout.vue` 中：

将 lucide import 行：

```ts
  BarChart3, BookOpen, Bot, Boxes, Building2, FileSearch, Gauge,
  LayoutDashboard, LogOut, RefreshCw, Server, Users, Wallet,
```

改为（追加 `ShieldCheck`）：

```ts
  BarChart3, BookOpen, Bot, Boxes, Building2, FileSearch, Gauge,
  LayoutDashboard, LogOut, RefreshCw, Server, ShieldCheck, Users, Wallet,
```

在 `menuOptions` 的平台管理员专区，在 `runtime-nodes` 入口之后追加：

```ts
    items.push({ key: '/platform/permissions', label: '权限说明', icon: () => h(ShieldCheck, { size: 18 }) })
```

完整 `isPlatformAdmin` 分支变为：

```ts
  if (isPlatformAdmin.value) {
    items.push({ key: '/platform/dashboard', label: '平台', icon: () => h(Gauge, { size: 18 }) })
    items.push({ key: '/organizations', label: '组织', icon: () => h(Building2, { size: 18 }) })
    items.push({ key: '/assistant-versions', label: '助手版本', icon: () => h(Boxes, { size: 18 }) })
  }
  // ... 中间内容不变 ...
  if (isPlatformAdmin.value) {
    items.push({ key: '/runtime-nodes', label: '运行节点', icon: () => h(Server, { size: 18 }) })
    items.push({ key: '/platform/permissions', label: '权限说明', icon: () => h(ShieldCheck, { size: 18 }) })
  }
```

- [ ] **Step 4：提交**

```bash
git add web/src/pages/platform/PermissionsPage.vue web/src/app/router.ts web/src/layouts/DashboardLayout.vue
git commit -m "$(cat <<'EOF'
feat(web): 新增平台管理员专属权限说明页

在 /platform/permissions 新增静态权限矩阵页面，展示全量 17 个功能模块的
三角色权限范围。路由限 PLATFORM_ONLY，侧边栏导航新增"权限说明"入口。

Co-Authored-By: Claude Sonnet 4.6 <noreply@anthropic.com>
EOF
)"
```

---

## Task 8：浏览器功能验证

- [ ] **Step 1：启动开发服务**

```bash
# 确认后端已运行（docker-compose 或本地服务），然后启动前端开发服务
cd /home/hujing/dir/software/ywjs/oc-manager/web
npm run dev
```

- [ ] **Step 2：验证成员列表禁组织成员**

以 org_member 身份登录（组织标识 + 成员账号），打开浏览器 Network 面板，确认 `/api/v1/organizations/{orgId}/members` 返回 403；侧边栏"成员"入口不可见（路由守卫已排除）。

- [ ] **Step 3：验证切换版本对平台管理员可见**

以 platform_admin（`admin` / `admin123`）身份登录，进入任意应用概览 Tab，确认"切换"按钮可见；点击切换，选择版本后提交，确认请求成功，版本名称更新。

- [ ] **Step 4：验证启停/重启对平台管理员可见**

以 platform_admin 身份进入任意应用的运行时 Tab，确认启动/停止/重启按钮可见，操作后应用状态变化正常。

- [ ] **Step 5：验证组织成员可见版本名称**

以 org_member 身份登录，进入自己的应用概览 Tab，确认版本字段显示版本名称（而非版本 ID 或"—"）；"切换"按钮仍可见（org_member 可切换自己的应用）。

- [ ] **Step 6：验证权限说明页**

以 platform_admin 身份进入侧边栏，确认"权限说明"入口可见，点击进入 `/platform/permissions`，页面正常显示 17 个功能模块的权限矩阵。

以 org_admin 或 org_member 身份访问 `/platform/permissions`，确认被路由守卫重定向至首页。
