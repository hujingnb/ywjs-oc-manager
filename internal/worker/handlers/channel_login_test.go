package handlers

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/integrations/channel"
	"oc-manager/internal/integrations/ocops"
	"oc-manager/internal/store/sqlc"
)

// fakeEndpointResolver 实现 ChannelEndpointResolver，返回预设 Endpoint 或 error。
type fakeEndpointResolver struct {
	ep  ocops.Endpoint
	err error
}

func (r fakeEndpointResolver) ResolveEndpoint(_ context.Context, _ string) (ocops.Endpoint, error) {
	return r.ep, r.err
}

const (
	testChannelWorkerAppID   = "00000000-0000-0000-0000-00000000c101"
	testChannelWorkerOrgID   = "00000000-0000-0000-0000-00000000c102"
	testChannelWorkerOwnerID = "00000000-0000-0000-0000-00000000c103"
)

// TestChannelStartLoginHandlerWritesChallenge 验证渠道启动登录处理器写入Challenge的成功路径场景。
func TestChannelStartLoginHandlerWritesChallenge(t *testing.T) {
	store := newChannelWorkerStore(t)
	registry := channel.NewRegistry()
	registry.MustRegister(&workerFakeChannelAdapter{
		challenge: channel.AuthChallenge{
			Type:      "qrcode",
			QRCode:    "data:image/png;base64,qr",
			ExpiresAt: time.Date(2026, 5, 3, 12, 0, 0, 0, time.UTC),
			Hints:     map[string]string{"raw_qr": "https://liteapp.weixin.qq.com/q/test"},
		},
	})
	// resolver 传 nil：成功路径不依赖 oc-ops 坐标解析，Endpoint 留零值即可。
	handler := NewChannelStartLoginHandler(store, registry, nil)

	err := handler.Handle(context.Background(), sqlc.Job{
		Type:        domain.JobTypeChannelStartLogin,
		PayloadJson: []byte(`{"app_id":"` + testChannelWorkerAppID + `","channel_type":"wechat"}`),
	})
	require.NoError(t, err)
	require.Equal(t, domain.ChannelStatusPendingAuth, store.binding.Status)
	metadata := string(store.binding.MetadataJson)
	require.Contains(t, metadata, "data:image/png;base64,qr")
	require.Contains(t, metadata, "raw_qr")
	if len(store.jobs) != 1 || store.jobs[0].Type != domain.JobTypeChannelCheckBinding {
		t.Fatalf("应入队 channel_check_binding，jobs=%+v", store.jobs)
	}
}

// TestChannelStartLoginHandlerInjectsEndpoint 验证 BeginAuth 前 handler 经 resolver
// 解析 oc-ops 坐标并注入 AuthInput.Endpoint，确保微信扫码登录 SSE 路由到正确实例。
func TestChannelStartLoginHandlerInjectsEndpoint(t *testing.T) {
	store := newChannelWorkerStore(t)
	registry := channel.NewRegistry()
	adapter := &workerFakeChannelAdapter{
		challenge: channel.AuthChallenge{Type: "qrcode", QRCode: "data:image/png;base64,qr"},
	}
	registry.MustRegister(adapter)
	// fakeEndpointResolver 返回固定坐标，断言其原样透传到 adapter。
	wantEp := ocops.Endpoint{BaseURL: "http://app-x.ocops:8080", Token: "tok-x"}
	handler := NewChannelStartLoginHandler(store, registry, fakeEndpointResolver{ep: wantEp})

	err := handler.Handle(context.Background(), sqlc.Job{
		Type:        domain.JobTypeChannelStartLogin,
		PayloadJson: []byte(`{"app_id":"` + testChannelWorkerAppID + `","channel_type":"wechat"}`),
	})
	require.NoError(t, err)
	require.Equal(t, wantEp, adapter.gotBeginInput.Endpoint)
}

