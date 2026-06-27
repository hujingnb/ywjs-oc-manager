// apps/root locale (en). Filled by P2 i18n migration.
// Structure must align exactly with zh/apps/root.ts.
export default {
  // list: app list page
  list: {
    title: 'Instance list',
    eyebrowAdmin: 'Platform · Instances',
    eyebrowOrg: 'Org · Instances',
    orgFilter: 'Select organization',
    createMember: 'Create member & init',
    noOrgsAdmin: 'No organizations available',
    noOrgsUser: 'Account not associated with any organization',
    // Status column: version-out-of-sync hint
    restartNeeded: 'Restart needed',
    restartNeededTip: 'Version updated, restart to apply',
    // Action column
    colName: 'Name',
    colStatus: 'Status',
    colApiKey: 'API key',
    actionRestart: 'Restart',
    actionStop: 'Stop',
    actionDelete: 'Delete',
    // Delete confirm modal
    deleteTitle: 'Confirm instance deletion',
    deleteMessage: 'A delete job will be submitted. The container and API key for "{name}" will be reclaimed. Continue?',
    deleteConfirm: 'Confirm delete',
    deleteVerifyHint: 'Type the instance name "{name}" to confirm deletion',
  },
  // detail: instance detail page header (AppDetailPage)
  detail: {
    title: 'Instance detail',
    uuid: 'Instance UUID: ',
    loading: 'Loading…',
    loadError: 'Load failed: ',
    // Tab labels
    tabs: {
      overview: 'Overview',
      kanban: 'Tasks',
      cron: 'Scheduled jobs',
      runtime: 'Runtime',
      channels: 'Channels',
      knowledge: 'Knowledge base',
      skills: 'Skills',
      workspace: 'Workspace',
      audit: 'Audit',
      // conversations tab: instance hermes conversation management
      conversations: 'Conversations',
    },
  },
  // overview: Overview tab (AppOverviewTab)
  overview: {
    heading: 'Overview',
    noApp: 'Instance not loaded yet',
    // Retry init button
    retryInit: 'Re-initialize',
    retryInitPending: 'Submitting…',
    // Description item labels
    labelStatus: 'Status',
    labelApiKey: 'API key',
    labelVersion: 'Assistant version',
    labelOrg: 'Organization',
    labelDesc: 'Description',
    labelImageRef: 'Image ref',
    labelImageDigest: 'Image digest',
    // API key tags
    apiKeyActive: 'Active',
    apiKeyDisabled: 'Disabled',
    apiKeyRestore: 'Restore',
    // Version fields
    restartNeeded: 'Restart needed',
    switchVersion: 'Switch',
    restartNow: 'Restart now',
    restartNowPending: 'Submitting…',
    unknownOrg: 'Unknown organization',
    // Switch version modal
    switchVersionTitle: 'Switch assistant version',
    switchVersionPlaceholder: 'Select a version',
    switchVersionConfirm: 'Confirm switch',
    // Feedback messages
    switchVersionSuccess: 'Version switched. Restart the instance for it to take effect.',
    switchVersionError: 'Failed to switch version',
    restartJobTitle: 'Restart job',
    restartSubmitted: 'Restart job submitted: ',
    restartError: 'Restart failed',
    initJobTitle: 'Init job',
    initSubmitted: 'Init job submitted: ',
    initError: 'Initialization failed',
    // Error stage hint
    errorStageFmt: 'Failed at stage "{stage}"',
    // Key job
    keyJobTitle: 'Restore API key job',
    keyRestoreError: 'Restore failed',
    keyRestoreSubmitted: 'Restore job submitted: {jobId}',
    // Instance language: live current runtime language, not-running state and restart hint
    language: {
      label: 'Instance language',
      notRunning: 'Instance not running',
      needsRestart: 'Restart required to apply {lang}',
      restart: 'Restart to apply',
    },
  },
  // channels: Channels tab (AppChannelsTab)
  channels: {
    heading: 'Channel binding',
    noApp: 'Select a target instance',
    ariaList: 'Channel list',
    ariaDetail: 'WeChat channel detail',
    // Primary action button labels
    beginLogin: 'Begin login',
    regenQr: 'Regenerate QR code',
    relogin: 'Re-login',
    triggering: 'Triggering…',
    refreshQr: 'Refresh QR code',
    refreshQrPending: 'Generating…',
    unbind: 'Unbind',
    // Status hints
    boundIdentity: 'Bound: ',
    errorMsg: 'Last error: ',
    waitingQr: 'Generating login QR code…',
    qrExpired: 'The QR code has expired. Click "Refresh QR code" to regenerate.',
    instanceNotReady: 'Instance is restarting or upgrading, please retry shortly…',
    // Channel card: name / description / support label
    channelWechat: 'WeChat',
    channelWechatDesc: 'Scan to bind and receive assistant messages',
    channelWorkWechat: 'WeCom',
    channelWorkWechatDesc: 'Internal enterprise collaboration',
    channelFeishu: 'Feishu / Lark',
    channelFeishuDesc: 'Team messaging and workspace',
    channelDingtalk: 'DingTalk',
    channelDingtalkDesc: 'Enterprise communication and approval',
    channelTelegram: 'Telegram',
    channelTelegramDesc: 'Overseas IM and Bot integration',
    channelWhatsapp: 'WhatsApp',
    channelWhatsappDesc: 'Overseas user reach and customer service',
    channelDiscord: 'Discord',
    channelDiscordDesc: 'Community and gaming',
    channelSlack: 'Slack',
    channelSlackDesc: 'Team collaboration and workflows',
    channelLine: 'Line',
    channelLineDesc: 'Japan and Southeast Asia users',
    supported: 'Supported',
    unsupported: 'Not supported',
    // 飞书渠道扫码自动创建接入专用文案。
    feishuDomainLabel: 'Domain',
    feishuDomainFeishu: 'Feishu (China)',
    feishuDomainLark: 'Lark (Global)',
    feishuBotName: 'Bot name: ',
    feishuDomainCurrent: 'Domain: ',
    feishuGeneratingQr: 'Generating QR code…',
    feishuConnecting: 'Scanned, verifying and connecting…',
    // status 是渠道绑定进度状态的用户可读文案，由 formatChannelStatus 解析为 i18n key 后消费。
    status: {
      unbound: 'Not bound',
      pending_auth: 'Waiting for QR code scan',
      bound: 'Bound',
      failed: 'Binding failed',
      expired: 'QR code expired',
      unbound_by_user: 'Unbound',
      deleted: 'Deleted',
      not_started: 'Not started',
      unknown: 'Unknown status: {status}',
    },
  },
  // runtime: Runtime tab (AppRuntimeTab)
  runtime: {
    heading: 'Runtime',
    start: 'Start',
    stop: 'Stop',
    restart: 'Restart',
    delete: 'Delete',
    loadError: 'Load failed: ',
    currentStatus: 'Current status: ',
    noContainer: 'No container created yet',
    latestSnapshot: 'Latest snapshot: ',
    snapshotError: '｜ Snapshot error: ',
    noSnapshot: 'Resource metrics not yet collected (first collection within 30s).',
    recentOp: 'Recent runtime operation',
    // Delete confirm
    deleteTitle: 'Confirm instance deletion',
    deleteMessage: 'A delete job will be submitted. The container, API key, and workspace for this instance will be reclaimed. This action cannot be undone.',
    deleteConfirm: 'Confirm delete',
    deleteVerifyHint: 'Type the instance name "{name}" to confirm deletion',
    // Stop confirm
    stopTitle: 'Confirm stop container',
    stopMessage: 'Stopping will immediately interrupt Hermes container conversations; you can restart later.',
    stopConfirm: 'Confirm stop',
    stopVerifyHint: 'Type the instance name "{name}" to confirm stopping',
    // 运行时操作反馈（异步任务提交成功 / 操作失败）
    opSubmitted: '{op} submitted: {jobId}',
    opFailed: '{op} failed',
  },
  // workspace: Workspace tab (AppWorkspaceTab)
  workspace: {
    heading: 'Workspace',
    download: 'Download archive',
    searchPlaceholder: 'Search files (recursive)',
    currentPath: 'Current path: ',
    goUp: 'Go up',
    searchResults: 'Results for "{keyword}": {count}',
    noApp: 'Select a target instance',
    queryError: 'Load failed: ',
    colName: 'File name',
    colSize: 'Size',
    colCreatedAt: 'Created at',
    colActions: 'Actions',
    actionDownload: 'Download',
  },
  // knowledge: Knowledge base tab (AppKnowledgeTab)
  knowledge: {
    heading: 'Knowledge base',
    ragflowInfo: 'RAGFlow info',
    editQuota: 'Edit quota',
    upload: 'Upload files',
    searchPlaceholder: 'Search file name',
    statusAll: 'All statuses',
    noApp: 'Instance not loaded yet',
    queryError: 'Load failed: ',
    quotaTitle: 'Edit knowledge base quota',
    quotaLabel: 'Quota (GB)',
    quotaError: 'Failed to update quota',
    // File list columns
    colName: 'File name',
    colSize: 'Size',
    colType: 'Type',
    colParseStatus: 'Parse status',
    colCreatedAt: 'Created at',
    colActions: 'Actions',
    // Action buttons
    actionDownload: 'Download',
    actionDownloading: 'Downloading…',
    actionReparse: 'Reparse',
    actionReparsePending: 'Submitting…',
    actionDelete: 'Delete',
    // Quota summary
    quotaSummary: 'Used {used} / Quota {quota}, Remaining {remaining}',
    // Pagination prefix
    fileCountPrefix: '{count} files total',
    // Error fallbacks
    downloadError: 'Download failed',
    deleteError: 'Delete failed',
    reparseError: 'Reparse failed',
    uploadError: 'Another upload task is already running',
  },
  // audit: Audit tab (AppAuditTab)
  audit: {
    title: 'Instance audit',
    colTime: 'Time',
    colActor: 'Actor',
    colAction: 'Action',
    colDetail: 'Detail',
    colResult: 'Result',
    system: 'System',
    deleted: 'Deleted',
    noPermission: 'You do not have permission to view audit logs for this instance.',
  },
  // empty: No-app empty page (AppEmptyPage)
  empty: {
    descAdmin: 'You have no instance yet',
    descMember: 'Please contact your administrator to create an instance',
    createButton: 'Go to members page to create one',
  },
} as const
