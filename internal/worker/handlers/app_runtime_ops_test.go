package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/guregu/null/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"oc-manager/internal/domain"
	"oc-manager/internal/integrations/newapi"
	"oc-manager/internal/integrations/storage"
	"oc-manager/internal/store/sqlc"
)

const testRuntimeNodeID = "00000000-0000-0000-0000-000000000d01"

// runtimeStub 构建测试用 runtimeOpStub；ID 字段迁移为 string（MySQL uuid）。
func runtimeStub(t *testing.T) *runtimeOpStub {
	t.Helper()
	return &runtimeOpStub{
		app: sqlc.App{
			ID:            testAppID,
			OrgID:         testOrgID,
			OwnerUserID:   testUsrID,
			RuntimeNodeID: null.StringFrom(testRuntimeNodeID), // RuntimeNodeID nullable（spec-A2a）
			Status:        domain.AppStatusRunning,
			ContainerID:   null.StringFrom("ctr-existing"), // k8s 路径用 ContainerID 判断 Deployment 是否已建立
			ContainerName: null.StringFrom("ocm-app"),
			NewapiKeyID:   null.StringFrom("42"),
		},
	}
}

func runtimeJob(jobType, appID string) sqlc.Job {
	return sqlc.Job{Type: jobType, PayloadJson: []byte(`{"app_id":"` + appID + `"}`)}
}

// ─────────────────────────────────────────────
// AppStartContainerHandler 单测
// ─────────────────────────────────────────────

// TestAppStartContainerHandler_HappyPath 验证 Scale(1) 被调用且状态更新为 running 的成功路径。
func TestAppStartContainerHandler_HappyPath(t *testing.T) {
	stub := runtimeStub(t)
	orch := &fakeAppOrchestrator{}
	handler := NewAppStartContainerHandler(stub, orch)

	err := handler.Handle(context.Background(), runtimeJob(domain.JobTypeAppStartContainer, testAppID))
	require.NoError(t, err)
	// Scale(1) 必须被调用一次。
	require.Equal(t, 1, orch.scaleCalls)
	require.Equal(t, int32(1), orch.lastScaleReplicas)
	// 状态更新为 running。
	require.Equal(t, domain.AppStatusRunning, stub.statusUpdates[len(stub.statusUpdates)-1])
}

// TestAppStartContainerHandler_RejectsWithoutContainerID 验证未完成 init 时（ContainerID 空）拒绝启动。
// k8s Deployment 未建立时不能发 Scale，避免产生孤儿资源。
func TestAppStartContainerHandler_RejectsWithoutContainerID(t *testing.T) {
	stub := runtimeStub(t)
	stub.app.ContainerID = null.String{}
	handler := NewAppStartContainerHandler(stub, &fakeAppOrchestrator{})
	err := handler.Handle(context.Background(), runtimeJob(domain.JobTypeAppStartContainer, testAppID))
	require.Error(t, err)
}

// TestAppStartContainerHandler_PropagatesOrchestratorError 验证 Scale 失败时错误冒泡、状态不更新。
func TestAppStartContainerHandler_PropagatesOrchestratorError(t *testing.T) {
	stub := runtimeStub(t)
	orch := &fakeAppOrchestrator{scaleErr: errors.New("k8s boom")}
	handler := NewAppStartContainerHandler(stub, orch)
	err := handler.Handle(context.Background(), runtimeJob(domain.JobTypeAppStartContainer, testAppID))
	require.Error(t, err)
	// Scale 失败时不应更新状态。
	require.Equal(t, 0, len(stub.statusUpdates))
}

// ─────────────────────────────────────────────
// AppStopContainerHandler 单测
// ─────────────────────────────────────────────

// TestAppStopContainerHandler_HappyPath 验证 Scale(0) 被调用且状态更新为 stopped 的成功路径。
func TestAppStopContainerHandler_HappyPath(t *testing.T) {
	stub := runtimeStub(t)
	orch := &fakeAppOrchestrator{}
	handler := NewAppStopContainerHandler(stub, orch)
	err := handler.Handle(context.Background(), runtimeJob(domain.JobTypeAppStopContainer, testAppID))
	require.NoError(t, err)
	// Scale(0) 必须被调用一次。
	require.Equal(t, 1, orch.scaleCalls)
	require.Equal(t, int32(0), orch.lastScaleReplicas)
	require.Equal(t, domain.AppStatusStopped, stub.statusUpdates[len(stub.statusUpdates)-1])
}

