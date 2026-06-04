// Package handlers 的 Skill 市场 HTTP 接口实现。
// SkillMarketHandler 暴露 GET /api/v1/skill-market 供前端浏览/搜索平台库与公共库。
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

// skillMarketService 是 SkillMarketHandler 依赖的市场聚合能力接口。
// 包内接口便于在 handler 单测中注入 stub，隔离 HTTP 层与 service 层测试。
type skillMarketService interface {
	// List 按 source/q/cursor 返回一页市场条目。
	// source 取值："platform"、"clawhub"、""（聚合）；未知值返回 ErrSkillMarketSourceUnknown。
	List(ctx context.Context, principal auth.Principal, source, q, cursor string) (service.SkillPage, error)
	// Detail 返回指定 skill（source+ref）的富详情与版本列表，供详情抽屉。
	Detail(ctx context.Context, principal auth.Principal, source, ref string) (service.SkillDetailResult, []service.SkillVersionResult, error)
}

// SkillMarketHandler 处理 skill 市场 HTTP 路由。
type SkillMarketHandler struct {
	// service 是市场聚合层，聚合平台库与公共库两个来源。
	service skillMarketService
}

// NewSkillMarketHandler 构造市场 handler。
func NewSkillMarketHandler(svc skillMarketService) *SkillMarketHandler {
	return &SkillMarketHandler{service: svc}
}

// RegisterSkillMarketRoutes 注册 skill 市场路由。
// 路由：GET /api/v1/skill-market、GET /api/v1/skill-market/detail
func RegisterSkillMarketRoutes(router gin.IRouter, h *SkillMarketHandler) {
	router.GET("/api/v1/skill-market", h.List)
	router.GET("/api/v1/skill-market/detail", h.Detail)
}

// List 浏览/搜索 skill 市场。
//
// @Summary  浏览/搜索 skill 市场
// @Tags     skill-market
// @Produce  json
// @Security BearerAuth
// @Param    source query string false "来源过滤：platform | clawhub | （空=聚合）"
// @Param    q      query string false "关键词搜索"
// @Param    cursor query string false "分页游标（clawhub 分页使用，platform 忽略）"
// @Success  200 {object} map[string]service.SkillPage
// @Failure  400 {object} ErrorResponse
// @Failure  500 {object} ErrorResponse
// @Router   /skill-market [get]
func (h *SkillMarketHandler) List(c *gin.Context) {
	// 从 query string 读取三个可选参数；缺失时为空字符串（service 层处理默认行为）。
	source := c.Query("source")
	q := c.Query("q")
	cursor := c.Query("cursor")

	page, err := h.service.List(c.Request.Context(), principalFromCtx(c), source, q, cursor)
	if err != nil {
		writeSkillMarketError(c, err)
		return
	}
	// 统一用 "page" key 包装，保持与其他列表接口一致的响应结构。
	c.JSON(http.StatusOK, gin.H{"page": page})
}

// Detail 返回指定 skill 的富详情与版本列表，供详情抽屉展示。
//
// @Summary  查询某 skill 的详情与版本列表
// @Tags     skill-market
// @Produce  json
// @Security BearerAuth
// @Param    source query string true  "来源：platform | clawhub"
// @Param    ref    query string true  "来源内标识：platform=name，clawhub=slug"
// @Success  200 {object} map[string]interface{}
// @Failure  400 {object} ErrorResponse
// @Failure  500 {object} ErrorResponse
// @Router   /skill-market/detail [get]
func (h *SkillMarketHandler) Detail(c *gin.Context) {
	source := c.Query("source")
	ref := c.Query("ref")
	detail, versions, err := h.service.Detail(c.Request.Context(), principalFromCtx(c), source, ref)
	if err != nil {
		writeSkillMarketError(c, err)
		return
	}
	// detail 富信息 + versions 版本列表；versions 为空数组也照常返回。
	c.JSON(http.StatusOK, gin.H{"detail": detail, "versions": versions})
}

// writeSkillMarketError 把市场哨兵错误映射为 HTTP 状态码与固定文案错误体。
// 不回传 err.Error() 原始字符串，避免泄露内部实现细节。
func writeSkillMarketError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrSkillMarketSourceUnknown):
		// 未知来源是客户端入参错误，映射为 400 Bad Request。
		c.JSON(http.StatusBadRequest, apierror.New("INVALID_REQUEST", "未知的 skill 来源"))
	default:
		// 其他错误（网络、数据库等）映射为 500 Internal Server Error。
		c.JSON(http.StatusInternalServerError, apierror.New("INTERNAL", "服务器内部错误"))
	}
}
