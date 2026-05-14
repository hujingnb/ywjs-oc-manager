// Package channel 维护 manager 与不同消息渠道（微信、企微等）之间的协议适配。
//
// 当前版本仅实现微信渠道；其他渠道按相同 ChannelAdapter 接口逐步加入。
// service 层始终通过 Registry 拿到具体 adapter，不直接耦合任何特定渠道。
package channel

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// ErrAdapterNotFound 表示 Registry 中未注册指定渠道类型的 adapter。
var ErrAdapterNotFound = errors.New("未注册的渠道适配器")

// ErrChallengeExpired 由 adapter 在二维码 / 验证码已过期时返回。
var ErrChallengeExpired = errors.New("渠道登录挑战已过期")

// AuthChallenge 是登录开始后下发给前端展示的挑战信息。
// Type 决定前端如何渲染：qrcode 表示展示二维码，code 表示展示数字验证码。
type AuthChallenge struct {
	Type      string            `json:"type"`
	ExpiresAt time.Time         `json:"expires_at"`
	QRCode    string            `json:"qrcode,omitempty"`
	Code      string            `json:"code,omitempty"`
	Hints     map[string]string `json:"hints,omitempty"`
}

// AuthStatus 是 adapter 对外暴露的统一状态。
type AuthStatus string

const (
	// AuthStatusPending 表示挑战仍有效但用户尚未确认。
	AuthStatusPending AuthStatus = "pending"
	// AuthStatusBound 表示登录成功，channel binding 可以进入 bound 状态。
	AuthStatusBound AuthStatus = "bound"
	// AuthStatusFailed 表示登录失败但仍允许重试。
	AuthStatusFailed AuthStatus = "failed"
	// AuthStatusExpired 表示挑战过期，需要重新发起 BeginAuth。
	AuthStatusExpired AuthStatus = "expired"
)

// AuthProgress 是查询登录进度时返回的视图。
type AuthProgress struct {
	Status        AuthStatus        `json:"status"`
	BoundIdentity string            `json:"bound_identity,omitempty"`
	ChannelName   string            `json:"channel_name,omitempty"`
	ErrorMessage  string            `json:"error_message,omitempty"`
	UpdatedAt     time.Time         `json:"updated_at"`
	Metadata      map[string]string `json:"metadata,omitempty"`
}

// AuthInput 在 BeginAuth 时携带用户上下文，供 adapter 调用 runtime 容器。
type AuthInput struct {
	NodeID      string
	ContainerID string
	AppID       string
	OwnerUserID string
	ChannelName string
}

// ChannelAdapter 是渠道协议的统一接口。
// 为了保持简单，所有方法只暴露挑战与查询；解绑/重置等操作走 service 层直接写库。
type ChannelAdapter interface {
	Type() string
	BeginAuth(ctx context.Context, input AuthInput) (AuthChallenge, error)
	PollAuth(ctx context.Context, input AuthInput) (AuthProgress, error)
}

// Registry 维护 channel_type → adapter 的映射，service 层在路由阶段查表。
type Registry struct {
	adapters map[string]ChannelAdapter
}

// NewRegistry 创建空 registry。
func NewRegistry() *Registry { return &Registry{adapters: map[string]ChannelAdapter{}} }

// Register 添加一个 adapter；重复注册返回错误，避免静默覆盖。
func (r *Registry) Register(adapter ChannelAdapter) error {
	if adapter == nil {
		return fmt.Errorf("adapter 不能为 nil")
	}
	if _, ok := r.adapters[adapter.Type()]; ok {
		return fmt.Errorf("渠道 %q 已注册", adapter.Type())
	}
	r.adapters[adapter.Type()] = adapter
	return nil
}

// MustRegister 在重复注册时直接 panic，仅供启动期初始化。
func (r *Registry) MustRegister(adapter ChannelAdapter) {
	if err := r.Register(adapter); err != nil {
		panic(err)
	}
}

// Lookup 按类型取出 adapter，未注册返回 ErrAdapterNotFound。
func (r *Registry) Lookup(channelType string) (ChannelAdapter, error) {
	adapter, ok := r.adapters[channelType]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrAdapterNotFound, channelType)
	}
	return adapter, nil
}

// Types 列出当前注册的所有渠道类型，主要供测试和管理 API 使用。
func (r *Registry) Types() []string {
	out := make([]string, 0, len(r.adapters))
	for t := range r.adapters {
		out = append(out, t)
	}
	return out
}
