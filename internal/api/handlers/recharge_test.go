package handlers

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/service"
)

// rechargeServiceStub 实现 rechargeService 接口，仅 stub 测试用到的方法。
type rechargeServiceStub struct {
	rechargeResult service.RechargeRecordResult
	rechargeErr    error
	listResult     []service.RechargeRecordResult
	listErr        error
	balanceResult  service.BalanceView
	balanceErr     error
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

// newRechargeTestRouter 构建用于测试的 gin router + token manager。
func newRechargeTestRouter(t *testing.T, svc rechargeService) (*gin.Engine, *auth.TokenManager) {
	t.Helper()
	gin.SetMode(gin.ReleaseMode)
	tokens, err := auth.NewTokenManager("a", "b", time.Minute, time.Hour)
	require.NoError(t, err)
	router := gin.New()
	RegisterRechargeRoutes(router, NewRechargeHandler(svc, tokens))
	return router, tokens
}

func TestRechargeCreateHappy(t *testing.T) {
	stub := &rechargeServiceStub{rechargeResult: service.RechargeRecordResult{ID: "rec-1"}}
	router, tokens := newRechargeTestRouter(t, stub)
	token := mustSignAccess(t, tokens, auth.Principal{UserID: "u1", Role: domain.UserRolePlatformAdmin})

	w := httptest.NewRecorder()
	body := bytes.NewBufferString(`{"credit_amount":1000,"remark":"测试充值"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/organizations/org-1/recharge", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "recharge")
}

func TestRechargeCreateForbidden(t *testing.T) {
	stub := &rechargeServiceStub{rechargeErr: service.ErrRechargeDenied}
	router, tokens := newRechargeTestRouter(t, stub)
	token := mustSignAccess(t, tokens, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgAdmin, OrgID: "org-1"})

	w := httptest.NewRecorder()
	body := bytes.NewBufferString(`{"credit_amount":1000}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/organizations/org-1/recharge", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestRechargeCreateOrgNotFound(t *testing.T) {
	stub := &rechargeServiceStub{rechargeErr: service.ErrNotFound}
	router, tokens := newRechargeTestRouter(t, stub)
	token := mustSignAccess(t, tokens, auth.Principal{UserID: "u1", Role: domain.UserRolePlatformAdmin})

	w := httptest.NewRecorder()
	body := bytes.NewBufferString(`{"credit_amount":500}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/organizations/missing/recharge", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestRechargeListHappy(t *testing.T) {
	stub := &rechargeServiceStub{listResult: []service.RechargeRecordResult{{ID: "rec-1"}}}
	router, tokens := newRechargeTestRouter(t, stub)
	token := mustSignAccess(t, tokens, auth.Principal{UserID: "u1", Role: domain.UserRolePlatformAdmin})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/organizations/org-1/recharges", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "recharges")
}

func TestRechargeBalanceHappy(t *testing.T) {
	stub := &rechargeServiceStub{balanceResult: service.BalanceView{RemainQuota: 5000}}
	router, tokens := newRechargeTestRouter(t, stub)
	token := mustSignAccess(t, tokens, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgAdmin, OrgID: "org-1"})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/organizations/org-1/balance", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "balance")
}

func TestRechargeRequiresToken(t *testing.T) {
	stub := &rechargeServiceStub{}
	router, _ := newRechargeTestRouter(t, stub)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/organizations/org-1/balance", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}
