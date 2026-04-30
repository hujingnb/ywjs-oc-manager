package channel

import (
	"context"
	"testing"
	"time"
)

func TestWeChatAdapterBeginAuthReturnsQRCodeChallenge(t *testing.T) {
	expires := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	runner := &fakeRunner{lines: []string{
		`{"event":"qrcode","qrcode":"data:image/png;base64,xxx","expires_at":"` + expires + `"}`,
		`{"event":"bound","bound_identity":"alice","channel_name":"alice@wechat"}`,
	}}
	adapter := NewWeChatAdapter(runner)

	challenge, err := adapter.BeginAuth(context.Background(), AuthInput{AppID: "app-1"})
	if err != nil {
		t.Fatalf("BeginAuth() error = %v", err)
	}
	if challenge.Type != "qrcode" || challenge.QRCode == "" {
		t.Fatalf("challenge = %+v", challenge)
	}

	// 异步消费剩余事件，等待最长 200ms 让 progress 落地。
	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) {
		progress, _ := adapter.PollAuth(context.Background(), AuthInput{AppID: "app-1"})
		if progress.Status == AuthStatusBound {
			if progress.BoundIdentity != "alice" {
				t.Fatalf("bound identity = %s", progress.BoundIdentity)
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

func TestWeChatAdapterBeginAuthDetectsNonQRCodeFirst(t *testing.T) {
	runner := &fakeRunner{lines: []string{`{"event":"bound","bound_identity":"alice"}`}}
	adapter := NewWeChatAdapter(runner)

	_, err := adapter.BeginAuth(context.Background(), AuthInput{AppID: "app-1"})
	if err == nil {
		t.Fatalf("expected error for first event not being qrcode")
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
