// Package channel 是渠道适配层，把 worker handler 与具体 runtime 实现解耦。
// 微信扫码登录当前委托给 internal/integrations/hermes/wechat_runner.go 的
// WeixinRunner，后者经 oc-ops HTTP SSE 触发登录并翻译事件；保持向后兼容的
// type 名 DockerCommandRunner 和 method StreamWeChatLogin。
package channel

import (
	"context"

	"oc-manager/internal/integrations/hermes"
)

// DockerCommandRunner 是渠道适配层对外暴露的类型，委托给 hermes.WeixinRunner。
// 保持 type 名避免修改所有 caller；登录传输已从 docker exec 切换为 oc-ops HTTP SSE。
type DockerCommandRunner struct {
	// streamer 触发 oc-ops 渠道登录并订阅 SSE 事件流，生产实现为 *ocops.Client。
	streamer hermes.ChannelLoginStreamer
}

// NewDockerCommandRunner 工厂。
// streamer 满足 oc-ops SSE 登录能力（*ocops.Client）；每次 StreamWeChatLogin
// 调用时按 AuthInput.Endpoint 构造临时 hermes.WeixinRunner，把目标 app 实例坐标
// 注入，确保登录请求打到正确的 oc-ops 实例。
func NewDockerCommandRunner(streamer hermes.ChannelLoginStreamer) *DockerCommandRunner {
	return &DockerCommandRunner{streamer: streamer}
}

// StreamWeChatLogin 委托给 hermes.WeixinRunner，经 oc-ops HTTP SSE 触发微信登录。
// 返回 <-chan hermes.WeixinEvent，由上游 WeChatAdapter 消费。
func (r *DockerCommandRunner) StreamWeChatLogin(ctx context.Context, input AuthInput) (<-chan hermes.WeixinEvent, error) {
	// per-call 构造 runner，把 input.Endpoint（目标 app 实例的 oc-ops 坐标）注入，
	// 确保多 app 部署下登录请求路由到正确实例。
	runner := hermes.NewWeixinRunner(r.streamer, input.Endpoint)
	return runner.StreamWeChatLogin(ctx)
}

