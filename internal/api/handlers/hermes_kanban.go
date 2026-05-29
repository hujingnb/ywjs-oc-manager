// Package handlers —— hermes_kanban.go 暴露实例任务看板的 HTTP 端点。
// 包含全部读端点（boards/tasks/show/runs/stats）、写端点（create/comment/complete/
// block/unblock/archive/reassign/reclaim）以及 SSE 实时事件流端点（events）。
package handlers

import (
	"context"
	"errors"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"

	"oc-manager/internal/api/apierror"
	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/integrations/ocops"
	"oc-manager/internal/service"
)

// hermesKanbanService 抽象 handler 依赖的 Kanban 业务能力，便于单测注入 stub。
// 包含全部读/写/流方法。
type hermesKanbanService interface {
	// 读方法
	ListBoards(ctx context.Context, p auth.Principal, appID string) ([]ocops.KanbanBoard, error)
	ListTasks(ctx context.Context, p auth.Principal, appID string, f service.KanbanTaskFilter) ([]ocops.KanbanTask, error)
	ShowTask(ctx context.Context, p auth.Principal, appID, board, taskID string) (ocops.KanbanTaskDetail, error)
	TaskRuns(ctx context.Context, p auth.Principal, appID, board, taskID string) ([]ocops.KanbanTaskRun, error)
	Stats(ctx context.Context, p auth.Principal, appID, board string) (ocops.KanbanStats, error)
	Capabilities(ctx context.Context, p auth.Principal, appID string) (ocops.KanbanCapabilities, error)
	// 流方法
	StreamEvents(ctx context.Context, p auth.Principal, appID, board string, onLine func(string)) error
	// 写方法
	CreateTask(ctx context.Context, p auth.Principal, appID string, in service.CreateKanbanTaskInput) (ocops.KanbanTaskDetail, error)
	Comment(ctx context.Context, p auth.Principal, appID, board, taskID, body string) (ocops.KanbanTaskDetail, error)
	Complete(ctx context.Context, p auth.Principal, appID, board, taskID, result string) (ocops.KanbanTaskDetail, error)
	Block(ctx context.Context, p auth.Principal, appID, board, taskID, reason string) (ocops.KanbanTaskDetail, error)
	Unblock(ctx context.Context, p auth.Principal, appID, board, taskID string) (ocops.KanbanTaskDetail, error)
	Archive(ctx context.Context, p auth.Principal, appID, board, taskID string) (ocops.KanbanTaskDetail, error)
	Reassign(ctx context.Context, p auth.Principal, appID, board, taskID, profile string) (ocops.KanbanTaskDetail, error)
	Reclaim(ctx context.Context, p auth.Principal, appID, board, taskID string) (ocops.KanbanTaskDetail, error)
}

// HermesKanbanHandler 处理 /api/v1/apps/:appId/hermes/kanban/* 路由。
type HermesKanbanHandler struct {
	service hermesKanbanService
}

// NewHermesKanbanHandler 构造 handler。
func NewHermesKanbanHandler(svc hermesKanbanService) *HermesKanbanHandler {
	return &HermesKanbanHandler{service: svc}
}

// RegisterHermesKanbanRoutes 注册任务看板路由。
func RegisterHermesKanbanRoutes(router gin.IRouter, h *HermesKanbanHandler) {
	g := router.Group("/api/v1/apps/:appId/hermes/kanban")
	// 读端点
	g.GET("/boards", h.ListBoards)
	g.GET("/tasks", h.ListTasks)
	g.GET("/tasks/:taskId", h.ShowTask)
	g.GET("/tasks/:taskId/runs", h.TaskRuns)
	g.GET("/stats", h.Stats)
	g.GET("/capabilities", h.Capabilities)
	// SSE 事件流端点（board 级订阅，不带 taskId）
	g.GET("/events", h.StreamEvents)
	// 写端点
	g.POST("/tasks", h.CreateTask)
	g.POST("/tasks/:taskId/comment", h.Comment)
	g.POST("/tasks/:taskId/complete", h.Complete)
	g.POST("/tasks/:taskId/block", h.Block)
	g.POST("/tasks/:taskId/unblock", h.Unblock)
	g.POST("/tasks/:taskId/archive", h.Archive)
	g.POST("/tasks/:taskId/reassign", h.Reassign)
	g.POST("/tasks/:taskId/reclaim", h.Reclaim)
}

