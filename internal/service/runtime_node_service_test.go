package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/store/sqlc"
)

func TestRuntimeNodeServiceCreateRequiresPlatformAdmin(t *testing.T) {
	svc := newRuntimeNodeServiceForTest(t, nil)

	_, err := svc.CreateNode(context.Background(), auth.Principal{Role: domain.UserRoleOrgAdmin}, RuntimeNodeInput{Name: "node-1"})
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("CreateNode() error = %v, want ErrForbidden", err)
	}
}

func TestRuntimeNodeServiceCreateReturnsBootstrapToken(t *testing.T) {
	store := newRuntimeNodeStoreStub(t)
	svc := newRuntimeNodeServiceForTest(t, store)

	result, err := svc.CreateNode(context.Background(), platformAdmin(), RuntimeNodeInput{Name: "node-1"})
	if err != nil {
		t.Fatalf("CreateNode() error = %v", err)
	}
	if result.BootstrapToken == "" {
		t.Fatalf("expected bootstrap token in result")
	}
	if store.lastCreate.BootstrapTokenHash.String == result.BootstrapToken {
		t.Fatalf("bootstrap token should be hashed before persistence")
	}
	if !store.lastCreate.BootstrapTokenExpiresAt.Valid {
		t.Fatalf("expected expiration to be set")
	}
}

func TestRuntimeNodeServiceRegisterAgentSwapsTokens(t *testing.T) {
	store := newRuntimeNodeStoreStub(t)
	svc := newRuntimeNodeServiceForTest(t, store)

	created, err := svc.CreateNode(context.Background(), platformAdmin(), RuntimeNodeInput{Name: "node-1"})
	if err != nil {
		t.Fatalf("CreateNode() error = %v", err)
	}

	result, err := svc.RegisterAgent(context.Background(), AgentRegisterInput{
		BootstrapToken:      created.BootstrapToken,
		AgentDockerEndpoint: "tcp://127.0.0.1:2375",
		AgentFileEndpoint:   "https://127.0.0.1:8443",
		AgentVersion:        "0.1.0",
	})
	if err != nil {
		t.Fatalf("RegisterAgent() error = %v", err)
	}
	if result.AgentToken == "" {
		t.Fatalf("expected agent token")
	}
	node := store.findByName(t, "node-1")
	if node.BootstrapTokenHash.Valid {
		t.Fatalf("bootstrap token hash should be cleared after registration")
	}
	if !node.AgentTokenHash.Valid {
		t.Fatalf("agent token hash should be set after registration")
	}
}

func TestRuntimeNodeServiceRegisterAgentRejectsReusedBootstrap(t *testing.T) {
	store := newRuntimeNodeStoreStub(t)
	svc := newRuntimeNodeServiceForTest(t, store)

	created, err := svc.CreateNode(context.Background(), platformAdmin(), RuntimeNodeInput{Name: "node-1"})
	if err != nil {
		t.Fatalf("CreateNode() error = %v", err)
	}
	if _, err := svc.RegisterAgent(context.Background(), AgentRegisterInput{BootstrapToken: created.BootstrapToken}); err != nil {
		t.Fatalf("first RegisterAgent() error = %v", err)
	}

	_, err = svc.RegisterAgent(context.Background(), AgentRegisterInput{BootstrapToken: created.BootstrapToken})
	if !errors.Is(err, ErrBootstrapTokenInvalid) {
		t.Fatalf("second RegisterAgent() error = %v, want ErrBootstrapTokenInvalid", err)
	}
}

func TestRuntimeNodeServiceRegisterAgentRejectsExpiredBootstrap(t *testing.T) {
	store := newRuntimeNodeStoreStub(t)
	now := time.Now()
	svc := newRuntimeNodeServiceForTest(t, store)
	svc.now = func() time.Time { return now }

	if _, err := svc.CreateNode(context.Background(), platformAdmin(), RuntimeNodeInput{Name: "node-1"}); err != nil {
		t.Fatalf("CreateNode() error = %v", err)
	}
	// 模拟 1 小时后再来注册，bootstrap 默认 30 分钟必然过期。
	svc.now = func() time.Time { return now.Add(time.Hour) }

	_, err := svc.RegisterAgent(context.Background(), AgentRegisterInput{BootstrapToken: testBootstrapToken})
	if !errors.Is(err, ErrBootstrapTokenInvalid) {
		t.Fatalf("RegisterAgent() error = %v, want ErrBootstrapTokenInvalid", err)
	}
}

func TestRuntimeNodeServiceRegisterAgentRejectsInvalidToken(t *testing.T) {
	store := newRuntimeNodeStoreStub(t)
	svc := newRuntimeNodeServiceForTest(t, store)

	_, err := svc.RegisterAgent(context.Background(), AgentRegisterInput{BootstrapToken: "not-a-real-token"})
	if !errors.Is(err, ErrBootstrapTokenInvalid) {
		t.Fatalf("RegisterAgent() error = %v, want ErrBootstrapTokenInvalid", err)
	}
}

