// Package handlers —— hermes_cron.go 暴露实例定时任务的 HTTP 端点。
// handler 只负责 HTTP 绑定、稳定响应结构和平台管理员高级字段过滤；
// app 定位、权限校验、oc-cron 执行与输出校验均由 service.HermesCronService 负责。
package handlers

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"

	"oc-manager/internal/api/apierror"
	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/integrations/ocops"
	"oc-manager/internal/service"
)

// hermesCronService 抽象 handler 依赖的 Cron 业务能力，便于单测注入 stub。
// 方法列表与 service.HermesCronService 对外 HTTP 所需能力保持一致。
type hermesCronService interface {
	Capabilities(ctx context.Context, p auth.Principal, appID string) (ocops.CronCapabilities, error)
	Status(ctx context.Context, p auth.Principal, appID string) (ocops.CronStatus, error)
	ListJobs(ctx context.Context, p auth.Principal, appID string, f service.CronJobFilter) ([]ocops.CronJob, error)
	ShowJob(ctx context.Context, p auth.Principal, appID, jobID string) (ocops.CronJob, error)
	CreateJob(ctx context.Context, p auth.Principal, appID string, in service.CreateCronJobInput) (ocops.CronJob, error)
	UpdateJob(ctx context.Context, p auth.Principal, appID, jobID string, in service.UpdateCronJobInput) (ocops.CronJob, error)
	DeleteJob(ctx context.Context, p auth.Principal, appID, jobID string) error
	PauseJob(ctx context.Context, p auth.Principal, appID, jobID string) (ocops.CronJob, error)
	ResumeJob(ctx context.Context, p auth.Principal, appID, jobID string) (ocops.CronJob, error)
	RunJob(ctx context.Context, p auth.Principal, appID, jobID string) (ocops.CronJob, error)
	History(ctx context.Context, p auth.Principal, appID, jobID string) ([]ocops.CronRunEntry, error)
	Output(ctx context.Context, p auth.Principal, appID, jobID, fileName string) (ocops.CronRunOutput, error)
}

// HermesCronHandler 处理 /api/v1/apps/:appId/hermes/cron/* 路由。
type HermesCronHandler struct {
	service hermesCronService
}

// NewHermesCronHandler 构造 handler。
func NewHermesCronHandler(svc hermesCronService) *HermesCronHandler {
	return &HermesCronHandler{service: svc}
}

// RegisterHermesCronRoutes 注册 Hermes Cron 路由。
func RegisterHermesCronRoutes(router gin.IRouter, h *HermesCronHandler) {
	g := router.Group("/api/v1/apps/:appId/hermes/cron")
	// 读端点
	g.GET("/capabilities", h.Capabilities)
	g.GET("/status", h.Status)
	g.GET("/jobs", h.ListJobs)
	g.GET("/jobs/:jobId", h.ShowJob)
	g.GET("/jobs/:jobId/history", h.History)
	g.GET("/jobs/:jobId/output/:fileName", h.Output)
	// 写端点
	g.POST("/jobs", h.CreateJob)
	g.PATCH("/jobs/:jobId", h.UpdateJob)
	g.DELETE("/jobs/:jobId", h.DeleteJob)
	g.POST("/jobs/:jobId/pause", h.PauseJob)
	g.POST("/jobs/:jobId/resume", h.ResumeJob)
	g.POST("/jobs/:jobId/run", h.RunJob)
}

// writeCronError 把 service sentinel error 映射为 HTTP 响应。
// 映射规则见 request_errors.go 的 mappedServiceErrorRules（cron 节）。
func writeCronError(c *gin.Context, err error) {
	writeMappedServiceError(c, err, http.StatusInternalServerError, apierror.MsgInternal)
}