// writeKanbanError 把 service sentinel error 映射为 HTTP 响应。
// 映射规则见 request_errors.go 的 mappedServiceErrorRules（kanban 节）。
func writeKanbanError(c *gin.Context, err error) {
	writeMappedServiceError(c, err, http.StatusInternalServerError, "任务看板服务暂不可用")
}

// bindOptionalJSON 绑定可选的 JSON 请求体：空 body 视为成功，req 保持零值。
// 用于 unblock/archive/reclaim/complete 等无必填字段的写端点——前端可不发 body。
func bindOptionalJSON(c *gin.Context, req any) error {
	if c.Request.Body == nil || c.Request.ContentLength == 0 {
		return nil
	}
	if err := c.ShouldBindJSON(req); err != nil {
		if errors.Is(err, io.EOF) {
			return nil
		}
		return err
	}
	return nil
}

// ————————————————————————————————————————————————————
// 读端点
// ————————————————————————————————————————————————————

// ListBoards GET /api/v1/apps/{appId}/hermes/kanban/boards
//
// @Summary      列出实例任务看板的 board
// @Tags         hermes-kanban
// @Produce      json
// @Security     BearerAuth
// @Param        appId  path      string  true  "应用 ID"
// @Success      200    {object}  map[string][]ocops.KanbanBoard
// @Failure      403    {object}  ErrorResponse
// @Failure      503    {object}  ErrorResponse
// @Router       /apps/{appId}/hermes/kanban/boards [get]
func (h *HermesKanbanHandler) ListBoards(c *gin.Context) {
	boards, err := h.service.ListBoards(c.Request.Context(), principalFromCtx(c), c.Param("appId"))
	if err != nil {
		writeKanbanError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"boards": boards})
}

// ListTasks GET /api/v1/apps/{appId}/hermes/kanban/tasks
//
// @Summary      列出某 board 的任务
// @Tags         hermes-kanban
// @Produce      json
// @Security     BearerAuth
// @Param        appId     path      string  true   "应用 ID"
// @Param        board     query     string  false  "board slug，缺省 default"
// @Param        status    query     string  false  "按状态过滤"
// @Param        assignee  query     string  false  "按 assignee 过滤"
// @Success      200       {object}  map[string][]ocops.KanbanTask
// @Failure      403       {object}  ErrorResponse
// @Failure      502       {object}  ErrorResponse
// @Failure      503       {object}  ErrorResponse
// @Failure      500       {object}  ErrorResponse
// @Router       /apps/{appId}/hermes/kanban/tasks [get]
func (h *HermesKanbanHandler) ListTasks(c *gin.Context) {
	tasks, err := h.service.ListTasks(c.Request.Context(), principalFromCtx(c), c.Param("appId"), service.KanbanTaskFilter{
		Board:    c.Query("board"),
		Status:   c.Query("status"),
		Assignee: c.Query("assignee"),
	})
	if err != nil {
		writeKanbanError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"tasks": tasks})
}

// ShowTask GET /api/v1/apps/{appId}/hermes/kanban/tasks/{taskId}
//
// @Summary      查询单个任务详情
// @Tags         hermes-kanban
// @Produce      json
// @Security     BearerAuth
// @Param        appId   path      string  true   "应用 ID"
// @Param        taskId  path      string  true   "任务 ID"
// @Param        board   query     string  false  "board slug"
// @Success      200     {object}  map[string]ocops.KanbanTaskDetail
// @Failure      403     {object}  ErrorResponse
// @Failure      502     {object}  ErrorResponse
// @Failure      503     {object}  ErrorResponse
// @Failure      500     {object}  ErrorResponse
// @Router       /apps/{appId}/hermes/kanban/tasks/{taskId} [get]
func (h *HermesKanbanHandler) ShowTask(c *gin.Context) {
	detail, err := h.service.ShowTask(c.Request.Context(), principalFromCtx(c), c.Param("appId"), c.Query("board"), c.Param("taskId"))
	if err != nil {
		writeKanbanError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"task": detail})
}

