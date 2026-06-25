// conversation 收敛「管理后台查看会话历史」时的消息可见性判定，避免在会话页内联过滤逻辑。
// 背景：hermes api_server 返回的会话消息 role 有 user / assistant / tool 三类，
// 其中 role==='tool' 是引擎调用工具后的执行结果（tool_name/tool_call_id + 工具输出文本），
// 属于引擎内部过程而非「客服与用户的对话正文」；assistant 若某一步仅触发工具调用，
// 其 content 可能为空（真正的文字回复在后续消息）。管理后台只需展示对话正文，
// 故在此统一判定哪些消息属于对话、应当展示。

import type { ConversationMessage } from '@/api/conversations'

// hasRenderableContent 判断 content 是否含可展示正文：
//   - 字符串：去除首尾空白后非空；
//   - 多模态数组：至少含一个非空 text part 或一个 image_url part；
//   - 其它（null/undefined/对象等）：视为无可展示内容。
// 仅识别会话页实际渲染的 text / image_url 两类 part，与 ConversationMessageView 保持一致。
export function hasRenderableContent(content: unknown): boolean {
  if (typeof content === 'string') return content.trim() !== ''
  if (Array.isArray(content)) {
    return content.some((p) => {
      if (!p || typeof p !== 'object') return false
      const part = p as { type?: string; text?: unknown }
      if (part.type === 'text') return typeof part.text === 'string' && part.text.trim() !== ''
      if (part.type === 'image_url') return true
      return false
    })
  }
  return false
}

// isDialogueMessage 判定一条消息是否属于「对话正文」，用于查看会话历史时过滤：
//   - role==='tool' 的工具执行结果一律不展示（引擎内部过程，非对话）；
//   - 过滤工具消息后，仅含工具调用、content 为空的 assistant 步骤会留下空气泡，
//     故内容不可展示的消息也一并跳过。
// user / assistant 等其它角色只要含可展示内容即视为对话正文予以保留。
export function isDialogueMessage(m: ConversationMessage): boolean {
  if (m.role === 'tool') return false
  return hasRenderableContent(m.content)
}
