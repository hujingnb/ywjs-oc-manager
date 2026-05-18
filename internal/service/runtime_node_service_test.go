// Package service 的 runtime_node_service_test 覆盖运行节点注册、心跳和管理操作的服务边界。
package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/store/sqlc"
)

const testAgentToken = "agent-token-bbbb"

// TestRuntimeNodeServiceEnrollAgentCreatesNode 验证运行时节点服务注册agent创建节点的成功路径场景。
func TestRuntimeNodeServiceEnrollAgentCreatesNode(t *testing.T) {
	store := newRuntimeNodeStoreStub(t)
	svc := newRuntimeNodeServiceForTest(t, store)

	result, err := svc.EnrollAgent(context.Background(), validEnrollInput())
	require.NoError(t, err)
	require.Equal(t, testAgentToken, result.AgentToken)
	require.Equal(t, int32(30), result.HeartbeatIntervalSeconds)
	require.NotEmpty(t, result.NodeID)
	require.True(t, store.audited("agent_enrolled"))
	require.Equal(t, "system", store.auditLogs[0].ActorRole)

	node := store.findByAgentID(t, validEnrollInput().AgentID)
	require.Equal(t, domain.RuntimeNodeStatusActive, node.Status)
	require.Equal(t, fakeTokenHasher(testAgentToken), node.AgentTokenHash.String)
	require.Equal(t, "https://node-1.example:7001", node.AgentDockerEndpoint.String)
	require.True(t, node.MaxApps.Valid)
	require.Equal(t, int32(3), node.MaxApps.Int32)

	// agent_enrolled 审计详情应为「Agent 版本 <version>」。
	require.True(t, store.auditLogs[0].DetailMessage.Valid)
	require.Equal(t, "Agent 版本 0.1.0", store.auditLogs[0].DetailMessage.String)
}

// TestRuntimeNodeServiceEnrollAgentUpdatesExistingNode 验证运行时节点服务注册agentUpdates已有节点的预期行为场景。
func TestRuntimeNodeServiceEnrollAgentUpdatesExistingNode(t *testing.T) {
	store := newRuntimeNodeStoreStub(t)
	svc := newRuntimeNodeServiceForTest(t, store)

	first, err := svc.EnrollAgent(context.Background(), validEnrollInput())
	require.NoError(t, err)
	input := validEnrollInput()
	input.Name = "renamed-node"
	input.AgentVersion = "0.2.0"
	input.AgentFileEndpoint = "https://node-1.example:7443"
	input.MaxApps = int32PtrService(5)

	second, err := svc.EnrollAgent(context.Background(), input)
	require.NoError(t, err)
	require.Equal(t, first.NodeID, second.NodeID)
	require.True(t, store.audited("agent_re_enrolled"))

	node := store.findByAgentID(t, input.AgentID)
	require.Equal(t, "renamed-node", node.Name)
	require.Equal(t, "0.2.0", node.AgentVersion.String)
	require.Equal(t, "https://node-1.example:7443", node.AgentFileEndpoint.String)
	require.Equal(t, domain.RuntimeNodeStatusActive, node.Status)
	require.True(t, node.MaxApps.Valid)
	require.Equal(t, int32(5), node.MaxApps.Int32)

	// re_enroll 审计详情应反映新版本。
	require.True(t, store.auditLogs[1].DetailMessage.Valid)
	require.Equal(t, "Agent 版本 0.2.0", store.auditLogs[1].DetailMessage.String)
}

// TestRuntimeNodeServiceEnrollAgentPersistsNodeResourceSample 验证agent注册携带节点资源时保留首次采样。
func TestRuntimeNodeServiceEnrollAgentPersistsNodeResourceSample(t *testing.T) {
	store := newRuntimeNodeStoreStub(t)
	svc := newRuntimeNodeServiceForTest(t, store)
	sampledAt := mustTime(t, "2026-05-13T08:00:00Z")
	input := validEnrollInput()
	input.SampledAt = sampledAt
	input.NodeResource = &NodeResourceInput{
		CPUPercent:       float64PtrService(11.5),
		MemoryUsedBytes:  int64PtrService(2048),
		MemoryTotalBytes: int64PtrService(8192),
		DiskUsedBytes:    int64PtrService(4096),
		DiskTotalBytes:   int64PtrService(16384),
		NetworkRxBytes:   int64PtrService(700),
		NetworkTxBytes:   int64PtrService(600),
		InstanceCount:    int32PtrService(2),
	}

	result, err := svc.EnrollAgent(context.Background(), input)
	require.NoError(t, err)
	require.Len(t, store.nodeSamples, 1)
	sample := store.nodeSamples[0]
	assert.Equal(t, mustUUID(t, result.NodeID), sample.RuntimeNodeID)
	assert.Equal(t, sampledAt, sample.SampledAt.Time)
	assert.Equal(t, 11.5, sample.CpuPercent.Float64)
	assert.Equal(t, int64(2048), sample.MemoryUsedBytes.Int64)
	assert.Equal(t, int32(2), sample.InstanceCount.Int32)
}

