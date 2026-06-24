package apierror

// 本文件集中登记「会话」（conversation）domain handler 层**内联** apierror.New 调用的
// 静态中文 MsgKey 与中/英译文。
// 范围覆盖 internal/api/handlers/hermes_conversation.go 与 internal/api/handlers/hermes_kanban.go
// 中内联的静态中文 apierror.New 调用：消息内容/标题为空（绑定校验失败）、服务端不支持流式响应。
// 注意：会话/看板的 service sentinel error → 文案映射（forbidden / not_supported / 输出不兼容等）
// 已在 messages_common.go 登记（MsgConversation* / MsgKanban* / MsgHermesIncompatible），本文件
// 不重复定义；仅迁移这两个 handler 文件里内联的 apierror.New。
// zh 译文逐字取自 handler 原中文（标点/空格不改），en 为忠实英译。
// 「服务端不支持流式响应」在 conversation/kanban 两处文案一致，复用同一 key。

// 会话 domain 内联静态错误 MsgKey 常量（前缀 err.conversation.*）。
const (
	// MsgConversationEmptyMessage 消息内容不能为空（Chat / ChatStream 绑定校验失败）。
	MsgConversationEmptyMessage MsgKey = "err.conversation.empty_message"
	// MsgConversationEmptyTitle 标题不能为空（Rename 绑定校验失败）。
	MsgConversationEmptyTitle MsgKey = "err.conversation.empty_title"
	// MsgConversationNoStreaming 服务端不支持流式响应；conversation/kanban 两处 SSE flusher 缺失共用。
	MsgConversationNoStreaming MsgKey = "err.conversation.no_streaming"
)

// init 把会话 domain 内联错误译文并入中心 catalog。
func init() {
	Register(map[MsgKey]map[string]string{
		MsgConversationEmptyMessage: {"zh": "消息内容不能为空", "en": "The message content must not be empty"},
		MsgConversationEmptyTitle:   {"zh": "标题不能为空", "en": "The title must not be empty"},
		MsgConversationNoStreaming:  {"zh": "服务端不支持流式响应", "en": "The server does not support streaming responses"},
	})
}
