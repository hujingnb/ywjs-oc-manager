# 权限校验集中化 实施计划

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把散落在 `internal/service` 多个文件的 8 个权限谓词收编到 `internal/auth/authorizer.go`，service 层不再定义本地 `canX` 函数。

**Architecture:** 新建 `internal/auth/authorizer.go` 提供以 string ID 为参数的 `Can*` 函数；service 层调用点逐文件替换为 `auth.CanX(...)`，旧本地谓词定义在所有调用点迁移完成后统一删除，保证每个中间步骤可编译可测试。

**Tech Stack:** Go 1.25 / pgx / sqlc / stdlib testing / golang-migrate（迁移本身不影响 DB schema）

**Spec reference:** `docs/superpowers/specs/2026-05-09-auth-authorizer-consolidation-design.md`

**关键约束：**

- TDD 仅适用于 Task 1（新建 authorizer 包）。Task 2-7 是重构，依赖现有 service 测试作为回归网。每个 task 末尾必须 `go test ./...` 全绿。
- 所有调用点替换期间，旧本地谓词暂时保留为「无人调用的死函数」；Task 6 才统一删除。这是 spec 第 5 节明确的约束。
- 不引入任何新依赖（不动 `go.mod`）；不引入 testify（spec 第 6.3 节说明）。

---

## Chunk 1: 新建 authorizer 包

### Task 1: 创建 `internal/auth/authorizer.go` 与 table-driven 测试

**Files:**
- Create: `internal/auth/authorizer.go`
- Create: `internal/auth/authorizer_test.go`

**前置阅读：**
- `internal/auth/token.go:20-26` — `Principal` struct 字段（UserID / OrgID / Role）
- `internal/domain/enums.go` — `UserRolePlatformAdmin / UserRoleOrgAdmin / UserRoleOrgMember` 常量
- `internal/service/member_service.go:365-418` — 现有 5 个谓词原文
- `internal/service/app_service.go:107-118` — 现有 `canViewApp` 原文
- `internal/service/persona_service.go:121-141` — 现有 persona 两个谓词原文

- [ ] **Step 1.1: 先写 authorizer_test.go（TDD 先行）**

创建 `internal/auth/authorizer_test.go`，table-driven 覆盖所有 9 个谓词的角色 × 资源归属矩阵：