// TaskRuns GET /api/v1/apps/{appId}/hermes/kanban/tasks/{taskId}/runs
//
// @Summary      查询任务历次执行
// @Tags         hermes-kanban
// @Produce      json
// @Security     BearerAuth
// @Param        appId   path      string  true   "应用 ID"
// @Param        taskId  path      string  true   "任务 ID"
// @Param        board   query     string  false  "board slug"
// @Success      200     {object}  map[string][]ocops.KanbanTaskRun
// @Failure      403     {object}  ErrorResponse
// @Failure      502     {object}  ErrorResponse
// @Failure      503     {object}  ErrorResponse
// @Failure      500     {object}  ErrorResponse
// @Router       /apps/{appId}/hermes/kanban/tasks/{taskId}/runs [get]
func (h *HermesKanbanHandler) TaskRuns(c *gin.Context) {
	runs, err := h.service.TaskRuns(c.Request.Context(), principalFromCtx(c), c.Param("appId"), c.Query("board"), c.Param("taskId"))
	if err != nil {
		writeKanbanError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"runs": runs})
}

// Stats GET /api/v1/apps/{appId}/hermes/kanban/stats
//
// @Summary      查询任务看板统计
// @Tags         hermes-kanban
// @Produce      json
// @Security     BearerAuth
// @Param        appId  path      string  true   "应用 ID"
// @Param        board  query     string  false  "board slug"
// @Success      200    {object}  map[string]ocops.KanbanStats
// @Failure      403    {object}  ErrorResponse
// @Failure      502    {object}  ErrorResponse
// @Failure      503    {object}  ErrorResponse
// @Failure      500    {object}  ErrorResponse
// @Router       /apps/{appId}/hermes/kanban/stats [get]
func (h *HermesKanbanHandler) Stats(c *gin.Context) {
	stats, err := h.service.Stats(c.Request.Context(), principalFromCtx(c), c.Param("appId"), c.Query("board"))
	if err != nil {
		writeKanbanError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"stats": stats})
}

// Capabilities GET /api/v1/apps/{appId}/hermes/kanban/capabilities
//
// @Summary      查询实例任务看板的 oc-kanban 能力
// @Description  返回 oc-kanban 契约版本、支持的 verb 与 feature 开关，供前端按能力降级。
// @Tags         hermes-kanban
// @Produce      json
// @Security     BearerAuth
// @Param        appId  path      string  true  "应用 ID"
// @Success      200    {object}  map[string]ocops.KanbanCapabilities
// @Failure      403    {object}  ErrorResponse
// @Failure      502    {object}  ErrorResponse
// @Failure      503    {object}  ErrorResponse
// @Failure      500    {object}  ErrorResponse
// @Router       /apps/{appId}/hermes/kanban/capabilities [get]
func (h *HermesKanbanHandler) Capabilities(c *gin.Context) {
	caps, err := h.service.Capabilities(c.Request.Context(), principalFromCtx(c), c.Param("appId"))
	if err != nil {
		writeKanbanError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"capabilities": caps})
}

// ————————————————————————————————————————————————————
// SSE 事件流端点
// ————————————————————————————————————————————————————

// StreamEvents GET /api/v1/apps/{appId}/hermes/kanban/events
//
// @Summary      订阅任务看板实时事件流（SSE）
// @Description  以 Server-Sent Events 推送 hermes kanban watch 的 NDJSON 事件。board 维度订阅。
// @Tags         hermes-kanban
// @Produce      text/event-stream
// @Security     BearerAuth
// @Param        appId  path   string  true   "应用 ID"
// @Param        board  query  string  false  "board slug"
// @Success      200
// @Router       /apps/{appId}/hermes/kanban/events [get]
func (h *HermesKanbanHandler) StreamEvents(c *gin.Context) {
	// 设置 SSE 所需响应头：禁止缓存、保持长连接、禁止反代缓冲。
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")

	// 检查 ResponseWriter 是否支持 Flusher；不支持时无法做流式推送。
	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		c.JSON(http.StatusInternalServerError, apierror.New("INTERNAL", "服务端不支持流式响应"))
		return
	}

	err := h.service.StreamEvents(c.Request.Context(), principalFromCtx(c), c.Param("appId"), c.Query("board"), func(line string) {
		// 每行 NDJSON 包成一个 SSE data 事件推给客户端。
		_, _ = c.Writer.WriteString("data: " + line + "\n\n")
		flusher.Flush()
	})
	if err != nil && !errors.Is(err, context.Canceled) {
		// 流已开始（响应头已发送），无法再改 HTTP 状态码，
		// 写一个固定结构的 error 事件让客户端感知异常后关闭连接；
		// 不暴露 err.Error() 内部细节（可能含容器路径等敏感信息）。
		_, _ = c.Writer.WriteString("event: error\ndata: {\"code\":\"KANBAN_STREAM_ERROR\"}\n\n")
		flusher.Flush()
	}
}

