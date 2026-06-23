// 实例会话 API：列会话 / 读历史 / 续聊(流式) / 新建 / 删除 / 重命名。
import { apiRequest, getStoredAccessToken, getCsrfToken } from '@/api/client'

// ConversationSession 对应后端 hermes 会话对象，source 区分来源渠道（web/wechat 等）。
export interface ConversationSession {
  id: string
  source: string
  user_id?: string
  title?: string
  model?: string
  started_at?: string
  last_active?: string
  message_count?: number
  preview?: string
}

// ConversationMessage 对应单条会话消息；content 可以是字符串或多模态 parts 数组。
export interface ConversationMessage {
  role: string
  content: unknown
  timestamp?: string
  tool_calls?: unknown
  finish_reason?: string
}

// base 构造实例会话 API 的基础路径。
const base = (appId: string) => `/api/v1/apps/${appId}/hermes/conversations`

// listConversations 获取实例的所有会话列表；source 可选用于过滤来源渠道。
export async function listConversations(appId: string, source = ''): Promise<ConversationSession[]> {
  const r = await apiRequest<{ sessions: ConversationSession[] }>(base(appId), {
    query: source ? { source } : undefined,
  })
  return r.sessions ?? []
}

// listMessages 获取指定会话的历史消息列表。
export async function listMessages(appId: string, sid: string): Promise<ConversationMessage[]> {
  const r = await apiRequest<{ messages: ConversationMessage[] }>(
    `${base(appId)}/${encodeURIComponent(sid)}/messages`,
  )
  return r.messages ?? []
}

// createConversation 新建一条 web 来源的会话；title 可选。
export async function createConversation(appId: string, title = ''): Promise<ConversationSession> {
  const r = await apiRequest<{ session: ConversationSession }>(base(appId), {
    method: 'POST',
    body: title ? { title } : {},
  })
  return r.session
}

// renameConversation 更新指定会话的标题。
export async function renameConversation(
  appId: string,
  sid: string,
  title: string,
): Promise<ConversationSession> {
  const r = await apiRequest<{ session: ConversationSession }>(
    `${base(appId)}/${encodeURIComponent(sid)}`,
    { method: 'PATCH', body: { title } },
  )
  return r.session
}

// deleteConversation 删除指定会话（后端返回 204）。
export async function deleteConversation(appId: string, sid: string): Promise<void> {
  await apiRequest(`${base(appId)}/${encodeURIComponent(sid)}`, { method: 'DELETE' })
}

// chatStream 以 SSE 流式发送消息并逐帧回调：
//   onDelta  — 收到 assistant.delta 帧时累积文本片段；
//   onDone   — 流结束（assistant.completed 或 reader 读完）时触发；
//   onError  — 收到 error 帧或 HTTP 失败时触发。
// 直接使用 fetch 以支持 SSE；手动附加 Authorization 和 X-CSRF-Token，与 apiRequest 保持一致。
export async function chatStream(
  appId: string,
  sid: string,
  message: string,
  cb: {
    onDelta: (d: string) => void
    onDone: () => void
    onError?: (m: string) => void
  },
): Promise<void> {
  const token = getStoredAccessToken()
  const csrf = getCsrfToken()
  const headers: Record<string, string> = {
    'Content-Type': 'application/json',
  }
  if (token) headers['Authorization'] = `Bearer ${token}`
  if (csrf) headers['X-CSRF-Token'] = csrf

  let resp: Response
  try {
    resp = await fetch(`${base(appId)}/${encodeURIComponent(sid)}/chat/stream`, {
      method: 'POST',
      headers,
      body: JSON.stringify({ message }),
    })
  } catch (e) {
    cb.onError?.(e instanceof Error ? e.message : 'network error')
    cb.onDone()
    return
  }

  if (!resp.ok || !resp.body) {
    cb.onError?.(`stream failed: ${resp.status}`)
    cb.onDone()
    return
  }

  const reader = resp.body.getReader()
  const decoder = new TextDecoder()
  let buf = ''

  for (;;) {
    const { done, value } = await reader.read()
    if (done) break
    buf += decoder.decode(value, { stream: true })
    // SSE 帧以两个换行分隔；split 后末尾可能有未完整帧，留入 buf 等待下次数据。
    const frames = buf.split('\n\n')
    buf = frames.pop() ?? ''
    for (const f of frames) {
      const line = f.split('\n').find((l) => l.startsWith('data:'))
      if (!line) continue
      try {
        const evt = JSON.parse(line.slice(5).trim())
        if (evt.event === 'assistant.delta') {
          cb.onDelta(evt.payload?.delta ?? '')
        } else if (evt.event === 'assistant.completed') {
          cb.onDone()
        } else if (evt.event === 'error') {
          cb.onError?.(evt.message ?? evt.payload?.message ?? 'error')
        }
      } catch {
        // 跳过无法解析的帧（可能是注释行或残包）。
      }
    }
  }
  cb.onDone()
}
