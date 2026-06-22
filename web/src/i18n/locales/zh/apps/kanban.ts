// apps/kanban 文案（zh）。由 P2 迁移填充。
export default {
  // KanbanCreateModal 新建任务弹窗文案
  createModal: {
    title: '新建任务',
    fieldTitle: '标题',
    fieldTitlePlaceholder: '任务标题',
    fieldAssignee: 'assignee',
    fieldAssigneePlaceholder: '如 devops、claude（小写 slug）',
    fieldPriority: '优先级',
    fieldBody: '任务描述',
    fieldBodyPlaceholder: '任务详细说明',
    fieldSkills: 'skills',
    fieldSkillsPlaceholder: '逗号分隔，如 bash,grep',
    fieldWorkspace: 'workspace',
    fieldWorkspacePlaceholder: 'scratch / worktree / dir:/路径',
    fieldParentId: 'parent_id',
    fieldParentIdPlaceholder: '父任务 ID（可选）',
    fieldMaxRetries: 'max_retries',
    assigneeHint: '小写字母/数字开头，仅含小写字母、数字、_、-',
    assigneeError: 'assignee 含非法字符：只能用小写字母、数字、下划线（_）或连字符（-），且以小写字母或数字开头',
    priorityLow: '低',
    priorityMid: '中',
    priorityHigh: '高',
  },
  // KanbanTaskList 任务列表文案
  taskList: {
    empty: '无任务',
  },
  // KanbanTaskRow 任务行文案
  taskRow: {
    justNow: '刚刚',
    minutesAgo: '{n} 分钟前',
    hoursAgo: '{n} 小时前',
    daysAgo: '{n} 天前',
  },
  // KanbanTaskDetail 任务详情面板文案
  taskDetail: {
    selectHint: '从左侧选择一个任务查看详情。',
    noTitle: '（无标题）',
    sectionMeta: '元信息',
    sectionBody: '任务 body',
    sectionLive: '实时执行流',
    sectionLiveSuffix: '● LIVE',
    liveEmpty: '等待事件…',
    sectionRuns: '历次执行',
    runsEmpty: '暂无执行记录。',
    runsColStatus: '状态',
    runsColProfile: 'profile',
    runsColResult: '结果',
    sectionComments: '评论 ({n})',
    anonymous: '匿名',
  },
  // KanbanTaskActions 操作按钮文案
  taskActions: {
    complete: '标记完成',
    block: '阻塞',
    unblock: '解除阻塞',
    reclaim: '释放 claim',
    reassign: '重新分配',
    comment: '评论',
    archive: '归档',
  },
  // AppKanbanTab 顶层 tab 文案（工具栏 / 错误 / 操作反馈）
  tab: {
    searchPlaceholder: '搜索任务标题',
    stubDesc: '该实例运行的是本地 dev 镜像，任务看板不可用；切换到生产镜像后该功能自动启用。',
    loadError: '加载失败',
    successCreate: '任务已创建',
    errorCreate: '创建失败',
    successAction: '操作成功',
    errorAction: '操作失败',
    // NEEDS_INPUT 提示文本
    promptComment: '添加评论',
    promptBlock: '阻塞原因',
    promptComplete: '完成结果（可选）',
    promptReassign: '重新分配给（profile）',
    confirmAction: '确定要执行「{verb}」吗？',
  },
} as const
