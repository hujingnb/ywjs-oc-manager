package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/stretchr/testify/assert"
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

// fakeInputRefresher 是 AppInputRefresher 的测试桩。
// orderTracker 用来断言「refresher 在 stop 之前被调用」: stop / refresh 都把
// 自己的 sequence 追加到 tracker, 由测试比较顺序。
type fakeInputRefresher struct {
	calls       int
	lastNodeID  string
	lastAppID   string
	returnError error
	// returnResult 是 RefreshAppInput 成功时返回的版本信息，供测试断言 SetAppAppliedVersion 入参。
	returnResult AppInputRefreshResult
	// orderTracker 由测试注入, refresh 时 append "refresh", 与 fakeLifecycle 共享同一 slice
	// 即可断言事件先后。
	orderTracker *[]string
}

func (f *fakeInputRefresher) RefreshAppInput(_ context.Context, nodeID string, app sqlc.App) (AppInputRefreshResult, error) {
	f.calls++
	f.lastNodeID = nodeID
	f.lastAppID = uuidToString(app.ID)
	if f.orderTracker != nil {
		*f.orderTracker = append(*f.orderTracker, "refresh")
	}
	return f.returnResult, f.returnError
}

// orderedLifecycle 在 fakeLifecycle 基础上把每次 stop / start / restart 调用
// 也写到 orderTracker 上, 便于测试断言"refresh 在 stop 前发生"。
type orderedLifecycle struct {
	fakeLifecycle
	orderTracker *[]string
}

func (f *orderedLifecycle) StopContainer(ctx context.Context, nodeID, containerID string) error {
	if f.orderTracker != nil {
		*f.orderTracker = append(*f.orderTracker, "stop")
	}
	return f.fakeLifecycle.StopContainer(ctx, nodeID, containerID)
}

func (f *orderedLifecycle) StartContainer(ctx context.Context, nodeID, containerID string) error {
	if f.orderTracker != nil {
		*f.orderTracker = append(*f.orderTracker, "start")
	}
	return f.fakeLifecycle.StartContainer(ctx, nodeID, containerID)
}

func (f *orderedLifecycle) RestartContainer(ctx context.Context, nodeID, containerID string) error {
	if f.orderTracker != nil {
		*f.orderTracker = append(*f.orderTracker, "restart")
	}
	return f.fakeLifecycle.RestartContainer(ctx, nodeID, containerID)
}

// TestAppRestartContainerHandler_RefreshesInputBeforeRestart 验证注入 AppInputRefresher
// 后, Handle 会在容器实际 stop 之前调用 refresher.RefreshAppInput。
// 这是 hermes 镜像自包含 (oc-entrypoint 启动时根据 input/ 重渲染) 流程下
// 「改 model / 改 prompt / 改 persona 后 restart 真正生效」的关键: input 必须
// 先被刷新到节点, 后续 stop → start 才能让容器读到最新数据。
func TestAppRestartContainerHandler_RefreshesInputBeforeRestart(t *testing.T) {
	stub := runtimeStub(t)
	order := make([]string, 0, 4)
	containers := &orderedLifecycle{orderTracker: &order}
	cleaner := &fakeSessionCleaner{}
	refresher := &fakeInputRefresher{orderTracker: &order}
	handler := NewAppRestartContainerHandler(stub, containers)
	handler.SetInputRefresher(refresher)
	handler.SetSessionCleaner(cleaner)
	err := handler.Handle(context.Background(), runtimeJob(domain.JobTypeAppRestartContainer, testAppID))
	require.NoError(t, err)
	// refresher 被调用一次, 透传到正确的 appID。
	require.Equal(t, 1, refresher.calls)
	require.Equal(t, testAppID, refresher.lastAppID)
	// 顺序必须是 refresh → stop → start (cleaner 调用插在 stop 与 start 之间);
	// refresh 不能出现在 stop 之后, 否则 oc-entrypoint 仍会读到旧 manifest。
	require.Equal(t, []string{"refresh", "stop", "start"}, order)
}

