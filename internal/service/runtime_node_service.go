package service

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/guregu/null/v5"

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
	// EnrollRuntimeNodeInsert 新节点注册（:exec），写入后通过 GetRuntimeNode 读回。
	EnrollRuntimeNodeInsert(ctx context.Context, arg sqlc.EnrollRuntimeNodeInsertParams) error
	// EnrollRuntimeNodeUpdate 已有节点重注册（:exec），写入后通过 GetRuntimeNodeByAgentID 读回。
	EnrollRuntimeNodeUpdate(ctx context.Context, arg sqlc.EnrollRuntimeNodeUpdateParams) error
	GetRuntimeNode(ctx context.Context, id string) (sqlc.RuntimeNode, error)
	GetRuntimeNodeByAgentID(ctx context.Context, agentID null.String) (sqlc.RuntimeNode, error)
	GetRuntimeNodeByName(ctx context.Context, name string) (sqlc.RuntimeNode, error)
	ListRuntimeNodes(ctx context.Context, arg sqlc.ListRuntimeNodesParams) ([]sqlc.RuntimeNode, error)
	// InsertNodeResourceSample 插入资源采样（:exec）。
	InsertNodeResourceSample(ctx context.Context, arg sqlc.InsertNodeResourceSampleParams) error
	ListLatestNodeResourceSamples(ctx context.Context, runtimeNodeIds []string) ([]sqlc.NodeResourceSample, error)
	// UpdateRuntimeNodeHeartbeat 更新心跳（:exec），写入后通过 GetRuntimeNode 读回。
	UpdateRuntimeNodeHeartbeat(ctx context.Context, arg sqlc.UpdateRuntimeNodeHeartbeatParams) error
	// SetRuntimeNodeStatus 更新节点状态（:exec），写入后通过 GetRuntimeNode 读回。
	SetRuntimeNodeStatus(ctx context.Context, arg sqlc.SetRuntimeNodeStatusParams) error
	CreateAuditLog(ctx context.Context, arg sqlc.CreateAuditLogParams) error
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
	// ID 是 manager 侧 runtime_nodes 主键。
	ID string `json:"id"`
	// Name 是管理员后台展示的节点名称。
	Name string `json:"name"`
	// Status 是节点管理状态，active 才允许 agent 心跳继续更新。
	Status string `json:"status"`
	// AgentID 是 agent 自报的外部稳定 ID，用于幂等 enroll。
	AgentID string `json:"agent_id,omitempty"`
	// AgentDockerEndpoint 是 manager 访问该节点 Docker 代理的 HTTPS 地址。
	AgentDockerEndpoint string `json:"agent_docker_endpoint,omitempty"`
	// AgentFileEndpoint 是 manager 访问该节点文件代理的 HTTPS 地址。
	AgentFileEndpoint string `json:"agent_file_endpoint,omitempty"`
	// AgentVersion 是最近一次 enroll 或 heartbeat 上报的 agent 版本。
	AgentVersion string `json:"agent_version,omitempty"`
	// HeartbeatIntervalSeconds 是 manager 告知 agent 的心跳间隔秒数。
	HeartbeatIntervalSeconds int32 `json:"heartbeat_interval_seconds"`
	// NodeDataRoot 是 agent 侧应用数据根目录。
	NodeDataRoot string `json:"node_data_root,omitempty"`
	// HasAgentToken 表示节点是否已保存 agent token hash；不会返回 token 明文。
	HasAgentToken bool `json:"has_agent_token"`
	// MaxApps 是节点可承载应用上限；nil 表示不限制。
	MaxApps *int32 `json:"max_apps,omitempty"`
	// LastProbeAttemptedAt 是最近一次探测开始时间，空值表示尚未探测。
	LastProbeAttemptedAt string `json:"last_probe_attempted_at,omitempty"`
	// LastProbeOKAt 是最近一次探测成功时间。
	LastProbeOKAt string `json:"last_probe_ok_at,omitempty"`
	// LastProbeFailedAt 是最近一次探测失败时间。
	LastProbeFailedAt string `json:"last_probe_failed_at,omitempty"`
	// LastProbeError 是最近一次探测失败原因，已由探测流程写入安全错误文本。
	LastProbeError string `json:"last_probe_error,omitempty"`
	// ProbeFailureStreak 是连续探测失败次数，用于前端展示节点健康风险。
	ProbeFailureStreak int32 `json:"probe_failure_streak"`
	// ProbeSuccessStreak 是连续探测成功次数，用于判断节点恢复稳定性。
	ProbeSuccessStreak int32 `json:"probe_success_streak"`
	// CurrentResource 是节点最近一次资源采样摘要；列表页用于展示当前资源状态。
	CurrentResource *NodeCurrentResourceResult `json:"current_resource,omitempty"`
}