// ————————————————————————————————————————————————————
// 写端点
// ————————————————————————————————————————————————————

// CreateTask POST /api/v1/apps/{appId}/hermes/kanban/tasks
//
// @Summary      新建任务
// @Description  创建一个 Kanban 任务。Skills/Workspace/ParentID/MaxRetries 仅平台管理员可生效。
// @Tags         hermes-kanban
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        appId  path      string                   true  "应用 ID"
// @Param        body   body      CreateKanbanTaskRequest  true  "新建任务请求"
// @Success      201    {object}  map[string]ocops.KanbanTaskDetail
// @Failure      400    {object}  ErrorResponse
// @Failure      403    {object}  ErrorResponse
// @Router       /apps/{appId}/hermes/kanban/tasks [post]
func (h *HermesKanbanHandler) CreateTask(c *gin.Context) {
	var req CreateKanbanTaskRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeBindError(c, err)
		return
	}
	principal := principalFromCtx(c)
	// 基础字段所有可写角色均可填写。
	in := service.CreateKanbanTaskInput{
		Board:    req.Board,
		Title:    req.Title,
		Body:     req.Body,
		Assignee: req.Assignee,
		Priority: req.Priority,
	}
	// 高级字段仅平台管理员生效：非平台管理员提交的高级字段被静默丢弃（spec §5.5）。
	// 避免普通成员通过 API 绕过 UI 注入高级配置。
	if principal.Role == domain.UserRolePlatformAdmin {
		in.Skills = req.Skills
		in.Workspace = req.Workspace
		in.ParentID = req.ParentID
		in.MaxRetries = req.MaxRetries
	}
	detail, err := h.service.CreateTask(c.Request.Context(), principal, c.Param("appId"), in)
	if err != nil {
		writeKanbanError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"task": detail})
}

// Comment POST /api/v1/apps/{appId}/hermes/kanban/tasks/{taskId}/comment
//
// @Summary      给任务加评论
// @Tags         hermes-kanban
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        appId   path      string                true  "应用 ID"
// @Param        taskId  path      string                true  "任务 ID"
// @Param        body    body      KanbanCommentRequest  true  "评论请求"
// @Success      200    {object}  map[string]ocops.KanbanTaskDetail
// @Failure      400    {object}  ErrorResponse
// @Failure      403    {object}  ErrorResponse
// @Router       /apps/{appId}/hermes/kanban/tasks/{taskId}/comment [post]
func (h *HermesKanbanHandler) Comment(c *gin.Context) {
	var req KanbanCommentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeBindError(c, err)
		return
	}
	detail, err := h.service.Comment(c.Request.Context(), principalFromCtx(c), c.Param("appId"), req.Board, c.Param("taskId"), req.Body)
	if err != nil {
		writeKanbanError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"task": detail})
}

// Complete POST /api/v1/apps/{appId}/hermes/kanban/tasks/{taskId}/complete
//
// @Summary      标记任务完成
// @Tags         hermes-kanban
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        appId   path      string                  true  "应用 ID"
// @Param        taskId  path      string                  true  "任务 ID"
// @Param        body    body      KanbanCompleteRequest   false  "完成请求"
// @Success      200    {object}  map[string]ocops.KanbanTaskDetail
// @Failure      400    {object}  ErrorResponse
// @Failure      403    {object}  ErrorResponse
// @Router       /apps/{appId}/hermes/kanban/tasks/{taskId}/complete [post]
func (h *HermesKanbanHandler) Complete(c *gin.Context) {
	var req KanbanCompleteRequest
	if err := bindOptionalJSON(c, &req); err != nil {
		writeBindError(c, err)
		return
	}
	detail, err := h.service.Complete(c.Request.Context(), principalFromCtx(c), c.Param("appId"), req.Board, c.Param("taskId"), req.Result)
	if err != nil {
		writeKanbanError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"task": detail})
}

// Block POST /api/v1/apps/{appId}/hermes/kanban/tasks/{taskId}/block
//
// @Summary      阻塞任务
// @Tags         hermes-kanban
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        appId   path      string              true  "应用 ID"
// @Param        taskId  path      string              true  "任务 ID"
// @Param        body    body      KanbanBlockRequest  true  "阻塞请求"
// @Success      200    {object}  map[string]ocops.KanbanTaskDetail
// @Failure      400    {object}  ErrorResponse
// @Failure      403    {object}  ErrorResponse
// @Router       /apps/{appId}/hermes/kanban/tasks/{taskId}/block [post]
func (h *HermesKanbanHandler) Block(c *gin.Context) {
	var req KanbanBlockRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeBindError(c, err)
		return
	}
	detail, err := h.service.Block(c.Request.Context(), principalFromCtx(c), c.Param("appId"), req.Board, c.Param("taskId"), req.Reason)
	if err != nil {
		writeKanbanError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"task": detail})
}

