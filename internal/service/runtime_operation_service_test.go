package service

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/stretchr/testify/require"
	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/store/sqlc"
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

// TestRuntimeOperationTriggersJobAndAudit 验证运行时OperationTriggers任务并审计的预期行为场景。
func TestRuntimeOperationTriggersJobAndAudit(t *testing.T) {
	store := newRuntimeOperationStub(t)
	svc := NewRuntimeOperationService(store, newDiscardLogger())

	result, err := svc.Trigger(context.Background(), runtimeOrgAdminPrincipal(), testRuntimeOpAppID, RuntimeOperationStart)
	require.NoError(t, err)
	require.NotEmpty(t, result.JobID)
	require.Equal(t, RuntimeOperationStart, result.Operation)
	require.Equal(t, domain.JobTypeAppStartContainer, store.lastJobType)
	require.True(t, store.auditWritten)
}

// TestRuntimeOperationDeniesOtherOrg 验证运行时OperationDenies其他组织的预期行为场景。
func TestRuntimeOperationDeniesOtherOrg(t *testing.T) {
	store := newRuntimeOperationStub(t)
	svc := NewRuntimeOperationService(store, newDiscardLogger())

	_, err := svc.Trigger(context.Background(), auth.Principal{Role: domain.UserRoleOrgAdmin, OrgID: "another-org"}, testRuntimeOpAppID, RuntimeOperationStop)
	require.ErrorIs(t, err, ErrRuntimeOperationDenied)
}

// TestRuntimeOperationRejectsUnknown 验证运行时Operation拒绝未知的异常或拒绝路径场景。
func TestRuntimeOperationRejectsUnknown(t *testing.T) {
	store := newRuntimeOperationStub(t)
	svc := NewRuntimeOperationService(store, newDiscardLogger())

	_, err := svc.Trigger(context.Background(), platformAdmin(), testRuntimeOpAppID, "boom")
	require.Error(t, err)
}

// TestRuntimeOperationEnqueuesNotifierWhenProvided 验证运行时OperationEnqueuesNotifier当Provided的预期行为场景。
func TestRuntimeOperationEnqueuesNotifierWhenProvided(t *testing.T) {
	store := newRuntimeOperationStub(t)
	notifier := &fakeNotifier{}
	svc := NewRuntimeOperationService(store, newDiscardLogger(), notifier)

	result, err := svc.Trigger(context.Background(), runtimeOrgAdminPrincipal(), testRuntimeOpAppID, RuntimeOperationStop)
	require.NoError(t, err)
	require.Equal(t, result.JobID, notifier.lastJobID)
}

// TestRuntimeOperationSurvivesNotifierError 验证运行时OperationSurvivesNotifier错误的预期行为场景。
func TestRuntimeOperationSurvivesNotifierError(t *testing.T) {
	store := newRuntimeOperationStub(t)
	notifier := &fakeNotifier{err: errors.New("redis down")}
	svc := NewRuntimeOperationService(store, newDiscardLogger(), notifier)

	_, err := svc.Trigger(context.Background(), runtimeOrgAdminPrincipal(), testRuntimeOpAppID, RuntimeOperationStop)
	require.NoError(t, err)
}

// TestRuntimeOperationRejectsPlatformAdminWrite 验证运行时Operation拒绝平台管理员写入的异常或拒绝路径场景。
func TestRuntimeOperationRejectsPlatformAdminWrite(t *testing.T) {
	store := newRuntimeOperationStub(t)
	svc := NewRuntimeOperationService(store, newDiscardLogger())

	_, err := svc.Trigger(context.Background(), platformAdmin(), testRuntimeOpAppID, RuntimeOperationStop)
	require.ErrorIs(t, err, ErrRuntimeOperationDenied)
}

