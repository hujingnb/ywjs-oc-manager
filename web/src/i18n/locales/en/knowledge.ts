// knowledge 模块文案（en）。企业知识库页面文案。
export default {
  // page 页面标题与副标题。
  page: {
    eyebrowPlatform: 'Platform · Knowledge Base',
    eyebrowOrg: 'Org · Knowledge Base',
    heading: 'Org Knowledge Base',
  },
  // actions 操作按钮文案。
  actions: {
    ragflowInfo: 'RAGFlow Info',
    clearFiles: 'Clear Files',
    uploadFiles: 'Upload Files',
  },
  // filters 筛选区占位符与标签。
  filters: {
    selectOrg: 'Select organization',
    searchFileName: 'Search file name',
    allStatuses: 'All statuses',
  },
  // state 加载/错误/空态文案。
  state: {
    queryFailed: 'Query failed: {msg}',
    noOrg: 'No organizations available',
    noOrgLinked: 'Current account is not linked to an organization',
  },
  // quota 容量展示文案。
  quota: {
    summary: 'Used {used} / Limit {quota}, Remaining {remaining}',
  },
  // pagination 分页摘要。
  pagination: {
    totalFiles: 'Total {n} files',
  },
  // table 文件列表列名。
  table: {
    fileName: 'File Name',
    size: 'Size',
    type: 'Type',
    parseStatus: 'Parse Status',
  },
  // fileActions 文件行操作按钮。
  fileActions: {
    download: 'Download',
    downloading: 'Downloading…',
    reparse: 'Reparse',
    reparsing: 'Submitting…',
  },
  // confirm 清空整库二次确认弹窗文案。
  confirm: {
    clearTitle: 'Confirm clearing org knowledge base files',
    clearMessage: 'This will delete all files in the org knowledge base. Organization and knowledge base configuration will be preserved. This action cannot be undone.',
    clearLabel: 'Confirm Clear',
    clearVerifyValue: 'Clear Files',
    clearVerifyHint: 'Enter "Clear Files" to confirm',
  },
  // messages 操作结果消息。
  messages: {
    clearSuccess: 'Org knowledge base files cleared',
    clearFailed: 'Clear failed',
    downloadFailed: 'Download failed',
    uploadBusy: 'An upload is already in progress',
    deleteConfirm: 'Delete {name}?',
    // uploadMaxMessage 是单文件上限提示，由 knowledgeUploadBatch 在文件超限时展示。
    uploadMaxMessage: 'Max file size: {label}',
    // uploadSkipMultiple 是多文件批量过滤时跳过文件的提示，其中包含上限文案。
    uploadSkipMultiple: 'Skipped {count} files exceeding the limit, max file size: {label}',
    // uploadAcceptedTypes 是上传区常驻的支持格式说明，label 为白名单类型列表。
    uploadAcceptedTypes: 'Supported formats: {label}',
    // uploadTypeRejected 是单文件类型不支持提示，label 为白名单类型列表。
    uploadTypeRejected: 'Unsupported file type, only supports: {label}',
    // uploadSkipTypeMultiple 是多文件批量过滤时跳过不支持类型文件的提示，其中包含允许的类型列表。
    uploadSkipTypeMultiple: 'Skipped {count} files of unsupported type, only supports: {label}',
  },
} as const