// NodeCurrentResourceResult 是运行节点最近一次资源采样摘要。
type NodeCurrentResourceResult struct {
	// SampledAt 是最近一次采样时间，统一输出 UTC RFC3339。
	SampledAt string `json:"sampled_at"`
	// CPUPercent 是节点 CPU 使用百分比；nil 表示最近采样缺少该指标。
	CPUPercent *float64 `json:"cpu_percent,omitempty"`
	// MemoryUsedBytes 是节点内存已用字节数。
	MemoryUsedBytes *int64 `json:"memory_used_bytes,omitempty"`
	// MemoryTotalBytes 是节点内存总字节数。
	MemoryTotalBytes *int64 `json:"memory_total_bytes,omitempty"`
	// DiskUsedBytes 是节点磁盘已用字节数。
	DiskUsedBytes *int64 `json:"disk_used_bytes,omitempty"`
	// DiskTotalBytes 是节点磁盘总字节数。
	DiskTotalBytes *int64 `json:"disk_total_bytes,omitempty"`
	// InstanceCount 是采样时节点承载的实例数量。
	InstanceCount *int32 `json:"instance_count,omitempty"`
	// LastError 是最近一次节点资源采样错误，空字符串表示无错误或未上报。
	LastError string `json:"last_error,omitempty"`
}

// AgentEnrollInput 是 agent 自动注册时提交的自描述信息。
type AgentEnrollInput struct {
	// AgentID 是 agent 自报的外部稳定 ID，必须是 UUID 字符串并用于幂等复用节点行。
	AgentID string
	// Name 是节点展示名；为空时用 AgentID 前缀生成默认名称。
	Name string
	// MaxApps 是 agent 声明的应用容量上限；nil 表示不限，负数视为非法。
	MaxApps *int32
	// AgentDockerEndpoint 是 manager 访问 agent Docker 代理的 HTTPS URL。
	AgentDockerEndpoint string
	// AgentFileEndpoint 是 manager 访问 agent 文件代理的 HTTPS URL。
	AgentFileEndpoint string
	// AgentTLSCACert 是 agent 代理的 CA 证书 PEM，enroll 时必须能解析。
	AgentTLSCACert string
	// AgentVersion 是当前 agent 版本。
	AgentVersion string
	// NodeDataRoot 是 agent 侧应用数据根目录。
	NodeDataRoot string
	// SampledAt 是 agent 侧资源采样时间；handler 负责为空时补当前 UTC。
	SampledAt time.Time
	// NodeResource 是 enroll 时可选的节点资源采样，用于保留 agent 注册时的首次资源状态。
	NodeResource *NodeResourceInput
	// ResourceSnapshot 是 agent 上报的资源快照 JSON 原文，由 handler 负责序列化。
	ResourceSnapshot []byte
	// Metadata 是 agent 上报的附加元数据 JSON 原文，由 handler 负责序列化。
	Metadata []byte
}

// AgentEnrollResult 是 enroll 成功后返回给 agent 的凭证。
type AgentEnrollResult struct {
	// NodeID 是 manager 为该 agent 分配或复用的 runtime node ID。
	NodeID string `json:"node_id"`
	// AgentToken 是新签发的 agent 明文 token，仅在 enroll 响应中返回一次。
	AgentToken string `json:"agent_token"`
	// HeartbeatIntervalSeconds 是 agent 后续上报心跳的建议间隔。
	HeartbeatIntervalSeconds int32 `json:"heartbeat_interval_seconds"`
}

