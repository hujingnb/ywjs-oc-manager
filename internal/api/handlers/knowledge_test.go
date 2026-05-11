package handlers

import (
	"bytes"
	"context"
	"io"
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

// knowledgeServiceStub 实现 knowledgeService 接口，仅 stub 测试用到的方法。
type knowledgeServiceStub struct {
	syncStatuses  []service.SyncStatusResult
	syncErr       error
	retryErr      error
	listOrgResult service.KnowledgeListResult
	listOrgErr    error
	saveOrgErr    error
	deleteOrgErr  error
	listAppResult service.KnowledgeListResult
	listAppErr    error
	saveAppErr    error
	deleteAppErr  error
}

func (s *knowledgeServiceStub) GetOrgSyncStatus(_ context.Context, _ auth.Principal, _ string) ([]service.SyncStatusResult, error) {
	return s.syncStatuses, s.syncErr
}

func (s *knowledgeServiceStub) RetryOrgNodeSync(_ context.Context, _ auth.Principal, _, _ string) error {
	return s.retryErr
}

func (s *knowledgeServiceStub) ListOrg(_ context.Context, _ auth.Principal, _, _ string) (service.KnowledgeListResult, error) {
	return s.listOrgResult, s.listOrgErr
}

func (s *knowledgeServiceStub) SaveOrgFile(_ context.Context, _ auth.Principal, _, _ string, _ io.Reader, _ int64) error {
	return s.saveOrgErr
}

func (s *knowledgeServiceStub) DeleteOrgFile(_ context.Context, _ auth.Principal, _, _ string) error {
	return s.deleteOrgErr
}

func (s *knowledgeServiceStub) ListApp(_ context.Context, _ auth.Principal, _, _, _, _ string) (service.KnowledgeListResult, error) {
	return s.listAppResult, s.listAppErr
}

func (s *knowledgeServiceStub) SaveAppFile(_ context.Context, _ auth.Principal, _, _, _, _ string, _ io.Reader, _ int64) error {
	return s.saveAppErr
}

func (s *knowledgeServiceStub) DeleteAppFile(_ context.Context, _ auth.Principal, _, _, _, _ string) error {
	return s.deleteAppErr
}

// newKnowledgeTestRouter 构建用于测试的 gin router + token manager。
func newKnowledgeTestRouter(t *testing.T, svc knowledgeService) (*gin.Engine, *auth.TokenManager) {
	t.Helper()
	gin.SetMode(gin.ReleaseMode)
	tokens, err := auth.NewTokenManager("a", "b", time.Minute, time.Hour)
	require.NoError(t, err)
	router := gin.New()
	RegisterKnowledgeRoutes(router, NewKnowledgeHandler(svc, tokens))
	return router, tokens
}

func TestKnowledgeGetOrgSyncStatusHappy(t *testing.T) {
	stub := &knowledgeServiceStub{syncStatuses: []service.SyncStatusResult{{NodeID: "n1", Status: "ok"}}}
	router, tokens := newKnowledgeTestRouter(t, stub)
	token := mustSignAccess(t, tokens, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgAdmin, OrgID: "org-1"})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/organizations/org-1/knowledge/sync-status", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "statuses")
}

func TestKnowledgeGetOrgSyncStatusForbidden(t *testing.T) {
	stub := &knowledgeServiceStub{syncErr: service.ErrKnowledgeForbidden}
	router, tokens := newKnowledgeTestRouter(t, stub)
	token := mustSignAccess(t, tokens, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgMember, OrgID: "org-1"})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/organizations/org-1/knowledge/sync-status", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestKnowledgeListOrgHappy(t *testing.T) {
	stub := &knowledgeServiceStub{listOrgResult: service.KnowledgeListResult{}}
	router, tokens := newKnowledgeTestRouter(t, stub)
	token := mustSignAccess(t, tokens, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgAdmin, OrgID: "org-1"})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/organizations/org-1/knowledge", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
}

func TestKnowledgeSaveOrgHappy(t *testing.T) {
	stub := &knowledgeServiceStub{}
	router, tokens := newKnowledgeTestRouter(t, stub)
	token := mustSignAccess(t, tokens, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgAdmin, OrgID: "org-1"})

	w := httptest.NewRecorder()
	body := bytes.NewBufferString("file content")
	req := httptest.NewRequest(http.MethodPost, "/api/v1/organizations/org-1/knowledge?path=docs/readme.txt", body)
	req.Header.Set("Authorization", "Bearer "+token)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNoContent, w.Code)
}

func TestKnowledgeSaveOrgMissingPath(t *testing.T) {
	stub := &knowledgeServiceStub{}
	router, tokens := newKnowledgeTestRouter(t, stub)
	token := mustSignAccess(t, tokens, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgAdmin, OrgID: "org-1"})

	w := httptest.NewRecorder()
	body := bytes.NewBufferString("file content")
	// 缺少必填 path 参数
	req := httptest.NewRequest(http.MethodPost, "/api/v1/organizations/org-1/knowledge", body)
	req.Header.Set("Authorization", "Bearer "+token)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestKnowledgeRetryOrgSyncHappy(t *testing.T) {
	stub := &knowledgeServiceStub{}
	router, tokens := newKnowledgeTestRouter(t, stub)
	token := mustSignAccess(t, tokens, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgAdmin, OrgID: "org-1"})

	w := httptest.NewRecorder()
	body := bytes.NewBufferString(`{"node_id":"n1"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/organizations/org-1/knowledge/sync-status/retry", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusAccepted, w.Code)
}

func TestKnowledgeRequiresToken(t *testing.T) {
	stub := &knowledgeServiceStub{}
	router, _ := newKnowledgeTestRouter(t, stub)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/organizations/org-1/knowledge", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestKnowledgeListAppHappy(t *testing.T) {
	stub := &knowledgeServiceStub{listAppResult: service.KnowledgeListResult{}}
	router, tokens := newKnowledgeTestRouter(t, stub)
	token := mustSignAccess(t, tokens, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgMember, OrgID: "org-1"})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/apps/app-1/knowledge?org_id=org-1&owner_user_id=u1", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
}

func TestKnowledgeListAppMissingParams(t *testing.T) {
	stub := &knowledgeServiceStub{}
	router, tokens := newKnowledgeTestRouter(t, stub)
	token := mustSignAccess(t, tokens, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgMember, OrgID: "org-1"})

	w := httptest.NewRecorder()
	// 缺少 owner_user_id
	req := httptest.NewRequest(http.MethodGet, "/api/v1/apps/app-1/knowledge?org_id=org-1", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}
