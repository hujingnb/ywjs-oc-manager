package handlers

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"oc-manager/internal/service"
)

// AgentEndpointsService 抽象 manager 处理 agent 自动注册与心跳所需的业务能力。
type AgentEndpointsService interface {
	EnrollAgent(ctx context.Context, input service.AgentEnrollInput) (service.AgentEnrollResult, error)
	HandleHeartbeat(ctx context.Context, input service.AgentHeartbeatInput) (service.RuntimeNodeResult, error)
}

// AgentEndpointsHandler 暴露给 runtime agent 的 HTTP 端点。
//
// enroll 用共享 enrollment secret 鉴权；heartbeat 仍使用 agent token 本身鉴权。
type AgentEndpointsHandler struct {
	service          AgentEndpointsService
	enrollmentSecret string
	tokenSink        func(nodeID, agentToken string)
}

// NewAgentEndpointsHandler 创建 agent 端点 handler。
func NewAgentEndpointsHandler(svc AgentEndpointsService, enrollmentSecret string, sink ...func(nodeID, agentToken string)) *AgentEndpointsHandler {
	var s func(string, string)
	if len(sink) > 0 {
		s = sink[0]
	}
	return &AgentEndpointsHandler{service: svc, enrollmentSecret: enrollmentSecret, tokenSink: s}
}

// RegisterAgentRoutes 注册 agent 路由前缀 /api/v1/agent。
func RegisterAgentRoutes(router gin.IRouter, handler *AgentEndpointsHandler) {
	group := router.Group("/api/v1/agent")
	group.POST("/enroll", handler.Enroll)
	group.POST("/heartbeat", handler.Heartbeat)
}

// Enroll 处理 agent 自动注册并换取 agent token。
//
// @Summary      Agent 自动注册
// @Description  runtime agent 使用共享 enrollment secret 自动注册或刷新节点信息，并换取长效 agent token
// @Tags         agent
// @Accept       json
// @Produce      json
// @Param        Authorization  header    string                    true  "Bearer enrollment_secret"
// @Param        body           body      AgentEnrollRequest        true  "自动注册请求"
// @Success      200            {object}  service.AgentEnrollResult
// @Failure      400            {object}  ErrorResponse
// @Failure      401            {object}  ErrorResponse
// @Failure      500            {object}  ErrorResponse
// @Router       /agent/enroll [post]
func (h *AgentEndpointsHandler) Enroll(c *gin.Context) {
	if !h.validEnrollmentSecret(c.GetHeader("Authorization")) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "enrollment secret 无效"})
		return
	}
	var req AgentEnrollRequest
	// enroll 是机器对机器接口，不经过 CSRF；请求体字段校验在这里完成，幂等 upsert 在 service 层完成。
	if err := c.ShouldBindJSON(&req); err != nil {
		writeBindError(c, err)
		return
	}
	resourceJSON, err := agentJSONOrEmpty(req.ResourceSnapshot)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "resource_snapshot 序列化失败"})
		return
	}
	metadataJSON, err := agentJSONOrEmpty(req.Metadata)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "metadata 序列化失败"})
		return
	}
	sampledAt, err := parseAgentSampledAt(req.SampledAt)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "sampled_at 格式无效"})
		return
	}
	result, err := h.service.EnrollAgent(c.Request.Context(), service.AgentEnrollInput{
		AgentID:             req.AgentID,
		Name:                req.Name,
		MaxApps:             req.MaxApps,
		AgentDockerEndpoint: req.AgentDockerEndpoint,
		AgentFileEndpoint:   req.AgentFileEndpoint,
		AgentTLSCACert:      req.AgentTLSCACert,
		AgentVersion:        req.AgentVersion,
		NodeDataRoot:        req.NodeDataRoot,
		SampledAt:           sampledAt,
		NodeResource:        toNodeResourceInput(req.NodeResource),
		ResourceSnapshot:    resourceJSON,
		Metadata:            metadataJSON,
	})
	if err != nil {
		writeMappedServiceError(c, err, http.StatusInternalServerError, "服务暂时不可用")
		return
	}
	if h.tokenSink != nil && result.AgentToken != "" {
		// tokenSink 只缓存本进程 docker proxy 需要的 agent token，持久化仍由 service 负责。
		h.tokenSink(result.NodeID, result.AgentToken)
	}
	c.JSON(http.StatusOK, gin.H{
		"node_id":                    result.NodeID,
		"agent_token":                result.AgentToken,
		"heartbeat_interval_seconds": result.HeartbeatIntervalSeconds,
	})
}