// TestRuntimeNodeServiceEnrollAgentRejectsInvalidInput 验证运行时节点服务注册agent拒绝非法输入的异常或拒绝路径场景。
func TestRuntimeNodeServiceEnrollAgentRejectsInvalidInput(t *testing.T) {
	store := newRuntimeNodeStoreStub(t)
	svc := newRuntimeNodeServiceForTest(t, store)

	input := validEnrollInput()
	input.AgentDockerEndpoint = "http://node-1.example:7001"
	_, err := svc.EnrollAgent(context.Background(), input)
	require.ErrorIs(t, err, ErrEnrollInputInvalid)
}

// TestRuntimeNodeServiceEnrollAgentRejectsNegativeMaxApps 验证运行时节点服务注册agent拒绝负数最大应用的异常或拒绝路径场景。
func TestRuntimeNodeServiceEnrollAgentRejectsNegativeMaxApps(t *testing.T) {
	store := newRuntimeNodeStoreStub(t)
	svc := newRuntimeNodeServiceForTest(t, store)

	input := validEnrollInput()
	input.MaxApps = int32PtrService(-1)
	_, err := svc.EnrollAgent(context.Background(), input)
	require.ErrorIs(t, err, ErrEnrollInputInvalid)
}

// TestRuntimeNodeServiceHeartbeatRequiresValidAgentToken 验证运行时节点服务心跳要求合法agent令牌的预期行为场景。
func TestRuntimeNodeServiceHeartbeatRequiresValidAgentToken(t *testing.T) {
	store := newRuntimeNodeStoreStub(t)
	svc := newRuntimeNodeServiceForTest(t, store)

	_, err := svc.HandleHeartbeat(context.Background(), AgentHeartbeatInput{AgentToken: ""})
	require.ErrorIs(t, err, ErrAgentTokenInvalid)

	_, err = svc.HandleHeartbeat(context.Background(), AgentHeartbeatInput{AgentToken: "missing"})
	require.ErrorIs(t, err, ErrAgentTokenInvalid)
}

// TestRuntimeNodeServiceHeartbeatUpdatesActiveNode 验证运行时节点服务心跳Updates启用节点的预期行为场景。
func TestRuntimeNodeServiceHeartbeatUpdatesActiveNode(t *testing.T) {
	store := newRuntimeNodeStoreStub(t)
	svc := newRuntimeNodeServiceForTest(t, store)
	enrolled, err := svc.EnrollAgent(context.Background(), validEnrollInput())
	require.NoError(t, err)

	result, err := svc.HandleHeartbeat(context.Background(), AgentHeartbeatInput{AgentToken: enrolled.AgentToken, AgentVersion: "0.2.0"})
	require.NoError(t, err)
	require.Equal(t, "0.2.0", result.AgentVersion)
}

// TestAgentHeartbeatPersistsNodeResourceSample 验证agent心跳携带节点资源时写入节点采样表。
func TestAgentHeartbeatPersistsNodeResourceSample(t *testing.T) {
	store := newRuntimeNodeStoreStub(t)
	svc := newRuntimeNodeServiceForTest(t, store)
	enrolled, err := svc.EnrollAgent(context.Background(), validEnrollInput())
	require.NoError(t, err)
	sampledAt := mustTime(t, "2026-05-13T12:34:56Z")

	_, err = svc.HandleHeartbeat(context.Background(), AgentHeartbeatInput{
		AgentToken: enrolled.AgentToken,
		SampledAt:  sampledAt,
		NodeResource: &NodeResourceInput{
			CPUPercent:       float64PtrService(42.5),
			MemoryUsedBytes:  int64PtrService(1024),
			MemoryTotalBytes: int64PtrService(4096),
			DiskUsedBytes:    int64PtrService(2048),
			DiskTotalBytes:   int64PtrService(8192),
			NetworkRxBytes:   int64PtrService(300),
			NetworkTxBytes:   int64PtrService(200),
			InstanceCount:    int32PtrService(3),
		},
	})
	require.NoError(t, err)
	require.Len(t, store.nodeSamples, 1)
	sample := store.nodeSamples[0]
	assert.Equal(t, mustUUID(t, enrolled.NodeID), sample.RuntimeNodeID)
	assert.Equal(t, sampledAt, sample.SampledAt.Time)
	assert.Equal(t, 42.5, sample.CpuPercent.Float64)
	assert.Equal(t, int64(1024), sample.MemoryUsedBytes.Int64)
	assert.Equal(t, int32(3), sample.InstanceCount.Int32)
}