// TestAppRestartContainerHandler_RefresherErrorAbortsRestart 验证 refresher 失败时
// 容器不会被 stop, 错误冒泡让 worker 重试。
// 不允许出现"先 stop 再失败"的中间态: 那会让容器陷入 stopped 状态而 input 未刷新,
// 用户感知不到, 后续就算手动 start 也是用旧配置。
func TestAppRestartContainerHandler_RefresherErrorAbortsRestart(t *testing.T) {
	stub := runtimeStub(t)
	containers := &fakeLifecycle{}
	refresher := &fakeInputRefresher{returnError: fmt.Errorf("agent 写 manifest 失败")}
	handler := NewAppRestartContainerHandler(stub, containers)
	handler.SetInputRefresher(refresher)
	err := handler.Handle(context.Background(), runtimeJob(domain.JobTypeAppRestartContainer, testAppID))
	require.Error(t, err)
	// refresher 调用过一次, 但任何容器生命周期方法都不应触发。
	require.Equal(t, 1, refresher.calls)
	require.Equal(t, 0, containers.stopCalls)
	require.Equal(t, 0, containers.startCalls)
	require.Equal(t, 0, containers.restartCalls)
}

// TestAppRestartContainerHandler_NoInputRefresherSkipsRefresh 验证未注入 InputRefresher
// (测试装配 / 旧 wiring) 时, Handle 跳过 input 刷新但 stop/start 仍能正常执行,
// 保持与原 restart 链路的向后兼容(不影响那些只测试容器生命周期的测试装配)。
func TestAppRestartContainerHandler_NoInputRefresherSkipsRefresh(t *testing.T) {
	stub := runtimeStub(t)
	containers := &fakeLifecycle{}
	handler := NewAppRestartContainerHandler(stub, containers)
	// 注入 session cleaner 走 stop/start 路径, 不注入 input refresher。
	handler.SetSessionCleaner(&fakeSessionCleaner{})
	err := handler.Handle(context.Background(), runtimeJob(domain.JobTypeAppRestartContainer, testAppID))
	require.NoError(t, err)
	// stop/start 仍能照常调用, 不应因为 refresher 缺失而失败。
	require.Equal(t, 1, containers.stopCalls)
	require.Equal(t, 1, containers.startCalls)
}

// TestAppRestartContainerHandler_RecordsAppliedVersionAfterRefresh 验证注入 inputRefresher
// 后，Handle 在成功重启完成后调用 SetAppAppliedVersion，把 refresher 返回的
// VersionRevision / ImageRef 写入 DB，供前端 version_synced 检测使用。
func TestAppRestartContainerHandler_RecordsAppliedVersionAfterRefresh(t *testing.T) {
	stub := runtimeStub(t)
	// 容器当前镜像与 refresher 返回镜像同值：本用例验证镜像未变时的常规重启路径，
	// 必须让 RuntimeImageRef 与 refresher.ImageRef 一致，避免触发镜像变更重建分支。
	stub.app.RuntimeImageRef = "hermes-v2:sha256-abc"
	containers := &fakeLifecycle{}
	// refresher 返回版本修订=5，镜像 ref="hermes-v2:sha256-abc"。
	refresher := &fakeInputRefresher{
		returnResult: AppInputRefreshResult{
			VersionRevision: 5,
			ImageRef:        "hermes-v2:sha256-abc",
		},
	}
	handler := NewAppRestartContainerHandler(stub, containers)
	handler.SetInputRefresher(refresher)

	err := handler.Handle(context.Background(), runtimeJob(domain.JobTypeAppRestartContainer, testAppID))
	require.NoError(t, err)
	// refresher 被调用一次。
	require.Equal(t, 1, refresher.calls)
	// SetAppAppliedVersion 必须被调用，入参与 refresher 返回值一致。
	require.True(t, stub.appliedVersionSet, "重启后应调用 SetAppAppliedVersion 记录已应用版本")
	assert.Equal(t, mustUUIDForTest(t, testAppID), stub.lastAppliedVersion.ID)
	require.Equal(t, int32(5), stub.lastAppliedVersion.AppliedVersionRevision)
	require.Equal(t, "hermes-v2:sha256-abc", stub.lastAppliedVersion.AppliedImageRef)
}

// TestAppRestartContainerHandler_NilRefresherSkipsAppliedVersion 验证 inputRefresher 为 nil
// （测试装配）时，Handle 完成重启但不调用 SetAppAppliedVersion——未刷新版本数据，
// 不应声称 applied，避免前端 version_synced 误置位。
func TestAppRestartContainerHandler_NilRefresherSkipsAppliedVersion(t *testing.T) {
	stub := runtimeStub(t)
	containers := &fakeLifecycle{}
	handler := NewAppRestartContainerHandler(stub, containers)
	// 不注入 inputRefresher，走 nil 路径。

	err := handler.Handle(context.Background(), runtimeJob(domain.JobTypeAppRestartContainer, testAppID))
	require.NoError(t, err)
	// SetAppAppliedVersion 不应被调用：没有刷新版本数据，无法确认 applied。
	require.False(t, stub.appliedVersionSet, "inputRefresher 为 nil 时不应调用 SetAppAppliedVersion")
}

