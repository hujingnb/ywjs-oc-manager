// status.ts 负责把后端状态机原值映射为 i18n 标签键和视觉语义。
// label 字段已从中文字面量迁移为 i18n 键，由消费方（StatusBadge / 组件）通过 t() 解析为实际文案。
// 未识别状态保留原值并使用 warning，确保新增状态不会在 UI 中静默消失。
// StatusView 是页面 badge 组件消费的统一状态展示结构。
export interface StatusView {
  // i18n 键，消费方通过 t(label, params) 解析为展示文案。
  label: string
  // 视觉语义，由页面组件映射到具体颜色。
  tone: 'neutral' | 'success' | 'warning' | 'danger'
  // i18n 插值参数，用于含占位符的降级文案（如未知状态 {status}）；无插值时省略。
  params?: Record<string, string>
}

// appStatusViews 覆盖应用生命周期状态，新增后端状态时应同步补充。
// label 现为 i18n 键（domain.appStatus.*），由 StatusBadge 通过 t() 展示为当前语言文案。
const appStatusViews: Record<string, Omit<StatusView, 'params'>> = {
  draft:                  { label: 'domain.appStatus.draft',                 tone: 'neutral' },
  pulling_runtime_image:  { label: 'domain.appStatus.pulling_runtime_image', tone: 'warning' },
  preparing_runtime:      { label: 'domain.appStatus.preparing_runtime',     tone: 'warning' },
  creating_container:     { label: 'domain.appStatus.creating_container',    tone: 'warning' },
  starting:               { label: 'domain.appStatus.starting',              tone: 'warning' },
  binding_waiting:        { label: 'domain.appStatus.binding_waiting',       tone: 'warning' },
  binding_failed:         { label: 'domain.appStatus.binding_failed',        tone: 'danger' },
  running:                { label: 'domain.appStatus.running',               tone: 'success' },
  stopped:                { label: 'domain.appStatus.stopped',               tone: 'neutral' },
  error:                  { label: 'domain.appStatus.error',                 tone: 'danger' },
  deleted:                { label: 'domain.appStatus.deleted',               tone: 'neutral' },
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

// isInitPhase 判断 status 是否处于初始化进度可视化的 4 个子状态之一。
export function isInitPhase(status: string): boolean {
  return initPhaseStatuses.has(status)
}

// formatAppStatus 将后端状态机值映射为 i18n 标签键和视觉语义。
// 未识别状态返回 label='domain.appStatus.unknown' + params.status=原始值，
// 消费方通过 t(view.label, view.params) 展示含状态原值的降级文案。
export function formatAppStatus(status: string): StatusView {
  return appStatusViews[status] ?? { label: 'domain.appStatus.unknown', tone: 'warning', params: { status } }
}

// kanbanStatusViews 覆盖 Hermes Kanban 任务状态。
// label 现为 i18n 键（domain.kanbanStatus.*），消费方通过 t() 展示当前语言文案。
const kanbanStatusViews: Record<string, Omit<StatusView, 'params'>> = {
  running: { label: 'domain.kanbanStatus.running',  tone: 'warning' },
  ready:   { label: 'domain.kanbanStatus.ready',    tone: 'warning' },
  todo:    { label: 'domain.kanbanStatus.todo',     tone: 'neutral' },
  blocked: { label: 'domain.kanbanStatus.blocked',  tone: 'danger' },
  triage:  { label: 'domain.kanbanStatus.triage',   tone: 'neutral' },
  done:    { label: 'domain.kanbanStatus.done',     tone: 'success' },
  archived:{ label: 'domain.kanbanStatus.archived', tone: 'neutral' },
}

// formatKanbanStatus 将任务看板状态映射为 i18n 标签键和视觉语义。
// 未知状态返回 label='domain.kanbanStatus.unknown' + params.status=原始值。
export function formatKanbanStatus(status: string): StatusView {
  return kanbanStatusViews[status] ?? { label: 'domain.kanbanStatus.unknown', tone: 'warning', params: { status } }
}

// orgStatusViews 覆盖组织状态。
// label 现为 i18n 键（domain.orgStatus.*）。
const orgStatusViews: Record<string, Omit<StatusView, 'params'>> = {
  active:  { label: 'domain.orgStatus.active',   tone: 'success' },
  disabled:{ label: 'domain.orgStatus.disabled', tone: 'warning' },
  deleted: { label: 'domain.orgStatus.deleted',  tone: 'neutral' },
}

// memberStatusViews 覆盖用户启停状态；users.deleted_at 仅作为下线时间戳，不在此展示。
// label 现为 i18n 键（domain.memberStatus.*）。
const memberStatusViews: Record<string, Omit<StatusView, 'params'>> = {
  active:  { label: 'domain.memberStatus.active',   tone: 'success' },
  disabled:{ label: 'domain.memberStatus.disabled', tone: 'warning' },
}

// memberRoleLabels 是角色展示降级表，值已迁移为 i18n 键；未知角色返回原始值（非 i18n 键）。
const memberRoleLabels: Record<string, string> = {
  platform_admin: 'domain.memberRole.platform_admin',
  org_admin:      'domain.memberRole.org_admin',
  org_member:     'domain.memberRole.org_member',
}

// formatOrgStatus 将组织状态映射为 i18n 标签键和视觉语义。
// 未识别状态返回 label='domain.orgStatus.unknown' + params.status=原始值。
export function formatOrgStatus(status: string): StatusView {
  return orgStatusViews[status] ?? { label: 'domain.orgStatus.unknown', tone: 'warning', params: { status } }
}

// formatMemberStatus 将成员状态映射为 i18n 标签键和视觉语义。
// 未识别状态返回 label='domain.memberStatus.unknown' + params.status=原始值。
export function formatMemberStatus(status: string): StatusView {
  return memberStatusViews[status] ?? { label: 'domain.memberStatus.unknown', tone: 'warning', params: { status } }
}

// formatMemberRole 将成员角色映射为 i18n 键；未知角色返回原始值（非 i18n 键，直接展示）。
// 消费方需对已知角色调用 t(key)，对未知角色直接展示返回值（fallback 语义）。
export function formatMemberRole(role: string): string {
  return memberRoleLabels[role] ?? role
}
