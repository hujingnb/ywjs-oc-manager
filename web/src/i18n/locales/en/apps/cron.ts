// apps/cron 文案（en）。由 P2 迁移填充。
export default {
  // CronJobList 左侧卡片列表文案
  list: {
    empty: 'No scheduled jobs',
    unnamed: 'Unnamed job',
    schedule: 'Schedule',
    next: 'Next',
  },
  // CronJobDetail 详情面板文案
  detail: {
    selectHint: 'Select a job from the left to view details.',
    unnamed: 'Unnamed job',
    runNow: 'Run now',
    resume: 'Resume',
    pause: 'Pause',
    sectionBasic: 'Basic fields',
    sectionAdvanced: 'Platform advanced fields',
    sectionHistory: 'Run history',
    sectionOutput: 'Output preview',
    selectOutputHint: 'Select a run entry with an output file to preview it.',
    repeatUnlimited: 'Unlimited',
  },
  // CronJobFormModal 表单弹窗文案
  form: {
    titleEdit: 'Edit scheduled job',
    titleCreate: 'New scheduled job',
    namePlaceholder: 'Job name',
    promptPlaceholder: 'Prompt sent to Hermes on trigger',
    repeatLabel: 'Max runs',
    repeatHint: 'Leave empty to run indefinitely; set N to stop after N runs',
    noAgentLabel: 'Skip AI, run script only',
    noAgentTooltip: 'When checked, the AI agent is skipped and the script runs directly (faster, no token usage). Suitable for pure-script jobs; otherwise the AI executes based on the prompt.',
    workdirPlaceholder: 'Working directory for the job',
    skillsPlaceholder: 'Comma-separated, e.g. shell,git',
    modelPlaceholder: 'Model name',
    providerPlaceholder: 'Provider name',
    baseUrlPlaceholder: 'https://provider.example/v1',
  },
  // CronRunHistory 执行历史文案
  history: {
    empty: 'No run history.',
    unknownTime: 'Unknown time',
    noOutput: 'No output file',
  },
  // DeliverField 投递渠道字段文案
  deliver: {
    noChannelHint: 'No channels bound yet. Go to the Channels page to bind one.',
  },
  // ScheduleField 调度点选器文案
  schedule: {
    modeCalendar: 'Daily / Weekly',
    modeInterval: 'Interval',
    modeExpr: 'Advanced expression',
    exprPlaceholder: 'cron 0 9 * * 1-5 or every 10m',
    freqDaily: 'Every day',
    freqWeekly: 'Every week',
    removeTime: 'Remove',
    addTime: '+ Add time',
    intervalPrefix: 'Every',
    previewLabel: 'Actual runs: ',
    previewWarnNote: '(Inconsistent minutes — all of the above trigger points will fire)',
    generatedLabel: 'Generates: ',
    // 周几选项标签（cron dow 顺序：1=Mon…6=Sat，0=Sun）
    weekdays: {
      mon: 'Mon',
      tue: 'Tue',
      wed: 'Wed',
      thu: 'Thu',
      fri: 'Fri',
      sat: 'Sat',
      sun: 'Sun',
    },
    // 间隔单位选项
    units: {
      minute: 'minute(s)',
      hour: 'hour(s)',
    },
  },
  // WorkspaceFilePicker 工作目录文件选择器文案
  picker: {
    scriptPlaceholder: 'Script file name in the workspace',
    selectFile: 'Pick file',
  },
  // display.* — human-readable output of cronDisplay / scheduleExpr / deliverOptions helpers
  display: {
    // 状态标签
    state: {
      scheduled: 'Scheduled',
      paused: 'Paused',
      running: 'Running',
      disabled: 'Disabled',
      error: 'Error',
      removed: 'Removed',
      unknown: 'Unknown',
    },
    // 投递渠道标签
    deliver: {
      wechat: 'WeChat',
      email: 'Email',
      none: 'No delivery',
      empty: '—',
    },
    // 周几标签（cron dow 顺序：0=Sun）
    weekday: {
      sun: 'Sun',
      mon: 'Mon',
      tue: 'Tue',
      wed: 'Wed',
      thu: 'Thu',
      fri: 'Fri',
      sat: 'Sat',
    },
    // 调度表达式模板
    schedule: {
      // every / interval 格式：每 N 单位
      everyHour: 'Every {value} hour(s)',
      everyMinute: 'Every {value} minute(s)',
      everySecond: 'Every {value} second(s)',
      // at 格式：一次性绝对时间
      atTime: 'At {time}',
      // cron 每小时
      everyHourFixed: 'Every hour',
      // cron 每天
      everyDay: 'Daily at {time}',
      // cron 每周某天
      everyWeekday: 'Weekly on {day} at {time}',
      // cron 每月某日
      everyMonthDay: 'Monthly on the {day} at {time}',
      // cron 每 N 分钟
      everyNMinutes: 'Every {n} minute(s)',
      // calendar 模式：每天（多时刻）
      calendarDaily: 'Daily at {times}',
      // calendar 模式：每周（星期列表 + 多时刻）
      calendarWeekly: '{days} at {times}',
      // calendar 模式：间隔
      calendarInterval: 'Every {value} {unit}',
      // 间隔单位
      unitHour: 'hour(s)',
      unitMinute: 'minute(s)',
    },
    // 不投递常驻选项标签
    deliverNone: 'No delivery',
  },
  // AppCronTab top-level tab copy (toolbar / status options / action feedback)
  tab: {
    searchPlaceholder: 'Search jobs',
    refresh: 'Refresh',
    createJob: '+ New job',
    statusAll: 'All statuses',
    statusScheduled: 'Scheduled',
    statusPaused: 'Paused',
    statusRunning: 'Running',
    statusDisabled: 'Disabled',
    statusError: 'Error',
    stubDesc: 'This instance runs a local dev image — scheduled jobs are unavailable. Switch to a production image to enable this feature.',
    loadError: 'Load failed',
    successUpdate: 'Scheduled job updated',
    successCreate: 'Scheduled job created',
    errorSave: 'Save failed',
    confirmDelete: 'Delete this scheduled job?',
    successAction: 'Operation successful',
    errorAction: 'Operation failed',
  },
} as const
