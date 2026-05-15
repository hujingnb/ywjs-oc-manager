package handlers

import (
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

type modelServiceStub struct {
	models []service.ModelResult
	err    error
}

func (s *modelServiceStub) List(context.Context, auth.Principal) ([]service.ModelResult, error) {
	return s.models, s.err
}

// newModelsTestRouter 构建模型目录 handler 测试专用路由。
func newModelsTestRouter(t *testing.T, svc modelService) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	RegisterModelRoutes(router, NewModelsHandler(svc))
	return router
}

// TestModelsListReturnsCatalog 验证平台管理员可通过 manager 读取实时模型列表。
func TestModelsListReturnsCatalog(t *testing.T) {
	t.Parallel()
	svc := &modelServiceStub{models: []service.ModelResult{{ID: "qwen", Name: "qwen"}}}
	router := newModelsTestRouter(t, svc)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/models", nil)
	req = withPrincipal(req, auth.Principal{UserID: "u-1", Role: domain.UserRolePlatformAdmin})
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)
	require.Equal(t, http.StatusOK, resp.Code)
	assert.JSONEq(t, `{"models":[{"id":"qwen","name":"qwen"}]}`, resp.Body.String())
}

// TestModelsListMapsForbidden 验证非平台管理员读取模型列表时返回 403。
func TestModelsListMapsForbidden(t *testing.T) {
	t.Parallel()
	svc := &modelServiceStub{err: service.ErrForbidden}
	router := newModelsTestRouter(t, svc)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/models", nil)
	req = withPrincipal(req, auth.Principal{UserID: "u-1", Role: domain.UserRoleOrgAdmin})
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)
	require.Equal(t, http.StatusForbidden, resp.Code)
}
