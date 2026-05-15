package handlers

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/service"
)

// rechargeServiceStub 实现 rechargeService 接口，仅 stub 测试用到的方法。
type rechargeServiceStub struct {
	rechargeResult      service.RechargeRecordResult
	rechargeErr         error
	listResult          []service.RechargeRecordResult
	listErr             error
	balanceResult       service.BalanceView
	balanceErr          error
	billingStatusResult service.BillingStatusView
	billingStatusErr    error
}

func (s *rechargeServiceStub) Recharge(_ context.Context, _ auth.Principal, _ string, _ int64, _ string) (service.RechargeRecordResult, error) {
	return s.rechargeResult, s.rechargeErr
}

func (s *rechargeServiceStub) ListRecharges(_ context.Context, _ auth.Principal, _ string, _, _ int32) ([]service.RechargeRecordResult, error) {
	return s.listResult, s.listErr
}

func (s *rechargeServiceStub) GetBalance(_ context.Context, _ auth.Principal, _ string) (service.BalanceView, error) {
	return s.balanceResult, s.balanceErr
}

func (s *rechargeServiceStub) GetBillingStatus(_ context.Context, _ auth.Principal) (service.BillingStatusView, error) {
	return s.billingStatusResult, s.billingStatusErr
}

// newRechargeTestRouter 构建用于测试的 gin router。
func newRechargeTestRouter(t *testing.T, svc rechargeService) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	RegisterRechargeRoutes(router, NewRechargeHandler(svc))
	return router
}

// TestRechargeCreateHappy 验证充值创建成功路径的成功路径场景。
func TestRechargeCreateHappy(t *testing.T) {
	stub := &rechargeServiceStub{rechargeResult: service.RechargeRecordResult{ID: "rec-1"}}
	router := newRechargeTestRouter(t, stub)

	w := httptest.NewRecorder()
	body := bytes.NewBufferString(`{"credit_amount":1000,"remark":"测试充值"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/organizations/org-1/recharge", body)
	req.Header.Set("Content-Type", "application/json")
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRolePlatformAdmin})
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "recharge")
}

// TestRechargeCreateForbidden 验证充值创建禁止访问的异常或拒绝路径场景。
func TestRechargeCreateForbidden(t *testing.T) {
	stub := &rechargeServiceStub{rechargeErr: service.ErrRechargeDenied}
	router := newRechargeTestRouter(t, stub)

	w := httptest.NewRecorder()
	body := bytes.NewBufferString(`{"credit_amount":1000}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/organizations/org-1/recharge", body)
	req.Header.Set("Content-Type", "application/json")
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgAdmin, OrgID: "org-1"})
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

// TestRechargeCreateOrgNotFound 验证充值创建组织未找到的异常或拒绝路径场景。
func TestRechargeCreateOrgNotFound(t *testing.T) {
	stub := &rechargeServiceStub{rechargeErr: service.ErrNotFound}
	router := newRechargeTestRouter(t, stub)

	w := httptest.NewRecorder()
	body := bytes.NewBufferString(`{"credit_amount":500}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/organizations/missing/recharge", body)
	req.Header.Set("Content-Type", "application/json")
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRolePlatformAdmin})
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

// TestRechargeListHappy 验证充值列表成功路径的成功路径场景。
func TestRechargeListHappy(t *testing.T) {
	stub := &rechargeServiceStub{listResult: []service.RechargeRecordResult{{ID: "rec-1"}}}
	router := newRechargeTestRouter(t, stub)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/organizations/org-1/recharges", nil)
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRolePlatformAdmin})
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "recharges")
}

// TestRechargeBalanceHappy 验证充值余额成功路径的成功路径场景。
func TestRechargeBalanceHappy(t *testing.T) {
	stub := &rechargeServiceStub{balanceResult: service.BalanceView{RemainQuota: 5000}}
	router := newRechargeTestRouter(t, stub)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/organizations/org-1/balance", nil)
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgAdmin, OrgID: "org-1"})
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "balance")
}

// TestBillingStatusHappy 验证 billing status 路由返回 new-api 展示配置。
func TestBillingStatusHappy(t *testing.T) {
	stub := &rechargeServiceStub{billingStatusResult: service.BillingStatusView{
		QuotaPerUnit:      500000,
		QuotaDisplayType:  "USD",
		DisplayInCurrency: true,
	}}
	router := newRechargeTestRouter(t, stub)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/billing/status", nil)
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRolePlatformAdmin})
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "quota_per_unit")
}

