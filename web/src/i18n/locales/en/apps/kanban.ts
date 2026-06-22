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
} as const
