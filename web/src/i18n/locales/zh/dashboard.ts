// dashboard 模块文案（zh）。首屏快捷入口与欢迎区文案。
// 结构必须与 en/dashboard.ts 完全对齐（相同 key 路径）。
export default {
  // greeting 首屏欢迎语，{name} 替换为用户显示名。
  greeting: '欢迎回来，{name}',
  // roleLabel 当前角色标签，展示在欢迎区副标题。
  role: {
    platformAdmin: 'Platform Admin',
    orgAdmin: 'Org Admin',
    member: 'Member',
  },
  // cards 按角色展示快捷入口标题与副标题。
  cards: {
    organizations: {
      title: '企业管理',
      subtitle: '查看 / 创建 / 充值企业',
    },
    auditLogs: {
      title: '审计日志',
      subtitle: '高风险操作回溯',
    },
    members: {
      title: '成员管理',
      subtitle: '创建 / 禁用 / 删除企业成员',
    },
    apps: {
      title: '实例列表',
      subtitle: '企业内全部实例状态',
    },
    orgKnowledge: {
      title: '企业知识库',
      subtitle: '上传企业共享文件',
    },
    myApp: {
      title: '我的实例',
      subtitle: '查看状态、用量与实例审计',
    },
    myUsage: {
      title: '我的用量',
      subtitle: '查看自己实例的调用记录',
    },
    readKnowledge: {
      title: '企业知识库',
      subtitle: '可读资料',
    },
  },
} as const
