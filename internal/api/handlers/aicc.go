package handlers

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

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
	GetAgentSettings(ctx context.Context, principal auth.Principal, agentID string) (service.AICCAgentSettingsResult, error)
	UpdateAgentSettings(ctx context.Context, principal auth.Principal, agentID string, input service.AICCAgentSettingsInput) (service.AICCAgentSettingsResult, error)
	ListSessions(ctx context.Context, principal auth.Principal, agentID string, options service.AICCSessionListOptions) ([]service.AICCSessionResult, error)
	GetSession(ctx context.Context, principal auth.Principal, sessionID string) (service.AICCSessionDetailResult, error)
	ListLeads(ctx context.Context, principal auth.Principal, orgID string, limit, offset int32) ([]service.AICCLeadResult, error)
	ExportLeads(ctx context.Context, principal auth.Principal, orgID string) ([]service.AICCLeadResult, error)
	MarkLeadRead(ctx context.Context, principal auth.Principal, leadID string) error
	ListLeadFields(ctx context.Context, principal auth.Principal, agentID string) ([]service.AICCLeadFieldResult, error)
	ReplaceLeadFields(ctx context.Context, principal auth.Principal, agentID string, fields []service.AICCLeadFieldInput) ([]service.AICCLeadFieldResult, error)
	GetAgentKnowledge(ctx context.Context, principal auth.Principal, agentID string) (service.AICCKnowledgeResult, error)
	ListAgentKnowledgeOptions(ctx context.Context, principal auth.Principal, agentID string) (service.AICCKnowledgeOptionsResult, error)
	ReplaceAgentKnowledge(ctx context.Context, principal auth.Principal, agentID string, input service.AICCKnowledgeInput) (service.AICCKnowledgeResult, error)
	Analytics(ctx context.Context, principal auth.Principal, options service.AICCAnalyticsOptions) (service.AICCAnalyticsResult, error)
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
	group.GET("/agents/:agentId/settings", handler.GetAgentSettings)
	group.PUT("/agents/:agentId/settings", handler.UpdateAgentSettings)
	group.GET("/agents/:agentId/lead-fields", handler.ListLeadFields)
	group.PUT("/agents/:agentId/lead-fields", handler.ReplaceLeadFields)
	group.GET("/agents/:agentId/knowledge", handler.GetAgentKnowledge)
	group.GET("/agents/:agentId/knowledge-options", handler.ListAgentKnowledgeOptions)
	group.PUT("/agents/:agentId/knowledge", handler.ReplaceAgentKnowledge)
	group.GET("/agents/:agentId/sessions", handler.ListSessions)
	group.GET("/sessions/:sessionId", handler.GetSession)
	group.GET("/leads", handler.ListLeads)
	group.GET("/leads/export", handler.ExportLeads)
	group.POST("/leads/:leadId/read", handler.MarkLeadRead)
	group.GET("/analytics", handler.Analytics)
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
		AllowedDomains: req.AllowedDomains,
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

// GetAgentSettings 读取 AICC 智能体运营配置。
//
// @Summary      AICC 智能体运营配置
// @Description  企业管理员读取消息上限、敏感词、访客封禁和会话续接配置
// @Tags         aicc
// @Produce      json
// @Security     BearerAuth
// @Param        agentId  path      string  true  "智能体 ID"
// @Success      200      {object}  map[string]service.AICCAgentSettingsResult
// @Failure      401      {object}  ErrorResponse
// @Failure      403      {object}  ErrorResponse
// @Failure      404      {object}  ErrorResponse
// @Failure      500      {object}  ErrorResponse
// @Router       /aicc/agents/{agentId}/settings [get]
func (h *AICCHandler) GetAgentSettings(c *gin.Context) {
	result, err := h.service.GetAgentSettings(c.Request.Context(), principalFromCtx(c), c.Param("agentId"))
	if err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"settings": result})
}

