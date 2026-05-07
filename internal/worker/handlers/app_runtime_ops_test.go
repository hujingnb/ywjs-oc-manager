package handlers

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	"oc-manager/internal/domain"
	"oc-manager/internal/integrations/newapi"
	"oc-manager/internal/store/sqlc"
)

const testRuntimeNodeID = "00000000-0000-0000-0000-000000000d01"

func runtimeStub(t *testing.T) *runtimeOpStub {
	t.Helper()
	return &runtimeOpStub{
		app: sqlc.App{
			ID:            mustUUIDForTest(t, testAppID),
			OrgID:         mustUUIDForTest(t, testOrgID),
			OwnerUserID:   mustUUIDForTest(t, testUsrID),
			RuntimeNodeID: mustUUIDForTest(t, testRuntimeNodeID),
			Status:        domain.AppStatusRunning,
			ContainerID:   pgtype.Text{String: "ctr-existing", Valid: true},
			ContainerName: pgtype.Text{String: "ocm-app", Valid: true},
			NewapiKeyID:   pgtype.Text{String: "42", Valid: true},
		},
	}
}

func runtimeJob(jobType, appID string) sqlc.Job {
	return sqlc.Job{Type: jobType, PayloadJson: []byte(`{"app_id":"` + appID + `"}`)}
}

func TestAppStartContainerHandler_HappyPath(t *testing.T) {
	stub := runtimeStub(t)
	containers := &fakeLifecycle{}
	handler := NewAppStartContainerHandler(stub, containers)

	if err := handler.Handle(context.Background(), runtimeJob(domain.JobTypeAppStartContainer, testAppID)); err != nil {
		t.Fatalf("Handle err = %v", err)
	}
	if containers.startCalls != 1 {
		t.Fatalf("StartContainer 调用次数 = %d, want 1", containers.startCalls)
	}
	if stub.statusUpdates[len(stub.statusUpdates)-1] != domain.AppStatusRunning {
		t.Fatalf("最后状态 = %q, want running", stub.statusUpdates[len(stub.statusUpdates)-1])
	}
}

func TestAppStartContainerHandler_RejectsWithoutContainerID(t *testing.T) {
	stub := runtimeStub(t)
	stub.app.ContainerID = pgtype.Text{}
	handler := NewAppStartContainerHandler(stub, &fakeLifecycle{})
	err := handler.Handle(context.Background(), runtimeJob(domain.JobTypeAppStartContainer, testAppID))
	if err == nil {
		t.Fatal("缺 container_id 时应当报错")
	}
}

func TestAppStartContainerHandler_PropagatesAdapterError(t *testing.T) {
	stub := runtimeStub(t)
	containers := &fakeLifecycle{startErr: errors.New("docker boom")}
	handler := NewAppStartContainerHandler(stub, containers)
	err := handler.Handle(context.Background(), runtimeJob(domain.JobTypeAppStartContainer, testAppID))
	if err == nil {
		t.Fatal("adapter 失败时应冒泡")
	}
	if len(stub.statusUpdates) != 0 {
		t.Fatal("失败时不应更新状态")
	}
}

func TestAppStopContainerHandler_HappyPath(t *testing.T) {
	stub := runtimeStub(t)
	containers := &fakeLifecycle{}
	handler := NewAppStopContainerHandler(stub, containers)
	if err := handler.Handle(context.Background(), runtimeJob(domain.JobTypeAppStopContainer, testAppID)); err != nil {
		t.Fatalf("Handle err = %v", err)
	}
	if containers.stopCalls != 1 {
		t.Fatalf("StopContainer 调用次数 = %d", containers.stopCalls)
	}
	if stub.statusUpdates[len(stub.statusUpdates)-1] != domain.AppStatusStopped {
		t.Fatalf("最后状态 = %q", stub.statusUpdates[len(stub.statusUpdates)-1])
	}
}

func TestAppStopContainerHandler_NoContainerStillUpdatesStatus(t *testing.T) {
	stub := runtimeStub(t)
	stub.app.ContainerID = pgtype.Text{}
	containers := &fakeLifecycle{}
	handler := NewAppStopContainerHandler(stub, containers)
	if err := handler.Handle(context.Background(), runtimeJob(domain.JobTypeAppStopContainer, testAppID)); err != nil {
		t.Fatalf("Handle err = %v", err)
	}
	if containers.stopCalls != 0 {
		t.Fatal("没 container 时不应调 docker stop")
	}
	if stub.statusUpdates[len(stub.statusUpdates)-1] != domain.AppStatusStopped {
		t.Fatal("仍应推 stopped 状态")
	}
}

func TestAppRestartContainerHandler_HappyPath(t *testing.T) {
	stub := runtimeStub(t)
	containers := &fakeLifecycle{}
	handler := NewAppRestartContainerHandler(stub, containers)
	if err := handler.Handle(context.Background(), runtimeJob(domain.JobTypeAppRestartContainer, testAppID)); err != nil {
		t.Fatalf("Handle err = %v", err)
	}
	if containers.restartCalls != 1 {
		t.Fatalf("RestartContainer 调用次数 = %d", containers.restartCalls)
	}
	if stub.statusUpdates[len(stub.statusUpdates)-1] != domain.AppStatusRunning {
		t.Fatalf("最后状态 = %q", stub.statusUpdates[len(stub.statusUpdates)-1])
	}
}

