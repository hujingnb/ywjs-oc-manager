package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/service"
)

func TestRuntimeNodesCreateRequiresToken(t *testing.T) {
	router, _ := newRuntimeNodesTestRouter(t, &runtimeNodeServiceStub{})

	recorder := httptest.NewRecorder()
	body := bytes.NewBufferString(`{"name":"node"}`)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/runtime-nodes", body)
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", recorder.Code)
	}
}

func TestRuntimeNodesCreateReturnsBootstrapToken(t *testing.T) {
	stub := &runtimeNodeServiceStub{createResult: service.RuntimeNodeResult{ID: "n1", Name: "node-1", Status: domain.RuntimeNodeStatusPending, BootstrapToken: "boot"}}
	router, tokens := newRuntimeNodesTestRouter(t, stub)
	token := mustSignAccess(t, tokens, auth.Principal{UserID: "u1", Role: domain.UserRolePlatformAdmin})

	recorder := httptest.NewRecorder()
	body := bytes.NewBufferString(`{"name":"node-1"}`)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/runtime-nodes", body)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+token)
	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, body=%s", recorder.Code, recorder.Body.String())
	}
	var resp struct {
		Node service.RuntimeNodeResult `json:"runtime_node"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Node.BootstrapToken != "boot" {
		t.Fatalf("bootstrap_token = %q, want boot", resp.Node.BootstrapToken)
	}
}

func TestRuntimeNodesRotateBootstrapMapsBusyTo409(t *testing.T) {
	stub := &runtimeNodeServiceStub{rotateErr: service.ErrRuntimeNodeBusy}
	router, tokens := newRuntimeNodesTestRouter(t, stub)
	token := mustSignAccess(t, tokens, auth.Principal{UserID: "u1", Role: domain.UserRolePlatformAdmin})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/runtime-nodes/n1/rotate-bootstrap", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", recorder.Code)
	}
}

func TestRuntimeNodesListReturnsArray(t *testing.T) {
	stub := &runtimeNodeServiceStub{listResult: []service.RuntimeNodeResult{{ID: "n1", Name: "node-1"}}}
	router, tokens := newRuntimeNodesTestRouter(t, stub)
	token := mustSignAccess(t, tokens, auth.Principal{UserID: "u1", Role: domain.UserRolePlatformAdmin})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/runtime-nodes", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	var resp struct {
		Nodes []service.RuntimeNodeResult `json:"runtime_nodes"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Nodes) != 1 || resp.Nodes[0].Name != "node-1" {
		t.Fatalf("nodes = %+v", resp.Nodes)
	}
}

func TestRuntimeNodesPatchUpdateMaxApps(t *testing.T) {
	v := int32(3)
	stub := &runtimeNodeServiceStub{updateMaxAppsResult: service.RuntimeNodeResult{ID: "n1", Name: "node-1", MaxApps: &v}}
	router, tokens := newRuntimeNodesTestRouter(t, stub)
	token := mustSignAccess(t, tokens, auth.Principal{UserID: "u1", Role: domain.UserRolePlatformAdmin})

	recorder := httptest.NewRecorder()
	body := bytes.NewBufferString(`{"max_apps":3}`)
	request := httptest.NewRequest(http.MethodPatch, "/api/v1/runtime-nodes/n1", body)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+token)
	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", recorder.Code, recorder.Body.String())
	}
	if stub.updateMaxAppsLastVal == nil || *stub.updateMaxAppsLastVal != 3 {
		t.Fatalf("service 收到的 maxApps = %v, want 3", stub.updateMaxAppsLastVal)
	}
}

func TestRuntimeNodesPatchClearMaxApps(t *testing.T) {
	stub := &runtimeNodeServiceStub{updateMaxAppsResult: service.RuntimeNodeResult{ID: "n1", Name: "node-1"}}
	router, tokens := newRuntimeNodesTestRouter(t, stub)
	token := mustSignAccess(t, tokens, auth.Principal{UserID: "u1", Role: domain.UserRolePlatformAdmin})

	recorder := httptest.NewRecorder()
	body := bytes.NewBufferString(`{"max_apps":null}`)
	request := httptest.NewRequest(http.MethodPatch, "/api/v1/runtime-nodes/n1", body)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+token)
	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", recorder.Code, recorder.Body.String())
	}
	if stub.updateMaxAppsLastVal != nil {
		t.Fatalf("max_apps null 应映射为 nil, got %v", *stub.updateMaxAppsLastVal)
	}
}

func TestRuntimeNodesPatchOrgAdminForbidden(t *testing.T) {
	stub := &runtimeNodeServiceStub{updateMaxAppsErr: service.ErrForbidden}
	router, tokens := newRuntimeNodesTestRouter(t, stub)
	token := mustSignAccess(t, tokens, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgAdmin})

	recorder := httptest.NewRecorder()
	body := bytes.NewBufferString(`{"max_apps":1}`)
	request := httptest.NewRequest(http.MethodPatch, "/api/v1/runtime-nodes/n1", body)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+token)
	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", recorder.Code)
	}
}

func newRuntimeNodesTestRouter(t *testing.T, svc runtimeNodeService) (*gin.Engine, *auth.TokenManager) {
	t.Helper()
	gin.SetMode(gin.ReleaseMode)
	tokens, err := auth.NewTokenManager("a", "b", time.Minute, time.Hour)
	if err != nil {
		t.Fatalf("NewTokenManager() error = %v", err)
	}
	router := gin.New()
	RegisterRuntimeNodeRoutes(router, NewRuntimeNodesHandler(svc, tokens))
	return router, tokens
}

type runtimeNodeServiceStub struct {
	createResult         service.RuntimeNodeResult
	listResult           []service.RuntimeNodeResult
	getResult            service.RuntimeNodeResult
	rotateResult         service.RuntimeNodeResult
	statusResult         service.RuntimeNodeResult
	rotateErr            error
	updateMaxAppsResult  service.RuntimeNodeResult
	updateMaxAppsErr     error
	updateMaxAppsLastVal *int32
}

func (s *runtimeNodeServiceStub) CreateNode(_ context.Context, _ auth.Principal, _ service.RuntimeNodeInput) (service.RuntimeNodeResult, error) {
	return s.createResult, nil
}

func (s *runtimeNodeServiceStub) ListNodes(_ context.Context, _ auth.Principal, _, _ int32) ([]service.RuntimeNodeResult, error) {
	return s.listResult, nil
}

func (s *runtimeNodeServiceStub) GetNode(_ context.Context, _ auth.Principal, _ string) (service.RuntimeNodeResult, error) {
	return s.getResult, nil
}

func (s *runtimeNodeServiceStub) RotateBootstrap(_ context.Context, _ auth.Principal, _ string) (service.RuntimeNodeResult, error) {
	if s.rotateErr != nil {
		return service.RuntimeNodeResult{}, s.rotateErr
	}
	return s.rotateResult, nil
}

func (s *runtimeNodeServiceStub) SetNodeStatus(_ context.Context, _ auth.Principal, _, _ string) (service.RuntimeNodeResult, error) {
	return s.statusResult, nil
}

func (s *runtimeNodeServiceStub) UpdateMaxApps(_ context.Context, _ auth.Principal, _ string, maxApps *int32) (service.RuntimeNodeResult, error) {
	s.updateMaxAppsLastVal = maxApps
	if s.updateMaxAppsErr != nil {
		return service.RuntimeNodeResult{}, s.updateMaxAppsErr
	}
	return s.updateMaxAppsResult, nil
}

var _ = errors.New
