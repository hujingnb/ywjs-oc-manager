// app_skills.go — 实例级 skill 管理 HTTP 接口（列/装/卸/更新）。
//
// 路由基础路径：/api/v1/apps/:appId/skills
// 鉴权由上游 middleware 注入 Principal，handler 经 principalFromCtx 取出后透传给 service。
package handlers

import (
	"context"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"oc-manager/internal/api/apierror"
	"oc-manager/internal/auth"
	"oc-manager/internal/service"
)

// appSkillService 抽取 AppSkillService 的 4 个方法供 handler 注入（最小接口，便于测试替换）。
type appSkillService interface {
	// List 返回指定实例已安装的 skill 列表（含实时对账 status）。
	List(ctx context.Context, principal auth.Principal, appID string) ([]service.AppSkillResult, error)
	// Install 安装一个新 skill 到指定实例。
	Install(ctx context.Context, principal auth.Principal, appID string, in service.InstallSkillInput) (service.AppSkillResult, error)
	// Uninstall 卸载指定实例的指定 skill。
	Uninstall(ctx context.Context, principal auth.Principal, appID, name string) error
	// Update 将已安装的 skill 更新到目标版本。
	Update(ctx context.Context, principal auth.Principal, appID, name, targetVersion string) (service.AppSkillResult, error)
	// Reinstall 对 pending 状态的 skill 重新触发热装 + reload（重试）。
	Reinstall(ctx context.Context, principal auth.Principal, appID, name string) (service.AppSkillResult, error)
}

// AppSkillsHandler 封装实例级 skill 管理的 HTTP 端点。
type AppSkillsHandler struct {
	service appSkillService
}

// NewAppSkillsHandler 创建 AppSkillsHandler，注入 skill service。
func NewAppSkillsHandler(svc appSkillService) *AppSkillsHandler {
	return &AppSkillsHandler{service: svc}
}

// RegisterAppSkillRoutes 在 router 上挂载 per-app skill CRUD 路由。
// 路由组：/api/v1/apps/:appId/skills
func RegisterAppSkillRoutes(router gin.IRouter, h *AppSkillsHandler) {
	g := router.Group("/api/v1/apps/:appId/skills")
	g.GET("", h.List)
	g.POST("", h.Install)
	g.DELETE("/:skillName", h.Uninstall)
	g.POST("/:skillName/update", h.Update)
	g.POST("/:skillName/reinstall", h.Reinstall)
}

// List 列出指定实例已安装的所有 skill（含实时对账 status）。
//
// @Summary      列出实例 skill
// @Description  返回指定实例的 skill 列表，每条包含 name/source/version/status/protected 等字段
// @Tags         app-skills
// @Produce      json
// @Security     BearerAuth
// @Param        appId  path      string  true  "实例 ID"
// @Success      200    {array}   service.AppSkillResult
// @Failure      401    {object}  ErrorResponse
// @Failure      403    {object}  ErrorResponse
// @Failure      500    {object}  ErrorResponse
// @Router       /apps/{appId}/skills [get]
func (h *AppSkillsHandler) List(c *gin.Context) {
	result, err := h.service.List(c.Request.Context(), principalFromCtx(c), c.Param("appId"))
	if err != nil {
		writeAppSkillError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

// Install 安装一个 skill 到指定实例。
//
// @Summary      安装实例 skill
// @Description  从指定来源（platform / clawhub）安装 skill 到实例；成功返回 201 及安装结果
// @Tags         app-skills
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        appId  path      string                    true  "实例 ID"
// @Param        body   body      InstallAppSkillRequest    true  "安装参数"
// @Success      201    {object}  service.AppSkillResult
// @Failure      400    {object}  ErrorResponse
// @Failure      401    {object}  ErrorResponse
// @Failure      403    {object}  ErrorResponse
// @Failure      409    {object}  ErrorResponse
// @Failure      500    {object}  ErrorResponse
// @Router       /apps/{appId}/skills [post]
func (h *AppSkillsHandler) Install(c *gin.Context) {
	var req InstallAppSkillRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, apierror.New("BAD_REQUEST", "请求体格式错误"))
		return
	}
	result, err := h.service.Install(c.Request.Context(), principalFromCtx(c), c.Param("appId"), service.InstallSkillInput{
		Source:    req.Source,
		SourceRef: req.SourceRef,
		Name:      req.Name,
		Version:   req.Version,
	})
	if err != nil {
		writeAppSkillError(c, err)
		return
	}
	c.JSON(http.StatusCreated, result)
}

