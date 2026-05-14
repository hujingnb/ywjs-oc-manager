package handlers

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/stretchr/testify/require"
	"oc-manager/internal/domain"
	"oc-manager/internal/integrations/channel"
	"oc-manager/internal/store/sqlc"
)

const (
	testChannelWorkerAppID   = "00000000-0000-0000-0000-00000000c101"
	testChannelWorkerOrgID   = "00000000-0000-0000-0000-00000000c102"
	testChannelWorkerOwnerID = "00000000-0000-0000-0000-00000000c103"
	testChannelWorkerNodeID  = "00000000-0000-0000-0000-00000000c104"
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
	handler := NewChannelStartLoginHandler(store, registry)

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
	require.Len(t, store.auditLogs, 1)
	require.Equal(t, "app", store.auditLogs[0].TargetType)
	require.Equal(t, testChannelWorkerAppID, store.auditLogs[0].TargetID)
	require.Equal(t, "channel_bound", store.auditLogs[0].Action)
	require.Equal(t, "succeeded", store.auditLogs[0].Result)
}

// TestChannelStartLoginHandlerRecordsFailedAudit 验证渠道启动登录失败时写入应用审计的错误记录场景。
func TestChannelStartLoginHandlerRecordsFailedAudit(t *testing.T) {
	store := newChannelWorkerStore(t)
	registry := channel.NewRegistry()
	registry.MustRegister(&workerFakeChannelAdapter{beginErr: errors.New("weixin qrcode failed")})
	handler := NewChannelStartLoginHandler(store, registry)

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

type workerFakeChannelAdapter struct {
	challenge channel.AuthChallenge
	progress  channel.AuthProgress
	beginErr  error
}

func (a *workerFakeChannelAdapter) Type() string { return domain.ChannelTypeWeChat }
func (a *workerFakeChannelAdapter) BeginAuth(_ context.Context, _ channel.AuthInput) (channel.AuthChallenge, error) {
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

func (r *workerFakeBindingResolver) ResolveWeChatBoundIdentity(_ context.Context, _, _ string) (string, error) {
	r.calls++
	return r.identity, nil
}

type channelWorkerStore struct {
	t            *testing.T
	app          sqlc.App
	binding      sqlc.ChannelBinding
	jobs         []sqlc.Job
	auditLogs    []sqlc.CreateAuditLogParams
	appStatusSet bool
}

func newChannelWorkerStore(t *testing.T) *channelWorkerStore {
	appID := mustWorkerUUID(t, testChannelWorkerAppID)
	app := sqlc.App{
		ID:            appID,
		OrgID:         mustWorkerUUID(t, testChannelWorkerOrgID),
		OwnerUserID:   mustWorkerUUID(t, testChannelWorkerOwnerID),
		RuntimeNodeID: mustWorkerUUID(t, testChannelWorkerNodeID),
		Status:        domain.AppStatusBindingWaiting,
		ContainerID:   pgtype.Text{String: "ctr-1", Valid: true},
	}
	return &channelWorkerStore{
		t:   t,
		app: app,
		binding: sqlc.ChannelBinding{
			ID:          mustWorkerUUID(t, "00000000-0000-0000-0000-00000000c105"),
			AppID:       appID,
			ChannelType: domain.ChannelTypeWeChat,
			Status:      domain.ChannelStatusUnbound,
		},
	}
}

func (s *channelWorkerStore) GetApp(_ context.Context, id pgtype.UUID) (sqlc.App, error) {
	if id != s.app.ID {
		return sqlc.App{}, pgx.ErrNoRows
	}
	return s.app, nil
}

func (s *channelWorkerStore) GetChannelBindingByAppAndType(_ context.Context, arg sqlc.GetChannelBindingByAppAndTypeParams) (sqlc.ChannelBinding, error) {
	if arg.AppID != s.binding.AppID || arg.ChannelType != s.binding.ChannelType {
		return sqlc.ChannelBinding{}, pgx.ErrNoRows
	}
	return s.binding, nil
}

func (s *channelWorkerStore) SetChannelBindingChallenge(_ context.Context, arg sqlc.SetChannelBindingChallengeParams) (sqlc.ChannelBinding, error) {
	s.binding.Status = domain.ChannelStatusPendingAuth
	s.binding.MetadataJson = arg.MetadataJson
	return s.binding, nil
}

func (s *channelWorkerStore) SetChannelBindingStatus(_ context.Context, arg sqlc.SetChannelBindingStatusParams) (sqlc.ChannelBinding, error) {
	s.binding.Status = arg.Status
	s.binding.LastError = arg.LastError
	return s.binding, nil
}

func (s *channelWorkerStore) MarkChannelBindingBound(_ context.Context, arg sqlc.MarkChannelBindingBoundParams) (sqlc.ChannelBinding, error) {
	s.binding.Status = domain.ChannelStatusBound
	s.binding.BoundIdentity = arg.BoundIdentity
	s.binding.ChannelName = arg.ChannelName
	s.binding.MetadataJson = arg.MetadataJson
	return s.binding, nil
}

func (s *channelWorkerStore) SetAppStatus(_ context.Context, arg sqlc.SetAppStatusParams) (sqlc.App, error) {
	s.appStatusSet = true
	s.app.Status = arg.Status
	return s.app, nil
}

func (s *channelWorkerStore) CreateJob(_ context.Context, arg sqlc.CreateJobParams) (sqlc.Job, error) {
	job := sqlc.Job{
		ID:          mustWorkerUUID(s.t, "00000000-0000-0000-0000-00000000c200"),
		Type:        arg.Type,
		Status:      domain.JobStatusPending,
		RunAfter:    arg.RunAfter,
		MaxAttempts: arg.MaxAttempts,
		PayloadJson: arg.PayloadJson,
	}
	s.jobs = append(s.jobs, job)
	return job, nil
}

func (s *channelWorkerStore) CreateAuditLog(_ context.Context, arg sqlc.CreateAuditLogParams) (sqlc.AuditLog, error) {
	s.auditLogs = append(s.auditLogs, arg)
	return sqlc.AuditLog{TargetType: arg.TargetType, TargetID: arg.TargetID, Action: arg.Action, Result: arg.Result, ErrorMessage: arg.ErrorMessage}, nil
}

func mustWorkerUUID(t *testing.T, value string) pgtype.UUID {
	t.Helper()
	var id pgtype.UUID
	err := id.Scan(value)
	require.NoError(t, err)
	return id
}
