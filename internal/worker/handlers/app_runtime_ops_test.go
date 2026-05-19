package handlers

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/stretchr/testify/require"
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

// TestAppStartContainerHandler_HappyPath 验证应用启动容器处理器成功路径的成功路径场景。
func TestAppStartContainerHandler_HappyPath(t *testing.T) {
	stub := runtimeStub(t)
	containers := &fakeLifecycle{}
	handler := NewAppStartContainerHandler(stub, containers)

	err := handler.Handle(context.Background(), runtimeJob(domain.JobTypeAppStartContainer, testAppID))
	require.NoError(t, err)
	require.Equal(t, 1, containers.startCalls)
	require.Equal(t, domain.AppStatusRunning, stub.statusUpdates[len(stub.statusUpdates)-1])
}

// TestAppStartContainerHandler_RejectsWithoutContainerID 验证应用启动容器处理器拒绝不使用容器ID的异常或拒绝路径场景。
func TestAppStartContainerHandler_RejectsWithoutContainerID(t *testing.T) {
	stub := runtimeStub(t)
	stub.app.ContainerID = pgtype.Text{}
	handler := NewAppStartContainerHandler(stub, &fakeLifecycle{})
	err := handler.Handle(context.Background(), runtimeJob(domain.JobTypeAppStartContainer, testAppID))
	require.Error(t, err)
}

// TestAppStartContainerHandler_PropagatesAdapterError 验证应用启动容器处理器透传适配器错误的错误映射或错误记录场景。
func TestAppStartContainerHandler_PropagatesAdapterError(t *testing.T) {
	stub := runtimeStub(t)
	containers := &fakeLifecycle{startErr: errors.New("docker boom")}
	handler := NewAppStartContainerHandler(stub, containers)
	err := handler.Handle(context.Background(), runtimeJob(domain.JobTypeAppStartContainer, testAppID))
	require.Error(t, err)
	require.Equal(t, 0, len(stub.statusUpdates))
}

// TestAppStopContainerHandler_HappyPath 验证应用停止容器处理器成功路径的成功路径场景。
func TestAppStopContainerHandler_HappyPath(t *testing.T) {
	stub := runtimeStub(t)
	containers := &fakeLifecycle{}
	handler := NewAppStopContainerHandler(stub, containers)
	err := handler.Handle(context.Background(), runtimeJob(domain.JobTypeAppStopContainer, testAppID))
	require.NoError(t, err)
	require.Equal(t, 1, containers.stopCalls)
	require.Equal(t, domain.AppStatusStopped, stub.statusUpdates[len(stub.statusUpdates)-1])
}

// TestAppStopContainerHandler_NoContainerStillUpdatesStatus 验证应用停止容器处理器无容器仍然Updates状态的预期行为场景。
func TestAppStopContainerHandler_NoContainerStillUpdatesStatus(t *testing.T) {
	stub := runtimeStub(t)
	stub.app.ContainerID = pgtype.Text{}
	containers := &fakeLifecycle{}
	handler := NewAppStopContainerHandler(stub, containers)
	err := handler.Handle(context.Background(), runtimeJob(domain.JobTypeAppStopContainer, testAppID))
	require.NoError(t, err)
	require.Equal(t, 0, containers.stopCalls)
	require.Equal(t, domain.AppStatusStopped, stub.statusUpdates[len(stub.statusUpdates)-1])
}

// TestAppRestartContainerHandler_HappyPath 验证应用重启容器处理器成功路径的成功路径场景。
func TestAppRestartContainerHandler_HappyPath(t *testing.T) {
	stub := runtimeStub(t)
	containers := &fakeLifecycle{}
	handler := NewAppRestartContainerHandler(stub, containers)
	err := handler.Handle(context.Background(), runtimeJob(domain.JobTypeAppRestartContainer, testAppID))
	require.NoError(t, err)
	require.Equal(t, 1, containers.restartCalls)
	require.Equal(t, domain.AppStatusRunning, stub.statusUpdates[len(stub.statusUpdates)-1])
}

// fakeSessionCleaner 是 SessionCleaner 测试桩。
type fakeSessionCleaner struct {
	calls       int
	lastNodeID  string
	lastAppID   string
	returnError error
}

func (f *fakeSessionCleaner) ClearAppSessions(_ context.Context, nodeID, appID string) error {
	f.calls++
	f.lastNodeID = nodeID
	f.lastAppID = appID
	return f.returnError
}