// TestChannelStartLoginHandlerEndpointResolveFailsSoft 验证 resolver 解析失败时
// handler 不阻断登录：Endpoint 留零值继续走 BeginAuth（由下游在不可达时报错）。
func TestChannelStartLoginHandlerEndpointResolveFailsSoft(t *testing.T) {
	store := newChannelWorkerStore(t)
	registry := channel.NewRegistry()
	adapter := &workerFakeChannelAdapter{
		challenge: channel.AuthChallenge{Type: "qrcode", QRCode: "data:image/png;base64,qr"},
	}
	registry.MustRegister(adapter)
	// resolver 返回 error：handler 应吞掉错误、Endpoint 留零值，登录流程照常推进。
	handler := NewChannelStartLoginHandler(store, registry, fakeEndpointResolver{err: errors.New("resolve boom")})

	err := handler.Handle(context.Background(), sqlc.Job{
		Type:        domain.JobTypeChannelStartLogin,
		PayloadJson: []byte(`{"app_id":"` + testChannelWorkerAppID + `","channel_type":"wechat"}`),
	})
	require.NoError(t, err)
	require.Equal(t, ocops.Endpoint{}, adapter.gotBeginInput.Endpoint)
	require.Equal(t, domain.ChannelStatusPendingAuth, store.binding.Status)
}

// TestChannelCheckBindingHandlerMarksBoundAndRunsApp 验证渠道Check绑定处理器Marks已绑定并Runs应用的预期行为场景。
func TestChannelCheckBindingHandlerMarksBoundAndRunsApp(t *testing.T) {
	store := newChannelWorkerStore(t)
	store.app.Status = domain.AppStatusBindingWaiting
	registry := channel.NewRegistry()
	registry.MustRegister(&workerFakeChannelAdapter{
		progress: channel.AuthProgress{
			Status:        channel.AuthStatusBound,
			BoundIdentity: "wxid_from_stdout",
			ChannelName:   "测试微信",
			UpdatedAt:     time.Now(),
		},
	})
	handler := NewChannelCheckBindingHandler(store, registry, nil)
	// 注入 restarter,断言 bound 后会触发 hermes 容器重启,
	// 让 hermes 重新读 platforms 配置加载新绑定的微信账号。
	restarter := &workerFakeRestarter{}
	handler.SetRestarter(restarter)

	err := handler.Handle(context.Background(), sqlc.Job{
		Type:        domain.JobTypeChannelCheckBinding,
		PayloadJson: []byte(`{"app_id":"` + testChannelWorkerAppID + `","channel_type":"wechat"}`),
	})
	require.NoError(t, err)
	require.Equal(t, domain.ChannelStatusBound, store.binding.Status)
	require.Equal(t, "wxid_from_stdout", store.binding.BoundIdentity.String)
	if !store.appStatusSet || store.app.Status != domain.AppStatusRunning {
		t.Fatalf("app 未推进到 running: set=%v status=%q", store.appStatusSet, store.app.Status)
	}
	// k8s 时代：bound 后触发 rollout restart，按 appID 重建 pod 重载 hermes platform 配置。
	require.Equal(t, 1, restarter.calls, "bound 后应触发 RestartApp")
	require.Equal(t, testChannelWorkerAppID, restarter.lastAppID)
	require.Len(t, store.auditLogs, 1)
	require.Equal(t, "app", store.auditLogs[0].TargetType)
	require.Equal(t, testChannelWorkerAppID, store.auditLogs[0].TargetID)
	require.Equal(t, "channel_bound", store.auditLogs[0].Action)
	require.Equal(t, "succeeded", store.auditLogs[0].Result)
	// 详情字段应同时拼出渠道和绑定身份，便于审计列表识别。
	require.True(t, store.auditLogs[0].DetailMessage.Valid)
	require.Equal(t, "渠道 微信，身份 wxid_from_stdout", store.auditLogs[0].DetailMessage.String)
}

