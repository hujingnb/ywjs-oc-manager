package handlers

import (
	"context"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"oc-manager/internal/api/apierror"
	"oc-manager/internal/auth"
	redactlog "oc-manager/internal/log"
	"oc-manager/internal/service"
)

// rechargeService 是 handler 与 service.RechargeService 的最小契约。
// 抽出接口便于在测试中注入桩，handler 单元测试不依赖 new-api。
type rechargeService interface {
	Recharge(ctx context.Context, principal auth.Principal, orgID string, amount int64, remark string) (service.RechargeRecordResult, error)
	ListRecharges(ctx context.Context, principal auth.Principal, orgID string, limit, offset int32) ([]service.RechargeRecordResult, error)
	GetBalance(ctx context.Context, principal auth.Principal, orgID string) (service.BalanceView, error)
	GetBillingStatus(ctx context.Context, principal auth.Principal) (service.BillingStatusView, error)
}

// RechargeHandler 把组织充值与余额查询接口暴露给前端。
type RechargeHandler struct {
	service rechargeService
}

// NewRechargeHandler 创建 handler。
func NewRechargeHandler(svc rechargeService) *RechargeHandler {
	return &RechargeHandler{service: svc}
}

// RegisterRechargeRoutes 注册充值相关路由。
func RegisterRechargeRoutes(router gin.IRouter, handler *RechargeHandler) {
	router.POST("/api/v1/organizations/:orgId/recharge", handler.Create)
	router.GET("/api/v1/organizations/:orgId/recharges", handler.List)
	router.GET("/api/v1/organizations/:orgId/balance", handler.Balance)
	router.GET("/api/v1/billing/status", handler.BillingStatus)
}

// Create 处理充值。
//
// @Summary      企业充值
// @Description  平台管理员为指定企业充值额度；企业须已关联 new-api 账户
// @Tags         recharge
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        orgId  path      string          true  "企业 ID"
// @Param        body   body      RechargeRequest true  "充值请求"
// @Success      200    {object}  map[string]service.RechargeRecordResult
// @Failure      400    {object}  ErrorResponse
// @Failure      401    {object}  ErrorResponse
// @Failure      403    {object}  ErrorResponse
// @Failure      404    {object}  ErrorResponse
// @Failure      409    {object}  ErrorResponse
// @Failure      502    {object}  ErrorResponse
// @Router       /organizations/{orgId}/recharge [post]
func (h *RechargeHandler) Create(c *gin.Context) {
	principal := principalFromCtx(c)
	var req RechargeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeBindError(c, err)
		return
	}
	result, err := h.service.Recharge(c.Request.Context(), principal, c.Param("orgId"), req.CreditAmount, req.Remark)
	if err != nil {
		writeRechargeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"recharge": result})
}

// List 列出充值历史。
//
// @Summary      企业充值历史列表
// @Description  分页查询指定企业的充值记录
// @Tags         recharge
// @Produce      json
// @Security     BearerAuth
// @Param        orgId   path      string  true   "企业 ID"
// @Param        limit   query     int     false  "每页条数（默认 50）"
// @Param        offset  query     int     false  "分页偏移（默认 0）"
// @Success      200     {object}  map[string][]service.RechargeRecordResult
// @Failure      401     {object}  ErrorResponse
// @Failure      403     {object}  ErrorResponse
// @Failure      404     {object}  ErrorResponse
// @Failure      502     {object}  ErrorResponse
// @Router       /organizations/{orgId}/recharges [get]
func (h *RechargeHandler) List(c *gin.Context) {
	principal := principalFromCtx(c)
	limit := queryInt32(c, "limit", 50)
	offset := queryInt32(c, "offset", 0)
	results, err := h.service.ListRecharges(c.Request.Context(), principal, c.Param("orgId"), limit, offset)
	if err != nil {
		writeRechargeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"recharges": results})
}

// Balance 查询企业余额。
//
// @Summary      查询企业余额
// @Description  查询指定企业在 new-api 中的当前额度余额
// @Tags         recharge
// @Produce      json
// @Security     BearerAuth
// @Param        orgId  path      string  true  "企业 ID"
// @Success      200    {object}  map[string]service.BalanceView
// @Failure      401    {object}  ErrorResponse
// @Failure      403    {object}  ErrorResponse
// @Failure      404    {object}  ErrorResponse
// @Failure      502    {object}  ErrorResponse
// @Router       /organizations/{orgId}/balance [get]
func (h *RechargeHandler) Balance(c *gin.Context) {
	principal := principalFromCtx(c)
	view, err := h.service.GetBalance(c.Request.Context(), principal, c.Param("orgId"))
	if err != nil {
		writeRechargeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"balance": view})
}

// BillingStatus 查询 new-api 金额 / 额度展示配置。
//
// @Summary      查询计费展示配置
// @Description  透传 new-api /api/status 中用于余额、用量和充值展示的配置；manager 不维护 token 单价
// @Tags         recharge
// @Produce      json
// @Security     BearerAuth
// @Success      200  {object}  map[string]service.BillingStatusView
// @Failure      401  {object}  ErrorResponse
// @Failure      403  {object}  ErrorResponse
// @Failure      502  {object}  ErrorResponse
// @Router       /billing/status [get]
func (h *RechargeHandler) BillingStatus(c *gin.Context) {
	principal := principalFromCtx(c)
	view, err := h.service.GetBillingStatus(c.Request.Context(), principal)
	if err != nil {
		writeRechargeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"billing_status": view})
}

func writeRechargeError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrRechargeDenied), errors.Is(err, service.ErrForbidden):
		c.JSON(http.StatusForbidden, apierror.New("RECHARGE_FORBIDDEN", "无权执行该操作"))
	case errors.Is(err, service.ErrNotFound):
		c.JSON(http.StatusNotFound, apierror.New("NOT_FOUND", "企业不存在"))
	case errors.Is(err, service.ErrInvalidRechargeAmount):
		c.JSON(http.StatusBadRequest, apierror.New("INVALID_RECHARGE_AMOUNT", "充值金额必须为正"))
	case errors.Is(err, service.ErrOrgMissingNewAPIUserID):
		c.JSON(http.StatusConflict, apierror.New("ORG_MISSING_NEWAPI_USER", "企业未关联 new-api 账户"))
	default:
		c.JSON(http.StatusBadGateway, apierror.New("INTERNAL", redactlog.SafeErrorMessage(err)))
	}
}