// TestAppRestartContainerHandler_SessionCleanerCalledBeforeRestart 验证 SetSessionCleaner
// 注入后,Handle 走 stop → clear sessions → start 三步,不走原子 RestartContainer。
// state.db (SQLite) 持有文件锁,运行中删会损坏数据库,所以必须先停容器再清。
// Hermes 在 session 启动时把 system_prompt 冻结进 SQLite,清掉旧 session
// 才能让最新 SOUL.md(含改后的 model / persona / 知识库)进入新对话。
func TestAppRestartContainerHandler_SessionCleanerCalledBeforeRestart(t *testing.T) {
	stub := runtimeStub(t)
	containers := &fakeLifecycle{}
	cleaner := &fakeSessionCleaner{}
	handler := NewAppRestartContainerHandler(stub, containers)
	handler.SetSessionCleaner(cleaner)
	err := handler.Handle(context.Background(), runtimeJob(domain.JobTypeAppRestartContainer, testAppID))
	require.NoError(t, err)
	// stop + clear + start 各调一次,RestartContainer 不被调用。
	require.Equal(t, 1, containers.stopCalls)
	require.Equal(t, 1, cleaner.calls)
	require.Equal(t, 1, containers.startCalls)
	require.Equal(t, 0, containers.restartCalls)
	// cleaner 接收正确的 appID(透传校验)。
	require.Equal(t, testAppID, cleaner.lastAppID)
}

// TestAppRestartContainerHandler_SessionCleanerErrorAbortsRestart 验证清 session 失败时
// 容器不会被 start,job 返回错误让 worker 重试——清 session 是配置变更进入对话的必经路径,
// 失败时让重试比"用旧 session 跑起来"更安全。
func TestAppRestartContainerHandler_SessionCleanerErrorAbortsRestart(t *testing.T) {
	stub := runtimeStub(t)
	containers := &fakeLifecycle{}
	cleaner := &fakeSessionCleaner{returnError: fmt.Errorf("agent 清 session 失败")}
	handler := NewAppRestartContainerHandler(stub, containers)
	handler.SetSessionCleaner(cleaner)
	err := handler.Handle(context.Background(), runtimeJob(domain.JobTypeAppRestartContainer, testAppID))
	require.Error(t, err)
	// 容器虽然被 stop 了(SQLite 必须容器停才能清),但 cleaner 失败后不再 start——
	// 让用户感知 restart 失败,而非用旧 session 静默成功启动。
	require.Equal(t, 1, containers.stopCalls)
	require.Equal(t, 0, containers.startCalls)
	require.Equal(t, 0, containers.restartCalls)
}

// TestAppRestartContainerHandler_NoSessionCleanerFallsBackToAtomicRestart 验证 SessionCleaner
// 未注入(旧装配 / 测试装配)时,Handle 退回到原 docker restart 行为,保持向后兼容。
func TestAppRestartContainerHandler_NoSessionCleanerFallsBackToAtomicRestart(t *testing.T) {
	stub := runtimeStub(t)
	containers := &fakeLifecycle{}
	handler := NewAppRestartContainerHandler(stub, containers)
	// 不调 SetSessionCleaner。
	err := handler.Handle(context.Background(), runtimeJob(domain.JobTypeAppRestartContainer, testAppID))
	require.NoError(t, err)
	require.Equal(t, 1, containers.restartCalls)
	require.Equal(t, 0, containers.stopCalls)
	require.Equal(t, 0, containers.startCalls)
}

// TestAppDeleteHandler_HappyPath 验证应用删除处理器成功路径的成功路径场景。
func TestAppDeleteHandler_HappyPath(t *testing.T) {
	stub := runtimeStub(t)
	containers := &fakeLifecycle{}
	disabler := &fakeDisabler{}
	fileOps := &fakeFileOps{}
	handler := NewAppDeleteHandler(stub, containers, disabler, fileOps)
	err := handler.Handle(context.Background(), runtimeJob(domain.JobTypeAppDelete, testAppID))
	require.NoError(t, err)
	if containers.stopCalls != 1 || containers.removeCalls != 1 {
		t.Fatalf("stop=%d remove=%d, want 1/1", containers.stopCalls, containers.removeCalls)
	}
	if disabler.id != 42 || disabler.status != 2 {
		t.Fatalf("disabler 调用 = (%d,%d), want (42,2)", disabler.id, disabler.status)
	}
	require.Equal(t, testAppID, fileOps.deletedAppID)
	require.True(t, stub.softDeleted)
}

