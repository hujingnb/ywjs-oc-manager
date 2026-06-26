package channel

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"oc-manager/internal/domain"
	"oc-manager/internal/integrations/ocops"
)

// FeishuRegisterRunner 抽象通过 oc-ops 触发飞书扫码注册 SSE 的能力。
// 实际实现（Task 12 的 OcOpsFeishuRunner）包裹 ocops.Client.FeishuRegister；
// 本任务仅依赖该接口，测试用 fake 注入，避免耦合真实 SSE。
type FeishuRegisterRunner interface {
	StreamFeishuRegister(ctx context.Context, input AuthInput, domain string) (<-chan ocops.FeishuRegisterEvent, error)
}

// FeishuCredentials 是扫码 / 手填取得的飞书凭证（仅在 manager 内部流转，secret 明文）。
// 该结构绝不进入 AuthProgress：secret 经 TakeCredentials 私有交接给 worker，
// 不经 PollAuth 透传前端，防止凭证泄露。
type FeishuCredentials struct {
	AppID     string
	AppSecret string
	Domain    string
	BotName   string
	BotOpenID string
}

// feishuState 是单个 app 的飞书绑定内部状态（含敏感凭证，不对外序列化）。
type feishuState struct {
	status  AuthStatus         // 对外可见的统一状态
	qrURL   string             // 扫码二维码 URL（仅内部留存）
	errMsg  string             // 失败原因，透传到 AuthProgress.ErrorMessage
	creds   *FeishuCredentials // 已取得但尚未交接给 worker 的凭证；TakeCredentials 取后置 nil
	updated time.Time          // 最近一次状态更新时间
}

// FeishuAdapter 实现 ChannelAdapter：扫码模式经 oc-ops SSE 取凭证；
// 凭证经 TakeCredentials 私有交接给 worker，secret 不进 PollAuth。
// 这与微信「引擎自落盘、adapter 只判 bound」的模式不同：飞书凭证需 manager 中转。
type FeishuAdapter struct {
	runner FeishuRegisterRunner
	prober FeishuProber
	mu     sync.Mutex
	states map[string]*feishuState
}

// NewFeishuAdapter 创建飞书 adapter。
func NewFeishuAdapter(runner FeishuRegisterRunner) *FeishuAdapter {
	return &FeishuAdapter{runner: runner, states: map[string]*feishuState{}}
}

// Type 返回 feishu。
func (a *FeishuAdapter) Type() string { return domain.ChannelTypeFeishu }

// BeginAuth 启动扫码注册：读到二维码即返回 challenge，后台消费 credentials/failed。
// input.ChannelName 复用为 domain（feishu|lark）传递；为空默认 feishu。
func (a *FeishuAdapter) BeginAuth(ctx context.Context, input AuthInput) (AuthChallenge, error) {
	if a.runner == nil {
		return AuthChallenge{}, errors.New("feishu adapter 未配置 FeishuRegisterRunner")
	}
	feishuDomain := input.ChannelName
	if feishuDomain == "" {
		feishuDomain = "feishu"
	}
	events, err := a.runner.StreamFeishuRegister(ctx, input, feishuDomain)
	if err != nil {
		return AuthChallenge{}, fmt.Errorf("启动飞书扫码注册失败: %w", err)
	}
	// 读到 qrcode 立即返回 challenge，并把剩余事件交后台 goroutine 消费；
	// 注意：循环只在拿到 qrcode/failed 时退出，确保不会把 credentials 事件提前读掉。
	for ev := range events {
		switch ev.Event {
		case "qrcode":
			if ev.URL == "" {
				a.set(input.AppID, feishuState{status: AuthStatusFailed, errMsg: "二维码事件缺少 URL", updated: time.Now()})
				return AuthChallenge{}, errors.New("二维码事件缺少 URL")
			}
			a.set(input.AppID, feishuState{status: AuthStatusPending, qrURL: ev.URL, updated: time.Now()})
			go a.consume(input.AppID, events)
			return AuthChallenge{Type: "qrcode", QRCode: ev.URL, ExpiresAt: time.Now().Add(10 * time.Minute)}, nil
		case "failed":
			a.set(input.AppID, feishuState{status: AuthStatusFailed, errMsg: ev.Reason, updated: time.Now()})
			return AuthChallenge{}, fmt.Errorf("飞书扫码注册失败: %s", ev.Reason)
		}
	}
	a.set(input.AppID, feishuState{status: AuthStatusFailed, errMsg: "未收到二维码事件", updated: time.Now()})
	return AuthChallenge{}, errors.New("飞书扫码注册未输出二维码")
}