```go
package auth

import (
	"testing"

	"oc-manager/internal/domain"
)

const (
	orgA  = "org-A"
	orgB  = "org-B"
	userA = "user-A"
	userB = "user-B"
)

type orgCase struct {
	name      string
	role      string
	pOrgID    string
	targetOrg string
	want      bool
}

func runOrgCases(t *testing.T, fn func(Principal, string) bool, cases []orgCase) {
	t.Helper()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := Principal{UserID: userA, OrgID: c.pOrgID, Role: c.role}
			if got := fn(p, c.targetOrg); got != c.want {
				t.Fatalf("got %v, want %v", got, c.want)
			}
		})
	}
}

func TestCanManageOrg(t *testing.T) {
	cases := []orgCase{
		{"platform_admin 跨组织可管", domain.UserRolePlatformAdmin, orgA, orgB, true},
		{"org_admin 同组织可管", domain.UserRoleOrgAdmin, orgA, orgA, true},
		{"org_admin 跨组织不可管", domain.UserRoleOrgAdmin, orgA, orgB, false},
		{"org_member 同组织也不可管", domain.UserRoleOrgMember, orgA, orgA, false},
		{"未知角色不可管", "unknown", orgA, orgA, false},
	}
	runOrgCases(t, CanManageOrg, cases)
}

func TestCanViewOrg(t *testing.T) {
	cases := []orgCase{
		{"platform_admin 跨组织可读", domain.UserRolePlatformAdmin, orgA, orgB, true},
		{"org_admin 同组织可读", domain.UserRoleOrgAdmin, orgA, orgA, true},
		{"org_admin 跨组织不可读", domain.UserRoleOrgAdmin, orgA, orgB, false},
		{"org_member 同组织可读", domain.UserRoleOrgMember, orgA, orgA, true},
		{"org_member 跨组织不可读", domain.UserRoleOrgMember, orgA, orgB, false},
	}
	runOrgCases(t, CanViewOrg, cases)
}

type memberCase struct {
	name        string
	role        string
	pOrgID      string
	pUserID     string
	targetOrg   string
	targetUser  string
	want        bool
}

func TestCanViewMember(t *testing.T) {
	cases := []memberCase{
		{"platform_admin 任意成员可看", domain.UserRolePlatformAdmin, orgA, userA, orgB, userB, true},
		{"org_admin 同组织可看", domain.UserRoleOrgAdmin, orgA, userA, orgA, userB, true},
		{"org_admin 跨组织不可看", domain.UserRoleOrgAdmin, orgA, userA, orgB, userB, false},
		{"org_member 仅看自己", domain.UserRoleOrgMember, orgA, userA, orgA, userA, true},
		{"org_member 不可看他人", domain.UserRoleOrgMember, orgA, userA, orgA, userB, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := Principal{UserID: c.pUserID, OrgID: c.pOrgID, Role: c.role}
			if got := CanViewMember(p, c.targetOrg, c.targetUser); got != c.want {
				t.Fatalf("got %v, want %v", got, c.want)
			}
		})
	}
}

func TestCanManageMember(t *testing.T) {
	cases := []orgCase{
		{"platform_admin 跨组织可管成员", domain.UserRolePlatformAdmin, orgA, orgB, true},
		{"org_admin 同组织可管成员", domain.UserRoleOrgAdmin, orgA, orgA, true},
		{"org_admin 跨组织不可管", domain.UserRoleOrgAdmin, orgA, orgB, false},
		{"org_member 一律不可管", domain.UserRoleOrgMember, orgA, orgA, false},
	}
	runOrgCases(t, CanManageMember, cases)
}

func TestCanEditMember(t *testing.T) {
	cases := []memberCase{
		{"platform_admin 任意可编辑", domain.UserRolePlatformAdmin, orgA, userA, orgB, userB, true},
		{"org_admin 同组织可编辑", domain.UserRoleOrgAdmin, orgA, userA, orgA, userB, true},
		{"org_admin 跨组织不可编辑", domain.UserRoleOrgAdmin, orgA, userA, orgB, userB, false},
		{"org_member 仅可编辑自己", domain.UserRoleOrgMember, orgA, userA, orgA, userA, true},
		{"org_member 不可编辑他人", domain.UserRoleOrgMember, orgA, userA, orgA, userB, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := Principal{UserID: c.pUserID, OrgID: c.pOrgID, Role: c.role}
			if got := CanEditMember(p, c.targetOrg, c.targetUser); got != c.want {
				t.Fatalf("got %v, want %v", got, c.want)
			}
		})
	}
}

func TestCanViewApp(t *testing.T) {
	cases := []memberCase{
		{"platform_admin 任意应用可看", domain.UserRolePlatformAdmin, orgA, userA, orgB, userB, true},
		{"org_admin 同组织应用可看", domain.UserRoleOrgAdmin, orgA, userA, orgA, userB, true},
		{"org_admin 跨组织不可看", domain.UserRoleOrgAdmin, orgA, userA, orgB, userB, false},
		{"org_member 仅看自己拥有的", domain.UserRoleOrgMember, orgA, userA, orgA, userA, true},
		{"org_member 不可看同组织他人", domain.UserRoleOrgMember, orgA, userA, orgA, userB, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := Principal{UserID: c.pUserID, OrgID: c.pOrgID, Role: c.role}
			if got := CanViewApp(p, c.targetOrg, c.targetUser); got != c.want {
				t.Fatalf("got %v, want %v", got, c.want)
			}
		})
	}
}

func TestCanViewOrgPersona_等价于CanViewOrg(t *testing.T) {
	roles := []string{domain.UserRolePlatformAdmin, domain.UserRoleOrgAdmin, domain.UserRoleOrgMember}
	pairs := [][2]string{{orgA, orgA}, {orgA, orgB}}
	for _, role := range roles {
		for _, pair := range pairs {
			p := Principal{UserID: userA, OrgID: pair[0], Role: role}
			if CanViewOrgPersona(p, pair[1]) != CanViewOrg(p, pair[1]) {
				t.Fatalf("CanViewOrgPersona 与 CanViewOrg 行为不一致: role=%s pOrg=%s targetOrg=%s",
					role, pair[0], pair[1])
			}
		}
	}
}

func TestCanManageOrgPersona_等价于CanManageOrg(t *testing.T) {
	roles := []string{domain.UserRolePlatformAdmin, domain.UserRoleOrgAdmin, domain.UserRoleOrgMember}
	pairs := [][2]string{{orgA, orgA}, {orgA, orgB}}
	for _, role := range roles {
		for _, pair := range pairs {
			p := Principal{UserID: userA, OrgID: pair[0], Role: role}
			if CanManageOrgPersona(p, pair[1]) != CanManageOrg(p, pair[1]) {
				t.Fatalf("CanManageOrgPersona 与 CanManageOrg 行为不一致: role=%s pOrg=%s targetOrg=%s",
					role, pair[0], pair[1])
			}
		}
	}
}
```

