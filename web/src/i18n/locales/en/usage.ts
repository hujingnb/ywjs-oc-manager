// usage 模块文案（en）。用量报表页面四维度 Tab 与汇总卡片文案。
export default {
  // page 级标题与副标题。
  page: {
    eyebrow: 'Usage · Report',
    heading: 'Usage Overview',
  },
  // tabs 四个用量维度 Tab 名称。
  tabs: {
    organization: 'Organization',
    member: 'Member',
    myUsage: 'My Usage',
    app: 'Instance',
    platform: 'Platform',
  },
  // filters 查询筛选区标签。
  filters: {
    org: 'Organization:',
    member: 'Member:',
    app: 'Instance:',
    selectOrg: 'Select organization',
    searchMember: 'Search member',
    searchApp: 'Search instance',
  },
  // state 加载/错误状态文案。
  state: {
    queryFailed: 'Query failed: {msg}',
    noKeyBound: 'This instance has no new-api key bound; no instance-level usage available.',
  },
  // empty 各维度空态文案。
  empty: {
    org: 'No usage records for this organization',
    member: 'No usage records',
    app: 'No usage records',
    platform: 'No platform usage records',
  },
  // summary 汇总卡片标签。
  summary: {
    totalTokens: 'Total Tokens',
    totalAmount: 'Amount',
    totalCount: 'Total Calls',
    modelCount: 'Models',
  },
  // chart 折线图区域文案。
  chart: {
    heading: 'Usage Trend',
    lastUpdated: 'Last updated: {time}',
    ariaLabel: 'Usage trend line chart',
    legendAmount: 'Amount',
  },
  // table 数据表列名。
  table: {
    amount: 'Amount',
    callCount: 'Call Count',
  },
} as const