// TestChannelStartLoginHandlerRecordsFailedAudit 验证渠道启动登录失败时写入应用审计的错误记录场景。
func TestChannelStartLoginHandlerRecordsFailedAudit(t *testing.T) {
	store := newChannelWorkerStore(t)
	registry := channel.NewRegistry()
	registry.MustRegister(&workerFakeChannelAdapter{beginErr: errors.New("weixin qrcode failed")})
	// resolver 传 nil：失败路径同样不依赖 oc-ops 坐标解析。
	handler := NewChannelStartLoginHandler(store, registry, nil)

	err := handler.Handle(context.Background(), sqlc.Job{
		Type:        domain.JobTypeChannelStartLogin,
		PayloadJson: []byte(`{"app_id":"` + testChannelWorkerAppID + `","channel_type":"wechat"}`),
	})
	require.Error(t, err)
	require.Equal(t, domain.ChannelStatusFailed, store.binding.Status)
	require.Len(t, store.auditLogs, 1)
	require.Equal(t, "app", store.auditLogs[0].TargetType)
	require.Equal(t, testChannelWorkerAppID, store.auditLogs[0].TargetID)
	require.Equal(t, "channel_auth_start", store.auditLogs[0].Action)
	require.Equal(t, "failed", store.auditLogs[0].Result)
	require.Contains(t, store.auditLogs[0].ErrorMessage.String, "weixin qrcode failed")
	// channel_auth_start 的失败详情也应包含渠道名，让审计列表区分是哪条渠道。
	require.True(t, store.auditLogs[0].DetailMessage.Valid)
	require.Equal(t, "渠道 微信", store.auditLogs[0].DetailMessage.String)
}

// TestChannelCheckBindingHandlerRecordsFailedAudit 验证渠道轮询确认绑定失败时写入应用审计的错误记录场景。
func TestChannelCheckBindingHandlerRecordsFailedAudit(t *testing.T) {
	store := newChannelWorkerStore(t)
	registry := channel.NewRegistry()
	registry.MustRegister(&workerFakeChannelAdapter{
		progress: channel.AuthProgress{
			Status:       channel.AuthStatusFailed,
			ErrorMessage: "user rejected login",
			UpdatedAt:    time.Now(),
		},
	})
	handler := NewChannelCheckBindingHandler(store, registry, nil)

	err := handler.Handle(context.Background(), sqlc.Job{
		Type:        domain.JobTypeChannelCheckBinding,
		PayloadJson: []byte(`{"app_id":"` + testChannelWorkerAppID + `","channel_type":"wechat"}`),
	})
	require.NoError(t, err)
	require.Equal(t, domain.ChannelStatusFailed, store.binding.Status)
	require.Len(t, store.auditLogs, 1)
	require.Equal(t, "channel_bound", store.auditLogs[0].Action)
	require.Equal(t, "failed", store.auditLogs[0].Result)
	require.Contains(t, store.auditLogs[0].ErrorMessage.String, "user rejected login")
	// 场景：失败路径没有 identity，详情仅包含渠道名。
	require.True(t, store.auditLogs[0].DetailMessage.Valid)
	require.Equal(t, "渠道 微信", store.auditLogs[0].DetailMessage.String)
}

// TestChannelCheckBindingHandlerUsesResolverIdentity 验证渠道Check绑定处理器使用解析器身份的预期行为场景。
func TestChannelCheckBindingHandlerUsesResolverIdentity(t *testing.T) {
	store := newChannelWorkerStore(t)
	registry := channel.NewRegistry()
	registry.MustRegister(&workerFakeChannelAdapter{
		progress: channel.AuthProgress{Status: channel.AuthStatusBound, UpdatedAt: time.Now()},
	})
	resolver := &workerFakeBindingResolver{identity: "user-from-plugin-state"}
	handler := NewChannelCheckBindingHandler(store, registry, resolver)

	err := handler.Handle(context.Background(), sqlc.Job{
		Type:        domain.JobTypeChannelCheckBinding,
		PayloadJson: []byte(`{"app_id":"` + testChannelWorkerAppID + `","channel_type":"wechat"}`),
	})
	require.NoError(t, err)
	require.Equal(t, "user-from-plugin-state", store.binding.BoundIdentity.String)
	require.Equal(t, 1, resolver.calls)
}

// TestChannelCheckBindingHandlerRequeuesPending 验证渠道Check绑定处理器Requeues等待中的预期行为场景。
func TestChannelCheckBindingHandlerRequeuesPending(t *testing.T) {
	store := newChannelWorkerStore(t)
	registry := channel.NewRegistry()
	registry.MustRegister(&workerFakeChannelAdapter{
		progress: channel.AuthProgress{Status: channel.AuthStatusPending, UpdatedAt: time.Now()},
	})
	handler := NewChannelCheckBindingHandler(store, registry, nil)

	err := handler.Handle(context.Background(), sqlc.Job{
		Type:        domain.JobTypeChannelCheckBinding,
		PayloadJson: []byte(`{"app_id":"` + testChannelWorkerAppID + `","channel_type":"wechat"}`),
	})
	require.NoError(t, err)
	require.Equal(t, domain.ChannelStatusPendingAuth, store.binding.Status)
	if len(store.jobs) != 1 || store.jobs[0].Type != domain.JobTypeChannelCheckBinding {
		t.Fatalf("pending 状态应延迟重查，jobs=%+v", store.jobs)
	}
}

