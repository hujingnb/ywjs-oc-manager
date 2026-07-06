// permissions.ts 收敛前端页面级权限 helper。
// 这些函数只决定入口显示和前端交互，后端 authorizer 仍是最终权限来源。
export type Role = 'platform_admin' | 'org_admin' | 'org_member'

// PermissionUser 是权限判断需要的最小用户视图。
export interface PermissionUser {
  // 用户 ID，用于成员判断自有应用。
  id?: string
  // 所属组织 ID，用于组织管理员边界判断。
  org_id?: string
  // 角色允许 string 是为了兼容后端新增角色时前端降级为无写权限。
  role?: Role | string
}

// PermissionApp 是权限判断需要的最小应用视图。
export interface PermissionApp {
  // 应用所属组织。
  org_id?: string
  // 应用拥有者用户。
  owner_user_id?: string
}

// 组织侧创建应用必须经组织管理员入口；平台管理员只保留跨组织观察能力。
export function canCreateAppForOrg(user: PermissionUser | null | undefined, orgId?: string): boolean {
  return user?.role === 'org_admin' && Boolean(orgId) && user.org_id === orgId
}

// 组织知识库写入：组织管理员管本组织、平台管理员可跨组织维护
// （上传公共制度文档、运维补充资料等场景需要平台侧介入，与后端 CanWriteOrgKnowledge 保持一致）。
// orgId 省略时回退到用户自身的 org_id，避免页面未显式传入组织上下文时按钮消失。
export function canManageOrgKnowledge(user: PermissionUser | null | undefined, orgId?: string): boolean {
  if (!user) return false
  if (user.role === 'platform_admin') return true
  if (user.role !== 'org_admin') return false
  // target 优先使用调用方传入的 orgId；未传时回退用户自身 org_id（org_admin 只有一个归属组织）。
  const target = orgId ?? user.org_id
  return Boolean(target) && user.org_id === target
}

// 应用写操作包含运行时、渠道、API key 与应用知识库变更。
// 平台管理员需运维介入任意组织实例（协助排障 / 代客接入），故恒可管理；与后端 CanManageApp 一致。
export function canManageApp(user: PermissionUser | null | undefined, app: PermissionApp | null | undefined): boolean {
  if (!user || !app) return false
  if (user.role === 'platform_admin') return true
  if (user.role === 'org_admin') return user.org_id === app.org_id
  if (user.role === 'org_member') return user.id === app.owner_user_id
  return false
}

// canUpdateAppKnowledgeQuota：实例知识库容量属于平台侧租户配额，只允许平台管理员修改。
// 企业管理员不能单独在实例页面调整知识库大小（入口关闭），普通成员同样不可编辑。
export function canUpdateAppKnowledgeQuota(user: PermissionUser | null | undefined): boolean {
  return user?.role === 'platform_admin'
}

// canManageRAGFlowDatasetInfo 控制 RAGFlow dataset 运维弹框入口；后端仍是最终权限边界。
export function canManageRAGFlowDatasetInfo(user: PermissionUser | null | undefined): boolean {
  return user?.role === 'platform_admin'
}

// 组织级审计是管理视角；普通成员只能进入自己的应用审计。
export function canViewOrgAudit(user: PermissionUser | null | undefined, orgId?: string): boolean {
  if (!user || !orgId) return false
  return user.role === 'platform_admin' || (user.role === 'org_admin' && user.org_id === orgId)
}

// 应用审计是 target 视角：平台管理员可观察全部，组织管理员限本组织，成员限 owner。
export function canViewOwnAppAudit(user: PermissionUser | null | undefined, app: PermissionApp | null | undefined): boolean {
  if (!user || !app) return false
  if (user.role === 'platform_admin') return true
  if (user.role === 'org_admin') return user.org_id === app.org_id
  if (user.role === 'org_member') return user.id === app.owner_user_id
  return false
}

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

// canManageAppSkill 判断是否可管理某实例的 skill：平台管理员可管理任意实例，
// 否则与应用写权限同款（owner 本人 / 本 org 的 org_admin）。
// 注意：删除「当前版本必需的 skill」对所有角色仍禁止，由后端 ErrAppSkillProtected 保证。
export function canManageAppSkill(
  user: PermissionUser | null | undefined,
  app: PermissionApp | null | undefined,
): boolean {
  if (!user) return false
  if (user.role === 'platform_admin') return true
  return canManageApp(user, app)
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