// TestRequestInitialize_HappyPathFromError 验证请求初始化成功路径来自错误的成功路径场景。
func TestRequestInitialize_HappyPathFromError(t *testing.T) {
	store := newRuntimeOperationStub(t)
	store.app.Status = domain.AppStatusError
	store.app.ApiKeyStatus = domain.APIKeyStatusError
	store.app.ContainerID = pgtype.Text{String: "old", Valid: true}
	notifier := &fakeNotifier{}
	svc := NewRuntimeOperationService(store, newDiscardLogger(), notifier)

	result, err := svc.RequestInitialize(context.Background(), runtimeOrgAdminPrincipal(), testRuntimeOpAppID)
	require.NoError(t, err)
	require.NotEmpty(t, result.JobID)
	require.Equal(t, RuntimeOperation("initialize"), result.Operation)
	// 重置目标由 draft 改为 pulling_image,worker 直接进入第一阶段重跑(5.6)。
	require.Equal(t, domain.AppStatusPullingImage, store.app.Status)
	require.Equal(t, domain.APIKeyStatusPending, store.app.ApiKeyStatus)
	require.False(t, store.app.ContainerID.Valid)
	// 5.6 新增:ClearAppProgress 必须被调用,否则前端会看到上一次失败遗留的进度数。
	require.True(t, store.progressCleared)
	require.Equal(t, domain.JobTypeAppInitialize, store.lastJobType)
	require.True(t, store.auditWritten)
	require.Equal(t, result.JobID, notifier.lastJobID)
}

// TestRequestInitialize_RejectsRunningStatus 验证请求初始化拒绝Running状态的异常或拒绝路径场景。
func TestRequestInitialize_RejectsRunningStatus(t *testing.T) {
	store := newRuntimeOperationStub(t)
	store.app.Status = domain.AppStatusRunning
	svc := NewRuntimeOperationService(store, newDiscardLogger())
	_, err := svc.RequestInitialize(context.Background(), runtimeOrgAdminPrincipal(), testRuntimeOpAppID)
	require.ErrorIs(t, err, ErrAppNotReinitializable)
}

// TestRequestInitialize_RejectsPlatformAdminWrite 验证请求初始化拒绝平台管理员写入的异常或拒绝路径场景。
func TestRequestInitialize_RejectsPlatformAdminWrite(t *testing.T) {
	store := newRuntimeOperationStub(t)
	store.app.Status = domain.AppStatusError
	svc := NewRuntimeOperationService(store, newDiscardLogger())

	_, err := svc.RequestInitialize(context.Background(), platformAdmin(), testRuntimeOpAppID)
	require.ErrorIs(t, err, ErrRuntimeOperationDenied)
}

// TestInspectApp_NoContainerReturnsSentinel 验证检查应用无容器返回Sentinel的成功路径场景。
func TestInspectApp_NoContainerReturnsSentinel(t *testing.T) {
	store := newRuntimeOperationStub(t)
	store.app.ContainerID = pgtype.Text{}
	svc := NewRuntimeOperationService(store, newDiscardLogger())
	view, err := svc.InspectApp(context.Background(), platformAdmin(), testRuntimeOpAppID)
	require.NoError(t, err)
	require.Equal(t, "no_container", view.Status)
	require.Nil(t, view.Container)
}

// TestInspectApp_DelegatesToInspectorWhenAvailable 验证检查应用Delegates到检查or当可用的预期行为场景。
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

// TestInspectApp_FallsBackToDBStatusWithoutInspector 验证检查应用回退回退到DB状态不使用检查or的特殊分支或幂等场景。
func TestInspectApp_FallsBackToDBStatusWithoutInspector(t *testing.T) {
	store := newRuntimeOperationStub(t)
	store.app.ContainerID = pgtype.Text{String: "ctr-x", Valid: true}
	store.app.Status = domain.AppStatusRunning
	svc := NewRuntimeOperationService(store, newDiscardLogger())
	view, err := svc.InspectApp(context.Background(), platformAdmin(), testRuntimeOpAppID)
	require.NoError(t, err)
	require.Equal(t, domain.AppStatusRunning, view.Status)
}

// TestInspectApp_InspectorErrorMapsToErrorStatus 验证检查应用检查or错误映射到错误状态的错误映射或错误记录场景。
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

