package handlers

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/stretchr/testify/require"
	"oc-manager/internal/domain"
	"oc-manager/internal/integrations/runtime"
	"oc-manager/internal/store/sqlc"
)

type fakeRuntimeSnapshotStore struct {
	app          sqlc.App
	getErr       error
	saveErr      error
	savedPayload []byte
	samples      []sqlc.InsertInstanceResourceSampleParams
}

func (s *fakeRuntimeSnapshotStore) GetApp(_ context.Context, _ pgtype.UUID) (sqlc.App, error) {
	if s.getErr != nil {
		return sqlc.App{}, s.getErr
	}
	return s.app, nil
}

func (s *fakeRuntimeSnapshotStore) SetAppStatus(_ context.Context, _ sqlc.SetAppStatusParams) (sqlc.App, error) {
	return s.app, nil
}

func (s *fakeRuntimeSnapshotStore) SoftDeleteApp(_ context.Context, _ pgtype.UUID) (sqlc.App, error) {
	return s.app, nil
}

func (s *fakeRuntimeSnapshotStore) SetAppRuntimeSnapshot(_ context.Context, arg sqlc.SetAppRuntimeSnapshotParams) (sqlc.App, error) {
	if s.saveErr != nil {
		return sqlc.App{}, s.saveErr
	}
	s.savedPayload = arg.RuntimeSnapshotJson
	return s.app, nil
}

func (s *fakeRuntimeSnapshotStore) InsertInstanceResourceSample(_ context.Context, arg sqlc.InsertInstanceResourceSampleParams) (sqlc.InstanceResourceSample, error) {
	if s.saveErr != nil {
		return sqlc.InstanceResourceSample{}, s.saveErr
	}
	s.samples = append(s.samples, arg)
	return sqlc.InstanceResourceSample{}, nil
}

// SetAppAppliedVersion 实现 AppRuntimeStore 接口；刷新状态流程不写版本已应用信息，此处仅满足接口约束。
func (s *fakeRuntimeSnapshotStore) SetAppAppliedVersion(_ context.Context, _ sqlc.SetAppAppliedVersionParams) (sqlc.App, error) {
	return s.app, nil
}

// SetAppContainer 实现 AppRuntimeStore 接口；刷新状态流程不重建容器，此处仅满足接口约束。
func (s *fakeRuntimeSnapshotStore) SetAppContainer(_ context.Context, _ sqlc.SetAppContainerParams) (sqlc.App, error) {
	return s.app, nil
}

// CreateJob 实现 AppRuntimeStore 接口；刷新状态流程不入队 job，此处仅满足接口约束。
func (s *fakeRuntimeSnapshotStore) CreateJob(_ context.Context, _ sqlc.CreateJobParams) (sqlc.Job, error) {
	return sqlc.Job{}, nil
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

func makeAppForRefresh(t *testing.T) sqlc.App {
	t.Helper()
	id, err := pgUUIDFromString("11111111-1111-1111-1111-111111111111")
	require.NoError(t, err)
	node, err := pgUUIDFromString("22222222-2222-2222-2222-222222222222")
	require.NoError(t, err)
	return sqlc.App{
		ID:            id,
		RuntimeNodeID: node,
		ContainerID:   pgtype.Text{String: "ctr-abc", Valid: true},
		Status:        domain.AppStatusRunning,
	}
}

// pgUUIDFromString 把标准 uuid 字符串解码成 pgtype.UUID；测试本地用，避免依赖 service 包。
func pgUUIDFromString(s string) (pgtype.UUID, error) {
	var out pgtype.UUID
	if err := out.Scan(s); err != nil {
		return pgtype.UUID{}, err
	}
	return out, nil
}

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
	require.Equal(t, makeAppForRefresh(t).RuntimeNodeID, got.RuntimeNodeID)
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
	app.ContainerID = pgtype.Text{}
	store := &fakeRuntimeSnapshotStore{app: app}
	h := NewRuntimeRefreshStatusHandler(store, &fakeRuntimeInspector{})
	job := sqlc.Job{Type: domain.JobTypeRuntimeRefreshStatus, PayloadJson: []byte(`{"app_id":"11111111-1111-1111-1111-111111111111"}`)}
	err := h.Handle(context.Background(), job)
	require.NoError(t, err)
	require.Nil(t, store.savedPayload)
}