// AgentHeartbeatInput 是 agent 上报心跳时提交的快照信息。
type AgentHeartbeatInput struct {
	// AgentToken 是 enroll 返回的节点令牌，service 只用其 hash 匹配节点。
	AgentToken string
	// AgentVersion 是心跳时的 agent 版本。
	AgentVersion string
	// SampledAt 是 agent 侧资源采样时间；handler 负责为空时补当前 UTC。
	SampledAt time.Time
	// NodeResource 是心跳携带的节点资源采样；nil 表示本次心跳不写资源表。
	NodeResource *NodeResourceInput
	// ResourceSnapshot 是心跳上报的资源快照 JSON 原文。
	ResourceSnapshot []byte
	// Metadata 是心跳上报的附加元数据 JSON 原文。
	Metadata []byte
}

// NodeResourceInput 是 service 层写入节点资源采样表的规整输入。
// 指针字段沿用 HTTP DTO 语义，确保缺失指标写 NULL 而不是误写成 0。
type NodeResourceInput struct {
	// CPUPercent 是节点 CPU 使用百分比；nil 表示本次未采集。
	CPUPercent *float64
	// MemoryUsedBytes 是节点内存已用字节数。
	MemoryUsedBytes *int64
	// MemoryTotalBytes 是节点内存总字节数。
	MemoryTotalBytes *int64
	// DiskUsedBytes 是节点磁盘已用字节数。
	DiskUsedBytes *int64
	// DiskTotalBytes 是节点磁盘总字节数。
	DiskTotalBytes *int64
	// NetworkRxBytes 是节点网络累计接收字节数。
	NetworkRxBytes *int64
	// NetworkTxBytes 是节点网络累计发送字节数。
	NetworkTxBytes *int64
	// InstanceCount 是采样时节点承载的实例数量。
	InstanceCount *int32
	// LastError 是 agent 侧采样错误；空字符串表示未报告错误。
	LastError string
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
	tokenHash := null.StringFrom(s.hashToken(agentToken))
	agentID := null.StringFrom(strings.TrimSpace(input.AgentID))
	name := strings.TrimSpace(input.Name)
	if name == "" {
		name = "Runtime Node " + shortAgentID(input.AgentID)
	}

	// 预生成节点 ID 用于 INSERT 路径；UPDATE 路径不需要（复用已有行 ID）。
	nodeID := newUUID()

	_, findErr := s.store.GetRuntimeNodeByAgentID(ctx, agentID)
	action := "agent_re_enrolled"
	if errors.Is(findErr, sql.ErrNoRows) {
		action = "agent_enrolled"
		// EnrollRuntimeNodeInsert 为 :exec；写入后通过 GetRuntimeNode 读回。
		if err := s.store.EnrollRuntimeNodeInsert(ctx, sqlc.EnrollRuntimeNodeInsertParams{
			ID:                       nodeID,
			AgentID:                  agentID,
			Name:                     name,
			MaxApps:                  int32PtrToNullInt(input.MaxApps),
			AgentDockerEndpoint:      textOrNull(input.AgentDockerEndpoint),
			AgentFileEndpoint:        textOrNull(input.AgentFileEndpoint),
			AgentTlsCaCert:           textOrNull(input.AgentTLSCACert),
			AgentTokenHash:           tokenHash,
			HeartbeatIntervalSeconds: s.heartbeatInterval,
			AgentVersion:             textOrNull(input.AgentVersion),
			NodeDataRoot:             textOrNull(input.NodeDataRoot),
			ResourceSnapshotJson:     input.ResourceSnapshot,
			MetadataJson:             input.Metadata,
			AgentTokenCiphertext:     null.String{},
		}); err != nil {
			return AgentEnrollResult{}, fmt.Errorf("写入 runtime 节点失败: %w", err)
		}
	} else if findErr != nil {
		return AgentEnrollResult{}, fmt.Errorf("查询 runtime 节点失败: %w", findErr)
	} else {
		// EnrollRuntimeNodeUpdate 为 :exec；写入后通过 GetRuntimeNodeByAgentID 读回节点 ID。
		if err := s.store.EnrollRuntimeNodeUpdate(ctx, sqlc.EnrollRuntimeNodeUpdateParams{
			AgentID:              agentID,
			Name:                 name,
			MaxApps:              int32PtrToNullInt(input.MaxApps),
			AgentDockerEndpoint:  textOrNull(input.AgentDockerEndpoint),
			AgentFileEndpoint:    textOrNull(input.AgentFileEndpoint),
			AgentTlsCaCert:       textOrNull(input.AgentTLSCACert),
			AgentTokenHash:       tokenHash,
			AgentVersion:         textOrNull(input.AgentVersion),
			NodeDataRoot:         textOrNull(input.NodeDataRoot),
			ResourceSnapshotJson: input.ResourceSnapshot,
			MetadataJson:         input.Metadata,
			AgentTokenCiphertext: null.String{},
		}); err != nil {
			return AgentEnrollResult{}, fmt.Errorf("写入 runtime 节点失败: %w", err)
		}
	}
	// 读回节点行（INSERT 路径用 GetRuntimeNode，UPDATE 路径用 GetRuntimeNodeByAgentID）。
	var node sqlc.RuntimeNode
	if action == "agent_enrolled" {
		node, err = s.store.GetRuntimeNode(ctx, nodeID)
	} else {
		node, err = s.store.GetRuntimeNodeByAgentID(ctx, agentID)
	}
	if err != nil {
		return AgentEnrollResult{}, fmt.Errorf("读取注册后节点失败: %w", err)
	}
	if input.NodeResource != nil {
		// enroll 首次采样同样使用数据库返回的节点 ID，避免把 agent 自报信息当作主键来源。
		if err := s.store.InsertNodeResourceSample(ctx, nodeResourceSampleParams(node.ID, input.SampledAt, input.NodeResource)); err != nil {
			return AgentEnrollResult{}, fmt.Errorf("写入节点资源采样失败: %w", err)
		}
	}
	// 详情字段记录 agent 版本，方便审计列表识别是哪个版本上线 / 重连。
	// 版本未上报时落空字符串（落库为 NULL），与 spec 设计一致。
	var auditDetail null.String
	if v := strings.TrimSpace(input.AgentVersion); v != "" {
		auditDetail = null.StringFrom("Agent 版本 " + v)
	}
	if err := s.store.CreateAuditLog(ctx, sqlc.CreateAuditLogParams{
		ID:            newUUID(),
		ActorRole:     "system",
		TargetType:    "runtime_node",
		TargetID:      node.ID,
		Action:        action,
		Result:        "succeeded",
		DetailMessage: auditDetail,
	}); err != nil {
		return AgentEnrollResult{}, fmt.Errorf("写审计失败: %w", err)
	}
	return AgentEnrollResult{
		NodeID:                   node.ID,
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
	// ListLatestNodeResourceSamples 取 []string；从 nodes 中收集 ID。
	nodeIDs := make([]string, 0, len(nodes))
	for _, node := range nodes {
		nodeIDs = append(nodeIDs, node.ID)
	}
	latestByNode := map[string]sqlc.NodeResourceSample{}
	if len(nodeIDs) > 0 {
		samples, err := s.store.ListLatestNodeResourceSamples(ctx, nodeIDs)
		if err != nil {
			return nil, fmt.Errorf("查询 runtime 节点最近资源采样失败: %w", err)
		}
		for _, sample := range samples {
			// sample.RuntimeNodeID 是 string。
			latestByNode[sample.RuntimeNodeID] = sample
		}
	}
	results := make([]RuntimeNodeResult, 0, len(nodes))
	for _, node := range nodes {
		result := toRuntimeNodeResult(node)
		if sample, ok := latestByNode[node.ID]; ok {
			current := toNodeCurrentResourceResult(sample)
			result.CurrentResource = &current
		}
		results = append(results, result)
	}
	return results, nil
}

// GetNode 获取 runtime 节点详情。
func (s *RuntimeNodeService) GetNode(ctx context.Context, principal auth.Principal, nodeID string) (RuntimeNodeResult, error) {
	if principal.Role != domain.UserRolePlatformAdmin {
		return RuntimeNodeResult{}, ErrForbidden
	}
	node, err := s.store.GetRuntimeNode(ctx, nodeID)
	if errors.Is(err, sql.ErrNoRows) {
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
	// SetRuntimeNodeStatus 为 :exec；写入后通过 GetRuntimeNode 读回。
	if err := s.store.SetRuntimeNodeStatus(ctx, sqlc.SetRuntimeNodeStatusParams{ID: nodeID, Status: status}); err != nil {
		return RuntimeNodeResult{}, fmt.Errorf("更新节点状态失败: %w", err)
	}
	node, err := s.store.GetRuntimeNode(ctx, nodeID)
	if errors.Is(err, sql.ErrNoRows) {
		return RuntimeNodeResult{}, ErrNotFound
	}
	if err != nil {
		return RuntimeNodeResult{}, fmt.Errorf("读取状态更新后节点失败: %w", err)
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
	// UpdateRuntimeNodeHeartbeat 为 :exec；写入后通过 GetRuntimeNode 读回。
	if err := s.store.UpdateRuntimeNodeHeartbeat(ctx, sqlc.UpdateRuntimeNodeHeartbeatParams{
		ID:                   node.ID,
		AgentVersion:         textOrNull(input.AgentVersion),
		ResourceSnapshotJson: input.ResourceSnapshot,
		MetadataJson:         input.Metadata,
	}); err != nil {
		return RuntimeNodeResult{}, fmt.Errorf("更新心跳失败: %w", err)
	}
	updated, err := s.store.GetRuntimeNode(ctx, node.ID)
	if err != nil {
		return RuntimeNodeResult{}, fmt.Errorf("读取心跳更新后节点失败: %w", err)
	}
	if input.NodeResource != nil {
		// 资源采样必须绑定数据库更新后的节点 ID，避免信任 agent 请求体里的任何节点身份字段。
		if err := s.store.InsertNodeResourceSample(ctx, nodeResourceSampleParams(updated.ID, input.SampledAt, input.NodeResource)); err != nil {
			return RuntimeNodeResult{}, fmt.Errorf("写入节点资源采样失败: %w", err)
		}
	}
	return toRuntimeNodeResult(updated), nil
}

// findNodeByAgentToken 通过 agent token hash 查找节点。
// runtime_nodes 当前没有按 token hash 的查询，临时扫描节点列表；比较对象始终是 hash，避免明文 token 入库。
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

// validateEnrollInput 校验 agent enroll 的外部 ID、代理地址和 TLS CA。
// AgentID 必须是 UUID，两个 endpoint 必须是 HTTPS URL，CA 必须是 PEM，避免写入不可连接节点。
func validateEnrollInput(input AgentEnrollInput) error {
	// AgentID 格式校验：必须是合法 UUID 字符串（标准格式 xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx，36 字符）。
	agentIDTrimmed := strings.TrimSpace(input.AgentID)
	if len(agentIDTrimmed) != 36 {
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

// validateHTTPSURL 解析并规范校验 agent 暴露给 manager 的代理地址。
// 只允许 https 且必须有 host，阻止 agent 注册本地路径、空 host 或明文 HTTP endpoint。
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

// toRuntimeNodeResult 将 sqlc 节点行转成 API 视图。
// null 空值会被归一化为空字符串或 nil，探测时间统一输出 UTC RFC3339。
func toRuntimeNodeResult(node sqlc.RuntimeNode) RuntimeNodeResult {
	result := RuntimeNodeResult{
		ID:                       node.ID,
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
		v := int32(node.MaxApps.Int64)
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

// toNodeCurrentResourceResult 将最近一次节点资源采样转成列表摘要。
// nullable 指标保持 nil，避免把采集缺失误展示为 0。
func toNodeCurrentResourceResult(sample sqlc.NodeResourceSample) NodeCurrentResourceResult {
	// SampledAt 是 time.Time（非空）。
	result := NodeCurrentResourceResult{SampledAt: formatSampledAt(sample.SampledAt)}
	if sample.CpuPercent.Valid {
		result.CPUPercent = float64Ptr(sample.CpuPercent.Float64)
	}
	if sample.MemoryUsedBytes.Valid {
		result.MemoryUsedBytes = int64Ptr(sample.MemoryUsedBytes.Int64)
	}
	if sample.MemoryTotalBytes.Valid {
		result.MemoryTotalBytes = int64Ptr(sample.MemoryTotalBytes.Int64)
	}
	if sample.DiskUsedBytes.Valid {
		result.DiskUsedBytes = int64Ptr(sample.DiskUsedBytes.Int64)
	}
	if sample.DiskTotalBytes.Valid {
		result.DiskTotalBytes = int64Ptr(sample.DiskTotalBytes.Int64)
	}
	if sample.InstanceCount.Valid {
		result.InstanceCount = int32Ptr(int32(sample.InstanceCount.Int64))
	}
	if sample.LastError.Valid {
		result.LastError = sample.LastError.String
	}
	return result
}

// textOrNull 将外部输入去首尾空白后转换为 null.String。
// 空字符串写成 NULL，避免把未配置 endpoint / 版本误展示为空值字段。
func textOrNull(value string) null.String {
	trimmed := strings.TrimSpace(value)
	return nullStr(trimmed)
}

// int32PtrToNullInt 将可选容量上限转换为 null.Int。
// nil 表示 agent 未声明上限，调用方已在 EnrollAgent 中拒绝负数。
func int32PtrToNullInt(value *int32) null.Int {
	if value == nil {
		return null.Int{}
	}
	return null.IntFrom(int64(*value))
}

// nodeResourceSampleParams 将可选资源指标转换为 sqlc 参数；nil 指标保留为数据库 NULL。
// nodeID 已是 string；SampledAt 是 time.Time。
func nodeResourceSampleParams(nodeID string, sampledAt time.Time, resource *NodeResourceInput) sqlc.InsertNodeResourceSampleParams {
	if sampledAt.IsZero() {
		sampledAt = time.Now().UTC()
	}
	return sqlc.InsertNodeResourceSampleParams{
		ID:               newUUID(),
		RuntimeNodeID:    nodeID,
		SampledAt:        sampledAt.UTC(),
		CpuPercent:       float64PtrToNullFloat(resource.CPUPercent),
		MemoryUsedBytes:  int64PtrToNullInt(resource.MemoryUsedBytes),
		MemoryTotalBytes: int64PtrToNullInt(resource.MemoryTotalBytes),
		DiskUsedBytes:    int64PtrToNullInt(resource.DiskUsedBytes),
		DiskTotalBytes:   int64PtrToNullInt(resource.DiskTotalBytes),
		NetworkRxBytes:   int64PtrToNullInt(resource.NetworkRxBytes),
		NetworkTxBytes:   int64PtrToNullInt(resource.NetworkTxBytes),
		InstanceCount:    int32PtrToNullInt(resource.InstanceCount),
		LastError:        textOrNull(resource.LastError),
	}
}

// float64PtrToNullFloat 将可选浮点指标转换为 null.Float，保留 0 作为有效采样值。
func float64PtrToNullFloat(value *float64) null.Float {
	if value == nil {
		return null.Float{}
	}
	return null.FloatFrom(*value)
}

// int64PtrToNullInt 将可选整型指标转换为 null.Int，保留 0 作为有效采样值。
func int64PtrToNullInt(value *int64) null.Int {
	if value == nil {
		return null.Int{}
	}
	return null.IntFrom(*value)
}

// shortAgentID 生成默认节点名使用的短外部 ID。
// 只截取 trim 后的前 8 位，避免默认名称过长，同时保持同一 AgentID 的展示稳定。
func shortAgentID(value string) string {
	trimmed := strings.TrimSpace(value)
	if len(trimmed) <= 8 {
		return trimmed
	}
	return trimmed[:8]
}

// generateRandomToken 生成 agent token 明文。
// 返回 hex 字符串给 agent，service 仅保存其 hash，用于后续 heartbeat 鉴权。
func generateRandomToken(byteLen int) (string, error) {
	buf := make([]byte, byteLen)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
