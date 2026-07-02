// layout 模块文案（en）。由 P2 迁移逐步填充。
export default {
  // auth 区域：登录页左侧平台介绍
  auth: {
    // heroLabel：平台介绍区域的无障碍标签
    heroLabel: 'Platform Introduction',
    // metricsLabel：平台能力区域的无障碍标签
    metricsLabel: 'Platform Capabilities',
    // loginLabel：登录控制台区域的无障碍标签
    loginLabel: 'Login to Console',
    // title：主标题中嵌入品牌热词的前段
    titlePrefix: 'Bring ',
    // titleHot：主标题中渐变高亮的品牌热词
    titleHot: 'AI Agents',
    // titleSuffix：主标题品牌热词后的结语
    titleSuffix: ' into Enterprise Workflows',
    // lead：平台简介引导段落
    lead: 'Enterprise AI employee management platform — connect cloud resources, corporate knowledge bases, and multi-model capabilities with Agents to automate daily employee tasks.',
    // metrics：三列能力卡片
    metrics: {
      agent: {
        title: 'One Sandbox per Person',
        desc: 'Independent runtime environment, multi-agent collaboration plugged into workflows, and a personal knowledge base',
      },
      unified: {
        title: 'Unified Control',
        desc: 'Centrally manage accounts & permissions, knowledge bases, LLMs, and token consumption',
      },
      custom: {
        title: 'Customizable',
        desc: 'Supports custom requirements, private deployment, and full compliance with enterprise security standards',
      },
    },
  },
  // nav：左侧导航菜单文案
  nav: {
    overview: 'Overview',
    channels: 'Channels',
    workspace: 'Workspace',
    personalKnowledge: 'Personal Knowledge',
    orgKnowledge: 'Org Knowledge',
    skills: 'Skills',
    tasks: 'Tasks',
    cron: 'Scheduled Tasks',
    // conversations: member sidebar entry for the conversations tab
    conversations: 'Conversations',
    usage: 'Usage',
    console: 'Console',
    organizations: 'Organizations',
    assistantVersions: 'Assistant Versions',
    industryKnowledge: 'Industry Knowledge',
    platformSkills: 'Platform Skills',
    customSkills: 'Custom Skills',
    // webPublishConfig: platform admin entry for enabling and configuring org web-publish
    webPublishConfig: 'Web Publish Setup',
    members: 'Members',
    // publishedSites: org-management entry for the published sites list (org_admin & platform_admin)
    publishedSites: 'Published Sites',
    instance: 'Instance',
    balance: 'Account Balance',
    audit: 'Audit',
    permissions: 'Permissions',
  },
  // header：顶栏区域文案
  header: {
    // console：顶栏主标题
    console: 'Console',
    // envLabel：未登录或无用户时的环境标签
    envLabel: 'Local Dev Environment',
    // envLabelWithRole：已登录时拼接角色的环境标签（{role} 为插值占位符）
    envLabelWithRole: 'Local Dev Environment · {role}',
    // apiStatus：右上角 API 状态绿标
    apiStatus: 'API OK',
    // helpManual：使用手册按钮文案与 title
    helpManual: 'User Manual',
  },
  // perspective：org_admin 视角切换按钮文案
  perspective: {
    manage: 'Org Management',
    instance: 'My Instance',
  },
  // password：侧边栏改密入口与弹窗内文案
  password: {
    // changePassword：改密按钮与弹窗标题
    changePassword: 'Change Password',
    // currentPassword：当前密码表单项标签
    currentPassword: 'Current Password',
    // currentPasswordPlaceholder：当前密码输入框占位文字
    currentPasswordPlaceholder: 'Enter current password',
    // newPassword：新密码表单项标签
    newPassword: 'New Password',
    // newPasswordPlaceholder：新密码输入框占位文字
    newPasswordPlaceholder: 'At least 8 characters',
    // confirmPassword：确认新密码表单项标签
    confirmPassword: 'Confirm New Password',
    // confirmPasswordPlaceholder：确认新密码输入框占位文字
    confirmPasswordPlaceholder: 'Re-enter new password',
    // submitButton：提交改密的按钮文案
    submitButton: 'Confirm Change',
    // errAllRequired：未填全三个密码字段的提示
    errAllRequired: 'Please fill in current password, new password, and confirmation',
    // errMinLength：新密码长度不足的提示
    errMinLength: 'New password must be at least 8 characters',
    // errSameAsOld：新旧密码相同的提示
    errSameAsOld: 'New password must differ from the current password',
    // errMismatch：两次输入新密码不一致的提示
    errMismatch: 'New passwords do not match',
    // errChangeFailed：后端改密接口失败的提示
    errChangeFailed: 'Failed to change password',
  },
  // sidebar：侧边栏底部用户区文案
  sidebar: {
    // logout：退出登录按钮文案
    logout: 'Log out',
  },
} as const