// TestAppRestartContainerHandler_ImageChangeRecreatesViaInitJob 验证 restart 检测到
// 绑定版本解析镜像与 apps.runtime_image_ref 不一致时进入重建分支：
// stop + remove 旧容器、清空 container_id、置 status=pulling_runtime_image、入队
// app_initialize job 复用初始化 4 阶段重拉新镜像并重建容器，不再走原 restart 三步，
// 也不调 SetAppAppliedVersion（由 init handler 负责）。
func TestAppRestartContainerHandler_ImageChangeRecreatesViaInitJob(t *testing.T) {
	stub := runtimeStub(t)
	// 容器当前镜像为旧 ref，模拟绑定版本镜像已升级。
	stub.app.RuntimeImageRef = "hermes-v1:old"
	containers := &fakeLifecycle{}
	cleaner := &fakeSessionCleaner{}
	// refresher 返回新镜像 ref，触发镜像变更重建分支。
	refresher := &fakeInputRefresher{
		returnResult: AppInputRefreshResult{VersionRevision: 7, ImageRef: "hermes-v2:new"},
	}
	notifier := &fakeRestartNotifier{}
	handler := NewAppRestartContainerHandler(stub, containers)
	handler.SetSessionCleaner(cleaner)
	handler.SetInputRefresher(refresher)
	handler.SetJobNotifier(notifier)

	err := handler.Handle(context.Background(), runtimeJob(domain.JobTypeAppRestartContainer, testAppID))
	require.NoError(t, err)
	// 旧容器 stop + remove 各一次，原 restart 路径的 start 不应被调用。
	require.Equal(t, 1, containers.stopCalls)
	require.Equal(t, 1, containers.removeCalls)
	require.Equal(t, 0, containers.startCalls)
	require.Equal(t, 0, containers.restartCalls)
	// container_id 已被清空，便于 app_initialize 重新创建容器。
	require.True(t, stub.containerCleared)
	// 状态被推到 pulling_runtime_image，交还初始化 4 阶段。
	require.Contains(t, stub.statusUpdates, domain.AppStatusPullingRuntimeImage)
	// 恰好入队一条 app_initialize job。
	require.Len(t, stub.createdJobs, 1)
	assert.Equal(t, domain.JobTypeAppInitialize, stub.createdJobs[0].Type)
	// payload 中 app_id 必须指向当前应用。
	var payload map[string]any
	require.NoError(t, json.Unmarshal(stub.createdJobs[0].PayloadJson, &payload))
	assert.Equal(t, testAppID, payload["app_id"])
	// notifier 收到 CreateJob 桩返回的固定 job ID。
	require.Equal(t, 1, notifier.calls)
	assert.Equal(t, testRestartInitJobID, notifier.enqueuedJobID)
	// 镜像变更分支不应记录 applied 版本，交由 init handler 在初始化完成时写入。
	require.False(t, stub.appliedVersionSet, "镜像变更重建分支不应调用 SetAppAppliedVersion")
}

// TestAppRestartContainerHandler_ImageUnchangedKeepsRestart 验证 restart 解析镜像与
// apps.runtime_image_ref 一致时保持原 stop → clear sessions → start 行为，
// 不重建容器、不入队 app_initialize，并正常记录 applied 版本。
func TestAppRestartContainerHandler_ImageUnchangedKeepsRestart(t *testing.T) {
	stub := runtimeStub(t)
	// 容器当前镜像与 refresher 返回镜像同值，镜像未变。
	stub.app.RuntimeImageRef = "hermes-v1:same"
	containers := &fakeLifecycle{}
	cleaner := &fakeSessionCleaner{}
	refresher := &fakeInputRefresher{
		returnResult: AppInputRefreshResult{VersionRevision: 3, ImageRef: "hermes-v1:same"},
	}
	notifier := &fakeRestartNotifier{}
	handler := NewAppRestartContainerHandler(stub, containers)
	handler.SetSessionCleaner(cleaner)
	handler.SetInputRefresher(refresher)
	handler.SetJobNotifier(notifier)

	err := handler.Handle(context.Background(), runtimeJob(domain.JobTypeAppRestartContainer, testAppID))
	require.NoError(t, err)
	// 走原 stop → clear sessions → start 三步，不 remove 容器、不入队 job。
	require.Equal(t, 1, containers.stopCalls)
	require.Equal(t, 1, containers.startCalls)
	require.Equal(t, 0, containers.removeCalls)
	require.Len(t, stub.createdJobs, 0)
	require.Equal(t, 0, notifier.calls)
	// 镜像未变，原路径应正常记录 applied 版本。
	require.True(t, stub.appliedVersionSet, "镜像未变时仍应调用 SetAppAppliedVersion")
	assert.Equal(t, "hermes-v1:same", stub.lastAppliedVersion.AppliedImageRef)
}

