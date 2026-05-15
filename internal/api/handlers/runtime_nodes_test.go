// Package handlers 的 runtime_nodes_test 覆盖运行节点管理 handler 的注册、更新和列表响应。
package handlers

import (
	"bytes"
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

// TestRuntimeNodesCreateRouteRemoved 验证运行时节点创建路由移除d的预期行为场景。
func TestRuntimeNodesCreateRouteRemoved(t *testing.T) {
	router := newRuntimeNodesTestRouter(t, &runtimeNodeServiceStub{})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/runtime-nodes", bytes.NewBufferString(`{"name":"node"}`))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusNotFound, recorder.Code)
}

// TestRuntimeNodesListReturnsArray 验证运行时节点列表返回数组的成功路径场景。
func TestRuntimeNodesListReturnsArray(t *testing.T) {
	stub := &runtimeNodeServiceStub{listResult: []service.RuntimeNodeResult{{ID: "n1", Name: "node-1", Status: domain.RuntimeNodeStatusActive}}}
	router := newRuntimeNodesTestRouter(t, stub)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/runtime-nodes", nil)
	request = withPrincipal(request, auth.Principal{UserID: "u1", Role: domain.UserRolePlatformAdmin})
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	var resp struct {
		Nodes []service.RuntimeNodeResult `json:"runtime_nodes"`
	}
	err := json.Unmarshal(recorder.Body.Bytes(), &resp)
	require.NoError(t, err)
	require.Len(t, resp.Nodes, 1)
	require.Equal(t, "node-1", resp.Nodes[0].Name)
}

func newRuntimeNodesTestRouter(t *testing.T, svc runtimeNodeService) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	RegisterRuntimeNodeRoutes(router, NewRuntimeNodesHandler(svc))
	return router
}

type runtimeNodeServiceStub struct {
	listResult   []service.RuntimeNodeResult
	getResult    service.RuntimeNodeResult
	statusResult service.RuntimeNodeResult
}

func (s *runtimeNodeServiceStub) ListNodes(_ context.Context, _ auth.Principal, _, _ int32) ([]service.RuntimeNodeResult, error) {
	return s.listResult, nil
}

func (s *runtimeNodeServiceStub) GetNode(_ context.Context, _ auth.Principal, _ string) (service.RuntimeNodeResult, error) {
	return s.getResult, nil
}

func (s *runtimeNodeServiceStub) SetNodeStatus(_ context.Context, _ auth.Principal, _, _ string) (service.RuntimeNodeResult, error) {
	return s.statusResult, nil
}
