package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

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

func TestChannelServiceBeginAuthSuccess(t *testing.T) {
	store := newChannelStub(t)
	registry := channel.NewRegistry()
	registry.MustRegister(&fakeAdapter{
		challenge: channel.AuthChallenge{Type: "qrcode", QRCode: "data:image/png;base64,xxx", ExpiresAt: time.Now().Add(time.Hour)},
	})
	svc := NewChannelService(store, registry)

	result, err := svc.BeginAuth(context.Background(), platformAdmin(), testChannelAppID, domain.ChannelTypeWeChat)
	if err != nil {
		t.Fatalf("BeginAuth() error = %v", err)
	}
	if result.QRCode == "" {
		t.Fatalf("expected qr code in result")
	}
	if !store.statusUpdated || store.lastStatus != domain.ChannelStatusPendingAuth {
		t.Fatalf("expected status to be pending_auth, got %s", store.lastStatus)
	}
}

func TestChannelServiceBeginAuthMissingAdapter(t *testing.T) {
	svc := NewChannelService(newChannelStub(t), channel.NewRegistry())
	_, err := svc.BeginAuth(context.Background(), platformAdmin(), testChannelAppID, "missing")
	if !errors.Is(err, ErrChannelAdapterMissing) {
		t.Fatalf("error = %v, want ErrChannelAdapterMissing", err)
	}
}

func TestChannelServiceBeginAuthForbidden(t *testing.T) {
	store := newChannelStub(t)
	registry := channel.NewRegistry()
	registry.MustRegister(&fakeAdapter{})
	svc := NewChannelService(store, registry)

	_, err := svc.BeginAuth(context.Background(), auth.Principal{Role: domain.UserRoleOrgMember, OrgID: testChannelOrg, UserID: "00000000-0000-0000-0000-0000000000ff"}, testChannelAppID, domain.ChannelTypeWeChat)
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("error = %v, want ErrForbidden", err)
	}
}

func TestChannelServicePollAuthMarksBound(t *testing.T) {
	store := newChannelStub(t)
	registry := channel.NewRegistry()
	registry.MustRegister(&fakeAdapter{
		progress: channel.AuthProgress{
			Status:        channel.AuthStatusBound,
			BoundIdentity: "alice",
			ChannelName:   "alice@wechat",
			UpdatedAt:     time.Now(),
		},
	})
	svc := NewChannelService(store, registry)

	progress, err := svc.PollAuth(context.Background(), platformAdmin(), testChannelAppID, domain.ChannelTypeWeChat)
	if err != nil {
		t.Fatalf("PollAuth() error = %v", err)
	}
	if progress.Status != string(channel.AuthStatusBound) || progress.BoundIdentity != "alice" {
		t.Fatalf("progress = %+v", progress)
	}
	if !store.boundCalled {
		t.Fatalf("expected MarkChannelBindingBound to be called")
	}
}

func TestChannelServicePollAuthFillsIdentityFromResolver(t *testing.T) {
	// Sprint 0 实测：bound 时 stdout 不携带 wxid；service 必须经 resolver 从 plugin state 补 userId。
	store := newChannelStub(t)
	registry := channel.NewRegistry()
	registry.MustRegister(&fakeAdapter{
		progress: channel.AuthProgress{
			Status:        channel.AuthStatusBound,
			BoundIdentity: "", // 模拟 stdout 不带账号
			UpdatedAt:     time.Now(),
		},
	})
	svc := NewChannelService(store, registry)
	resolver := &fakeBindingResolver{identity: "o9cq800x@im.wechat"}
	svc.SetWeChatBindingResolver(resolver)

	progress, err := svc.PollAuth(context.Background(), platformAdmin(), testChannelAppID, domain.ChannelTypeWeChat)
	if err != nil {
		t.Fatalf("PollAuth err=%v", err)
	}
	if progress.BoundIdentity != "o9cq800x@im.wechat" {
		t.Fatalf("BoundIdentity = %q，应被 resolver 覆盖", progress.BoundIdentity)
	}
	if !store.boundCalled || store.binding.BoundIdentity.String != "o9cq800x@im.wechat" {
		t.Fatalf("DB 中 bound_identity = %q", store.binding.BoundIdentity.String)
	}
	if resolver.calls != 1 {
		t.Fatalf("resolver 调用次数 = %d", resolver.calls)
	}
}

func TestChannelServicePollAuthIgnoresResolverError(t *testing.T) {
	// resolver 失败时 BoundIdentity 留空，仍 mark bound（plugin 可能稍后才写文件，等下次 poll 重试）。
	store := newChannelStub(t)
	registry := channel.NewRegistry()
	registry.MustRegister(&fakeAdapter{
		progress: channel.AuthProgress{
			Status:    channel.AuthStatusBound,
			UpdatedAt: time.Now(),
		},
	})
	svc := NewChannelService(store, registry)
	svc.SetWeChatBindingResolver(&fakeBindingResolver{err: channel.ErrIdentityUnavailable})

	progress, err := svc.PollAuth(context.Background(), platformAdmin(), testChannelAppID, domain.ChannelTypeWeChat)
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if progress.BoundIdentity != "" {
		t.Fatalf("resolver 失败时 BoundIdentity 应留空，got %q", progress.BoundIdentity)
	}
	if !store.boundCalled {
		t.Fatalf("仍应 mark bound，等下次 poll 重试")
	}
}

type fakeBindingResolver struct {
	identity string
	err      error
	calls    int
}

func (f *fakeBindingResolver) ResolveWeChatBoundIdentity(_ context.Context, _, _ string) (string, error) {
	f.calls++
	if f.err != nil {
		return "", f.err
	}
	return f.identity, nil
}

func TestChannelServiceUnbindUpdatesStatus(t *testing.T) {
	store := newChannelStub(t)
	svc := NewChannelService(store, channel.NewRegistry())

	if err := svc.Unbind(context.Background(), platformAdmin(), testChannelAppID, domain.ChannelTypeWeChat); err != nil {
		t.Fatalf("Unbind() error = %v", err)
	}
	if store.lastStatus != domain.ChannelStatusUnboundByUser {
		t.Fatalf("status = %s, want %s", store.lastStatus, domain.ChannelStatusUnboundByUser)
	}
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
}

func newChannelStub(t *testing.T) *channelStub {
	app := sqlc.App{
		ID:           mustUUID(t, testChannelAppID),
		OrgID:        mustUUID(t, testChannelOrg),
		OwnerUserID:  mustUUID(t, testChannelOwner),
		Status:       domain.AppStatusBindingWaiting,
		PersonaMode:  domain.PersonaModeOrgInherited,
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

func (s *channelStub) MarkChannelBindingBound(_ context.Context, arg sqlc.MarkChannelBindingBoundParams) (sqlc.ChannelBinding, error) {
	s.boundCalled = true
	s.binding.Status = domain.ChannelStatusBound
	s.binding.BoundIdentity = arg.BoundIdentity
	s.binding.ChannelName = arg.ChannelName
	return s.binding, nil
}
