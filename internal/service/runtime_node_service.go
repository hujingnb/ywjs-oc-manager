package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/store/sqlc"
)

// 默认安全参数；测试可注入更短 TTL 加速覆盖过期路径。
const (
	defaultBootstrapTokenTTL    = 30 * time.Minute
	defaultBootstrapTokenLength = 24
	defaultAgentTokenLength     = 32
	defaultHeartbeatInterval    = 30
)

// RuntimeNodeStore 抽象 runtime node 服务所需的数据访问能力。
type RuntimeNodeStore interface {
	CreateRuntimeNode(ctx context.Context, arg sqlc.CreateRuntimeNodeParams) (sqlc.RuntimeNode, error)
	GetRuntimeNode(ctx context.Context, id pgtype.UUID) (sqlc.RuntimeNode, error)
	GetRuntimeNodeByName(ctx context.Context, name string) (sqlc.RuntimeNode, error)
	ListRuntimeNodes(ctx context.Context, arg sqlc.ListRuntimeNodesParams) ([]sqlc.RuntimeNode, error)
	RegisterRuntimeNode(ctx context.Context, arg sqlc.RegisterRuntimeNodeParams) (sqlc.RuntimeNode, error)
	UpdateRuntimeNodeHeartbeat(ctx context.Context, arg sqlc.UpdateRuntimeNodeHeartbeatParams) (sqlc.RuntimeNode, error)
	SetRuntimeNodeStatus(ctx context.Context, arg sqlc.SetRuntimeNodeStatusParams) (sqlc.RuntimeNode, error)
	UpdateRuntimeNodeMaxApps(ctx context.Context, arg sqlc.UpdateRuntimeNodeMaxAppsParams) (sqlc.RuntimeNode, error)
	CreateAuditLog(ctx context.Context, arg sqlc.CreateAuditLogParams) (sqlc.AuditLog, error)
}

// TokenHasher 将 bootstrap/agent token 单向 hash 后存库。
// 默认使用 SHA-256；测试中可替换为可观测函数以便断言传入参数。
type TokenHasher func(string) string

// TokenGenerator 生成随机 token。
// 抽象出来主要便于在测试中注入确定性序列。
type TokenGenerator func() (string, error)

// RuntimeNodeService 维护 runtime 节点的生命周期。
// 节点首次创建时会发放 bootstrap token，agent 启动后用 bootstrap token 注册并换发 agent token；
// agent token 是后续心跳和文件 API 调用的凭证。
type RuntimeNodeService struct {
	store              RuntimeNodeStore
	hashToken          TokenHasher
	generateBootstrap  TokenGenerator
	generateAgent      TokenGenerator
	bootstrapTokenTTL  time.Duration
	heartbeatInterval  int32
	now                func() time.Time
}

// NewRuntimeNodeService 创建 runtime node 服务。
func NewRuntimeNodeService(store RuntimeNodeStore, hash TokenHasher) *RuntimeNodeService {
	return &RuntimeNodeService{
		store:             store,
		hashToken:         hash,
		generateBootstrap: func() (string, error) { return generateRandomToken(defaultBootstrapTokenLength) },
		generateAgent:     func() (string, error) { return generateRandomToken(defaultAgentTokenLength) },
		bootstrapTokenTTL: defaultBootstrapTokenTTL,
		heartbeatInterval: defaultHeartbeatInterval,
		now:               time.Now,
	}
}

// RuntimeNodeInput 是创建/更新节点的入参。
type RuntimeNodeInput struct {
	Name                     string
	HeartbeatIntervalSeconds int32
	NodeDataRoot             string
}

// RuntimeNodeResult 是对外返回的节点视图。
// BootstrapToken 仅在创建/旋转时返回一次，后续调用不再回显。
type RuntimeNodeResult struct {
	ID                       string `json:"id"`
	Name                     string `json:"name"`
	Status                   string `json:"status"`
	AgentDockerEndpoint      string `json:"agent_docker_endpoint,omitempty"`
	AgentFileEndpoint        string `json:"agent_file_endpoint,omitempty"`
	AgentVersion             string `json:"agent_version,omitempty"`
	HeartbeatIntervalSeconds int32  `json:"heartbeat_interval_seconds"`
	NodeDataRoot             string `json:"node_data_root,omitempty"`
	BootstrapToken           string `json:"bootstrap_token,omitempty"`
	BootstrapTokenExpiresAt  string `json:"bootstrap_token_expires_at,omitempty"`
	HasAgentToken            bool   `json:"has_agent_token"`
	MaxApps                  *int32 `json:"max_apps,omitempty"`
}

