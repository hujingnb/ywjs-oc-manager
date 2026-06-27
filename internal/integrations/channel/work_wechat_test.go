package channel

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"oc-manager/internal/integrations/ocops"
)

// fakeWWLocation 实现 OcOpsLocationResolver：按需返回 supported / 错误。
type fakeWWLocation struct {
	supported bool
	err       error
}

func (f fakeWWLocation) Resolve(_ context.Context, _ string) (ocops.Endpoint, bool, error) {
	return ocops.Endpoint{}, f.supported, f.err
}

// fakeWWStatus 实现 channelStatusClient：返回预置 ChannelStatus / 错误。
type fakeWWStatus struct {
	st  ocops.ChannelStatus
	err error
}

func (f fakeWWStatus) ChannelStatus(_ context.Context, _ ocops.Endpoint, _ string) (ocops.ChannelStatus, error) {
	return f.st, f.err
}

// TestWorkWeChatAdapter_PollAuth 覆盖企业微信连通态映射的五种场景。
func TestWorkWeChatAdapter_PollAuth(t *testing.T) {
	cases := []struct {
		name   string              // 场景名
		loc    fakeWWLocation      // 坐标解析行为
		st     ocops.ChannelStatus // oc-ops 返回的连通态
		stErr  error               // oc-ops 查询错误
		expect AuthStatus          // 期望对外状态
	}{
		// platform_state=connected → 已连上企业微信开放平台 → Bound
		{"connected→bound", fakeWWLocation{supported: true}, ocops.ChannelStatus{PlatformState: "connected"}, nil, AuthStatusBound},
		// platform_state=fatal → 凭证无效等致命错误 → Failed
		{"fatal→failed", fakeWWLocation{supported: true}, ocops.ChannelStatus{PlatformState: "fatal", ErrorMessage: "invalid secret"}, nil, AuthStatusFailed},
		// 连接中（connecting/空）→ Pending，继续等
		{"connecting→pending", fakeWWLocation{supported: true}, ocops.ChannelStatus{PlatformState: "connecting"}, nil, AuthStatusPending},
		// oc-ops 不可达（重启窗口）→ 吞错返回 Pending，不判失败
		{"oc-ops 错误→pending", fakeWWLocation{supported: true}, ocops.ChannelStatus{}, errors.New("connection refused"), AuthStatusPending},
		// dev stub（supported=false）→ Pending
		{"dev stub→pending", fakeWWLocation{supported: false}, ocops.ChannelStatus{}, nil, AuthStatusPending},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := NewWorkWeChatAdapter(fakeWWStatus{st: c.st, err: c.stErr}, c.loc)
			got, err := a.PollAuth(context.Background(), AuthInput{AppID: "a1"})
			require.NoError(t, err)
			assert.Equal(t, c.expect, got.Status)
			if c.expect == AuthStatusFailed {
				assert.Equal(t, "invalid secret", got.ErrorMessage)
			}
		})
	}
}

// TestWorkWeChatAdapter_Type_Begin 覆盖 Type 标识与 BeginAuth 占位（企业微信无扫码发起）。
func TestWorkWeChatAdapter_Type_Begin(t *testing.T) {
	a := NewWorkWeChatAdapter(fakeWWStatus{}, fakeWWLocation{supported: true})
	assert.Equal(t, "work_wechat", a.Type())
	_, err := a.BeginAuth(context.Background(), AuthInput{AppID: "a1"})
	require.Error(t, err) // 占位：企业微信凭证经表单提交，不走 adapter 发起
}
