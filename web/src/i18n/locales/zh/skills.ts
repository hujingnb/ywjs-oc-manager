// skills 模块文案（zh）。技能页面文案。
// 结构必须与 en/skills.ts 完全对齐（相同 key 路径）。
export default {
  // state 加载/错误/空态文案。
  state: {
    noApp: '你还没有实例，可提交定制技能需求；交付后需有实例才能安装。',
  },
  // messages 操作结果消息。
  messages: {
    createAppFirst: '请先创建实例后再安装',
  },
} as const