// AgentRegisterInput 是 agent 用 bootstrap token 注册时提交的信息。
type AgentRegisterInput struct {
	BootstrapToken      string
	AgentDockerEndpoint string
	AgentFileEndpoint   string
	AgentTLSCACert      string
	AgentVersion        string
	NodeDataRoot        string
	ResourceSnapshot    []byte
	Metadata            []byte
}

// AgentRegisterResult 注册成功后服务器返回给 agent 的凭证。
type AgentRegisterResult struct {
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

// CreateNode 平台管理员注册一个新 runtime 节点，并返回一次性 bootstrap token。
func (s *RuntimeNodeService) CreateNode(ctx context.Context, principal auth.Principal, input RuntimeNodeInput) (RuntimeNodeResult, error) {
	if principal.Role != domain.UserRolePlatformAdmin {
		return RuntimeNodeResult{}, ErrForbidden
	}
	if strings.TrimSpace(input.Name) == "" {
		return RuntimeNodeResult{}, fmt.Errorf("节点名称不能为空")
	}
	bootstrap, err := s.generateBootstrap()
	if err != nil {
		return RuntimeNodeResult{}, fmt.Errorf("生成 bootstrap token 失败: %w", err)
	}
	heartbeat := input.HeartbeatIntervalSeconds
	if heartbeat <= 0 {
		heartbeat = s.heartbeatInterval
	}
	expiresAt := s.now().Add(s.bootstrapTokenTTL)
	node, err := s.store.CreateRuntimeNode(ctx, sqlc.CreateRuntimeNodeParams{
		Name:                     input.Name,
		Status:                   domain.RuntimeNodeStatusPending,
		BootstrapTokenHash:       pgtype.Text{String: s.hashToken(bootstrap), Valid: true},
		BootstrapTokenExpiresAt:  pgtype.Timestamptz{Time: expiresAt, Valid: true},
		HeartbeatIntervalSeconds: heartbeat,
		NodeDataRoot:             pgtype.Text{String: input.NodeDataRoot, Valid: input.NodeDataRoot != ""},
	})
	if err != nil {
		return RuntimeNodeResult{}, fmt.Errorf("创建 runtime 节点失败: %w", err)
	}
	result := toRuntimeNodeResult(node)
	result.BootstrapToken = bootstrap
	result.BootstrapTokenExpiresAt = expiresAt.UTC().Format(time.RFC3339)
	return result, nil
}

// ListNodes 列出 runtime 节点。
// 当前实现仅平台管理员可访问；未来若组织维度需要查看节点资源，需要在 service 层补充按权限过滤。
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

// RotateBootstrap 重新发放 bootstrap token。
// 仅在节点未注册或处于 pending/disabled 时允许；已经 active 的节点必须先 SetStatusDisabled 再轮换，
// 避免误操作冲掉正在工作中的 agent_token。
func (s *RuntimeNodeService) RotateBootstrap(ctx context.Context, principal auth.Principal, nodeID string) (RuntimeNodeResult, error) {
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
	if node.Status == domain.RuntimeNodeStatusActive {
		return RuntimeNodeResult{}, ErrRuntimeNodeBusy
	}
	bootstrap, err := s.generateBootstrap()
	if err != nil {
		return RuntimeNodeResult{}, fmt.Errorf("生成 bootstrap token 失败: %w", err)
	}
	expiresAt := s.now().Add(s.bootstrapTokenTTL)
	updated, err := s.persistBootstrapRotation(ctx, node.ID, bootstrap, expiresAt)
	if err != nil {
		return RuntimeNodeResult{}, err
	}
	result := toRuntimeNodeResult(updated)
	result.BootstrapToken = bootstrap
	result.BootstrapTokenExpiresAt = expiresAt.UTC().Format(time.RFC3339)
	return result, nil
}

// UpdateMaxApps 由平台管理员设置或清空节点的应用数上限。
// maxApps == nil 表示清空（不限）；负数返错。设值与审计在 service 层完成；
// 同一动作只写一条 audit_logs 记录，结果固定 succeeded（节点不存在视为返错而非降级）。
func (s *RuntimeNodeService) UpdateMaxApps(ctx context.Context, principal auth.Principal, nodeID string, maxApps *int32) (RuntimeNodeResult, error) {
	if principal.Role != domain.UserRolePlatformAdmin {
		return RuntimeNodeResult{}, ErrForbidden
	}
	id, err := parseUUID(nodeID)
	if err != nil {
		return RuntimeNodeResult{}, ErrNotFound
	}
	param := sqlc.UpdateRuntimeNodeMaxAppsParams{ID: id}
	if maxApps != nil {
		if *maxApps < 0 {
			return RuntimeNodeResult{}, fmt.Errorf("max_apps 不能为负")
		}
		param.MaxApps = pgtype.Int4{Int32: *maxApps, Valid: true}
	}
	node, err := s.store.UpdateRuntimeNodeMaxApps(ctx, param)
	if errors.Is(err, pgx.ErrNoRows) {
		return RuntimeNodeResult{}, ErrNotFound
	}
	if err != nil {
		return RuntimeNodeResult{}, fmt.Errorf("更新节点 max_apps 失败: %w", err)
	}
	actorUUID, _ := optionalUUID(principal.UserID)
	if _, err := s.store.CreateAuditLog(ctx, sqlc.CreateAuditLogParams{
		ActorID:    actorUUID,
		ActorRole:  principal.Role,
		TargetType: "runtime_node",
		TargetID:   uuidToString(node.ID),
		Action:     "update_max_apps",
		Result:     "succeeded",
	}); err != nil {
		return RuntimeNodeResult{}, fmt.Errorf("写审计失败: %w", err)
	}
	return toRuntimeNodeResult(node), nil
}

// SetNodeStatus 启用或禁用节点。
// 禁用节点会同时清空 agent token 让旧 agent 失效；启用通常是在重新发放 bootstrap token 之后。
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

// RegisterAgent 由 agent 在容器启动后调用，用 bootstrap token 换取 agent token。
// 校验通过后会清空 bootstrap token 字段，保证一次性消费。
func (s *RuntimeNodeService) RegisterAgent(ctx context.Context, input AgentRegisterInput) (AgentRegisterResult, error) {
	if input.BootstrapToken == "" {
		return AgentRegisterResult{}, ErrBootstrapTokenInvalid
	}
	tokenHash := s.hashToken(input.BootstrapToken)
	node, err := s.findNodeByBootstrap(ctx, tokenHash)
	if err != nil {
		return AgentRegisterResult{}, err
	}
	if !node.BootstrapTokenExpiresAt.Valid || !node.BootstrapTokenExpiresAt.Time.After(s.now()) {
		return AgentRegisterResult{}, ErrBootstrapTokenInvalid
	}
	agentToken, err := s.generateAgent()
	if err != nil {
		return AgentRegisterResult{}, fmt.Errorf("生成 agent token 失败: %w", err)
	}
	updated, err := s.store.RegisterRuntimeNode(ctx, sqlc.RegisterRuntimeNodeParams{
		ID:                   node.ID,
		AgentDockerEndpoint:  pgtype.Text{String: input.AgentDockerEndpoint, Valid: input.AgentDockerEndpoint != ""},
		AgentFileEndpoint:    pgtype.Text{String: input.AgentFileEndpoint, Valid: input.AgentFileEndpoint != ""},
		AgentTlsCaCert:       pgtype.Text{String: input.AgentTLSCACert, Valid: input.AgentTLSCACert != ""},
		AgentTokenHash:       pgtype.Text{String: s.hashToken(agentToken), Valid: true},
		AgentVersion:         pgtype.Text{String: input.AgentVersion, Valid: input.AgentVersion != ""},
		NodeDataRoot:         pgtype.Text{String: input.NodeDataRoot, Valid: input.NodeDataRoot != ""},
		ResourceSnapshotJson: input.ResourceSnapshot,
		MetadataJson:         input.Metadata,
	})
	if err != nil {
		return AgentRegisterResult{}, fmt.Errorf("注册 runtime 节点失败: %w", err)
	}
	return AgentRegisterResult{
		NodeID:                   uuidToString(updated.ID),
		AgentToken:               agentToken,
		HeartbeatIntervalSeconds: updated.HeartbeatIntervalSeconds,
	}, nil
}

// HandleHeartbeat 校验 agent token 并更新心跳与资源快照。
// 禁用节点的 agent token 一律拒绝，强制 agent 通过新的 bootstrap 流程重新注册。
func (s *RuntimeNodeService) HandleHeartbeat(ctx context.Context, input AgentHeartbeatInput) (RuntimeNodeResult, error) {
	if input.AgentToken == "" {
		return RuntimeNodeResult{}, ErrAgentTokenInvalid
	}
	tokenHash := s.hashToken(input.AgentToken)
	node, err := s.findNodeByAgentToken(ctx, tokenHash)
	if err != nil {
		return RuntimeNodeResult{}, err
	}
	if node.Status == domain.RuntimeNodeStatusDisabled {
		return RuntimeNodeResult{}, ErrAgentTokenInvalid
	}
	updated, err := s.store.UpdateRuntimeNodeHeartbeat(ctx, sqlc.UpdateRuntimeNodeHeartbeatParams{
		ID:                   node.ID,
		AgentVersion:         pgtype.Text{String: input.AgentVersion, Valid: input.AgentVersion != ""},
		ResourceSnapshotJson: input.ResourceSnapshot,
		MetadataJson:         input.Metadata,
	})
	if err != nil {
		return RuntimeNodeResult{}, fmt.Errorf("更新心跳失败: %w", err)
	}
	return toRuntimeNodeResult(updated), nil
}

// findNodeByBootstrap 在所有节点中查找 bootstrap token hash 匹配的节点。
// 当前数据规模小，使用 ListRuntimeNodes 的简单实现；生产环境节点数量增加后可换为索引查询。
func (s *RuntimeNodeService) findNodeByBootstrap(ctx context.Context, hash string) (sqlc.RuntimeNode, error) {
	nodes, err := s.store.ListRuntimeNodes(ctx, sqlc.ListRuntimeNodesParams{Limit: 1000, Offset: 0})
	if err != nil {
		return sqlc.RuntimeNode{}, fmt.Errorf("查询 runtime 节点失败: %w", err)
	}
	for _, node := range nodes {
		if node.BootstrapTokenHash.Valid && node.BootstrapTokenHash.String == hash {
			return node, nil
		}
	}
	return sqlc.RuntimeNode{}, ErrBootstrapTokenInvalid
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

// persistBootstrapRotation 将旋转后的 bootstrap 写回。
// 这里复用 RegisterRuntimeNode 是不合适的（语义不同），采用 SetRuntimeNodeStatus 以保持节点状态机；
// 由于现有 querier 没有专门的 rotate query，我们额外在 service 层用 GetRuntimeNode 校验后通过 RegisterRuntimeNode 之外的事务路径写入。
// 实际写入借助 BootstrapTokenHash 的清理与新值落库；这里通过 store 抽象的 RotateBootstrap 桩点，
// 避免 service 直接操作 SQL，保持职责清晰。
func (s *RuntimeNodeService) persistBootstrapRotation(ctx context.Context, nodeID pgtype.UUID, token string, expiresAt time.Time) (sqlc.RuntimeNode, error) {
	rotator, ok := s.store.(BootstrapRotator)
	if !ok {
		return sqlc.RuntimeNode{}, fmt.Errorf("当前数据访问层不支持 bootstrap 轮换")
	}
	return rotator.RotateBootstrapToken(ctx, RotateBootstrapTokenParams{
		ID:                      nodeID,
		BootstrapTokenHash:      s.hashToken(token),
		BootstrapTokenExpiresAt: expiresAt,
	})
}

// BootstrapRotator 是 RuntimeNodeStore 的可选扩展。
// 仓库实现需要在 service 包外提供 RotateBootstrapToken 实现，避免污染 sqlc 生成代码。
type BootstrapRotator interface {
	RotateBootstrapToken(ctx context.Context, arg RotateBootstrapTokenParams) (sqlc.RuntimeNode, error)
}

// RotateBootstrapTokenParams 定义 rotate 调用参数。
type RotateBootstrapTokenParams struct {
	ID                      pgtype.UUID
	BootstrapTokenHash      string
	BootstrapTokenExpiresAt time.Time
}

func toRuntimeNodeResult(node sqlc.RuntimeNode) RuntimeNodeResult {
	result := RuntimeNodeResult{
		ID:                       uuidToString(node.ID),
		Name:                     node.Name,
		Status:                   node.Status,
		HeartbeatIntervalSeconds: node.HeartbeatIntervalSeconds,
		HasAgentToken:            node.AgentTokenHash.Valid,
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
	if node.BootstrapTokenExpiresAt.Valid {
		result.BootstrapTokenExpiresAt = node.BootstrapTokenExpiresAt.Time.UTC().Format(time.RFC3339)
	}
	if node.MaxApps.Valid {
		v := node.MaxApps.Int32
		result.MaxApps = &v
	}
	return result
}

func generateRandomToken(byteLen int) (string, error) {
	buf := make([]byte, byteLen)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
