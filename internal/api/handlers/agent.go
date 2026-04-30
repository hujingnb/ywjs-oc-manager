package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"oc-manager/internal/service"
)

// AgentEndpointsService 抽象 manager 处理 agent 注册与心跳所需的业务能力。
type AgentEndpointsService interface {
	RegisterAgent(ctx context.Context, input service.AgentRegisterInput) (service.AgentRegisterResult, error)
	HandleHeartbeat(ctx context.Context, input service.AgentHeartbeatInput) (service.RuntimeNodeResult, error)
}

// AgentEndpointsHandler 暴露给 runtime agent 的 HTTP 端点。
// 这里没有 manager 用户的 Authorization，因此不复用其它 handler 的 token 校验，
// 鉴权完全靠 bootstrap_token / agent_token 自身。
type AgentEndpointsHandler struct {
	service AgentEndpointsService
}

// NewAgentEndpointsHandler 创建 agent 端点 handler。
func NewAgentEndpointsHandler(svc AgentEndpointsService) *AgentEndpointsHandler {
	return &AgentEndpointsHandler{service: svc}
}

// RegisterAgentRoutes 注册 agent 路由前缀 /api/v1/agent。
func RegisterAgentRoutes(router gin.IRouter, handler *AgentEndpointsHandler) {
	group := router.Group("/api/v1/agent")
	group.POST("/register", handler.Register)
	group.POST("/heartbeat", handler.Heartbeat)
}

type agentRegisterRequest struct {
	BootstrapToken      string         `json:"bootstrap_token" binding:"required"`
	AgentDockerEndpoint string         `json:"agent_docker_endpoint"`
	AgentFileEndpoint   string         `json:"agent_file_endpoint"`
	AgentTLSCACert      string         `json:"agent_tls_ca_cert"`
	AgentVersion        string         `json:"agent_version"`
	NodeDataRoot        string         `json:"node_data_root"`
	ResourceSnapshot    map[string]any `json:"resource_snapshot"`
	Metadata            map[string]any `json:"metadata"`
}

type agentHeartbeatRequest struct {
	AgentToken       string         `json:"agent_token" binding:"required"`
	AgentVersion     string         `json:"agent_version"`
	ResourceSnapshot map[string]any `json:"resource_snapshot"`
	Metadata         map[string]any `json:"metadata"`
}

// Register 处理 agent 用 bootstrap token 注册并换取 agent token。
func (h *AgentEndpointsHandler) Register(c *gin.Context) {
	var req agentRegisterRequest
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
	result, err := h.service.RegisterAgent(c.Request.Context(), service.AgentRegisterInput{
		BootstrapToken:      req.BootstrapToken,
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
	c.JSON(http.StatusOK, gin.H{
		"node_id":                    result.NodeID,
		"agent_token":                result.AgentToken,
		"heartbeat_interval_seconds": result.HeartbeatIntervalSeconds,
	})
}

// Heartbeat 处理 agent 上报心跳。
func (h *AgentEndpointsHandler) Heartbeat(c *gin.Context) {
	var req agentHeartbeatRequest
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

func agentJSONOrEmpty(value map[string]any) ([]byte, error) {
	if len(value) == 0 {
		return nil, nil
	}
	return json.Marshal(value)
}

func writeAgentEndpointError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrBootstrapTokenInvalid),
		errors.Is(err, service.ErrAgentTokenInvalid):
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": "服务暂时不可用"})
	}
}