// stripCronAdvancedFields 根据 principal.Role 过滤平台管理员专属字段。
// service 层保留完整 DTO 以表达 oc-cron 能力；handler 层负责阻断非平台管理员
// 通过 API 直接提交 Skills/Model/Provider/BaseURL 等高级运行配置。
func stripCronAdvancedFields(principal auth.Principal, input any) {
	if principal.Role == domain.UserRolePlatformAdmin {
		return
	}
	switch in := input.(type) {
	case *service.CreateCronJobInput:
		in.Skills = nil
		in.Model = ""
		in.Provider = ""
		in.BaseURL = ""
	case *service.UpdateCronJobInput:
		in.Skills = nil
		in.ClearSkills = false
		in.Model = nil
		in.Provider = nil
		in.BaseURL = nil
	}
}

// cronAllQuery 把 jobs 列表的 all 查询参数转为 bool。
// 只接受常见真值；其它值按 false 处理，避免只读接口因可选过滤参数返回 400。
func cronAllQuery(c *gin.Context) bool {
	value := c.Query("all")
	return value == "true" || value == "1"
}

// ————————————————————————————————————————————————————
// 读端点
// ————————————————————————————————————————————————————

// Capabilities GET /api/v1/apps/{appId}/hermes/cron/capabilities
//
// @Summary      查询实例 Hermes Cron 能力
// @Description  返回 oc-cron 契约版本、支持的 verb 与 feature 开关，供前端按能力降级。
// @Tags         hermes-cron
// @Produce      json
// @Security     BearerAuth
// @Param        appId  path      string  true  "应用 ID"
// @Success      200    {object}  map[string]ocops.CronCapabilities
// @Failure      403    {object}  ErrorResponse
// @Failure      502    {object}  ErrorResponse
// @Failure      503    {object}  ErrorResponse
// @Failure      500    {object}  ErrorResponse
// @Router       /apps/{appId}/hermes/cron/capabilities [get]
func (h *HermesCronHandler) Capabilities(c *gin.Context) {
	caps, err := h.service.Capabilities(c.Request.Context(), principalFromCtx(c), c.Param("appId"))
	if err != nil {
		writeCronError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"capabilities": caps})
}

// Status GET /api/v1/apps/{appId}/hermes/cron/status
//
// @Summary      查询实例 Hermes Cron 调度器状态
// @Tags         hermes-cron
// @Produce      json
// @Security     BearerAuth
// @Param        appId  path      string  true  "应用 ID"
// @Success      200    {object}  map[string]ocops.CronStatus
// @Failure      403    {object}  ErrorResponse
// @Failure      502    {object}  ErrorResponse
// @Failure      503    {object}  ErrorResponse
// @Failure      500    {object}  ErrorResponse
// @Router       /apps/{appId}/hermes/cron/status [get]
func (h *HermesCronHandler) Status(c *gin.Context) {
	status, err := h.service.Status(c.Request.Context(), principalFromCtx(c), c.Param("appId"))
	if err != nil {
		writeCronError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": status})
}

// ListJobs GET /api/v1/apps/{appId}/hermes/cron/jobs
//
// @Summary      列出实例 Hermes Cron 任务
// @Tags         hermes-cron
// @Produce      json
// @Security     BearerAuth
// @Param        appId  path      string  true   "应用 ID"
// @Param        all    query     bool    false  "是否包含 disabled/removed 等非活动任务"
// @Success      200    {object}  map[string][]ocops.CronJob
// @Failure      403    {object}  ErrorResponse
// @Failure      502    {object}  ErrorResponse
// @Failure      503    {object}  ErrorResponse
// @Failure      500    {object}  ErrorResponse
// @Router       /apps/{appId}/hermes/cron/jobs [get]
func (h *HermesCronHandler) ListJobs(c *gin.Context) {
	jobs, err := h.service.ListJobs(c.Request.Context(), principalFromCtx(c), c.Param("appId"), service.CronJobFilter{
		All: cronAllQuery(c),
	})
	if err != nil {
		writeCronError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"jobs": jobs})
}

