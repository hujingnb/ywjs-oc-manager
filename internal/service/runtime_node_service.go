package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/store/sqlc"
)

// 默认安全参数。agent token 是 manager 与 runtime-agent 之间的长期通信凭证。
const (
	defaultAgentTokenLength  = 32
	defaultHeartbeatInterval = 30
)

// RuntimeNodeStore 抽象 runtime node 服务所需的数据访问能力。
type RuntimeNodeStore interface {
	EnrollRuntimeNodeInsert(ctx context.Context, arg sqlc.EnrollRuntimeNodeInsertParams) (sqlc.RuntimeNode, error)
	EnrollRuntimeNodeUpdate(ctx context.Context, arg sqlc.EnrollRuntimeNodeUpdateParams) (sqlc.RuntimeNode, error)
	GetRuntimeNode(ctx context.Context, id pgtype.UUID) (sqlc.RuntimeNode, error)
	GetRuntimeNodeByAgentID(ctx context.Context, agentID pgtype.Text) (sqlc.RuntimeNode, error)
	GetRuntimeNodeByName(ctx context.Context, name string) (sqlc.RuntimeNode, error)
	ListRuntimeNodes(ctx context.Context, arg sqlc.ListRuntimeNodesParams) ([]sqlc.RuntimeNode, error)
	UpdateRuntimeNodeHeartbeat(ctx context.Context, arg sqlc.UpdateRuntimeNodeHeartbeatParams) (sqlc.RuntimeNode, error)
	SetRuntimeNodeStatus(ctx context.Context, arg sqlc.SetRuntimeNodeStatusParams) (sqlc.RuntimeNode, error)
	CreateAuditLog(ctx context.Context, arg sqlc.CreateAuditLogParams) (sqlc.AuditLog, error)
}

// TokenHasher 将 agent token 单向 hash 后存库。
type TokenHasher func(string) string

// TokenGenerator 生成随机 token，测试中可替换为确定性序列。
type TokenGenerator func() (string, error)

// RuntimeNodeService 维护 runtime 节点生命周期。
//
// 节点由 agent 主动 enroll 自动创建；管理员后台只负责查看和启停。
// 容量上限由 agent.max_apps 定义，并在 agent enroll 时同步。
type RuntimeNodeService struct {
	store             RuntimeNodeStore
	hashToken         TokenHasher
	generateAgent     TokenGenerator
	heartbeatInterval int32
}

// NewRuntimeNodeService 创建 runtime node 服务。
func NewRuntimeNodeService(store RuntimeNodeStore, hash TokenHasher) *RuntimeNodeService {
	return &RuntimeNodeService{
		store:             store,
		hashToken:         hash,
		generateAgent:     func() (string, error) { return generateRandomToken(defaultAgentTokenLength) },
		heartbeatInterval: defaultHeartbeatInterval,
	}
}

// RuntimeNodeResult 是对外返回的节点视图。
type RuntimeNodeResult struct {
	ID                       string `json:"id"`
	Name                     string `json:"name"`
	Status                   string `json:"status"`
	AgentID                  string `json:"agent_id,omitempty"`
	AgentDockerEndpoint      string `json:"agent_docker_endpoint,omitempty"`
	AgentFileEndpoint        string `json:"agent_file_endpoint,omitempty"`
	AgentVersion             string `json:"agent_version,omitempty"`
	HeartbeatIntervalSeconds int32  `json:"heartbeat_interval_seconds"`
	NodeDataRoot             string `json:"node_data_root,omitempty"`
	HasAgentToken            bool   `json:"has_agent_token"`
	MaxApps                  *int32 `json:"max_apps,omitempty"`
	LastProbeAttemptedAt     string `json:"last_probe_attempted_at,omitempty"`
	LastProbeOKAt            string `json:"last_probe_ok_at,omitempty"`
	LastProbeFailedAt        string `json:"last_probe_failed_at,omitempty"`
	LastProbeError           string `json:"last_probe_error,omitempty"`
	ProbeFailureStreak       int32  `json:"probe_failure_streak"`
	ProbeSuccessStreak       int32  `json:"probe_success_streak"`
}

