// org 模块文案（zh）。组织/成员/账户余额页文案。
// 结构必须与 en/org.ts 完全对齐（相同 key 路径）。
export default {
  // console 组织控制台首页（OrgConsolePage）。
  console: {
    // tabs 图表区 Tab 名称。
    tabs: {
      usageTrend: '用量趋势',
      instanceStatus: '实例状态',
    },
    // stats 统计卡片标签。
    stats: {
      memberCount: '成员数',
      instanceCount: '实例数',
      running: '运行中',
      error: '异常',
      currentBalance: '当前余额',
      todayTokens: '今日 Token',
      realtimeNote: 'new-api 实时',
      unavailable: '不可用',
    },
    // chart 图表中的状态文案与分类标签。
    chart: {
      running: '运行中',
      stopped: '停止',
      error: '异常',
    },
    // state 图表区加载/错误/空态文案。
    state: {
      usageUnavailable: '用量服务不可用',
      instanceUnavailable: '实例数据不可用',
    },
  },
  // members 成员管理页（MembersPage）。
  members: {
    // page 页头文案。
    page: {
      eyebrowPlatform: 'Platform · 企业成员',
      eyebrowOrg: '我的企业',
    },
    // list 成员列表区标题与工具栏。
    list: {
      title: '成员列表',
      selectOrg: '选择企业',
      createAndInit: '创建并初始化',
      addMember: '新增成员',
    },
    // form 创建成员内联表单标题与字段标签。
    form: {
      createTitle: '创建成员',
      username: '用户名 *',
      displayName: '显示名 *',
      password: '初始密码 *',
      role: '角色',
      usernamePlaceholder: 'username',
      displayNamePlaceholder: '显示名称',
      passwordPlaceholder: '至少 8 位',
    },
    // createApp 补建实例表单标题与字段。
    createApp: {
      title: '为该成员创建实例',
      appName: '实例名 *',
      assistantVersion: '助手版本 *',
      assistantVersionPlaceholder: '请选择助手版本',
      submitCreate: '提交创建',
      selectVersionError: '请选择助手版本',
      createError: '创建实例失败',
      createdResult: '已创建实例 {name}，Job ID：{jobId}',
      // 补建实例时根据成员显示名自动填充的实例名默认值
      defaultAppName: '{name} 的实例',
    },
    // table 成员列表列名。
    table: {
      username: '用户名',
      displayName: '姓名',
      role: '角色',
      status: '状态',
      instance: '实例',
      noInstance: '无实例',
    },
    // actions 行操作按钮文案。
    actions: {
      disable: '禁用',
      enable: '启用',
      resetPassword: '重置密码',
      createInstance: '为该成员创建实例',
    },
    // modal 确认弹窗文案。
    modal: {
      deleteTitle: '确认删除成员',
      deleteMessage: '将禁用账号 {username} 并提交其名下实例的删除任务，操作不可撤销。',
      deleteConfirm: '确认删除',
      resetTitle: '确认重置成员密码',
      resetMessage: '将强制重置成员 {username} 的登录密码，原密码立即失效。',
      resetConfirm: '确认重置',
      resetPasswordPrompt: '输入成员 {username} 的新密码（至少 8 位）',
      resetSuccess: '已重置密码',
      resetFailed: '重置失败',
    },
    // role 角色选项文案。
    role: {
      orgMember: '企业成员',
      orgAdmin: '企业管理员',
    },
    // state 无企业等错误态文案。
    state: {
      noOrg: '暂无可查看企业',
      noOrgLinked: '当前账号未关联企业',
    },
  },
  // balance 账户余额页（OrgBalancePage）。
  balance: {
    // page 页头文案。
    page: {
      eyebrow: 'Billing · 账户余额',
      title: '账户余额',
    },
    // stats 概况卡片标签。
    stats: {
      totalRecharged: '累计充值金额',
      currentBalance: '当前剩余金额',
      queryFailed: '查询失败',
    },
    // table 充值记录列名与状态。
    table: {
      time: '时间',
      amount: '金额',
      status: '状态',
      succeeded: '成功',
      failed: '失败',
    },
    // state 加载/错误态文案。
    state: {
      loadError: '加载失败，请刷新重试',
    },
  },
  // createMember 创建成员并初始化实例页（CreateMemberPage）。
  createMember: {
    // page 页头文案。
    page: {
      eyebrowPlatform: 'Platform · 创建成员',
      eyebrowOrg: '企业 · 创建成员',
      title: '创建成员并初始化实例',
    },
    // section 表单分节标题。
    section: {
      account: '账号信息',
      instance: '实例信息',
    },
    // form 字段标签与占位符。
    form: {
      username: '用户名 *',
      displayName: '显示名 *',
      password: '初始密码 *',
      role: '角色',
      appName: '实例名 *',
      assistantVersion: '助手版本 *',
      usernamePlaceholder: 'username',
      displayNamePlaceholder: '显示名称',
      passwordPlaceholder: '至少 8 位',
      appNamePlaceholder: '实例名称',
      assistantVersionPlaceholder: '请选择助手版本',
    },
    // role 角色选项文案。
    role: {
      orgMember: '企业成员',
      orgAdmin: '企业管理员',
    },
    // actions 按钮文案。
    actions: {
      cancel: '取消',
      submitting: '提交中…',
      createAndInit: '创建并初始化',
    },
    // state 无关联企业、版本未选、提交错误等提示。
    state: {
      noOrgLinked: '当前账号未关联企业，无法创建成员。',
      selectVersionError: '请选择助手版本',
      createError: '创建失败',
    },
    // success 创建成功的反馈消息。
    success: {
      created: '已创建成员 {name} 并提交实例初始化',
    },
  },
} as const
