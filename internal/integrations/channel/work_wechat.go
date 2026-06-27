package channel

import (
	"context"
	"errors"
	"time"

	"oc-manager/internal/domain"
)

// WorkWeChatAdapter 实现 ChannelAdapter：企业微信无扫码、无 SSE，凭证经 manager 表单同步注入。
// 它只承载「连通状态检查」——PollAuth 经 oc-ops ChannelStatus(work_wechat) 读 platforms.wecom，
// 插进 worker 通用 check 路径（channel_login.go 非飞书分支），无需飞书式两阶段特判。
// BeginAuth 为占位：企业微信不入 channel_start_login，凭证经 POST /channels/work_wechat/auth 提交。
type WorkWeChatAdapter struct {
	// ops 查 oc-ops 渠道连通态（platform_state）。
	ops channelStatusClient
	// resolver 把 appID 解析为 oc-ops 调用坐标及 dev stub 标志。
	resolver OcOpsLocationResolver
}

// NewWorkWeChatAdapter 构造企业微信 adapter；ops 与 resolver 均不得为 nil。
func NewWorkWeChatAdapter(ops channelStatusClient, resolver OcOpsLocationResolver) *WorkWeChatAdapter {
	return &WorkWeChatAdapter{ops: ops, resolver: resolver}
}

// Type 返回 work_wechat（供 Registry 路由；与 oc-ops WorkWechatChannelOps.channel 注册键一致）。
func (a *WorkWeChatAdapter) Type() string { return domain.ChannelTypeWorkWeChat }

// BeginAuth 占位：企业微信无扫码发起，凭证经表单提交，故不应被 worker 调用（不入 channel_start_login）。
func (a *WorkWeChatAdapter) BeginAuth(_ context.Context, _ AuthInput) (AuthChallenge, error) {
	return AuthChallenge{}, errors.New("企业微信不支持扫码发起，凭证经 POST /channels/work_wechat/auth 表单提交")
}

// PollAuth 查 oc-ops 企业微信连通态并映射为统一 AuthStatus。
//
// 关键容错：坐标解析失败 / oc-ops 不可达（解绑重启窗口）/ dev stub 一律返回 Pending，
// 吞瞬时错误让 worker 通用分支按退避 re-enqueue，不把 check job 判失败。
// 仅 platform_state 明确为 connected/fatal 时给终态（Bound/Failed）。
func (a *WorkWeChatAdapter) PollAuth(ctx context.Context, input AuthInput) (AuthProgress, error) {
	now := time.Now()
	ep, supported, err := a.resolver.Resolve(ctx, input.AppID)
	if err != nil || !supported {
		// 解析失败（基础设施抖动）或 dev stub（无真实 hermes）：等下次 poll。
		return AuthProgress{Status: AuthStatusPending, UpdatedAt: now}, nil
	}
	st, err := a.ops.ChannelStatus(ctx, ep, domain.ChannelTypeWorkWeChat)
	if err != nil {
		// oc-ops 不可达（pod 重启窗口）：吞错继续等，不判失败。
		return AuthProgress{Status: AuthStatusPending, UpdatedAt: now}, nil
	}
	switch st.PlatformState {
	case "connected":
		return AuthProgress{Status: AuthStatusBound, UpdatedAt: now}, nil
	case "fatal":
		return AuthProgress{Status: AuthStatusFailed, ErrorMessage: st.ErrorMessage, UpdatedAt: now}, nil
	default:
		// connecting / retrying / disconnected / 空：连接中，继续等。
		return AuthProgress{Status: AuthStatusPending, UpdatedAt: now}, nil
	}
}

// 确保实现 ChannelAdapter 接口（编译期校验）。
var _ ChannelAdapter = (*WorkWeChatAdapter)(nil)
