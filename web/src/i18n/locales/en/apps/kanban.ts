// apps/kanban 文案（en）。由 P2 迁移填充。
export default {
  // KanbanCreateModal 新建任务弹窗文案
  createModal: {
    title: 'New task',
    fieldTitle: 'Title',
    fieldTitlePlaceholder: 'Task title',
    fieldAssignee: 'assignee',
    fieldAssigneePlaceholder: 'e.g. devops, claude (lowercase slug)',
    fieldPriority: 'Priority',
    fieldBody: 'Description',
    fieldBodyPlaceholder: 'Task description',
    fieldSkills: 'skills',
    fieldSkillsPlaceholder: 'Comma-separated, e.g. bash,grep',
    fieldWorkspace: 'workspace',
    fieldWorkspacePlaceholder: 'scratch / worktree / dir:/path',
    fieldParentId: 'parent_id',
    fieldParentIdPlaceholder: 'Parent task ID (optional)',
    fieldMaxRetries: 'max_retries',
    assigneeHint: 'Lowercase letter/digit start, only a-z 0-9 _ -',
    assigneeError: 'assignee contains invalid characters: only lowercase letters, digits, underscores (_), or hyphens (-), and must start with a lowercase letter or digit',
    priorityLow: 'Low',
    priorityMid: 'Medium',
    priorityHigh: 'High',
  },
  // KanbanTaskList 任务列表文案
  taskList: {
    empty: 'No tasks',
  },
  // KanbanTaskRow 任务行文案
  taskRow: {
    justNow: 'just now',
    minutesAgo: '{n} min ago',
    hoursAgo: '{n} hr ago',
    daysAgo: '{n} d ago',
  },
  // KanbanTaskDetail 任务详情面板文案
  taskDetail: {
    selectHint: 'Select a task from the left to view details.',
    noTitle: '(no title)',
    sectionMeta: 'Meta',
    sectionBody: 'Task body',
    sectionLive: 'Live stream',
    sectionLiveSuffix: '● LIVE',
    liveEmpty: 'Waiting for events…',
    sectionRuns: 'Run history',
    runsEmpty: 'No run history.',
    runsColStatus: 'Status',
    runsColProfile: 'profile',
    runsColResult: 'Result',
    sectionComments: 'Comments ({n})',
    anonymous: 'anonymous',
  },
  // KanbanTaskActions 操作按钮文案
  taskActions: {
    complete: 'Mark complete',
    block: 'Block',
    unblock: 'Unblock',
    reclaim: 'Release claim',
    reassign: 'Reassign',
    comment: 'Comment',
    archive: 'Archive',
  },
  // AppKanbanTab top-level tab copy (toolbar / errors / action feedback)
  tab: {
    searchPlaceholder: 'Search task titles',
    stubDesc: 'This instance runs a local dev image — the task board is unavailable. Switch to a production image to enable this feature.',
    loadError: 'Load failed',
    successCreate: 'Task created',
    errorCreate: 'Create failed',
    successAction: 'Operation successful',
    errorAction: 'Operation failed',
    // NEEDS_INPUT prompt labels
    promptComment: 'Add comment',
    promptBlock: 'Blocking reason',
    promptComplete: 'Completion result (optional)',
    promptReassign: 'Reassign to (profile)',
    confirmAction: 'Execute "{verb}"?',
    // 工具栏徽标与控件
    taskCount: '{n} tasks',
    oldestReady: 'Oldest ready: {age}',
    liveLabel: '● Live',
    reconnect: 'Reconnect stream',
    createTask: '+ New task',
    // formatAge 时长单位（秒/分/时/天）
    ageSeconds: '{n}s',
    ageMinutes: '{n}min',
    ageHours: '{n}h',
    ageDays: '{n}d',
  },
} as const