// Heartbeat 处理 agent 上报心跳。
//
// @Summary      Agent 心跳
// @Description  runtime agent 定期上报心跳及资源快照；鉴权通过请求体中的 agent_token 字段完成
// @Tags         agent
// @Accept       json
// @Produce      json
// @Param        body  body      AgentHeartbeatRequest  true  "心跳请求（含 agent_token）"
// @Success      200   {object}  map[string]service.RuntimeNodeResult
// @Failure      400   {object}  ErrorResponse
// @Failure      401   {object}  ErrorResponse
// @Failure      500   {object}  ErrorResponse
// @Router       /agent/heartbeat [post]
func (h *AgentEndpointsHandler) Heartbeat(c *gin.Context) {
	var req AgentHeartbeatRequest
	// heartbeat 使用请求体里的 agent_token 鉴权，避免 runtime agent 额外拼 Authorization header。
	if err := c.ShouldBindJSON(&req); err != nil {
		writeBindError(c, err)
		return
	}
	resourceJSON, err := agentJSONOrEmpty(req.ResourceSnapshot)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "resource_snapshot 序列化失败"})
		return
	}
	metadataJSON, err := agentJSONOrEmpty(req.Metadata)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "metadata 序列化失败"})
		return
	}
	sampledAt, err := parseAgentSampledAt(req.SampledAt)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "sampled_at 格式无效"})
		return
	}
	result, err := h.service.HandleHeartbeat(c.Request.Context(), service.AgentHeartbeatInput{
		AgentToken:       req.AgentToken,
		AgentVersion:     req.AgentVersion,
		SampledAt:        sampledAt,
		NodeResource:     toNodeResourceInput(req.NodeResource),
		ResourceSnapshot: resourceJSON,
		Metadata:         metadataJSON,
	})
	if err != nil {
		writeMappedServiceError(c, err, http.StatusInternalServerError, "服务暂时不可用")
		return
	}
	c.JSON(http.StatusOK, gin.H{"runtime_node": result})
}

// validEnrollmentSecret 使用常量时间比较校验共享密钥，避免长度相同场景下的时序泄露。
func (h *AgentEndpointsHandler) validEnrollmentSecret(header string) bool {
	token, ok := bearerToken(header)
	if !ok || h.enrollmentSecret == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(token), []byte(h.enrollmentSecret)) == 1
}

// agentJSONOrEmpty 将可选 map 编码为 JSON；空 map 保持 nil，避免数据库写入无意义的 {}。
func agentJSONOrEmpty(value map[string]any) ([]byte, error) {
	if len(value) == 0 {
		return nil, nil
	}
	return json.Marshal(value)
}

// parseAgentSampledAt 兼容旧 agent：未带 sampled_at 时由 manager 生成当前 UTC 采样时间。
func parseAgentSampledAt(value string) (time.Time, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return time.Now().UTC(), nil
	}
	parsed, err := time.Parse(time.RFC3339, trimmed)
	if err != nil {
		return time.Time{}, err
	}
	return parsed.UTC(), nil
}

// toNodeResourceInput 将 HTTP DTO 转成 service 输入，保留 nil 指针表达指标缺失。
func toNodeResourceInput(req *AgentNodeResourceRequest) *service.NodeResourceInput {
	if req == nil {
		return nil
	}
	return &service.NodeResourceInput{
		CPUPercent:       req.CPUPercent,
		MemoryUsedBytes:  req.MemoryUsedBytes,
		MemoryTotalBytes: req.MemoryTotalBytes,
		DiskUsedBytes:    req.DiskUsedBytes,
		DiskTotalBytes:   req.DiskTotalBytes,
		NetworkRxBytes:   req.NetworkRxBytes,
		NetworkTxBytes:   req.NetworkTxBytes,
		InstanceCount:    req.InstanceCount,
		LastError:        req.LastError,
	}
}

// bearerToken 从 Authorization header 提取 Bearer token。
// scheme 比较大小写不敏感；缺失或空 token 统一返回 false。
// 此函数仅供 agent 自注册鉴权（enrollment_secret 对比）使用。
func bearerToken(header string) (string, bool) {
	scheme, token, ok := strings.Cut(header, " ")
	return token, ok && strings.EqualFold(scheme, "Bearer") && token != ""
}
