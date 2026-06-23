// usage 模块文案（zh）。用量报表页面四维度 Tab 与汇总卡片文案。
// 结构必须与 en/usage.ts 完全对齐（相同 key 路径）。
export default {
  // page 级标题与副标题。
  page: {
    eyebrow: 'Usage · 用量报表',
    heading: '用量四维度',
  },
  // tabs 四个用量维度 Tab 名称。
  tabs: {
    organization: '企业',
    member: '成员',
    myUsage: '我的用量',
    app: '实例',
    platform: '平台',
  },
  // filters 查询筛选区标签。
  filters: {
    org: '企业：',
    member: '成员：',
    app: '实例：',
    selectOrg: '选择企业',
    searchMember: '搜索成员',
    searchApp: '搜索实例',
  },
  // state 加载/错误状态文案。
  state: {
    queryFailed: '查询失败：{msg}',
    noKeyBound: '该实例尚未绑定 new-api key，暂无实例维度用量。',
  },
  // empty 各维度空态文案。
  empty: {
    org: '该企业暂无实例用量记录',
    member: '暂无实例用量记录',
    app: '暂无实例用量记录',
    platform: '暂无平台用量记录',
  },
  // summary 汇总卡片标签。
  summary: {
    totalTokens: 'Token 总量',
    totalAmount: '金额',
    totalCount: '使用总量',
    modelCount: '模型数',
  },
  // chart 折线图区域文案。
  chart: {
    heading: '用量趋势',
    lastUpdated: '最近更新：{time}',
    ariaLabel: '用量趋势折线图',
    legendAmount: '金额',
  },
  // table 数据表列名。
  table: {
    amount: '金额',
    callCount: '使用次数',
  },
} as const
