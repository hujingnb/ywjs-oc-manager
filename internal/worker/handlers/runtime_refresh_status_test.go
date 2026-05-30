package handlers

import (
	"context"
	"errors"
	"testing"

	"github.com/guregu/null/v5"
	"github.com/stretchr/testify/require"
	"oc-manager/internal/domain"
	"oc-manager/internal/integrations/runtime"
	"oc-manager/internal/store/sqlc"
)

// fakeRuntimeSnapshotStore 实现 AppRuntimeStore + InsertInstanceResourceSample 接口。
// 迁移后方法签名均使用 string/null.* 类型，:exec 方法仅返回 error。
type fakeRuntimeSnapshotStore struct {
	app          sqlc.App
	getErr       error
	saveErr      error
	savedPayload []byte
	samples      []sqlc.InsertInstanceResourceSampleParams
}

func (s *fakeRuntimeSnapshotStore) GetApp(_ context.Context, _ string) (sqlc.App, error) {
	if s.getErr != nil {
		return sqlc.App{}, s.getErr
	}
	return s.app, nil
}

func (s *fakeRuntimeSnapshotStore) SetAppStatus(_ context.Context, _ sqlc.SetAppStatusParams) error {
	return nil
}

func (s *fakeRuntimeSnapshotStore) SoftDeleteApp(_ context.Context, _ string) error {
	return nil
}

func (s *fakeRuntimeSnapshotStore) SetAppRuntimeSnapshot(_ context.Context, arg sqlc.SetAppRuntimeSnapshotParams) error {
	if s.saveErr != nil {
		return s.saveErr
	}
	s.savedPayload = arg.RuntimeSnapshotJson
	return nil
}

// InsertInstanceResourceSample 存档采样参数；:exec 语义仅返回 error。
func (s *fakeRuntimeSnapshotStore) InsertInstanceResourceSample(_ context.Context, arg sqlc.InsertInstanceResourceSampleParams) error {
	if s.saveErr != nil {
		return s.saveErr
	}
	s.samples = append(s.samples, arg)
	return nil
}

// SetAppAppliedVersion 实现 AppRuntimeStore 接口；刷新状态流程不写版本已应用信息，此处仅满足接口约束。
func (s *fakeRuntimeSnapshotStore) SetAppAppliedVersion(_ context.Context, _ sqlc.SetAppAppliedVersionParams) error {
	return nil
}

// CreateJob 实现 AppRuntimeStore 接口；刷新状态流程不入队 job，此处仅满足接口约束。
func (s *fakeRuntimeSnapshotStore) CreateJob(_ context.Context, _ sqlc.CreateJobParams) error {
	return nil
}

type fakeRuntimeInspector struct {
	info       runtime.ContainerInfo
	stats      runtime.ContainerStats
	inspectErr error
	statsErr   error
}

func (i *fakeRuntimeInspector) InspectContainer(_ context.Context, _, _ string) (runtime.ContainerInfo, error) {
	return i.info, i.inspectErr
}

func (i *fakeRuntimeInspector) ContainerStats(_ context.Context, _, _ string) (runtime.ContainerStats, error) {
	return i.stats, i.statsErr
}

// makeAppForRefresh 构造一个运行中的 app，ID 现为 string（MySQL uuid）。
func makeAppForRefresh(t *testing.T) sqlc.App {
	t.Helper()
	return sqlc.App{
		ID:            "11111111-1111-1111-1111-111111111111",
		RuntimeNodeID: null.StringFrom("22222222-2222-2222-2222-222222222222"), // RuntimeNodeID nullable（spec-A2a）
		ContainerID:   null.StringFrom("ctr-abc"),
		Status:        domain.AppStatusRunning,
	}
}

// 确保 null 包导入被使用。
var _ = null.String{}