// TestAppStopContainerHandler_NoContainerStillUpdatesStatus 验证 ContainerID 为空时跳过 Scale
// 直接推 stopped 状态（Deployment 未建立时等价于已停止）。
func TestAppStopContainerHandler_NoContainerStillUpdatesStatus(t *testing.T) {
	stub := runtimeStub(t)
	stub.app.ContainerID = null.String{}
	orch := &fakeAppOrchestrator{}
	handler := NewAppStopContainerHandler(stub, orch)
	err := handler.Handle(context.Background(), runtimeJob(domain.JobTypeAppStopContainer, testAppID))
	require.NoError(t, err)
	// Deployment 未建立时不调 Scale，直接推状态收敛状态机。
	require.Equal(t, 0, orch.scaleCalls)
	require.Equal(t, domain.AppStatusStopped, stub.statusUpdates[len(stub.statusUpdates)-1])
}

// ─────────────────────────────────────────────
// AppRestartContainerHandler 单测
// ─────────────────────────────────────────────

// TestAppRestartContainerHandler_ImageUnchanged_DeletesSessionsThenScales 验证镜像不变时：
// 删 S3 sessions + state.db → Scale(0) → Scale(1) → status=running → SetAppAppliedVersion。
// hermes 重新启动后从 bootstrap 获取最新配置，sessions 被清除后 snapshot 最新 SOUL.md。
func TestAppRestartContainerHandler_ImageUnchanged_DeletesSessionsThenScales(t *testing.T) {
	stub := runtimeStub(t)
	stub.app.RuntimeImageRef = "hermes-v1:same"
	orch := &fakeAppOrchestrator{}
	objects := &fakeObjectStore{}
	// refresher 返回与当前一致的镜像，触发镜像不变路径。
	refresher := &fakeInputRefresher{
		returnResult: AppInputRefreshResult{VersionRevision: 3, ImageRef: "hermes-v1:same"},
	}
	handler := NewAppRestartContainerHandler(stub, orch, objects)
	handler.SetInputRefresher(refresher)

	err := handler.Handle(context.Background(), runtimeJob(domain.JobTypeAppRestartContainer, testAppID))
	require.NoError(t, err)
	// S3 sessions 和 state.db 被删除。
	require.True(t, objects.deletedSessionsPrefix, "重启时必须清除 S3 sessions")
	require.True(t, objects.deletedStateDB, "重启时必须清除 S3 state.db")
	// Scale(0) 然后 Scale(1)：重建 pod。
	require.Equal(t, 2, orch.scaleCalls)
	require.Equal(t, int32(0), orch.scaleHistory[0])
	require.Equal(t, int32(1), orch.scaleHistory[1])
	// 状态更新为 running。
	require.Equal(t, domain.AppStatusRunning, stub.statusUpdates[len(stub.statusUpdates)-1])
	// SetAppAppliedVersion 被调用，记录版本信息。
	require.True(t, stub.appliedVersionSet, "镜像不变重启后应调用 SetAppAppliedVersion")
	assert.Equal(t, "hermes-v1:same", stub.lastAppliedVersion.AppliedImageRef)
	assert.Equal(t, int32(3), stub.lastAppliedVersion.AppliedVersionRevision)
}

