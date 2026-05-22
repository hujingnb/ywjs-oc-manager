# 权限重构与说明页设计文档

**日期**：2026-05-22  
**状态**：待实现

---

## 1. 背景与目标

当前 `internal/auth/authorizer.go` 已集中管理大部分权限谓词，但有四处权限与业务需求不符，需要调整。同时，平台管理员缺少一个可供查阅的权限说明页，导致角色边界不透明。

本次变更目标：

1. 修正四处权限规则，保持后端 authorizer 与前端 permissions.ts 对齐。
2. 新增平台管理员专属"权限说明"页，展示全量权限矩阵。

---

## 2. 权限规则变更

### 2.1 成员列表禁止组织成员访问

**变更前**：`member_service.ListMembers` 调用 `auth.CanViewOrg`，org_member 可拿到本组织全部成员列表。

**变更后**：新增谓词 `CanListMembers`，仅 platform_admin 和 org_admin 可访问；org_member 返回 403。

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

**调用点更新**：`member_service.go` `ListMembers` 函数将 `auth.CanViewOrg` 改为 `auth.CanListMembers`。

**前端**：`MembersPage` 路由已设置 `allowedRoles: ORG_ADMIN_ABOVE`，路由守卫已阻止 org_member 访问，无需额外改动。

---

### 2.2 切换助手版本允许平台管理员操作

**变更前**：`app_service.SwitchAppVersion` 调用 `auth.CanManageApp`，该谓词明确排除 platform_admin（平台管理员只有跨组织观察权，不得写应用配置）。

**变更后**：新增谓词 `CanSwitchAppVersion`，在现有 org_admin / org_member 的基础上加入 platform_admin。`CanManageApp` 本身保持不变，继续用于渠道绑定、知识库写入等操作（这些仍不对 platform_admin 开放）。

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

**调用点更新**：`app_service.go` `SwitchAppVersion` 将 `auth.CanManageApp` 改为 `auth.CanSwitchAppVersion`。

**前端 permissions.ts**：

```ts
// canSwitchAppVersion：平台管理员可统一管理版本，故加入写权限。
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
```

**前端 AppOverviewTab.vue**：切换按钮的 `v-if` 从 `canManageApp(auth.user, app) && canViewVersions` 改为 `canSwitchAppVersion(auth.user, app) && canViewVersions`。

---

### 2.3 启动 / 停止 / 重启应用允许平台管理员操作

**变更前**：`CanTriggerRuntimeOperation` 委托 `CanManageApp`，platform_admin 无法触发运行时操作。

**变更后**：`CanTriggerRuntimeOperation` 不再委托 `CanManageApp`，改为独立实现，加入 platform_admin。

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

**调用点**：`runtime_operation_service.go` 两处调用 `auth.CanTriggerRuntimeOperation`，不需要改动，谓词实现已更新。

**前端 permissions.ts**：

