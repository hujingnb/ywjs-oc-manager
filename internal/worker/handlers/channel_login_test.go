package handlers

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

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
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if store.binding.Status != domain.ChannelStatusPendingAuth {
		t.Fatalf("binding status = %q, want pending_auth", store.binding.Status)
	}
	metadata := string(store.binding.MetadataJson)
	if !strings.Contains(metadata, "data:image/png;base64,qr") || !strings.Contains(metadata, "raw_qr") {
		t.Fatalf("metadata_json 未包含二维码信息: %s", metadata)
	}
	if len(store.jobs) != 1 || store.jobs[0].Type != domain.JobTypeChannelCheckBinding {
		t.Fatalf("应入队 channel_check_binding，jobs=%+v", store.jobs)
	}
}

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
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if store.binding.Status != domain.ChannelStatusBound {
		t.Fatalf("binding status = %q, want bound", store.binding.Status)
	}
	if store.binding.BoundIdentity.String != "wxid_from_stdout" {
		t.Fatalf("bound_identity = %q", store.binding.BoundIdentity.String)
	}
	if !store.appStatusSet || store.app.Status != domain.AppStatusRunning {
		t.Fatalf("app 未推进到 running: set=%v status=%q", store.appStatusSet, store.app.Status)
	}
}

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
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if store.binding.BoundIdentity.String != "user-from-plugin-state" {
		t.Fatalf("bound_identity = %q", store.binding.BoundIdentity.String)
	}
	if resolver.calls != 1 {
		t.Fatalf("resolver calls = %d", resolver.calls)
	}
}

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
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if store.binding.Status != domain.ChannelStatusPendingAuth {
		t.Fatalf("binding status = %q", store.binding.Status)
	}
	if len(store.jobs) != 1 || store.jobs[0].Type != domain.JobTypeChannelCheckBinding {
		t.Fatalf("pending 状态应延迟重查，jobs=%+v", store.jobs)
	}
}

// TestChannelCheckBindingHandlerFallsBackToResolverWhenAdapterPending 校验：
// 当 PollAuth 返回 pending（plugin stdout 没输出 "bound"），但 plugin state 文件里
// 已经有真实账号 session 时（resolver 返回非空 identity），应当推到 bound 而不是
// 等到 expired。
//
// 这个 fallback 修复的是 OpenClaw weixin plugin 的真实行为：第二次扫码（同一微信
// 账号已授权过）plugin 静默成功不再 emit bound 事件，但 accounts.json 仍真实可用。
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
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if store.binding.Status != domain.ChannelStatusBound {
		t.Fatalf("binding status = %q, want bound (resolver fallback should have promoted)", store.binding.Status)
	}
	if store.binding.BoundIdentity.String != "o9cq800xszCM8jyoS9YpRKpvAN9c@im.wechat" {
		t.Fatalf("bound_identity = %q", store.binding.BoundIdentity.String)
	}
	if !store.appStatusSet {
		t.Fatal("app status 应被推进到 running")
	}
	if resolver.calls != 1 {
		t.Fatalf("resolver calls = %d, want 1", resolver.calls)
	}
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
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if store.binding.Status != domain.ChannelStatusPendingAuth {
		t.Fatalf("没有 resolver 时应保持 pending_auth, got %q", store.binding.Status)
	}
}

type workerFakeChannelAdapter struct {
	challenge channel.AuthChallenge
	progress  channel.AuthProgress
}

func (a *workerFakeChannelAdapter) Type() string { return domain.ChannelTypeWeChat }
func (a *workerFakeChannelAdapter) BeginAuth(_ context.Context, _ channel.AuthInput) (channel.AuthChallenge, error) {
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

func mustWorkerUUID(t *testing.T, value string) pgtype.UUID {
	t.Helper()
	var id pgtype.UUID
	if err := id.Scan(value); err != nil {
		t.Fatalf("scan uuid %s: %v", value, err)
	}
	return id
}
