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

const (
	testRuntimeOpAppID = "00000000-0000-0000-0000-000000001001"
	testRuntimeOpOrg   = "00000000-0000-0000-0000-000000001002"
	testRuntimeOpOwner = "00000000-0000-0000-0000-000000001003"
)

func TestRuntimeOperationTriggersJobAndAudit(t *testing.T) {
	store := newRuntimeOperationStub(t)
	svc := NewRuntimeOperationService(store)

	result, err := svc.Trigger(context.Background(), platformAdmin(), testRuntimeOpAppID, RuntimeOperationStart)
	if err != nil {
		t.Fatalf("Trigger() error = %v", err)
	}
	if result.JobID == "" || result.Operation != RuntimeOperationStart {
		t.Fatalf("result = %+v", result)
	}
	if store.lastJobType != domain.JobTypeAppStartContainer || !store.auditWritten {
		t.Fatalf("store side effects not as expected: %+v", store)
	}
}

func TestRuntimeOperationDeniesOtherOrg(t *testing.T) {
	store := newRuntimeOperationStub(t)
	svc := NewRuntimeOperationService(store)

	_, err := svc.Trigger(context.Background(), auth.Principal{Role: domain.UserRoleOrgAdmin, OrgID: "another-org"}, testRuntimeOpAppID, RuntimeOperationStop)
	if !errors.Is(err, ErrRuntimeOperationDenied) {
		t.Fatalf("error = %v, want ErrRuntimeOperationDenied", err)
	}
}

func TestRuntimeOperationRejectsUnknown(t *testing.T) {
	store := newRuntimeOperationStub(t)
	svc := NewRuntimeOperationService(store)

	if _, err := svc.Trigger(context.Background(), platformAdmin(), testRuntimeOpAppID, "boom"); err == nil {
		t.Fatalf("expected error for unsupported op")
	}
}

func TestRuntimeOperationMembersCanOnlyTriggerOwnApp(t *testing.T) {
	store := newRuntimeOperationStub(t)
	svc := NewRuntimeOperationService(store)

	if _, err := svc.Trigger(context.Background(), auth.Principal{Role: domain.UserRoleOrgMember, OrgID: testRuntimeOpOrg, UserID: "stranger"}, testRuntimeOpAppID, RuntimeOperationRestart); !errors.Is(err, ErrRuntimeOperationDenied) {
		t.Fatalf("error = %v, want ErrRuntimeOperationDenied", err)
	}

	if _, err := svc.Trigger(context.Background(), auth.Principal{Role: domain.UserRoleOrgMember, OrgID: testRuntimeOpOrg, UserID: testRuntimeOpOwner}, testRuntimeOpAppID, RuntimeOperationRestart); err != nil {
		t.Fatalf("expected owner allowed, got %v", err)
	}
}

type runtimeOperationStub struct {
	t            *testing.T
	app          sqlc.App
	lastJobType  string
	auditWritten bool
}

func newRuntimeOperationStub(t *testing.T) *runtimeOperationStub {
	app := sqlc.App{
		ID:           mustUUID(t, testRuntimeOpAppID),
		OrgID:        mustUUID(t, testRuntimeOpOrg),
		OwnerUserID:  mustUUID(t, testRuntimeOpOwner),
		Status:       domain.AppStatusRunning,
		PersonaMode:  domain.PersonaModeOrgInherited,
		ApiKeyStatus: domain.APIKeyStatusActive,
	}
	return &runtimeOperationStub{t: t, app: app}
}

func (s *runtimeOperationStub) GetApp(_ context.Context, id pgtype.UUID) (sqlc.App, error) {
	if id != s.app.ID {
		return sqlc.App{}, fakeNotFound
	}
	return s.app, nil
}

func (s *runtimeOperationStub) CreateJob(_ context.Context, arg sqlc.CreateJobParams) (sqlc.Job, error) {
	s.lastJobType = arg.Type
	return sqlc.Job{ID: mustUUID(s.t, "00000000-0000-0000-0000-000000001ff1"), Type: arg.Type}, nil
}

func (s *runtimeOperationStub) CreateAuditLog(_ context.Context, _ sqlc.CreateAuditLogParams) (sqlc.AuditLog, error) {
	s.auditWritten = true
	return sqlc.AuditLog{}, nil
}

var fakeNotFound = errors.New("not found")
