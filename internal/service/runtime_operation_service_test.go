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

func TestRuntimeOperationEnqueuesNotifierWhenProvided(t *testing.T) {
	store := newRuntimeOperationStub(t)
	notifier := &fakeNotifier{}
	svc := NewRuntimeOperationService(store, notifier)

	result, err := svc.Trigger(context.Background(), platformAdmin(), testRuntimeOpAppID, RuntimeOperationStop)
	if err != nil {
		t.Fatalf("Trigger err = %v", err)
	}
	if notifier.lastJobID != result.JobID {
		t.Fatalf("notifier 收到的 jobID = %q, want %q", notifier.lastJobID, result.JobID)
	}
}

func TestRuntimeOperationSurvivesNotifierError(t *testing.T) {
	store := newRuntimeOperationStub(t)
	notifier := &fakeNotifier{err: errors.New("redis down")}
	svc := NewRuntimeOperationService(store, notifier)

	if _, err := svc.Trigger(context.Background(), platformAdmin(), testRuntimeOpAppID, RuntimeOperationStop); err != nil {
		t.Fatalf("notifier 失败时 service 不应冒泡: %v", err)
	}
}

func TestRequestInitialize_HappyPathFromError(t *testing.T) {
	store := newRuntimeOperationStub(t)
	store.app.Status = domain.AppStatusError
	store.app.ApiKeyStatus = domain.APIKeyStatusError
	store.app.ContainerID = pgtype.Text{String: "old", Valid: true}
	notifier := &fakeNotifier{}
	svc := NewRuntimeOperationService(store, notifier)

	result, err := svc.RequestInitialize(context.Background(), platformAdmin(), testRuntimeOpAppID)
	if err != nil {
		t.Fatalf("RequestInitialize err = %v", err)
	}
	if result.JobID == "" || result.Operation != "initialize" {
		t.Fatalf("result = %+v", result)
	}
	if store.app.Status != domain.AppStatusDraft {
		t.Fatalf("status 未重置: %q", store.app.Status)
	}
	if store.app.ApiKeyStatus != domain.APIKeyStatusPending {
		t.Fatalf("api_key_status 未重置: %q", store.app.ApiKeyStatus)
	}
	if store.app.ContainerID.Valid {
		t.Fatalf("container_id 应该被清空，实际 = %+v", store.app.ContainerID)
	}
	if store.lastJobType != domain.JobTypeAppInitialize {
		t.Fatalf("入队 job 类型 = %q, want app_initialize", store.lastJobType)
	}
	if !store.auditWritten {
		t.Fatal("应当写审计日志")
	}
	if notifier.lastJobID != result.JobID {
		t.Fatalf("notifier.lastJobID = %q, want %q", notifier.lastJobID, result.JobID)
	}
}

func TestRequestInitialize_RejectsRunningStatus(t *testing.T) {
	store := newRuntimeOperationStub(t)
	store.app.Status = domain.AppStatusRunning
	svc := NewRuntimeOperationService(store)
	_, err := svc.RequestInitialize(context.Background(), platformAdmin(), testRuntimeOpAppID)
	if !errors.Is(err, ErrAppNotReinitializable) {
		t.Fatalf("err = %v, want ErrAppNotReinitializable", err)
	}
}

func TestInspectApp_NoContainerReturnsSentinel(t *testing.T) {
	store := newRuntimeOperationStub(t)
	store.app.ContainerID = pgtype.Text{}
	svc := NewRuntimeOperationService(store)
	view, err := svc.InspectApp(context.Background(), platformAdmin(), testRuntimeOpAppID)
	if err != nil {
		t.Fatalf("InspectApp err = %v", err)
	}
	if view.Status != "no_container" {
		t.Fatalf("status = %q, want no_container", view.Status)
	}
	if view.Container != nil {
		t.Fatal("container 应当为 nil")
	}
}

func TestInspectApp_DelegatesToInspectorWhenAvailable(t *testing.T) {
	store := newRuntimeOperationStub(t)
	store.app.ContainerID = pgtype.Text{String: "ctr-x", Valid: true}
	svc := NewRuntimeOperationService(store)
	svc.SetInspector(stubInspector{info: RuntimeContainerInfo{ID: "ctr-x", Status: "running"}})
	view, err := svc.InspectApp(context.Background(), platformAdmin(), testRuntimeOpAppID)
	if err != nil {
		t.Fatalf("InspectApp err = %v", err)
	}
	if view.Status != "running" || view.Container == nil || view.Container.ID != "ctr-x" {
		t.Fatalf("view = %+v", view)
	}
}

func TestInspectApp_FallsBackToDBStatusWithoutInspector(t *testing.T) {
	store := newRuntimeOperationStub(t)
	store.app.ContainerID = pgtype.Text{String: "ctr-x", Valid: true}
	store.app.Status = domain.AppStatusRunning
	svc := NewRuntimeOperationService(store)
	view, err := svc.InspectApp(context.Background(), platformAdmin(), testRuntimeOpAppID)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if view.Status != domain.AppStatusRunning {
		t.Fatalf("status = %q, want running", view.Status)
	}
}

func TestInspectApp_InspectorErrorMapsToErrorStatus(t *testing.T) {
	store := newRuntimeOperationStub(t)
	store.app.ContainerID = pgtype.Text{String: "ctr-x", Valid: true}
	svc := NewRuntimeOperationService(store)
	svc.SetInspector(stubInspector{err: errors.New("connection refused")})
	view, err := svc.InspectApp(context.Background(), platformAdmin(), testRuntimeOpAppID)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if view.Status != "error" {
		t.Fatalf("status = %q, want error", view.Status)
	}
}

type stubInspector struct {
	info RuntimeContainerInfo
	err  error
}

func (s stubInspector) InspectContainer(_ context.Context, _, _ string) (RuntimeContainerInfo, error) {
	if s.err != nil {
		return RuntimeContainerInfo{}, s.err
	}
	return s.info, nil
}

func TestRequestInitialize_DeniesOtherOrg(t *testing.T) {
	store := newRuntimeOperationStub(t)
	store.app.Status = domain.AppStatusError
	svc := NewRuntimeOperationService(store)
	_, err := svc.RequestInitialize(context.Background(), auth.Principal{Role: domain.UserRoleOrgAdmin, OrgID: "other"}, testRuntimeOpAppID)
	if !errors.Is(err, ErrRuntimeOperationDenied) {
		t.Fatalf("err = %v, want ErrRuntimeOperationDenied", err)
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

func (s *runtimeOperationStub) SetAppStatus(_ context.Context, arg sqlc.SetAppStatusParams) (sqlc.App, error) {
	s.app.Status = arg.Status
	return s.app, nil
}

func (s *runtimeOperationStub) SetAppNewAPIKey(_ context.Context, arg sqlc.SetAppNewAPIKeyParams) (sqlc.App, error) {
	s.app.ApiKeyStatus = arg.ApiKeyStatus
	s.app.NewapiKeyID = arg.NewapiKeyID
	s.app.NewapiKeyCiphertext = arg.NewapiKeyCiphertext
	return s.app, nil
}

func (s *runtimeOperationStub) SetAppContainer(_ context.Context, arg sqlc.SetAppContainerParams) (sqlc.App, error) {
	s.app.ContainerID = arg.ContainerID
	s.app.ContainerName = arg.ContainerName
	return s.app, nil
}

var fakeNotFound = errors.New("not found")

type fakeNotifier struct {
	lastJobID string
	err       error
}

func (f *fakeNotifier) Enqueue(_ context.Context, jobID string) error {
	f.lastJobID = jobID
	return f.err
}
