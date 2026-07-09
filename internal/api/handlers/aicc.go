package handlers

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"

	"oc-manager/internal/auth"
	"oc-manager/internal/service"
)

// AICCHandler 处理已认证的 AICC 管理接口。
type AICCHandler struct {
	service aiccService
}

// aiccService 是 AICC handler 依赖的最小 service 接口，便于单测注入 stub。
type aiccService interface {
	CreateAgent(ctx context.Context, principal auth.Principal, input service.AICCAgentInput) (service.AICCAgentResult, error)
	ListAgents(ctx context.Context, principal auth.Principal, orgID string, limit, offset int32) ([]service.AICCAgentResult, error)
	GetAgent(ctx context.Context, principal auth.Principal, agentID string) (service.AICCAgentResult, error)
	UpdateAgent(ctx context.Context, principal auth.Principal, agentID string, input service.AICCAgentInput) (service.AICCAgentResult, error)
	SetAgentStatus(ctx context.Context, principal auth.Principal, agentID, action string) (service.AICCAgentResult, error)
	DeleteAgent(ctx context.Context, principal auth.Principal, agentID string) error
}

// NewAICCHandler 创建 AICC 管理 handler。
func NewAICCHandler(service aiccService) *AICCHandler {
	return &AICCHandler{service: service}
}

// RegisterAICCRoutes 注册 AICC 已认证管理路由。
func RegisterAICCRoutes(router gin.IRouter, handler *AICCHandler) {
	group := router.Group("/api/v1/aicc")
	group.GET("/agents", handler.ListAgents)
	group.POST("/agents", handler.CreateAgent)
	group.GET("/agents/:agentId", handler.GetAgent)
	group.PATCH("/agents/:agentId", handler.UpdateAgent)
	group.DELETE("/agents/:agentId", handler.DeleteAgent)
	group.POST("/agents/:agentId/start", handler.StartAgent)
	group.POST("/agents/:agentId/stop", handler.StopAgent)
}

// CreateAgent 创建 AICC 智能体。
//
// @Summary      创建 AICC 智能体
// @Description  企业管理员创建 AICC 智能体，并自动绑定隐藏 app runtime
// @Tags         aicc
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        body  body      CreateAICCAgentRequest  true  "创建智能体请求"
// @Success      201   {object}  map[string]service.AICCAgentResult
// @Failure      400   {object}  ErrorResponse
// @Failure      401   {object}  ErrorResponse
// @Failure      403   {object}  ErrorResponse
// @Failure      409   {object}  ErrorResponse
// @Failure      500   {object}  ErrorResponse
// @Router       /aicc/agents [post]
func (h *AICCHandler) CreateAgent(c *gin.Context) {
	var req CreateAICCAgentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeBindError(c, err)
		return
	}
	result, err := h.service.CreateAgent(c.Request.Context(), principalFromCtx(c), toAICCAgentInput(req))
	if err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"agent": result})
}

// ListAgents 列出 AICC 智能体。
//
// @Summary      AICC 智能体列表
// @Description  平台管理员可通过 org_id 查询，企业管理员默认查询本企业
// @Tags         aicc
// @Produce      json
// @Security     BearerAuth
// @Param        org_id  query     string  false  "企业 ID（平台管理员必填）"
// @Param        limit   query     int     false  "每页条数（默认 50）"
// @Param        offset  query     int     false  "分页偏移（默认 0）"
// @Success      200     {object}  map[string][]service.AICCAgentResult
// @Failure      401     {object}  ErrorResponse
// @Failure      403     {object}  ErrorResponse
// @Failure      500     {object}  ErrorResponse
// @Router       /aicc/agents [get]
func (h *AICCHandler) ListAgents(c *gin.Context) {
	results, err := h.service.ListAgents(c.Request.Context(), principalFromCtx(c), c.Query("org_id"), queryInt32(c, "limit", 50), queryInt32(c, "offset", 0))
	if err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"agents": results})
}

// GetAgent 读取单个 AICC 智能体。
//
// @Summary      AICC 智能体详情
// @Description  读取单个 AICC 智能体管理面资料
// @Tags         aicc
// @Produce      json
// @Security     BearerAuth
// @Param        agentId  path      string  true  "智能体 ID"
// @Success      200      {object}  map[string]service.AICCAgentResult
// @Failure      401      {object}  ErrorResponse
// @Failure      403      {object}  ErrorResponse
// @Failure      404      {object}  ErrorResponse
// @Failure      500      {object}  ErrorResponse
// @Router       /aicc/agents/{agentId} [get]
func (h *AICCHandler) GetAgent(c *gin.Context) {
	result, err := h.service.GetAgent(c.Request.Context(), principalFromCtx(c), c.Param("agentId"))
	if err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"agent": result})
}