// TestChannelCheckBindingHandlerFallsBackToResolverWhenAdapterPending 校验：
// 当 PollAuth 返回 pending（plugin stdout 没输出 "bound"），但 plugin state 文件里
// 已经有真实账号 session 时（resolver 返回非空 identity），应当推到 bound 而不是
// 等到 expired。
//
// 这个 fallback 修复的是 Hermes weixin plugin 的真实行为（legacy OpenClaw 时代同样存在）：
// 第二次扫码（同一微信账号已授权过）plugin 静默成功不再 emit bound 事件，但 accounts.json
// 仍真实可用。
func TestChannelCheckBindingHandlerFallsBackToResolverWhenAdapterPending(t *testing.T) {
	store := newChannelWorkerStore(t)
	registry := channel.NewRegistry()
	registry.MustRegister(&workerFakeChannelAdapter{
		progress: channel.AuthProgress{Status: channel.AuthStatusPending, UpdatedAt: time.Now()},
	})
	resolver := &workerFakeBindingResolver{identity: "o9cq800xszCM8jyoS9YpRKpvAN9c@im.wechat"}
	handler := NewChannelCheckBindingHandler(store, registry, resolver)

	err := handler.Handle(context.Background(), sqlc.Job{
		Type:        domain.JobTypeChannelCheckBinding,
		PayloadJson: []byte(`{"app_id":"` + testChannelWorkerAppID + `","channel_type":"wechat"}`),
	})
	require.NoError(t, err)
	require.Equal(t, domain.ChannelStatusBound, store.binding.Status)
	require.Equal(t, "o9cq800xszCM8jyoS9YpRKpvAN9c@im.wechat", store.binding.BoundIdentity.String)
	require.True(t, store.appStatusSet)
	require.Equal(t, 1, resolver.calls)
	require.Len(t, store.auditLogs, 1)
	require.Equal(t, "channel_bound", store.auditLogs[0].Action)
}

// TestChannelCheckBindingHandlerSkipsResolverFallbackWithoutResolver 校验：
// 没装 resolver（如非 wechat 渠道）时 pending 仍走原始重新入队路径，不报错。
func TestChannelCheckBindingHandlerSkipsResolverFallbackWithoutResolver(t *testing.T) {
	store := newChannelWorkerStore(t)
	registry := channel.NewRegistry()
	registry.MustRegister(&workerFakeChannelAdapter{
		progress: channel.AuthProgress{Status: channel.AuthStatusPending, UpdatedAt: time.Now()},
	})
	handler := NewChannelCheckBindingHandler(store, registry, nil)

	err := handler.Handle(context.Background(), sqlc.Job{
		Type:        domain.JobTypeChannelCheckBinding,
		PayloadJson: []byte(`{"app_id":"` + testChannelWorkerAppID + `","channel_type":"wechat"}`),
	})
	require.NoError(t, err)
	require.Equal(t, domain.ChannelStatusPendingAuth, store.binding.Status)
}

// workerFakeFeishuRunner 是 channel.FeishuRegisterRunner 的 worker 包内测试实现：
// 一次性把预置事件塞入 channel 后立即 close，模拟 oc-ops 飞书扫码注册 SSE 事件流。
// 之所以在 worker 测试里单独定义（而非复用 channel 包测试里的同名 fake），是因为
// 那个定义在 channel 包的 _test.go 内，跨包不可见；这里仅需 qrcode 事件即可驱动
// FeishuAdapter.BeginAuth 拿到二维码返回。
type workerFakeFeishuRunner struct{ events []ocops.FeishuRegisterEvent }

