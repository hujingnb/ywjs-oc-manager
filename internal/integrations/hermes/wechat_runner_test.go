package hermes

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"oc-manager/internal/integrations/ocops"
)

// fakeStreamer 模拟 ChannelLoginStreamer：按预设序列投递 ocops.ChannelLoginEvent，
// 或在 ChannelLogin 调用阶段直接返回错误（模拟 oc-ops 不可达）。
type fakeStreamer struct {
	events  []ocops.ChannelLoginEvent
	err     error
	gotEp   ocops.Endpoint
	gotChan string
}

func (f *fakeStreamer) ChannelLogin(_ context.Context, ep ocops.Endpoint, channel string) (<-chan ocops.ChannelLoginEvent, error) {
	if f.err != nil {
		return nil, f.err
	}
	f.gotEp = ep
	f.gotChan = channel
	ch := make(chan ocops.ChannelLoginEvent, len(f.events))
	for _, ev := range f.events {
		ch <- ev
	}
	close(ch)
	return ch, nil
}

// 覆盖正常路径：oc-ops 先推 qrcode 再推 bound → runner 翻译为 QRCode + Bound 事件。
// 同时校验 runner 把注入的 Endpoint 和固定 channel="weixin" 透传给 streamer。
func TestStreamWeChatLogin_SuccessYieldsQRThenBound(t *testing.T) {
	streamer := &fakeStreamer{events: []ocops.ChannelLoginEvent{
		{Event: "qrcode", URL: "https://liteapp.weixin.qq.com/q/abc?qrcode=tok&bot_type=3"},
		{Event: "bound"},
	}}
	ep := ocops.Endpoint{BaseURL: "http://app-1.ocops:8080", Token: "tok-1"}
	runner := NewWeixinRunner(streamer, ep)

	events, err := runner.StreamWeChatLogin(context.Background())
	require.NoError(t, err)

	var qr, bound *WeixinEvent
	for ev := range events {
		ev := ev
		switch ev.Type {
		case WeixinEventQRCode:
			qr = &ev
		case WeixinEventBound:
			bound = &ev
		}
	}
	require.NotNil(t, qr, "应收到 qrcode 事件")
	require.Equal(t, "https://liteapp.weixin.qq.com/q/abc?qrcode=tok&bot_type=3", qr.QRCodeURL)
	require.NotNil(t, bound, "应收到 bound 事件")
	// runner 必须把注入坐标与固定渠道名透传给 oc-ops 客户端。
	require.Equal(t, ep, streamer.gotEp)
	require.Equal(t, "weixin", streamer.gotChan)
}

// 覆盖失败路径：oc-ops 推 failed 事件（带 reason）→ runner 翻译为 Failed，reason 写入 Error。
func TestStreamWeChatLogin_FailedEventYieldsFailed(t *testing.T) {
	streamer := &fakeStreamer{events: []ocops.ChannelLoginEvent{
		{Event: "failed", Reason: "LOGIN_FAILED_OR_TIMEOUT"},
	}}
	runner := NewWeixinRunner(streamer, ocops.Endpoint{})
	events, err := runner.StreamWeChatLogin(context.Background())
	require.NoError(t, err)

	var failed *WeixinEvent
	for ev := range events {
		ev := ev
		if ev.Type == WeixinEventFailed {
			failed = &ev
		}
	}
	require.NotNil(t, failed)
	require.Contains(t, failed.Error, "LOGIN_FAILED_OR_TIMEOUT")
}

// 覆盖 timeout 路径：oc-ops 推 timeout 事件也应转化为 Failed 事件，
// reason 字段（这里携带）写入 Error 供上层审计记录。
func TestStreamWeChatLogin_TimeoutYieldsFailed(t *testing.T) {
	streamer := &fakeStreamer{events: []ocops.ChannelLoginEvent{
		{Event: "timeout", Reason: "qr expired"},
	}}
	runner := NewWeixinRunner(streamer, ocops.Endpoint{})
	events, err := runner.StreamWeChatLogin(context.Background())
	require.NoError(t, err)

	var failed *WeixinEvent
	for ev := range events {
		ev := ev
		if ev.Type == WeixinEventFailed {
			failed = &ev
		}
	}
	require.NotNil(t, failed)
	require.Equal(t, "qr expired", failed.Error)
}

// 覆盖 timeout 无 reason：应以事件名 "timeout" 兜底作为 Error。
func TestStreamWeChatLogin_TimeoutNoReasonFallsBackToEventName(t *testing.T) {
	streamer := &fakeStreamer{events: []ocops.ChannelLoginEvent{
		{Event: "timeout"},
	}}
	runner := NewWeixinRunner(streamer, ocops.Endpoint{})
	events, err := runner.StreamWeChatLogin(context.Background())
	require.NoError(t, err)

	var failed *WeixinEvent
	for ev := range events {
		ev := ev
		if ev.Type == WeixinEventFailed {
			failed = &ev
		}
	}
	require.NotNil(t, failed)
	require.Equal(t, "timeout", failed.Error)
}

// 覆盖 oc-ops 触发登录就失败的场景（如服务不可达）。
func TestStreamWeChatLogin_StreamerError(t *testing.T) {
	streamer := &fakeStreamer{err: errors.New("oc-ops unreachable")}
	runner := NewWeixinRunner(streamer, ocops.Endpoint{})
	_, err := runner.StreamWeChatLogin(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "oc-ops unreachable")
}