// UpdateAgent 更新 AICC 智能体资料。
//
// @Summary      更新 AICC 智能体
// @Description  企业管理员更新 AICC 智能体资料
// @Tags         aicc
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        agentId  path      string                  true  "智能体 ID"
// @Param        body     body      UpdateAICCAgentRequest  true  "更新智能体请求"
// @Success      200      {object}  map[string]service.AICCAgentResult
// @Failure      400      {object}  ErrorResponse
// @Failure      401      {object}  ErrorResponse
// @Failure      403      {object}  ErrorResponse
// @Failure      404      {object}  ErrorResponse
// @Failure      500      {object}  ErrorResponse
// @Router       /aicc/agents/{agentId} [patch]
func (h *AICCHandler) UpdateAgent(c *gin.Context) {
	var req UpdateAICCAgentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeBindError(c, err)
		return
	}
	result, err := h.service.UpdateAgent(c.Request.Context(), principalFromCtx(c), c.Param("agentId"), service.AICCAgentInput{
		Name:           req.Name,
		Scenario:       req.Scenario,
		Greeting:       req.Greeting,
		AnswerBoundary: req.AnswerBoundary,
		PrivacyMode:    req.PrivacyMode,
		PrivacyText:    req.PrivacyText,
		RetentionDays:  req.RetentionDays,
	})
	if err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"agent": result})
}

// StartAgent 启动 AICC 智能体。
//
// @Summary      启动 AICC 智能体
// @Description  企业管理员将 AICC 智能体切换为 active
// @Tags         aicc
// @Produce      json
// @Security     BearerAuth
// @Param        agentId  path      string  true  "智能体 ID"
// @Success      200      {object}  map[string]service.AICCAgentResult
// @Failure      400      {object}  ErrorResponse
// @Failure      401      {object}  ErrorResponse
// @Failure      403      {object}  ErrorResponse
// @Failure      404      {object}  ErrorResponse
// @Failure      500      {object}  ErrorResponse
// @Router       /aicc/agents/{agentId}/start [post]
func (h *AICCHandler) StartAgent(c *gin.Context) {
	h.setStatus(c, "start")
}

// StopAgent 停止 AICC 智能体。
//
// @Summary      停止 AICC 智能体
// @Description  企业管理员将 AICC 智能体切换为 paused
// @Tags         aicc
// @Produce      json
// @Security     BearerAuth
// @Param        agentId  path      string  true  "智能体 ID"
// @Success      200      {object}  map[string]service.AICCAgentResult
// @Failure      400      {object}  ErrorResponse
// @Failure      401      {object}  ErrorResponse
// @Failure      403      {object}  ErrorResponse
// @Failure      404      {object}  ErrorResponse
// @Failure      500      {object}  ErrorResponse
// @Router       /aicc/agents/{agentId}/stop [post]
func (h *AICCHandler) StopAgent(c *gin.Context) {
	h.setStatus(c, "stop")
}

// DeleteAgent 软删除 AICC 智能体。
//
// @Summary      删除 AICC 智能体
// @Description  企业管理员软删除 AICC 智能体
// @Tags         aicc
// @Security     BearerAuth
// @Param        agentId  path  string  true  "智能体 ID"
// @Success      204
// @Failure      401  {object}  ErrorResponse
// @Failure      403  {object}  ErrorResponse
// @Failure      404  {object}  ErrorResponse
// @Failure      500  {object}  ErrorResponse
// @Router       /aicc/agents/{agentId} [delete]
func (h *AICCHandler) DeleteAgent(c *gin.Context) {
	if err := h.service.DeleteAgent(c.Request.Context(), principalFromCtx(c), c.Param("agentId")); err != nil {
		writeServiceError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *AICCHandler) setStatus(c *gin.Context, action string) {
	result, err := h.service.SetAgentStatus(c.Request.Context(), principalFromCtx(c), c.Param("agentId"), action)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"agent": result})
}

func toAICCAgentInput(req CreateAICCAgentRequest) service.AICCAgentInput {
	return service.AICCAgentInput{
		Name:           req.Name,
		Scenario:       req.Scenario,
		Greeting:       req.Greeting,
		AnswerBoundary: req.AnswerBoundary,
		PrivacyMode:    req.PrivacyMode,
		PrivacyText:    req.PrivacyText,
		RetentionDays:  req.RetentionDays,
	}
}
