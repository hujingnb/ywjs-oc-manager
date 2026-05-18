// Package service 的 probe_reconciler_test 覆盖运行节点探测调和器的状态更新和错误记录。
package service

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"

	"oc-manager/internal/domain"
	agentint "oc-manager/internal/integrations/agent"
	"oc-manager/internal/store/sqlc"
)

// TestRuntimeNodeProbeReconcilerFailureDegradesActiveNode 验证运行时节点探测调和器失败Degrades启用节点的预期行为场景。
func TestRuntimeNodeProbeReconcilerFailureDegradesActiveNode(t *testing.T) {
	store := newProbeStoreStub(t)
	node := probeNode(t, domain.RuntimeNodeStatusActive)
	store.nodes[uuidToString(node.ID)] = node
	rec := NewRuntimeNodeProbeReconciler(store, probeTokenResolver{"node-token"}, probeClientStub{result: agentint.ProbeResult{OK: false, Error: "dial refused"}}, RuntimeNodeProbeConfig{FailureThreshold: 1, RecoveryThreshold: 2})

	checked, err := rec.Reconcile(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, checked)

	updated := store.nodes[uuidToString(node.ID)]
	require.Equal(t, domain.RuntimeNodeStatusDegraded, updated.Status)
	require.Equal(t, int32(1), updated.ProbeFailureStreak)
	require.True(t, store.audited("node_probe_degraded"))
	// 探测降级审计详情包含状态切换：active → degraded。
	require.Len(t, store.auditLogs, 1)
	require.True(t, store.auditLogs[0].DetailMessage.Valid)
	require.Equal(t, "状态：active → degraded", store.auditLogs[0].DetailMessage.String)
}

// TestRuntimeNodeProbeReconcilerSuccessRecoversDegradedNode 验证运行时节点探测调和器成功Recovers降级节点的成功路径场景。
func TestRuntimeNodeProbeReconcilerSuccessRecoversDegradedNode(t *testing.T) {
	store := newProbeStoreStub(t)
	node := probeNode(t, domain.RuntimeNodeStatusDegraded)
	store.nodes[uuidToString(node.ID)] = node
	rec := NewRuntimeNodeProbeReconciler(store, probeTokenResolver{"node-token"}, probeClientStub{result: agentint.ProbeResult{OK: true}}, RuntimeNodeProbeConfig{FailureThreshold: 2, RecoveryThreshold: 1})

	checked, err := rec.Reconcile(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, checked)

	updated := store.nodes[uuidToString(node.ID)]
	require.Equal(t, domain.RuntimeNodeStatusActive, updated.Status)
	require.Equal(t, int32(1), updated.ProbeSuccessStreak)
	require.True(t, store.audited("node_probe_recovered"))
	// 探测恢复审计详情包含状态切换：degraded → active。
	require.Len(t, store.auditLogs, 1)
	require.True(t, store.auditLogs[0].DetailMessage.Valid)
	require.Equal(t, "状态：degraded → active", store.auditLogs[0].DetailMessage.String)
}

// TestRuntimeNodeProbeReconcilerSkipsDisabledNode 验证运行时节点探测调和器跳过禁用节点的特殊分支或幂等场景。
func TestRuntimeNodeProbeReconcilerSkipsDisabledNode(t *testing.T) {
	store := newProbeStoreStub(t)
	node := probeNode(t, domain.RuntimeNodeStatusDisabled)
	store.nodes[uuidToString(node.ID)] = node
	rec := NewRuntimeNodeProbeReconciler(store, probeTokenResolver{"node-token"}, probeClientStub{result: agentint.ProbeResult{OK: false, Error: "boom"}}, RuntimeNodeProbeConfig{})

	checked, err := rec.Reconcile(context.Background())
	require.NoError(t, err)
	require.Equal(t, 0, checked)
	require.Equal(t, domain.RuntimeNodeStatusDisabled, store.nodes[uuidToString(node.ID)].Status)
}

func probeNode(t *testing.T, status string) sqlc.RuntimeNode {
	t.Helper()
	return sqlc.RuntimeNode{
		ID:                  mustUUID(t, "00000000-0000-0000-0000-000000009001"),
		Name:                "node-1",
		Status:              status,
		AgentDockerEndpoint: pgtype.Text{String: "https://node-1.example:7001", Valid: true},
		AgentFileEndpoint:   pgtype.Text{String: "https://node-1.example:7002", Valid: true},
		AgentTlsCaCert:      pgtype.Text{String: "-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----\n", Valid: true},
	}
}

type probeTokenResolver struct {
	token string
}

func (r probeTokenResolver) Get(string) (string, error) { return r.token, nil }

type probeClientStub struct {
	result agentint.ProbeResult
}

func (c probeClientStub) Probe(context.Context, string, string, string, string) agentint.ProbeResult {
	return c.result
}

type probeStoreStub struct {
	t         *testing.T
	nodes     map[string]sqlc.RuntimeNode
	auditLogs []sqlc.CreateAuditLogParams
}

func newProbeStoreStub(t *testing.T) *probeStoreStub {
	t.Helper()
	return &probeStoreStub{t: t, nodes: map[string]sqlc.RuntimeNode{}}
}

func (s *probeStoreStub) ListRuntimeNodes(context.Context, sqlc.ListRuntimeNodesParams) ([]sqlc.RuntimeNode, error) {
	results := make([]sqlc.RuntimeNode, 0, len(s.nodes))
	for _, node := range s.nodes {
		results = append(results, node)
	}
	return results, nil
}

func (s *probeStoreStub) UpdateRuntimeNodeProbeSuccess(_ context.Context, arg sqlc.UpdateRuntimeNodeProbeSuccessParams) (sqlc.RuntimeNode, error) {
	node := s.nodes[uuidToString(arg.ID)]
	if node.Status == domain.RuntimeNodeStatusDegraded && node.ProbeSuccessStreak+1 >= arg.ProbeSuccessStreak {
		node.Status = domain.RuntimeNodeStatusActive
	}
	node.ProbeSuccessStreak++
	node.ProbeFailureStreak = 0
	node.LastProbeError = pgtype.Text{}
	s.nodes[uuidToString(arg.ID)] = node
	return node, nil
}

func (s *probeStoreStub) UpdateRuntimeNodeProbeFailure(_ context.Context, arg sqlc.UpdateRuntimeNodeProbeFailureParams) (sqlc.RuntimeNode, error) {
	node := s.nodes[uuidToString(arg.ID)]
	if node.Status == domain.RuntimeNodeStatusActive && node.ProbeFailureStreak+1 >= arg.ProbeFailureStreak {
		node.Status = domain.RuntimeNodeStatusDegraded
	}
	node.ProbeFailureStreak++
	node.ProbeSuccessStreak = 0
	node.LastProbeError = arg.LastProbeError
	s.nodes[uuidToString(arg.ID)] = node
	return node, nil
}

func (s *probeStoreStub) CreateAuditLog(_ context.Context, arg sqlc.CreateAuditLogParams) (sqlc.AuditLog, error) {
	s.auditLogs = append(s.auditLogs, arg)
	return sqlc.AuditLog{}, nil
}

func (s *probeStoreStub) audited(action string) bool {
	for _, log := range s.auditLogs {
		if log.Action == action {
			return true
		}
	}
	return false
}
