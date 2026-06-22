// knowledge 模块文案（zh）。企业知识库页面文案。
// 结构必须与 en/knowledge.ts 完全对齐（相同 key 路径）。
export default {
  // page 页面标题与副标题。
  page: {
    eyebrowPlatform: 'Platform · 知识库',
    eyebrowOrg: '企业 · 知识库',
    heading: '企业知识库',
  },
  // actions 操作按钮文案。
  actions: {
    ragflowInfo: 'RAGFlow 信息',
    clearFiles: '清空文件',
    uploadFiles: '上传文件',
  },
  // filters 筛选区占位符与标签。
  filters: {
    selectOrg: '选择企业',
    searchFileName: '搜索文件名称',
    allStatuses: '全部状态',
  },
  // state 加载/错误/空态文案。
  state: {
    queryFailed: '查询失败：{msg}',
    noOrg: '暂无可查看企业',
    noOrgLinked: '当前账号未关联企业',
  },
  // quota 容量展示文案。
  quota: {
    summary: '已用 {used} / 上限 {quota}，剩余 {remaining}',
  },
  // pagination 分页摘要。
  pagination: {
    totalFiles: '共 {n} 个文件',
  },
  // table 文件列表列名。
  table: {
    fileName: '文件名称',
    size: '大小',
    type: '类型',
    parseStatus: '解析状态',
  },
  // fileActions 文件行操作按钮。
  fileActions: {
    download: '下载',
    downloading: '下载中…',
    reparse: '重解析',
    reparsing: '提交中…',
  },
  // confirm 清空整库二次确认弹窗文案。
  confirm: {
    clearTitle: '确认清空企业知识库文件',
    clearMessage: '将删除当前企业知识库中的全部文件内容，企业和知识库配置会保留。该操作不可撤销。',
    clearLabel: '确认清空',
    clearVerifyValue: '清空文件',
    clearVerifyHint: '输入 "清空文件" 以确认清空',
  },
  // messages 操作结果消息。
  messages: {
    clearSuccess: '已清空企业知识库文件',
    clearFailed: '清空失败',
    downloadFailed: '下载失败',
    uploadBusy: '已有上传任务正在进行',
    deleteConfirm: '确认删除 {name} ？',
  },
} as const
