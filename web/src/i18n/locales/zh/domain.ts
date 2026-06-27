// domain 模块文案（zh）。覆盖 status.ts、parseStatus.ts 等 domain 层状态标签。
// label 字段从中文字面量迁移为 i18n 键后，本文件提供中文原始文案。
export default {
  // 应用（worker）生命周期状态
  appStatus: {
    draft: '待初始化',
    pulling_runtime_image: '拉取运行时镜像',
    preparing_runtime: '准备运行时配置',
    creating_container: '创建容器',
    starting: '启动容器',
    binding_waiting: '待绑定',
    binding_failed: '绑定失败',
    running: '运行中',
    restarting: '重启中',
    stopped: '已停止',
    error: '异常',
    deleted: '已删除',
    // 未知状态降级文案，{status} 为后端原始值
    unknown: '未知状态：{status}',
  },
  // Hermes Kanban 任务状态
  kanbanStatus: {
    running: '运行中',
    ready: '就绪',
    todo: '待办',
    blocked: '阻塞',
    triage: '待分诊',
    done: '已完成',
    archived: '已归档',
    // 未知状态降级文案，{status} 为后端原始值
    unknown: '未知状态：{status}',
  },
  // 组织状态
  orgStatus: {
    active: '启用',
    disabled: '禁用',
    deleted: '已删除',
    // 未知状态降级文案，{status} 为后端原始值
    unknown: '未知状态：{status}',
  },
  // 成员（用户）状态
  memberStatus: {
    active: '启用',
    disabled: '禁用',
    // 未知状态降级文案，{status} 为后端原始值
    unknown: '未知状态：{status}',
  },
  // 成员角色
  memberRole: {
    platform_admin: '平台管理员',
    org_admin: '企业管理员',
    org_member: '企业成员',
  },
  // RAGFlow 解析状态
  parseStatus: {
    queued: '等待解析',
    running: '解析中',
    completed: '已完成',
    failed: '解析失败',
    stopped: '已停止',
  },
} as const
