package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"oc-manager/internal/service"
)

func TestAgentRegisterReturnsToken(t *testing.T) {
	stub := &agentEndpointsStub{registerResult: service.AgentRegisterResult{NodeID: "node-1", AgentToken: "agent-1", HeartbeatIntervalSeconds: 30}}
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
		AgentToken string `json:"agent_token"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.AgentToken != "agent-1" {
		t.Fatalf("agent_token = %q", resp.AgentToken)
	}
}

func TestAgentRegisterMapsBootstrapErrorTo401(t *testing.T) {
	stub := &agentEndpointsStub{registerErr: service.ErrBootstrapTokenInvalid}
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

func TestAgentHeartbeatRequiresAgentToken(t *testing.T) {
	router := newAgentRouter(&agentEndpointsStub{})

	body := bytes.NewBufferString(`{}`)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/agent/heartbeat", body)
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", recorder.Code)
	}
}

func TestAgentHeartbeatPropagatesAgentTokenError(t *testing.T) {
	stub := &agentEndpointsStub{heartbeatErr: service.ErrAgentTokenInvalid}
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

func newAgentRouter(svc *agentEndpointsStub) *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	RegisterAgentRoutes(router, NewAgentEndpointsHandler(svc))
	return router
}

type agentEndpointsStub struct {
	registerResult  service.AgentRegisterResult
	registerErr     error
	heartbeatResult service.RuntimeNodeResult
	heartbeatErr    error
}

func (s *agentEndpointsStub) RegisterAgent(_ context.Context, _ service.AgentRegisterInput) (service.AgentRegisterResult, error) {
	if s.registerErr != nil {
		return service.AgentRegisterResult{}, s.registerErr
	}
	return s.registerResult, nil
}

func (s *agentEndpointsStub) HandleHeartbeat(_ context.Context, _ service.AgentHeartbeatInput) (service.RuntimeNodeResult, error) {
	if s.heartbeatErr != nil {
		return service.RuntimeNodeResult{}, s.heartbeatErr
	}
	return s.heartbeatResult, nil
}
