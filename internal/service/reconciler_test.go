package service

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/stretchr/testify/require"
	"oc-manager/internal/domain"
	"oc-manager/internal/store/sqlc"
)

const testReconcilerNodeID = "00000000-0000-0000-0000-000000005001"

type reconcilerStub struct {
	nodes        []sqlc.RuntimeNode
	apps         map[string][]sqlc.App
	updatedNodes []string
	updatedApps  map[string]string
}

func newReconcilerStub() *reconcilerStub {
	return &reconcilerStub{
		apps:        map[string][]sqlc.App{},
		updatedApps: map[string]string{},
	}
}

func (s *reconcilerStub) ListRuntimeNodes(_ context.Context, _ sqlc.ListRuntimeNodesParams) ([]sqlc.RuntimeNode, error) {
	return s.nodes, nil
}

func (s *reconcilerStub) SetRuntimeNodeStatus(_ context.Context, arg sqlc.SetRuntimeNodeStatusParams) (sqlc.RuntimeNode, error) {
	s.updatedNodes = append(s.updatedNodes, uuidToString(arg.ID)+"="+arg.Status)
	for i := range s.nodes {
		if s.nodes[i].ID == arg.ID {
			s.nodes[i].Status = arg.Status
			return s.nodes[i], nil
		}
	}
	return sqlc.RuntimeNode{}, nil
}

func (s *reconcilerStub) ListAppsByRuntimeNode(_ context.Context, arg sqlc.ListAppsByRuntimeNodeParams) ([]sqlc.App, error) {
	return s.apps[uuidToString(arg.RuntimeNodeID)], nil
}

func (s *reconcilerStub) SetAppStatus(_ context.Context, arg sqlc.SetAppStatusParams) (sqlc.App, error) {
	s.updatedApps[uuidToString(arg.ID)] = arg.Status
	return sqlc.App{ID: arg.ID, Status: arg.Status}, nil
}

func TestNodeHealthReconciler_DemotesTimedOutNodesAndApps(t *testing.T) {
	stub := newReconcilerStub()
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)

	timeoutNode := sqlc.RuntimeNode{
		ID:              mustUUID(t, testReconcilerNodeID),
		Status:          domain.RuntimeNodeStatusActive,
		LastHeartbeatAt: pgtype.Timestamptz{Time: now.Add(-5 * time.Minute), Valid: true},
	}
	healthyNode := sqlc.RuntimeNode{
		ID:              mustUUID(t, "00000000-0000-0000-0000-000000005002"),
		Status:          domain.RuntimeNodeStatusActive,
		LastHeartbeatAt: pgtype.Timestamptz{Time: now.Add(-5 * time.Second), Valid: true},
	}
	stub.nodes = []sqlc.RuntimeNode{timeoutNode, healthyNode}
	app := sqlc.App{
		ID:            mustUUID(t, "00000000-0000-0000-0000-000000005003"),
		RuntimeNodeID: timeoutNode.ID,
		Status:        domain.AppStatusRunning,
	}
	stub.apps[uuidToString(timeoutNode.ID)] = []sqlc.App{app}

	rec := NewNodeHealthReconciler(stub, 90*time.Second)
	rec.SetClock(func() time.Time { return now })
	demoted, err := rec.Reconcile(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, demoted)
	if len(stub.updatedNodes) != 1 || stub.updatedNodes[0] != testReconcilerNodeID+"="+domain.RuntimeNodeStatusUnreachable {
		t.Fatalf("updatedNodes = %+v", stub.updatedNodes)
	}
	require.Equal(t, domain.AppStatusError, stub.updatedApps[uuidToString(app.ID)])
}

func TestNodeHealthReconciler_SkipsAlreadyDisabledNodes(t *testing.T) {
	stub := newReconcilerStub()
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	stub.nodes = []sqlc.RuntimeNode{
		{
			ID:              mustUUID(t, testReconcilerNodeID),
			Status:          domain.RuntimeNodeStatusDisabled,
			LastHeartbeatAt: pgtype.Timestamptz{Time: now.Add(-1 * time.Hour), Valid: true},
		},
	}
	rec := NewNodeHealthReconciler(stub, 90*time.Second)
	rec.SetClock(func() time.Time { return now })
	demoted, err := rec.Reconcile(context.Background())
	require.NoError(t, err)
	require.Equal(t, 0, demoted)
	require.Equal(t, 0, len(stub.updatedNodes))
}

func TestNodeHealthReconciler_NeverHeartbeatedTreatedAsTimedOut(t *testing.T) {
	stub := newReconcilerStub()
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	stub.nodes = []sqlc.RuntimeNode{
		{
			ID:              mustUUID(t, testReconcilerNodeID),
			Status:          domain.RuntimeNodeStatusActive,
			LastHeartbeatAt: pgtype.Timestamptz{Valid: false},
		},
	}
	rec := NewNodeHealthReconciler(stub, 90*time.Second)
	rec.SetClock(func() time.Time { return now })
	demoted, err := rec.Reconcile(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, demoted)
}