// TestAppRestartContainerHandler_ImageChangeRetryAfterContainerCleared 验证镜像变更
// 重建分支被 worker 重试时的幂等性：上一次尝试已清空 container_id，重入时跳过
// stop/remove，仍重新建 app_initialize job 并即时入队。
func TestAppRestartContainerHandler_ImageChangeRetryAfterContainerCleared(t *testing.T) {
	stub := runtimeStub(t)
	stub.app.RuntimeImageRef = "hermes-v1:old"
	// 模拟重试：container_id 已被上一次尝试清空。
	stub.app.ContainerID = pgtype.Text{}
	containers := &fakeLifecycle{}
	refresher := &fakeInputRefresher{
		returnResult: AppInputRefreshResult{VersionRevision: 7, ImageRef: "hermes-v2:new"},
	}
	notifier := &fakeRestartNotifier{}
	handler := NewAppRestartContainerHandler(stub, containers)
	handler.SetInputRefresher(refresher)
	handler.SetJobNotifier(notifier)

	err := handler.Handle(context.Background(), runtimeJob(domain.JobTypeAppRestartContainer, testAppID))
	require.NoError(t, err)
	// container_id 已空，不应再 stop/remove 容器。
	require.Equal(t, 0, containers.stopCalls)
	require.Equal(t, 0, containers.removeCalls)
	// 仍应重新建恰好一条 app_initialize job 并入队。
	require.Len(t, stub.createdJobs, 1)
	assert.Equal(t, domain.JobTypeAppInitialize, stub.createdJobs[0].Type)
	require.Equal(t, 1, notifier.calls)
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
	// appliedVersionSet 标记 SetAppAppliedVersion 是否被调用，供重启链路断言使用。
	appliedVersionSet bool
	// lastAppliedVersion 记录最近一次 SetAppAppliedVersion 的入参，供断言 applied 字段。
	lastAppliedVersion sqlc.SetAppAppliedVersionParams
	// containerCleared 标记 SetAppContainer 是否被调用（镜像变更重建时清空 container_id）。
	containerCleared bool
	// createdJobs 记录所有 CreateJob 入参，供断言 restart 镜像变更后入队 app_initialize。
	createdJobs []sqlc.CreateJobParams
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

// SetAppAppliedVersion 实现 AppRuntimeStore 接口；记录已应用的版本修订与镜像 ref。
func (s *runtimeOpStub) SetAppAppliedVersion(_ context.Context, arg sqlc.SetAppAppliedVersionParams) (sqlc.App, error) {
	s.appliedVersionSet = true
	s.lastAppliedVersion = arg
	s.app.AppliedVersionRevision = arg.AppliedVersionRevision
	s.app.AppliedImageRef = arg.AppliedImageRef
	return s.app, nil
}

// SetAppContainer 实现 AppRuntimeStore 接口；镜像变更重建时清空 container_id / container_name。
func (s *runtimeOpStub) SetAppContainer(_ context.Context, arg sqlc.SetAppContainerParams) (sqlc.App, error) {
	s.containerCleared = true
	s.app.ContainerID = arg.ContainerID
	s.app.ContainerName = arg.ContainerName
	return s.app, nil
}

// CreateJob 实现 AppRuntimeStore 接口；记录入参并返回带固定 ID 的 job，供断言入队。
func (s *runtimeOpStub) CreateJob(_ context.Context, arg sqlc.CreateJobParams) (sqlc.Job, error) {
	s.createdJobs = append(s.createdJobs, arg)
	var id pgtype.UUID
	// 忽略 Scan 错误：testRestartInitJobID 是固定合法 UUID 字面量。
	_ = id.Scan(testRestartInitJobID)
	return sqlc.Job{ID: id, Type: arg.Type}, nil
}

// testRestartInitJobID 是 CreateJob 桩返回的固定 job ID，供 notifier 入队断言。
const testRestartInitJobID = "00000000-0000-0000-0000-0000000a0b01"

// fakeRestartNotifier 是 RestartJobNotifier 测试桩，记录被即时推送的 jobID。
type fakeRestartNotifier struct {
	enqueuedJobID string
	calls         int
}

func (f *fakeRestartNotifier) Enqueue(_ context.Context, jobID string) error {
	f.calls++
	f.enqueuedJobID = jobID
	return nil
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
