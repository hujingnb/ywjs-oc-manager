// layout 模块文案（zh）。由 P2 迁移逐步填充。
export default {
  // auth 区域：登录页左侧平台介绍
  auth: {
    // heroLabel：平台介绍区域的无障碍标签
    heroLabel: '平台介绍',
    // metricsLabel：平台能力区域的无障碍标签
    metricsLabel: '平台能力',
    // loginLabel：登录控制台区域的无障碍标签
    loginLabel: '登录控制台',
    // title：主标题中嵌入品牌热词的前段
    titlePrefix: '让',
    // titleHot：主标题中渐变高亮的品牌热词
    titleHot: '智能体',
    // titleSuffix：主标题品牌热词后的结语
    titleSuffix: '融入企业工作流',
    // lead：平台简介引导段落
    lead: '企业 AI 数智员工运行管理平台，用 Agent 连通云资源、企业知识库和多模型能力，深度接管员工日常工作任务。',
    // metrics：三列能力卡片
    metrics: {
      agent: {
        title: '一人一 Agent',
        desc: '配置独立运行环境、专属任务管理及独享个人知识库',
      },
      unified: {
        title: '统一管控',
        desc: '实现账号权限、知识库、大模型与 Token 消耗的统一管控',
      },
      custom: {
        title: '可定制化',
        desc: '支持定制需求，可私有化部署，完全适配企业安全规范',
      },
    },
  },
  // nav：左侧导航菜单文案
  nav: {
    overview: '总览',
    channels: '渠道',
    workspace: '工作目录',
    personalKnowledge: '个人知识库',
    orgKnowledge: '企业知识库',
    skills: '技能',
    tasks: '任务',
    cron: '定时任务',
    // conversations：成员侧边栏「对话」入口，指向实例对话管理 tab
    conversations: '对话',
    usage: '用量',
    console: '控制台',
    organizations: '企业',
    assistantVersions: '助手版本',
    industryKnowledge: '行业知识库',
    platformSkills: '平台技能',
    customSkills: '定制技能',
    members: '成员',
    instance: '实例',
    balance: '账户余额',
    audit: '审计',
    permissions: '权限说明',
  },
  // header：顶栏区域文案
  header: {
    // console：顶栏主标题
    console: '控制台',
    // envLabel：未登录或无用户时的环境标签
    envLabel: '本地调试环境',
    // envLabelWithRole：已登录时拼接角色的环境标签（{role} 为插值占位符）
    envLabelWithRole: '本地调试环境 · {role}',
    // apiStatus：右上角 API 状态绿标
    apiStatus: 'API 正常',
    // helpManual：使用手册按钮文案与 title
    helpManual: '使用手册',
  },
  // perspective：org_admin 视角切换按钮文案
  perspective: {
    manage: '企业管理',
    instance: '我的实例',
  },
  // password：侧边栏改密入口与弹窗内文案
  password: {
    // changePassword：改密按钮与弹窗标题
    changePassword: '修改密码',
    // currentPassword：当前密码表单项标签
    currentPassword: '当前密码',
    // currentPasswordPlaceholder：当前密码输入框占位文字
    currentPasswordPlaceholder: '请输入当前密码',
    // newPassword：新密码表单项标签
    newPassword: '新密码',
    // newPasswordPlaceholder：新密码输入框占位文字
    newPasswordPlaceholder: '至少 8 位',
    // confirmPassword：确认新密码表单项标签
    confirmPassword: '确认新密码',
    // confirmPasswordPlaceholder：确认新密码输入框占位文字
    confirmPasswordPlaceholder: '再次输入新密码',
    // submitButton：提交改密的按钮文案
    submitButton: '确认修改',
    // errAllRequired：未填全三个密码字段的提示
    errAllRequired: '请填写当前密码、新密码和确认新密码',
    // errMinLength：新密码长度不足的提示
    errMinLength: '新密码至少 8 位',
    // errSameAsOld：新旧密码相同的提示
    errSameAsOld: '新密码不能与当前密码相同',
    // errMismatch：两次输入新密码不一致的提示
    errMismatch: '两次输入的新密码不一致',
    // errChangeFailed：后端改密接口失败的提示
    errChangeFailed: '修改密码失败',
  },
  // sidebar：侧边栏底部用户区文案
  sidebar: {
    // logout：退出登录按钮文案
    logout: '退出',
  },
} as const