// Uninstall 卸载指定实例的某个 skill。
//
// @Summary      卸载实例 skill
// @Description  按 skillName 卸载实例 skill；受版本保护的 skill 返回 403
// @Tags         app-skills
// @Produce      json
// @Security     BearerAuth
// @Param        appId      path  string  true  "实例 ID"
// @Param        skillName  path  string  true  "skill 目录名"
// @Success      204        "卸载成功，无响应体"
// @Failure      401        {object}  ErrorResponse
// @Failure      403        {object}  ErrorResponse
// @Failure      404        {object}  ErrorResponse
// @Failure      500        {object}  ErrorResponse
// @Router       /apps/{appId}/skills/{skillName} [delete]
func (h *AppSkillsHandler) Uninstall(c *gin.Context) {
	if err := h.service.Uninstall(c.Request.Context(), principalFromCtx(c), c.Param("appId"), c.Param("skillName")); err != nil {
		writeAppSkillError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// Update 将已安装的 skill 更新到目标版本。
//
// @Summary      更新实例 skill 版本
// @Description  按 skillName 将已安装的 skill 更新到指定版本；成功返回 200 及更新后的结果
// @Tags         app-skills
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        appId      path      string                   true  "实例 ID"
// @Param        skillName  path      string                   true  "skill 目录名"
// @Param        body       body      UpdateAppSkillRequest    true  "目标版本"
// @Success      200        {object}  service.AppSkillResult
// @Failure      400        {object}  ErrorResponse
// @Failure      401        {object}  ErrorResponse
// @Failure      403        {object}  ErrorResponse
// @Failure      404        {object}  ErrorResponse
// @Failure      500        {object}  ErrorResponse
// @Router       /apps/{appId}/skills/{skillName}/update [post]
func (h *AppSkillsHandler) Update(c *gin.Context) {
	var req UpdateAppSkillRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, apierror.New("BAD_REQUEST", "请求体格式错误"))
		return
	}
	result, err := h.service.Update(c.Request.Context(), principalFromCtx(c), c.Param("appId"), c.Param("skillName"), req.Version)
	if err != nil {
		writeAppSkillError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

// Reinstall 对 pending 状态的实例 skill 重新触发 oc-ops 热装 + reload（重试）。
//
// @Summary      重新安装实例 skill
// @Description  对已记录但未生效（pending）的 skill 重新触发热装 + reload；用于首次热装/reload 失败后的手动重试
// @Tags         app-skills
// @Produce      json
// @Security     BearerAuth
// @Param        appId      path  string  true  "应用 ID"
// @Param        skillName  path  string  true  "skill 名称"
// @Success      200  {object}  service.AppSkillResult
// @Failure      403  {object}  ErrorResponse
// @Failure      404  {object}  ErrorResponse
// @Failure      500  {object}  ErrorResponse
// @Router       /apps/{appId}/skills/{skillName}/reinstall [post]
func (h *AppSkillsHandler) Reinstall(c *gin.Context) {
	result, err := h.service.Reinstall(c.Request.Context(), principalFromCtx(c), c.Param("appId"), c.Param("skillName"))
	if err != nil {
		writeAppSkillError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

// writeAppSkillError 将 AppSkillService 哨兵错误映射为固定 HTTP 状态码与错误码。
// 不回传 err.Error() 原文，避免内部实现细节泄露给前端（对齐平台库 handler 静态文案模式）。
func writeAppSkillError(c *gin.Context, err error) {
	switch {
	// 无权操作（非 owner 且非同 org 管理员）→ 403
	case errors.Is(err, service.ErrAppSkillDenied):
		c.JSON(http.StatusForbidden, apierror.New("APP_SKILL_DENIED", "无权管理该实例的 skill"))
	// skill 不存在 → 404
	case errors.Is(err, service.ErrAppSkillNotFound):
		c.JSON(http.StatusNotFound, apierror.New("APP_SKILL_NOT_FOUND", "该实例 skill 不存在"))
	// 同名 skill 已安装 → 409
	case errors.Is(err, service.ErrAppSkillNameConflict):
		c.JSON(http.StatusConflict, apierror.New("APP_SKILL_NAME_CONFLICT", "已有同名 skill，不允许重复安装"))
	// 版本内置保护，禁止卸载 → 403
	case errors.Is(err, service.ErrAppSkillProtected):
		c.JSON(http.StatusForbidden, apierror.New("APP_SKILL_PROTECTED", "当前助手版本必需的 skill 不可删除"))
	// 未知来源类型 → 400
	case errors.Is(err, service.ErrAppSkillSourceUnknown):
		c.JSON(http.StatusBadRequest, apierror.New("APP_SKILL_SOURCE_UNKNOWN", "未知的 skill 来源"))
	// 归档解压炸弹检测失败 → 400
	case errors.Is(err, service.ErrAppSkillArchiveTooDangerous):
		c.JSON(http.StatusBadRequest, apierror.New("APP_SKILL_ARCHIVE_TOO_DANGEROUS", "skill 归档解压校验失败，文件可能存在安全风险"))
	// 实例运行的 hermes 版本过旧、不支持 skill 管理（oc-ops 无 /oc/skills 路由）→ 409，提示更新版本
	case errors.Is(err, service.ErrAppSkillRuntimeUnsupported):
		c.JSON(http.StatusConflict, apierror.New("APP_SKILL_RUNTIME_UNSUPPORTED", "当前实例运行的 hermes 版本过旧，不支持技能管理，请更新实例的运行时版本后重试"))
	// 上游市场下载失败（缓存未命中无法降级）→ 502，区别于 manager 自身 500
	case errors.Is(err, service.ErrSkillMarketUpstreamUnavailable):
		c.JSON(http.StatusBadGateway, apierror.New("APP_SKILL_UPSTREAM_UNAVAILABLE", "上游技能市场暂时不可用，请稍后重试"))
	// 其他未预期错误 → 500
	default:
		c.JSON(http.StatusInternalServerError, apierror.New("INTERNAL_ERROR", "服务器内部错误"))
	}
}
