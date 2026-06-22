// domain 模块文案（en）。覆盖 status.ts、parseStatus.ts 等 domain 层状态标签。
// 结构与 zh/domain.ts 完全对应，翻译为自然英文。
export default {
  // 应用（worker）生命周期状态
  appStatus: {
    draft: 'Pending init',
    pulling_runtime_image: 'Pulling runtime image',
    preparing_runtime: 'Preparing runtime config',
    creating_container: 'Creating container',
    starting: 'Starting container',
    binding_waiting: 'Awaiting binding',
    binding_failed: 'Binding failed',
    running: 'Running',
    stopped: 'Stopped',
    error: 'Error',
    deleted: 'Deleted',
    // 未知状态降级文案，{status} 为后端原始值
    unknown: 'Unknown status: {status}',
  },
  // Hermes Kanban 任务状态
  kanbanStatus: {
    running: 'Running',
    ready: 'Ready',
    todo: 'To do',
    blocked: 'Blocked',
    triage: 'Triage',
    done: 'Done',
    archived: 'Archived',
    // 未知状态降级文案，{status} 为后端原始值
    unknown: 'Unknown status: {status}',
  },
  // 组织状态
  orgStatus: {
    active: 'Active',
    disabled: 'Disabled',
    deleted: 'Deleted',
    // 未知状态降级文案，{status} 为后端原始值
    unknown: 'Unknown status: {status}',
  },
  // 成员（用户）状态
  memberStatus: {
    active: 'Active',
    disabled: 'Disabled',
    // 未知状态降级文案，{status} 为后端原始值
    unknown: 'Unknown status: {status}',
  },
  // 成员角色
  memberRole: {
    platform_admin: 'Platform admin',
    org_admin: 'Org admin',
    org_member: 'Org member',
  },
  // RAGFlow 解析状态
  parseStatus: {
    queued: 'Queued',
    running: 'Parsing',
    completed: 'Completed',
    failed: 'Failed',
    stopped: 'Stopped',
  },
} as const