// TestRuntimeRefreshStatusHappyPath 验证运行时刷新状态成功路径的成功路径场景。
func TestRuntimeRefreshStatusHappyPath(t *testing.T) {
	store := &fakeRuntimeSnapshotStore{app: makeAppForRefresh(t)}
	inspector := &fakeRuntimeInspector{
		info:  runtime.ContainerInfo{ID: "ctr-abc", Name: "ocm-app", Image: "hermes-runtime:v2026.5.16-dev", Status: "running"},
		stats: runtime.ContainerStats{CPUPercent: 12.5, MemoryUsage: 1024, MemoryLimit: 4096, NetworkRxBytes: 100, NetworkTxBytes: 50},
	}
	h := NewRuntimeRefreshStatusHandler(store, inspector)
	job := sqlc.Job{Type: domain.JobTypeRuntimeRefreshStatus, PayloadJson: []byte(`{"app_id":"11111111-1111-1111-1111-111111111111"}`)}
	err := h.Handle(context.Background(), job)
	require.NoError(t, err)
	require.Len(t, store.samples, 1)
	got := store.samples[0]
	require.Equal(t, "ctr-abc", got.ContainerID)
	require.Equal(t, "running", got.ContainerStatus.String)
	require.Equal(t, 12.5, got.CpuPercent.Float64)
	require.Equal(t, int64(1024), got.MemoryUsedBytes.Int64)
	require.Equal(t, int64(100), got.NetworkRxBytes.Int64)
	require.Nil(t, store.savedPayload)
}

// TestRuntimeRefreshStatusWritesInstanceSample 验证运行时刷新资源展示数据写入实例采样表而不是应用快照字段。
func TestRuntimeRefreshStatusWritesInstanceSample(t *testing.T) {
	store := &fakeRuntimeSnapshotStore{app: makeAppForRefresh(t)}
	inspector := &fakeRuntimeInspector{
		info:  runtime.ContainerInfo{ID: "ctr-abc", Name: "ocm-app", Image: "hermes-runtime:v2026.5.16-dev", Status: "running"},
		stats: runtime.ContainerStats{CPUPercent: 12.5, MemoryUsage: 1024, MemoryLimit: 4096, DiskReadBytes: 77, DiskWriteBytes: 88, NetworkRxBytes: 100, NetworkTxBytes: 50},
	}
	h := NewRuntimeRefreshStatusHandler(store, inspector)
	job := sqlc.Job{Type: domain.JobTypeRuntimeRefreshStatus, PayloadJson: []byte(`{"app_id":"11111111-1111-1111-1111-111111111111"}`)}

	err := h.Handle(context.Background(), job)
	require.NoError(t, err)
	require.Len(t, store.samples, 1)
	got := store.samples[0]
	require.Equal(t, makeAppForRefresh(t).ID, got.AppID)
	// RuntimeNodeID nullable（spec-A2a）：app.RuntimeNodeID 是 null.String，采样记录中是 string，取 .String 比较。
	require.Equal(t, makeAppForRefresh(t).RuntimeNodeID.String, got.RuntimeNodeID)
	require.Equal(t, "ctr-abc", got.ContainerID)
	require.Equal(t, "running", got.ContainerStatus.String)
	require.Equal(t, 12.5, got.CpuPercent.Float64)
	require.Equal(t, int64(77), got.DiskReadBytes.Int64)
	require.Equal(t, int64(88), got.DiskWriteBytes.Int64)
	require.Nil(t, store.savedPayload)
}

// TestRuntimeRefreshStatusInspectErrorRecorded 验证运行时刷新状态检查错误记录ed的预期行为场景。
func TestRuntimeRefreshStatusInspectErrorRecorded(t *testing.T) {
	store := &fakeRuntimeSnapshotStore{app: makeAppForRefresh(t)}
	inspector := &fakeRuntimeInspector{inspectErr: errors.New("dial err")}
	h := NewRuntimeRefreshStatusHandler(store, inspector)
	job := sqlc.Job{Type: domain.JobTypeRuntimeRefreshStatus, PayloadJson: []byte(`{"app_id":"11111111-1111-1111-1111-111111111111"}`)}
	err := h.Handle(context.Background(), job)
	require.NoError(t, err)
	require.Len(t, store.samples, 1)
	require.NotEqual(t, "", store.samples[0].LastError.String)
}

// TestRuntimeRefreshStatusSkipsNoContainer 验证运行时刷新状态跳过无容器的特殊分支或幂等场景。
func TestRuntimeRefreshStatusSkipsNoContainer(t *testing.T) {
	app := makeAppForRefresh(t)
	// ContainerID 迁移为 null.String；零值 null.String{} 表示 NULL（无容器）。
	app.ContainerID = null.String{}
	store := &fakeRuntimeSnapshotStore{app: app}
	h := NewRuntimeRefreshStatusHandler(store, &fakeRuntimeInspector{})
	job := sqlc.Job{Type: domain.JobTypeRuntimeRefreshStatus, PayloadJson: []byte(`{"app_id":"11111111-1111-1111-1111-111111111111"}`)}
	err := h.Handle(context.Background(), job)
	require.NoError(t, err)
	require.Nil(t, store.savedPayload)
}
