package channel

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"oc-manager/internal/integrations/ocops"
)

// fakeChannelStatusClient 实现 channelStatusClient，返回预设的 ChannelStatus 或 error。
type fakeChannelStatusClient struct {
	status      ocops.ChannelStatus
	err         error
	gotEndpoint ocops.Endpoint // 记录传入的 Endpoint，供断言验证
	gotChannel  string         // 记录传入的 channel 名，供断言验证
}

func (f *fakeChannelStatusClient) ChannelStatus(_ context.Context, ep ocops.Endpoint, channel string) (ocops.ChannelStatus, error) {
	f.gotEndpoint = ep
	f.gotChannel = channel
	return f.status, f.err
}

// fakeOcOpsLocationResolver 实现 OcOpsLocationResolver，返回预设的 Endpoint/supported/error。
type fakeOcOpsLocationResolver struct {
	ep        ocops.Endpoint
	supported bool
	err       error
}

func (f *fakeOcOpsLocationResolver) Resolve(_ context.Context, _ string) (ocops.Endpoint, bool, error) {
	return f.ep, f.supported, f.err
}

// testEndpoint 是各测试用例共用的 Endpoint 预设值，便于断言传参正确性。
var testEndpoint = ocops.Endpoint{BaseURL: "http://app-x.ocops:8080", Token: "tok-x"}

// TestOcOpsBindingResolver_BoundWithAccountID 验证：渠道已绑定且 AccountID 非空时返回该 AccountID。
func TestOcOpsBindingResolver_BoundWithAccountID(t *testing.T) {
	// 正常路径：hermes 侧已绑定，AccountID 有值，直接返回。
	ops := &fakeChannelStatusClient{
		status: ocops.ChannelStatus{Channel: "weixin", Bound: true, AccountID: "o9cq800xszCM8jyoS9YpRKpvAN9c@im.wechat"},
	}
	res := &fakeOcOpsLocationResolver{ep: testEndpoint, supported: true}
	r := NewOcOpsBindingResolver(ops, res)

	got, err := r.ResolveWeChatBoundIdentity(context.Background(), "app-1")
	require.NoError(t, err)
	assert.Equal(t, "o9cq800xszCM8jyoS9YpRKpvAN9c@im.wechat", got)
}

// TestOcOpsBindingResolver_PassesEndpointAndWeixinChannel 验证：传给 ChannelStatus 的 Endpoint
// 来自 resolver，channel 名固定为 "weixin"（hermes 侧枚举，非 manager 侧 "wechat"）。
func TestOcOpsBindingResolver_PassesEndpointAndWeixinChannel(t *testing.T) {
	// 断言参数透传正确：Endpoint 从 resolver 来，channel 名写死 "weixin"。
	ops := &fakeChannelStatusClient{
		status: ocops.ChannelStatus{Bound: true, AccountID: "wxid_abc"},
	}
	res := &fakeOcOpsLocationResolver{ep: testEndpoint, supported: true}
	r := NewOcOpsBindingResolver(ops, res)

	_, err := r.ResolveWeChatBoundIdentity(context.Background(), "app-1")
	require.NoError(t, err)
	// 断言 channel 名是 "weixin"（hermes 约定），而非 manager 侧的 "wechat"。
	assert.Equal(t, "weixin", ops.gotChannel)
	// 断言 Endpoint 原样透传自 resolver。
	assert.Equal(t, testEndpoint, ops.gotEndpoint)
}

// TestOcOpsBindingResolver_NotBound 验证：渠道绑定状态为 false（扫码未完成）→ ErrIdentityUnavailable。
// 调用方不应将 binding 推到 failed，等下次 polling 重试。
func TestOcOpsBindingResolver_NotBound(t *testing.T) {
	// 边界条件：Bound=false，账号尚未落定，等重试。
	ops := &fakeChannelStatusClient{
		status: ocops.ChannelStatus{Bound: false, AccountID: ""},
	}
	res := &fakeOcOpsLocationResolver{ep: testEndpoint, supported: true}
	r := NewOcOpsBindingResolver(ops, res)

	_, err := r.ResolveWeChatBoundIdentity(context.Background(), "app-1")
	require.ErrorIs(t, err, ErrIdentityUnavailable)
}

