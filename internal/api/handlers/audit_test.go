package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/service"
	"github.com/stretchr/testify/require"
)

func TestAuditListByOrgRequiresToken(t *testing.T) {
	router, _ := newAuditTestRouter(t, &auditServiceStub{})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/organizations/o1/audit-logs", nil)
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusUnauthorized, recorder.Code)
}

func TestAuditListByOrgPropagatesPrincipal(t *testing.T) {
	svc := &auditServiceStub{byOrg: []service.AuditResult{{Action: "create"}}}
	router, tokens := newAuditTestRouter(t, svc)
	token := mustSignAccess(t, tokens, auth.Principal{UserID: "u1", OrgID: "o1", Role: domain.UserRoleOrgAdmin})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/organizations/o1/audit-logs?limit=10", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	var resp struct {
		Logs []service.AuditResult `json:"audit_logs"`
	}
	err := json.Unmarshal(recorder.Body.Bytes(), &resp)
	require.NoError(t, err)
	if len(resp.Logs) != 1 || resp.Logs[0].Action != "create" {
		t.Fatalf("logs = %+v", resp.Logs)
	}
	if svc.lastOrgID != "o1" || svc.lastLimit != 10 {
		t.Fatalf("forward = %s/%d", svc.lastOrgID, svc.lastLimit)
	}
}

func TestAuditListByTargetRequiresParams(t *testing.T) {
	router, tokens := newAuditTestRouter(t, &auditServiceStub{})
	token := mustSignAccess(t, tokens, auth.Principal{UserID: "u1", Role: domain.UserRolePlatformAdmin})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/audit-logs", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusBadRequest, recorder.Code)
}

func newAuditTestRouter(t *testing.T, svc auditService) (*gin.Engine, *auth.TokenManager) {
	t.Helper()
	gin.SetMode(gin.ReleaseMode)
	tokens, err := auth.NewTokenManager("a", "b", time.Minute, time.Hour)
	require.NoError(t, err)
	router := gin.New()
	RegisterAuditRoutes(router, NewAuditHandler(svc, tokens))
	return router, tokens
}

type auditServiceStub struct {
	byOrg     []service.AuditResult
	byTarget  []service.AuditResult
	lastOrgID string
	lastLimit int32
}

func (s *auditServiceStub) ListByOrg(_ context.Context, _ auth.Principal, orgID string, limit, _ int32) ([]service.AuditResult, error) {
	s.lastOrgID = orgID
	s.lastLimit = limit
	return s.byOrg, nil
}

func (s *auditServiceStub) ListByTarget(_ context.Context, _ auth.Principal, _, _ string, _, _ int32) ([]service.AuditResult, error) {
	return s.byTarget, nil
}
