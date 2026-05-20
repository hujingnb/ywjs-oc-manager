// Package handlers —— hermes_kanban.go 暴露实例任务看板的 HTTP 读端点。
// 写端点（CreateTask / Comment / Complete / Block / Unblock / Archive / Reassign / Reclaim）
// 与 SSE 事件流端点（StreamEvents）的方法体由 Task D4 实现；本文件 D3 阶段仅实现
// 5 个读端点，写端点路由与接口声明已准备好，方法体留 TODO(D4) 占位。
package handlers

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"

	"oc-manager/internal/auth"
	"oc-manager/internal/service"
)

// hermesKanbanService 抽象 handler 依赖的 Kanban 业务能力，便于单测注入 stub。
// 包含全部读/写/流方法，写方法体由 D4 实现，但接口在此声明完整以便路由注册可引用。
type hermesKanbanService interface {
	// 读方法
	ListBoards(ctx context.Context, p auth.Principal, appID string) ([]service.KanbanBoard, error)
	ListTasks(ctx context.Context, p auth.Principal, appID string, f service.KanbanTaskFilter) ([]service.KanbanTask, error)
	ShowTask(ctx context.Context, p auth.Principal, appID, board, taskID string) (service.KanbanTaskDetail, error)
	TaskRuns(ctx context.Context, p auth.Principal, appID, board, taskID string) ([]service.KanbanTaskRun, error)
	Stats(ctx context.Context, p auth.Principal, appID, board string) (service.KanbanStats, error)
	// 流方法（方法体 D4 实现）
	StreamEvents(ctx context.Context, p auth.Principal, appID, board string, onLine func(string)) error
	// 写方法（方法体 D4 实现）
	CreateTask(ctx context.Context, p auth.Principal, appID string, in service.CreateKanbanTaskInput) (service.KanbanTaskDetail, error)
	Comment(ctx context.Context, p auth.Principal, appID, board, taskID, body string) error
	Complete(ctx context.Context, p auth.Principal, appID, board, taskID, result string) error
	Block(ctx context.Context, p auth.Principal, appID, board, taskID, reason string) error
	Unblock(ctx context.Context, p auth.Principal, appID, board, taskID string) error
	Archive(ctx context.Context, p auth.Principal, appID, board, taskID string) error
	Reassign(ctx context.Context, p auth.Principal, appID, board, taskID, profile string) error
	Reclaim(ctx context.Context, p auth.Principal, appID, board, taskID string) error
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
// 读端点（D3）已完整实现；写端点和事件流路由已注册，方法体 TODO(D4) 补全。
func RegisterHermesKanbanRoutes(router gin.IRouter, h *HermesKanbanHandler) {
	g := router.Group("/api/v1/apps/:appId/hermes/kanban")
	// 读端点（D3 实现）
	g.GET("/boards", h.ListBoards)
	g.GET("/tasks", h.ListTasks)
	g.GET("/tasks/:taskId", h.ShowTask)
	g.GET("/tasks/:taskId/runs", h.TaskRuns)
	g.GET("/stats", h.Stats)
	// TODO(D4): 写端点与事件流端点路由已注册，方法体 D4 补全
	g.GET("/events", h.StreamEvents)
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

// ————————————————————————————————————————————————————
// D3：读端点
// ————————————————————————————————————————————————————

// ListBoards GET /api/v1/apps/{appId}/hermes/kanban/boards
//
// @Summary      列出实例任务看板的 board
// @Tags         hermes-kanban
// @Produce      json
// @Security     BearerAuth
// @Param        appId  path      string  true  "应用 ID"
// @Success      200    {object}  map[string][]service.KanbanBoard
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
// @Success      200       {object}  map[string][]service.KanbanTask
// @Failure      403       {object}  ErrorResponse
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
// @Success      200     {object}  map[string]service.KanbanTaskDetail
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
// @Success      200     {object}  map[string][]service.KanbanTaskRun
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
// @Success      200    {object}  map[string]service.KanbanStats
// @Router       /apps/{appId}/hermes/kanban/stats [get]
func (h *HermesKanbanHandler) Stats(c *gin.Context) {
	stats, err := h.service.Stats(c.Request.Context(), principalFromCtx(c), c.Param("appId"), c.Query("board"))
	if err != nil {
		writeKanbanError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"stats": stats})
}

// ————————————————————————————————————————————————————
// TODO(D4)：写端点与事件流端点方法体（路由已注册，实现留待 D4）
// ————————————————————————————————————————————————————

// StreamEvents GET /api/v1/apps/{appId}/hermes/kanban/events
//
// @Summary      订阅任务看板实时事件流（SSE）
// @Tags         hermes-kanban
// @Produce      text/event-stream
// @Security     BearerAuth
// @Param        appId  path   string  true   "应用 ID"
// @Param        board  query  string  false  "board slug"
// @Router       /apps/{appId}/hermes/kanban/events [get]
func (h *HermesKanbanHandler) StreamEvents(c *gin.Context) {
	// TODO(D4): 实现 SSE 推送，调用 h.service.StreamEvents 并转 text/event-stream。
	c.JSON(http.StatusNotImplemented, gin.H{"code": "NOT_IMPLEMENTED", "message": "事件流端点将在 D4 实现"})
}

// CreateTask POST /api/v1/apps/{appId}/hermes/kanban/tasks
//
// @Summary      新建任务
// @Tags         hermes-kanban
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        appId  path   string                   true  "应用 ID"
// @Param        body   body   CreateKanbanTaskRequest  true  "新建任务请求"
// @Success      200    {object}  map[string]service.KanbanTaskDetail
// @Failure      400    {object}  ErrorResponse
// @Failure      403    {object}  ErrorResponse
// @Router       /apps/{appId}/hermes/kanban/tasks [post]
func (h *HermesKanbanHandler) CreateTask(c *gin.Context) {
	// TODO(D4): 解析 CreateKanbanTaskRequest，按 principal.Role 过滤高级字段，调用 service.CreateTask。
	c.JSON(http.StatusNotImplemented, gin.H{"code": "NOT_IMPLEMENTED", "message": "新建任务端点将在 D4 实现"})
}

// Comment POST /api/v1/apps/{appId}/hermes/kanban/tasks/{taskId}/comment
//
// @Summary      给任务加评论
// @Tags         hermes-kanban
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        appId   path   string                true  "应用 ID"
// @Param        taskId  path   string                true  "任务 ID"
// @Param        body    body   KanbanCommentRequest  true  "评论请求"
// @Success      204
// @Router       /apps/{appId}/hermes/kanban/tasks/{taskId}/comment [post]
func (h *HermesKanbanHandler) Comment(c *gin.Context) {
	// TODO(D4): 解析 KanbanCommentRequest，调用 service.Comment。
	c.JSON(http.StatusNotImplemented, gin.H{"code": "NOT_IMPLEMENTED", "message": "评论端点将在 D4 实现"})
}

// Complete POST /api/v1/apps/{appId}/hermes/kanban/tasks/{taskId}/complete
//
// @Summary      标记任务完成
// @Tags         hermes-kanban
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        appId   path   string                  true  "应用 ID"
// @Param        taskId  path   string                  true  "任务 ID"
// @Param        body    body   KanbanCompleteRequest   true  "完成请求"
// @Success      204
// @Router       /apps/{appId}/hermes/kanban/tasks/{taskId}/complete [post]
func (h *HermesKanbanHandler) Complete(c *gin.Context) {
	// TODO(D4): 解析 KanbanCompleteRequest，调用 service.Complete。
	c.JSON(http.StatusNotImplemented, gin.H{"code": "NOT_IMPLEMENTED", "message": "完成端点将在 D4 实现"})
}

// Block POST /api/v1/apps/{appId}/hermes/kanban/tasks/{taskId}/block
//
// @Summary      阻塞任务
// @Tags         hermes-kanban
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        appId   path   string              true  "应用 ID"
// @Param        taskId  path   string              true  "任务 ID"
// @Param        body    body   KanbanBlockRequest  true  "阻塞请求"
// @Success      204
// @Router       /apps/{appId}/hermes/kanban/tasks/{taskId}/block [post]
func (h *HermesKanbanHandler) Block(c *gin.Context) {
	// TODO(D4): 解析 KanbanBlockRequest，调用 service.Block。
	c.JSON(http.StatusNotImplemented, gin.H{"code": "NOT_IMPLEMENTED", "message": "阻塞端点将在 D4 实现"})
}

// Unblock POST /api/v1/apps/{appId}/hermes/kanban/tasks/{taskId}/unblock
//
// @Summary      解除任务阻塞
// @Tags         hermes-kanban
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        appId   path   string             true  "应用 ID"
// @Param        taskId  path   string             true  "任务 ID"
// @Param        body    body   KanbanBoardRequest  false  "board 请求"
// @Success      204
// @Router       /apps/{appId}/hermes/kanban/tasks/{taskId}/unblock [post]
func (h *HermesKanbanHandler) Unblock(c *gin.Context) {
	// TODO(D4): 解析 KanbanBoardRequest，调用 service.Unblock。
	c.JSON(http.StatusNotImplemented, gin.H{"code": "NOT_IMPLEMENTED", "message": "解除阻塞端点将在 D4 实现"})
}

// Archive POST /api/v1/apps/{appId}/hermes/kanban/tasks/{taskId}/archive
//
// @Summary      归档任务
// @Tags         hermes-kanban
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        appId   path   string              true  "应用 ID"
// @Param        taskId  path   string              true  "任务 ID"
// @Param        body    body   KanbanBoardRequest  false  "board 请求"
// @Success      204
// @Router       /apps/{appId}/hermes/kanban/tasks/{taskId}/archive [post]
func (h *HermesKanbanHandler) Archive(c *gin.Context) {
	// TODO(D4): 解析 KanbanBoardRequest，调用 service.Archive。
	c.JSON(http.StatusNotImplemented, gin.H{"code": "NOT_IMPLEMENTED", "message": "归档端点将在 D4 实现"})
}

// Reassign POST /api/v1/apps/{appId}/hermes/kanban/tasks/{taskId}/reassign
//
// @Summary      重新分配任务
// @Tags         hermes-kanban
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        appId   path   string                  true  "应用 ID"
// @Param        taskId  path   string                  true  "任务 ID"
// @Param        body    body   KanbanReassignRequest   true  "重分配请求"
// @Success      204
// @Router       /apps/{appId}/hermes/kanban/tasks/{taskId}/reassign [post]
func (h *HermesKanbanHandler) Reassign(c *gin.Context) {
	// TODO(D4): 解析 KanbanReassignRequest，调用 service.Reassign。
	c.JSON(http.StatusNotImplemented, gin.H{"code": "NOT_IMPLEMENTED", "message": "重分配端点将在 D4 实现"})
}

// Reclaim POST /api/v1/apps/{appId}/hermes/kanban/tasks/{taskId}/reclaim
//
// @Summary      重置任务认领状态
// @Tags         hermes-kanban
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        appId   path   string              true  "应用 ID"
// @Param        taskId  path   string              true  "任务 ID"
// @Param        body    body   KanbanBoardRequest  false  "board 请求"
// @Success      204
// @Router       /apps/{appId}/hermes/kanban/tasks/{taskId}/reclaim [post]
func (h *HermesKanbanHandler) Reclaim(c *gin.Context) {
	// TODO(D4): 解析 KanbanBoardRequest，调用 service.Reclaim。
	c.JSON(http.StatusNotImplemented, gin.H{"code": "NOT_IMPLEMENTED", "message": "重置认领端点将在 D4 实现"})
}
