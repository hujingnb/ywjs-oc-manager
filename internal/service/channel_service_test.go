package service

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

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
	// 详情字段应展示渠道名称（中文），便于审计列表一眼识别。
	require.True(t, store.auditLogs[0].DetailMessage.Valid)
	require.Equal(t, "渠道 微信", store.auditLogs[0].DetailMessage.String)
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
	store.binding.BoundIdentity = pgtype.Text{String: "alice", Valid: true}
	store.binding.ChannelName = pgtype.Text{String: "alice@wechat", Valid: true}
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

// TestChannelServiceUnbindPlatformAdminForbidden 验证渠道服务解绑平台管理员禁止访问的异常或拒绝路径场景。
func TestChannelServiceUnbindPlatformAdminForbidden(t *testing.T) {
	store := newChannelStub(t)
	svc := NewChannelService(store, channel.NewRegistry())

	err := svc.Unbind(context.Background(), platformAdmin(), testChannelAppID, domain.ChannelTypeWeChat)
	require.ErrorIs(t, err, ErrForbidden)
}

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

func (s *channelStub) GetApp(_ context.Context, id pgtype.UUID) (sqlc.App, error) {
	if id != s.app.ID {
		return sqlc.App{}, pgx.ErrNoRows
	}
	return s.app, nil
}

func (s *channelStub) GetChannelBindingByAppAndType(_ context.Context, arg sqlc.GetChannelBindingByAppAndTypeParams) (sqlc.ChannelBinding, error) {
	if arg.AppID != s.binding.AppID || arg.ChannelType != s.binding.ChannelType {
		return sqlc.ChannelBinding{}, pgx.ErrNoRows
	}
	return s.binding, nil
}

func (s *channelStub) SetChannelBindingChallenge(_ context.Context, arg sqlc.SetChannelBindingChallengeParams) (sqlc.ChannelBinding, error) {
	s.binding.MetadataJson = arg.MetadataJson
	return s.binding, nil
}

func (s *channelStub) SetChannelBindingStatus(_ context.Context, arg sqlc.SetChannelBindingStatusParams) (sqlc.ChannelBinding, error) {
	s.statusUpdated = true
	s.lastStatus = arg.Status
	s.binding.Status = arg.Status
	s.binding.LastError = arg.LastError
	return s.binding, nil
}

func (s *channelStub) SetAppStatus(_ context.Context, arg sqlc.SetAppStatusParams) (sqlc.App, error) {
	s.appStatusSet = true
	s.lastAppStatus = arg.Status
	if s.appStatusErr != nil {
		return sqlc.App{}, s.appStatusErr
	}
	s.app.Status = arg.Status
	return s.app, nil
}

func (s *channelStub) MarkChannelBindingBound(_ context.Context, arg sqlc.MarkChannelBindingBoundParams) (sqlc.ChannelBinding, error) {
	s.boundCalled = true
	s.binding.Status = domain.ChannelStatusBound
	s.binding.BoundIdentity = arg.BoundIdentity
	s.binding.ChannelName = arg.ChannelName
	return s.binding, nil
}

func (s *channelStub) CreateJob(_ context.Context, arg sqlc.CreateJobParams) (sqlc.Job, error) {
	job := sqlc.Job{
		ID:          mustUUID(s.t, "00000000-0000-0000-0000-000000000d02"),
		Type:        arg.Type,
		Status:      domain.JobStatusPending,
		RunAfter:    arg.RunAfter,
		MaxAttempts: arg.MaxAttempts,
		PayloadJson: arg.PayloadJson,
	}
	s.jobs = append(s.jobs, job)
	return job, nil
}

func (s *channelStub) CreateAuditLog(_ context.Context, arg sqlc.CreateAuditLogParams) (sqlc.AuditLog, error) {
	s.auditLogs = append(s.auditLogs, arg)
	return sqlc.AuditLog{TargetType: arg.TargetType, TargetID: arg.TargetID, Action: arg.Action, Result: arg.Result}, nil
}

func channelOrgAdminPrincipal() auth.Principal {
	return auth.Principal{
		Role:   domain.UserRoleOrgAdmin,
		OrgID:  testChannelOrg,
		UserID: "00000000-0000-0000-0000-000000000caa",
	}
}