// AgentEnrollInput 是 agent 自动注册时提交的自描述信息。
type AgentEnrollInput struct {
	AgentID             string
	Name                string
	MaxApps             *int32
	AgentDockerEndpoint string
	AgentFileEndpoint   string
	AgentTLSCACert      string
	AgentVersion        string
	NodeDataRoot        string
	ResourceSnapshot    []byte
	Metadata            []byte
}

// AgentEnrollResult 是 enroll 成功后返回给 agent 的凭证。
type AgentEnrollResult struct {
	NodeID                   string `json:"node_id"`
	AgentToken               string `json:"agent_token"`
	HeartbeatIntervalSeconds int32  `json:"heartbeat_interval_seconds"`
}

// AgentHeartbeatInput 是 agent 上报心跳时提交的快照信息。
type AgentHeartbeatInput struct {
	AgentToken       string
	AgentVersion     string
	ResourceSnapshot []byte
	Metadata         []byte
}

// EnrollAgent 按 agent_id 幂等创建或刷新 runtime 节点，并签发新的 agent token。
func (s *RuntimeNodeService) EnrollAgent(ctx context.Context, input AgentEnrollInput) (AgentEnrollResult, error) {
	if err := validateEnrollInput(input); err != nil {
		return AgentEnrollResult{}, err
	}
	if input.MaxApps != nil && *input.MaxApps < 0 {
		return AgentEnrollResult{}, ErrEnrollInputInvalid
	}
	agentToken, err := s.generateAgent()
	if err != nil {
		return AgentEnrollResult{}, fmt.Errorf("生成 agent token 失败: %w", err)
	}
	tokenHash := pgtype.Text{String: s.hashToken(agentToken), Valid: true}
	agentID := pgtype.Text{String: strings.TrimSpace(input.AgentID), Valid: true}
	name := strings.TrimSpace(input.Name)
	if name == "" {
		name = "Runtime Node " + shortAgentID(input.AgentID)
	}

	_, findErr := s.store.GetRuntimeNodeByAgentID(ctx, agentID)
	var node sqlc.RuntimeNode
	action := "agent_re_enrolled"
	if errors.Is(findErr, pgx.ErrNoRows) {
		action = "agent_enrolled"
		node, err = s.store.EnrollRuntimeNodeInsert(ctx, sqlc.EnrollRuntimeNodeInsertParams{
			AgentID:                  agentID,
			Name:                     name,
			MaxApps:                  int32OrNull(input.MaxApps),
			AgentDockerEndpoint:      textOrNull(input.AgentDockerEndpoint),
			AgentFileEndpoint:        textOrNull(input.AgentFileEndpoint),
			AgentTlsCaCert:           textOrNull(input.AgentTLSCACert),
			AgentTokenHash:           tokenHash,
			HeartbeatIntervalSeconds: s.heartbeatInterval,
			AgentVersion:             textOrNull(input.AgentVersion),
			NodeDataRoot:             textOrNull(input.NodeDataRoot),
			ResourceSnapshotJson:     input.ResourceSnapshot,
			MetadataJson:             input.Metadata,
			AgentTokenCiphertext:     pgtype.Text{},
		})
	} else if findErr != nil {
		return AgentEnrollResult{}, fmt.Errorf("查询 runtime 节点失败: %w", findErr)
	} else {
		node, err = s.store.EnrollRuntimeNodeUpdate(ctx, sqlc.EnrollRuntimeNodeUpdateParams{
			AgentID:              agentID,
			Name:                 name,
			MaxApps:              int32OrNull(input.MaxApps),
			AgentDockerEndpoint:  textOrNull(input.AgentDockerEndpoint),
			AgentFileEndpoint:    textOrNull(input.AgentFileEndpoint),
			AgentTlsCaCert:       textOrNull(input.AgentTLSCACert),
			AgentTokenHash:       tokenHash,
			AgentVersion:         textOrNull(input.AgentVersion),
			NodeDataRoot:         textOrNull(input.NodeDataRoot),
			ResourceSnapshotJson: input.ResourceSnapshot,
			MetadataJson:         input.Metadata,
			AgentTokenCiphertext: pgtype.Text{},
		})
	}
	if err != nil {
		return AgentEnrollResult{}, fmt.Errorf("写入 runtime 节点失败: %w", err)
	}
	if _, err := s.store.CreateAuditLog(ctx, sqlc.CreateAuditLogParams{
		ActorRole:  "system",
		TargetType: "runtime_node",
		TargetID:   uuidToString(node.ID),
		Action:     action,
		Result:     "succeeded",
	}); err != nil {
		return AgentEnrollResult{}, fmt.Errorf("写审计失败: %w", err)
	}
	return AgentEnrollResult{
		NodeID:                   uuidToString(node.ID),
		AgentToken:               agentToken,
		HeartbeatIntervalSeconds: node.HeartbeatIntervalSeconds,
	}, nil
}