- [ ] **Step 1.2: 跑测试，确认编译失败（authorizer.go 还没建）**

```bash
go test ./internal/auth/... -run 'TestCan' -v
```

预期：编译错误，类似 `undefined: CanManageOrg`。

- [ ] **Step 1.3: 创建 internal/auth/authorizer.go**

```go
// Package auth 已含 Principal / TokenManager 等身份相关原语。
// authorizer.go 把所有「角色 + 资源归属」权限谓词集中在此，service 层不再定义本地 canX 函数。
package auth

import "oc-manager/internal/domain"

// 组织资源 ----------------------------------------------------------

// CanManageOrg 判断主体能否对指定组织执行写操作（成员管理、状态调整等）。
func CanManageOrg(p Principal, orgID string) bool {
	switch p.Role {
	case domain.UserRolePlatformAdmin:
		return true
	case domain.UserRoleOrgAdmin:
		return p.OrgID == orgID
	default:
		return false
	}
}

// CanViewOrg 判断主体能否查看指定组织内的资源（读路径）。
func CanViewOrg(p Principal, orgID string) bool {
	if p.Role == domain.UserRolePlatformAdmin {
		return true
	}
	return p.OrgID == orgID
}

// 成员资源 ----------------------------------------------------------

// CanViewMember 判断主体能否查看目标成员明细。
// 普通成员只能查看自己；组织管理员可查本组织成员；平台管理员不限。
func CanViewMember(p Principal, memberOrgID, memberUserID string) bool {
	switch p.Role {
	case domain.UserRolePlatformAdmin:
		return true
	case domain.UserRoleOrgAdmin:
		return p.OrgID == memberOrgID
	case domain.UserRoleOrgMember:
		return p.UserID == memberUserID
	default:
		return false
	}
}

// CanManageMember 判断主体能否对目标成员执行写操作（角色调整、状态切换、密码重置）。
func CanManageMember(p Principal, memberOrgID string) bool {
	switch p.Role {
	case domain.UserRolePlatformAdmin:
		return true
	case domain.UserRoleOrgAdmin:
		return p.OrgID == memberOrgID
	default:
		return false
	}
}

// CanEditMember 判断主体能否更新目标成员资料（含本人编辑自身）。
func CanEditMember(p Principal, memberOrgID, memberUserID string) bool {
	if CanManageMember(p, memberOrgID) {
		return true
	}
	return p.UserID == memberUserID
}

// 应用资源 ----------------------------------------------------------

// CanViewApp 判断主体能否查看指定应用。
func CanViewApp(p Principal, appOrgID, appOwnerUserID string) bool {
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

// Persona 资源 ----------------------------------------------------
// 当前规则与组织读/写谓词完全等价；保留独立函数以便未来 persona
// 单独演进权限规则时只改这两处，不动调用方。

// CanViewOrgPersona 等价于 CanViewOrg，保留位置以便未来差异化。
func CanViewOrgPersona(p Principal, orgID string) bool {
	return CanViewOrg(p, orgID)
}

// CanManageOrgPersona 等价于 CanManageOrg，保留位置以便未来差异化。
func CanManageOrgPersona(p Principal, orgID string) bool {
	return CanManageOrg(p, orgID)
}
```

- [ ] **Step 1.4: 跑测试，确认全绿**

```bash
go test ./internal/auth/... -v
```

预期：所有 `TestCan*` 用例 PASS。

- [ ] **Step 1.5: vet + 全量 build**

```bash
go vet ./internal/auth/...
go build ./...
```

预期：无输出（vet 无警告 / build 成功）。

- [ ] **Step 1.6: commit**

