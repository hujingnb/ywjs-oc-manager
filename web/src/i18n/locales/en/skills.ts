// skills 模块文案（en）。技能页面文案。
// 结构必须与 zh/skills.ts 完全对齐（相同 key 路径）。
export default {
  // state 加载/错误/空态文案。
  state: {
    noApp: 'You have no app yet. You can submit a custom skill request; installation requires an app.',
  },
  // messages 操作结果消息。
  messages: {
    createAppFirst: 'Please create an app before installing',
  },
} as const
