// Package handlers 的 audit_test 覆盖组织审计与应用审计 handler 的权限、分页和错误映射。
package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/stretchr/testify/require"
	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/service"
)

// TestAuditListByOrgPropagatesPrincipal 验证审计列表通过组织透传Principal的错误映射或错误记录场景。
func TestAuditListByOrgPropagatesPrincipal(t *testing.T) {
	svc := &auditServiceStub{byOrg: []service.AuditResult{{Action: "create"}}}
	router := newAuditTestRouter(t, svc)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/organizations/o1/audit-logs?limit=10", nil)
	request = withPrincipal(request, auth.Principal{UserID: "u1", OrgID: "o1", Role: domain.UserRoleOrgAdmin})
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

// TestAuditListByTargetRequiresParams 验证审计列表通过目标要求参数的预期行为场景。
func TestAuditListByTargetRequiresParams(t *testing.T) {
	router := newAuditTestRouter(t, &auditServiceStub{})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/audit-logs", nil)
	request = withPrincipal(request, auth.Principal{UserID: "u1", Role: domain.UserRolePlatformAdmin})
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusBadRequest, recorder.Code)
}

func newAuditTestRouter(t *testing.T, svc auditService) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	RegisterAuditRoutes(router, NewAuditHandler(svc))
	return router
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