// Unblock POST /api/v1/apps/{appId}/hermes/kanban/tasks/{taskId}/unblock
//
// @Summary      解除任务阻塞
// @Tags         hermes-kanban
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        appId   path      string              true   "应用 ID"
// @Param        taskId  path      string              true   "任务 ID"
// @Param        body    body      KanbanBoardRequest  false  "board 请求"
// @Success      200    {object}  map[string]ocops.KanbanTaskDetail
// @Failure      403    {object}  ErrorResponse
// @Router       /apps/{appId}/hermes/kanban/tasks/{taskId}/unblock [post]
func (h *HermesKanbanHandler) Unblock(c *gin.Context) {
	var req KanbanBoardRequest
	if err := bindOptionalJSON(c, &req); err != nil {
		writeBindError(c, err)
		return
	}
	detail, err := h.service.Unblock(c.Request.Context(), principalFromCtx(c), c.Param("appId"), req.Board, c.Param("taskId"))
	if err != nil {
		writeKanbanError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"task": detail})
}

// Archive POST /api/v1/apps/{appId}/hermes/kanban/tasks/{taskId}/archive
//
// @Summary      归档任务
// @Tags         hermes-kanban
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        appId   path      string              true   "应用 ID"
// @Param        taskId  path      string              true   "任务 ID"
// @Param        body    body      KanbanBoardRequest  false  "board 请求"
// @Success      200    {object}  map[string]ocops.KanbanTaskDetail
// @Failure      403    {object}  ErrorResponse
// @Router       /apps/{appId}/hermes/kanban/tasks/{taskId}/archive [post]
func (h *HermesKanbanHandler) Archive(c *gin.Context) {
	var req KanbanBoardRequest
	if err := bindOptionalJSON(c, &req); err != nil {
		writeBindError(c, err)
		return
	}
	detail, err := h.service.Archive(c.Request.Context(), principalFromCtx(c), c.Param("appId"), req.Board, c.Param("taskId"))
	if err != nil {
		writeKanbanError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"task": detail})
}

// Reassign POST /api/v1/apps/{appId}/hermes/kanban/tasks/{taskId}/reassign
//
// @Summary      重新分配任务
// @Tags         hermes-kanban
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        appId   path      string                  true  "应用 ID"
// @Param        taskId  path      string                  true  "任务 ID"
// @Param        body    body      KanbanReassignRequest   true  "重分配请求"
// @Success      200    {object}  map[string]ocops.KanbanTaskDetail
// @Failure      400    {object}  ErrorResponse
// @Failure      403    {object}  ErrorResponse
// @Router       /apps/{appId}/hermes/kanban/tasks/{taskId}/reassign [post]
func (h *HermesKanbanHandler) Reassign(c *gin.Context) {
	var req KanbanReassignRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeBindError(c, err)
		return
	}
	detail, err := h.service.Reassign(c.Request.Context(), principalFromCtx(c), c.Param("appId"), req.Board, c.Param("taskId"), req.To)
	if err != nil {
		writeKanbanError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"task": detail})
}

// Reclaim POST /api/v1/apps/{appId}/hermes/kanban/tasks/{taskId}/reclaim
//
// @Summary      重置任务认领状态
// @Tags         hermes-kanban
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        appId   path      string              true   "应用 ID"
// @Param        taskId  path      string              true   "任务 ID"
// @Param        body    body      KanbanBoardRequest  false  "board 请求"
// @Success      200    {object}  map[string]ocops.KanbanTaskDetail
// @Failure      403    {object}  ErrorResponse
// @Router       /apps/{appId}/hermes/kanban/tasks/{taskId}/reclaim [post]
func (h *HermesKanbanHandler) Reclaim(c *gin.Context) {
	var req KanbanBoardRequest
	if err := bindOptionalJSON(c, &req); err != nil {
		writeBindError(c, err)
		return
	}
	detail, err := h.service.Reclaim(c.Request.Context(), principalFromCtx(c), c.Param("appId"), req.Board, c.Param("taskId"))
	if err != nil {
		writeKanbanError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"task": detail})
}
