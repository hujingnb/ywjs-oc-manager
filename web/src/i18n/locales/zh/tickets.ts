// tickets 模块文案（zh）。工单详情页面文案。
// 结构必须与 en/tickets.ts 完全对齐（相同 key 路径）。
export default {
  // state 加载/错误文案。
  state: {
    loadFailed: '工单详情加载失败',
    noDelivery: '未交付',
  },
  // actions 操作按钮文案。
  actions: {
    back: '返回',
    start: '开始制作',
    deliver: '交付',
    redeliver: '迭代交付',
    editTargets: '编辑可见范围',
    reopen: '重新受理',
    reject: '拒绝',
    install: '去安装',
    saveQuote: '保存报价',
    cancel: '取消',
    confirmReject: '确认拒绝',
    save: '保存',
  },
  // section 区块标题。
  section: {
    submitter: '提交信息',
    rejectReason: '拒绝原因',
    quote: '报价',
    targets: '可见范围',
    conversation: '对话',
  },
  // fields 表单字段标签。
  fields: {
    submitter: '提交者',
    organization: '所属企业',
    quotePlaceholder: '报价（元）',
  },
  // modal 弹窗标题。
  modal: {
    rejectTitle: '拒绝工单',
    targetsTitle: '编辑可见范围',
    rejectPlaceholder: '填写拒绝原因',
  },
  // audience 可见范围标签。
  audience: {
    all_org: '整企业',
    org_admins: '仅企业管理员',
    requester_only: '仅申请人',
  },
  // requesterRole 申请人角色标签。
  requesterRole: {
    org_admin: '管理员',
    org_member: '成员',
  },
  // messages 操作结果消息。
  messages: {
    quoteSaved: '报价已保存',
  },
} as const