// UpdateAgentSettings 保存 AICC 智能体运营配置。
//
// @Summary      保存 AICC 智能体运营配置
// @Description  企业管理员保存消息上限、敏感词、访客封禁和会话续接配置
// @Tags         aicc
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        agentId  path      string                          true  "智能体 ID"
// @Param        body     body      UpdateAICCAgentSettingsRequest  true  "运营配置"
// @Success      200      {object}  map[string]service.AICCAgentSettingsResult
// @Failure      400      {object}  ErrorResponse
// @Failure      401      {object}  ErrorResponse
// @Failure      403      {object}  ErrorResponse
// @Failure      404      {object}  ErrorResponse
// @Failure      500      {object}  ErrorResponse
// @Router       /aicc/agents/{agentId}/settings [put]
func (h *AICCHandler) UpdateAgentSettings(c *gin.Context) {
	var req UpdateAICCAgentSettingsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeBindError(c, err)
		return
	}
	var thresholdJSON []byte
	if req.BlockedVisitorThresholdJSON != nil {
		var err error
		thresholdJSON, err = json.Marshal(req.BlockedVisitorThresholdJSON)
		if err != nil {
			writeServiceError(c, err)
			return
		}
	}
	result, err := h.service.UpdateAgentSettings(c.Request.Context(), principalFromCtx(c), c.Param("agentId"), service.AICCAgentSettingsInput{
		MessageLimitPerSession:      req.MessageLimitPerSession,
		SensitiveWords:              req.SensitiveWords,
		BlockedVisitorEnabled:       req.BlockedVisitorEnabled,
		BlockedVisitorThresholdJSON: thresholdJSON,
		SessionResumeTTLMinutes:     req.SessionResumeTTLMinutes,
	})
	if err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"settings": result})
}

// GetAgentKnowledge 读取 AICC 智能体知识范围。
//
// @Summary      AICC 智能体知识范围
// @Description  读取企业知识库、行业知识库和专属文档的挂载配置
// @Tags         aicc
// @Produce      json
// @Security     BearerAuth
// @Param        agentId  path      string  true  "智能体 ID"
// @Success      200      {object}  map[string]service.AICCKnowledgeResult
// @Failure      401      {object}  ErrorResponse
// @Failure      403      {object}  ErrorResponse
// @Failure      404      {object}  ErrorResponse
// @Failure      500      {object}  ErrorResponse
// @Router       /aicc/agents/{agentId}/knowledge [get]
func (h *AICCHandler) GetAgentKnowledge(c *gin.Context) {
	result, err := h.service.GetAgentKnowledge(c.Request.Context(), principalFromCtx(c), c.Param("agentId"))
	if err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"knowledge": result})
}

// ListAgentKnowledgeOptions 读取 AICC 知识范围候选项。
//
// @Summary      AICC 知识范围候选项
// @Description  读取企业管理员可选择的行业知识库和当前智能体专属文档
// @Tags         aicc
// @Produce      json
// @Security     BearerAuth
// @Param        agentId  path      string  true  "智能体 ID"
// @Success      200      {object}  map[string]service.AICCKnowledgeOptionsResult
// @Failure      401      {object}  ErrorResponse
// @Failure      403      {object}  ErrorResponse
// @Failure      404      {object}  ErrorResponse
// @Failure      500      {object}  ErrorResponse
// @Router       /aicc/agents/{agentId}/knowledge-options [get]
func (h *AICCHandler) ListAgentKnowledgeOptions(c *gin.Context) {
	result, err := h.service.ListAgentKnowledgeOptions(c.Request.Context(), principalFromCtx(c), c.Param("agentId"))
	if err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"options": result})
}

// ReplaceAgentKnowledge 整组保存 AICC 智能体知识范围。
//
// @Summary      保存 AICC 智能体知识范围
// @Description  企业管理员整组替换企业知识库、行业知识库和专属文档挂载配置
// @Tags         aicc
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        agentId  path      string                       true  "智能体 ID"
// @Param        body     body      ReplaceAICCKnowledgeRequest  true  "知识范围配置"
// @Success      200      {object}  map[string]service.AICCKnowledgeResult
// @Failure      400      {object}  ErrorResponse
// @Failure      401      {object}  ErrorResponse
// @Failure      403      {object}  ErrorResponse
// @Failure      404      {object}  ErrorResponse
// @Failure      500      {object}  ErrorResponse
// @Router       /aicc/agents/{agentId}/knowledge [put]
func (h *AICCHandler) ReplaceAgentKnowledge(c *gin.Context) {
	var req ReplaceAICCKnowledgeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeBindError(c, err)
		return
	}
	result, err := h.service.ReplaceAgentKnowledge(c.Request.Context(), principalFromCtx(c), c.Param("agentId"), service.AICCKnowledgeInput{
		UseOrgKnowledge:          req.UseOrgKnowledge,
		IndustryKnowledgeBaseIDs: req.IndustryKnowledgeBaseIDs,
		AppDocumentIDs:           req.AppDocumentIDs,
	})
	if err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"knowledge": result})
}