func TestRuntimeNodeServiceHeartbeatRequiresValidAgentToken(t *testing.T) {
	store := newRuntimeNodeStoreStub(t)
	svc := newRuntimeNodeServiceForTest(t, store)

	if _, err := svc.HandleHeartbeat(context.Background(), AgentHeartbeatInput{AgentToken: ""}); !errors.Is(err, ErrAgentTokenInvalid) {
		t.Fatalf("expected ErrAgentTokenInvalid for empty token, got %v", err)
	}
	if _, err := svc.HandleHeartbeat(context.Background(), AgentHeartbeatInput{AgentToken: "missing"}); !errors.Is(err, ErrAgentTokenInvalid) {
		t.Fatalf("expected ErrAgentTokenInvalid for missing token, got %v", err)
	}
}

func TestRuntimeNodeServiceHeartbeatUpdatesActiveNode(t *testing.T) {
	store := newRuntimeNodeStoreStub(t)
	svc := newRuntimeNodeServiceForTest(t, store)

	created, err := svc.CreateNode(context.Background(), platformAdmin(), RuntimeNodeInput{Name: "node-1"})
	if err != nil {
		t.Fatalf("CreateNode() error = %v", err)
	}
	registered, err := svc.RegisterAgent(context.Background(), AgentRegisterInput{BootstrapToken: created.BootstrapToken})
	if err != nil {
		t.Fatalf("RegisterAgent() error = %v", err)
	}

	result, err := svc.HandleHeartbeat(context.Background(), AgentHeartbeatInput{AgentToken: registered.AgentToken, AgentVersion: "0.2.0"})
	if err != nil {
		t.Fatalf("HandleHeartbeat() error = %v", err)
	}
	if result.AgentVersion != "0.2.0" {
		t.Fatalf("agent_version = %q, want updated", result.AgentVersion)
	}
}

func TestRuntimeNodeServiceHeartbeatRejectsDisabledNode(t *testing.T) {
	store := newRuntimeNodeStoreStub(t)
	svc := newRuntimeNodeServiceForTest(t, store)

	created, err := svc.CreateNode(context.Background(), platformAdmin(), RuntimeNodeInput{Name: "node-1"})
	if err != nil {
		t.Fatalf("CreateNode() error = %v", err)
	}
	registered, err := svc.RegisterAgent(context.Background(), AgentRegisterInput{BootstrapToken: created.BootstrapToken})
	if err != nil {
		t.Fatalf("RegisterAgent() error = %v", err)
	}
	if _, err := svc.SetNodeStatus(context.Background(), platformAdmin(), uuidToString(store.findByName(t, "node-1").ID), domain.RuntimeNodeStatusDisabled); err != nil {
		t.Fatalf("SetNodeStatus() error = %v", err)
	}

	if _, err := svc.HandleHeartbeat(context.Background(), AgentHeartbeatInput{AgentToken: registered.AgentToken}); !errors.Is(err, ErrAgentTokenInvalid) {
		t.Fatalf("HandleHeartbeat() error = %v, want ErrAgentTokenInvalid", err)
	}
}

func TestRuntimeNodeServiceRotateBootstrapBlockedForActive(t *testing.T) {
	store := newRuntimeNodeStoreStub(t)
	svc := newRuntimeNodeServiceForTest(t, store)

	created, err := svc.CreateNode(context.Background(), platformAdmin(), RuntimeNodeInput{Name: "node-1"})
	if err != nil {
		t.Fatalf("CreateNode() error = %v", err)
	}
	if _, err := svc.RegisterAgent(context.Background(), AgentRegisterInput{BootstrapToken: created.BootstrapToken}); err != nil {
		t.Fatalf("RegisterAgent() error = %v", err)
	}
	nodeID := uuidToString(store.findByName(t, "node-1").ID)

	if _, err := svc.RotateBootstrap(context.Background(), platformAdmin(), nodeID); !errors.Is(err, ErrRuntimeNodeBusy) {
		t.Fatalf("RotateBootstrap() error = %v, want ErrRuntimeNodeBusy", err)
	}
}

func TestRuntimeNodeServiceRotateBootstrapAllowedForPending(t *testing.T) {
	store := newRuntimeNodeStoreStub(t)
	svc := newRuntimeNodeServiceForTest(t, store)

	created, err := svc.CreateNode(context.Background(), platformAdmin(), RuntimeNodeInput{Name: "node-1"})
	if err != nil {
		t.Fatalf("CreateNode() error = %v", err)
	}
	nodeID := uuidToString(store.findByName(t, "node-1").ID)
	rotated, err := svc.RotateBootstrap(context.Background(), platformAdmin(), nodeID)
	if err != nil {
		t.Fatalf("RotateBootstrap() error = %v", err)
	}
	if rotated.BootstrapToken == "" || rotated.BootstrapToken == created.BootstrapToken {
		t.Fatalf("expected fresh bootstrap token, got %q", rotated.BootstrapToken)
	}
}

const (
	testBootstrapToken = "bootstrap-token-aaaa"
	testAgentToken     = "agent-token-bbbb"
)