// consume 后台消费剩余事件，落地 credentials/failed。
func (a *FeishuAdapter) consume(appID string, events <-chan ocops.FeishuRegisterEvent) {
	for ev := range events {
		switch ev.Event {
		case "credentials":
			a.set(appID, feishuState{
				status: AuthStatusPending, // 凭证已取，但连接未确认，仍 pending
				creds: &FeishuCredentials{
					AppID: ev.AppID, AppSecret: ev.AppSecret, Domain: ev.Domain,
					BotName: ev.BotName, BotOpenID: ev.BotOpenID,
				},
				updated: time.Now(),
			})
			return
		case "failed":
			a.set(appID, feishuState{status: AuthStatusFailed, errMsg: ev.Reason, updated: time.Now()})
			return
		}
	}
}

// PollAuth 返回不含 secret 的进度视图（供 HTTP 与 worker 通用读取状态）。
func (a *FeishuAdapter) PollAuth(_ context.Context, input AuthInput) (AuthProgress, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	st, ok := a.states[input.AppID]
	if !ok {
		return AuthProgress{Status: AuthStatusPending, UpdatedAt: time.Now()}, nil
	}
	return AuthProgress{Status: st.status, ErrorMessage: st.errMsg, UpdatedAt: st.updated}, nil
}

// TakeCredentials 取出并清空某 app 的飞书凭证（worker 专用；secret 经此交接，不走 PollAuth）。
func (a *FeishuAdapter) TakeCredentials(appID string) (FeishuCredentials, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	st, ok := a.states[appID]
	if !ok || st.creds == nil {
		return FeishuCredentials{}, false
	}
	c := *st.creds
	st.creds = nil // 取后清空，避免重复注入
	return c, true
}

// SetCredentials 直接写入凭证（手填模式无 SSE，由 service/worker 注入；Task 12 用）。
func (a *FeishuAdapter) SetCredentials(appID string, c FeishuCredentials) {
	a.set(appID, feishuState{status: AuthStatusPending, creds: &c, updated: time.Now()})
}

// FeishuProber 抽象手填模式经 oc-ops 即时校验飞书凭证的能力。
type FeishuProber interface {
	ProbeFeishu(ctx context.Context, input AuthInput, appID, appSecret, domain string) (ok bool, botName, botOpenID string, err error)
}

// SetProber 注入手填校验器。
func (a *FeishuAdapter) SetProber(p FeishuProber) { a.prober = p }

// BeginManual 手填模式：可选 probe 校验，通过则置凭证（带回 bot_name/open_id）。
func (a *FeishuAdapter) BeginManual(ctx context.Context, input AuthInput, creds FeishuCredentials) (AuthChallenge, error) {
	if a.prober != nil {
		ok, botName, botOpenID, err := a.prober.ProbeFeishu(ctx, input, creds.AppID, creds.AppSecret, creds.Domain)
		if err != nil {
			return AuthChallenge{}, fmt.Errorf("飞书凭证校验失败: %w", err)
		}
		if !ok {
			a.set(input.AppID, feishuState{status: AuthStatusFailed, errMsg: "飞书凭证无效", updated: time.Now()})
			return AuthChallenge{}, errors.New("飞书凭证无效")
		}
		creds.BotName, creds.BotOpenID = botName, botOpenID
	}
	a.SetCredentials(input.AppID, creds)
	return AuthChallenge{Type: "feishu_manual"}, nil
}

// set 以写锁覆盖某 app 的内部状态。
func (a *FeishuAdapter) set(appID string, st feishuState) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.states[appID] = &st
}