// ListSessions 列出 AICC 会话。
//
// @Summary      AICC 会话列表
// @Description  企业管理员查看指定智能体的访客会话列表
// @Tags         aicc
// @Produce      json
// @Security     BearerAuth
// @Param        agentId  path      string  true   "智能体 ID"
// @Param        resolution_status  query  string  false  "解决状态：resolved / unresolved / unknown"
// @Param        lead_status        query  string  false  "留资状态：pending / complete / skipped"
// @Param        channel            query  string  false  "渠道：web_link / web_widget / voice"
// @Param        region             query  string  false  "访客地域"
// @Param        start_at           query  string  false  "创建时间下界（RFC3339）"
// @Param        end_at             query  string  false  "创建时间上界（RFC3339）"
// @Param        keyword            query  string  false  "来源 URL 或 referrer 关键词"
// @Param        limit    query     int     false  "每页条数（默认 50）"
// @Param        offset   query     int     false  "分页偏移（默认 0）"
// @Success      200      {object}  map[string][]service.AICCSessionResult
// @Failure      400      {object}  ErrorResponse
// @Failure      401      {object}  ErrorResponse
// @Failure      403      {object}  ErrorResponse
// @Failure      404      {object}  ErrorResponse
// @Failure      500      {object}  ErrorResponse
// @Router       /aicc/agents/{agentId}/sessions [get]
func (h *AICCHandler) ListSessions(c *gin.Context) {
	startAt, ok := queryTime(c, "start_at")
	if !ok {
		return
	}
	endAt, ok := queryTime(c, "end_at")
	if !ok {
		return
	}
	results, err := h.service.ListSessions(c.Request.Context(), principalFromCtx(c), c.Param("agentId"), service.AICCSessionListOptions{
		ResolutionStatus: c.Query("resolution_status"),
		LeadStatus:       c.Query("lead_status"),
		Channel:          c.Query("channel"),
		Region:           c.Query("region"),
		StartAt:          startAt,
		EndAt:            endAt,
		Keyword:          c.Query("keyword"),
		Limit:            queryInt32(c, "limit", 50),
		Offset:           queryInt32(c, "offset", 0),
	})
	if err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"sessions": results})
}

// ListLeadFields 列出 AICC 智能体公开页留资字段。
//
// @Summary      AICC 留资字段列表
// @Description  企业管理员查看指定智能体公开页留资字段配置
// @Tags         aicc
// @Produce      json
// @Security     BearerAuth
// @Param        agentId  path      string  true  "智能体 ID"
// @Success      200      {object}  map[string][]service.AICCLeadFieldResult
// @Failure      401      {object}  ErrorResponse
// @Failure      403      {object}  ErrorResponse
// @Failure      404      {object}  ErrorResponse
// @Failure      500      {object}  ErrorResponse
// @Router       /aicc/agents/{agentId}/lead-fields [get]
func (h *AICCHandler) ListLeadFields(c *gin.Context) {
	results, err := h.service.ListLeadFields(c.Request.Context(), principalFromCtx(c), c.Param("agentId"))
	if err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"fields": results})
}

// ReplaceLeadFields 整组保存 AICC 智能体公开页留资字段。
//
// @Summary      保存 AICC 留资字段
// @Description  企业管理员整组替换指定智能体公开页留资字段配置
// @Tags         aicc
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        agentId  path      string                         true  "智能体 ID"
// @Param        body     body      ReplaceAICCLeadFieldsRequest   true  "留资字段配置"
// @Success      200      {object}  map[string][]service.AICCLeadFieldResult
// @Failure      400      {object}  ErrorResponse
// @Failure      401      {object}  ErrorResponse
// @Failure      403      {object}  ErrorResponse
// @Failure      404      {object}  ErrorResponse
// @Failure      500      {object}  ErrorResponse
// @Router       /aicc/agents/{agentId}/lead-fields [put]
func (h *AICCHandler) ReplaceLeadFields(c *gin.Context) {
	var req ReplaceAICCLeadFieldsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeBindError(c, err)
		return
	}
	inputs := make([]service.AICCLeadFieldInput, 0, len(req.Fields))
	for _, field := range req.Fields {
		inputs = append(inputs, service.AICCLeadFieldInput{
			FieldKey:   field.FieldKey,
			Label:      field.Label,
			FieldType:  field.FieldType,
			Required:   field.Required,
			PromptText: field.PromptText,
			SortOrder:  field.SortOrder,
		})
	}
	results, err := h.service.ReplaceLeadFields(c.Request.Context(), principalFromCtx(c), c.Param("agentId"), inputs)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"fields": results})
}