// StreamFeishuRegister 返回携带预置事件的只读 channel；缓冲容量等于事件数，
// 确保塞入不阻塞，close 后 adapter 既能读到 qrcode 也能让后台 goroutine 读完剩余事件。
func (r *workerFakeFeishuRunner) StreamFeishuRegister(_ context.Context, _ channel.AuthInput, _ string) (<-chan ocops.FeishuRegisterEvent, error) {
	ch := make(chan ocops.FeishuRegisterEvent, len(r.events))
	for _, e := range r.events {
		ch <- e
	}
	close(ch)
	return ch, nil
}

// feishuStartJob 构造一条飞书 channel_start_login job，payload 含 app_id /
// channel_type=feishu / mode（scan|manual）/ domain（feishu|lark）。
func feishuStartJob(t *testing.T, mode, feishuDomain string) sqlc.Job {
	t.Helper()
	payload := `{"app_id":"` + testChannelWorkerAppID + `","channel_type":"feishu","mode":"` + mode + `","domain":"` + feishuDomain + `"}`
	return sqlc.Job{Type: domain.JobTypeChannelStartLogin, PayloadJson: []byte(payload)}
}

// newFeishuWorkerStore 复用 channelWorkerStore，但把绑定渠道改为 feishu，
// 否则 GetChannelBindingByAppAndType 因 ChannelType 不匹配返回 ErrNoRows。
func newFeishuWorkerStore(t *testing.T) *channelWorkerStore {
	store := newChannelWorkerStore(t)
	store.binding.ChannelType = domain.ChannelTypeFeishu
	return store
}

// TestChannelStartLoginFeishuScanSavesQR 验证飞书扫码：BeginAuth 拿到二维码后
// 把二维码 metadata 写入 channel_binding，并入队 channel_check_binding。
func TestChannelStartLoginFeishuScanSavesQR(t *testing.T) {
	store := newFeishuWorkerStore(t)
	registry := channel.NewRegistry()
	registry.MustRegister(channel.NewFeishuAdapter(&workerFakeFeishuRunner{events: []ocops.FeishuRegisterEvent{
		{Event: "qrcode", URL: "https://open.feishu.cn/qr/x"},
	}}))
	handler := NewChannelStartLoginHandler(store, registry, nil)

	err := handler.Handle(context.Background(), feishuStartJob(t, "scan", "feishu"))
	require.NoError(t, err)
	// 二维码 URL 应写入 challenge metadata，供前端 PollAuth 展示。
	require.Contains(t, string(store.binding.MetadataJson), "open.feishu.cn/qr/x")
	require.Equal(t, domain.ChannelStatusPendingAuth, store.binding.Status)
	// 扫码后应入队一条 check 任务，进入轮询绑定阶段。
	require.Len(t, store.jobs, 1)
	require.Equal(t, domain.JobTypeChannelCheckBinding, store.jobs[0].Type)
}

// TestChannelStartLoginFeishuManualEnqueuesCheck 验证手填：凭证已由 service 写入
// metadata，worker 不调 SSE、不出码，直接入队 check（注入阶段在 check handler 做）。
func TestChannelStartLoginFeishuManualEnqueuesCheck(t *testing.T) {
	store := newFeishuWorkerStore(t)
	registry := channel.NewRegistry()
	// manual 模式不消费 runner，传 nil 即可。
	registry.MustRegister(channel.NewFeishuAdapter(nil))
	handler := NewChannelStartLoginHandler(store, registry, nil)

	err := handler.Handle(context.Background(), feishuStartJob(t, "manual", "feishu"))
	require.NoError(t, err)
	// 手填路径不出码，仅入队一条 check 任务。
	require.Len(t, store.jobs, 1)
	require.Equal(t, domain.JobTypeChannelCheckBinding, store.jobs[0].Type)
}

// fakeFeishuPatcher 实现 FeishuSecretPatcher，记录被注入的 feishu-* key 与删除项，
// 供阶段1断言「凭证已 patch 到 app Secret」。
type fakeFeishuPatcher struct {
	patched bool
	set     map[string]string
	deleted []string
}

func (p *fakeFeishuPatcher) PatchSecretKeys(_ context.Context, _ string, set map[string]string, del []string) error {
	p.patched = true
	p.set = set
	p.deleted = del
	return nil
}

