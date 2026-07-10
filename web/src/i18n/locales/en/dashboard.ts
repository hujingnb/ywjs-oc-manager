// dashboard 模块文案（en）。首屏快捷入口与欢迎区文案。
export default {
  // greeting 首屏欢迎语，{name} 替换为用户显示名。
  greeting: 'Welcome back, {name}',
  // roleLabel 当前角色标签，展示在欢迎区副标题。
  role: {
    platformAdmin: 'Platform Admin',
    orgAdmin: 'Org Admin',
    member: 'Member',
  },
  // cards 按角色展示快捷入口标题与副标题。
  cards: {
    organizations: {
      title: 'Organizations',
      subtitle: 'View / create / recharge organizations',
    },
    auditLogs: {
      title: 'Audit Logs',
      subtitle: 'High-risk operation traceability',
    },
    members: {
      title: 'Members',
      subtitle: 'Create / disable / delete members',
    },
    apps: {
      title: 'Instances',
      subtitle: 'All instance status in the organization',
    },
    orgKnowledge: {
      title: 'Org Knowledge Base',
      subtitle: 'Upload shared organization files',
    },
    myApp: {
      title: 'My Instance',
      subtitle: 'View status, usage, and instance audit',
    },
    myUsage: {
      title: 'My Usage',
      subtitle: 'View call records for my instance',
    },
    readKnowledge: {
      title: 'Org Knowledge Base',
      subtitle: 'Reference materials',
    },
  },
  // subsystems 展示企业已开通的独立子系统入口。
  subsystems: {
    eyebrow: 'Subsystems',
    title: 'Subsystem Entry',
    aicc: {
      title: 'AICC Service',
      subtitle: 'Open the dedicated service workspace for reception, sessions, leads, and deployment',
      action: 'Open workspace',
    },
  },
} as const