// TestAppRestartContainerHandler_ImageChanged_CallsUpdateImage 验证镜像变更时：
// UpdateImage → status=pulling_runtime_image → 入队 app_initialize job → 即时通知 notifier。
// k8s UpdateImage 触发 Deployment Recreate，不需要 manager 手动 Scale。
func TestAppRestartContainerHandler_ImageChanged_CallsUpdateImage(t *testing.T) {
	stub := runtimeStub(t)
	stub.app.RuntimeImageRef = "hermes-v1:old"
	orch := &fakeAppOrchestrator{}
	objects := &fakeObjectStore{}
	// refresher 返回新镜像 ref，触发镜像变更分支。
	refresher := &fakeInputRefresher{
		returnResult: AppInputRefreshResult{VersionRevision: 7, ImageRef: "hermes-v2:new"},
	}
	notifier := &fakeRestartNotifier{}
	handler := NewAppRestartContainerHandler(stub, orch, objects)
	handler.SetInputRefresher(refresher)
	handler.SetJobNotifier(notifier)

	err := handler.Handle(context.Background(), runtimeJob(domain.JobTypeAppRestartContainer, testAppID))
	require.NoError(t, err)
	// UpdateImage 被调用一次，传入新镜像 ref。
	require.Equal(t, 1, orch.updateImageCalls)
	require.Equal(t, "hermes-v2:new", orch.lastUpdateImage)
	// Scale 不应被调用（UpdateImage 触发 Recreate，k8s 自行处理）。
	require.Equal(t, 0, orch.scaleCalls)
	// S3 sessions 不应被清除（镜像变更路径不清 sessions）。
	require.False(t, objects.deletedSessionsPrefix, "镜像变更路径不清 S3 sessions")
	// 状态被推到 pulling_runtime_image。
	require.Contains(t, stub.statusUpdates, domain.AppStatusPullingRuntimeImage)
	// 恰好入队一条 app_initialize job。
	require.Len(t, stub.createdJobs, 1)
	assert.Equal(t, domain.JobTypeAppInitialize, stub.createdJobs[0].Type)
	var jobPayload map[string]any
	require.NoError(t, json.Unmarshal(stub.createdJobs[0].PayloadJson, &jobPayload))
	assert.Equal(t, testAppID, jobPayload["app_id"])
	// notifier 被即时通知。
	require.Equal(t, 1, notifier.calls)
	assert.Equal(t, stub.createdJobs[0].ID, notifier.enqueuedJobID)
	// 镜像变更分支不记录 applied 版本（交由 init handler 负责）。
	require.False(t, stub.appliedVersionSet, "镜像变更分支不应调用 SetAppAppliedVersion")
}

// TestAppRestartContainerHandler_NoRefresher_ScalesDirectly 验证 inputRefresher 为 nil 时
// 直接走 Scale(0)→Scale(1) 路径，跳过 S3 清除和 applied 版本记录（测试装配兼容）。
func TestAppRestartContainerHandler_NoRefresher_ScalesDirectly(t *testing.T) {
	stub := runtimeStub(t)
	orch := &fakeAppOrchestrator{}
	objects := &fakeObjectStore{}
	handler := NewAppRestartContainerHandler(stub, orch, objects)
	// 不注入 inputRefresher。

	err := handler.Handle(context.Background(), runtimeJob(domain.JobTypeAppRestartContainer, testAppID))
	require.NoError(t, err)
	// 无 refresher 时仍执行 Scale(0→1)。
	require.Equal(t, 2, orch.scaleCalls)
	// UpdateImage 不被调用。
	require.Equal(t, 0, orch.updateImageCalls)
	// S3 objects 被清除（objects != nil 时总执行）。
	require.True(t, objects.deletedSessionsPrefix)
	require.True(t, objects.deletedStateDB)
	// 状态更新为 running。
	require.Equal(t, domain.AppStatusRunning, stub.statusUpdates[len(stub.statusUpdates)-1])
	// 无 refresher 时不记录 applied 版本。
	require.False(t, stub.appliedVersionSet, "无 refresher 时不应调用 SetAppAppliedVersion")
}

// TestAppRestartContainerHandler_NoObjectStore_SkipsS3Cleanup 验证 objects 为 nil 时
// 跳过 S3 清除步骤，Scale(0→1) 仍正常执行（无 S3 时的兼容路径）。
func TestAppRestartContainerHandler_NoObjectStore_SkipsS3Cleanup(t *testing.T) {
	stub := runtimeStub(t)
	orch := &fakeAppOrchestrator{}
	handler := NewAppRestartContainerHandler(stub, orch, nil) // objects=nil

	err := handler.Handle(context.Background(), runtimeJob(domain.JobTypeAppRestartContainer, testAppID))
	require.NoError(t, err)
	// Scale(0→1) 仍正常执行。
	require.Equal(t, 2, orch.scaleCalls)
	require.Equal(t, domain.AppStatusRunning, stub.statusUpdates[len(stub.statusUpdates)-1])
}

// TestAppRestartContainerHandler_RefresherError_AbortsRestart 验证 refresher 失败时
// 错误冒泡、Scale 和 S3 操作不被触发。
func TestAppRestartContainerHandler_RefresherError_AbortsRestart(t *testing.T) {
	stub := runtimeStub(t)
	orch := &fakeAppOrchestrator{}
	objects := &fakeObjectStore{}
	refresher := &fakeInputRefresher{returnError: errors.New("刷新配置失败")}
	handler := NewAppRestartContainerHandler(stub, orch, objects)
	handler.SetInputRefresher(refresher)

	err := handler.Handle(context.Background(), runtimeJob(domain.JobTypeAppRestartContainer, testAppID))
	require.Error(t, err)
	// refresher 失败后不应触发任何运行时操作。
	require.Equal(t, 0, orch.scaleCalls)
	require.Equal(t, 0, orch.updateImageCalls)
	require.False(t, objects.deletedSessionsPrefix)
}

