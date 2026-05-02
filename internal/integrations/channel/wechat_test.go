package channel

import (
	"context"
	"testing"
	"time"
)

func TestWeChatAdapterBeginAuthReturnsQRCodeChallenge(t *testing.T) {
	// Sprint 0 POC 实测样本：plugin loading 噪声 + 中文提示行 + ASCII QR + URL + 等待提示。
	// 上游真实 stdout 形态见 docs/superpowers/poc/.../06-qrcode-format.md。
	runner := &fakeRunner{lines: []string{
		"[plugins] loading anthropic from /root/.openclaw/...",
		"[plugins] loaded 118 plugin(s) (70 attempted) in 11035.8ms",
		"正在启动...",
		"用手机微信扫描以下二维码，以继续连接：",
		"▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄▄",
		"若二维码未能显示或无法使用，你可以访问以下链接以继续：",
		"https://liteapp.weixin.qq.com/q/7GiQu1?qrcode=85e18acc56ebd5937ad4caa5fe1b01a1&bot_type=3",
		"正在等待操作...",
		"已连接微信账号 alice@wxid_xyz",
	}}
	adapter := NewWeChatAdapter(runner)

	challenge, err := adapter.BeginAuth(context.Background(), AuthInput{AppID: "app-1"})
	if err != nil {
		t.Fatalf("BeginAuth() error = %v", err)
	}
	if challenge.Type != "qrcode" || challenge.QRCode == "" {
		t.Fatalf("challenge = %+v", challenge)
	}
	if challenge.ExpiresAt.IsZero() {
		t.Fatalf("ExpiresAt 未设置")
	}

	// 异步消费剩余事件，等待最长 200ms 让 progress 落地。
	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) {
		progress, _ := adapter.PollAuth(context.Background(), AuthInput{AppID: "app-1"})
		if progress.Status == AuthStatusBound {
			if progress.BoundIdentity == "" {
				t.Fatalf("bound identity 为空")
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("expected bound progress within 200ms")
}

func TestWeChatAdapterBeginAuthRejectsUnparsableOutput(t *testing.T) {
	runner := &fakeRunner{lines: []string{"hello world"}}
	adapter := NewWeChatAdapter(runner)

	_, err := adapter.BeginAuth(context.Background(), AuthInput{AppID: "app-1"})
	if err == nil {
		t.Fatalf("expected error for unparsable first line")
	}
	progress, _ := adapter.PollAuth(context.Background(), AuthInput{AppID: "app-1"})
	if progress.Status != AuthStatusFailed {
		t.Fatalf("progress status = %s, want failed", progress.Status)
	}
}

func TestWeChatAdapterBeginAuthDetectsExpiredFirst(t *testing.T) {
	// 极少见情况：plugin 加载完后直接出 expired（如 wechat 服务端拒绝）。
	runner := &fakeRunner{lines: []string{
		"[plugins] loaded 118 plugin(s)",
		"二维码已过期",
	}}
	adapter := NewWeChatAdapter(runner)

	_, err := adapter.BeginAuth(context.Background(), AuthInput{AppID: "app-1"})
	if err == nil {
		t.Fatalf("expected error when first decision event is expired")
	}
}

func TestWeChatAdapterPollAuthDefaultsToPending(t *testing.T) {
	adapter := NewWeChatAdapter(&fakeRunner{})
	progress, err := adapter.PollAuth(context.Background(), AuthInput{AppID: "missing"})
	if err != nil {
		t.Fatalf("PollAuth() error = %v", err)
	}
	if progress.Status != AuthStatusPending {
		t.Fatalf("progress = %+v", progress)
	}
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