// GetSession 读取 AICC 会话详情。
//
// @Summary      AICC 会话详情
// @Description  企业管理员查看会话摘要和消息镜像
// @Tags         aicc
// @Produce      json
// @Security     BearerAuth
// @Param        sessionId  path      string  true  "会话 ID"
// @Success      200        {object}  service.AICCSessionDetailResult
// @Failure      401        {object}  ErrorResponse
// @Failure      403        {object}  ErrorResponse
// @Failure      404        {object}  ErrorResponse
// @Failure      500        {object}  ErrorResponse
// @Router       /aicc/sessions/{sessionId} [get]
func (h *AICCHandler) GetSession(c *gin.Context) {
	result, err := h.service.GetSession(c.Request.Context(), principalFromCtx(c), c.Param("sessionId"))
	if err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

// ListLeads 列出 AICC 线索。
//
// @Summary      AICC 线索列表
// @Description  企业管理员查看本企业线索，平台管理员可通过 org_id 只读排障
// @Tags         aicc
// @Produce      json
// @Security     BearerAuth
// @Param        org_id  query     string  false  "企业 ID（平台管理员排障使用）"
// @Param        limit   query     int     false  "每页条数（默认 50）"
// @Param        offset  query     int     false  "分页偏移（默认 0）"
// @Success      200     {object}  map[string][]service.AICCLeadResult
// @Failure      401     {object}  ErrorResponse
// @Failure      403     {object}  ErrorResponse
// @Failure      500     {object}  ErrorResponse
// @Router       /aicc/leads [get]
func (h *AICCHandler) ListLeads(c *gin.Context) {
	results, err := h.service.ListLeads(c.Request.Context(), principalFromCtx(c), c.Query("org_id"), queryInt32(c, "limit", 50), queryInt32(c, "offset", 0))
	if err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"leads": results})
}

// ExportLeads 导出 AICC 线索 CSV。
//
// @Summary      导出 AICC 线索
// @Description  导出当前筛选企业的线索基础字段
// @Tags         aicc
// @Produce      text/csv
// @Security     BearerAuth
// @Param        org_id  query  string  false  "企业 ID（平台管理员排障使用）"
// @Success      200
// @Failure      401  {object}  ErrorResponse
// @Failure      403  {object}  ErrorResponse
// @Failure      500  {object}  ErrorResponse
// @Router       /aicc/leads/export [get]
func (h *AICCHandler) ExportLeads(c *gin.Context) {
	results, err := h.service.ExportLeads(c.Request.Context(), principalFromCtx(c), c.Query("org_id"))
	if err != nil {
		writeServiceError(c, err)
		return
	}
	var buf bytes.Buffer
	writer := csv.NewWriter(&buf)
	valueColumns := aiccLeadValueColumns(results)
	header := append([]string{"lead_id", "display_name", "unread", "updated_at"}, valueColumns.headers...)
	if err := writer.Write(header); err != nil {
		writeServiceError(c, err)
		return
	}
	for _, lead := range results {
		row := []string{
			safeCSVCell(lead.ID),
			safeCSVCell(lead.DisplayName),
			boolCSV(lead.Unread),
			lead.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"),
		}
		valuesByKey := map[string]string{}
		for _, value := range lead.Values {
			valuesByKey[value.FieldKey] = value.Value
		}
		for _, key := range valueColumns.keys {
			row = append(row, safeCSVCell(valuesByKey[key]))
		}
		if err := writer.Write(row); err != nil {
			writeServiceError(c, err)
			return
		}
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		writeServiceError(c, err)
		return
	}
	c.Header("Content-Disposition", `attachment; filename="aicc-leads.csv"`)
	c.Data(http.StatusOK, "text/csv; charset=utf-8", buf.Bytes())
}