// fakeHealthClient 实现 FeishuHealthClient，返回预置的 platform_state / bot_open_id / errMsg，
// 模拟 oc-ops ChannelStatus(feishu) 的健康探测结果。
type fakeHealthClient struct {
	state     string
	botOpenID string
	errMsg    string
	err       error
}

func (c *fakeHealthClient) FeishuStatus(_ context.Context, _ string) (string, string, string, error) {
	return c.state, c.botOpenID, c.errMsg, c.err
}

// newTestCipher 用固定 32 字节 master_key 造 cipher，供飞书 secret 加解密断言。
func newTestCipher(t *testing.T) *auth.Cipher {
	t.Helper()
	c, err := auth.NewCipher([]byte("0123456789abcdef0123456789abcdef"))
	require.NoError(t, err)
	return c
}

// feishuCheckJob 构造一条飞书 channel_check_binding job（payload channel_type=feishu）。
func feishuCheckJob(t *testing.T) sqlc.Job {
	t.Helper()
	return sqlc.Job{
		Type:        domain.JobTypeChannelCheckBinding,
		PayloadJson: []byte(`{"app_id":"` + testChannelWorkerAppID + `","channel_type":"feishu"}`),
	}
}

// newFeishuCheckStore 构造飞书 check 用 store：渠道改 feishu、状态 pending_auth，
// metadata 由各测试自行覆盖（injected 标记决定走阶段1 还是阶段2）。
func newFeishuCheckStore(t *testing.T) *channelWorkerStore {
	store := newFeishuWorkerStore(t)
	store.binding.Status = domain.ChannelStatusPendingAuth
	return store
}

// TestFeishuCheckPhase1InjectsAndRestarts 验证扫码凭证就绪→加密落库+patch Secret+重启+标 injected。
func TestFeishuCheckPhase1InjectsAndRestarts(t *testing.T) {
	store := newFeishuCheckStore(t)
	// injected=false：进入阶段1，凭证从 adapter 取出后注入并重启。
	store.binding.MetadataJson = []byte(`{"acquired_by":"qr","domain":"feishu","injected":"false"}`)
	fa := channel.NewFeishuAdapter(nil)
	fa.SetCredentials(testChannelWorkerAppID, channel.FeishuCredentials{AppID: "cli_x", AppSecret: "sec", Domain: "feishu", BotName: "Bot", BotOpenID: "ou_1"})
	registry := channel.NewRegistry()
	registry.MustRegister(fa)
	patcher := &fakeFeishuPatcher{}
	restarter := &workerFakeRestarter{}
	h := NewChannelCheckBindingHandler(store, registry, nil)
	h.SetRestarter(restarter)
	h.SetFeishuDeps(patcher, newTestCipher(t), &fakeHealthClient{})

	require.NoError(t, h.Handle(context.Background(), feishuCheckJob(t)))
	require.True(t, patcher.patched, "应 patch feishu-* key")
	require.Equal(t, "cli_x", patcher.set["feishu-app-id"])
	require.Equal(t, "sec", patcher.set["feishu-app-secret"])
	require.Equal(t, 1, restarter.calls, "注入后应触发重启")
	require.Contains(t, string(store.feishuMeta), "\"injected\":\"true\"")
	require.NotContains(t, string(store.feishuMeta), "\"sec\"", "secret 落库必须密文")
	// 阶段1结束应入队一条 check（进入阶段2 health 探测）。
	require.Len(t, store.jobs, 1)
	require.Equal(t, domain.JobTypeChannelCheckBinding, store.jobs[0].Type)
}

// TestFeishuCheckPhase2HealthConnectedBinds 验证已注入→health connected→MarkBound（identity 取 metadata bot_open_id）。
func TestFeishuCheckPhase2HealthConnectedBinds(t *testing.T) {
	store := newFeishuCheckStore(t)
	store.app.Status = domain.AppStatusBindingWaiting
	// injected=true：进入阶段2，identity 从 metadata 的 bot_open_id 取（health 不回传）。
	store.binding.MetadataJson = []byte(`{"app_id":"cli_x","domain":"feishu","bot_name":"Bot","bot_open_id":"ou_1","injected":"true"}`)
	registry := channel.NewRegistry()
	registry.MustRegister(channel.NewFeishuAdapter(nil))
	h := NewChannelCheckBindingHandler(store, registry, nil)
	h.SetFeishuDeps(&fakeFeishuPatcher{}, newTestCipher(t), &fakeHealthClient{state: "connected"})

	require.NoError(t, h.Handle(context.Background(), feishuCheckJob(t)))
	require.Equal(t, domain.ChannelStatusBound, store.binding.Status)
	require.Equal(t, "ou_1", store.binding.BoundIdentity.String, "identity 来自 metadata，非 health")
	require.Equal(t, "Bot", store.binding.ChannelName.String)
	require.True(t, store.appStatusSet)
}