```bash
git add internal/auth/authorizer.go internal/auth/authorizer_test.go
git commit -m "$(cat <<'EOF'
feat(auth): 新增 authorizer.go 集中权限谓词

为后续从 service 层收编 canManageOrg / canViewOrg / canAccessMember /
canManageMember / canEditOwnProfile / canViewApp / canViewOrgPersona /
canEditOrgPersona 等谓词提供集中位置；参数采用 string ID 形态，避免
auth 包反向依赖 sqlc / pgtype。CanViewOrgPersona / CanManageOrgPersona
转发到 Org 谓词，为 persona 未来差异化预留位置。

table-driven 测试覆盖三种角色 × 同/异组织 × 同/异 user 的关键组合。
本步骤仅新增文件，service 层尚未切换。
EOF
)"
```

---

## Chunk 2: 调用点逐文件迁移（旧谓词暂留）

**重构守则（Task 2-5 共用）：**

- 改之前先 `go test ./internal/service/...` 一次，确认 baseline 全绿，作为本次重构的安全网。
- 每个 task 改完 `go vet ./... && go test ./...`，确认全绿再 commit。
- service 包内**不删除旧本地 canX 函数定义**，只把调用点替换掉；删除统一在 Task 6。
- 替换时记得 `import "oc-manager/internal/auth"`（如果文件里已有 `auth` 包别的引用，无需重复加）。

### Task 2: 迁移 `member_service.go` 的 8 处调用点

**Files:**
- Modify: `internal/service/member_service.go`

- [ ] **Step 2.1: 跑测试，确认 baseline 全绿**

```bash
go test ./internal/service/... -count=1
```

预期：全部 PASS。如有 fail，先停下来排查，不要带病改重构。

- [ ] **Step 2.2: 用 grep 精确定位 8 处调用点**

```bash
git grep -nE 'canManageOrg|canViewOrg|canAccessMember|canManageMember|canEditOwnProfile' internal/service/member_service.go | grep -v '^internal/service/member_service.go:.*func can'
```

预期看到 8 行调用（不含 5 行函数定义）。

- [ ] **Step 2.3: 替换调用点**

逐行替换为 `auth.CanX(...)`，按下面规则。**注意**：`uuidToString` / `uuidToOptionalString` 来自同包 `internal/service/pgtype.go`，不需要 import。

| 调用现状 | 改为 |
|---|---|
| `canManageOrg(principal, orgID)` | `auth.CanManageOrg(principal, orgID)` |
| `canViewOrg(principal, orgID)` | `auth.CanViewOrg(principal, orgID)` |
| `canAccessMember(principal, user)` | `auth.CanViewMember(principal, uuidToOptionalString(user.OrgID), uuidToString(user.ID))` |
| `canManageMember(principal, user)` | `auth.CanManageMember(principal, uuidToOptionalString(user.OrgID))` |
| `canEditOwnProfile(principal, user)` | `auth.CanEditMember(principal, uuidToOptionalString(user.OrgID), uuidToString(user.ID))` |

- [ ] **Step 2.4: 验证无遗漏**

```bash
git grep -nE '\bcan(ManageOrg|ViewOrg|AccessMember|ManageMember|EditOwnProfile)\(' internal/service/member_service.go | grep -v 'func can'
```

预期：输出为空（所有调用都已替换；只剩 5 个函数定义，被 grep 排除）。

- [ ] **Step 2.5: vet + test**

```bash
go vet ./internal/service/...
go test ./internal/service/... -count=1
```

预期：vet 无警告，所有测试 PASS。

- [ ] **Step 2.6: commit**

```bash
git add internal/service/member_service.go
git commit -m "$(cat <<'EOF'
refactor(auth): member_service 调用点改用 auth.CanX

将 member_service.go 内 8 处 canManageOrg / canViewOrg / canAccessMember /
canManageMember / canEditOwnProfile 调用迁移到 auth.CanX 形态，传 string
ID 而非 sqlc.User。本地 5 个 canX 函数暂保留（已无人调用），
统一在最后一步删除。
EOF
)"
```

---

### Task 3: 迁移 `app_service.go` 及调用方

**Files:**
- Modify: `internal/service/app_service.go`
- Modify: `internal/service/workspace_service.go`
- Modify: `internal/service/channel_service.go`
- Modify: `internal/service/runtime_operation_service.go`

- [ ] **Step 3.1: 定位所有 canViewApp / canViewOrg 调用**

