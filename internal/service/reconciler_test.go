// Package service 的 reconciler_test 覆盖节点心跳超时检测的状态迁移与跳过逻辑。
// spec-A2b：apps.runtime_node_id 列概念已去除，markRunningAppsAsError 为 no-op，
// 节点超时不再联动推应用到 error（k8s 路径由 AppStatusReconciler 通过 orch.Status 感知）。
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

// reconcilerStub 实现 ReconcilerStore，记录节点状态更新调用供断言使用。
// spec-A2b：去掉 apps/updatedApps（ListAppsByRuntimeNode 与 SetAppStatus 已从接口移除）。
type reconcilerStub struct {
	nodes        []sqlc.RuntimeNode
	updatedNodes []string
}

func newReconcilerStub() *reconcilerStub {
	return &reconcilerStub{}
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

// SetAppStatus 为接口要求保留，spec-A2b 的 markRunningAppsAsError 为 no-op，此方法不应被调用。
func (s *reconcilerStub) SetAppStatus(_ context.Context, _ sqlc.SetAppStatusParams) error {
	return nil
}

// TestNodeHealthReconciler_DemotesTimedOutNodes 验证心跳超时节点被推到 unreachable。
// spec-A2b：markRunningAppsAsError 为 no-op，节点超时不再联动推应用到 error；
// k8s 路径下 pod 崩溃感知由 AppStatusReconciler 通过 orch.Status 负责。
func TestNodeHealthReconciler_DemotesTimedOutNodes(t *testing.T) {
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

	rec := NewNodeHealthReconciler(stub, 90*time.Second)
	rec.SetClock(func() time.Time { return now })
	demoted, err := rec.Reconcile(context.Background())
	require.NoError(t, err)
	// 超时节点被推到 unreachable，健康节点跳过
	require.Equal(t, 1, demoted)
	if len(stub.updatedNodes) != 1 || stub.updatedNodes[0] != testReconcilerNodeID+"="+domain.RuntimeNodeStatusUnreachable {
		t.Fatalf("updatedNodes = %+v", stub.updatedNodes)
	}
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
