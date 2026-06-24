// apps/root 文案（zh）。由 P2 迁移填充。
// 对应 en/apps/root.ts，结构必须与 en 完全对齐（相同 key 路径）。
export default {
  // list：应用列表页文案
  list: {
    title: '实例列表',
    eyebrowAdmin: 'Platform · Instances',
    eyebrowOrg: '企业 · Instances',
    orgFilter: '选择企业',
    createMember: '创建成员并初始化',
    noOrgsAdmin: '暂无可查看企业',
    noOrgsUser: '当前账号未关联企业',
    // 状态列：版本未同步提示
    restartNeeded: '需重启',
    restartNeededTip: '版本已更新，需重启生效',
    // 操作列
    colName: '名称',
    colStatus: '状态',
    colApiKey: 'API key',
    actionRestart: '重启',
    actionStop: '停止',
    actionDelete: '删除',
    // 删除确认弹窗
    deleteTitle: '确认删除实例',
    deleteMessage: '将提交删除任务，实例 "{name}" 关联的容器和 API key 都会被回收。是否继续？',
    deleteConfirm: '确认删除',
    deleteVerifyHint: '输入实例名 "{name}" 以确认删除',
  },
  // detail：实例详情页头部（AppDetailPage）
  detail: {
    title: '实例详情',
    uuid: '实例 UUID：',
    loading: '加载中…',
    loadError: '查询失败：',
    // 详情页 tab 标签
    tabs: {
      overview: '概览',
      kanban: '任务',
      cron: '定时任务',
      runtime: '运行时',
      channels: '渠道',
      knowledge: '实例知识库',
      skills: '技能',
      workspace: '工作目录',
      audit: '审计',
      // conversations tab：实例 hermes 会话对话管理
      conversations: '对话',
    },
  },
  // overview：概览 tab（AppOverviewTab）
  overview: {
    heading: '概览',
    noApp: '尚未加载实例信息',
    // 重新初始化按钮
    retryInit: '重新初始化',
    retryInitPending: '提交中…',
    // 描述项标签
    labelStatus: '状态',
    labelApiKey: 'API key',
    labelVersion: '助手版本',
    labelOrg: '所属企业',
    labelDesc: '描述',
    labelImageRef: '镜像引用',
    labelImageDigest: '镜像 Digest',
    // API key 标签
    apiKeyActive: '启用',
    apiKeyDisabled: '已禁用',
    apiKeyRestore: '恢复',
    // 版本相关
    restartNeeded: '需重启',
    switchVersion: '切换',
    restartNow: '立即重启',
    restartNowPending: '提交中…',
    unknownOrg: '未知企业',
    // 版本切换弹窗
    switchVersionTitle: '切换助手版本',
    switchVersionPlaceholder: '请选择助手版本',
    switchVersionConfirm: '确认切换',
    // 反馈文案
    switchVersionSuccess: '已切换助手版本，重启实例后生效',
    switchVersionError: '切换版本失败',
    restartJobTitle: '重启任务',
    restartSubmitted: '已提交重启任务：',
    restartError: '重启失败',
    initJobTitle: '初始化任务',
    initSubmitted: '已提交初始化任务：',
    initError: '初始化失败',
    // error 状态失败阶段文案
    errorStageFmt: '在「{stage}」阶段失败',
    // key 任务
    keyJobTitle: '恢复 API key 任务',
    keyRestoreError: 'restore 失败',
    keyRestoreSubmitted: '已提交 restore 任务：{jobId}',
    // 实例语言：实时展示实例当前运行语言、未运行态及需重启提示
    language: {
      label: '实例语言',
      notRunning: '实例未运行',
      needsRestart: '切换语言后需重启生效为 {lang}',
      restart: '重启应用',
    },
  },
  // channels：渠道绑定 tab（AppChannelsTab）
  channels: {
    heading: '渠道绑定',
    noApp: '请选择目标实例',
    ariaList: '渠道列表',
    ariaDetail: '微信渠道详情',
    // 主操作按钮文案
    beginLogin: '发起登录',
    regenQr: '重新生成二维码',
    relogin: '重新登录',
    triggering: '触发中…',
    refreshQr: '刷新二维码',
    refreshQrPending: '生成中…',
    unbind: '解绑',
    // 状态提示
    boundIdentity: '已绑定：',
    errorMsg: '最近错误：',
    waitingQr: '正在生成登录二维码…',
    qrExpired: '当前二维码已过期，请点击"刷新二维码"重新生成。',
    // 渠道卡片：名称 / 描述 / 支持标签
    channelWechat: '微信',
    channelWechatDesc: '扫码绑定后接收助手消息',
    channelWorkWechat: '企业微信',
    channelWorkWechatDesc: '企业内部协作场景',
    channelFeishu: '飞书',
    channelFeishuDesc: '团队消息与工作台场景',
    channelDingtalk: '钉钉',
    channelDingtalkDesc: '企业通讯与审批场景',
    channelTelegram: 'Telegram',
    channelTelegramDesc: '海外即时通讯与 Bot 接入场景',
    channelWhatsapp: 'WhatsApp',
    channelWhatsappDesc: '海外用户触达与客服场景',
    channelDiscord: 'Discord',
    channelDiscordDesc: '社区与游戏玩家场景',
    channelSlack: 'Slack',
    channelSlackDesc: '团队协作与工作流场景',
    channelLine: 'Line',
    channelLineDesc: '日本与东南亚用户场景',
    supported: '已支持',
    unsupported: '暂不支持',
    // status 是渠道绑定进度状态的用户可读文案，由 formatChannelStatus 解析为 i18n key 后消费。
    status: {
      unbound: '未绑定',
      pending_auth: '等待扫码授权',
      bound: '已绑定',
      failed: '绑定失败',
      expired: '二维码已过期',
      unbound_by_user: '已解绑',
      deleted: '已删除',
      not_started: '未发起',
      unknown: '未知状态：{status}',
    },
  },
  // runtime：运行时 tab（AppRuntimeTab）
  runtime: {
    heading: '运行时',
    start: '启动',
    stop: '停止',
    restart: '重启',
    delete: '删除',
    loadError: '查询失败：',
    currentStatus: '当前状态：',
    noContainer: '尚未创建容器',
    latestSnapshot: '最新采样：',
    snapshotError: '｜ 采样错误：',
    noSnapshot: '资源指标尚未采集（首次采集需 30s 内完成）。',
    recentOp: '最近运行操作',
    // 删除确认
    deleteTitle: '确认删除实例',
    deleteMessage: '将提交删除任务，实例容器、API key 和工作目录都会被回收。该操作不可撤销。',
    deleteConfirm: '确认删除',
    deleteVerifyHint: '输入实例名 "{name}" 以确认删除',
    // 停止确认
    stopTitle: '确认停止容器',
    stopMessage: '停止后 Hermes 容器对话立即中断；可在恢复时重新启动。',
    stopConfirm: '确认停止',
    stopVerifyHint: '输入实例名 "{name}" 以确认停止运行',
    // 运行时操作反馈（异步任务提交成功 / 操作失败）
    opSubmitted: '已提交 {op}：{jobId}',
    opFailed: '{op} 操作失败',
  },
  // workspace：工作目录 tab（AppWorkspaceTab）
  workspace: {
    heading: '工作目录',
    download: '下载归档',
    searchPlaceholder: '搜索文件（递归整个工作目录）',
    currentPath: '当前路径：',
    goUp: '返回上级',
    searchResults: '搜索「{keyword}」：{count} 个结果',
    noApp: '请选择目标实例',
    queryError: '查询失败：',
    colName: '文件名称',
    colSize: '大小',
    colCreatedAt: '创建时间',
    colActions: '操作',
    actionDownload: '下载',
  },
  // knowledge：实例知识库 tab（AppKnowledgeTab）
  knowledge: {
    heading: '实例知识库',
    ragflowInfo: 'RAGFlow 信息',
    editQuota: '编辑空间',
    upload: '上传文件',
    searchPlaceholder: '搜索文件名称',
    statusAll: '全部状态',
    noApp: '尚未加载实例信息',
    queryError: '查询失败：',
    quotaTitle: '编辑实例知识库空间',
    quotaLabel: '空间大小 (GB)',
    quotaError: '更新空间失败',
    // 文件列表列头
    colName: '文件名称',
    colSize: '大小',
    colType: '类型',
    colParseStatus: '解析状态',
    colCreatedAt: '创建时间',
    colActions: '操作',
    // 操作按钮
    actionDownload: '下载',
    actionDownloading: '下载中…',
    actionReparse: '重解析',
    actionReparsePending: '提交中…',
    actionDelete: '删除',
    // 配额摘要
    quotaSummary: '已用 {used} / 上限 {quota}，剩余 {remaining}',
    // 文件总数分页前缀
    fileCountPrefix: '共 {count} 个文件',
    // 错误兜底
    downloadError: '下载失败',
    deleteError: '删除失败',
    reparseError: '重解析失败',
    uploadError: '已有上传任务正在进行',
  },
  // audit：审计 tab（AppAuditTab）
  audit: {
    title: '实例审计',
    colTime: '时间',
    colActor: '操作者',
    colAction: '操作',
    colDetail: '详情',
    colResult: '结果',
    system: '系统',
    deleted: '已删除',
    noPermission: '当前账号无权查看该实例审计。',
  },
  // empty：无实例空状态页（AppEmptyPage）
  empty: {
    descAdmin: '你还没有属于自己的实例',
    descMember: '请联系管理员创建实例',
    createButton: '去成员页创建实例',
  },
} as const