// ─────────────────────────────────────────────
// AppDeleteHandler 单测
// ─────────────────────────────────────────────

// TestAppDeleteHandler_HappyPath 验证完整删除路径：
// Delete k8s → 禁 new-api key → 归档 S3 → 清 KB → 软删。
func TestAppDeleteHandler_HappyPath(t *testing.T) {
	stub := runtimeStub(t)
	orch := &fakeAppOrchestrator{}
	disabler := &fakeDisabler{}
	objects := &fakeObjectStore{}
	knowledge := &fakeKnowledgeCleaner{}
	handler := NewAppDeleteHandler(stub, orch, disabler, objects, knowledge)

	err := handler.Handle(context.Background(), runtimeJob(domain.JobTypeAppDelete, testAppID))
	require.NoError(t, err)
	// k8s Delete 被调用一次。
	require.Equal(t, 1, orch.deleteCalls)
	// new-api key 被禁用（keyID=42, status=2）。
	assert.Equal(t, int64(42), disabler.id)
	assert.Equal(t, 2, disabler.status)
	// S3 应用目录被归档（MovePrefix 被调用）。
	require.True(t, objects.movedPrefix, "删除时必须归档 S3 应用目录")
	assert.Equal(t, "apps/"+testAppID+"/", objects.moveSrc)
	assert.Equal(t, "apps/"+testAppID+"/archive/", objects.moveDst)
	// KB 被清理。
	require.Equal(t, testAppID, knowledge.cleanedAppID)
	// 应用被软删。
	require.True(t, stub.softDeleted)
}

// TestAppDeleteHandler_TreatsKnowledgeCleanupErrorAsBestEffort 验证 RAGFlow dataset 清理失败
// 不阻断本地应用软删（外部派生资源，best-effort 清理）。
func TestAppDeleteHandler_TreatsKnowledgeCleanupErrorAsBestEffort(t *testing.T) {
	stub := runtimeStub(t)
	knowledge := &fakeKnowledgeCleaner{err: errors.New("ragflow unavailable")}
	handler := NewAppDeleteHandler(stub, &fakeAppOrchestrator{}, &fakeDisabler{}, nil, knowledge)

	err := handler.Handle(context.Background(), runtimeJob(domain.JobTypeAppDelete, testAppID))
	require.NoError(t, err)
	require.True(t, stub.softDeleted)
	require.Equal(t, testAppID, knowledge.cleanedAppID)
}

// TestAppDeleteHandler_SkipsNewAPIWhenNoKey 验证 NewapiKeyID 为空时跳过禁 key 步骤。
func TestAppDeleteHandler_SkipsNewAPIWhenNoKey(t *testing.T) {
	stub := runtimeStub(t)
	stub.app.NewapiKeyID = null.String{}
	disabler := &fakeDisabler{}
	handler := NewAppDeleteHandler(stub, &fakeAppOrchestrator{}, disabler, nil)
	err := handler.Handle(context.Background(), runtimeJob(domain.JobTypeAppDelete, testAppID))
	require.NoError(t, err)
	// 无 key_id 时不调 SetAPIKeyStatus。
	require.Equal(t, int64(0), disabler.id)
}

// TestAppDeleteHandler_PropagatesNewAPIError 验证禁 key 失败时错误冒泡，应用不被软删。
func TestAppDeleteHandler_PropagatesNewAPIError(t *testing.T) {
	stub := runtimeStub(t)
	disabler := &fakeDisabler{err: errors.New("upstream")}
	handler := NewAppDeleteHandler(stub, &fakeAppOrchestrator{}, disabler, nil)
	err := handler.Handle(context.Background(), runtimeJob(domain.JobTypeAppDelete, testAppID))
	require.Error(t, err)
	require.False(t, stub.softDeleted)
}

