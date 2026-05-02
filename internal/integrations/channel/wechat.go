package channel

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"oc-manager/internal/domain"
	"oc-manager/internal/integrations/openclaw"
)

// CommandRunner 抽象通过 agent docker exec 拉起 OpenClaw 命令并按行返回 stdout 的能力。
// 实现可以是基于 docker proxy 的 stream，也可以是测试用的内存版。
type CommandRunner interface {
	StreamWeChatLogin(ctx context.Context, input AuthInput) (<-chan string, error)
}

// WeChatAdapter 使用 CommandRunner 把 OpenClaw 微信登录的 stdout 解析为统一的 AuthChallenge / AuthProgress。
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
	stream, err := a.runner.StreamWeChatLogin(ctx, input)
	if err != nil {
		return AuthChallenge{}, fmt.Errorf("启动微信登录失败: %w", err)
	}
	// Sprint 0 POC 实测：上游 stdout 前 11 秒是 plugin loading 噪声，后续才出现二维码 URL。
	// 这里持续读 stream 直到拿到 qrcode/expired/failed 中任一事件；pending 与 unparsable 行都跳过。
	event, ok := readFirstAuthEvent(stream)
	if !ok {
		a.recordProgress(input.AppID, AuthProgress{
			Status:       AuthStatusFailed,
			ErrorMessage: "OpenClaw 未输出可解析的登录事件",
			UpdatedAt:    time.Now(),
		})
		return AuthChallenge{}, errors.New("OpenClaw 未输出可解析的登录事件")
	}
	switch event.Type {
	case "qrcode":
		if event.QRCode == "" {
			a.recordProgress(input.AppID, AuthProgress{Status: AuthStatusFailed, ErrorMessage: "二维码事件缺少 URL", UpdatedAt: time.Now()})
			return AuthChallenge{}, errors.New("二维码事件缺少 URL")
		}
		a.recordProgress(input.AppID, AuthProgress{Status: AuthStatusPending, UpdatedAt: time.Now()})
		go a.consumeStream(input.AppID, stream)
		return AuthChallenge{
			Type:      "qrcode",
			ExpiresAt: event.ExpiresAt,
			QRCode:    event.QRCode,
			Hints:     event.Metadata,
		}, nil
	case "expired":
		a.recordProgress(input.AppID, AuthProgress{Status: AuthStatusExpired, UpdatedAt: time.Now()})
		return AuthChallenge{}, errors.New("OpenClaw 在出二维码前已宣告过期")
	case "failed":
		a.recordProgress(input.AppID, AuthProgress{Status: AuthStatusFailed, ErrorMessage: event.Error, UpdatedAt: time.Now()})
		return AuthChallenge{}, fmt.Errorf("OpenClaw 登录失败: %s", event.Error)
	default:
		a.recordProgress(input.AppID, AuthProgress{Status: AuthStatusFailed, ErrorMessage: "首条事件不是二维码", UpdatedAt: time.Now()})
		return AuthChallenge{}, fmt.Errorf("首条事件不是二维码: %s", event.Type)
	}
}

// PollAuth 返回最新的登录进度。
func (a *WeChatAdapter) PollAuth(ctx context.Context, input AuthInput) (AuthProgress, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	progress, ok := a.progress[input.AppID]
	if !ok {
		return AuthProgress{Status: AuthStatusPending, UpdatedAt: time.Now()}, nil
	}
	return progress, nil
}

func (a *WeChatAdapter) consumeStream(appID string, stream <-chan string) {
	for line := range stream {
		event, err := openclaw.ParseChannelLoginEvent(line)
		if err != nil {
			// 协议外的行（plugin loading log / ASCII QR / 中文提示行）正常跳过，
			// 不视为登录失败。
			continue
		}
		switch event.Type {
		case "bound":
			a.recordProgress(appID, AuthProgress{
				Status:        AuthStatusBound,
				BoundIdentity: event.Bound,
				ChannelName:   event.Channel,
				UpdatedAt:     time.Now(),
				Metadata:      event.Metadata,
			})
			return
		case "expired":
			a.recordProgress(appID, AuthProgress{Status: AuthStatusExpired, UpdatedAt: time.Now()})
			return
		case "failed":
			a.recordProgress(appID, AuthProgress{Status: AuthStatusFailed, ErrorMessage: event.Error, UpdatedAt: time.Now()})
			return
		default:
			a.recordProgress(appID, AuthProgress{Status: AuthStatusPending, UpdatedAt: time.Now(), Metadata: event.Metadata})
		}
	}
}

func (a *WeChatAdapter) recordProgress(appID string, progress AuthProgress) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.progress[appID] = progress
}

// readFirstAuthEvent 持续读 stream 直到第一个能 parse 出的、对 BeginAuth 决策性事件
// （qrcode / expired / failed）。pending 与 ErrUnparsableOutput 行被跳过。
// stream 关闭仍未拿到决策性事件时返回 (_, false)。
func readFirstAuthEvent(stream <-chan string) (openclaw.ChannelLoginEvent, bool) {
	for line := range stream {
		event, err := openclaw.ParseChannelLoginEvent(line)
		if err != nil {
			continue
		}
		switch event.Type {
		case "qrcode", "expired", "failed":
			return event, true
		default:
			// pending 等中间状态先吞掉，等下一个决策性事件。
			continue
		}
	}
	return openclaw.ChannelLoginEvent{}, false
}
