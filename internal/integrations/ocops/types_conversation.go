// types_conversation.go — ocops 会话端点的 DTO。
// 字段名对齐 hermes api_server /api/sessions 响应（经 oc-ops 透传，已读源码确认）。
package ocops

// ConversationSession 是一条会话（跨渠道；source 标识来源渠道）。
// 字段名对齐 api_server `_session_response` safe_keys。
type ConversationSession struct {
	ID           string `json:"id"`
	Source       string `json:"source"`                  // 渠道来源：weixin / web / api_server 等
	UserID       string `json:"user_id,omitempty"`       // 会话归属用户标识（渠道侧）
	Title        string `json:"title,omitempty"`         // 会话标题（可空）
	Model        string `json:"model,omitempty"`         // 绑定模型（可空）
	StartedAt    string `json:"started_at,omitempty"`    // 会话开始时间
	LastActive   string `json:"last_active,omitempty"`   // 最近活跃时间（列表按此排序）
	MessageCount int    `json:"message_count,omitempty"` // 消息数（列表展示）
	Preview      string `json:"preview,omitempty"`       // 末条消息预览（列表展示）
}

// ConversationMessage 是一条历史消息。content 可能是字符串或多模态 parts（文字/图片），
// 用 any 容纳两种形态，由前端按 type 渲染。字段名对齐 api_server `_message_response` safe_keys。
type ConversationMessage struct {
	Role         string `json:"role"`                 // user / assistant
	Content      any    `json:"content"`              // 字符串或 [{type,text|image_url}]
	Timestamp    string `json:"timestamp,omitempty"`  // 消息时间戳
	ToolCalls    any    `json:"tool_calls,omitempty"` // 工具调用（透传，前端可忽略）
	FinishReason string `json:"finish_reason,omitempty"`
}

// ConversationChatReq 是续聊请求体。Message 为文字字符串；多模态时为 parts 数组。
type ConversationChatReq struct {
	Message any `json:"message"` // 文字字符串或多模态 parts 数组
}

// ConversationChatResult 是续聊回复。
// Usage 为上游用量统计的透传字段，用 any 容纳任意 JSON（swag v2 无法解析
// json.RawMessage，故用 any）。
type ConversationChatResult struct {
	SessionID string              `json:"session_id"`
	Message   ConversationMessage `json:"message"`
	Usage     any                 `json:"usage,omitempty"`
}

// ConversationCreateReq 是新建会话请求体。
type ConversationCreateReq struct {
	Source string `json:"source,omitempty"` // 默认 web
	Title  string `json:"title,omitempty"`
}
