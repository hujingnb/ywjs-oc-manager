// tickets 模块文案（en）。工单详情页面文案。
// 结构必须与 zh/tickets.ts 完全对齐（相同 key 路径）。
export default {
  // state 加载/错误文案。
  state: {
    loadFailed: 'Failed to load ticket details',
    noDelivery: 'Not delivered',
  },
  // actions 操作按钮文案。
  actions: {
    back: 'Back',
    start: 'Start',
    deliver: 'Deliver',
    redeliver: 'Re-deliver',
    editTargets: 'Edit Visibility',
    reopen: 'Reopen',
    reject: 'Reject',
    install: 'Go to Install',
    saveQuote: 'Save Quote',
    cancel: 'Cancel',
    confirmReject: 'Confirm Reject',
    save: 'Save',
  },
  // section 区块标题。
  section: {
    submitter: 'Submission Info',
    rejectReason: 'Rejection Reason',
    quote: 'Quote',
    targets: 'Visibility',
    conversation: 'Conversation',
  },
  // fields 表单字段标签。
  fields: {
    submitter: 'Submitter',
    organization: 'Organization',
    quotePlaceholder: 'Quote (CNY)',
  },
  // modal 弹窗标题。
  modal: {
    rejectTitle: 'Reject Ticket',
    targetsTitle: 'Edit Visibility',
    rejectPlaceholder: 'Enter rejection reason',
  },
  // audience 可见范围标签。
  audience: {
    all_org: 'Entire Organization',
    org_admins: 'Org Admins Only',
    requester_only: 'Requester Only',
  },
  // requesterRole 申请人角色标签。
  requesterRole: {
    org_admin: 'Admin',
    org_member: 'Member',
  },
  // messages 操作结果消息。
  messages: {
    quoteSaved: 'Quote saved',
  },
} as const