// ListNodes 列出 runtime 节点。
func (s *RuntimeNodeService) ListNodes(ctx context.Context, principal auth.Principal, limit, offset int32) ([]RuntimeNodeResult, error) {
	if principal.Role != domain.UserRolePlatformAdmin {
		return nil, ErrForbidden
	}
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	if offset < 0 {
		offset = 0
	}
	nodes, err := s.store.ListRuntimeNodes(ctx, sqlc.ListRuntimeNodesParams{Limit: limit, Offset: offset})
	if err != nil {
		return nil, fmt.Errorf("查询 runtime 节点失败: %w", err)
	}
	results := make([]RuntimeNodeResult, 0, len(nodes))
	for _, node := range nodes {
		results = append(results, toRuntimeNodeResult(node))
	}
	return results, nil
}

// GetNode 获取 runtime 节点详情。
func (s *RuntimeNodeService) GetNode(ctx context.Context, principal auth.Principal, nodeID string) (RuntimeNodeResult, error) {
	if principal.Role != domain.UserRolePlatformAdmin {
		return RuntimeNodeResult{}, ErrForbidden
	}
	id, err := parseUUID(nodeID)
	if err != nil {
		return RuntimeNodeResult{}, ErrNotFound
	}
	node, err := s.store.GetRuntimeNode(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return RuntimeNodeResult{}, ErrNotFound
	}
	if err != nil {
		return RuntimeNodeResult{}, fmt.Errorf("查询 runtime 节点失败: %w", err)
	}
	return toRuntimeNodeResult(node), nil
}

// SetNodeStatus 启用或禁用节点。
func (s *RuntimeNodeService) SetNodeStatus(ctx context.Context, principal auth.Principal, nodeID, status string) (RuntimeNodeResult, error) {
	if principal.Role != domain.UserRolePlatformAdmin {
		return RuntimeNodeResult{}, ErrForbidden
	}
	if status != domain.RuntimeNodeStatusActive && status != domain.RuntimeNodeStatusDisabled {
		return RuntimeNodeResult{}, fmt.Errorf("非法节点状态: %s", status)
	}
	id, err := parseUUID(nodeID)
	if err != nil {
		return RuntimeNodeResult{}, ErrNotFound
	}
	node, err := s.store.SetRuntimeNodeStatus(ctx, sqlc.SetRuntimeNodeStatusParams{ID: id, Status: status})
	if errors.Is(err, pgx.ErrNoRows) {
		return RuntimeNodeResult{}, ErrNotFound
	}
	if err != nil {
		return RuntimeNodeResult{}, fmt.Errorf("更新节点状态失败: %w", err)
	}
	return toRuntimeNodeResult(node), nil
}

// HandleHeartbeat 校验 agent token 并更新心跳与资源快照。
func (s *RuntimeNodeService) HandleHeartbeat(ctx context.Context, input AgentHeartbeatInput) (RuntimeNodeResult, error) {
	if input.AgentToken == "" {
		return RuntimeNodeResult{}, ErrAgentTokenInvalid
	}
	node, err := s.findNodeByAgentToken(ctx, s.hashToken(input.AgentToken))
	if err != nil {
		return RuntimeNodeResult{}, err
	}
	if node.Status == domain.RuntimeNodeStatusDisabled {
		return RuntimeNodeResult{}, ErrAgentTokenInvalid
	}
	updated, err := s.store.UpdateRuntimeNodeHeartbeat(ctx, sqlc.UpdateRuntimeNodeHeartbeatParams{
		ID:                   node.ID,
		AgentVersion:         textOrNull(input.AgentVersion),
		ResourceSnapshotJson: input.ResourceSnapshot,
		MetadataJson:         input.Metadata,
	})
	if err != nil {
		return RuntimeNodeResult{}, fmt.Errorf("更新心跳失败: %w", err)
	}
	return toRuntimeNodeResult(updated), nil
}

