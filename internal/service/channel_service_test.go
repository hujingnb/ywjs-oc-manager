package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	null "github.com/guregu/null/v5"

	"github.com/stretchr/testify/require"
	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/integrations/channel"
	"oc-manager/internal/store/sqlc"
)

const (
	testChannelAppID = "00000000-0000-0000-0000-000000000c01"
	testChannelOrg   = "00000000-0000-0000-0000-000000000c02"
	testChannelOwner = "00000000-0000-0000-0000-000000000c03"
)

// TestChannelServiceBeginAuthSuccess 验证渠道服务开始认证成功的成功路径场景。
func TestChannelServiceBeginAuthSuccess(t *testing.T) {
	store := newChannelStub(t)
	registry := channel.NewRegistry()
	registry.MustRegister(&fakeAdapter{
		challenge: channel.AuthChallenge{Type: "qrcode", QRCode: "data:image/png;base64,xxx", ExpiresAt: time.Now().Add(time.Hour)},
	})
	svc := NewChannelService(store, registry)

	result, err := svc.BeginAuth(context.Background(), channelOrgAdminPrincipal(), testChannelAppID, domain.ChannelTypeWeChat)
	require.NoError(t, err)
	require.NotEqual(t, "", result.JobID)
	require.True(t, store.statusUpdated)
	require.Equal(t, domain.ChannelStatusPendingAuth, store.lastStatus)
	require.Len(t, store.jobs, 1)
	require.Equal(t, domain.JobTypeChannelStartLogin, store.jobs[0].Type)
	require.Len(t, store.auditLogs, 1)
	require.Equal(t, "app", store.auditLogs[0].TargetType)
	require.Equal(t, testChannelAppID, store.auditLogs[0].TargetID)
	require.Equal(t, "channel_auth_start", store.auditLogs[0].Action)
	require.Equal(t, "succeeded", store.auditLogs[0].Result)
	// 审计迁移：不再写冻结中文文案，改用 metadata.channel_type 存储渠道类型 code，供前端按语言渲染。
	require.False(t, store.auditLogs[0].DetailMessage.Valid, "新记录不应写入冻结文案")
	require.NotEmpty(t, store.auditLogs[0].MetadataJson, "channel_auth_start 应写入 metadata")
	var meta map[string]any
	require.NoError(t, json.Unmarshal(store.auditLogs[0].MetadataJson, &meta))
	require.Equal(t, domain.ChannelTypeWeChat, meta["channel_type"], "metadata.channel_type 应为渠道类型 code")
}

// TestChannelServiceBeginAuthMissingAdapter 验证渠道服务开始认证缺失适配器的异常或拒绝路径场景。
func TestChannelServiceBeginAuthMissingAdapter(t *testing.T) {
	svc := NewChannelService(newChannelStub(t), channel.NewRegistry())
	_, err := svc.BeginAuth(context.Background(), channelOrgAdminPrincipal(), testChannelAppID, "missing")
	require.ErrorIs(t, err, ErrChannelAdapterMissing)
}

// TestChannelServiceBeginAuthForbidden 验证渠道服务开始认证禁止访问的异常或拒绝路径场景。
func TestChannelServiceBeginAuthForbidden(t *testing.T) {
	store := newChannelStub(t)
	registry := channel.NewRegistry()
	registry.MustRegister(&fakeAdapter{})
	svc := NewChannelService(store, registry)

	_, err := svc.BeginAuth(context.Background(), auth.Principal{Role: domain.UserRoleOrgMember, OrgID: testChannelOrg, UserID: "00000000-0000-0000-0000-0000000000ff"}, testChannelAppID, domain.ChannelTypeWeChat)
	require.ErrorIs(t, err, ErrForbidden)
}

// TestChannelServiceBeginAuthPlatformAdminForbidden 验证渠道服务开始认证平台管理员禁止访问的异常或拒绝路径场景。
func TestChannelServiceBeginAuthPlatformAdminForbidden(t *testing.T) {
	store := newChannelStub(t)
	registry := channel.NewRegistry()
	registry.MustRegister(&fakeAdapter{})
	svc := NewChannelService(store, registry)

	_, err := svc.BeginAuth(context.Background(), platformAdmin(), testChannelAppID, domain.ChannelTypeWeChat)
	require.ErrorIs(t, err, ErrForbidden)
}

