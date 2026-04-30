package openclaw

import (
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// ChannelLoginEvent 是从 OpenClaw runtime 解析出的登录事件统一表示。
type ChannelLoginEvent struct {
	Type      string            `json:"type"`
	QRCode    string            `json:"qrcode,omitempty"`
	Code      string            `json:"code,omitempty"`
	Bound     string            `json:"bound_identity,omitempty"`
	Channel   string            `json:"channel_name,omitempty"`
	Error     string            `json:"error,omitempty"`
	ExpiresAt time.Time         `json:"expires_at,omitempty"`
	Metadata  map[string]string `json:"metadata,omitempty"`
}

// ParseChannelLoginEvent 解析 OpenClaw runtime 输出的 JSON 行。
//
// OpenClaw 默认 stdout 是非结构化中文/英文混合输出。
// 为了保持解析稳定，本项目要求 OpenClaw 在 wechat 登录流程上额外打印形如
//
//	{"event":"qrcode","qrcode":"...","expires_at":"..."}
//
// 的 JSON 行。如果 stdout 不符合该 wrapper 协议，
// 解析会直接返回 ErrUnparsableOutput，上层 adapter 据此把状态置为 failed。
var (
	ErrUnparsableOutput = errors.New("OpenClaw 输出未遵循登录事件协议")
	ErrEventExpired     = errors.New("OpenClaw 登录事件已过期")
)

// ParseChannelLoginEvent 解析一行 stdout 文本。
func ParseChannelLoginEvent(line string) (ChannelLoginEvent, error) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || !strings.HasPrefix(trimmed, "{") {
		return ChannelLoginEvent{}, ErrUnparsableOutput
	}
	var raw struct {
		Event     string            `json:"event"`
		QRCode    string            `json:"qrcode"`
		Code      string            `json:"code"`
		Bound     string            `json:"bound_identity"`
		Channel   string            `json:"channel_name"`
		Error     string            `json:"error"`
		ExpiresAt string            `json:"expires_at"`
		Metadata  map[string]string `json:"metadata"`
	}
	if err := json.Unmarshal([]byte(trimmed), &raw); err != nil {
		return ChannelLoginEvent{}, ErrUnparsableOutput
	}
	if raw.Event == "" {
		return ChannelLoginEvent{}, ErrUnparsableOutput
	}
	event := ChannelLoginEvent{
		Type:     raw.Event,
		QRCode:   raw.QRCode,
		Code:     raw.Code,
		Bound:    raw.Bound,
		Channel:  raw.Channel,
		Error:    raw.Error,
		Metadata: raw.Metadata,
	}
	if raw.ExpiresAt != "" {
		if expires, err := time.Parse(time.RFC3339, raw.ExpiresAt); err == nil {
			event.ExpiresAt = expires
		}
	}
	if !event.ExpiresAt.IsZero() && time.Now().After(event.ExpiresAt) {
		return event, ErrEventExpired
	}
	return event, nil
}
