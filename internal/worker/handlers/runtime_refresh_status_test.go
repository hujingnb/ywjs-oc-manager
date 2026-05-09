package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	"oc-manager/internal/domain"
	"oc-manager/internal/integrations/runtime"
	"oc-manager/internal/store/sqlc"
	"github.com/stretchr/testify/require"
)

type fakeRuntimeSnapshotStore struct {
	app          sqlc.App
	getErr       error
	saveErr      error
	savedPayload []byte
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

func TestRuntimeRefreshStatusHappyPath(t *testing.T) {
	store := &fakeRuntimeSnapshotStore{app: makeAppForRefresh(t)}
	inspector := &fakeRuntimeInspector{
		info:  runtime.ContainerInfo{ID: "ctr-abc", Name: "ocm-app", Image: "openclaw:dev", Status: "running"},
		stats: runtime.ContainerStats{CPUPercent: 12.5, MemoryUsage: 1024, MemoryLimit: 4096, NetworkRxBytes: 100, NetworkTxBytes: 50},
	}
	h := NewRuntimeRefreshStatusHandler(store, inspector)
	job := sqlc.Job{Type: domain.JobTypeRuntimeRefreshStatus, PayloadJson: []byte(`{"app_id":"11111111-1111-1111-1111-111111111111"}`)}
	err := h.Handle(context.Background(), job)
	require.NoError(t, err)
	var got AppRuntimeSnapshot
	err = json.Unmarshal(store.savedPayload, &got)
	require.NoError(t, err)
	if got.CPUPercent != 12.5 || got.MemoryUsage != 1024 || got.NetworkRxBytes != 100 || got.Status != "running" {
		t.Fatalf("snapshot = %+v", got)
	}
}

func TestRuntimeRefreshStatusInspectErrorRecorded(t *testing.T) {
	store := &fakeRuntimeSnapshotStore{app: makeAppForRefresh(t)}
	inspector := &fakeRuntimeInspector{inspectErr: errors.New("dial err")}
	h := NewRuntimeRefreshStatusHandler(store, inspector)
	job := sqlc.Job{Type: domain.JobTypeRuntimeRefreshStatus, PayloadJson: []byte(`{"app_id":"11111111-1111-1111-1111-111111111111"}`)}
	err := h.Handle(context.Background(), job)
	require.NoError(t, err)
	var got AppRuntimeSnapshot
	err = json.Unmarshal(store.savedPayload, &got)
	require.NoError(t, err)
	require.NotEqual(t, "", got.LastError)
}

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
