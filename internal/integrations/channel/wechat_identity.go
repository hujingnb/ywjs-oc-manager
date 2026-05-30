package channel

import (
	"context"
	"errors"
	"fmt"

	"oc-manager/internal/integrations/ocops"
)

// BindingResolver 抽象从 oc-ops 取出真实账号标识的能力。
//
// 微信扫码绑定完成后，manager 须调用本接口从 oc-ops ChannelStatus 取出 AccountID，
// 写入 channel_bindings.bound_identity。
// 签名由 (nodeID,containerID) 改为 appID：spec-A 改走 oc-ops HTTP，不再依赖 docker exec。
type BindingResolver interface {
	ResolveWeChatBoundIdentity(ctx context.Context, appID string) (string, error)
}

// channelStatusClient 窄接口：查询 oc-ops 渠道绑定状态（仅取 AccountID）。
// 由 *ocops.Client 满足；channel 包对 ocops 包的依赖仅止于此接口，避免过度耦合。
type channelStatusClient interface {
	ChannelStatus(ctx context.Context, ep ocops.Endpoint, channel string) (ocops.ChannelStatus, error)
}

// OcOpsLocationResolver 由 appID 解析 oc-ops Endpoint 与是否支持（Supported=false 代表 dev stub）。
// main 包用 service.OcOpsResolver 适配注入，隔离 channel→service 循环依赖。
type OcOpsLocationResolver interface {
	Resolve(ctx context.Context, appID string) (ep ocops.Endpoint, supported bool, err error)
}

// OcOpsBindingResolver 通过 oc-ops ChannelStatus 接口解析微信绑定身份（AccountID）。
// 取代旧 DockerBindingResolver（docker exec 读容器内 plugin state 文件）。
type OcOpsBindingResolver struct {
	// ops 调用 oc-ops ChannelStatus RPC 查询渠道绑定状态。
	ops channelStatusClient
	// resolver 把 appID 解析为 oc-ops 调用坐标及 dev stub 标志。
	resolver OcOpsLocationResolver
}

// NewOcOpsBindingResolver 构造 OcOpsBindingResolver；ops 与 resolver 均不得为 nil。
func NewOcOpsBindingResolver(ops channelStatusClient, resolver OcOpsLocationResolver) *OcOpsBindingResolver {
	return &OcOpsBindingResolver{ops: ops, resolver: resolver}
}

// ResolveWeChatBoundIdentity 向 oc-ops 查询微信渠道绑定状态，返回 AccountID。
//
// 业务逻辑：
//  1. 由 resolver 解析 appID 对应的 oc-ops 坐标；resolver 报错直接透出（基础设施故障）。
//  2. supported=false（dev stub 镜像，无真实 hermes）→ 返回 ErrIdentityUnavailable，
//     语义与旧实现一致：调用方不应把 binding 标记 failed，等下次 polling 重试。
//  3. 向 oc-ops 查 ChannelStatus（weixin 是 hermes 侧的渠道名，区别于 manager 侧的 "wechat"）。
//  4. Bound=false 或 AccountID 为空 → 绑定尚未完成/账号尚未落定，返回 ErrIdentityUnavailable。
//  5. 否则返回 AccountID。
func (r *OcOpsBindingResolver) ResolveWeChatBoundIdentity(ctx context.Context, appID string) (string, error) {
	// Step 1：解析 oc-ops 坐标。
	ep, supported, err := r.resolver.Resolve(ctx, appID)
	if err != nil {
		return "", fmt.Errorf("解析 oc-ops 坐标失败: %w", err)
	}

	// Step 2：dev stub 实例没有真实 hermes，无法取得身份，等下次 poll。
	if !supported {
		return "", ErrIdentityUnavailable
	}

	// Step 3：查询 oc-ops weixin 渠道状态。
	// "weixin" 是 hermes/oc-ops 侧的渠道名（见 hermes/wechat_runner.go:68）；
	// manager 侧枚举值 domain.ChannelTypeWeChat="wechat" 是另一套命名，不要混用。
	st, err := r.ops.ChannelStatus(ctx, ep, "weixin")
	if err != nil {
		return "", fmt.Errorf("查询 oc-ops 微信渠道状态失败: %w", err)
	}

	// Step 4：绑定未完成或账号尚未落定，等下次 poll 重试。
	if !st.Bound || st.AccountID == "" {
		return "", ErrIdentityUnavailable
	}

	return st.AccountID, nil
}

// ErrIdentityUnavailable 表示微信身份暂不可得（绑定未完成、dev stub 或 oc-ops 尚未返回绑定账号）。
// 调用方应等下次 polling 重试，不应把 binding 推到 failed。
var ErrIdentityUnavailable = errors.New("微信账号身份暂不可用")