// ShowJob GET /api/v1/apps/{appId}/hermes/cron/jobs/{jobId}
//
// @Summary      查询单个 Hermes Cron 任务
// @Tags         hermes-cron
// @Produce      json
// @Security     BearerAuth
// @Param        appId  path      string  true  "应用 ID"
// @Param        jobId  path      string  true  "Cron 任务 ID"
// @Success      200    {object}  map[string]ocops.CronJob
// @Failure      400    {object}  ErrorResponse
// @Failure      403    {object}  ErrorResponse
// @Failure      404    {object}  ErrorResponse
// @Failure      502    {object}  ErrorResponse
// @Failure      503    {object}  ErrorResponse
// @Failure      500    {object}  ErrorResponse
// @Router       /apps/{appId}/hermes/cron/jobs/{jobId} [get]
func (h *HermesCronHandler) ShowJob(c *gin.Context) {
	job, err := h.service.ShowJob(c.Request.Context(), principalFromCtx(c), c.Param("appId"), c.Param("jobId"))
	if err != nil {
		writeCronError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"job": job})
}

// History GET /api/v1/apps/{appId}/hermes/cron/jobs/{jobId}/history
//
// @Summary      查询 Hermes Cron 任务执行历史
// @Tags         hermes-cron
// @Produce      json
// @Security     BearerAuth
// @Param        appId  path      string  true  "应用 ID"
// @Param        jobId  path      string  true  "Cron 任务 ID"
// @Success      200    {object}  map[string][]ocops.CronRunEntry
// @Failure      400    {object}  ErrorResponse
// @Failure      403    {object}  ErrorResponse
// @Failure      404    {object}  ErrorResponse
// @Failure      502    {object}  ErrorResponse
// @Failure      503    {object}  ErrorResponse
// @Failure      500    {object}  ErrorResponse
// @Router       /apps/{appId}/hermes/cron/jobs/{jobId}/history [get]
func (h *HermesCronHandler) History(c *gin.Context) {
	runs, err := h.service.History(c.Request.Context(), principalFromCtx(c), c.Param("appId"), c.Param("jobId"))
	if err != nil {
		writeCronError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"runs": runs})
}

// Output GET /api/v1/apps/{appId}/hermes/cron/jobs/{jobId}/output/{fileName}
//
// @Summary      读取 Hermes Cron 单次运行输出
// @Tags         hermes-cron
// @Produce      json
// @Security     BearerAuth
// @Param        appId     path      string  true  "应用 ID"
// @Param        jobId     path      string  true  "Cron 任务 ID"
// @Param        fileName  path      string  true  "输出文件名"
// @Success      200       {object}  map[string]ocops.CronRunOutput
// @Failure      400       {object}  ErrorResponse
// @Failure      403       {object}  ErrorResponse
// @Failure      404       {object}  ErrorResponse
// @Failure      502       {object}  ErrorResponse
// @Failure      503       {object}  ErrorResponse
// @Failure      500       {object}  ErrorResponse
// @Router       /apps/{appId}/hermes/cron/jobs/{jobId}/output/{fileName} [get]
func (h *HermesCronHandler) Output(c *gin.Context) {
	output, err := h.service.Output(c.Request.Context(), principalFromCtx(c), c.Param("appId"), c.Param("jobId"), c.Param("fileName"))
	if err != nil {
		writeCronError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"output": output})
}

// ————————————————————————————————————————————————————
// 写端点
// ————————————————————————————————————————————————————