// TestRuntimeNodeServiceHeartbeatKeepsDegradedStatus 验证运行时节点服务心跳保留降级状态的预期行为场景。
func TestRuntimeNodeServiceHeartbeatKeepsDegradedStatus(t *testing.T) {
	store := newRuntimeNodeStoreStub(t)
	svc := newRuntimeNodeServiceForTest(t, store)
	enrolled, err := svc.EnrollAgent(context.Background(), validEnrollInput())
	require.NoError(t, err)
	node := store.findByAgentID(t, validEnrollInput().AgentID)
	node.Status = domain.RuntimeNodeStatusDegraded
	store.nodes[uuidToString(node.ID)] = node

	result, err := svc.HandleHeartbeat(context.Background(), AgentHeartbeatInput{AgentToken: enrolled.AgentToken})
	require.NoError(t, err)
	require.Equal(t, domain.RuntimeNodeStatusDegraded, result.Status)
}

// TestRuntimeNodeServiceHeartbeatRejectsDisabledNode 验证运行时节点服务心跳拒绝禁用节点的异常或拒绝路径场景。
func TestRuntimeNodeServiceHeartbeatRejectsDisabledNode(t *testing.T) {
	store := newRuntimeNodeStoreStub(t)
	svc := newRuntimeNodeServiceForTest(t, store)
	enrolled, err := svc.EnrollAgent(context.Background(), validEnrollInput())
	require.NoError(t, err)
	node := store.findByAgentID(t, validEnrollInput().AgentID)
	node.Status = domain.RuntimeNodeStatusDisabled
	store.nodes[uuidToString(node.ID)] = node

	_, err = svc.HandleHeartbeat(context.Background(), AgentHeartbeatInput{AgentToken: enrolled.AgentToken})
	require.ErrorIs(t, err, ErrAgentTokenInvalid)
}

// TestRuntimeNodeServiceListRequiresPlatformAdmin 验证运行时节点服务列表要求平台管理员的预期行为场景。
func TestRuntimeNodeServiceListRequiresPlatformAdmin(t *testing.T) {
	svc := newRuntimeNodeServiceForTest(t, nil)

	_, err := svc.ListNodes(context.Background(), auth.Principal{Role: domain.UserRoleOrgAdmin}, 10, 0)
	require.ErrorIs(t, err, ErrForbidden)
}

// TestRuntimeNodeServiceListNodesIncludesCurrentResource 验证运行节点列表带出每个节点最近一次资源采样摘要。
func TestRuntimeNodeServiceListNodesIncludesCurrentResource(t *testing.T) {
	store := newRuntimeNodeStoreStub(t)
	svc := newRuntimeNodeServiceForTest(t, store)
	enrolled, err := svc.EnrollAgent(context.Background(), validEnrollInput())
	require.NoError(t, err)
	nodeID := mustUUID(t, enrolled.NodeID)
	store.latestNodeSamples[enrolled.NodeID] = sqlc.NodeResourceSample{
		RuntimeNodeID: nodeID,
		SampledAt:     pgtype.Timestamptz{Time: mustTime(t, "2026-05-13T12:00:00Z"), Valid: true},
		CpuPercent:    pgtype.Float8{Float64: 42.5, Valid: true},
	}

	results, err := svc.ListNodes(context.Background(), auth.Principal{Role: domain.UserRolePlatformAdmin}, 10, 0)
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.NotNil(t, results[0].CurrentResource)
	require.NotNil(t, results[0].CurrentResource.CPUPercent)
	assert.Equal(t, 42.5, *results[0].CurrentResource.CPUPercent)
}