```bash
git grep -nE '\bcan(ViewApp|ViewOrg)\(' internal/service/app_service.go internal/service/workspace_service.go internal/service/channel_service.go internal/service/runtime_operation_service.go
```

预期：5 行（app_service:67 canViewApp, app_service:75 canViewOrg, workspace:144 canViewApp, channel:209 canViewApp, runtime_operation:126 canViewApp）。

- [ ] **Step 3.2: 替换 4 处 canViewApp 调用**

| 文件 | 改为 |
|---|---|
| `app_service.go` | `auth.CanViewApp(principal, uuidToString(app.OrgID), uuidToString(app.OwnerUserID))` |
| `workspace_service.go` | 同上模板 |
| `channel_service.go` | 同上模板 |
| `runtime_operation_service.go` | 同上模板 |

注意：每个文件如果还没有 `import "oc-manager/internal/auth"` 需要加上（多数 service 已 import auth 用于 Principal）。

- [ ] **Step 3.3: 替换 app_service.go 的 1 处 canViewOrg**

```go
// 原（app_service.go:75 附近）
if !canViewOrg(principal, orgID) { ... }
// 改为
if !auth.CanViewOrg(principal, orgID) { ... }
```

- [ ] **Step 3.4: 验证无遗漏**

```bash
git grep -nE '\bcan(ViewApp|ViewOrg)\(' internal/service/app_service.go internal/service/workspace_service.go internal/service/channel_service.go internal/service/runtime_operation_service.go | grep -v 'func can'
```

预期：输出为空。

- [ ] **Step 3.5: vet + test + commit**

```bash
go vet ./internal/service/...
go test ./internal/service/... -count=1
git add internal/service/app_service.go internal/service/workspace_service.go internal/service/channel_service.go internal/service/runtime_operation_service.go
git commit -m "$(cat <<'EOF'
refactor(auth): app/workspace/channel/runtime_operation 调用点改用 auth.CanX

4 处 canViewApp + 1 处 canViewOrg 调用迁移到 auth.CanX。
本地 canViewApp 函数暂保留，统一在最后一步删除。
EOF
)"
```

---

### Task 4: 迁移 `persona_service.go`

**Files:**
- Modify: `internal/service/persona_service.go`

- [ ] **Step 4.1: 定位 2 处调用**

```bash
git grep -nE '\bcan(ViewOrgPersona|EditOrgPersona)\(' internal/service/persona_service.go | grep -v 'func can'
```

预期：2 行调用。

- [ ] **Step 4.2: 替换调用点**

| 调用现状 | 改为 |
|---|---|
| `canViewOrgPersona(principal, orgID)` | `auth.CanViewOrgPersona(principal, orgID)` |
| `canEditOrgPersona(principal, orgID)` | `auth.CanManageOrgPersona(principal, orgID)` |

- [ ] **Step 4.3: 验证无遗漏 + vet + test + commit**

```bash
git grep -nE '\bcan(ViewOrgPersona|EditOrgPersona)\(' internal/service/persona_service.go | grep -v 'func can'
go vet ./internal/service/...
go test ./internal/service/... -count=1
git add internal/service/persona_service.go
git commit -m "$(cat <<'EOF'
refactor(auth): persona_service 调用点改用 auth.CanX

canViewOrgPersona → auth.CanViewOrgPersona;
canEditOrgPersona → auth.CanManageOrgPersona。
本地两个 persona 谓词暂保留，统一在最后一步删除。
EOF
)"
```

---

### Task 5: 迁移收尾（knowledge / onboarding / audit）

**Files:**
- Modify: `internal/service/knowledge_service.go`
- Modify: `internal/service/onboarding_service.go`
- Modify: `internal/service/audit_service.go`

- [ ] **Step 5.1: 定位剩余调用**

```bash
git grep -nE '\bcan(ManageOrg|ViewOrg)\(' internal/service/knowledge_service.go internal/service/onboarding_service.go internal/service/audit_service.go
```

预期：7 行（knowledge: 5 行；onboarding: 1 行；audit: 1 行；都不含函数定义因为这三个文件本来就没定义）。

- [ ] **Step 5.2: 批量替换**

| 调用现状 | 改为 |
|---|---|
| `canManageOrg(principal, orgID)` | `auth.CanManageOrg(principal, orgID)` |
| `canViewOrg(principal, orgID)` | `auth.CanViewOrg(principal, orgID)` |

