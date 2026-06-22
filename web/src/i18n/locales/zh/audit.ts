// audit 模块文案（zh）。审计日志页面文案。
// 结构必须与 en/audit.ts 完全对齐（相同 key 路径）。
export default {
  // page 页面标题与副标题。
  page: {
    eyebrowPlatform: 'Platform · 审计',
    eyebrowOrg: '企业 · 审计',
    title: '审计日志',
  },
  // filters 筛选区占位符与标签。
  filters: {
    selectOrg: '选择企业',
  },
  // table 列表列名。
  table: {
    time: '时间',
    actor: '操作者',
    target: '资源',
    action: '操作',
    detail: '详情',
    result: '结果',
    deleted: '已删除',
  },
  // state 空态/错误文案。
  state: {
    noOrg: '暂无可查看企业',
    noOrgLinked: '当前账号未关联企业，无法查看审计日志。',
    noPermission: '当前账号无权查看企业级审计，请在自己的实例详情中查看实例审计。',
  },
} as const
