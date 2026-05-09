# 权限校验集中化：authorizer 收编

- 日期：2026-05-09
- 范围：后端 Go 代码库
- 主线项编号：A-1（出自 2026-05-09 全面体检报告）

## 1. 背景

当前角色/资源权限谓词散落在 `internal/service` 多个文件中：

| 谓词 | 当前位置 | 调用点数 |
|---|---|---|
| `canManageOrg` | `member_service.go:366` | knowledge ×4、member ×1、onboarding ×1 |
| `canViewOrg` | `member_service.go:378` | knowledge ×1、member ×1、audit ×1、app ×1 |
| `canAccessMember` | `member_service.go:387` | member ×1 |
| `canManageMember` | `member_service.go:401` | member ×4 |
| `canEditOwnProfile` | `member_service.go:413` | member ×1 |
| `canViewApp` | `app_service.go:107` | app ×1、workspace ×1、channel ×1、runtime_operation ×1 |
| `canViewOrgPersona` | `persona_service.go:121` | persona ×1（**谓词体与 `canViewOrg` 完全相同**） |
| `canEditOrgPersona` | `persona_service.go:132` | persona ×1（**谓词体与 `canManageOrg` 完全相同**） |

合计 22 处调用点。带来的具体问题：

1. `knowledge_service.go` / `onboarding_service.go` 等模块为了用 `canManageOrg`，需要反向 import 它的定义所在文件（`member_service.go`）所在包，形成隐性耦合。
2. `persona_service.go` 出现了两个与 `canViewOrg / canManageOrg` 谓词体逐字段相同的复制粘贴函数，是「无人收编」导致的死代码。
3. 角色判断规则（platform_admin / org_admin / org_member 三层）未来若调整，需在多处同步修改，遗漏概率高。

## 2. 目标

将所有角色/资源权限谓词收编到 `internal/auth/authorizer.go`，service 层不再定义本地 `canX` 函数，仅作为消费方调用。

## 3. 非目标（避免范围蔓延）

- 不引入路由层声明式权限（如 `Route{RequiredRole: ...}`）；那是更大的另一项改造。
- 不修改 `auth.Principal` 结构。
- 不动 handler / middleware。
- 不迁移 `uuidToString` / `parseUUID` 等 pgtype helper（仍留在 `internal/service/pgtype.go`）。

## 4. 设计

### 4.1 关键决策

| 决策点 | 选择 | 理由 |
|---|---|---|
| 谓词参数形态 | **string ID**（不传 `sqlc.User` / `sqlc.App`） | 让 `internal/auth` 保持零下游依赖；若传 sqlc 类型，auth 包将反向 import `pgtype` + `sqlc`，污染分层 |
| persona 谓词去重 | **保留 `CanViewOrgPersona` / `CanManageOrgPersona`**，身体直接转发到 `CanViewOrg` / `CanManageOrg` | 为 persona 未来可能的独立规则（如「persona 仅 org_admin 可见」）预留扩展位 |
| 约定落地 | **在 `AGENTS.md` 增加权限谓词集中位置约定** | 避免后续新 service 重蹈覆辙 |

### 4.2 目标 API

新文件 `internal/auth/authorizer.go`：

```go
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

// Persona ----------------------------------------------------------
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

### 4.3 调用点改造模板

#### 仅传 orgID 的（`canManageOrg` / `canViewOrg`）

```go
// 改前
if !canManageOrg(principal, orgID) { return ErrForbidden }
// 改后
if !auth.CanManageOrg(principal, orgID) { return ErrForbidden }
```

#### 传 sqlc.User 的（`canAccessMember` / `canManageMember` / `canEditOwnProfile`）

```go
// 改前
if !canManageMember(principal, user) { ... }
// 改后
if !auth.CanManageMember(principal, uuidToOptionalString(user.OrgID)) { ... }