注意确认这 3 个文件都已 `import "oc-manager/internal/auth"`（多数已 import）。

- [ ] **Step 5.3: 验证 service 包内除了三个谓词定义文件外，没有其它本地 canX 调用**

```bash
git grep -nE '\bcan(ManageOrg|ViewOrg|AccessMember|ManageMember|EditOwnProfile|ViewApp|ViewOrgPersona|EditOrgPersona)\(' internal/service/ | grep -v 'func can' | grep -v _test.go
```

预期：输出为空（所有非测试调用都已迁移；测试文件里若有少量直接调谓词的旧用例，下一步处理）。

- [ ] **Step 5.4: 检查测试文件中的本地 canX 调用**

```bash
git grep -nE '\bcan(ManageOrg|ViewOrg|AccessMember|ManageMember|EditOwnProfile|ViewApp|ViewOrgPersona|EditOrgPersona)\(' internal/service/ | grep _test.go
```

如有命中：测试文件直接调本地 canX 的，按 Task 2-4 同样的规则替换为 `auth.CanX`。这能确保 Task 6 删除函数时测试不会断。

- [ ] **Step 5.5: vet + test + commit**

```bash
go vet ./internal/service/...
go test ./internal/service/... -count=1
git add internal/service/knowledge_service.go internal/service/onboarding_service.go internal/service/audit_service.go
# 如 Step 5.4 有改测试文件也一起 add
git commit -m "$(cat <<'EOF'
refactor(auth): knowledge/onboarding/audit 调用点改用 auth.CanX

迁移 knowledge_service 5 处 + onboarding_service 1 处 + audit_service 1 处
canManageOrg / canViewOrg 调用为 auth.CanX。至此 service 包内除三个谓词
定义文件外，无任何本地 canX 调用。
EOF
)"
```

---

## Chunk 3: 删除旧定义 + 文档约定

### Task 6: 删除 service 包内全部本地 canX 函数定义

**Files:**
- Modify: `internal/service/member_service.go`（删 5 个函数）
- Modify: `internal/service/app_service.go`（删 1 个函数）
- Modify: `internal/service/persona_service.go`（删 2 个函数）

- [ ] **Step 6.1: 全仓确认无任何调用残留**

```bash
git grep -nE '\bcan(ManageOrg|ViewOrg|AccessMember|ManageMember|EditOwnProfile|ViewApp|ViewOrgPersona|EditOrgPersona)\(' internal/ | grep -v 'func can'
```

预期：输出为空。**如有命中，必须先回到 Task 2-5 补迁移，绝对不能直接删函数。**

- [ ] **Step 6.2: 删除 member_service.go 中 5 个本地谓词函数**

定位：

```bash
grep -nE '^// can(ManageOrg|ViewOrg|AccessMember|ManageMember|EditOwnProfile)|^func can(ManageOrg|ViewOrg|AccessMember|ManageMember|EditOwnProfile)' internal/service/member_service.go
```

逐个用 Edit 工具删除以下函数及其上方 godoc 注释（spec 引用区间约 365-418 行，但行号可能因 Task 2 的调用替换略有漂移，以实际 grep 输出为准）：

- `canManageOrg(principal auth.Principal, orgID string) bool` — 含 `// canManageOrg ...` 注释
- `canViewOrg(principal auth.Principal, orgID string) bool` — 含 `// canViewOrg ...` 注释
- `canAccessMember(principal auth.Principal, user sqlc.User) bool` — 含 `// canAccessMember ...` 注释
- `canManageMember(principal auth.Principal, user sqlc.User) bool` — 含 `// canManageMember ...` 注释
- `canEditOwnProfile(principal auth.Principal, user sqlc.User) bool` — 含 `// canEditOwnProfile ...` 注释

- [ ] **Step 6.3: 删除 app_service.go 中 canViewApp**

```bash
grep -nE '^// canViewApp|^func canViewApp' internal/service/app_service.go
```

删除 `canViewApp(principal auth.Principal, app sqlc.App) bool` 及其注释。

- [ ] **Step 6.4: 删除 persona_service.go 中两个 persona 谓词**

```bash
grep -nE '^// can(ViewOrgPersona|EditOrgPersona)|^func can(ViewOrgPersona|EditOrgPersona)' internal/service/persona_service.go
```