func TestAppDeleteHandler_HappyPath(t *testing.T) {
	stub := runtimeStub(t)
	containers := &fakeLifecycle{}
	disabler := &fakeDisabler{}
	fileOps := &fakeFileOps{}
	handler := NewAppDeleteHandler(stub, containers, disabler, fileOps)
	if err := handler.Handle(context.Background(), runtimeJob(domain.JobTypeAppDelete, testAppID)); err != nil {
		t.Fatalf("Handle err = %v", err)
	}
	if containers.stopCalls != 1 || containers.removeCalls != 1 {
		t.Fatalf("stop=%d remove=%d, want 1/1", containers.stopCalls, containers.removeCalls)
	}
	if disabler.id != 42 || disabler.status != 2 {
		t.Fatalf("disabler 调用 = (%d,%d), want (42,2)", disabler.id, disabler.status)
	}
	if fileOps.deletedAppID != testAppID {
		t.Fatalf("fileOps 收到 appID = %q", fileOps.deletedAppID)
	}
	if !stub.softDeleted {
		t.Fatal("未触发 SoftDeleteApp")
	}
}

func TestAppDeleteHandler_PrefersArchiveOverDelete(t *testing.T) {
	// Sprint 2：fileOps 实现 AppArchiver 时应优先归档而非直接删除，保留节点目录。
	stub := runtimeStub(t)
	containers := &fakeLifecycle{}
	disabler := &fakeDisabler{}
	fileOps := &fakeArchivingFileOps{}
	handler := NewAppDeleteHandler(stub, containers, disabler, fileOps)
	if err := handler.Handle(context.Background(), runtimeJob(domain.JobTypeAppDelete, testAppID)); err != nil {
		t.Fatalf("Handle err = %v", err)
	}
	if fileOps.archivedAppID != testAppID {
		t.Fatalf("ArchiveApp 应被调，got archivedAppID=%q", fileOps.archivedAppID)
	}
	if fileOps.deletedAppID != "" {
		t.Fatalf("有 ArchiveApp 实现时不应调 DeleteAppPath，got %q", fileOps.deletedAppID)
	}
}

func TestAppDeleteHandler_PropagatesArchiveError(t *testing.T) {
	stub := runtimeStub(t)
	containers := &fakeLifecycle{}
	disabler := &fakeDisabler{}
	fileOps := &fakeArchivingFileOps{archiveErr: errors.New("disk full")}
	handler := NewAppDeleteHandler(stub, containers, disabler, fileOps)
	err := handler.Handle(context.Background(), runtimeJob(domain.JobTypeAppDelete, testAppID))
	if err == nil || !strings.Contains(err.Error(), "归档应用工作目录失败") {
		t.Fatalf("err=%v", err)
	}
	if stub.softDeleted {
		t.Fatal("归档失败时不应软删 apps 行")
	}
}

func TestAppDeleteHandler_SkipsContainerStepWithoutID(t *testing.T) {
	stub := runtimeStub(t)
	stub.app.ContainerID = pgtype.Text{}
	containers := &fakeLifecycle{}
	disabler := &fakeDisabler{}
	handler := NewAppDeleteHandler(stub, containers, disabler, nil)
	if err := handler.Handle(context.Background(), runtimeJob(domain.JobTypeAppDelete, testAppID)); err != nil {
		t.Fatalf("Handle err = %v", err)
	}
	if containers.stopCalls != 0 || containers.removeCalls != 0 {
		t.Fatal("没 container_id 时不应调 docker")
	}
	if !stub.softDeleted {
		t.Fatal("仍应软删 app")
	}
}

func TestAppDeleteHandler_SkipsNewAPIWhenNoKey(t *testing.T) {
	stub := runtimeStub(t)
	stub.app.NewapiKeyID = pgtype.Text{}
	disabler := &fakeDisabler{}
	handler := NewAppDeleteHandler(stub, &fakeLifecycle{}, disabler, nil)
	if err := handler.Handle(context.Background(), runtimeJob(domain.JobTypeAppDelete, testAppID)); err != nil {
		t.Fatalf("Handle err = %v", err)
	}
	if disabler.id != 0 {
		t.Fatal("没 newapi_key_id 时不应禁用 token")
	}
}

func TestAppDeleteHandler_PropagatesNewAPIError(t *testing.T) {
	stub := runtimeStub(t)
	disabler := &fakeDisabler{err: errors.New("upstream")}
	handler := NewAppDeleteHandler(stub, &fakeLifecycle{}, disabler, nil)
	err := handler.Handle(context.Background(), runtimeJob(domain.JobTypeAppDelete, testAppID))
	if err == nil {
		t.Fatal("禁用 token 失败应冒泡")
	}
	if stub.softDeleted {
		t.Fatal("失败时不应软删")
	}
}

