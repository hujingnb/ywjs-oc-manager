package hermes

import (
	"context"
	"fmt"

	"oc-manager/internal/integrations/ocops"
)

// ChannelLoginStreamer 抽象「触发某渠道登录并订阅其 SSE 事件流」的能力。
// 生产实现为 *ocops.Client（其 ChannelLogin 方法满足本接口），manager 端通过
// oc-ops HTTP SSE 链路替代了原先的 docker exec 流式登录。
//
// ep 是目标 app 实例的 oc-ops 调用坐标（基址 + per-app token），由 worker 在
// 装配阶段经 OcOpsResolver 解析后注入；channel 取 "weixin"。
type ChannelLoginStreamer interface {
	ChannelLogin(ctx context.Context, ep ocops.Endpoint, channel string) (<-chan ocops.ChannelLoginEvent, error)
}

// WeixinEventType 表示扫码登录过程中产生的事件类型。
type WeixinEventType string

const (
	// WeixinEventQRCode 收到二维码 URL（供前端展示）。
	WeixinEventQRCode WeixinEventType = "qrcode"
	// WeixinEventBound 扫码成功，oc-ops 侧已完成凭证落盘到 hermes 自管目录
	// （/opt/data/weixin/accounts/），manager 仅需触发 hermes 重启重新读 platforms 配置。
	WeixinEventBound WeixinEventType = "bound"
	// WeixinEventFailed 登录失败或超时。
	WeixinEventFailed WeixinEventType = "failed"
)

// WeixinEvent 是 runner 推给上层的事件。
// 凭证由 oc-ops 侧自管，manager 不再透传 account_id/token/base_url 等字段；
// Bound 事件只表达「扫码完成」的信号，身份由 BindingResolver 从 plugin state 解析。
type WeixinEvent struct {
	Type      WeixinEventType
	QRCodeURL string // QRCode 类型用
	Error     string // Failed 类型用
}

// WeixinRunner 是微信扫码登录的协调器。
// 通过 ChannelLoginStreamer（oc-ops HTTP SSE）触发 weixin 渠道登录，把
// ocops.ChannelLoginEvent（qrcode/bound/timeout/failed）翻译成 WeixinEvent：
//   - qrcode → QRCode 事件（携带 URL）
//   - bound  → Bound 事件
//   - timeout/failed → Failed 事件（reason 写入 Error）
type WeixinRunner struct {
	// streamer 触发登录并订阅 SSE 事件流。
	streamer ChannelLoginStreamer
	// endpoint 是目标 app 实例的 oc-ops 调用坐标，由调用方注入。
	endpoint ocops.Endpoint
}

// NewWeixinRunner 创建 runner。
// streamer 满足 oc-ops SSE 登录能力（*ocops.Client）；endpoint 指定目标 app 实例坐标。
func NewWeixinRunner(streamer ChannelLoginStreamer, endpoint ocops.Endpoint) *WeixinRunner {
	return &WeixinRunner{streamer: streamer, endpoint: endpoint}
}

// StreamWeChatLogin 触发一次微信扫码登录，返回事件 channel。
// channel 在登录结束（成功/失败/超时）或 SSE 流关闭后关闭。
// 调用方负责消费 channel 直到关闭；不消费会阻塞 runner goroutine。
//
// 登录走 oc-ops HTTP SSE（POST /oc/channels/weixin/login），由 oc-ops 侧完成
// 扫码 + 凭证落盘到 /opt/data/weixin/accounts/，manager 不再解析凭证字段。
func (r *WeixinRunner) StreamWeChatLogin(ctx context.Context) (<-chan WeixinEvent, error) {
	source, err := r.streamer.ChannelLogin(ctx, r.endpoint, "weixin")
	if err != nil {
		return nil, fmt.Errorf("触发 oc-ops 微信登录失败: %w", err)
	}

	events := make(chan WeixinEvent, 8)
	go func() {
		defer close(events)
		// 逐条把 oc-ops SSE 事件翻译为 WeixinEvent。
		// timeout/failed 统一映射成 Failed；failed 携带 reason，timeout 用事件名兜底。
		for ev := range source {
			switch ev.Event {
			case "qrcode":
				events <- WeixinEvent{Type: WeixinEventQRCode, QRCodeURL: ev.URL}
			case "bound":
				events <- WeixinEvent{Type: WeixinEventBound}
			case "failed":
				reason := ev.Reason
				if reason == "" {
					reason = "failed"
				}
				events <- WeixinEvent{Type: WeixinEventFailed, Error: reason}
			case "timeout":
				reason := ev.Reason
				if reason == "" {
					reason = "timeout"
				}
				events <- WeixinEvent{Type: WeixinEventFailed, Error: reason}
			}
		}
	}()
	return events, nil
}