// TestOcOpsBindingResolver_BoundButEmptyAccountID 验证：渠道已绑定但 AccountID 为空
// （hermes 侧账号尚未落定）→ ErrIdentityUnavailable，避免写入空身份。
func TestOcOpsBindingResolver_BoundButEmptyAccountID(t *testing.T) {
	// 边界条件：Bound=true 但 AccountID 空，身份尚不可用，等下次 poll。
	ops := &fakeChannelStatusClient{
		status: ocops.ChannelStatus{Bound: true, AccountID: ""},
	}
	res := &fakeOcOpsLocationResolver{ep: testEndpoint, supported: true}
	r := NewOcOpsBindingResolver(ops, res)

	_, err := r.ResolveWeChatBoundIdentity(context.Background(), "app-1")
	require.ErrorIs(t, err, ErrIdentityUnavailable)
}

// TestOcOpsBindingResolver_DevStub 验证：resolver 返回 supported=false（dev stub 镜像，
// 无真实 hermes）→ 返回 ErrIdentityUnavailable，且不调用 ChannelStatus。
func TestOcOpsBindingResolver_DevStub(t *testing.T) {
	// dev stub 场景：镜像以 -dev 结尾，oc-ops 没有真实服务，不应发起 HTTP 调用。
	ops := &fakeChannelStatusClient{} // 若被调用则 gotChannel 会有值
	res := &fakeOcOpsLocationResolver{ep: testEndpoint, supported: false}
	r := NewOcOpsBindingResolver(ops, res)

	_, err := r.ResolveWeChatBoundIdentity(context.Background(), "app-1")
	require.ErrorIs(t, err, ErrIdentityUnavailable)
	// 确认 ChannelStatus 未被调用（channel 名仍为零值）。
	assert.Equal(t, "", ops.gotChannel, "dev stub 不应调用 ChannelStatus")
}

// TestOcOpsBindingResolver_ResolverError 验证：resolver.Resolve 返回 error 时，错误透出给调用方。
// 属于基础设施故障，不应吞掉错误。
func TestOcOpsBindingResolver_ResolverError(t *testing.T) {
	// 异常路径：resolver 报错（如 app 不存在或网络问题），直接透出。
	resolveErr := errors.New("app 查询失败")
	ops := &fakeChannelStatusClient{}
	res := &fakeOcOpsLocationResolver{err: resolveErr}
	r := NewOcOpsBindingResolver(ops, res)

	_, err := r.ResolveWeChatBoundIdentity(context.Background(), "app-1")
	require.Error(t, err)
	// 原始 error 应被包装透出，用 errors.Is 或字符串检查均可。
	assert.ErrorContains(t, err, "app 查询失败")
}

// TestOcOpsBindingResolver_ChannelStatusError 验证：ChannelStatus RPC 报错时，错误透出给调用方。
// oc-ops 侧不可达或内部错误，属于基础设施故障。
func TestOcOpsBindingResolver_ChannelStatusError(t *testing.T) {
	// 异常路径：oc-ops 查询失败（如 pod 未就绪、连接超时）。
	opsErr := errors.New("oc-ops 连接超时")
	ops := &fakeChannelStatusClient{err: opsErr}
	res := &fakeOcOpsLocationResolver{ep: testEndpoint, supported: true}
	r := NewOcOpsBindingResolver(ops, res)

	_, err := r.ResolveWeChatBoundIdentity(context.Background(), "app-1")
	require.Error(t, err)
	assert.ErrorContains(t, err, "oc-ops 连接超时")
}