// TestRequestInitialize_DeniesOtherOrg 验证请求初始化Denies其他组织的预期行为场景。
func TestRequestInitialize_DeniesOtherOrg(t *testing.T) {
	store := newRuntimeOperationStub(t)
	store.app.Status = domain.AppStatusError
	svc := NewRuntimeOperationService(store, newDiscardLogger())
	_, err := svc.RequestInitialize(context.Background(), auth.Principal{Role: domain.UserRoleOrgAdmin, OrgID: "other"}, testRuntimeOpAppID)
	require.ErrorIs(t, err, ErrRuntimeOperationDenied)
}

// TestRuntimeOperationMembersCanOnlyTriggerOwnApp 验证运行时Operation成员权限判断仅触发本人应用的预期行为场景。
func TestRuntimeOperationMembersCanOnlyTriggerOwnApp(t *testing.T) {
	store := newRuntimeOperationStub(t)
	svc := NewRuntimeOperationService(store, newDiscardLogger())

	if _, err := svc.Trigger(context.Background(), auth.Principal{Role: domain.UserRoleOrgMember, OrgID: testRuntimeOpOrg, UserID: "stranger"}, testRuntimeOpAppID, RuntimeOperationRestart); !errors.Is(err, ErrRuntimeOperationDenied) {
		t.Fatalf("error = %v, want ErrRuntimeOperationDenied", err)
	}

	_, err := svc.Trigger(context.Background(), auth.Principal{Role: domain.UserRoleOrgMember, OrgID: testRuntimeOpOrg, UserID: testRuntimeOpOwner}, testRuntimeOpAppID, RuntimeOperationRestart)
	require.NoError(t, err)
}

func runtimeOrgAdminPrincipal() auth.Principal {
	return auth.Principal{
		Role:   domain.UserRoleOrgAdmin,
		OrgID:  testRuntimeOpOrg,
		UserID: "00000000-0000-0000-0000-0000000010aa",
	}
}

type runtimeOperationStub struct {
	t   *testing.T
	app sqlc.App
	// userStatus 控制 GetUser 返回的用户状态；默认为 active。
	userStatus   string
	lastJobType  string
	auditWritten bool
	// progressCleared 标记 ClearAppProgress 被调用过,
	// RequestInitialize 用例据此断言 5.6 的进度重置分支被走到。
	progressCleared bool
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

// ClearAppProgress 模拟 sqlc.ClearAppProgress:清空 progress_*,
// 让 RequestInitialize 测试能跑通新增的进度重置分支。
func (s *runtimeOperationStub) ClearAppProgress(_ context.Context, _ pgtype.UUID) (sqlc.App, error) {
	s.app.ProgressCurrent = pgtype.Int8{}
	s.app.ProgressTotal = pgtype.Int8{}
	s.progressCleared = true
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

// TestTrigger_DisabledPrincipal_Denied 验证触发禁用PrincipalDenied的预期行为场景。
func TestTrigger_DisabledPrincipal_Denied(t *testing.T) {
	store := newRuntimeOperationStub(t)
	// 将主体状态设为 disabled，模拟账号被封禁后 token 仍未过期的场景。
	store.userStatus = domain.StatusDisabled
	svc := NewRuntimeOperationService(store, newDiscardLogger())

	_, err := svc.Trigger(context.Background(), platformAdmin(), testRuntimeOpAppID, RuntimeOperationStart)
	require.ErrorIs(t, err, ErrRuntimeOperationDenied)
}

// TestRequestInitialize_DisabledPrincipal_Denied 验证请求初始化禁用PrincipalDenied的预期行为场景。
func TestRequestInitialize_DisabledPrincipal_Denied(t *testing.T) {
	store := newRuntimeOperationStub(t)
	store.app.Status = domain.AppStatusError
	// 将主体状态设为 disabled，验证 RequestInitialize 同样拒绝被禁用账号。
	store.userStatus = domain.StatusDisabled
	svc := NewRuntimeOperationService(store, newDiscardLogger())

	_, err := svc.RequestInitialize(context.Background(), platformAdmin(), testRuntimeOpAppID)
	require.ErrorIs(t, err, ErrRuntimeOperationDenied)
}