// TestChannelServicePollAuthMarksBound 验证渠道服务轮询认证Marks已绑定的预期行为场景。
func TestChannelServicePollAuthMarksBound(t *testing.T) {
	store := newChannelStub(t)
	store.binding.Status = domain.ChannelStatusBound
	store.binding.BoundIdentity = null.StringFrom("alice")
	store.binding.ChannelName = null.StringFrom("alice@wechat")
	store.binding.MetadataJson = []byte(`{"type":"qrcode","qrcode":"https://liteapp.weixin.qq.com/q/test","expires_at":"2026-05-03T12:00:00Z","hints":{"raw_qr":"https://liteapp.weixin.qq.com/q/test"}}`)
	registry := channel.NewRegistry()
	registry.MustRegister(&fakeAdapter{})
	svc := NewChannelService(store, registry)

	progress, err := svc.PollAuth(context.Background(), platformAdmin(), testChannelAppID, domain.ChannelTypeWeChat)
	require.NoError(t, err)
	require.Equal(t, string(channel.AuthStatusBound), progress.Status)
	require.Equal(t, "alice", progress.BoundIdentity)
	require.NotEmpty(t, progress.Metadata["qrcode"])
	require.NotEmpty(t, progress.Metadata["raw_qr"])
}

// TestChannelServicePollAuthPushesAppToRunningOnBound 验证渠道服务轮询认证Pushes应用到RunningOn已绑定的预期行为场景。
func TestChannelServicePollAuthPushesAppToRunningOnBound(t *testing.T) {
	// 状态推进由 channel_check_binding worker 负责，PollAuth 只读 DB。
	store := newChannelStub(t)
	store.binding.Status = domain.ChannelStatusBound
	registry := channel.NewRegistry()
	registry.MustRegister(&fakeAdapter{})
	svc := NewChannelService(store, registry)

	_, err := svc.PollAuth(context.Background(), platformAdmin(), testChannelAppID, domain.ChannelTypeWeChat)
	require.NoError(t, err)
	if store.appStatusSet || store.boundCalled {
		t.Fatalf("PollAuth 不应写 binding/app 状态")
	}
}

// TestChannelServicePollAuthDoesNotOverrideRunningStatus 验证渠道服务轮询认证Does未OverrideRunning状态的预期行为场景。
func TestChannelServicePollAuthDoesNotOverrideRunningStatus(t *testing.T) {
	// 已经 running 的应用再次 PollAuth bound 时不应再写一次 SetAppStatus。
	store := newChannelStub(t)
	store.binding.Status = domain.ChannelStatusBound
	store.app.Status = domain.AppStatusRunning
	registry := channel.NewRegistry()
	registry.MustRegister(&fakeAdapter{})
	svc := NewChannelService(store, registry)

	_, err := svc.PollAuth(context.Background(), platformAdmin(), testChannelAppID, domain.ChannelTypeWeChat)
	require.NoError(t, err)
	require.False(t, store.appStatusSet)
}

// TestChannelServicePollAuthDoesNotPushOnNonBindingWaiting 验证渠道服务轮询认证Does未PushOn非绑定Waiting的预期行为场景。
func TestChannelServicePollAuthDoesNotPushOnNonBindingWaiting(t *testing.T) {
	// stopped / error 状态时 bound 也不该自动推到 running——避免覆盖运维侧停机决策。
	store := newChannelStub(t)
	store.binding.Status = domain.ChannelStatusBound
	store.app.Status = domain.AppStatusStopped
	registry := channel.NewRegistry()
	registry.MustRegister(&fakeAdapter{})
	svc := NewChannelService(store, registry)

	_, err := svc.PollAuth(context.Background(), platformAdmin(), testChannelAppID, domain.ChannelTypeWeChat)
	require.NoError(t, err)
	require.False(t, store.appStatusSet)
}

// TestChannelServiceUnbindUpdatesStatus 验证渠道服务解绑Updates状态的预期行为场景。
func TestChannelServiceUnbindUpdatesStatus(t *testing.T) {
	store := newChannelStub(t)
	svc := NewChannelService(store, channel.NewRegistry())

	err := svc.Unbind(context.Background(), channelOrgAdminPrincipal(), testChannelAppID, domain.ChannelTypeWeChat)
	require.NoError(t, err)
	require.Equal(t, domain.ChannelStatusUnboundByUser, store.lastStatus)
}

