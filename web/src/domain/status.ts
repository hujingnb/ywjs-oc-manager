// status.ts 负责把后端状态机原值映射为中文标签和视觉语义。
// 未识别状态保留原值并使用 warning，确保新增状态不会在 UI 中静默消失。
// StatusView 是页面 badge 组件消费的统一状态展示结构。
export interface StatusView {
  // 中文展示文案。
  label: string
  // 视觉语义，由页面组件映射到具体颜色。
  tone: 'neutral' | 'success' | 'warning' | 'danger'
}

// appStatusViews 覆盖应用生命周期状态，新增后端状态时应同步补充。
// 4 个 init 子状态文案与后端状态机 1:1 对应，用户能直观看到”现在在做什么”。
const appStatusViews: Record<string, StatusView> = {
  draft:                  { label: '待初始化',         tone: 'neutral' },
  pulling_runtime_image:  { label: '拉取运行时镜像',   tone: 'warning' },
  preparing_runtime:      { label: '准备运行时配置',   tone: 'warning' },
  creating_container:     { label: '创建容器',         tone: 'warning' },
  starting:               { label: '启动容器',         tone: 'warning' },
  binding_waiting:        { label: '待绑定',           tone: 'warning' },
  binding_failed:         { label: '绑定失败',         tone: 'danger' },
  running:                { label: '运行中',           tone: 'success' },
  stopped:                { label: '已停止',           tone: 'neutral' },
  error:                  { label: '异常',             tone: 'danger' },
  deleted:                { label: '已删除',           tone: 'neutral' },
}

// initPhaseStatuses 是 worker 初始化期间会出现的 4 个子状态集合。
// AppOverviewTab 在 status ∈ 该集合时额外渲染 progress 进度条。
// 集合写在这里集中维护，避免组件硬编码字符串列表。
const initPhaseStatuses: ReadonlySet<string> = new Set([
  'pulling_runtime_image',
  'preparing_runtime',
  'creating_container',
  'starting',
])

// isInitPhase 判断 status 是否处于初始化进度可视化的 5 个子状态之一。
export function isInitPhase(status: string): boolean {
  return initPhaseStatuses.has(status)
}

// formatAppStatus 将后端状态机值映射为页面可展示的中文标签和视觉语义。
// 未识别状态使用 warning，便于在测试环境中尽早发现后端新增状态未同步到前端。
export function formatAppStatus(status: string): StatusView {
  return appStatusViews[status] ?? { label: `未知状态：${status}`, tone: 'warning' }
}

// orgStatusViews 覆盖企业状态。
const orgStatusViews: Record<string, StatusView> = {
  active: { label: '启用', tone: 'success' },
  disabled: { label: '禁用', tone: 'warning' },
  deleted: { label: '已删除', tone: 'neutral' },
}

// memberStatusViews 覆盖用户启停状态；users.deleted_at 仅作为下线时间戳，不在此展示。
const memberStatusViews: Record<string, StatusView> = {
  active: { label: '启用', tone: 'success' },
  disabled: { label: '禁用', tone: 'warning' },
}

// memberRoleLabels 是角色展示降级表，未知角色保留原值。
const memberRoleLabels: Record<string, string> = {
  platform_admin: '平台管理员',
  org_admin: '企业管理员',
  org_member: '企业成员',
}

// formatOrgStatus 将企业状态映射为标签和视觉语义。
export function formatOrgStatus(status: string): StatusView {
  return orgStatusViews[status] ?? { label: `未知状态：${status}`, tone: 'warning' }
}

// formatMemberStatus 将成员状态映射为标签和视觉语义。
export function formatMemberStatus(status: string): StatusView {
  return memberStatusViews[status] ?? { label: `未知状态：${status}`, tone: 'warning' }
}

// formatMemberRole 将成员角色映射为中文文案。
// 未知角色返回原始值，避免后端灰度新增角色时页面显示空白。
export function formatMemberRole(role: string): string {
  return memberRoleLabels[role] ?? role
}

// runtimeNodeStatusViews 覆盖 runtime 节点心跳和可调度状态。
const runtimeNodeStatusViews: Record<string, StatusView> = {
  pending: { label: '待注册', tone: 'warning' },
  active: { label: '在线', tone: 'success' },
  degraded: { label: '探测异常', tone: 'warning' },
  unreachable: { label: '失联', tone: 'danger' },
  disabled: { label: '禁用', tone: 'neutral' },
}

// formatRuntimeNodeStatus 将节点状态映射为中文文案与视觉语义。
export function formatRuntimeNodeStatus(status: string): StatusView {
  return runtimeNodeStatusViews[status] ?? { label: `未知状态：${status}`, tone: 'warning' }
}
