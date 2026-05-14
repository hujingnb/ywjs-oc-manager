package channel

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"oc-manager/internal/domain"
	"oc-manager/internal/integrations/hermes"
)

// CommandRunner 抽象通过 agent docker exec 拉起 Hermes 扫码命令并返回事件 channel 的能力。
// Hermes 时代事件类型升级为 hermes.WeixinEvent（legacy OpenClaw 时代是 <-chan string）。
type CommandRunner interface {
	StreamWeChatLogin(ctx context.Context, input AuthInput) (<-chan hermes.WeixinEvent, error)
}

// WeChatAdapter 使用 CommandRunner 把 Hermes 微信登录事件解析为统一的 AuthChallenge / AuthProgress。
//
// 当前实现不持久化挑战；service 层会在调用 BeginAuth 后把结果写入 channel_bindings 表，
// 后续 PollAuth 通过 service 层补充 metadata。这里 adapter 仅负责解析协议。
type WeChatAdapter struct {
	runner CommandRunner

	mu       sync.Mutex
	progress map[string]AuthProgress
}

// NewWeChatAdapter 创建微信 adapter。
func NewWeChatAdapter(runner CommandRunner) *WeChatAdapter {
	return &WeChatAdapter{runner: runner, progress: map[string]AuthProgress{}}
}

// Type 返回 wechat。
func (a *WeChatAdapter) Type() string { return domain.ChannelTypeWeChat }

// BeginAuth 启动一次微信登录，并返回首条 qrcode 事件作为 challenge。
// 启动后 adapter 会异步把后续事件累积到内部 progress 状态，PollAuth 直接读取。
func (a *WeChatAdapter) BeginAuth(ctx context.Context, input AuthInput) (AuthChallenge, error) {
	if a.runner == nil {
		return AuthChallenge{}, errors.New("wechat adapter 未配置 CommandRunner")
	}
	events, err := a.runner.StreamWeChatLogin(ctx, input)
	if err != nil {
		return AuthChallenge{}, fmt.Errorf("启动微信登录失败: %w", err)
	}
	// 持续读事件流直到拿到 QRCode 或失败事件；QRCode 出现后把剩余消费交给后台 goroutine。
	for ev := range events {
		switch ev.Type {
		case hermes.WeixinEventQRCode:
			if ev.QRCodeURL == "" {
				a.recordProgress(input.AppID, AuthProgress{Status: AuthStatusFailed, ErrorMessage: "二维码事件缺少 URL", UpdatedAt: time.Now()})
				return AuthChallenge{}, errors.New("二维码事件缺少 URL")
			}
			a.recordProgress(input.AppID, AuthProgress{Status: AuthStatusPending, UpdatedAt: time.Now()})
			go a.consumeStream(input.AppID, events)
			// iLink QR 默认寿命 5 分钟(参考 OpenClaw 时代 CONTRACT 实测);
			// Hermes weixin.qr_login 内部 QR_TIMEOUT_MS=35s 是 polling 间隔,
			// 不是 QR 寿命。前端按 ExpiresAt 显示倒计时;过期后用户点"刷新二维码"
			// 触发新一轮 ChannelStartLogin。
			return AuthChallenge{
				Type:      "qrcode",
				QRCode:    ev.QRCodeURL,
				ExpiresAt: time.Now().Add(5 * time.Minute),
			}, nil
		case hermes.WeixinEventFailed:
			a.recordProgress(input.AppID, AuthProgress{Status: AuthStatusFailed, ErrorMessage: ev.Error, UpdatedAt: time.Now()})
			return AuthChallenge{}, fmt.Errorf("Hermes 登录失败: %s", ev.Error)
		case hermes.WeixinEventBound:
			// 极少数情况下可能直接 bound（无 QR 扫码步骤）；认为成功。
			a.recordProgress(input.AppID, AuthProgress{
				Status:        AuthStatusBound,
				BoundIdentity: ev.AccountID,
				UpdatedAt:     time.Now(),
			})
			return AuthChallenge{Type: "qrcode", QRCode: ""}, nil
		}
	}
	a.recordProgress(input.AppID, AuthProgress{
		Status:       AuthStatusFailed,
		ErrorMessage: "Hermes 未输出可解析的登录事件",
		UpdatedAt:    time.Now(),
	})
	return AuthChallenge{}, errors.New("Hermes 未输出可解析的登录事件")
}

// PollAuth 返回最新的登录进度。
func (a *WeChatAdapter) PollAuth(_ context.Context, input AuthInput) (AuthProgress, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	progress, ok := a.progress[input.AppID]
	if !ok {
		return AuthProgress{Status: AuthStatusPending, UpdatedAt: time.Now()}, nil
	}
	return progress, nil
}

// consumeStream 在后台消费剩余的 WeixinEvent，直到 channel 关闭。
func (a *WeChatAdapter) consumeStream(appID string, events <-chan hermes.WeixinEvent) {
	for ev := range events {
		switch ev.Type {
		case hermes.WeixinEventBound:
			a.recordProgress(appID, AuthProgress{
				Status:        AuthStatusBound,
				BoundIdentity: ev.AccountID,
				ChannelName:   ev.AccountID,
				UpdatedAt:     time.Now(),
			})
			return
		case hermes.WeixinEventFailed:
			a.recordProgress(appID, AuthProgress{Status: AuthStatusFailed, ErrorMessage: ev.Error, UpdatedAt: time.Now()})
			return
		default:
			a.recordProgress(appID, AuthProgress{Status: AuthStatusPending, UpdatedAt: time.Now()})
		}
	}
}

func (a *WeChatAdapter) recordProgress(appID string, progress AuthProgress) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.progress[appID] = progress
}
