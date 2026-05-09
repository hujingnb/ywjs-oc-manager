package channel

import (
	"context"
	"testing"
	"time"
	"github.com/stretchr/testify/require"
)

func TestWeChatAdapterBeginAuthReturnsQRCodeChallenge(t *testing.T) {
	// Sprint 0 POC 实测样本：plugin loading 噪声 + 中文提示行 + ASCII QR + URL + 等待提示。
	runner := &fakeRunner{lines: []string{
		"[plugins] loading anthropic from /root/.openclaw/...",
		"[plugins] loaded 118 plugin(s) (70 attempted) in 11035.8ms",
		"正在启动...",
		"用手机微信扫描以下二维码，以继续连接：",
		"▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄",
		"若二维码未能显示或无法使用，你可以访问以下链接以继续：",
		"https://liteapp.weixin.qq.com/q/7GiQu1?qrcode=85e18acc56ebd5937ad4caa5fe1b01a1&bot_type=3",
		"正在等待操作...",
		"已将此 OpenClaw 连接到微信。",
	}}
	adapter := NewWeChatAdapter(runner)

	challenge, err := adapter.BeginAuth(context.Background(), AuthInput{AppID: "app-1"})
	require.NoError(t, err)
	if challenge.Type != "qrcode" || challenge.QRCode == "" {
		t.Fatalf("challenge = %+v", challenge)
	}
	if challenge.ExpiresAt.IsZero() {
		t.Fatalf("ExpiresAt 未设置")
	}

	// 异步消费剩余事件，等待最长 200ms 让 progress 落地。
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		progress, _ := adapter.PollAuth(context.Background(), AuthInput{AppID: "app-1"})
		if progress.Status == AuthStatusBound {
			// stdout 不携带 wxid/userId（实测发现）；BoundIdentity 由 service 层
			// 在收到 bound 事件后调 openclaw channels list 或读 plugin state 补齐。
			// 此测试仅验证 bound 状态翻转。
			require.Equal(t, "openclaw-weixin", progress.ChannelName)
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("expected bound progress within 500ms")
}

func TestWeChatAdapterBeginAuthRejectsUnparsableOutput(t *testing.T) {
	runner := &fakeRunner{lines: []string{"hello world"}}
	adapter := NewWeChatAdapter(runner)

	_, err := adapter.BeginAuth(context.Background(), AuthInput{AppID: "app-1"})
	require.Error(t, err)
	progress, _ := adapter.PollAuth(context.Background(), AuthInput{AppID: "app-1"})
	require.Equal(t, AuthStatusFailed, progress.Status)
}

func TestWeChatAdapterBeginAuthDetectsExpiredFirst(t *testing.T) {
	// 极少见情况：plugin 加载完后直接出 expired（如 wechat 服务端拒绝）。
	runner := &fakeRunner{lines: []string{
		"[plugins] loaded 118 plugin(s)",
		"二维码已过期",
	}}
	adapter := NewWeChatAdapter(runner)

	_, err := adapter.BeginAuth(context.Background(), AuthInput{AppID: "app-1"})
	require.Error(t, err)
}

func TestWeChatAdapterPollAuthDefaultsToPending(t *testing.T) {
	adapter := NewWeChatAdapter(&fakeRunner{})
	progress, err := adapter.PollAuth(context.Background(), AuthInput{AppID: "missing"})
	require.NoError(t, err)
	require.Equal(t, AuthStatusPending, progress.Status)
}

type fakeRunner struct {
	lines []string
}

func (r *fakeRunner) StreamWeChatLogin(_ context.Context, _ AuthInput) (<-chan string, error) {
	ch := make(chan string, len(r.lines))
	for _, line := range r.lines {
		ch <- line
	}
	close(ch)
	return ch, nil
}