// TestFeishuCheckPhase2HealthFatalFails 验证 health fatal→failed 带原因。
func TestFeishuCheckPhase2HealthFatalFails(t *testing.T) {
	store := newFeishuCheckStore(t)
	store.binding.MetadataJson = []byte(`{"app_id":"cli_x","injected":"true"}`)
	registry := channel.NewRegistry()
	registry.MustRegister(channel.NewFeishuAdapter(nil))
	h := NewChannelCheckBindingHandler(store, registry, nil)
	h.SetFeishuDeps(&fakeFeishuPatcher{}, newTestCipher(t), &fakeHealthClient{state: "fatal", errMsg: "invalid app_secret"})

	require.NoError(t, h.Handle(context.Background(), feishuCheckJob(t)))
	require.Equal(t, domain.ChannelStatusFailed, store.binding.Status)
	require.Contains(t, store.binding.LastError.String, "invalid app_secret")
}

type workerFakeChannelAdapter struct {
	challenge channel.AuthChallenge
	progress  channel.AuthProgress
	beginErr  error
	// gotBeginInput 记录最近一次 BeginAuth 收到的入参，供断言 Endpoint 注入。
	gotBeginInput channel.AuthInput
}

func (a *workerFakeChannelAdapter) Type() string { return domain.ChannelTypeWeChat }
func (a *workerFakeChannelAdapter) BeginAuth(_ context.Context, input channel.AuthInput) (channel.AuthChallenge, error) {
	a.gotBeginInput = input
	if a.beginErr != nil {
		return channel.AuthChallenge{}, a.beginErr
	}
	return a.challenge, nil
}
func (a *workerFakeChannelAdapter) PollAuth(_ context.Context, _ channel.AuthInput) (channel.AuthProgress, error) {
	return a.progress, nil
}

type workerFakeBindingResolver struct {
	identity string
	calls    int
}

// ResolveWeChatBoundIdentity 实现 channel.BindingResolver，接收 appID（新签名，已去掉 nodeID/containerID）。
func (r *workerFakeBindingResolver) ResolveWeChatBoundIdentity(_ context.Context, _ string) (string, error) {
	r.calls++
	return r.identity, nil
}

// workerFakeRestarter 是 ChannelRestarter 的测试桩，记录调用次数与最后一次传入的 appID，
// 用于断言 bound 后是否正确触发 hermes 重启（k8s rollout restart 路径）。
type workerFakeRestarter struct {
	calls      int
	lastAppID  string
	err        error
}

func (r *workerFakeRestarter) RestartApp(_ context.Context, appID string) error {
	r.calls++
	r.lastAppID = appID
	return r.err
}

type channelWorkerStore struct {
	t            *testing.T
	app          sqlc.App
	binding      sqlc.ChannelBinding
	jobs         []sqlc.Job
	auditLogs    []sqlc.CreateAuditLogParams
	appStatusSet bool
	// feishuMeta 记录最近一次 SetFeishuCredentials 写入的 metadata，供飞书阶段1断言
	// 「injected=true 已落库 + secret 密文化」。
	feishuMeta []byte
}

// newChannelWorkerStore 构造 channelWorkerStore；ID 字段迁移为 string（MySQL uuid）。
// spec-A2b：runtime_node_id / container_id 已从 schema 删除，不再填充。
func newChannelWorkerStore(t *testing.T) *channelWorkerStore {
	app := sqlc.App{
		ID:          testChannelWorkerAppID,
		OrgID:       testChannelWorkerOrgID,
		OwnerUserID: testChannelWorkerOwnerID,
		Status:      domain.AppStatusBindingWaiting,
	}
	return &channelWorkerStore{
		t:   t,
		app: app,
		binding: sqlc.ChannelBinding{
			ID:          "00000000-0000-0000-0000-00000000c105",
			AppID:       testChannelWorkerAppID,
			ChannelType: domain.ChannelTypeWeChat,
			Status:      domain.ChannelStatusUnbound,
		},
	}
}

