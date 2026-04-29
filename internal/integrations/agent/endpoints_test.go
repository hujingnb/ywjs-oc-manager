package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"oc-manager/internal/service"
)

func TestRegisterAgentReturnsToken(t *testing.T) {
	stub := &agentServiceStub{registerResult: service.AgentRegisterResult{NodeID: "node-1", AgentToken: "agent-1", HeartbeatIntervalSeconds: 30}}
	router := newAgentRouter(stub)

	body := bytes.NewBufferString(`{"bootstrap_token":"boot","agent_docker_endpoint":"tcp://127.0.0.1","agent_version":"0.1.0"}`)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/agent/register", body)
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", recorder.Code, recorder.Body.String())
	}
	var resp struct {
		NodeID     string `json:"node_id"`
		AgentToken string `json:"agent_token"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.AgentToken != "agent-1" {
		t.Fatalf("agent_token = %q, want agent-1", resp.AgentToken)
	}
	if stub.lastRegister.BootstrapToken != "boot" {
		t.Fatalf("bootstrap_token = %q", stub.lastRegister.BootstrapToken)
	}
}

func TestRegisterAgentMapsBootstrapErrorTo401(t *testing.T) {
	stub := &agentServiceStub{registerErr: service.ErrBootstrapTokenInvalid}
	router := newAgentRouter(stub)

	body := bytes.NewBufferString(`{"bootstrap_token":"bad"}`)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/agent/register", body)
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", recorder.Code)
	}
}

func TestHeartbeatRequiresAgentToken(t *testing.T) {
	stub := &agentServiceStub{}
	router := newAgentRouter(stub)

	body := bytes.NewBufferString(`{}`)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/agent/heartbeat", body)
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", recorder.Code)
	}
}

func TestHeartbeatPropagatesAgentTokenError(t *testing.T) {
	stub := &agentServiceStub{heartbeatErr: service.ErrAgentTokenInvalid}
	router := newAgentRouter(stub)

	body := bytes.NewBufferString(`{"agent_token":"missing"}`)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/agent/heartbeat", body)
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", recorder.Code)
	}
}

func TestHeartbeatReturnsRuntimeNodeBody(t *testing.T) {
	stub := &agentServiceStub{
		heartbeatResult: service.RuntimeNodeResult{ID: "node-1", Name: "node-1", Status: "active"},
	}
	router := newAgentRouter(stub)

	body := bytes.NewBufferString(`{"agent_token":"agent-1","agent_version":"0.1.0"}`)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/agent/heartbeat", body)
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", recorder.Code, recorder.Body.String())
	}
	var resp struct {
		Node service.RuntimeNodeResult `json:"runtime_node"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Node.Name != "node-1" {
		t.Fatalf("node = %+v", resp.Node)
	}
}

func newAgentRouter(svc *agentServiceStub) *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	RegisterRoutes(router, NewEndpointsHandler(svc))
	return router
}

type agentServiceStub struct {
	registerResult  service.AgentRegisterResult
	registerErr     error
	heartbeatResult service.RuntimeNodeResult
	heartbeatErr    error
	lastRegister    service.AgentRegisterInput
	lastHeartbeat   service.AgentHeartbeatInput
}

func (s *agentServiceStub) RegisterAgent(_ context.Context, input service.AgentRegisterInput) (service.AgentRegisterResult, error) {
	s.lastRegister = input
	if s.registerErr != nil {
		return service.AgentRegisterResult{}, s.registerErr
	}
	return s.registerResult, nil
}

func (s *agentServiceStub) HandleHeartbeat(_ context.Context, input service.AgentHeartbeatInput) (service.RuntimeNodeResult, error) {
	s.lastHeartbeat = input
	if s.heartbeatErr != nil {
		return service.RuntimeNodeResult{}, s.heartbeatErr
	}
	return s.heartbeatResult, nil
}

var _ = errors.New
