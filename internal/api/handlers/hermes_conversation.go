// Package handlers —— hermes_conversation.go 暴露实例会话 HTTP 端点：
// 列会话 / 读历史 / 续聊 / 新建 / 删除。链路转发到 oc-ops 再到 hermes api_server。
package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"oc-manager/internal/api/apierror"
	"oc-manager/internal/auth"
	"oc-manager/internal/integrations/ocops"
)

// conversationHandlerService 抽象 handler 依赖的会话业务能力，便于单测注入 stub。
type conversationHandlerService interface {
	ListSessions(ctx context.Context, p auth.Principal, appID, source string, limit, offset int) ([]ocops.ConversationSession, error)
	Messages(ctx context.Context, p auth.Principal, appID, sid string) ([]ocops.ConversationMessage, error)
	CreateSession(ctx context.Context, p auth.Principal, appID, title string) (ocops.ConversationSession, error)
	DeleteSession(ctx context.Context, p auth.Principal, appID, sid string) error
	Chat(ctx context.Context, p auth.Principal, appID, sid, message string) (ocops.ConversationChatResult, error)
	ChatStream(ctx context.Context, p auth.Principal, appID, sid, message string) (<-chan ocops.ConversationStreamEvent, error)
}

// HermesConversationHandler 处理 /api/v1/apps/:appId/hermes/conversations/* 路由。
type HermesConversationHandler struct {
	service conversationHandlerService
}

// NewHermesConversationHandler 构造 handler。
func NewHermesConversationHandler(svc conversationHandlerService) *HermesConversationHandler {
	return &HermesConversationHandler{service: svc}
}

// RegisterHermesConversationRoutes 注册实例会话路由。
func RegisterHermesConversationRoutes(router gin.IRouter, h *HermesConversationHandler) {
	g := router.Group("/api/v1/apps/:appId/hermes/conversations")
	g.GET("", h.List)
	g.POST("", h.Create)
	g.GET("/:sid/messages", h.Messages)
	g.POST("/:sid/chat", h.Chat)
	g.POST("/:sid/chat/stream", h.ChatStream)
	g.DELETE("/:sid", h.Delete)
}

// writeConversationError 把 service 哨兵错误映射为 HTTP 响应。
// 映射规则见 request_errors.go 的 mappedServiceErrorRules（conversation 节）。
func writeConversationError(c *gin.Context, err error) {
	writeMappedServiceError(c, err, http.StatusInternalServerError, "会话服务暂不可用")
}

// List GET /api/v1/apps/{appId}/hermes/conversations
//
// @Summary      列出实例会话
// @Tags         hermes-conversation
// @Produce      json
// @Security     BearerAuth
// @Param        appId   path   string  true   "应用 ID"
// @Param        source  query  string  false  "渠道来源过滤，如 weixin"
// @Param        limit   query  int     false  "分页大小，0 使用 service 默认值"
// @Param        offset  query  int     false  "分页偏移"
// @Success      200     {object}  map[string][]ocops.ConversationSession
// @Failure      403     {object}  ErrorResponse
// @Failure      503     {object}  ErrorResponse
// @Router       /apps/{appId}/hermes/conversations [get]
func (h *HermesConversationHandler) List(c *gin.Context) {
	// limit/offset 解析失败时退化为 0，由 service 层使用默认值。
	limit, _ := strconv.Atoi(c.Query("limit"))
	offset, _ := strconv.Atoi(c.Query("offset"))
	out, err := h.service.ListSessions(c.Request.Context(), principalFromCtx(c),
		c.Param("appId"), c.Query("source"), limit, offset)
	if err != nil {
		writeConversationError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"sessions": out})
}

// Messages GET /api/v1/apps/{appId}/hermes/conversations/{sid}/messages
//
// @Summary      读会话历史
// @Tags         hermes-conversation
// @Produce      json
// @Security     BearerAuth
// @Param        appId  path  string  true  "应用 ID"
// @Param        sid    path  string  true  "会话 ID"
// @Success      200    {object}  map[string][]ocops.ConversationMessage
// @Failure      403    {object}  ErrorResponse
// @Failure      503    {object}  ErrorResponse
// @Router       /apps/{appId}/hermes/conversations/{sid}/messages [get]
func (h *HermesConversationHandler) Messages(c *gin.Context) {
	out, err := h.service.Messages(c.Request.Context(), principalFromCtx(c), c.Param("appId"), c.Param("sid"))
	if err != nil {
		writeConversationError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"messages": out})
}

