// org 模块文案（en）。组织/成员/账户余额页文案。
// 结构必须与 zh/org.ts 完全对齐（相同 key 路径）。
export default {
  // console 组织控制台首页（OrgConsolePage）。
  console: {
    // tabs 图表区 Tab 名称。
    tabs: {
      usageTrend: 'Usage Trend',
      instanceStatus: 'Instance Status',
    },
    // stats 统计卡片标签。
    stats: {
      memberCount: 'Members',
      instanceCount: 'Instances',
      running: 'Running',
      error: 'Error',
      currentBalance: 'Balance',
      todayTokens: 'Tokens Today',
      realtimeNote: 'new-api realtime',
      unavailable: 'Unavailable',
    },
    // chart 图表中的状态文案与分类标签。
    chart: {
      running: 'Running',
      stopped: 'Stopped',
      error: 'Error',
    },
    // state 图表区加载/错误/空态文案。
    state: {
      usageUnavailable: 'Usage service unavailable',
      instanceUnavailable: 'Instance data unavailable',
    },
  },
  // members 成员管理页（MembersPage）。
  members: {
    // page 页头文案。
    page: {
      eyebrowPlatform: 'Platform · Members',
      eyebrowOrg: 'My Organization',
    },
    // list 成员列表区标题与工具栏。
    list: {
      title: 'Member List',
      selectOrg: 'Select organization',
      createAndInit: 'Create & Initialize',
      addMember: 'Add Member',
    },
    // form 创建成员内联表单标题与字段标签。
    form: {
      createTitle: 'Create Member',
      username: 'Username *',
      displayName: 'Display Name *',
      password: 'Initial Password *',
      role: 'Role',
      usernamePlaceholder: 'username',
      displayNamePlaceholder: 'Display name',
      passwordPlaceholder: 'At least 8 characters',
    },
    // createApp 补建实例表单标题与字段。
    createApp: {
      title: 'Create Instance for This Member',
      appName: 'Instance Name *',
      assistantVersion: 'Assistant Version *',
      assistantVersionPlaceholder: 'Select assistant version',
      submitCreate: 'Submit',
      selectVersionError: 'Please select an assistant version',
      createError: 'Failed to create instance',
      createdResult: 'Instance {name} created, Job ID: {jobId}',
    },
    // table 成员列表列名。
    table: {
      username: 'Username',
      displayName: 'Name',
      role: 'Role',
      status: 'Status',
      instance: 'Instance',
      noInstance: 'No Instance',
    },
    // actions 行操作按钮文案。
    actions: {
      disable: 'Disable',
      enable: 'Enable',
      resetPassword: 'Reset Password',
      createInstance: 'Create Instance',
    },
    // modal 确认弹窗文案。
    modal: {
      deleteTitle: 'Confirm Delete Member',
      deleteMessage: 'This will disable the account {username} and submit deletion tasks for all their instances. This action cannot be undone.',
      deleteConfirm: 'Confirm Delete',
      resetTitle: 'Confirm Reset Member Password',
      resetMessage: 'This will forcibly reset the login password for member {username}. The original password will be immediately invalidated.',
      resetConfirm: 'Confirm Reset',
      resetPasswordPrompt: 'Enter new password for member {username} (at least 8 characters)',
      resetSuccess: 'Password reset successfully',
      resetFailed: 'Reset failed',
    },
    // role 角色选项文案。
    role: {
      orgMember: 'Member',
      orgAdmin: 'Admin',
    },
    // state 无企业等错误态文案。
    state: {
      noOrg: 'No organizations available',
      noOrgLinked: 'Current account is not linked to an organization',
    },
  },
  // balance 账户余额页（OrgBalancePage）。
  balance: {
    // page 页头文案。
    page: {
      eyebrow: 'Billing · Account Balance',
      title: 'Account Balance',
    },
    // stats 概况卡片标签。
    stats: {
      totalRecharged: 'Total Recharged',
      currentBalance: 'Current Balance',
      queryFailed: 'Query failed',
    },
    // table 充值记录列名与状态。
    table: {
      time: 'Time',
      amount: 'Amount',
      status: 'Status',
      succeeded: 'Success',
      failed: 'Failed',
    },
    // state 加载/错误态文案。
    state: {
      loadError: 'Load failed, please refresh and try again',
    },
  },
  // createMember 创建成员并初始化实例页（CreateMemberPage）。
  createMember: {
    // page 页头文案。
    page: {
      eyebrowPlatform: 'Platform · Create Member',
      eyebrowOrg: 'Organization · Create Member',
      title: 'Create Member & Initialize Instance',
    },
    // section 表单分节标题。
    section: {
      account: 'Account Info',
      instance: 'Instance Info',
    },
    // form 字段标签与占位符。
    form: {
      username: 'Username *',
      displayName: 'Display Name *',
      password: 'Initial Password *',
      role: 'Role',
      appName: 'Instance Name *',
      assistantVersion: 'Assistant Version *',
      usernamePlaceholder: 'username',
      displayNamePlaceholder: 'Display name',
      passwordPlaceholder: 'At least 8 characters',
      appNamePlaceholder: 'Instance name',
      assistantVersionPlaceholder: 'Select assistant version',
    },
    // role 角色选项文案。
    role: {
      orgMember: 'Member',
      orgAdmin: 'Admin',
    },
    // actions 按钮文案。
    actions: {
      cancel: 'Cancel',
      submitting: 'Submitting…',
      createAndInit: 'Create & Initialize',
    },
    // state 无关联企业、版本未选、提交错误等提示。
    state: {
      noOrgLinked: 'Current account is not linked to an organization. Cannot create member.',
      selectVersionError: 'Please select an assistant version',
      createError: 'Creation failed',
    },
    // success 创建成功的反馈消息。
    success: {
      created: 'Member {name} created and instance initialization submitted',
    },
  },
} as const
