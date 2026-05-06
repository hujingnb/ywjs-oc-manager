package handlers

import (
	"context"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

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
}

// RechargeHandler 把组织充值与余额查询接口暴露给前端。
type RechargeHandler struct {
	service rechargeService
	tokens  *auth.TokenManager
}

// NewRechargeHandler 创建 handler。
func NewRechargeHandler(svc rechargeService, tokens *auth.TokenManager) *RechargeHandler {
	return &RechargeHandler{service: svc, tokens: tokens}
}

// RegisterRechargeRoutes 注册充值相关路由。
func RegisterRechargeRoutes(router gin.IRouter, handler *RechargeHandler) {
	router.POST("/api/v1/organizations/:orgId/recharge", handler.Create)
	router.GET("/api/v1/organizations/:orgId/recharges", handler.List)
	router.GET("/api/v1/organizations/:orgId/balance", handler.Balance)
}

type rechargeRequest struct {
	CreditAmount int64  `json:"credit_amount" binding:"required"`
	Remark       string `json:"remark"`
}

// Create 处理充值。
func (h *RechargeHandler) Create(c *gin.Context) {
	principal, ok := h.principal(c)
	if !ok {
		return
	}
	var req rechargeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求参数不完整"})
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
func (h *RechargeHandler) List(c *gin.Context) {
	principal, ok := h.principal(c)
	if !ok {
		return
	}
	limit := queryInt32(c, "limit", 50)
	offset := queryInt32(c, "offset", 0)
	results, err := h.service.ListRecharges(c.Request.Context(), principal, c.Param("orgId"), limit, offset)
	if err != nil {
		writeRechargeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"recharges": results})
}

// Balance 查询组织余额。
func (h *RechargeHandler) Balance(c *gin.Context) {
	principal, ok := h.principal(c)
	if !ok {
		return
	}
	view, err := h.service.GetBalance(c.Request.Context(), principal, c.Param("orgId"))
	if err != nil {
		writeRechargeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"balance": view})
}

func (h *RechargeHandler) principal(c *gin.Context) (auth.Principal, bool) {
	token, ok := bearerToken(c.GetHeader("Authorization"))
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "缺少访问令牌"})
		return auth.Principal{}, false
	}
	principal, err := h.tokens.VerifyAccessToken(token)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "访问令牌无效"})
		return auth.Principal{}, false
	}
	return principal, true
}

func writeRechargeError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrRechargeDenied), errors.Is(err, service.ErrForbidden):
		c.JSON(http.StatusForbidden, gin.H{"error": "无权执行该操作"})
	case errors.Is(err, service.ErrNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": "组织不存在"})
	case errors.Is(err, service.ErrInvalidRechargeAmount):
		c.JSON(http.StatusBadRequest, gin.H{"error": "充值金额必须为正"})
	case errors.Is(err, service.ErrOrgMissingNewAPIUserID):
		c.JSON(http.StatusConflict, gin.H{"error": "组织未关联 new-api 账户"})
	default:
		c.JSON(http.StatusBadGateway, gin.H{"error": redactlog.SafeErrorMessage(err)})
	}
}