// 改前
if !canEditOwnProfile(principal, user) { ... }
// 改后
if !auth.CanEditMember(principal,
    uuidToOptionalString(user.OrgID),
    uuidToString(user.ID),
) { ... }
```

#### 传 sqlc.App 的（`canViewApp`）

```go
// 改前
if !canViewApp(principal, app) { ... }
// 改后
if !auth.CanViewApp(principal,
    uuidToString(app.OrgID),
    uuidToString(app.OwnerUserID),
) { ... }
```

#### persona

```go
// 改前
if !canViewOrgPersona(principal, orgID) { ... }
// 改后
if !auth.CanViewOrgPersona(principal, orgID) { ... }
```

## 5. 迁移步骤

每步独立可回滚，每步一个 commit。**关键约束**：旧谓词定义在 step 6 才统一删除——前面所有调用点替换期间，service 包的本地 `canX` 暂时保留但调用归零，保证每一步都能编译、测试能跑。

| Step | 内容 | 完成判据 |
|---|---|---|
| 1 | 新建 `internal/auth/authorizer.go` + table-driven `internal/auth/authorizer_test.go`；service 层暂不动 | `go test ./internal/auth/...` 全绿；`go vet ./...` 无新增告警 |
| 2 | 迁移 `member_service.go` 内 8 处调用点为 `auth.X`；同步更新 `member_service_test.go`；旧本地谓词**暂保留** | `go test ./internal/service/...` 全绿；`grep -n 'canManageOrg\|canViewOrg\|canAccessMember\|canManageMember\|canEditOwnProfile' internal/service/member_service.go` 仅剩函数定义行（无调用） |
| 3 | 迁移 `app_service.go` + `workspace_service.go` + `channel_service.go` + `runtime_operation_service.go` 共 4 处调用点为 `auth.CanViewApp`；旧 `canViewApp` 定义暂保留 | 同上风格 |
| 4 | 迁移 `persona_service.go` 内 2 处调用点为 `auth.CanViewOrgPersona` / `auth.CanManageOrgPersona`；旧两个本地谓词暂保留 | 同上 |
| 5 | 迁移收尾：`knowledge_service.go`(5)、`onboarding_service.go`(1)、`audit_service.go`(1) 共 7 处调用点替换为 `auth.X` | `grep -nE 'can(Manage\|View\|Access\|Edit)(Org\|Member\|App\|OwnProfile)\b' internal/service/*.go` 仅剩 `member_service.go` / `app_service.go` / `persona_service.go` 中的函数定义行 |
| 6 | 删除 `member_service.go` / `app_service.go` / `persona_service.go` 中全部本地 `canX` 函数定义 | `git grep -nE '^func can[A-Z]' internal/service/` 输出为空；`go test ./...` 全绿 |
| 7 | 在 `AGENTS.md` 增加权限校验约定段落 | reviewer 复核文案 |

## 6. 测试策略

### 6.1 `internal/auth/authorizer_test.go`（新增）

table-driven，对每个谓词覆盖：

- 三种 `principal.Role`：`platform_admin` / `org_admin` / `org_member`
- 主体 OrgID 与目标 OrgID 同/异
- 主体 UserID 与目标 UserID 同/异（仅成员维度）

例：

```go
func TestCanManageMember(t *testing.T) {
    cases := []struct {
        name        string
        role        string
        principalOrg string
        memberOrg    string
        want         bool
    }{
        {"platform_admin 可管任意成员", "platform_admin", "org-A", "org-B", true},
        {"org_admin 仅可管本组织成员", "org_admin", "org-A", "org-A", true},
        {"org_admin 不可跨组织管成员", "org_admin", "org-A", "org-B", false},
        {"org_member 不可管成员（含自己）", "org_member", "org-A", "org-A", false},
    }
    for _, c := range cases {
        t.Run(c.name, func(t *testing.T) {
            p := Principal{Role: c.role, OrgID: c.principalOrg}
            if got := CanManageMember(p, c.memberOrg); got != c.want {
                t.Fatalf("got %v, want %v", got, c.want)
            }
        })
    }
}
```

### 6.2 `internal/service/*_test.go`（保留 + 简化）

- 保留每个 service 测试中针对「拒绝路径」的覆盖（确保业务流确实在 forbidden 时返回 `ErrForbidden`）。
- **不再在每个 service 重复枚举完整角色 × 资源矩阵**——矩阵已由 auth 包测试覆盖。

### 6.3 不引入 testify

按全面体检报告，testify 统一是另一条独立改造（C-2）。本步骤保持现有 `t.Fatalf` 风格，避免范围混杂。

## 7. AGENTS.md 增量

在 AGENTS.md「基本原则」之后新增：

```markdown
## 权限校验

- 角色 / 资源权限谓词（platform_admin / org_admin / org_member 三层判断）必须放在
  `internal/auth/authorizer.go`，service 包不再定义本地 `canX` 函数。
- 新增权限规则时优先扩展现有 `Can*` 函数，避免在 handler 或 service 内联写
  `if principal.Role == "..."` 判断；如确需新增，提交 PR 时请说明设计取舍。
```

## 8. 风险与缓解

| 风险 | 严重度 | 缓解 |
|---|---|---|
| 调用点改成传 string ID 时漏写字段提取（如把 `user.ID` 当 OrgID 传） | 中 | 编译期类型检查仅拦得住数量错误，**写错字段名不会编译失败**；强制每个调用点对应有 forbidden 分支测试，CI 覆盖 |
| `persona` 谓词独立保留后看似废代码，未来被人误删 | 低 | spec 中明确「故意保留」；godoc 中写明保留意图 |
| 后续新加 service 仍可能写本地 `canX` | 中 | AGENTS.md 约定 + 不定期 `git grep -nE '^func can[A-Z]' internal/service/` 巡检 |
| 本设计未做路由层声明式权限改造，handler 层仍依赖 service 层抛 `ErrForbidden` 兜底 | 低 | 已在「非目标」中明确，留给后续独立 spec |

## 9. 完成定义（DoD）

- [ ] `git grep -nE '^func can[A-Z]' internal/service/` 输出为空
- [ ] `go test ./...` 全绿
- [ ] `go vet ./...` 无新增告警
- [ ] `internal/auth/authorizer.go` 所有公共函数有 godoc 注释（中文，符合 AGENTS.md 注释规范）
- [ ] `internal/auth/authorizer_test.go` 覆盖每个谓词的角色 × 资源 矩阵
- [ ] AGENTS.md 增加权限校验约定段落

## 10. 后续

本 spec 落地后，进入 writing-plans 出更细的实施计划（按 step 1-7 拆 task，标记可并行/必须串行的依赖）。
