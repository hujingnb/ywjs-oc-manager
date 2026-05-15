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

// personaServiceStub 实现 personaService 接口，仅 stub 测试用到的方法。
type personaServiceStub struct {
	getResult service.PersonaResult
	getErr    error
	putResult service.PersonaResult
	putErr    error
}

func (s *personaServiceStub) GetCurrent(_ context.Context, _ auth.Principal, _ string) (service.PersonaResult, error) {
	return s.getResult, s.getErr
}

func (s *personaServiceStub) Replace(_ context.Context, _ auth.Principal, _ string, _ service.PersonaInput) (service.PersonaResult, error) {
	return s.putResult, s.putErr
}

// newPersonaTestRouter 构建用于测试的 gin router。
func newPersonaTestRouter(t *testing.T, svc personaService) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	RegisterPersonaRoutes(router, NewPersonaHandler(svc))
	return router
}

// TestPersonaGetHappy 验证人设获取成功路径的成功路径场景。
func TestPersonaGetHappy(t *testing.T) {
	stub := &personaServiceStub{getResult: service.PersonaResult{SystemPrompt: "你好"}}
	router := newPersonaTestRouter(t, stub)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/orgs/org-1/persona", nil)
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgAdmin, OrgID: "org-1"})
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "persona")
}

// TestPersonaGetForbidden 验证人设获取禁止访问的异常或拒绝路径场景。
func TestPersonaGetForbidden(t *testing.T) {
	stub := &personaServiceStub{getErr: service.ErrPersonaDenied}
	router := newPersonaTestRouter(t, stub)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/orgs/org-2/persona", nil)
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgMember, OrgID: "org-1"})
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

// TestPersonaGetNotFound 验证人设获取未找到的异常或拒绝路径场景。
func TestPersonaGetNotFound(t *testing.T) {
	stub := &personaServiceStub{getErr: service.ErrPersonaNotFound}
	router := newPersonaTestRouter(t, stub)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/orgs/org-1/persona", nil)
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgAdmin, OrgID: "org-1"})
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

// TestPersonaPutHappy 验证人设更新成功路径的成功路径场景。
func TestPersonaPutHappy(t *testing.T) {
	stub := &personaServiceStub{putResult: service.PersonaResult{SystemPrompt: "新人设"}}
	router := newPersonaTestRouter(t, stub)

	w := httptest.NewRecorder()
	body := bytes.NewBufferString(`{"system_prompt":"新人设"}`)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/orgs/org-1/persona", body)
	req.Header.Set("Content-Type", "application/json")
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgAdmin, OrgID: "org-1"})
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "persona")
}

