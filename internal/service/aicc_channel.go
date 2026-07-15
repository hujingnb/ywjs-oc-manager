package service

import (
	"errors"
	"strings"
	"time"

	"oc-manager/internal/domain"
)

// ErrAICCChannelUnsupported 表示尚未接入或不受信任的外部渠道，不能回退成网页渠道。
var ErrAICCChannelUnsupported = errors.New("AICC 渠道不受支持")

// AICCChannelInput 是外部渠道 adapter 的最小输入。语音适配器只能传已经得到的 transcript，
// 不在客服核心引入 ASR、TTS、音频或任意厂商配置。
type AICCChannelInput struct {
	TurnID     string
	SessionID  string
	Channel    string
	Text       string
	Locale     string
	OccurredAt time.Time
}

// AICCMockVoiceEvent 是无供应商依赖的语音出口契约，供未来电话/语音网关 adapter 映射。
type AICCMockVoiceEvent struct {
	Type       string
	Text       string
	Sources    []AICCResponseSource
	NextAction string
}

// AICCMockVoiceAdapter 只声明语音渠道与客服核心的字段映射，不实现识别、合成或传输。
type AICCMockVoiceAdapter struct{}

// NormalizeAICCInboundTurn 把已验证的外部文本归一化为核心 turn，未知渠道必须显式失败。
func NormalizeAICCInboundTurn(input AICCChannelInput) (AICCInboundTurn, error) {
	channel := strings.TrimSpace(input.Channel)
	switch channel {
	case domain.AICCChannelWebLink, domain.AICCChannelWebWidget, domain.AICCChannelVoice:
	default:
		return AICCInboundTurn{}, ErrAICCChannelUnsupported
	}
	return AICCInboundTurn{
		TurnID: input.TurnID, SessionID: input.SessionID, Channel: channel,
		Text: strings.TrimSpace(input.Text), Locale: strings.TrimSpace(input.Locale), OccurredAt: input.OccurredAt,
	}, nil
}

// NewAICCMockVoiceAdapter 返回无状态 mock adapter，避免当前阶段绑定具体语音供应商。
func NewAICCMockVoiceAdapter() AICCMockVoiceAdapter { return AICCMockVoiceAdapter{} }

// InboundTurn 将语音网关已完成的 transcript 映射为 voice channel 的核心输入。
func (AICCMockVoiceAdapter) InboundTurn(input AICCChannelInput) (AICCInboundTurn, error) {
	input.Channel = domain.AICCChannelVoice
	return NormalizeAICCInboundTurn(input)
}

// ResponseEvent 保留客服答复的文字、已验证来源和下一步动作，绝不改变 offer_lead 等能力语义。
func (AICCMockVoiceAdapter) ResponseEvent(reply AICCResponseEnvelope) (AICCMockVoiceEvent, error) {
	if strings.TrimSpace(reply.Text) == "" {
		return AICCMockVoiceEvent{}, ErrAICCResponsePolicy
	}
	return AICCMockVoiceEvent{Type: "mock_voice.response", Text: reply.Text, Sources: reply.Sources, NextAction: reply.NextAction}, nil
}

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