func validEnrollInput() AgentEnrollInput {
	return AgentEnrollInput{
		AgentID:             "00000000-0000-0000-0000-00000000a001",
		Name:                "node-1",
		AgentDockerEndpoint: "https://node-1.example:7001",
		AgentFileEndpoint:   "https://node-1.example:7002",
		AgentTLSCACert:      "-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----\n",
		AgentVersion:        "0.1.0",
		NodeDataRoot:        "/var/lib/oc-agent",
		MaxApps:             int32PtrService(3),
		ResourceSnapshot:    []byte(`{"cpu":1}`),
		Metadata:            []byte(`{"hostname":"node-1"}`),
	}
}

func int32PtrService(v int32) *int32 { return &v }

func int64PtrService(v int64) *int64 { return &v }

func float64PtrService(v float64) *float64 { return &v }

func newRuntimeNodeServiceForTest(t *testing.T, store *runtimeNodeStoreStub) *RuntimeNodeService {
	t.Helper()
	if store == nil {
		store = newRuntimeNodeStoreStub(t)
	}
	svc := NewRuntimeNodeService(store, fakeTokenHasher)
	svc.generateAgent = func() (string, error) { return testAgentToken, nil }
	return svc
}

func fakeTokenHasher(token string) string { return "hashed:" + token }

type runtimeNodeStoreStub struct {
	t                 *testing.T
	nodes             map[string]sqlc.RuntimeNode
	latestNodeSamples map[string]sqlc.NodeResourceSample
	nodeSamples       []sqlc.InsertNodeResourceSampleParams
	nextID            int
	lastHeartbeat     sqlc.UpdateRuntimeNodeHeartbeatParams
	auditLogs         []sqlc.CreateAuditLogParams
}

func newRuntimeNodeStoreStub(t *testing.T) *runtimeNodeStoreStub {
	t.Helper()
	return &runtimeNodeStoreStub{t: t, nodes: map[string]sqlc.RuntimeNode{}, latestNodeSamples: map[string]sqlc.NodeResourceSample{}, nextID: 1}
}

func (s *runtimeNodeStoreStub) EnrollRuntimeNodeInsert(_ context.Context, arg sqlc.EnrollRuntimeNodeInsertParams) (sqlc.RuntimeNode, error) {
	id := mustUUID(s.t, "00000000-0000-0000-0000-000000000c01")
	if s.nextID > 1 {
		id = mustUUID(s.t, "00000000-0000-0000-0000-000000000c02")
	}
	s.nextID++
	node := sqlc.RuntimeNode{
		ID:                       id,
		Name:                     arg.Name,
		Status:                   domain.RuntimeNodeStatusActive,
		AgentID:                  arg.AgentID,
		MaxApps:                  arg.MaxApps,
		AgentDockerEndpoint:      arg.AgentDockerEndpoint,
		AgentFileEndpoint:        arg.AgentFileEndpoint,
		AgentTlsCaCert:           arg.AgentTlsCaCert,
		AgentTokenHash:           arg.AgentTokenHash,
		AgentVersion:             arg.AgentVersion,
		HeartbeatIntervalSeconds: arg.HeartbeatIntervalSeconds,
		NodeDataRoot:             arg.NodeDataRoot,
		ResourceSnapshotJson:     arg.ResourceSnapshotJson,
		MetadataJson:             arg.MetadataJson,
		AgentTokenCiphertext:     arg.AgentTokenCiphertext,
	}
	s.nodes[uuidToString(id)] = node
	return node, nil
}

func (s *runtimeNodeStoreStub) EnrollRuntimeNodeUpdate(_ context.Context, arg sqlc.EnrollRuntimeNodeUpdateParams) (sqlc.RuntimeNode, error) {
	node, err := s.GetRuntimeNodeByAgentID(context.Background(), arg.AgentID)
	if err != nil {
		return sqlc.RuntimeNode{}, err
	}
	node.Status = domain.RuntimeNodeStatusActive
	node.Name = arg.Name
	node.MaxApps = arg.MaxApps
	node.AgentDockerEndpoint = arg.AgentDockerEndpoint
	node.AgentFileEndpoint = arg.AgentFileEndpoint
	node.AgentTlsCaCert = arg.AgentTlsCaCert
	node.AgentTokenHash = arg.AgentTokenHash
	node.AgentVersion = arg.AgentVersion
	node.NodeDataRoot = arg.NodeDataRoot
	node.ResourceSnapshotJson = arg.ResourceSnapshotJson
	node.MetadataJson = arg.MetadataJson
	node.AgentTokenCiphertext = arg.AgentTokenCiphertext
	node.ProbeFailureStreak = 0
	node.ProbeSuccessStreak = 0
	s.nodes[uuidToString(node.ID)] = node
	return node, nil
}