// TestAppDeleteHandler_SkipsS3WhenNoObjectStore 验证 objects 为 nil 时跳过 S3 归档，
// 其他步骤正常执行（无 S3 时的兼容路径）。
func TestAppDeleteHandler_SkipsS3WhenNoObjectStore(t *testing.T) {
	stub := runtimeStub(t)
	handler := NewAppDeleteHandler(stub, &fakeAppOrchestrator{}, &fakeDisabler{}, nil) // objects=nil
	err := handler.Handle(context.Background(), runtimeJob(domain.JobTypeAppDelete, testAppID))
	require.NoError(t, err)
	require.True(t, stub.softDeleted)
}

// TestAppDeleteHandler_AlreadyDeletedStillCleansExternalResources 验证删除成员预先软删应用后，
// app_delete 仍会清理 k8s 资源、new-api key、S3 目录和 RAGFlow dataset，但不重复软删。
func TestAppDeleteHandler_AlreadyDeletedStillCleansExternalResources(t *testing.T) {
	stub := runtimeStub(t)
	stub.app.DeletedAt = null.TimeFrom(time.Now()) // 模拟已软删除
	orch := &fakeAppOrchestrator{}
	disabler := &fakeDisabler{}
	objects := &fakeObjectStore{}
	knowledge := &fakeKnowledgeCleaner{}
	handler := NewAppDeleteHandler(stub, orch, disabler, objects, knowledge)

	err := handler.Handle(context.Background(), runtimeJob(domain.JobTypeAppDelete, testAppID))
	require.NoError(t, err)
	// k8s Delete 被调用。
	require.Equal(t, 1, orch.deleteCalls)
	// new-api key 被禁用。
	assert.Equal(t, int64(42), disabler.id)
	// S3 被归档。
	require.True(t, objects.movedPrefix)
	// KB 被清理。
	require.Equal(t, testAppID, knowledge.cleanedAppID)
	// 不重复软删。
	require.False(t, stub.softDeleted)
}

// ─────────────────────────────────────────────
// orch=nil 保护：编排器未配置时返回错误而非 panic
// ─────────────────────────────────────────────

// TestAppStartContainerHandler_NilOrch_ReturnsError 验证编排器未配置时
// AppStartContainerHandler.Handle 返回可诊断错误，而非 nil-panic 崩 worker。
func TestAppStartContainerHandler_NilOrch_ReturnsError(t *testing.T) {
	stub := runtimeStub(t)
	// 注入 nil orch，模拟 k8s 未配置场景（misconfiguration）。
	handler := NewAppStartContainerHandler(stub, nil)

	err := handler.Handle(context.Background(), runtimeJob(domain.JobTypeAppStartContainer, testAppID))
	// 必须返回错误，不能 panic。
	require.Error(t, err, "orch=nil 时应返回错误而非 panic")
	// 状态不应被更新（操作未执行）。
	require.Empty(t, stub.statusUpdates, "orch=nil 时不应更新 app 状态")
}

// TestAppStopContainerHandler_NilOrch_ReturnsError 验证编排器未配置时
// AppStopContainerHandler.Handle 返回可诊断错误，而非 nil-panic 崩 worker。
func TestAppStopContainerHandler_NilOrch_ReturnsError(t *testing.T) {
	stub := runtimeStub(t)
	// 注入 nil orch，模拟 k8s 未配置场景（misconfiguration）。
	handler := NewAppStopContainerHandler(stub, nil)

	err := handler.Handle(context.Background(), runtimeJob(domain.JobTypeAppStopContainer, testAppID))
	// 必须返回错误，不能 panic。
	require.Error(t, err, "orch=nil 时应返回错误而非 panic")
	// 状态不应被更新（操作未执行）。
	require.Empty(t, stub.statusUpdates, "orch=nil 时不应更新 app 状态")
}

