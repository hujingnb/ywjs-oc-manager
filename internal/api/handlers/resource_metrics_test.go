package handlers

import (
	"context"
	"encoding/json"
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

// TestResourceMetricsHandlerRejectsOrgMemberForNodeResources 验证组织成员不能读取平台节点资源指标。
func TestResourceMetricsHandlerRejectsOrgMemberForNodeResources(t *testing.T) {
	stub := &resourceMetricsServiceStub{nodeResourcesErr: service.ErrForbidden}
	router := newResourceMetricsTestRouter(t, stub)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/runtime-nodes/node-1/resources", nil)
	request = withPrincipal(request, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgMember, OrgID: "org-1"})
	router.ServeHTTP(recorder, request)

	assert.Equal(t, http.StatusForbidden, recorder.Code)
	assert.Equal(t, domain.UserRoleOrgMember, stub.nodeResourcesPrincipal.Role)
}

// TestResourceMetricsHandlerReturnsAppResources 验证允许访问应用时返回 samples 数组包装。
func TestResourceMetricsHandlerReturnsAppResources(t *testing.T) {
	stub := &resourceMetricsServiceStub{
		appResourcesResult: []service.InstanceResourceSampleResult{{SampledAt: "2026-05-13T01:02:03Z"}},
	}
	router := newResourceMetricsTestRouter(t, stub)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/apps/app-1/resources", nil)
	request = withPrincipal(request, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgAdmin, OrgID: "org-1"})
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	var response struct {
		Samples []service.InstanceResourceSampleResult `json:"samples"`
	}
	err := json.Unmarshal(recorder.Body.Bytes(), &response)
	require.NoError(t, err)
	require.Len(t, response.Samples, 1)
	assert.Equal(t, "2026-05-13T01:02:03Z", response.Samples[0].SampledAt)
	assert.Equal(t, "app-1", stub.appResourcesAppID)
}

// newResourceMetricsTestRouter 构造资源指标 handler 测试专用路由。
func newResourceMetricsTestRouter(t *testing.T, svc resourceMetricsService) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	RegisterResourceMetricsRoutes(router, NewResourceMetricsHandler(svc))
	return router
}

// resourceMetricsServiceStub 只实现资源指标 handler 测试覆盖到的 service 方法。
type resourceMetricsServiceStub struct {
	nodeResourcesResult         []service.NodeResourceSampleResult
	nodeResourcesErr            error
	nodeResourcesPrincipal      auth.Principal
	nodeInstancesResult         []service.NodeInstanceResult
	nodeInstancesErr            error
	nodeInstanceResourcesResult []service.InstanceResourceSampleResult
	nodeInstanceResourcesErr    error
	appResourcesResult          []service.InstanceResourceSampleResult
	appResourcesErr             error
	appResourcesAppID           string
}

func (s *resourceMetricsServiceStub) ListNodeResources(_ context.Context, principal auth.Principal, _ string, _ service.ResourceTimeRange) ([]service.NodeResourceSampleResult, error) {
	s.nodeResourcesPrincipal = principal
	return s.nodeResourcesResult, s.nodeResourcesErr
}

func (s *resourceMetricsServiceStub) ListNodeInstances(_ context.Context, _ auth.Principal, _ string, _, _ int32) ([]service.NodeInstanceResult, error) {
	return s.nodeInstancesResult, s.nodeInstancesErr
}

func (s *resourceMetricsServiceStub) ListNodeInstanceResources(_ context.Context, _ auth.Principal, _, _ string, _ service.ResourceTimeRange) ([]service.InstanceResourceSampleResult, error) {
	return s.nodeInstanceResourcesResult, s.nodeInstanceResourcesErr
}

func (s *resourceMetricsServiceStub) ListAppResources(_ context.Context, _ auth.Principal, appID string, _ service.ResourceTimeRange) ([]service.InstanceResourceSampleResult, error) {
	s.appResourcesAppID = appID
	return s.appResourcesResult, s.appResourcesErr
}
