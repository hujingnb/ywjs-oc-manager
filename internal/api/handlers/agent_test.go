// Package handlers 的 agent_test 覆盖 agent 注册、心跳与节点探测相关 HTTP handler 的请求绑定和响应语义。
package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"oc-manager/internal/service"
)

// TestAgentEnrollReturnsToken 验证agent注册返回令牌的成功路径场景。
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

// TestAgentEnrollRequiresEnrollmentSecret 验证agent注册要求Enrollment密钥的预期行为场景。
func TestAgentEnrollRequiresEnrollmentSecret(t *testing.T) {
	router := newAgentRouter(&agentEndpointsStub{})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/agent/enroll", bytes.NewBufferString(validEnrollJSON()))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusUnauthorized, recorder.Code)
}

// TestAgentEnrollMapsInvalidInputTo400 验证agent注册映射非法输入到400的异常或拒绝路径场景。
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

// TestAgentHeartbeatRequiresAgentToken 验证agent心跳要求agent令牌的预期行为场景。
func TestAgentHeartbeatRequiresAgentToken(t *testing.T) {
	router := newAgentRouter(&agentEndpointsStub{})

	body := bytes.NewBufferString(`{}`)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/agent/heartbeat", body)
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusBadRequest, recorder.Code)
}

// TestAgentHeartbeatPropagatesAgentTokenError 验证agent心跳透传agent令牌错误的错误映射或错误记录场景。
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

// TestAgentHeartbeatParsesNodeResourceSample 验证agent心跳会把节点资源采样绑定到service输入。
func TestAgentHeartbeatParsesNodeResourceSample(t *testing.T) {
	stub := &agentEndpointsStub{heartbeatResult: service.RuntimeNodeResult{ID: "node-1"}}
	router := newAgentRouter(stub)

	body := bytes.NewBufferString(`{
		"agent_token":"agent-token",
		"sampled_at":"2026-05-13T12:34:56Z",
		"node_resource":{
			"cpu_percent":42.5,
			"memory_used_bytes":1024,
			"memory_total_bytes":4096,
			"disk_used_bytes":2048,
			"disk_total_bytes":8192,
			"network_rx_bytes":300,
			"network_tx_bytes":200,
			"instance_count":3,
			"last_error":""
		}
	}`)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/agent/heartbeat", body)
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.NotNil(t, stub.heartbeatInput.NodeResource)
	assert.Equal(t, mustParseTimeHandler(t, "2026-05-13T12:34:56Z"), stub.heartbeatInput.SampledAt)
	assert.Equal(t, 42.5, *stub.heartbeatInput.NodeResource.CPUPercent)
	assert.Equal(t, int64(1024), *stub.heartbeatInput.NodeResource.MemoryUsedBytes)
	assert.Equal(t, int32(3), *stub.heartbeatInput.NodeResource.InstanceCount)
}

// TestAgentEnrollPushesTokenToSink 验证agent注册Pushes令牌到Sink的预期行为场景。
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

// TestAgentEnrollSinkSkippedWhenTokenEmpty 验证agent注册SinkSkipped当令牌空值的边界条件场景。
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

func mustParseTimeHandler(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, value)
	require.NoError(t, err)
	return parsed
}

type agentEndpointsStub struct {
	enrollResult    service.AgentEnrollResult
	enrollErr       error
	enrollInput     service.AgentEnrollInput
	heartbeatResult service.RuntimeNodeResult
	heartbeatErr    error
	heartbeatInput  service.AgentHeartbeatInput
}

func (s *agentEndpointsStub) EnrollAgent(_ context.Context, input service.AgentEnrollInput) (service.AgentEnrollResult, error) {
	s.enrollInput = input
	if s.enrollErr != nil {
		return service.AgentEnrollResult{}, s.enrollErr
	}
	return s.enrollResult, nil
}

func (s *agentEndpointsStub) HandleHeartbeat(_ context.Context, input service.AgentHeartbeatInput) (service.RuntimeNodeResult, error) {
	s.heartbeatInput = input
	if s.heartbeatErr != nil {
		return service.RuntimeNodeResult{}, s.heartbeatErr
	}
	return s.heartbeatResult, nil
}