// CreateJob POST /api/v1/apps/{appId}/hermes/cron/jobs
//
// @Summary      新建 Hermes Cron 任务
// @Description  创建一个 Hermes Cron 任务。Skills/Model/Provider/BaseURL 仅平台管理员可生效。
// @Tags         hermes-cron
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        appId  path      string                true  "应用 ID"
// @Param        body   body      CreateCronJobRequest  true  "新建 Cron 任务请求"
// @Success      201    {object}  map[string]ocops.CronJob
// @Failure      400    {object}  ErrorResponse
// @Failure      403    {object}  ErrorResponse
// @Failure      502    {object}  ErrorResponse
// @Failure      503    {object}  ErrorResponse
// @Failure      500    {object}  ErrorResponse
// @Router       /apps/{appId}/hermes/cron/jobs [post]
func (h *HermesCronHandler) CreateJob(c *gin.Context) {
	var req CreateCronJobRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeBindError(c, err)
		return
	}
	principal := principalFromCtx(c)
	in := service.CreateCronJobInput{
		Name:     req.Name,
		Schedule: req.Schedule,
		Prompt:   req.Prompt,
		Deliver:  req.Deliver,
		Repeat:   req.Repeat,
		Script:   req.Script,
		NoAgent:  req.NoAgent,
		Workdir:  req.Workdir,
		Skills:   req.Skills,
		Model:    req.Model,
		Provider: req.Provider,
		BaseURL:  req.BaseURL,
	}
	stripCronAdvancedFields(principal, &in)
	job, err := h.service.CreateJob(c.Request.Context(), principal, c.Param("appId"), in)
	if err != nil {
		writeCronError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"job": job})
}

// UpdateJob PATCH /api/v1/apps/{appId}/hermes/cron/jobs/{jobId}
//
// @Summary      更新 Hermes Cron 任务
// @Description  更新一个 Hermes Cron 任务。Skills/ClearSkills/Model/Provider/BaseURL 仅平台管理员可生效。
// @Tags         hermes-cron
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        appId  path      string                true  "应用 ID"
// @Param        jobId  path      string                true  "Cron 任务 ID"
// @Param        body   body      UpdateCronJobRequest  true  "更新 Cron 任务请求"
// @Success      200    {object}  map[string]ocops.CronJob
// @Failure      400    {object}  ErrorResponse
// @Failure      403    {object}  ErrorResponse
// @Failure      404    {object}  ErrorResponse
// @Failure      502    {object}  ErrorResponse
// @Failure      503    {object}  ErrorResponse
// @Failure      500    {object}  ErrorResponse
// @Router       /apps/{appId}/hermes/cron/jobs/{jobId} [patch]
func (h *HermesCronHandler) UpdateJob(c *gin.Context) {
	var req UpdateCronJobRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeBindError(c, err)
		return
	}
	if req.ClearRepeat {
		// 当前 oc-cron / hermes cron 只暴露设置 repeat 的稳定参数，没有清空 repeat
		// 的可验证契约；这里显式拒绝，避免公开字段被静默接受为 no-op。
		writeCronError(c, service.ErrCronBadRequest)
		return
	}
	in := service.UpdateCronJobInput{
		Name:        req.Name,
		Schedule:    req.Schedule,
		Prompt:      req.Prompt,
		Deliver:     req.Deliver,
		Repeat:      req.Repeat,
		Script:      req.Script,
		Workdir:     req.Workdir,
		Skills:      req.Skills,
		ClearSkills: req.ClearSkills,
		Model:       req.Model,
		Provider:    req.Provider,
		BaseURL:     req.BaseURL,
	}
	// NoAgent 是三态字段：nil 表示不修改；true/false 分别映射为 --no-agent / --agent。
	if req.NoAgent != nil {
		if *req.NoAgent {
			in.NoAgent = true
		} else {
			in.Agent = true
		}
	}
	principal := principalFromCtx(c)
	stripCronAdvancedFields(principal, &in)
	job, err := h.service.UpdateJob(c.Request.Context(), principal, c.Param("appId"), c.Param("jobId"), in)
	if err != nil {
		writeCronError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"job": job})
}