func newRuntimeNodeServiceForTest(t *testing.T, store *runtimeNodeStoreStub) *RuntimeNodeService {
	t.Helper()
	if store == nil {
		store = newRuntimeNodeStoreStub(t)
	}
	tokens := []string{testBootstrapToken, "bootstrap-token-rotated"}
	bootstrapIdx := 0
	svc := NewRuntimeNodeService(store, fakeTokenHasher)
	svc.generateBootstrap = func() (string, error) {
		idx := bootstrapIdx
		if idx >= len(tokens) {
			idx = len(tokens) - 1
		}
		bootstrapIdx++
		return tokens[idx], nil
	}
	svc.generateAgent = func() (string, error) { return testAgentToken, nil }
	return svc
}

func fakeTokenHasher(token string) string { return "hashed:" + token }

type runtimeNodeStoreStub struct {
	t              *testing.T
	nodes          map[string]sqlc.RuntimeNode
	lastCreate     sqlc.CreateRuntimeNodeParams
	lastHeartbeat  sqlc.UpdateRuntimeNodeHeartbeatParams
}

func newRuntimeNodeStoreStub(t *testing.T) *runtimeNodeStoreStub {
	t.Helper()
	return &runtimeNodeStoreStub{t: t, nodes: map[string]sqlc.RuntimeNode{}}
}

func (s *runtimeNodeStoreStub) CreateRuntimeNode(_ context.Context, arg sqlc.CreateRuntimeNodeParams) (sqlc.RuntimeNode, error) {
	s.lastCreate = arg
	id := mustUUID(s.t, "00000000-0000-0000-0000-000000000c01")
	node := sqlc.RuntimeNode{
		ID:                       id,
		Name:                     arg.Name,
		Status:                   arg.Status,
		BootstrapTokenHash:       arg.BootstrapTokenHash,
		BootstrapTokenExpiresAt:  arg.BootstrapTokenExpiresAt,
		HeartbeatIntervalSeconds: arg.HeartbeatIntervalSeconds,
		NodeDataRoot:             arg.NodeDataRoot,
	}
	s.nodes[uuidToString(id)] = node
	return node, nil
}

func (s *runtimeNodeStoreStub) GetRuntimeNode(_ context.Context, id pgtype.UUID) (sqlc.RuntimeNode, error) {
	node, ok := s.nodes[uuidToString(id)]
	if !ok {
		return sqlc.RuntimeNode{}, pgx.ErrNoRows
	}
	return node, nil
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

func (s *runtimeNodeStoreStub) RegisterRuntimeNode(_ context.Context, arg sqlc.RegisterRuntimeNodeParams) (sqlc.RuntimeNode, error) {
	node, ok := s.nodes[uuidToString(arg.ID)]
	if !ok {
		return sqlc.RuntimeNode{}, pgx.ErrNoRows
	}
	node.Status = domain.RuntimeNodeStatusActive
	node.AgentDockerEndpoint = arg.AgentDockerEndpoint
	node.AgentFileEndpoint = arg.AgentFileEndpoint
	node.AgentTlsCaCert = arg.AgentTlsCaCert
	node.AgentTokenHash = arg.AgentTokenHash
	node.BootstrapTokenHash = pgtype.Text{}
	node.BootstrapTokenExpiresAt = pgtype.Timestamptz{}
	node.AgentVersion = arg.AgentVersion
	node.NodeDataRoot = arg.NodeDataRoot
	node.ResourceSnapshotJson = arg.ResourceSnapshotJson
	node.MetadataJson = arg.MetadataJson
	s.nodes[uuidToString(arg.ID)] = node
	return node, nil
}

func (s *runtimeNodeStoreStub) UpdateRuntimeNodeHeartbeat(_ context.Context, arg sqlc.UpdateRuntimeNodeHeartbeatParams) (sqlc.RuntimeNode, error) {
	s.lastHeartbeat = arg
	node, ok := s.nodes[uuidToString(arg.ID)]
	if !ok {
		return sqlc.RuntimeNode{}, pgx.ErrNoRows
	}
	node.Status = domain.RuntimeNodeStatusActive
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

func (s *runtimeNodeStoreStub) RotateBootstrapToken(_ context.Context, arg RotateBootstrapTokenParams) (sqlc.RuntimeNode, error) {
	node, ok := s.nodes[uuidToString(arg.ID)]
	if !ok {
		return sqlc.RuntimeNode{}, pgx.ErrNoRows
	}
	node.BootstrapTokenHash = pgtype.Text{String: arg.BootstrapTokenHash, Valid: true}
	node.BootstrapTokenExpiresAt = pgtype.Timestamptz{Time: arg.BootstrapTokenExpiresAt, Valid: true}
	s.nodes[uuidToString(arg.ID)] = node
	return node, nil
}

func (s *runtimeNodeStoreStub) findByName(t *testing.T, name string) sqlc.RuntimeNode {
	t.Helper()
	for _, node := range s.nodes {
		if node.Name == name {
			return node
		}
	}
	t.Fatalf("node %q not found", name)
	return sqlc.RuntimeNode{}
}
