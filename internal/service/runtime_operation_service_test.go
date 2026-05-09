package service

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/store/sqlc"
	"github.com/stretchr/testify/require"
)

// newDiscardLogger 返回丢弃所有输出的测试用 logger，避免测试日志污染。
func newDiscardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

const (
	testRuntimeOpAppID = "00000000-0000-0000-0000-000000001001"
	testRuntimeOpOrg   = "00000000-0000-0000-0000-000000001002"
	testRuntimeOpOwner = "00000000-0000-0000-0000-000000001003"
)

func TestRuntimeOperationTriggersJobAndAudit(t *testing.T) {
	store := newRuntimeOperationStub(t)
	svc := NewRuntimeOperationService(store, newDiscardLogger())

	result, err := svc.Trigger(context.Background(), platformAdmin(), testRuntimeOpAppID, RuntimeOperationStart)
	require.NoError(t, err)
	if result.JobID == "" || result.Operation != RuntimeOperationStart {
		t.Fatalf("result = %+v", result)
	}
	if store.lastJobType != domain.JobTypeAppStartContainer || !store.auditWritten {
		t.Fatalf("store side effects not as expected: %+v", store)
	}
}

func TestRuntimeOperationDeniesOtherOrg(t *testing.T) {
	store := newRuntimeOperationStub(t)
	svc := NewRuntimeOperationService(store, newDiscardLogger())

	_, err := svc.Trigger(context.Background(), auth.Principal{Role: domain.UserRoleOrgAdmin, OrgID: "another-org"}, testRuntimeOpAppID, RuntimeOperationStop)
	require.ErrorIs(t, err, ErrRuntimeOperationDenied)
}

func TestRuntimeOperationRejectsUnknown(t *testing.T) {
	store := newRuntimeOperationStub(t)
	svc := NewRuntimeOperationService(store, newDiscardLogger())

	_, err := svc.Trigger(context.Background(), platformAdmin(), testRuntimeOpAppID, "boom")
	require.Error(t, err)
}

func TestRuntimeOperationEnqueuesNotifierWhenProvided(t *testing.T) {
	store := newRuntimeOperationStub(t)
	notifier := &fakeNotifier{}
	svc := NewRuntimeOperationService(store, newDiscardLogger(), notifier)

	result, err := svc.Trigger(context.Background(), platformAdmin(), testRuntimeOpAppID, RuntimeOperationStop)
	require.NoError(t, err)
	require.Equal(t, result.JobID, notifier.lastJobID)
}

func TestRuntimeOperationSurvivesNotifierError(t *testing.T) {
	store := newRuntimeOperationStub(t)
	notifier := &fakeNotifier{err: errors.New("redis down")}
	svc := NewRuntimeOperationService(store, newDiscardLogger(), notifier)

	_, err := svc.Trigger(context.Background(), platformAdmin(), testRuntimeOpAppID, RuntimeOperationStop)
	require.NoError(t, err)
}

func TestRequestInitialize_HappyPathFromError(t *testing.T) {
	store := newRuntimeOperationStub(t)
	store.app.Status = domain.AppStatusError
	store.app.ApiKeyStatus = domain.APIKeyStatusError
	store.app.ContainerID = pgtype.Text{String: "old", Valid: true}
	notifier := &fakeNotifier{}
	svc := NewRuntimeOperationService(store, newDiscardLogger(), notifier)

	result, err := svc.RequestInitialize(context.Background(), platformAdmin(), testRuntimeOpAppID)
	require.NoError(t, err)
	if result.JobID == "" || result.Operation != "initialize" {
		t.Fatalf("result = %+v", result)
	}
	require.Equal(t, domain.AppStatusDraft, store.app.Status)
	require.Equal(t, domain.APIKeyStatusPending, store.app.ApiKeyStatus)
	require.False(t, store.app.ContainerID.Valid)
	require.Equal(t, domain.JobTypeAppInitialize, store.lastJobType)
	require.True(t, store.auditWritten)
	require.Equal(t, result.JobID, notifier.lastJobID)
}

func TestRequestInitialize_RejectsRunningStatus(t *testing.T) {
	store := newRuntimeOperationStub(t)
	store.app.Status = domain.AppStatusRunning
	svc := NewRuntimeOperationService(store, newDiscardLogger())
	_, err := svc.RequestInitialize(context.Background(), platformAdmin(), testRuntimeOpAppID)
	require.ErrorIs(t, err, ErrAppNotReinitializable)
}

func TestInspectApp_NoContainerReturnsSentinel(t *testing.T) {
	store := newRuntimeOperationStub(t)
	store.app.ContainerID = pgtype.Text{}
	svc := NewRuntimeOperationService(store, newDiscardLogger())
	view, err := svc.InspectApp(context.Background(), platformAdmin(), testRuntimeOpAppID)
	require.NoError(t, err)
	require.Equal(t, "no_container", view.Status)
	require.Nil(t, view.Container)
}