// TestAppRestartContainerHandler_NilOrch_ReturnsError 验证编排器未配置时
// AppRestartContainerHandler.Handle 返回可诊断错误，而非 nil-panic 崩 worker。
// 两个分支（镜像变更 UpdateImage 和镜像不变 Scale）均依赖 orch，nil-guard 应在两者之前生效。
func TestAppRestartContainerHandler_NilOrch_ReturnsError(t *testing.T) {
	// 子测试 1：镜像不变路径（无 refresher），orch=nil 应在 Scale 前返回错误。
	t.Run("无_refresher_镜像不变路径", func(t *testing.T) {
		stub := runtimeStub(t)
		// 注入 nil orch，objects 非 nil 验证 S3 清除不会先于 nil-guard 触发。
		handler := NewAppRestartContainerHandler(stub, nil, &fakeObjectStore{})

		err := handler.Handle(context.Background(), runtimeJob(domain.JobTypeAppRestartContainer, testAppID))
		// 必须返回错误，不能 panic。
		require.Error(t, err, "orch=nil 时应返回错误而非 panic")
		require.Empty(t, stub.statusUpdates, "orch=nil 时不应更新 app 状态")
	})

	// 子测试 2：镜像变更路径（refresher 返回新镜像），orch=nil 应在 UpdateImage 前返回错误。
	t.Run("有_refresher_镜像变更路径", func(t *testing.T) {
		stub := runtimeStub(t)
		stub.app.RuntimeImageRef = "hermes-v1:old"
		refresher := &fakeInputRefresher{
			returnResult: AppInputRefreshResult{VersionRevision: 5, ImageRef: "hermes-v2:new"},
		}
		// 注入 nil orch，验证镜像变更分支同样受 nil-guard 保护。
		handler := NewAppRestartContainerHandler(stub, nil, nil)
		handler.SetInputRefresher(refresher)

		err := handler.Handle(context.Background(), runtimeJob(domain.JobTypeAppRestartContainer, testAppID))
		// 必须返回错误，不能 panic。
		require.Error(t, err, "orch=nil 时应返回错误而非 panic（镜像变更分支）")
		require.Empty(t, stub.statusUpdates, "orch=nil 时不应更新 app 状态")
		// app_initialize job 不应入队。
		require.Empty(t, stub.createdJobs, "orch=nil 时不应入队 app_initialize job")
	})
}

// ─────────────────────────────────────────────
// 通用校验
// ─────────────────────────────────────────────

// TestAppRuntimeHandlers_RejectMismatchedJobType 验证四个 handler 在收到错误 job 类型时拒绝处理。
func TestAppRuntimeHandlers_RejectMismatchedJobType(t *testing.T) {
	stub := runtimeStub(t)
	bad := runtimeJob("unknown", testAppID)
	orch := &fakeAppOrchestrator{}
	testHandlers := []func(context.Context, sqlc.Job) error{
		NewAppStartContainerHandler(stub, orch).Handle,
		NewAppStopContainerHandler(stub, orch).Handle,
		NewAppRestartContainerHandler(stub, orch, nil).Handle,
		NewAppDeleteHandler(stub, orch, &fakeDisabler{}, nil).Handle,
	}
	for _, h := range testHandlers {
		err := h(context.Background(), bad)
		require.Error(t, err)
	}
}

// ─────────────────────────────────────────────
// 测试桩实现
// ─────────────────────────────────────────────

// runtimeOpStub 是 AppRuntimeStore 的内存桩，记录各方法调用供断言使用。
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

func (s *runtimeOpStub) GetApp(_ context.Context, _ string) (sqlc.App, error) { return s.app, nil }

func (s *runtimeOpStub) SetAppStatus(_ context.Context, arg sqlc.SetAppStatusParams) error {
	s.statusUpdates = append(s.statusUpdates, arg.Status)
	s.app.Status = arg.Status
	return nil
}

func (s *runtimeOpStub) SoftDeleteApp(_ context.Context, _ string) error {
	s.softDeleted = true
	s.app.DeletedAt = null.TimeFrom(time.Now())
	return nil
}

// SetAppAppliedVersion 实现 AppRuntimeStore 接口；记录已应用的版本修订与镜像 ref。
func (s *runtimeOpStub) SetAppAppliedVersion(_ context.Context, arg sqlc.SetAppAppliedVersionParams) error {
	s.appliedVersionSet = true
	s.lastAppliedVersion = arg
	s.app.AppliedVersionRevision = arg.AppliedVersionRevision
	s.app.AppliedImageRef = arg.AppliedImageRef
	return nil
}

// SetAppContainer 实现 AppRuntimeStore 接口；k8s 路径在镜像变更时可选清空 container_id。
func (s *runtimeOpStub) SetAppContainer(_ context.Context, arg sqlc.SetAppContainerParams) error {
	s.containerCleared = true
	s.app.ContainerID = arg.ContainerID
	s.app.ContainerName = arg.ContainerName
	return nil
}

// CreateJob 实现 AppRuntimeStore 接口；记录入参供断言。
func (s *runtimeOpStub) CreateJob(_ context.Context, arg sqlc.CreateJobParams) error {
	s.createdJobs = append(s.createdJobs, arg)
	return nil
}

