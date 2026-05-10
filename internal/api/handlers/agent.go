package handlers

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	redactlog "oc-manager/internal/log"
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
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求参数不完整"})
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
	result, err := h.service.EnrollAgent(c.Request.Context(), service.AgentEnrollInput{
		AgentID:             req.AgentID,
		Name:                req.Name,
		MaxApps:             req.MaxApps,
		AgentDockerEndpoint: req.AgentDockerEndpoint,
		AgentFileEndpoint:   req.AgentFileEndpoint,
		AgentTLSCACert:      req.AgentTLSCACert,
		AgentVersion:        req.AgentVersion,
		NodeDataRoot:        req.NodeDataRoot,
		ResourceSnapshot:    resourceJSON,
		Metadata:            metadataJSON,
	})
	if err != nil {
		writeAgentEndpointError(c, err)
		return
	}
	if h.tokenSink != nil && result.AgentToken != "" {
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
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求参数不完整"})
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
	result, err := h.service.HandleHeartbeat(c.Request.Context(), service.AgentHeartbeatInput{
		AgentToken:       req.AgentToken,
		AgentVersion:     req.AgentVersion,
		ResourceSnapshot: resourceJSON,
		Metadata:         metadataJSON,
	})
	if err != nil {
		writeAgentEndpointError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"runtime_node": result})
}

func (h *AgentEndpointsHandler) validEnrollmentSecret(header string) bool {
	token, ok := bearerToken(header)
	if !ok || h.enrollmentSecret == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(token), []byte(h.enrollmentSecret)) == 1
}

func agentJSONOrEmpty(value map[string]any) ([]byte, error) {
	if len(value) == 0 {
		return nil, nil
	}
	return json.Marshal(value)
}

func writeAgentEndpointError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrAgentTokenInvalid):
		c.JSON(http.StatusUnauthorized, gin.H{"error": redactlog.SafeErrorMessage(err)})
	case errors.Is(err, service.ErrEnrollInputInvalid):
		c.JSON(http.StatusBadRequest, gin.H{"error": redactlog.SafeErrorMessage(err)})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": "服务暂时不可用"})
	}
}
