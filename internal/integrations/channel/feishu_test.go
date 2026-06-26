package channel

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"oc-manager/internal/integrations/ocops"
)

// fakeFeishuRunner 是 FeishuRegisterRunner 的测试实现：一次性把预置事件塞入
// channel 后立即 close，模拟 oc-ops SSE 的事件流，便于断言 adapter 的消费时序。
type fakeFeishuRunner struct{ events []ocops.FeishuRegisterEvent }

// StreamFeishuRegister 返回携带预置事件的只读 channel；缓冲容量等于事件数，
// 确保塞入不阻塞，close 后 adapter 既能读到 qrcode 也能让后台 goroutine 读完剩余事件。
func (r *fakeFeishuRunner) StreamFeishuRegister(_ context.Context, _ AuthInput, _ string) (<-chan ocops.FeishuRegisterEvent, error) {
	ch := make(chan ocops.FeishuRegisterEvent, len(r.events))
	for _, e := range r.events {
		ch <- e
	}
	close(ch)
	return ch, nil
}

// TestFeishuAdapterScanReturnsQRThenCredentials 验证扫码模式：BeginAuth 返回二维码，
// 后台消费 credentials 事件后 TakeCredentials 可取出凭证，且 PollAuth 不泄露 secret。
func TestFeishuAdapterScanReturnsQRThenCredentials(t *testing.T) {
	runner := &fakeFeishuRunner{events: []ocops.FeishuRegisterEvent{
		{Event: "qrcode", URL: "https://open.feishu.cn/qr/x"},
		{Event: "credentials", AppID: "cli_x", AppSecret: "sec", Domain: "feishu", BotName: "Bot"},
	}}
	a := NewFeishuAdapter(runner)
	ch, err := a.BeginAuth(context.Background(), AuthInput{AppID: "app-1"})
	require.NoError(t, err)
	require.Equal(t, "qrcode", ch.Type)
	require.Equal(t, "https://open.feishu.cn/qr/x", ch.QRCode)

	// 等后台消费 credentials。
	var creds *FeishuCredentials
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if c, ok := a.TakeCredentials("app-1"); ok {
			creds = &c
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	require.NotNil(t, creds)
	require.Equal(t, "cli_x", creds.AppID)
	require.Equal(t, "sec", creds.AppSecret)
	require.Equal(t, "Bot", creds.BotName)

	// PollAuth 不得含 secret。
	p, _ := a.PollAuth(context.Background(), AuthInput{AppID: "app-1"})
	for _, v := range p.Metadata {
		require.NotEqual(t, "sec", v, "PollAuth 不得泄露 app_secret")
	}
}

// TestFeishuAdapterScanFailed 验证扫码失败事件→PollAuth 报 failed。
func TestFeishuAdapterScanFailed(t *testing.T) {
	runner := &fakeFeishuRunner{events: []ocops.FeishuRegisterEvent{
		{Event: "qrcode", URL: "u"},
		{Event: "failed", Reason: "registration timeout or denied"},
	}}
	a := NewFeishuAdapter(runner)
	_, err := a.BeginAuth(context.Background(), AuthInput{AppID: "app-1"})
	require.NoError(t, err)
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		p, _ := a.PollAuth(context.Background(), AuthInput{AppID: "app-1"})
		if p.Status == AuthStatusFailed {
			require.Contains(t, p.ErrorMessage, "timeout")
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("未达到 failed 状态")
}

// fakeFeishuProber 模拟 oc-ops 手填校验。
type fakeFeishuProber struct {
	ok      bool
	botName string
}

func (p *fakeFeishuProber) ProbeFeishu(_ context.Context, _ AuthInput, _, _, _ string) (bool, string, string, error) {
	return p.ok, p.botName, "", nil
}

// TestFeishuAdapterManualProbeOK 验证手填校验通过后置凭证 + bot_name。
func TestFeishuAdapterManualProbeOK(t *testing.T) {
	a := NewFeishuAdapter(nil)
	a.SetProber(&fakeFeishuProber{ok: true, botName: "Bot"})
	ch, err := a.BeginManual(context.Background(), AuthInput{AppID: "app-1"},
		FeishuCredentials{AppID: "cli_x", AppSecret: "sec", Domain: "feishu"})
	require.NoError(t, err)
	require.Equal(t, "feishu_manual", ch.Type)
	c, ok := a.TakeCredentials("app-1")
	require.True(t, ok)
	require.Equal(t, "Bot", c.BotName)
}

// TestFeishuAdapterManualProbeFail 验证校验失败返回错误且不置凭证。
func TestFeishuAdapterManualProbeFail(t *testing.T) {
	a := NewFeishuAdapter(nil)
	a.SetProber(&fakeFeishuProber{ok: false})
	_, err := a.BeginManual(context.Background(), AuthInput{AppID: "app-1"},
		FeishuCredentials{AppID: "cli_x", AppSecret: "bad", Domain: "feishu"})
	require.Error(t, err)
	_, ok := a.TakeCredentials("app-1")
	require.False(t, ok)
}