// TestChannelServiceUnbindFeishuDeletesSecretKeys 验证飞书解绑删 Secret key + 重启 + 置 unbound_by_user。
func TestChannelServiceUnbindFeishuDeletesSecretKeys(t *testing.T) {
	store := newChannelStub(t)
	store.binding.ChannelType = domain.ChannelTypeFeishu
	patcher := &fakeFeishuPatcher{}
	restarter := &fakeRestarter{}
	svc := NewChannelService(store, channel.NewRegistry())
	svc.SetFeishuUnbindDeps(patcher, restarter)
	require.NoError(t, svc.Unbind(context.Background(), channelOrgAdminPrincipal(), testChannelAppID, domain.ChannelTypeFeishu))
	require.Equal(t, domain.ChannelStatusUnboundByUser, store.lastStatus)
	require.ElementsMatch(t, []string{"feishu-app-id", "feishu-app-secret", "feishu-domain"}, patcher.deleted)
	require.True(t, restarter.restarted)
}

// TestChannelServiceUnbindWechatUnchanged 验证微信解绑不调飞书依赖（patcher/restarter 不触发）。
func TestChannelServiceUnbindWechatUnchanged(t *testing.T) {
	store := newChannelStub(t) // 默认 wechat binding
	patcher := &fakeFeishuPatcher{}
	restarter := &fakeRestarter{}
	svc := NewChannelService(store, channel.NewRegistry())
	svc.SetFeishuUnbindDeps(patcher, restarter)
	require.NoError(t, svc.Unbind(context.Background(), channelOrgAdminPrincipal(), testChannelAppID, domain.ChannelTypeWeChat))
	require.Empty(t, patcher.deleted)
	require.False(t, restarter.restarted)
}

// TestChannelServiceUnbindPlatformAdminForbidden 验证渠道服务解绑平台管理员禁止访问的异常或拒绝路径场景。
func TestChannelServiceUnbindPlatformAdminForbidden(t *testing.T) {
	store := newChannelStub(t)
	svc := NewChannelService(store, channel.NewRegistry())

	err := svc.Unbind(context.Background(), platformAdmin(), testChannelAppID, domain.ChannelTypeWeChat)
	require.ErrorIs(t, err, ErrForbidden)
}

// TestChannelServiceBeginAuthFeishuManualCreatesBinding 验证飞书手填：
// 无绑定行时 create-on-demand，secret 加密写 metadata，并入队 channel_start_login job。
func TestChannelServiceBeginAuthFeishuManualCreatesBinding(t *testing.T) {
	store := newChannelStub(t)
	store.bindingMissing = true // GetChannelBindingByAppAndType 首次返回 ErrNoRows
	registry := channel.NewRegistry()
	registry.MustRegister(channel.NewFeishuAdapter(nil))
	svc := NewChannelService(store, registry)
	svc.SetCipher(newRuntimeTokenTestCipher(t)) // manual 模式需 cipher 加密 secret
	req := FeishuAuthInput{Mode: "manual", AppID: "cli_x", AppSecret: "sec", Domain: "feishu"}

	res, err := svc.BeginFeishuAuth(context.Background(), channelOrgAdminPrincipal(), testChannelAppID, req)
	require.NoError(t, err)
	require.NotEmpty(t, res.JobID)
	require.True(t, store.upsertCalled, "应 create-on-demand 绑定行")
	require.NotEmpty(t, store.feishuMeta, "应写入飞书 metadata")
	require.NotContains(t, string(store.feishuMeta), "\"sec\"", "secret 必须加密，不得明文出现")
	require.Len(t, store.jobs, 1)
	require.Equal(t, domain.JobTypeChannelStartLogin, store.jobs[0].Type)
}

// TestChannelServiceBeginAuthFeishuScanCreatesBinding 验证飞书扫码：
// create-on-demand 绑定行、metadata 不含凭证、入队 job，且无需 cipher。
func TestChannelServiceBeginAuthFeishuScanCreatesBinding(t *testing.T) {
	store := newChannelStub(t)
	store.bindingMissing = true
	registry := channel.NewRegistry()
	registry.MustRegister(channel.NewFeishuAdapter(nil))
	svc := NewChannelService(store, registry)

	res, err := svc.BeginFeishuAuth(context.Background(), channelOrgAdminPrincipal(), testChannelAppID, FeishuAuthInput{Mode: "scan", Domain: "lark"})
	require.NoError(t, err)
	require.NotEmpty(t, res.JobID)
	require.True(t, store.upsertCalled)
	require.NotContains(t, string(store.feishuMeta), "ciphertext", "扫码阶段尚无凭证")
	require.Len(t, store.jobs, 1)
}

