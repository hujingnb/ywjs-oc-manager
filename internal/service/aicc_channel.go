package service

import "time"

// AICCInboundTurn 是渠道适配层交给客服核心的一次不可变输入。
// 语音渠道未来只需在适配层完成 ASR/TTS，并复用本结构，不让渠道状态进入 Hermes。
type AICCInboundTurn struct {
	TurnID     string
	SessionID  string
	Channel    string
	Text       string
	Locale     string
	OccurredAt time.Time
	Context    AICCConversationContext
	// Instruction 是 manager 根据客服配置构造的受信任业务约束，不接受渠道侧输入。
	Instruction string
	// AppID 是绑定的隐藏运行时应用标识，仅供 manager 转发层路由使用。
	AppID string
}

// AICCResponseSource 描述答复依据，后续持久化与公开 API 均使用同一来源模型。
type AICCResponseSource struct {
	Type        string `json:"type"`
	Title       string `json:"title"`
	URL         string `json:"url"`
	Scope       string `json:"scope"`
	ReferenceID string `json:"reference_id"`
	Unconfirmed bool   `json:"unconfirmed"`
}

// AICCResponseEnvelope 是渠道无关的客服答复；当前文字网页仅消费 Text，
// 其余字段为来源、拒答、兜底与审计链路预留。
type AICCResponseEnvelope struct {
	Text       string
	Sources    []AICCResponseSource
	NextAction string
	Refusal    bool
	Fallback   bool
	AuditRef   string
	// ToolAudit 只能由受信任运行时适配层根据本轮工具执行记录写入。它用于校验
	// sources 的 reference_id，既不会持久化，也不会透出到公开 API。
	ToolAudit AICCResponseToolAudit
	// Raw 是 Hermes 原始输出，仅在 manager 到运行时的受信任边界内使用；持久化和公开 API 不输出它。
	Raw string
}
