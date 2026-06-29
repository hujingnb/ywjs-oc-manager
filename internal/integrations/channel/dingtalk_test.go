package channel

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"oc-manager/internal/domain"
	"oc-manager/internal/integrations/ocops"
)

// fakeDingtalkStatusClient 按预置返回桩 ChannelStatus / 错误，驱动 PollAuth 状态映射断言。
type fakeDingtalkStatusClient struct {
	st  ocops.ChannelStatus
	err error
}

func (f fakeDingtalkStatusClient) ChannelStatus(_ context.Context, _ ocops.Endpoint, _ string) (ocops.ChannelStatus, error) {
	return f.st, f.err
}

// fakeDingtalkResolver 控制坐标解析结果（supported=false 模拟 dev stub）。
type fakeDingtalkResolver struct {
	supported bool
	err       error
}

func (f fakeDingtalkResolver) Resolve(_ context.Context, _ string) (ocops.Endpoint, bool, error) {
	return ocops.Endpoint{}, f.supported, f.err
}

// TestDingtalkAdapter_PollAuth 覆盖连通态映射：connected→Bound、fatal→Failed、其余→Pending，
// 以及坐标解析失败/oc-ops 错误一律吞错返回 Pending（解绑重启窗口不误判失败）。
func TestDingtalkAdapter_PollAuth(t *testing.T) {
	cases := []struct {
		name      string                  // 场景
		resolver  fakeDingtalkResolver    // 坐标解析桩
		client    fakeDingtalkStatusClient // 连通态桩
		wantState AuthStatus              // 期望 AuthStatus
	}{
		// 引擎已连上钉钉开放平台 → 绑定成功
		{"connected→Bound", fakeDingtalkResolver{supported: true}, fakeDingtalkStatusClient{st: ocops.ChannelStatus{PlatformState: "connected"}}, AuthStatusBound},
		// 引擎报致命（钉钉实际不触发，但映射保留同构）
		{"fatal→Failed", fakeDingtalkResolver{supported: true}, fakeDingtalkStatusClient{st: ocops.ChannelStatus{PlatformState: "fatal", ErrorMessage: "boom"}}, AuthStatusFailed},
		// 连接中 → 继续等
		{"connecting→Pending", fakeDingtalkResolver{supported: true}, fakeDingtalkStatusClient{st: ocops.ChannelStatus{PlatformState: "connecting"}}, AuthStatusPending},
		// 空态 → 继续等（钉钉凭证错的典型表现：长期非 connected 直至 worker 退避超时）
		{"empty→Pending", fakeDingtalkResolver{supported: true}, fakeDingtalkStatusClient{st: ocops.ChannelStatus{PlatformState: ""}}, AuthStatusPending},
		// dev stub（supported=false）→ 等下次 poll
		{"resolve-unsupported→Pending", fakeDingtalkResolver{supported: false}, fakeDingtalkStatusClient{}, AuthStatusPending},
		// oc-ops 不可达（重启窗口）→ 吞错等，不判失败
		{"ocops-error→Pending", fakeDingtalkResolver{supported: true}, fakeDingtalkStatusClient{err: errors.New("unreachable")}, AuthStatusPending},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := NewDingtalkAdapter(c.client, c.resolver)
			pr, err := a.PollAuth(context.Background(), AuthInput{AppID: "a1"})
			require.NoError(t, err)
			assert.Equal(t, c.wantState, pr.Status)
		})
	}
}

// TestDingtalkAdapter_Type_Begin 校验 Type 标识与 BeginAuth 占位（钉钉无扫码发起，凭证经表单提交）。
func TestDingtalkAdapter_Type_Begin(t *testing.T) {
	a := NewDingtalkAdapter(fakeDingtalkStatusClient{}, fakeDingtalkResolver{})
	// Type 返回 dingtalk，与 domain.ChannelTypeDingTalk 一致
	assert.Equal(t, domain.ChannelTypeDingTalk, a.Type())
	// BeginAuth 占位必报错（钉钉不入 channel_start_login）
	_, err := a.BeginAuth(context.Background(), AuthInput{})
	require.Error(t, err)
}
