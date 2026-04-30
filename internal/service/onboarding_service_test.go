package service

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/store/sqlc"
)

func TestOnboardMemberCommitsOnSuccess(t *testing.T) {
	store := newOnboardingStub(t)
	tx := &txRunnerStub{store: store}
	svc := NewMemberOnboardingService(tx, fakeHash)

	result, err := svc.OnboardMember(context.Background(), platformAdmin(), testOrgID, OnboardMemberInput{
		Username: "alice", DisplayName: "Alice", Password: "pwd", AppName: "alice-bot",
	})
	if err != nil {
		t.Fatalf("OnboardMember() error = %v", err)
	}
	if result.JobID == "" || result.App.Name != "alice-bot" || result.Member.Username != "alice" {
		t.Fatalf("result = %+v", result)
	}
	if !tx.committed {
		t.Fatalf("expected commit")
	}
	if store.users == 0 || store.apps == 0 || store.bindings == 0 || store.audits == 0 || store.jobs == 0 {
		t.Fatalf("missing writes: %+v", store.counters())
	}
}

func TestOnboardMemberRollsBackWhenAppCreationFails(t *testing.T) {
	store := newOnboardingStub(t)
	store.appErr = errors.New("duplicate app for owner")
	tx := &txRunnerStub{store: store}
	svc := NewMemberOnboardingService(tx, fakeHash)

	_, err := svc.OnboardMember(context.Background(), platformAdmin(), testOrgID, OnboardMemberInput{
		Username: "alice", DisplayName: "Alice", Password: "pwd", AppName: "alice-bot",
	})
	if err == nil {
		t.Fatalf("expected error")
	}
	if tx.committed {
		t.Fatalf("expected rollback")
	}
}

func TestOnboardMemberRollsBackWhenJobCreationFails(t *testing.T) {
	store := newOnboardingStub(t)
	store.jobErr = errors.New("redis blocked")
	tx := &txRunnerStub{store: store}
	svc := NewMemberOnboardingService(tx, fakeHash)

	_, err := svc.OnboardMember(context.Background(), platformAdmin(), testOrgID, OnboardMemberInput{
		Username: "alice", DisplayName: "Alice", Password: "pwd", AppName: "alice-bot",
	})
	if err == nil {
		t.Fatalf("expected error")
	}
	if tx.committed {
		t.Fatalf("expected rollback")
	}
}

func TestOnboardMemberRequiresOrgManagement(t *testing.T) {
	tx := &txRunnerStub{store: newOnboardingStub(t)}
	svc := NewMemberOnboardingService(tx, fakeHash)

	_, err := svc.OnboardMember(context.Background(), auth.Principal{Role: domain.UserRoleOrgAdmin, OrgID: testOrg2ID}, testOrgID, OnboardMemberInput{
		Username: "alice", DisplayName: "Alice", Password: "pwd", AppName: "alice-bot",
	})
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("OnboardMember() error = %v, want ErrForbidden", err)
	}
}

func TestOnboardMemberRejectsDisabledOrg(t *testing.T) {
	store := newOnboardingStub(t)
	store.org.Status = domain.StatusDisabled
	tx := &txRunnerStub{store: store}
	svc := NewMemberOnboardingService(tx, fakeHash)

	_, err := svc.OnboardMember(context.Background(), platformAdmin(), testOrgID, OnboardMemberInput{
		Username: "alice", DisplayName: "Alice", Password: "pwd", AppName: "alice-bot",
	})
	if !errors.Is(err, ErrMemberCreateInvalid) {
		t.Fatalf("OnboardMember() error = %v, want ErrMemberCreateInvalid", err)
	}
}

type txRunnerStub struct {
	store     *onboardingStub
	committed bool
}

func (r *txRunnerStub) WithTx(ctx context.Context, fn func(OnboardingStore) error) error {
	r.committed = false
	r.store.reset()
	if err := fn(r.store); err != nil {
		return err
	}
	r.committed = true
	r.store.commit()
	return nil
}

type onboardingStub struct {
	t        *testing.T
	org      sqlc.Organization
	users    int
	apps     int
	bindings int
	audits   int
	jobs     int
	staged   counters
	appErr   error
	jobErr   error
}

type counters struct{ users, apps, bindings, audits, jobs int }

func newOnboardingStub(t *testing.T) *onboardingStub {
	return &onboardingStub{
		t:   t,
		org: sqlc.Organization{ID: mustUUID(t, testOrgID), Status: domain.StatusActive, Name: "测试组织"},
	}
}

func (s *onboardingStub) counters() counters {
	return counters{s.users, s.apps, s.bindings, s.audits, s.jobs}
}

func (s *onboardingStub) reset() { s.staged = counters{} }

func (s *onboardingStub) commit() {
	s.users += s.staged.users
	s.apps += s.staged.apps
	s.bindings += s.staged.bindings
	s.audits += s.staged.audits
	s.jobs += s.staged.jobs
	s.staged = counters{}
}

func (s *onboardingStub) GetOrganization(_ context.Context, id pgtype.UUID) (sqlc.Organization, error) {
	if id != s.org.ID {
		return sqlc.Organization{}, errors.New("not found")
	}
	return s.org, nil
}

func (s *onboardingStub) CreateUser(_ context.Context, arg sqlc.CreateUserParams) (sqlc.User, error) {
	s.staged.users++
	return sqlc.User{
		ID:       mustUUID(s.t, "00000000-0000-0000-0000-000000000a01"),
		OrgID:    arg.OrgID,
		Username: arg.Username,
		Role:     arg.Role,
		Status:   arg.Status,
	}, nil
}

func (s *onboardingStub) CreateApp(_ context.Context, arg sqlc.CreateAppParams) (sqlc.App, error) {
	if s.appErr != nil {
		return sqlc.App{}, s.appErr
	}
	s.staged.apps++
	return sqlc.App{
		ID:           mustUUID(s.t, "00000000-0000-0000-0000-000000000b01"),
		OrgID:        arg.OrgID,
		OwnerUserID:  arg.OwnerUserID,
		Name:         arg.Name,
		Status:       arg.Status,
		PersonaMode:  arg.PersonaMode,
		ApiKeyStatus: arg.ApiKeyStatus,
	}, nil
}

func (s *onboardingStub) CreateChannelBinding(_ context.Context, arg sqlc.CreateChannelBindingParams) (sqlc.ChannelBinding, error) {
	s.staged.bindings++
	return sqlc.ChannelBinding{
		ID:          mustUUID(s.t, "00000000-0000-0000-0000-000000000c01"),
		AppID:       arg.AppID,
		ChannelType: arg.ChannelType,
		Status:      arg.Status,
	}, nil
}

func (s *onboardingStub) CreateAuditLog(_ context.Context, arg sqlc.CreateAuditLogParams) (sqlc.AuditLog, error) {
	s.staged.audits++
	return sqlc.AuditLog{ActorRole: arg.ActorRole, TargetType: arg.TargetType, TargetID: arg.TargetID, Action: arg.Action, Result: arg.Result}, nil
}

func (s *onboardingStub) CreateJob(_ context.Context, arg sqlc.CreateJobParams) (sqlc.Job, error) {
	if s.jobErr != nil {
		return sqlc.Job{}, s.jobErr
	}
	s.staged.jobs++
	return sqlc.Job{
		ID:   mustUUID(s.t, "00000000-0000-0000-0000-000000000d01"),
		Type: arg.Type,
	}, nil
}