func (s *RuntimeNodeService) findNodeByAgentToken(ctx context.Context, hash string) (sqlc.RuntimeNode, error) {
	nodes, err := s.store.ListRuntimeNodes(ctx, sqlc.ListRuntimeNodesParams{Limit: 1000, Offset: 0})
	if err != nil {
		return sqlc.RuntimeNode{}, fmt.Errorf("查询 runtime 节点失败: %w", err)
	}
	for _, node := range nodes {
		if node.AgentTokenHash.Valid && node.AgentTokenHash.String == hash {
			return node, nil
		}
	}
	return sqlc.RuntimeNode{}, ErrAgentTokenInvalid
}

func validateEnrollInput(input AgentEnrollInput) error {
	if _, err := parseUUID(strings.TrimSpace(input.AgentID)); err != nil {
		return ErrEnrollInputInvalid
	}
	if err := validateHTTPSURL(input.AgentDockerEndpoint); err != nil {
		return ErrEnrollInputInvalid
	}
	if err := validateHTTPSURL(input.AgentFileEndpoint); err != nil {
		return ErrEnrollInputInvalid
	}
	if block, _ := pem.Decode([]byte(input.AgentTLSCACert)); block == nil {
		return ErrEnrollInputInvalid
	}
	return nil
}

func validateHTTPSURL(value string) error {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil {
		return err
	}
	if parsed.Scheme != "https" || parsed.Host == "" {
		return fmt.Errorf("endpoint 必须是 https URL")
	}
	return nil
}

func toRuntimeNodeResult(node sqlc.RuntimeNode) RuntimeNodeResult {
	result := RuntimeNodeResult{
		ID:                       uuidToString(node.ID),
		Name:                     node.Name,
		Status:                   node.Status,
		HeartbeatIntervalSeconds: node.HeartbeatIntervalSeconds,
		HasAgentToken:            node.AgentTokenHash.Valid,
		ProbeFailureStreak:       node.ProbeFailureStreak,
		ProbeSuccessStreak:       node.ProbeSuccessStreak,
	}
	if node.AgentID.Valid {
		result.AgentID = node.AgentID.String
	}
	if node.AgentDockerEndpoint.Valid {
		result.AgentDockerEndpoint = node.AgentDockerEndpoint.String
	}
	if node.AgentFileEndpoint.Valid {
		result.AgentFileEndpoint = node.AgentFileEndpoint.String
	}
	if node.AgentVersion.Valid {
		result.AgentVersion = node.AgentVersion.String
	}
	if node.NodeDataRoot.Valid {
		result.NodeDataRoot = node.NodeDataRoot.String
	}
	if node.MaxApps.Valid {
		v := node.MaxApps.Int32
		result.MaxApps = &v
	}
	if node.LastProbeAttemptedAt.Valid {
		result.LastProbeAttemptedAt = node.LastProbeAttemptedAt.Time.UTC().Format(time.RFC3339)
	}
	if node.LastProbeOkAt.Valid {
		result.LastProbeOKAt = node.LastProbeOkAt.Time.UTC().Format(time.RFC3339)
	}
	if node.LastProbeFailedAt.Valid {
		result.LastProbeFailedAt = node.LastProbeFailedAt.Time.UTC().Format(time.RFC3339)
	}
	if node.LastProbeError.Valid {
		result.LastProbeError = node.LastProbeError.String
	}
	return result
}

func textOrNull(value string) pgtype.Text {
	trimmed := strings.TrimSpace(value)
	return pgtype.Text{String: trimmed, Valid: trimmed != ""}
}

func int32OrNull(value *int32) pgtype.Int4 {
	if value == nil {
		return pgtype.Int4{}
	}
	return pgtype.Int4{Int32: *value, Valid: true}
}

func shortAgentID(value string) string {
	trimmed := strings.TrimSpace(value)
	if len(trimmed) <= 8 {
		return trimmed
	}
	return trimmed[:8]
}

func generateRandomToken(byteLen int) (string, error) {
	buf := make([]byte, byteLen)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
