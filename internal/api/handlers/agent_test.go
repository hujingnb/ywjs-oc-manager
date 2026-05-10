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

	"oc-manager/internal/service"
)

func TestAgentEnrollReturnsToken(t *testing.T) {
	stub := &agentEndpointsStub{enrollResult: service.AgentEnrollResult{NodeID: "node-1", AgentToken: "agent-1", HeartbeatIntervalSeconds: 30}}
	router := newAgentRouter(stub)

	body := bytes.NewBufferString(validEnrollJSON())
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/agent/enroll", body)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer enroll-secret")
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	var resp struct {
		AgentToken string `json:"agent_token"`
	}
	err := json.Unmarshal(recorder.Body.Bytes(), &resp)
	require.NoError(t, err)
	require.Equal(t, "agent-1", resp.AgentToken)
}

func TestAgentEnrollRequiresEnrollmentSecret(t *testing.T) {
	router := newAgentRouter(&agentEndpointsStub{})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/agent/enroll", bytes.NewBufferString(validEnrollJSON()))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusUnauthorized, recorder.Code)
}

func TestAgentEnrollMapsInvalidInputTo400(t *testing.T) {
	stub := &agentEndpointsStub{enrollErr: service.ErrEnrollInputInvalid}
	router := newAgentRouter(stub)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/agent/enroll", bytes.NewBufferString(validEnrollJSON()))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer enroll-secret")
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusBadRequest, recorder.Code)
}

func TestAgentHeartbeatRequiresAgentToken(t *testing.T) {
	router := newAgentRouter(&agentEndpointsStub{})

	body := bytes.NewBufferString(`{}`)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/agent/heartbeat", body)
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusBadRequest, recorder.Code)
}

func TestAgentHeartbeatPropagatesAgentTokenError(t *testing.T) {
	stub := &agentEndpointsStub{heartbeatErr: service.ErrAgentTokenInvalid}
	router := newAgentRouter(stub)

	body := bytes.NewBufferString(`{"agent_token":"missing"}`)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/agent/heartbeat", body)
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusUnauthorized, recorder.Code)
}

func TestAgentEnrollPushesTokenToSink(t *testing.T) {
	stub := &agentEndpointsStub{enrollResult: service.AgentEnrollResult{NodeID: "node-99", AgentToken: "agent-x", HeartbeatIntervalSeconds: 30}}
	var seenNode, seenToken string
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	handler := NewAgentEndpointsHandler(stub, "enroll-secret", func(nodeID, token string) {
		seenNode = nodeID
		seenToken = token
	})
	RegisterAgentRoutes(router, handler)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/agent/enroll", bytes.NewBufferString(validEnrollJSON()))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer enroll-secret")
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, "node-99", seenNode)
	require.Equal(t, "agent-x", seenToken)
}

func TestAgentEnrollSinkSkippedWhenTokenEmpty(t *testing.T) {
	stub := &agentEndpointsStub{enrollResult: service.AgentEnrollResult{NodeID: "n", AgentToken: ""}}
	called := false
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	handler := NewAgentEndpointsHandler(stub, "enroll-secret", func(_, _ string) { called = true })
	RegisterAgentRoutes(router, handler)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/agent/enroll", bytes.NewBufferString(validEnrollJSON()))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer enroll-secret")
	router.ServeHTTP(recorder, request)
	require.False(t, called)
}

func validEnrollJSON() string {
	return `{"agent_id":"00000000-0000-0000-0000-00000000a001","max_apps":3,"agent_docker_endpoint":"https://node-1.example:7001","agent_file_endpoint":"https://node-1.example:7002","agent_tls_ca_cert":"-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----\n","agent_version":"0.1.0"}`
}

func newAgentRouter(svc *agentEndpointsStub) *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	RegisterAgentRoutes(router, NewAgentEndpointsHandler(svc, "enroll-secret"))
	return router
}

type agentEndpointsStub struct {
	enrollResult    service.AgentEnrollResult
	enrollErr       error
	heartbeatResult service.RuntimeNodeResult
	heartbeatErr    error
}

func (s *agentEndpointsStub) EnrollAgent(_ context.Context, _ service.AgentEnrollInput) (service.AgentEnrollResult, error) {
	if s.enrollErr != nil {
		return service.AgentEnrollResult{}, s.enrollErr
	}
	return s.enrollResult, nil
}

func (s *agentEndpointsStub) HandleHeartbeat(_ context.Context, _ service.AgentHeartbeatInput) (service.RuntimeNodeResult, error) {
	if s.heartbeatErr != nil {
		return service.RuntimeNodeResult{}, s.heartbeatErr
	}
	return s.heartbeatResult, nil
}