// MarkLeadRead 标记 AICC 线索已读。
//
// @Summary      标记 AICC 线索已读
// @Description  企业管理员将本企业线索标记为已读
// @Tags         aicc
// @Security     BearerAuth
// @Param        leadId  path  string  true  "线索 ID"
// @Success      204
// @Failure      401  {object}  ErrorResponse
// @Failure      403  {object}  ErrorResponse
// @Failure      404  {object}  ErrorResponse
// @Failure      500  {object}  ErrorResponse
// @Router       /aicc/leads/{leadId}/read [post]
func (h *AICCHandler) MarkLeadRead(c *gin.Context) {
	if err := h.service.MarkLeadRead(c.Request.Context(), principalFromCtx(c), c.Param("leadId")); err != nil {
		writeServiceError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// Analytics 返回 AICC 运营统计。
//
// @Summary      AICC 运营统计
// @Description  返回会话、线索、趋势、地域、来源页和高频问题统计
// @Tags         aicc
// @Produce      json
// @Security     BearerAuth
// @Param        org_id    query     string  false  "企业 ID（平台管理员排障使用）"
// @Param        agent_id  query     string  false  "智能体 ID"
// @Param        start_at  query     string  false  "统计开始时间（RFC3339）"
// @Param        end_at    query     string  false  "统计结束时间（RFC3339）"
// @Param        bucket    query     string  false  "趋势粒度：day / week"
// @Success      200     {object}  map[string]service.AICCAnalyticsResult
// @Failure      400     {object}  ErrorResponse
// @Failure      401     {object}  ErrorResponse
// @Failure      403     {object}  ErrorResponse
// @Failure      500     {object}  ErrorResponse
// @Router       /aicc/analytics [get]
func (h *AICCHandler) Analytics(c *gin.Context) {
	startAt, ok := queryTime(c, "start_at")
	if !ok {
		return
	}
	endAt, ok := queryTime(c, "end_at")
	if !ok {
		return
	}
	result, err := h.service.Analytics(c.Request.Context(), principalFromCtx(c), service.AICCAnalyticsOptions{
		OrgID:   c.Query("org_id"),
		AgentID: c.Query("agent_id"),
		StartAt: startAt,
		EndAt:   endAt,
		Bucket:  c.DefaultQuery("bucket", "day"),
	})
	if err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"analytics": result})
}

func (h *AICCHandler) setStatus(c *gin.Context, action string) {
	result, err := h.service.SetAgentStatus(c.Request.Context(), principalFromCtx(c), c.Param("agentId"), action)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"agent": result})
}

func boolCSV(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

type aiccLeadCSVColumns struct {
	keys    []string
	headers []string
}

func aiccLeadValueColumns(leads []service.AICCLeadResult) aiccLeadCSVColumns {
	labelsByKey := map[string]string{}
	seenKeys := map[string]bool{}
	for _, lead := range leads {
		for _, value := range lead.Values {
			key := strings.TrimSpace(value.FieldKey)
			if key == "" || seenKeys[key] {
				continue
			}
			seenKeys[key] = true
			labelsByKey[key] = strings.TrimSpace(value.Label)
		}
	}
	keys := make([]string, 0, len(labelsByKey))
	for key := range labelsByKey {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	labelCounts := map[string]int{}
	for _, key := range keys {
		label := labelsByKey[key]
		if label == "" {
			label = key
		}
		labelCounts[label]++
	}
	headers := make([]string, 0, len(keys))
	for _, key := range keys {
		label := labelsByKey[key]
		if label == "" {
			label = key
		}
		if labelCounts[label] > 1 {
			label = label + " (" + key + ")"
		}
		headers = append(headers, safeCSVCell(label))
	}
	return aiccLeadCSVColumns{keys: keys, headers: headers}
}

func safeCSVCell(value string) string {
	if value == "" {
		return value
	}
	trimmed := strings.TrimLeft(value, " \t\r\n")
	if strings.HasPrefix(trimmed, "=") || strings.HasPrefix(trimmed, "+") || strings.HasPrefix(trimmed, "-") ||
		strings.HasPrefix(trimmed, "@") {
		return "'" + value
	}
	return value
}

func queryTime(c *gin.Context, key string) (time.Time, bool) {
	value := strings.TrimSpace(c.Query(key))
	if value == "" {
		return time.Time{}, true
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		writeServiceError(c, fmt.Errorf("%w: %s 必须使用 RFC3339 时间格式", service.ErrInvalidArgument, key))
		return time.Time{}, false
	}
	return parsed, true
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
		AllowedDomains: req.AllowedDomains,
	}
}
