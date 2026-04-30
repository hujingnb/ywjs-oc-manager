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
	first, ok := readFirstEvent(stream)
	if !ok {
		return AuthChallenge{}, errors.New("OpenClaw 未输出可解析的登录事件")
	}
	event, err := openclaw.ParseChannelLoginEvent(first)
	if err != nil {
		a.recordProgress(input.AppID, AuthProgress{
			Status:       AuthStatusFailed,
			ErrorMessage: err.Error(),
			UpdatedAt:    time.Now(),
		})
		return AuthChallenge{}, err
	}
	if event.Type != "qrcode" || event.QRCode == "" {
		a.recordProgress(input.AppID, AuthProgress{Status: AuthStatusFailed, ErrorMessage: "首条事件不是二维码", UpdatedAt: time.Now()})
		return AuthChallenge{}, fmt.Errorf("首条事件不是二维码: %s", event.Type)
	}
	a.recordProgress(input.AppID, AuthProgress{Status: AuthStatusPending, UpdatedAt: time.Now()})
	go a.consumeStream(input.AppID, stream)
	return AuthChallenge{
		Type:      "qrcode",
		ExpiresAt: event.ExpiresAt,
		QRCode:    event.QRCode,
		Hints:     event.Metadata,
	}, nil
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
			a.recordProgress(appID, AuthProgress{Status: AuthStatusFailed, ErrorMessage: err.Error(), UpdatedAt: time.Now()})
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

func readFirstEvent(stream <-chan string) (string, bool) {
	for line := range stream {
		if line == "" {
			continue
		}
		return line, true
	}
	return "", false
}