// Create POST /api/v1/apps/{appId}/hermes/conversations
//
// @Summary      新建 web 会话
// @Tags         hermes-conversation
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        appId  path  string                     true  "应用 ID"
// @Param        body   body  CreateConversationRequest  false "新建会话请求（title 可选）"
// @Success      201    {object}  map[string]ocops.ConversationSession
// @Failure      403    {object}  ErrorResponse
// @Failure      503    {object}  ErrorResponse
// @Router       /apps/{appId}/hermes/conversations [post]
func (h *HermesConversationHandler) Create(c *gin.Context) {
	var req CreateConversationRequest
	// title 可选，空 body 允许；bindOptionalJSON 对 EOF 静默处理。
	_ = bindOptionalJSON(c, &req)
	out, err := h.service.CreateSession(c.Request.Context(), principalFromCtx(c), c.Param("appId"), req.Title)
	if err != nil {
		writeConversationError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"session": out})
}

// Delete DELETE /api/v1/apps/{appId}/hermes/conversations/{sid}
//
// @Summary      删除会话
// @Tags         hermes-conversation
// @Security     BearerAuth
// @Param        appId  path  string  true  "应用 ID"
// @Param        sid    path  string  true  "会话 ID"
// @Success      204    "删除成功"
// @Failure      403    {object}  ErrorResponse
// @Failure      503    {object}  ErrorResponse
// @Router       /apps/{appId}/hermes/conversations/{sid} [delete]
func (h *HermesConversationHandler) Delete(c *gin.Context) {
	if err := h.service.DeleteSession(c.Request.Context(), principalFromCtx(c), c.Param("appId"), c.Param("sid")); err != nil {
		writeConversationError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// Chat POST /api/v1/apps/{appId}/hermes/conversations/{sid}/chat
//
// @Summary      续聊一轮
// @Tags         hermes-conversation
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        appId  path  string                   true  "应用 ID"
// @Param        sid    path  string                   true  "会话 ID"
// @Param        body   body  ConversationChatRequest  true  "续聊请求"
// @Success      200    {object}  map[string]ocops.ConversationChatResult
// @Failure      400    {object}  ErrorResponse
// @Failure      403    {object}  ErrorResponse
// @Failure      503    {object}  ErrorResponse
// @Router       /apps/{appId}/hermes/conversations/{sid}/chat [post]
func (h *HermesConversationHandler) Chat(c *gin.Context) {
	var req ConversationChatRequest
	// message 为必填字段，ShouldBindJSON 遇 required 校验失败返回 400。
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, apierror.New("CONVERSATION_BAD_REQUEST", "消息内容不能为空"))
		return
	}
	out, err := h.service.Chat(c.Request.Context(), principalFromCtx(c), c.Param("appId"), c.Param("sid"), req.Message)
	if err != nil {
		writeConversationError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"reply": out})
}

// ChatStream POST /api/v1/apps/{appId}/hermes/conversations/{sid}/chat/stream —— 流式续聊（SSE）。
//
// @Summary      流式续聊
// @Tags         hermes-conversation
// @Produce      text/event-stream
// @Security     BearerAuth
// @Param        appId  path  string                   true  "应用 ID"
// @Param        sid    path  string                   true  "会话 ID"
// @Param        body   body  ConversationChatRequest  true  "续聊请求"
// @Success      200    {string}  string  "SSE 事件流，每帧 data 为 {event,payload}"
// @Router       /apps/{appId}/hermes/conversations/{sid}/chat/stream [post]
func (h *HermesConversationHandler) ChatStream(c *gin.Context) {
	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		c.JSON(http.StatusInternalServerError, apierror.New("INTERNAL", "服务端不支持流式响应"))
		return
	}
	var req ConversationChatRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, apierror.New("CONVERSATION_BAD_REQUEST", "消息内容不能为空"))
		return
	}
	// resolve+鉴权在 ChatStream 内完成，此时尚未写响应头，错误可正常映射状态码。
	events, err := h.service.ChatStream(c.Request.Context(), principalFromCtx(c), c.Param("appId"), c.Param("sid"), req.Message)
	if err != nil {
		writeConversationError(c, err)
		return
	}
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")
	flusher.Flush()
	for ev := range events {
		payload, mErr := json.Marshal(ev)
		if mErr != nil {
			continue
		}
		_, _ = c.Writer.WriteString("data: " + string(payload) + "\n\n")
		flusher.Flush()
	}
}