// fakeAppOrchestrator 是 appOrchestrator 接口的测试桩，记录各方法调用。
type fakeAppOrchestrator struct {
	// Scale 相关
	scaleCalls        int
	lastScaleReplicas int32
	scaleHistory      []int32 // 按调用顺序记录 replicas，供 Scale(0)→Scale(1) 顺序断言
	scaleErr          error
	// UpdateImage 相关
	updateImageCalls int
	lastUpdateImage  string
	updateImageErr   error
	// Delete 相关
	deleteCalls int
	deleteErr   error
}

func (f *fakeAppOrchestrator) Scale(_ context.Context, _ string, replicas int32) error {
	f.scaleCalls++
	f.lastScaleReplicas = replicas
	f.scaleHistory = append(f.scaleHistory, replicas)
	return f.scaleErr
}

func (f *fakeAppOrchestrator) UpdateImage(_ context.Context, _ string, hermesImage string) error {
	f.updateImageCalls++
	f.lastUpdateImage = hermesImage
	return f.updateImageErr
}

func (f *fakeAppOrchestrator) Delete(_ context.Context, _ string) error {
	f.deleteCalls++
	return f.deleteErr
}

// fakeObjectStore 是 storage.ObjectStore 的最小测试桩，仅实现 MovePrefix / DeletePrefix。
type fakeObjectStore struct {
	// MovePrefix 调用记录
	movedPrefix bool
	moveSrc     string
	moveDst     string
	movePrefixErr error
	// DeletePrefix 调用记录（按 key 细化）
	deletedSessionsPrefix bool
	deletedStateDB        bool
	deletePrefixErr       error
	// 记录所有 DeletePrefix 的 key，供细化断言
	deletedPrefixes []string
}

func (f *fakeObjectStore) MovePrefix(_ context.Context, src, dst string) error {
	f.movedPrefix = true
	f.moveSrc = src
	f.moveDst = dst
	return f.movePrefixErr
}

func (f *fakeObjectStore) DeletePrefix(_ context.Context, prefix string) error {
	f.deletedPrefixes = append(f.deletedPrefixes, prefix)
	// 按 key 内容区分 sessions 与 state.db 的删除。
	if len(prefix) > 0 {
		// sessions/ 前缀末尾含 "sessions/"。
		if len(prefix) >= 9 && prefix[len(prefix)-9:] == "sessions/" {
			f.deletedSessionsPrefix = true
		}
		// state.db key 以 "state.db" 结尾。
		if len(prefix) >= 8 && prefix[len(prefix)-8:] == "state.db" {
			f.deletedStateDB = true
		}
	}
	return f.deletePrefixErr
}

// storage.ObjectStore 剩余方法不在删除路径使用，留空实现满足接口编译要求。
func (f *fakeObjectStore) PutObject(_ context.Context, _ string, _ io.Reader, _ int64) error {
	return nil
}
func (f *fakeObjectStore) PresignGet(_ context.Context, _ string, _ time.Duration) (string, error) {
	return "", nil
}
func (f *fakeObjectStore) ObjectExists(_ context.Context, _ string) (bool, error) {
	return false, nil
}
func (f *fakeObjectStore) ListObjects(_ context.Context, _ string) ([]storage.ObjectInfo, error) {
	return nil, nil
}

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

// fakeInputRefresher 是 AppInputRefresher 的测试桩。
type fakeInputRefresher struct {
	calls        int
	lastNodeID   string
	lastAppID    string
	returnError  error
	// returnResult 是 RefreshAppInput 成功时返回的版本信息。
	returnResult AppInputRefreshResult
}

func (f *fakeInputRefresher) RefreshAppInput(_ context.Context, nodeID string, app sqlc.App) (AppInputRefreshResult, error) {
	f.calls++
	f.lastNodeID = nodeID
	f.lastAppID = app.ID
	return f.returnResult, f.returnError
}

type fakeKnowledgeCleaner struct {
	cleanedAppID string
	err          error
}

// DeleteAppDataset 实现 KnowledgeCleaner 接口；appID 迁移为 string（MySQL uuid）。
func (f *fakeKnowledgeCleaner) DeleteAppDataset(_ context.Context, appID string) error {
	f.cleanedAppID = appID
	return f.err
}

// fakeDisabler 同时实现 NewAPIClientFactory + APIKeyClient：UserScopedFor 直接返回自身，
// 把"工厂派生 user-scoped client"的两层抽象在测试里压平。
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
