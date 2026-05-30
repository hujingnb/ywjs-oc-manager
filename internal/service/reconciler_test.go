// Package service 的 reconciler_test 覆盖应用调和器对初始化、运行时状态和失败重试的处理。
package service

import (
	"context"
	"testing"
	"time"

	null "github.com/guregu/null/v5"

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

// SetRuntimeNodeStatus 为 :exec；stub 记录更新并修改内存中的节点状态。
func (s *reconcilerStub) SetRuntimeNodeStatus(_ context.Context, arg sqlc.SetRuntimeNodeStatusParams) error {
	s.updatedNodes = append(s.updatedNodes, arg.ID+"="+arg.Status)
	for i := range s.nodes {
		if s.nodes[i].ID == arg.ID {
			s.nodes[i].Status = arg.Status
		}
	}
	return nil
}

func (s *reconcilerStub) ListAppsByRuntimeNode(_ context.Context, arg sqlc.ListAppsByRuntimeNodeParams) ([]sqlc.App, error) {
	// RuntimeNodeID nullable（spec-A2a）：.String 取 Go string 值，对应 map 字符串键。
	return s.apps[arg.RuntimeNodeID.String], nil
}

// SetAppStatus 为 :exec；stub 记录更新后的状态。
func (s *reconcilerStub) SetAppStatus(_ context.Context, arg sqlc.SetAppStatusParams) error {
	s.updatedApps[arg.ID] = arg.Status
	return nil
}

// TestNodeHealthReconciler_DemotesTimedOutNodesAndApps 验证节点健康检查ReconcilerDemotes超时超时节点并应用的预期行为场景。
func TestNodeHealthReconciler_DemotesTimedOutNodesAndApps(t *testing.T) {
	stub := newReconcilerStub()
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)

	timeoutNode := sqlc.RuntimeNode{
		ID:              mustUUID(t, testReconcilerNodeID),
		Status:          domain.RuntimeNodeStatusActive,
		LastHeartbeatAt: null.TimeFrom(now.Add(-5 * time.Minute)), // 超时 5 分钟
	}
	healthyNode := sqlc.RuntimeNode{
		ID:              mustUUID(t, "00000000-0000-0000-0000-000000005002"),
		Status:          domain.RuntimeNodeStatusActive,
		LastHeartbeatAt: null.TimeFrom(now.Add(-5 * time.Second)), // 仅 5 秒前，未超时
	}
	stub.nodes = []sqlc.RuntimeNode{timeoutNode, healthyNode}
	app := sqlc.App{
		ID:            mustUUID(t, "00000000-0000-0000-0000-000000005003"),
		RuntimeNodeID: null.StringFrom(timeoutNode.ID), // RuntimeNodeID nullable（spec-A2a）
		Status:        domain.AppStatusRunning,
	}
	stub.apps[timeoutNode.ID] = []sqlc.App{app}

	rec := NewNodeHealthReconciler(stub, 90*time.Second)
	rec.SetClock(func() time.Time { return now })
	demoted, err := rec.Reconcile(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, demoted)
	if len(stub.updatedNodes) != 1 || stub.updatedNodes[0] != testReconcilerNodeID+"="+domain.RuntimeNodeStatusUnreachable {
		t.Fatalf("updatedNodes = %+v", stub.updatedNodes)
	}
	require.Equal(t, domain.AppStatusError, stub.updatedApps[app.ID])
}

// TestNodeHealthReconciler_SkipsAlreadyDisabledNodes 验证节点健康检查Reconciler跳过已经禁用节点的特殊分支或幂等场景。
func TestNodeHealthReconciler_SkipsAlreadyDisabledNodes(t *testing.T) {
	stub := newReconcilerStub()
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	stub.nodes = []sqlc.RuntimeNode{
		{
			ID:              mustUUID(t, testReconcilerNodeID),
			Status:          domain.RuntimeNodeStatusDisabled,
			LastHeartbeatAt: null.TimeFrom(now.Add(-1 * time.Hour)), // 超时但已禁用，应跳过
		},
	}
	rec := NewNodeHealthReconciler(stub, 90*time.Second)
	rec.SetClock(func() time.Time { return now })
	demoted, err := rec.Reconcile(context.Background())
	require.NoError(t, err)
	require.Equal(t, 0, demoted)
	require.Equal(t, 0, len(stub.updatedNodes))
}

// TestNodeHealthReconciler_NeverHeartbeatedTreatedAsTimedOut 验证节点健康检查ReconcilerNeverHeartbeatedTreated作为超时超时的预期行为场景。
func TestNodeHealthReconciler_NeverHeartbeatedTreatedAsTimedOut(t *testing.T) {
	stub := newReconcilerStub()
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	stub.nodes = []sqlc.RuntimeNode{
		{
			ID:              mustUUID(t, testReconcilerNodeID),
			Status:          domain.RuntimeNodeStatusActive,
			LastHeartbeatAt: null.Time{}, // 从未心跳，视为超时
		},
	}
	rec := NewNodeHealthReconciler(stub, 90*time.Second)
	rec.SetClock(func() time.Time { return now })
	demoted, err := rec.Reconcile(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, demoted)
}