func TestAppDeleteHandler_AlreadyDeletedShortCircuits(t *testing.T) {
	stub := runtimeStub(t)
	stub.app.DeletedAt = pgtype.Timestamptz{Valid: true}
	handler := NewAppDeleteHandler(stub, &fakeLifecycle{}, &fakeDisabler{}, nil)
	if err := handler.Handle(context.Background(), runtimeJob(domain.JobTypeAppDelete, testAppID)); err != nil {
		t.Fatalf("Handle err = %v", err)
	}
	if stub.softDeleted {
		t.Fatal("已删除应当幂等返回，不再写库")
	}
}

func TestAppRuntimeHandlers_RejectMismatchedJobType(t *testing.T) {
	stub := runtimeStub(t)
	bad := runtimeJob("unknown", testAppID)
	handlers := []func(context.Context, sqlc.Job) error{
		NewAppStartContainerHandler(stub, &fakeLifecycle{}).Handle,
		NewAppStopContainerHandler(stub, &fakeLifecycle{}).Handle,
		NewAppRestartContainerHandler(stub, &fakeLifecycle{}).Handle,
		NewAppDeleteHandler(stub, &fakeLifecycle{}, &fakeDisabler{}, nil).Handle,
	}
	for _, h := range handlers {
		if err := h(context.Background(), bad); err == nil {
			t.Fatalf("应拒绝非匹配 job_type")
		}
	}
}

type runtimeOpStub struct {
	app           sqlc.App
	statusUpdates []string
	softDeleted   bool
}

func (s *runtimeOpStub) GetApp(_ context.Context, _ pgtype.UUID) (sqlc.App, error) { return s.app, nil }

func (s *runtimeOpStub) SetAppStatus(_ context.Context, arg sqlc.SetAppStatusParams) (sqlc.App, error) {
	s.statusUpdates = append(s.statusUpdates, arg.Status)
	s.app.Status = arg.Status
	return s.app, nil
}

func (s *runtimeOpStub) SoftDeleteApp(_ context.Context, _ pgtype.UUID) (sqlc.App, error) {
	s.softDeleted = true
	s.app.DeletedAt = pgtype.Timestamptz{Valid: true}
	return s.app, nil
}

type fakeLifecycle struct {
	startCalls   int
	stopCalls    int
	restartCalls int
	removeCalls  int
	startErr     error
	stopErr      error
	removeErr    error
}

func (f *fakeLifecycle) StartContainer(_ context.Context, _, _ string) error {
	f.startCalls++
	return f.startErr
}
func (f *fakeLifecycle) StopContainer(_ context.Context, _, _ string) error {
	f.stopCalls++
	return f.stopErr
}
func (f *fakeLifecycle) RestartContainer(_ context.Context, _, _ string) error {
	f.restartCalls++
	return nil
}
func (f *fakeLifecycle) RemoveContainer(_ context.Context, _, _ string) error {
	f.removeCalls++
	return f.removeErr
}

// fakeDisabler 同时实现 NewAPIClientFactory + APIKeyClient：UserScopedFor 直接返回自身，
// 把"工厂派生 user-scoped client"的两层抽象在测试里压平。CreateAPIKey / GetTokenFullKey
// 在 app_delete 流程里不会被调到，留空实现。
type fakeDisabler struct {
	id     int64
	status int
	err    error
}

func (f *fakeDisabler) UserScopedFor(_ context.Context, _ sqlc.App) (APIKeyClient, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f, nil
}

func (f *fakeDisabler) CreateAPIKey(_ context.Context, _ newapi.CreateAPIKeyInput) (newapi.APIKey, error) {
	return newapi.APIKey{}, nil
}

func (f *fakeDisabler) GetTokenFullKey(_ context.Context, _ int64) (string, error) {
	return "", nil
}

func (f *fakeDisabler) SetAPIKeyStatus(_ context.Context, id int64, status int) error {
	f.id = id
	f.status = status
	return f.err
}

type fakeFileOps struct {
	deletedAppID string
	err          error
}

func (f *fakeFileOps) DeleteAppPath(_ context.Context, _, appID string) error {
	f.deletedAppID = appID
	return f.err
}

// fakeArchivingFileOps 同时实现 AppDeleteFileOps + AppArchiver。用于断言
// app_delete handler 优先走 ArchiveApp（保留节点目录用于审计 / 误删恢复），
// 不再调 DeleteAppPath。
type fakeArchivingFileOps struct {
	archivedAppID string
	deletedAppID  string
	archiveErr    error
}

func (f *fakeArchivingFileOps) DeleteAppPath(_ context.Context, _, appID string) error {
	f.deletedAppID = appID
	return nil
}

func (f *fakeArchivingFileOps) ArchiveApp(_ context.Context, _, appID string) error {
	f.archivedAppID = appID
	return f.archiveErr
}