// TestAppDeleteHandler_PrefersArchiveOverDelete 验证应用删除处理器Prefers归档覆盖删除的预期行为场景。
func TestAppDeleteHandler_PrefersArchiveOverDelete(t *testing.T) {
	// Sprint 2：fileOps 实现 AppArchiver 时应优先归档而非直接删除，保留节点目录。
	stub := runtimeStub(t)
	containers := &fakeLifecycle{}
	disabler := &fakeDisabler{}
	fileOps := &fakeArchivingFileOps{}
	handler := NewAppDeleteHandler(stub, containers, disabler, fileOps)
	err := handler.Handle(context.Background(), runtimeJob(domain.JobTypeAppDelete, testAppID))
	require.NoError(t, err)
	require.Equal(t, testAppID, fileOps.archivedAppID)
	require.Equal(t, "", fileOps.deletedAppID)
}

// TestAppDeleteHandler_PropagatesArchiveError 验证应用删除处理器透传归档错误的错误映射或错误记录场景。
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
	require.False(t, stub.softDeleted)
}

// TestAppDeleteHandler_SkipsContainerStepWithoutID 验证应用删除处理器跳过容器Step不使用ID的特殊分支或幂等场景。
func TestAppDeleteHandler_SkipsContainerStepWithoutID(t *testing.T) {
	stub := runtimeStub(t)
	stub.app.ContainerID = pgtype.Text{}
	containers := &fakeLifecycle{}
	disabler := &fakeDisabler{}
	handler := NewAppDeleteHandler(stub, containers, disabler, nil)
	err := handler.Handle(context.Background(), runtimeJob(domain.JobTypeAppDelete, testAppID))
	require.NoError(t, err)
	if containers.stopCalls != 0 || containers.removeCalls != 0 {
		t.Fatal("没 container_id 时不应调 docker")
	}
	require.True(t, stub.softDeleted)
}

// TestAppDeleteHandler_SkipsNewAPIWhenNoKey 验证应用删除处理器跳过new-api当无Key的特殊分支或幂等场景。
func TestAppDeleteHandler_SkipsNewAPIWhenNoKey(t *testing.T) {
	stub := runtimeStub(t)
	stub.app.NewapiKeyID = pgtype.Text{}
	disabler := &fakeDisabler{}
	handler := NewAppDeleteHandler(stub, &fakeLifecycle{}, disabler, nil)
	err := handler.Handle(context.Background(), runtimeJob(domain.JobTypeAppDelete, testAppID))
	require.NoError(t, err)
	require.Equal(t, int64(0), disabler.id)
}

// TestAppDeleteHandler_PropagatesNewAPIError 验证应用删除处理器透传new-api错误的错误映射或错误记录场景。
func TestAppDeleteHandler_PropagatesNewAPIError(t *testing.T) {
	stub := runtimeStub(t)
	disabler := &fakeDisabler{err: errors.New("upstream")}
	handler := NewAppDeleteHandler(stub, &fakeLifecycle{}, disabler, nil)
	err := handler.Handle(context.Background(), runtimeJob(domain.JobTypeAppDelete, testAppID))
	require.Error(t, err)
	require.False(t, stub.softDeleted)
}

// TestAppDeleteHandler_AlreadyDeletedShortCircuits 验证应用删除处理器已经删除态过短Circuits的边界条件场景。
func TestAppDeleteHandler_AlreadyDeletedShortCircuits(t *testing.T) {
	stub := runtimeStub(t)
	stub.app.DeletedAt = pgtype.Timestamptz{Valid: true}
	handler := NewAppDeleteHandler(stub, &fakeLifecycle{}, &fakeDisabler{}, nil)
	err := handler.Handle(context.Background(), runtimeJob(domain.JobTypeAppDelete, testAppID))
	require.NoError(t, err)
	require.False(t, stub.softDeleted)
}

// TestAppRuntimeHandlers_RejectMismatchedJobType 验证应用运行时HandlersReject不匹配任务类型的预期行为场景。
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
		err := h(context.Background(), bad)
		require.Error(t, err)
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

// SetAppModelSynced 实现 AppRuntimeStore 接口；重启完成后标记模型已同步。
func (s *runtimeOpStub) SetAppModelSynced(_ context.Context, _ pgtype.UUID) (sqlc.App, error) {
	s.app.ModelSynced = true
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
