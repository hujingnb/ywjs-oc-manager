export interface StatusView {
  label: string
  tone: 'neutral' | 'success' | 'warning' | 'danger'
}

const appStatusViews: Record<string, StatusView> = {
  draft: { label: '草稿', tone: 'neutral' },
  initializing: { label: '初始化中', tone: 'warning' },
  binding_waiting: { label: '待绑定', tone: 'warning' },
  binding_failed: { label: '绑定失败', tone: 'danger' },
  running: { label: '运行中', tone: 'success' },
  stopped: { label: '已停止', tone: 'neutral' },
  error: { label: '异常', tone: 'danger' },
  deleted: { label: '已删除', tone: 'neutral' },
}

// formatAppStatus 将后端状态机值映射为页面可展示的中文标签和视觉语义。
// 未识别状态使用 warning，便于在测试环境中尽早发现后端新增状态未同步到前端。
export function formatAppStatus(status: string): StatusView {
  return appStatusViews[status] ?? { label: `未知状态：${status}`, tone: 'warning' }
}

const orgStatusViews: Record<string, StatusView> = {
  active: { label: '启用', tone: 'success' },
  disabled: { label: '禁用', tone: 'warning' },
  deleted: { label: '已删除', tone: 'neutral' },
}

const memberStatusViews: Record<string, StatusView> = {
  active: { label: '启用', tone: 'success' },
  disabled: { label: '禁用', tone: 'warning' },
}

const memberRoleLabels: Record<string, string> = {
  platform_admin: '平台管理员',
  org_admin: '组织管理员',
  org_member: '组织成员',
}

// formatOrgStatus 将组织状态映射为标签和视觉语义。
export function formatOrgStatus(status: string): StatusView {
  return orgStatusViews[status] ?? { label: `未知状态：${status}`, tone: 'warning' }
}

// formatMemberStatus 将成员状态映射为标签和视觉语义。
export function formatMemberStatus(status: string): StatusView {
  return memberStatusViews[status] ?? { label: `未知状态：${status}`, tone: 'warning' }
}

// formatMemberRole 将成员角色映射为中文文案。
export function formatMemberRole(role: string): string {
  return memberRoleLabels[role] ?? role
}
