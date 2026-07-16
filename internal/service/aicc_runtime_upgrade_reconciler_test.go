package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"oc-manager/internal/domain"
	"oc-manager/internal/store/sqlc"
)

// TestAICCRuntimeUpgradeReconcilerQueuesOneStaleApp 验证一轮升级只为一个镜像漂移的客服隐藏应用创建初始化任务。
func TestAICCRuntimeUpgradeReconcilerQueuesOneStaleApp(t *testing.T) {
	store := &aiccRuntimeUpgradeStoreStub{staleAppIDs: []string{"aicc-app-1"}}
	notifier := &aiccRuntimeUpgradeNotifierStub{}
	reconciler := NewAICCRuntimeUpgradeReconciler(store, notifier, "registry.example.com/app/oc-manager-aigowork-aicc:v1.0.0-test")

	require.NoError(t, reconciler.Tick(context.Background()))
	require.Len(t, store.createdJobs, 1)
	assert.Equal(t, domain.JobTypeAppInitialize, store.createdJobs[0].Type)
	assert.Equal(t, "aicc-app-1", payloadAppID(t, store.createdJobs[0].PayloadJson))
	assert.Equal(t, []string{store.createdJobs[0].ID}, notifier.jobIDs)
	assert.Equal(t, int32(1), store.lastListArg.Limit)
	assert.Equal(t, "registry.example.com/app/oc-manager-aigowork-aicc:v1.0.0-test", store.lastListArg.TargetImageRef)
}

// TestAICCRuntimeUpgradeReconcilerSkipsConvergedApps 验证没有镜像漂移客服时不会创建或通知初始化任务。
func TestAICCRuntimeUpgradeReconcilerSkipsConvergedApps(t *testing.T) {
	store := &aiccRuntimeUpgradeStoreStub{}
	notifier := &aiccRuntimeUpgradeNotifierStub{}
	reconciler := NewAICCRuntimeUpgradeReconciler(store, notifier, "registry.example.com/app/oc-manager-aigowork-aicc:v1.0.0-test")

	require.NoError(t, reconciler.Tick(context.Background()))
	assert.Empty(t, store.createdJobs)
	assert.Empty(t, notifier.jobIDs)
}

// TestAICCRuntimeUpgradeReconcilerRejectsEmptyRuntimeImage 验证协调器不会在缺失客服镜像配置时创建错误任务。
func TestAICCRuntimeUpgradeReconcilerRejectsEmptyRuntimeImage(t *testing.T) {
	store := &aiccRuntimeUpgradeStoreStub{staleAppIDs: []string{"aicc-app-1"}}
	reconciler := NewAICCRuntimeUpgradeReconciler(store, &aiccRuntimeUpgradeNotifierStub{}, "")

	err := reconciler.Tick(context.Background())
	require.Error(t, err)
	assert.ErrorContains(t, err, "aicc.runtime_image")
	assert.Empty(t, store.createdJobs)
}

type aiccRuntimeUpgradeStoreStub struct {
	staleAppIDs []string
	lastListArg sqlc.ListStaleAICCRuntimeAppsParams
	latestJob   sqlc.Job
	createdJobs []sqlc.CreateJobParams
}

func (s *aiccRuntimeUpgradeStoreStub) ListStaleAICCRuntimeApps(_ context.Context, arg sqlc.ListStaleAICCRuntimeAppsParams) ([]string, error) {
	s.lastListArg = arg
	return s.staleAppIDs, nil
}

func (s *aiccRuntimeUpgradeStoreStub) GetLatestAppInitJob(_ context.Context, _ string) (sqlc.Job, error) {
	if s.latestJob.ID == "" {
		return sqlc.Job{}, sql.ErrNoRows
	}
	return s.latestJob, nil
}

func (s *aiccRuntimeUpgradeStoreStub) RequeueJob(_ context.Context, _ string) error { return nil }

func (s *aiccRuntimeUpgradeStoreStub) CreateJob(_ context.Context, arg sqlc.CreateJobParams) error {
	s.createdJobs = append(s.createdJobs, arg)
	return nil
}

type aiccRuntimeUpgradeNotifierStub struct{ jobIDs []string }

func (n *aiccRuntimeUpgradeNotifierStub) Enqueue(_ context.Context, jobID string) error {
	n.jobIDs = append(n.jobIDs, jobID)
	return nil
}

func payloadAppID(t *testing.T, payload []byte) string {
	t.Helper()
	var value struct {
		AppID string `json:"app_id"`
	}
	require.NoError(t, json.Unmarshal(payload, &value))
	return value.AppID
}
