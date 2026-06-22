// apps/cron 文案（zh）。由 P2 迁移填充。
export default {
  // CronJobList 左侧卡片列表文案
  list: {
    empty: '暂无定时任务',
    unnamed: '未命名任务',
    schedule: '调度',
    next: '下次',
  },
  // CronJobDetail 详情面板文案
  detail: {
    selectHint: '从左侧选择一个定时任务查看详情。',
    unnamed: '未命名任务',
    runNow: '立即运行',
    resume: '恢复',
    pause: '暂停',
    sectionBasic: '基础字段',
    sectionAdvanced: '平台高级字段',
    sectionHistory: '执行历史',
    sectionOutput: '输出预览',
    selectOutputHint: '选择一条有输出文件的执行记录查看内容。',
    repeatUnlimited: '不限',
  },
  // CronJobFormModal 表单弹窗文案
  form: {
    titleEdit: '编辑定时任务',
    titleCreate: '新建定时任务',
    namePlaceholder: '任务名称',
    promptPlaceholder: '触发时交给 Hermes 的提示词',
    repeatLabel: '运行次数上限',
    repeatHint: '留空 = 一直按计划运行；填 N = 运行 N 次后停止',
    noAgentLabel: '不使用 AI，仅运行脚本',
    noAgentTooltip: '勾选后跳过 AI agent，直接执行 script 指定脚本（更快、不消耗 token），适合纯脚本任务；不勾选则由 AI 按 prompt 执行。',
    workdirPlaceholder: '任务运行目录',
    skillsPlaceholder: '逗号分隔，如 shell,git',
    modelPlaceholder: '模型名称',
    providerPlaceholder: 'provider 名称',
    baseUrlPlaceholder: 'https://provider.example/v1',
  },
  // CronRunHistory 执行历史文案
  history: {
    empty: '暂无执行历史。',
    unknownTime: '未知时间',
    noOutput: '无输出文件',
  },
  // DeliverField 投递渠道字段文案
  deliver: {
    noChannelHint: '暂无已绑定渠道，去「渠道」页绑定后可在此选择。',
  },
  // ScheduleField 调度点选器文案
  schedule: {
    modeCalendar: '按天/周',
    modeInterval: '按间隔',
    modeExpr: '高级表达式',
    freqDaily: '每天',
    freqWeekly: '每周',
    removeTime: '删除',
    addTime: '+ 添加时间',
    intervalPrefix: '每',
    previewLabel: '实际运行：',
    previewWarnNote: '（时间点分钟不一致，将产生上述全部触发点）',
    generatedLabel: '将生成：',
    // 周几选项标签（cron dow 顺序：1=一…6=六，0=日）
    weekdays: {
      mon: '一',
      tue: '二',
      wed: '三',
      thu: '四',
      fri: '五',
      sat: '六',
      sun: '日',
    },
    // 间隔单位选项
    units: {
      minute: '分钟',
      hour: '小时',
    },
  },
  // WorkspaceFilePicker 工作目录文件选择器文案
  picker: {
    scriptPlaceholder: '仓库内脚本文件名',
    selectFile: '选择文件',
  },
  // display.* — cronDisplay / scheduleExpr / deliverOptions 辅助函数输出的人类可读文案
  display: {
    // 状态标签
    state: {
      scheduled: '已调度',
      paused: '已暂停',
      running: '运行中',
      disabled: '已禁用',
      error: '错误',
      removed: '已移除',
      unknown: 'unknown',
    },
    // 投递渠道标签
    deliver: {
      wechat: '微信',
      email: '邮件',
      none: '不投递',
      empty: '—',
    },
    // 周几标签（cron dow 顺序：0=日）
    weekday: {
      sun: '周日',
      mon: '周一',
      tue: '周二',
      wed: '周三',
      thu: '周四',
      fri: '周五',
      sat: '周六',
    },
    // 调度表达式模板
    schedule: {
      // every / interval 格式：每 N 单位
      everyHour: '每 {value} 小时',
      everyMinute: '每 {value} 分钟',
      everySecond: '每 {value} 秒',
      // at 格式：一次性绝对时间
      atTime: '指定时间 {time}',
      // cron 每小时
      everyHourFixed: '每小时',
      // cron 每天
      everyDay: '每天 {time}',
      // cron 每周某天
      everyWeekday: '每{day} {time}',
      // cron 每月某日
      everyMonthDay: '每月{day}日 {time}',
      // cron 每 N 分钟
      everyNMinutes: '每 {n} 分钟',
      // calendar 模式：每天（多时刻）
      calendarDaily: '每天 {times}',
      // calendar 模式：每周（星期列表 + 多时刻）
      calendarWeekly: '{days} {times}',
      // calendar 模式：间隔
      calendarInterval: '每 {value} {unit}',
      // 间隔单位
      unitHour: '小时',
      unitMinute: '分钟',
    },
    // 不投递常驻选项标签
    deliverNone: '不投递',
  },
  // AppCronTab 顶层 tab 文案（工具栏 / 状态选项 / 操作反馈）
  tab: {
    searchPlaceholder: '搜索定时任务',
    refresh: '刷新',
    createJob: '+ 新建任务',
    statusAll: '全部状态',
    statusScheduled: '已调度',
    statusPaused: '已暂停',
    statusRunning: '运行中',
    statusDisabled: '已禁用',
    statusError: '错误',
    stubDesc: '该实例运行的是本地 dev 镜像，定时任务不可用；切换到生产镜像后该功能自动启用。',
    loadError: '加载失败',
    successUpdate: '定时任务已更新',
    successCreate: '定时任务已创建',
    errorSave: '保存失败',
    confirmDelete: '确定要删除这个定时任务吗？',
    successAction: '操作成功',
    errorAction: '操作失败',
  },
} as const