// DeleteJob DELETE /api/v1/apps/{appId}/hermes/cron/jobs/{jobId}
//
// @Summary      删除 Hermes Cron 任务
// @Tags         hermes-cron
// @Produce      json
// @Security     BearerAuth
// @Param        appId  path  string  true  "应用 ID"
// @Param        jobId  path  string  true  "Cron 任务 ID"
// @Success      204
// @Failure      400  {object}  ErrorResponse
// @Failure      403  {object}  ErrorResponse
// @Failure      404  {object}  ErrorResponse
// @Failure      502  {object}  ErrorResponse
// @Failure      503  {object}  ErrorResponse
// @Failure      500  {object}  ErrorResponse
// @Router       /apps/{appId}/hermes/cron/jobs/{jobId} [delete]
func (h *HermesCronHandler) DeleteJob(c *gin.Context) {
	if err := h.service.DeleteJob(c.Request.Context(), principalFromCtx(c), c.Param("appId"), c.Param("jobId")); err != nil {
		writeCronError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// PauseJob POST /api/v1/apps/{appId}/hermes/cron/jobs/{jobId}/pause
//
// @Summary      暂停 Hermes Cron 任务
// @Tags         hermes-cron
// @Produce      json
// @Security     BearerAuth
// @Param        appId  path      string  true  "应用 ID"
// @Param        jobId  path      string  true  "Cron 任务 ID"
// @Success      200    {object}  map[string]ocops.CronJob
// @Failure      400    {object}  ErrorResponse
// @Failure      403    {object}  ErrorResponse
// @Failure      404    {object}  ErrorResponse
// @Failure      502    {object}  ErrorResponse
// @Failure      503    {object}  ErrorResponse
// @Failure      500    {object}  ErrorResponse
// @Router       /apps/{appId}/hermes/cron/jobs/{jobId}/pause [post]
func (h *HermesCronHandler) PauseJob(c *gin.Context) {
	job, err := h.service.PauseJob(c.Request.Context(), principalFromCtx(c), c.Param("appId"), c.Param("jobId"))
	if err != nil {
		writeCronError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"job": job})
}

// ResumeJob POST /api/v1/apps/{appId}/hermes/cron/jobs/{jobId}/resume
//
// @Summary      恢复 Hermes Cron 任务
// @Tags         hermes-cron
// @Produce      json
// @Security     BearerAuth
// @Param        appId  path      string  true  "应用 ID"
// @Param        jobId  path      string  true  "Cron 任务 ID"
// @Success      200    {object}  map[string]ocops.CronJob
// @Failure      400    {object}  ErrorResponse
// @Failure      403    {object}  ErrorResponse
// @Failure      404    {object}  ErrorResponse
// @Failure      502    {object}  ErrorResponse
// @Failure      503    {object}  ErrorResponse
// @Failure      500    {object}  ErrorResponse
// @Router       /apps/{appId}/hermes/cron/jobs/{jobId}/resume [post]
func (h *HermesCronHandler) ResumeJob(c *gin.Context) {
	job, err := h.service.ResumeJob(c.Request.Context(), principalFromCtx(c), c.Param("appId"), c.Param("jobId"))
	if err != nil {
		writeCronError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"job": job})
}

// RunJob POST /api/v1/apps/{appId}/hermes/cron/jobs/{jobId}/run
//
// @Summary      立即触发 Hermes Cron 任务
// @Tags         hermes-cron
// @Produce      json
// @Security     BearerAuth
// @Param        appId  path      string  true  "应用 ID"
// @Param        jobId  path      string  true  "Cron 任务 ID"
// @Success      200    {object}  map[string]ocops.CronJob
// @Failure      400    {object}  ErrorResponse
// @Failure      403    {object}  ErrorResponse
// @Failure      404    {object}  ErrorResponse
// @Failure      502    {object}  ErrorResponse
// @Failure      503    {object}  ErrorResponse
// @Failure      500    {object}  ErrorResponse
// @Router       /apps/{appId}/hermes/cron/jobs/{jobId}/run [post]
func (h *HermesCronHandler) RunJob(c *gin.Context) {
	job, err := h.service.RunJob(c.Request.Context(), principalFromCtx(c), c.Param("appId"), c.Param("jobId"))
	if err != nil {
		writeCronError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"job": job})
}
