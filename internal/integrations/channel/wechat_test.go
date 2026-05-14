package channel

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"oc-manager/internal/integrations/hermes"
)

// TestWeChatAdapterBeginAuthReturnsQRCodeChallenge 验证 WeChatAdapter 在收到 QRCode 事件后
// 返回 AuthChallenge，并在后台消费剩余事件直到 Bound 状态。
func TestWeChatAdapterBeginAuthReturnsQRCodeChallenge(t *testing.T) {
	// 正常路径：先收 QRCode 事件，再收 Bound 事件。
	runner := &fakeHermesRunner{events: []hermes.WeixinEvent{
		{Type: hermes.WeixinEventQRCode, QRCodeURL: "https://liteapp.weixin.qq.com/q/abc"},
		{Type: hermes.WeixinEventBound, AccountID: "610@im.bot", Token: "t"},
	}}
	adapter := NewWeChatAdapter(runner)

	challenge, err := adapter.BeginAuth(context.Background(), AuthInput{AppID: "app-1"})
	require.NoError(t, err)
	// BeginAuth 应返回 qrcode 类型的 challenge，包含 QRCodeURL。
	require.Equal(t, "qrcode", challenge.Type)
	require.Equal(t, "https://liteapp.weixin.qq.com/q/abc", challenge.QRCode)

	// 异步消费剩余事件；等待最长 500ms 让 Bound 状态落地。
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		progress, _ := adapter.PollAuth(context.Background(), AuthInput{AppID: "app-1"})
		if progress.Status == AuthStatusBound {
			require.Equal(t, "610@im.bot", progress.BoundIdentity)
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("500ms 内未达到 bound 状态")
}

// TestWeChatAdapterBeginAuthFailedEvent 验证收到 Failed 事件时 BeginAuth 返回错误。
func TestWeChatAdapterBeginAuthFailedEvent(t *testing.T) {
	// 异常路径：直接收 Failed 事件，无 QR。
	runner := &fakeHermesRunner{events: []hermes.WeixinEvent{
		{Type: hermes.WeixinEventFailed, Error: "LOGIN_FAILED"},
	}}
	adapter := NewWeChatAdapter(runner)

	_, err := adapter.BeginAuth(context.Background(), AuthInput{AppID: "app-1"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "LOGIN_FAILED")
}

// TestWeChatAdapterBeginAuthEmptyStream 验证 events channel 立即关闭时 BeginAuth 返回错误。
func TestWeChatAdapterBeginAuthEmptyStream(t *testing.T) {
	// 边界条件：stream 无任何事件就关闭（如 exec 启动即失败）。
	runner := &fakeHermesRunner{events: nil}
	adapter := NewWeChatAdapter(runner)

	_, err := adapter.BeginAuth(context.Background(), AuthInput{AppID: "app-1"})
	require.Error(t, err)
}

// TestWeChatAdapterPollAuthDefaultsToPending 验证尚未发起登录的 app 默认返回 pending 状态。
func TestWeChatAdapterPollAuthDefaultsToPending(t *testing.T) {
	// 边界条件：PollAuth 在没有任何事件记录时应返回 pending。
	adapter := NewWeChatAdapter(&fakeHermesRunner{})
	progress, err := adapter.PollAuth(context.Background(), AuthInput{AppID: "missing"})
	require.NoError(t, err)
	require.Equal(t, AuthStatusPending, progress.Status)
}

// fakeHermesRunner 是 CommandRunner 的测试实现，返回预设的 hermes.WeixinEvent 序列。
type fakeHermesRunner struct {
	events []hermes.WeixinEvent
}

func (r *fakeHermesRunner) StreamWeChatLogin(_ context.Context, _ AuthInput) (<-chan hermes.WeixinEvent, error) {
	ch := make(chan hermes.WeixinEvent, len(r.events))
	for _, ev := range r.events {
		ch <- ev
	}
	close(ch)
	return ch, nil
}