func (s *runtimeNodeStoreStub) GetRuntimeNode(_ context.Context, id pgtype.UUID) (sqlc.RuntimeNode, error) {
	node, ok := s.nodes[uuidToString(id)]
	if !ok {
		return sqlc.RuntimeNode{}, pgx.ErrNoRows
	}
	return node, nil
}

func (s *runtimeNodeStoreStub) GetRuntimeNodeByAgentID(_ context.Context, agentID pgtype.Text) (sqlc.RuntimeNode, error) {
	for _, node := range s.nodes {
		if node.AgentID.Valid && node.AgentID.String == agentID.String {
			return node, nil
		}
	}
	return sqlc.RuntimeNode{}, pgx.ErrNoRows
}

func (s *runtimeNodeStoreStub) GetRuntimeNodeByName(_ context.Context, name string) (sqlc.RuntimeNode, error) {
	for _, node := range s.nodes {
		if node.Name == name {
			return node, nil
		}
	}
	return sqlc.RuntimeNode{}, pgx.ErrNoRows
}

func (s *runtimeNodeStoreStub) ListRuntimeNodes(_ context.Context, _ sqlc.ListRuntimeNodesParams) ([]sqlc.RuntimeNode, error) {
	results := make([]sqlc.RuntimeNode, 0, len(s.nodes))
	for _, node := range s.nodes {
		results = append(results, node)
	}
	return results, nil
}

func (s *runtimeNodeStoreStub) ListLatestNodeResourceSamples(_ context.Context, ids []pgtype.UUID) ([]sqlc.NodeResourceSample, error) {
	results := make([]sqlc.NodeResourceSample, 0, len(ids))
	for _, id := range ids {
		if sample, ok := s.latestNodeSamples[uuidToString(id)]; ok {
			results = append(results, sample)
		}
	}
	return results, nil
}

func (s *runtimeNodeStoreStub) InsertNodeResourceSample(_ context.Context, arg sqlc.InsertNodeResourceSampleParams) (sqlc.NodeResourceSample, error) {
	s.nodeSamples = append(s.nodeSamples, arg)
	return sqlc.NodeResourceSample{RuntimeNodeID: arg.RuntimeNodeID, SampledAt: arg.SampledAt}, nil
}

func (s *runtimeNodeStoreStub) UpdateRuntimeNodeHeartbeat(_ context.Context, arg sqlc.UpdateRuntimeNodeHeartbeatParams) (sqlc.RuntimeNode, error) {
	s.lastHeartbeat = arg
	node, ok := s.nodes[uuidToString(arg.ID)]
	if !ok {
		return sqlc.RuntimeNode{}, pgx.ErrNoRows
	}
	if node.Status == domain.RuntimeNodeStatusUnreachable {
		node.Status = domain.RuntimeNodeStatusActive
	}
	node.AgentVersion = arg.AgentVersion
	node.ResourceSnapshotJson = arg.ResourceSnapshotJson
	node.MetadataJson = arg.MetadataJson
	s.nodes[uuidToString(arg.ID)] = node
	return node, nil
}

func (s *runtimeNodeStoreStub) SetRuntimeNodeStatus(_ context.Context, arg sqlc.SetRuntimeNodeStatusParams) (sqlc.RuntimeNode, error) {
	node, ok := s.nodes[uuidToString(arg.ID)]
	if !ok {
		return sqlc.RuntimeNode{}, pgx.ErrNoRows
	}
	node.Status = arg.Status
	s.nodes[uuidToString(arg.ID)] = node
	return node, nil
}

func (s *runtimeNodeStoreStub) CreateAuditLog(_ context.Context, arg sqlc.CreateAuditLogParams) (sqlc.AuditLog, error) {
	s.auditLogs = append(s.auditLogs, arg)
	return sqlc.AuditLog{}, nil
}

func (s *runtimeNodeStoreStub) audited(action string) bool {
	for _, l := range s.auditLogs {
		if l.Action == action {
			return true
		}
	}
	return false
}

func (s *runtimeNodeStoreStub) findByAgentID(t *testing.T, agentID string) sqlc.RuntimeNode {
	t.Helper()
	node, err := s.GetRuntimeNodeByAgentID(context.Background(), pgtype.Text{String: agentID, Valid: true})
	if errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("node agent_id=%q not found", agentID)
	}
	require.NoError(t, err)
	return node
}

func mustTime(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, value)
	require.NoError(t, err)
	return parsed
}