// TestChannelServiceBeginFeishuAuthBoundShortCircuit 验证 bound 短路：
// 已绑定的飞书 app 再次发起，直接返回 bound，不重跑 upsert / 不写 metadata / 不入队 job。
func TestChannelServiceBeginFeishuAuthBoundShortCircuit(t *testing.T) {
	store := newChannelStub(t)
	// 现有飞书 binding 已是 bound 状态。
	store.binding.ChannelType = domain.ChannelTypeFeishu
	store.binding.Status = domain.ChannelStatusBound
	registry := channel.NewRegistry()
	registry.MustRegister(channel.NewFeishuAdapter(nil))
	svc := NewChannelService(store, registry)

	res, err := svc.BeginFeishuAuth(context.Background(), channelOrgAdminPrincipal(), testChannelAppID, FeishuAuthInput{Mode: "scan", Domain: "feishu"})
	require.NoError(t, err)
	require.Equal(t, domain.ChannelStatusBound, res.Status)
	require.Equal(t, domain.ChannelTypeFeishu, res.ChannelType)
	require.False(t, store.upsertCalled, "bound 短路不应 create-on-demand")
	require.Empty(t, store.feishuMeta, "bound 短路不应写 metadata")
	require.Empty(t, store.jobs, "bound 短路不应入队 job")
}

// TestChannelServiceBeginAuthFeishuManualMissingCredential 验证手填缺 app_id/secret 时拒绝。
func TestChannelServiceBeginAuthFeishuManualMissingCredential(t *testing.T) {
	store := newChannelStub(t)
	store.bindingMissing = true
	registry := channel.NewRegistry()
	registry.MustRegister(channel.NewFeishuAdapter(nil))
	svc := NewChannelService(store, registry)
	svc.SetCipher(newRuntimeTokenTestCipher(t))

	_, err := svc.BeginFeishuAuth(context.Background(), channelOrgAdminPrincipal(), testChannelAppID, FeishuAuthInput{Mode: "manual", AppID: "cli_x"})
	require.ErrorIs(t, err, ErrInvalidChannelCredential)
}

// TestChannelServicePollAuthRedactsSecret 验证 PollAuth 不把 *_ciphertext 透传前端。
func TestChannelServicePollAuthRedactsSecret(t *testing.T) {
	store := newChannelStub(t)
	store.binding.ChannelType = domain.ChannelTypeFeishu
	store.binding.MetadataJson = []byte(`{"app_id":"cli_x","app_secret_ciphertext":"ENC","domain":"feishu"}`)
	svc := NewChannelService(store, channel.NewRegistry())

	p, err := svc.PollAuth(context.Background(), platformAdmin(), testChannelAppID, domain.ChannelTypeFeishu)
	require.NoError(t, err)
	require.Equal(t, "cli_x", p.Metadata["app_id"])
	_, leaked := p.Metadata["app_secret_ciphertext"]
	require.False(t, leaked, "secret 密文不得透传前端")
}

// fakeFeishuPatcher 记录飞书解绑时对 app Secret 的增删 key，供断言三把飞书 key 被删除。
type fakeFeishuPatcher struct {
	deleted []string
	set     map[string]string
}

func (p *fakeFeishuPatcher) PatchSecretKeys(_ context.Context, _ string, set map[string]string, del []string) error {
	p.set = set
	p.deleted = append(p.deleted, del...)
	return nil
}

// fakeRestarter 记录解绑后是否触发了 app 运行时重启。
type fakeRestarter struct{ restarted bool }

func (r *fakeRestarter) RestartApp(_ context.Context, _ string) error { r.restarted = true; return nil }

type fakeAdapter struct {
	challenge channel.AuthChallenge
	progress  channel.AuthProgress
}

func (a *fakeAdapter) Type() string { return domain.ChannelTypeWeChat }
func (a *fakeAdapter) BeginAuth(_ context.Context, _ channel.AuthInput) (channel.AuthChallenge, error) {
	return a.challenge, nil
}
func (a *fakeAdapter) PollAuth(_ context.Context, _ channel.AuthInput) (channel.AuthProgress, error) {
	return a.progress, nil
}

type channelStub struct {
	t             *testing.T
	app           sqlc.App
	binding       sqlc.ChannelBinding
	statusUpdated bool
	lastStatus    string
	boundCalled   bool
	jobs          []sqlc.Job
	auditLogs     []sqlc.CreateAuditLogParams
	appStatusSet  bool
	lastAppStatus string
	appStatusErr  error
	// bindingMissing 为 true 时 GetChannelBindingByAppAndType 返回 ErrNoRows，
	// 用于模拟飞书 create-on-demand 场景下绑定行尚未建立。
	bindingMissing bool
	// upsertCalled 记录是否调用过 UpsertChannelBindingUnbound（create-on-demand）。
	upsertCalled bool
	// feishuMeta 记录 SetFeishuCredentials 写入的 metadata，用于断言 secret 已加密。
	feishuMeta []byte
}