删除 `canViewOrgPersona` 与 `canEditOrgPersona` 函数及其注释。

- [ ] **Step 6.5: 验证 service 包内不再有任何 `func can[A-Z]` 定义**

```bash
git grep -nE '^func can[A-Z]' internal/service/
```

预期：输出为空。

- [ ] **Step 6.6: 全量 vet + test**

```bash
go vet ./...
go test ./... -count=1
```

预期：vet 无新增警告，所有包测试 PASS。

- [ ] **Step 6.7: commit**

```bash
git add internal/service/member_service.go internal/service/app_service.go internal/service/persona_service.go
git commit -m "$(cat <<'EOF'
refactor(auth): 删除 service 包内全部本地权限谓词

调用点已全部迁至 auth.CanX，本次删除：
- member_service: canManageOrg / canViewOrg / canAccessMember /
  canManageMember / canEditOwnProfile
- app_service: canViewApp
- persona_service: canViewOrgPersona / canEditOrgPersona

至此 internal/service 不再持有任何角色权限判断逻辑。
EOF
)"
```

---

### Task 7: 在 AGENTS.md 增加权限校验约定

**Files:**
- Modify: `AGENTS.md`

- [ ] **Step 7.1: 定位插入位置**

```bash
grep -n '^## ' AGENTS.md
```

挑选合适的位置：在「基本原则」之后、「Commit Message」之前新增一节 `## 权限校验`。

- [ ] **Step 7.2: 用 Edit 工具插入新段落**

在「基本原则」一节末尾后插入：

```markdown
## 权限校验

- 角色 / 资源权限谓词（platform_admin / org_admin / org_member 三层判断）必须放在
  `internal/auth/authorizer.go`，service 包不再定义本地 `canX` 函数。
- 新增权限规则时优先扩展现有 `Can*` 函数，避免在 handler 或 service 内联写
  `if principal.Role == "..."` 判断；如确需新增，提交 PR 时请说明设计取舍。
```

- [ ] **Step 7.3: commit**

```bash
git add AGENTS.md
git commit -m "$(cat <<'EOF'
docs(agents): 增加权限谓词集中位置约定

明确 platform_admin / org_admin / org_member 三层角色权限谓词必须
放在 internal/auth/authorizer.go，service 包不再定义本地 canX 函数。
EOF
)"
```

---

## 完成定义验证

所有 task 完成后执行下面 6 项验收，全部通过才算 plan 落地：

- [ ] **DoD-1:** `git grep -nE '^func can[A-Z]' internal/service/` 输出为空
- [ ] **DoD-2:** `go test ./...` 全绿
- [ ] **DoD-3:** `go vet ./...` 无新增告警
- [ ] **DoD-4:** `internal/auth/authorizer.go` 所有公共函数有中文 godoc
- [ ] **DoD-5:** `internal/auth/authorizer_test.go` 覆盖每个谓词的角色 × 资源 矩阵（Task 1 已落地）
- [ ] **DoD-6:** AGENTS.md 含「权限校验」段落（Task 7 已落地）

---

## 回滚策略

每个 commit 独立可回退：

- 想回退到「权限尚未集中化」状态：`git revert` 从 Task 7 commit 倒序到 Task 1 commit 即可。
- Task 6 之前的任意 commit 都能独立回退而不影响其他：因为 Task 2-5 期间，service 层 canX 定义还在，调用点用哪个版本都能编译。
- Task 6（删定义）必须配合前面所有 Task 一起回退，因为它依赖调用点已全部迁移。

---

## 风险与应对

| 风险 | 何时出现 | 应对 |
|---|---|---|
| 替换调用点时把字段名提取写错（如把 `user.ID` 当 OrgID 传） | Task 2-5 | 每个 task 末尾 `go test ./internal/service/...` 必须全绿；service 测试中的 forbidden 分支会因权限判定结果反转而 fail |
| 测试文件直接调本地 canX，Task 6 删函数时编译失败 | Task 6.6 | Step 5.4 已显式排查；若 Task 6 编译失败回到 Step 5.4 补迁移再继续 |
| 替换时忘记 import auth 包 | Task 2-5 | 编译期立即暴露 `undefined: auth` |
| 顺手改了 spec 范围外的内容 | 任意 task | commit 前 `git diff --cached --stat` 看影响文件列表，仅本 task 列表的文件 |