// GetApp 按字符串 UUID 查 app；id 迁移为 string。
func (s *channelWorkerStore) GetApp(_ context.Context, id string) (sqlc.App, error) {
	if id != s.app.ID {
		return sqlc.App{}, sql.ErrNoRows
	}
	return s.app, nil
}

// GetChannelBindingByAppAndType 按 AppID（string）和 ChannelType 查渠道绑定。
func (s *channelWorkerStore) GetChannelBindingByAppAndType(_ context.Context, arg sqlc.GetChannelBindingByAppAndTypeParams) (sqlc.ChannelBinding, error) {
	if arg.AppID != s.binding.AppID || arg.ChannelType != s.binding.ChannelType {
		return sqlc.ChannelBinding{}, sql.ErrNoRows
	}
	return s.binding, nil
}

// SetChannelBindingChallenge :exec 语义仅返回 error；更新内存 binding 状态。
func (s *channelWorkerStore) SetChannelBindingChallenge(_ context.Context, arg sqlc.SetChannelBindingChallengeParams) error {
	s.binding.Status = domain.ChannelStatusPendingAuth
	s.binding.MetadataJson = arg.MetadataJson
	return nil
}

// SetChannelBindingStatus :exec 语义仅返回 error；更新 binding 状态与错误信息。
func (s *channelWorkerStore) SetChannelBindingStatus(_ context.Context, arg sqlc.SetChannelBindingStatusParams) error {
	s.binding.Status = arg.Status
	s.binding.LastError = arg.LastError
	return nil
}

// MarkChannelBindingBound :exec 语义仅返回 error；标记绑定为 bound，写入身份与渠道名。
func (s *channelWorkerStore) MarkChannelBindingBound(_ context.Context, arg sqlc.MarkChannelBindingBoundParams) error {
	s.binding.Status = domain.ChannelStatusBound
	s.binding.BoundIdentity = arg.BoundIdentity
	s.binding.ChannelName = arg.ChannelName
	s.binding.MetadataJson = arg.MetadataJson
	return nil
}

// SetAppStatus :exec 语义仅返回 error；记录状态更新。
func (s *channelWorkerStore) SetAppStatus(_ context.Context, arg sqlc.SetAppStatusParams) error {
	s.appStatusSet = true
	s.app.Status = arg.Status
	return nil
}

// CreateJob :exec 语义仅返回 error；记录入参（供断言 job type 等），不返回 job 对象。
// jobs 切片存档；测试只需检查 jobs 的 Type 字段，不依赖返回的 job ID。
func (s *channelWorkerStore) CreateJob(_ context.Context, arg sqlc.CreateJobParams) error {
	// ID 由调用方（source）自行生成，这里用 arg.ID 保留便于排查。
	job := sqlc.Job{
		ID:          arg.ID,
		Type:        arg.Type,
		Status:      domain.JobStatusPending,
		RunAfter:    arg.RunAfter,
		MaxAttempts: arg.MaxAttempts,
		PayloadJson: arg.PayloadJson,
	}
	s.jobs = append(s.jobs, job)
	return nil
}

// CreateAuditLog :exec 语义仅返回 error；存档入参供断言。
func (s *channelWorkerStore) CreateAuditLog(_ context.Context, arg sqlc.CreateAuditLogParams) error {
	s.auditLogs = append(s.auditLogs, arg)
	return nil
}

// SetFeishuCredentials :exec 语义仅返回 error；记录飞书凭证 metadata 与状态，
// 供阶段1注入断言（feishuMeta 落 injected=true、secret 密文）。
func (s *channelWorkerStore) SetFeishuCredentials(_ context.Context, arg sqlc.SetFeishuCredentialsParams) error {
	s.feishuMeta = arg.MetadataJson
	s.binding.MetadataJson = arg.MetadataJson
	s.binding.Status = arg.Status
	return nil
}
