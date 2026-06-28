// conversation 收敛「管理后台查看会话历史」时的消息可见性判定，避免在会话页内联过滤逻辑。
// 背景：hermes api_server 返回的会话消息 role 有 user / assistant / tool 三类，
// 其中 role==='tool' 是引擎调用工具后的执行结果（tool_name/tool_call_id + 工具输出文本），
// 属于引擎内部过程而非「客服与用户的对话正文」；assistant 若某一步仅触发工具调用，
// 其 content 可能为空（真正的文字回复在后续消息）。管理后台只需展示对话正文，
// 故在此统一判定哪些消息属于对话、应当展示。

import type { ConversationMessage } from '@/api/conversations'

// hasRenderableContent 判断 content 是否含可展示正文：
//   - 字符串：去除首尾空白后非空；
//   - 多模态数组：至少含一个非空 text part、一个 image_url part 或一个 input_file part；
//   - 其它（null/undefined/对象等）：视为无可展示内容。
// 识别会话页实际渲染的 text / image_url / input_file 三类 part，与 ConversationMessageView 保持一致。
export function hasRenderableContent(content: unknown): boolean {
  if (typeof content === 'string') return content.trim() !== ''
  if (Array.isArray(content)) {
    return content.some((p) => {
      if (!p || typeof p !== 'object') return false
      const part = p as { type?: string; text?: unknown }
      if (part.type === 'text') return typeof part.text === 'string' && part.text.trim() !== ''
      if (part.type === 'image_url') return true
      // input_file part 是用户上传的文件，有文件即有可展示内容。
      if (part.type === 'input_file') return true
      return false
    })
  }
  return false
}

// ConversationFileRef 是从消息文字里解析出的文件引用。
export interface ConversationFileRef {
  // fileId 对应历史文件下载端点的文件 id。
  fileId: string
  // filename 文件名，旧格式标记无文件名时为空串。
  filename: string
}

// safeDecode 容错 decodeURIComponent（遇非法百分号编码时原样返回，避免抛错）。
function safeDecode(s: string): string {
  try {
    return decodeURIComponent(s)
  } catch {
    return s
  }
}

// parseFileMarkers 从字符串内容里解析所有 <oc-file:id[:enc_filename]> 标记，
// 连同其前置的 [..] 英文注记一并剥离，返回剥离后的纯文字与文件引用列表。
// 背景：oc-ops 把历史文件改写成「英文注记 + 标记」存进 transcript（形如
// `[The user sent a document: 'x'. The file is saved at: /opt/data/...] <oc-file:id:enc>`），
// 前端若只剥标记会残留英文注记与内部路径 /opt/data/... 泄漏给用户，故连注记一并剥除。
// 注记块（[...]）必须紧邻标记（中间仅空白）才一并剥离，避免误删用户正文里的方括号。
// enc_filename 经 urllib.parse.quote 编码，用 decodeURIComponent 还原；
// 兼容旧格式 <oc-file:id>（无文件名，filename 置空串）。
export function parseFileMarkers(text: string): { clean: string; files: ConversationFileRef[] } {
  const files: ConversationFileRef[] = []
  // 1) 注记 + 新格式标记（含文件名）
  let out = text.replace(/\[[^\]]*\]\s*<oc-file:([^:>]+):([^>]*)>/g, (_m, id, enc) => {
    files.push({ fileId: id, filename: safeDecode(enc) })
    return ''
  })
  // 2) 注记 + 旧格式标记（无文件名）
  out = out.replace(/\[[^\]]*\]\s*<oc-file:([^>]+)>/g, (_m, id) => {
    files.push({ fileId: id, filename: '' })
    return ''
  })
  // 3) 裸新格式标记（无前置注记）
  out = out.replace(/<oc-file:([^:>]+):([^>]*)>/g, (_m, id, enc) => {
    files.push({ fileId: id, filename: safeDecode(enc) })
    return ''
  })
  // 4) 裸旧格式标记
  out = out.replace(/<oc-file:([^>]+)>/g, (_m, id) => {
    files.push({ fileId: id, filename: '' })
    return ''
  })
  return { clean: out.trim(), files }
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