func newChannelStub(t *testing.T) *channelStub {
	app := sqlc.App{
		ID:           mustUUID(t, testChannelAppID),
		OrgID:        mustUUID(t, testChannelOrg),
		OwnerUserID:  mustUUID(t, testChannelOwner),
		Status:       domain.AppStatusBindingWaiting,
		ApiKeyStatus: domain.APIKeyStatusActive,
	}
	binding := sqlc.ChannelBinding{
		ID:          mustUUID(t, "00000000-0000-0000-0000-000000000d01"),
		AppID:       app.ID,
		ChannelType: domain.ChannelTypeWeChat,
		Status:      domain.ChannelStatusUnbound,
	}
	return &channelStub{t: t, app: app, binding: binding}
}

func (s *channelStub) GetApp(_ context.Context, id string) (sqlc.App, error) {
	if id != s.app.ID {
		return sqlc.App{}, sql.ErrNoRows
	}
	return s.app, nil
}

func (s *channelStub) GetChannelBindingByAppAndType(_ context.Context, arg sqlc.GetChannelBindingByAppAndTypeParams) (sqlc.ChannelBinding, error) {
	if s.bindingMissing {
		return sqlc.ChannelBinding{}, sql.ErrNoRows
	}
	if arg.AppID != s.binding.AppID || arg.ChannelType != s.binding.ChannelType {
		return sqlc.ChannelBinding{}, sql.ErrNoRows
	}
	return s.binding, nil
}

// UpsertChannelBindingUnbound 记录 create-on-demand 调用，供飞书发起断言。
func (s *channelStub) UpsertChannelBindingUnbound(_ context.Context, _ sqlc.UpsertChannelBindingUnboundParams) error {
	s.upsertCalled = true
	return nil
}

// SetFeishuCredentials 记录飞书凭证 metadata 与状态，供加密/过滤断言。
func (s *channelStub) SetFeishuCredentials(_ context.Context, arg sqlc.SetFeishuCredentialsParams) error {
	s.feishuMeta = arg.MetadataJson
	s.binding.MetadataJson = arg.MetadataJson
	s.binding.Status = arg.Status
	return nil
}

func (s *channelStub) SetChannelBindingChallenge(_ context.Context, arg sqlc.SetChannelBindingChallengeParams) error {
	s.binding.MetadataJson = arg.MetadataJson
	return nil
}

func (s *channelStub) SetChannelBindingStatus(_ context.Context, arg sqlc.SetChannelBindingStatusParams) error {
	s.statusUpdated = true
	s.lastStatus = arg.Status
	s.binding.Status = arg.Status
	s.binding.LastError = arg.LastError
	return nil
}

func (s *channelStub) SetAppStatus(_ context.Context, arg sqlc.SetAppStatusParams) error {
	s.appStatusSet = true
	s.lastAppStatus = arg.Status
	if s.appStatusErr != nil {
		return s.appStatusErr
	}
	s.app.Status = arg.Status
	return nil
}

func (s *channelStub) MarkChannelBindingBound(_ context.Context, arg sqlc.MarkChannelBindingBoundParams) error {
	s.boundCalled = true
	s.binding.Status = domain.ChannelStatusBound
	s.binding.BoundIdentity = arg.BoundIdentity
	s.binding.ChannelName = arg.ChannelName
	return nil
}

func (s *channelStub) CreateJob(_ context.Context, arg sqlc.CreateJobParams) error {
	job := sqlc.Job{
		ID:          mustUUID(s.t, "00000000-0000-0000-0000-000000000d02"),
		Type:        arg.Type,
		Status:      domain.JobStatusPending,
		RunAfter:    arg.RunAfter,
		MaxAttempts: arg.MaxAttempts,
		PayloadJson: arg.PayloadJson,
	}
	s.jobs = append(s.jobs, job)
	return nil
}

func (s *channelStub) CreateAuditLog(_ context.Context, arg sqlc.CreateAuditLogParams) error {
	s.auditLogs = append(s.auditLogs, arg)
	return nil
}

func channelOrgAdminPrincipal() auth.Principal {
	return auth.Principal{
		Role:   domain.UserRoleOrgAdmin,
		OrgID:  testChannelOrg,
		UserID: "00000000-0000-0000-0000-000000000caa",
	}
}
