// audit 模块文案（en）。审计日志页面文案。
// 结构必须与 zh/audit.ts 完全对齐（相同 key 路径）。
export default {
  // page 页面标题与副标题。
  page: {
    eyebrowPlatform: 'Platform · Audit',
    eyebrowOrg: 'Org · Audit',
    title: 'Audit Log',
  },
  // filters 筛选区占位符与标签。
  filters: {
    selectOrg: 'Select organization',
  },
  // table 列表列名。
  table: {
    time: 'Time',
    actor: 'Actor',
    target: 'Resource',
    action: 'Action',
    detail: 'Detail',
    result: 'Result',
    deleted: 'Deleted',
  },
  // state 空态/错误文案。
  state: {
    noOrg: 'No organizations available',
    noOrgLinked: 'Current account is not linked to an organization. Cannot view audit log.',
    noPermission: 'Current account cannot view org-level audit. Check audit in your own app details.',
  },
} as const
