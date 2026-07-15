package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"oc-manager/internal/domain"
)

// TestAICCChannelNormalize 覆盖网页和模拟语音入口统一映射为渠道无关 turn 的边界。
func TestAICCChannelNormalize(t *testing.T) {
	now := time.Date(2026, 7, 15, 10, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name    string
		channel string
		want    string
	}{
		{name: "公开链接", channel: domain.AICCChannelWebLink, want: domain.AICCChannelWebLink},     // 网页公开链接保留来源标识。
		{name: "网页挂件", channel: domain.AICCChannelWebWidget, want: domain.AICCChannelWebWidget}, // 嵌入挂件保留来源标识。
		{name: "模拟语音", channel: domain.AICCChannelVoice, want: domain.AICCChannelVoice},         // 语音 transcript 复用文字 turn。
	} {
		t.Run(tc.name, func(t *testing.T) {
			// 每种受支持渠道都只归一化文本，不引入音频或供应商字段。
			turn, err := NormalizeAICCInboundTurn(AICCChannelInput{TurnID: "turn-1", SessionID: "session-1", Channel: tc.channel, Text: "  需要报价  ", OccurredAt: now})
			require.NoError(t, err)
			assert.Equal(t, tc.want, turn.Channel)
			assert.Equal(t, "需要报价", turn.Text)
			assert.Equal(t, now, turn.OccurredAt)
		})
	}
}

// TestAICCChannelRejectsUnknown 覆盖外部渠道不能伪装成默认网页入口的安全边界。
func TestAICCChannelRejectsUnknown(t *testing.T) {
	// 未登记渠道必须由 adapter 明确拒绝，不能回落 web_link 绕过渠道治理。
	_, err := NormalizeAICCInboundTurn(AICCChannelInput{TurnID: "turn-1", SessionID: "session-1", Channel: "sms", Text: "你好"})
	require.ErrorIs(t, err, ErrAICCChannelUnsupported)
}

// TestAICCChannelMockVoiceResponse 覆盖模拟语音只映射客服响应字段，且不改写客服留资能力动作。
func TestAICCChannelMockVoiceResponse(t *testing.T) {
	// mock voice 不做 ASR/TTS，只把已转写文本和渠道无关回复转换为可观察事件。
	event, err := NewAICCMockVoiceAdapter().ResponseEvent(AICCResponseEnvelope{
		Text:       "可以为您安排顾问。",
		Sources:    []AICCResponseSource{{Type: "knowledge", Title: "套餐说明", ReferenceID: "kb-1"}},
		NextAction: "offer_lead",
	})
	require.NoError(t, err)
	assert.Equal(t, "mock_voice.response", event.Type)
	assert.Equal(t, "可以为您安排顾问。", event.Text)
	assert.Equal(t, "offer_lead", event.NextAction)
	require.Len(t, event.Sources, 1)
	assert.Equal(t, "kb-1", event.Sources[0].ReferenceID)
}