```ts
// canTriggerRuntimeOperation：平台管理员需要运维介入能力，与 canManageApp 分离。
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

**前端 AppRuntimeTab.vue**：`canManage` computed 从 `canManageApp(auth.user, app?.value)` 改为 `canTriggerRuntimeOperation(auth.user, app?.value)`，import 同步更新。

---

### 2.4 组织成员可查看助手版本

**变更前**：`CanViewAssistantVersion` 仅 platform_admin 和 org_admin 可访问；org_member 无法获取版本名称，应用概览中版本字段回退显示版本 ID。

**变更后**：`CanViewAssistantVersion` 加入 org_member，使其可拉取版本目录，在应用概览中正常显示版本名称。

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

**调用点**：`assistant_version_service.go` 三处调用均为 `CanViewAssistantVersion`，不需要改动。

**前端 AppOverviewTab.vue**：`canViewVersions` 从 `auth.isPlatformAdmin || auth.user?.role === 'org_admin'` 改为 `!!auth.user`（已登录用户均可拉取版本目录）。

---

## 3. 权限说明页

### 3.1 页面位置与路由

| 项目 | 值 |
|---|---|
| 路由路径 | `/platform/permissions` |
| 访问权限 | `PLATFORM_ONLY`（仅 platform_admin） |
| 组件路径 | `web/src/pages/platform/PermissionsPage.vue` |
| 导航入口 | DashboardLayout 平台专区新增"权限说明"，图标 `ShieldCheck`（lucide-vue-next） |

### 3.2 页面内容

静态权限矩阵，按功能模块分组展示，列为三个角色，行为具体操作。图例说明三种符号含义。

页面布局（从上到下）：

1. **页面标题**：「权限说明」+ 副标题「各角色可见 / 可操作范围一览」
2. **图例**：✅ 可操作 / ❌ 无权限 / 🟡 有条件（本组织 / 自己的）
3. **权限矩阵表**：按以下 17 个功能分组，每组一段标题 + 表格

功能分组与操作行（以下为完整清单）：

**组织管理**
| 操作 | 平台管理员 | 组织管理员 | 组织成员 |
|---|---|---|---|
| 创建组织 | ✅ | ❌ | ❌ |
| 组织列表 | ✅ | ❌ | ❌ |
| 查看组织详情 | ✅ | 🟡 本组织 | 🟡 本组织 |
| 修改组织信息 | ✅ | ❌ | ❌ |
| 启用 / 禁用组织 | ✅ | ❌ | ❌ |

**成员管理**
| 操作 | 平台管理员 | 组织管理员 | 组织成员 |
|---|---|---|---|
| 成员列表 | ✅ | 🟡 本组织 | ❌ |
| 查看成员详情 | ✅ | 🟡 本组织 | 🟡 仅自己 |
| 创建成员 | ❌ | 🟡 本组织 | ❌ |
| 修改成员资料 | 🟡 仅自己 | 🟡 本组织 | 🟡 仅自己 |
| 启用 / 禁用成员 | ❌ | 🟡 本组织 | ❌ |
| 删除成员 | ❌ | 🟡 本组织 | ❌ |
| 重置成员密码 | ❌ | 🟡 本组织 | ❌ |
| Onboard（初始建实例）| ❌ | 🟡 本组织 | ❌ |
| 为成员复建实例 | ✅ | 🟡 本组织 | ❌ |

**应用实例**
| 操作 | 平台管理员 | 组织管理员 | 组织成员 |
|---|---|---|---|
| 应用列表 | ✅ | 🟡 本组织全部 | 🟡 仅自己 |
| 查看应用详情 | ✅ | 🟡 本组织 | 🟡 仅自己 |
| 切换助手版本 | ✅ | 🟡 本组织 | 🟡 仅自己 |

**运行时操作**
| 操作 | 平台管理员 | 组织管理员 | 组织成员 |
|---|---|---|---|
| 启动 / 停止 / 重启 | ✅ | 🟡 本组织 | 🟡 仅自己 |

**渠道（Channel）**
| 操作 | 平台管理员 | 组织管理员 | 组织成员 |
|---|---|---|---|
| 查看渠道信息 | ✅ | 🟡 本组织 | 🟡 仅自己 |
| 绑定渠道 | ❌ | 🟡 本组织 | 🟡 仅自己 |

**知识库**
| 操作 | 平台管理员 | 组织管理员 | 组织成员 |
|---|---|---|---|
| 读取组织知识库 | ✅ | 🟡 本组织 | 🟡 本组织 |
| 写入组织知识库 | ❌ | 🟡 本组织 | ❌ |
| 查看组织知识库同步状态 | ❌ | 🟡 本组织 | ❌ |
| 触发组织知识库同步重试 | ❌ | 🟡 本组织 | ❌ |
| 读取应用知识库 | ✅ | 🟡 本组织 | 🟡 仅自己 |
| 写入应用知识库 | ❌ | 🟡 本组织 | 🟡 仅自己 |

**助手版本**
| 操作 | 平台管理员 | 组织管理员 | 组织成员 |
|---|---|---|---|
| 查看助手版本列表 / 详情 | ✅ | ✅ | ✅ |
| 创建 / 修改 / 删除助手版本 | ✅ | ❌ | ❌ |
| 上传技能包 | ✅ | ❌ | ❌ |

**任务看板（Kanban）**
| 操作 | 平台管理员 | 组织管理员 | 组织成员 |
|---|---|---|---|
| 查看任务看板 | ✅ | 🟡 本组织 | 🟡 仅自己 |
| 写操作（评论 / 完成 / 阻塞）| ✅ | 🟡 本组织 | 🟡 仅自己 |

**Cron 任务**
| 操作 | 平台管理员 | 组织管理员 | 组织成员 |
|---|---|---|---|
| 查看 Cron 列表 / 详情 | ✅ | 🟡 本组织 | 🟡 仅自己 |
| 创建 / 修改 / 启停 / 删除 Cron | ✅ | 🟡 本组织 | 🟡 仅自己 |

**用量**
| 操作 | 平台管理员 | 组织管理员 | 组织成员 |
|---|---|---|---|
| 查看组织聚合用量 | ✅ | 🟡 本组织 | ❌ |
| 查看成员用量 | ✅ | 🟡 本组织 | 🟡 仅自己 |
| 查看应用用量 | ✅ | 🟡 本组织 | 🟡 仅自己 |

**审计日志**
| 操作 | 平台管理员 | 组织管理员 | 组织成员 |
|---|---|---|---|
| 查看组织审计 | ✅ | 🟡 本组织 | ❌ |
| 查看应用审计 | ✅ | 🟡 本组织 | 🟡 仅自己 |
| 查看"我的审计" | ✅ | ✅ | ✅ |

**充值记录**
| 操作 | 平台管理员 | 组织管理员 | 组织成员 |
|---|---|---|---|
| 查看充值记录 | ✅ | 🟡 本组织 | ❌ |
| 查看余额 | ✅ | 🟡 本组织 | ❌ |

**运行时节点**
| 操作 | 平台管理员 | 组织管理员 | 组织成员 |
|---|---|---|---|
| 节点列表 / 详情 | ✅ | ❌ | ❌ |
| 启用 / 禁用节点 | ✅ | ❌ | ❌ |

**平台总览**
| 操作 | 平台管理员 | 组织管理员 | 组织成员 |
|---|---|---|---|
| 平台总览统计 | ✅ | ❌ | ❌ |

**模型列表**
| 操作 | 平台管理员 | 组织管理员 | 组织成员 |
|---|---|---|---|
| 查看可用模型列表 | ✅ | ❌ | ❌ |

**后台任务（Jobs）**
| 操作 | 平台管理员 | 组织管理员 | 组织成员 |
|---|---|---|---|
| 查看后台任务列表 | ✅ | ❌ | ❌ |

**工作区**
| 操作 | 平台管理员 | 组织管理员 | 组织成员 |
|---|---|---|---|
| 查看 / 下载 / 打包工作区文件 | ✅ | 🟡 本组织 | 🟡 仅自己 |

**资源指标**
| 操作 | 平台管理员 | 组织管理员 | 组织成员 |
|---|---|---|---|
| 查看应用资源指标 | ✅ | 🟡 本组织 | 🟡 仅自己 |

---

## 4. 文件变更清单

### 后端

| 文件 | 变更类型 | 说明 |
|---|---|---|
| `internal/auth/authorizer.go` | 修改 + 新增 | 新增 `CanListMembers`、`CanSwitchAppVersion`；修改 `CanTriggerRuntimeOperation`、`CanViewAssistantVersion` |
| `internal/service/member_service.go` | 修改 | `ListMembers` 调用点 `CanViewOrg` → `CanListMembers` |
| `internal/service/app_service.go` | 修改 | `SwitchAppVersion` 调用点 `CanManageApp` → `CanSwitchAppVersion` |

### 前端

| 文件 | 变更类型 | 说明 |
|---|---|---|
| `web/src/domain/permissions.ts` | 新增 | 新增 `canSwitchAppVersion`、`canTriggerRuntimeOperation` |
| `web/src/pages/apps/AppOverviewTab.vue` | 修改 | `canViewVersions` 改为全登录用户可见；切换按钮改用 `canSwitchAppVersion` |
| `web/src/pages/apps/AppRuntimeTab.vue` | 修改 | `canManage` 改用 `canTriggerRuntimeOperation` |
| `web/src/pages/platform/PermissionsPage.vue` | 新增 | 权限矩阵静态说明页 |
| `web/src/app/router.ts` | 修改 | 新增 `/platform/permissions` 路由 |
| `web/src/layouts/DashboardLayout.vue` | 修改 | 平台管理员导航新增"权限说明"入口 |

---

## 5. 测试要点

### 后端单元测试

- `CanListMembers`：platform_admin 任意组织返回 true；org_admin 本组织 true / 他组织 false；org_member 返回 false
- `CanSwitchAppVersion`：platform_admin 任意应用返回 true；org_admin 本组织 true / 他组织 false；org_member own true / 他人 false
- `CanTriggerRuntimeOperation`：platform_admin true；org_admin 本组织 true / 他组织 false；org_member own true / 他人 false
- `CanViewAssistantVersion`：三个角色均返回 true；未知角色返回 false

### 浏览器验证

- 以 org_member 身份登录，验证成员列表接口返回 403
- 以 platform_admin 身份登录，验证应用概览"切换版本"按钮可见，点击切换成功
- 以 platform_admin 身份登录，验证运行时 Tab 启动/停止/重启按钮可见，操作成功
- 以 org_member 身份登录，验证应用概览显示版本名称（非 ID）
- 以 platform_admin 身份登录，导航到"/platform/permissions"，权限矩阵正常显示