func TestInspectApp_DelegatesToInspectorWhenAvailable(t *testing.T) {
	store := newRuntimeOperationStub(t)
	store.app.ContainerID = pgtype.Text{String: "ctr-x", Valid: true}
	svc := NewRuntimeOperationService(store, newDiscardLogger())
	svc.SetInspector(stubInspector{info: RuntimeContainerInfo{ID: "ctr-x", Status: "running"}})
	view, err := svc.InspectApp(context.Background(), platformAdmin(), testRuntimeOpAppID)
	require.NoError(t, err)
	if view.Status != "running" || view.Container == nil || view.Container.ID != "ctr-x" {
		t.Fatalf("view = %+v", view)
	}
}

func TestInspectApp_FallsBackToDBStatusWithoutInspector(t *testing.T) {
	store := newRuntimeOperationStub(t)
	store.app.ContainerID = pgtype.Text{String: "ctr-x", Valid: true}
	store.app.Status = domain.AppStatusRunning
	svc := NewRuntimeOperationService(store, newDiscardLogger())
	view, err := svc.InspectApp(context.Background(), platformAdmin(), testRuntimeOpAppID)
	require.NoError(t, err)
	require.Equal(t, domain.AppStatusRunning, view.Status)
}

func TestInspectApp_InspectorErrorMapsToErrorStatus(t *testing.T) {
	store := newRuntimeOperationStub(t)
	store.app.ContainerID = pgtype.Text{String: "ctr-x", Valid: true}
	svc := NewRuntimeOperationService(store, newDiscardLogger())
	svc.SetInspector(stubInspector{err: errors.New("connection refused")})
	view, err := svc.InspectApp(context.Background(), platformAdmin(), testRuntimeOpAppID)
	require.NoError(t, err)
	require.Equal(t, "error", view.Status)
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
	svc := NewRuntimeOperationService(store, newDiscardLogger())
	_, err := svc.RequestInitialize(context.Background(), auth.Principal{Role: domain.UserRoleOrgAdmin, OrgID: "other"}, testRuntimeOpAppID)
	require.ErrorIs(t, err, ErrRuntimeOperationDenied)
}

func TestRuntimeOperationMembersCanOnlyTriggerOwnApp(t *testing.T) {
	store := newRuntimeOperationStub(t)
	svc := NewRuntimeOperationService(store, newDiscardLogger())

	if _, err := svc.Trigger(context.Background(), auth.Principal{Role: domain.UserRoleOrgMember, OrgID: testRuntimeOpOrg, UserID: "stranger"}, testRuntimeOpAppID, RuntimeOperationRestart); !errors.Is(err, ErrRuntimeOperationDenied) {
		t.Fatalf("error = %v, want ErrRuntimeOperationDenied", err)
	}

	_, err := svc.Trigger(context.Background(), auth.Principal{Role: domain.UserRoleOrgMember, OrgID: testRuntimeOpOrg, UserID: testRuntimeOpOwner}, testRuntimeOpAppID, RuntimeOperationRestart)
	require.NoError(t, err)
}

type runtimeOperationStub struct {
	t            *testing.T
	app          sqlc.App
	// userStatus 控制 GetUser 返回的用户状态；默认为 active。
	userStatus   string
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
	return &runtimeOperationStub{t: t, app: app, userStatus: domain.StatusActive}
}

func (s *runtimeOperationStub) GetApp(_ context.Context, id pgtype.UUID) (sqlc.App, error) {
	if id != s.app.ID {
		return sqlc.App{}, fakeNotFound
	}
	return s.app, nil
}

func (s *runtimeOperationStub) GetUser(_ context.Context, _ pgtype.UUID) (sqlc.User, error) {
	return sqlc.User{Status: s.userStatus}, nil
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

func TestTrigger_DisabledPrincipal_Denied(t *testing.T) {
	store := newRuntimeOperationStub(t)
	// 将主体状态设为 disabled，模拟账号被封禁后 token 仍未过期的场景。
	store.userStatus = domain.StatusDisabled
	svc := NewRuntimeOperationService(store, newDiscardLogger())

	_, err := svc.Trigger(context.Background(), platformAdmin(), testRuntimeOpAppID, RuntimeOperationStart)
	require.ErrorIs(t, err, ErrRuntimeOperationDenied)
}

func TestRequestInitialize_DisabledPrincipal_Denied(t *testing.T) {
	store := newRuntimeOperationStub(t)
	store.app.Status = domain.AppStatusError
	// 将主体状态设为 disabled，验证 RequestInitialize 同样拒绝被禁用账号。
	store.userStatus = domain.StatusDisabled
	svc := NewRuntimeOperationService(store, newDiscardLogger())

	_, err := svc.RequestInitialize(context.Background(), platformAdmin(), testRuntimeOpAppID)
	require.ErrorIs(t, err, ErrRuntimeOperationDenied)
}
