package handlers

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/service"
)

// aiccServiceStub 实现 AICC handler 依赖的最小接口，并记录请求入参。
type aiccServiceStub struct {
	createResult service.AICCAgentResult
	createErr    error
	listResult   []service.AICCAgentResult
	listErr      error
	getResult    service.AICCAgentResult
	getErr       error
	updateResult service.AICCAgentResult
	updateErr    error
	statusResult service.AICCAgentResult
	statusErr    error
	deleteErr    error

	lastPrincipal auth.Principal
	lastInput     service.AICCAgentInput
	lastOrgID     string
	lastAgentID   string
	lastAction    string
}

// CreateAgent 记录创建请求并返回预设结果。
func (s *aiccServiceStub) CreateAgent(_ context.Context, principal auth.Principal, input service.AICCAgentInput) (service.AICCAgentResult, error) {
	s.lastPrincipal = principal
	s.lastInput = input
	return s.createResult, s.createErr
}

// ListAgents 记录企业 ID 并返回预设列表。
func (s *aiccServiceStub) ListAgents(_ context.Context, principal auth.Principal, orgID string, _, _ int32) ([]service.AICCAgentResult, error) {
	s.lastPrincipal = principal
	s.lastOrgID = orgID
	return s.listResult, s.listErr
}

// GetAgent 记录智能体 ID 并返回预设结果。
func (s *aiccServiceStub) GetAgent(_ context.Context, principal auth.Principal, agentID string) (service.AICCAgentResult, error) {
	s.lastPrincipal = principal
	s.lastAgentID = agentID
	return s.getResult, s.getErr
}

// UpdateAgent 记录更新请求并返回预设结果。
func (s *aiccServiceStub) UpdateAgent(_ context.Context, principal auth.Principal, agentID string, input service.AICCAgentInput) (service.AICCAgentResult, error) {
	s.lastPrincipal = principal
	s.lastAgentID = agentID
	s.lastInput = input
	return s.updateResult, s.updateErr
}

// SetAgentStatus 记录状态动作并返回预设结果。
func (s *aiccServiceStub) SetAgentStatus(_ context.Context, principal auth.Principal, agentID, action string) (service.AICCAgentResult, error) {
	s.lastPrincipal = principal
	s.lastAgentID = agentID
	s.lastAction = action
	return s.statusResult, s.statusErr
}

// DeleteAgent 记录删除目标并返回预设错误。
func (s *aiccServiceStub) DeleteAgent(_ context.Context, principal auth.Principal, agentID string) error {
	s.lastPrincipal = principal
	s.lastAgentID = agentID
	return s.deleteErr
}

// newAICCTestRouter 构建用于测试的 AICC router。
func newAICCTestRouter(t *testing.T, svc aiccService) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	RegisterAICCRoutes(router, NewAICCHandler(svc))
	return router
}

func testAICCAgentResult() service.AICCAgentResult {
	return service.AICCAgentResult{
		ID:            "agent-1",
		OrgID:         "org-1",
		AppID:         "app-hidden-1",
		Name:          "官网售前",
		Status:        domain.AICCAgentStatusDraft,
		PrivacyMode:   domain.AICCPrivacyModeNotice,
		RetentionDays: 180,
		CreatedAt:     time.Date(2026, 7, 9, 10, 0, 0, 0, time.UTC),
		UpdatedAt:     time.Date(2026, 7, 9, 10, 0, 0, 0, time.UTC),
	}
}

// TestAICCHandlerCreateAgent 覆盖正常路径：POST /api/v1/aicc/agents 绑定请求体并返回 agent。
func TestAICCHandlerCreateAgent(t *testing.T) {
	svc := &aiccServiceStub{createResult: testAICCAgentResult()}
	router := newAICCTestRouter(t, svc)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/aicc/agents", bytes.NewBufferString(`{"name":"官网售前","greeting":"你好","privacy_mode":"notice","retention_days":180}`))
	request.Header.Set("Content-Type", "application/json")
	request = withPrincipal(request, auth.Principal{Role: domain.UserRoleOrgAdmin, OrgID: "org-1", UserID: "admin-1"})
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusCreated, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "agent-1")
	assert.Equal(t, domain.UserRoleOrgAdmin, svc.lastPrincipal.Role)
	assert.Equal(t, "官网售前", svc.lastInput.Name)
	assert.Equal(t, int32(180), svc.lastInput.RetentionDays)
}

// TestAICCHandlerCreateAgentBadBody 覆盖异常路径：非法 JSON 或缺少名称时返回 400。
func TestAICCHandlerCreateAgentBadBody(t *testing.T) {
	cases := []struct {
		name string // 子场景说明
		body string
	}{
		{name: "非法 JSON 返回 400", body: `{"name":"官网售前"`}, // 场景：JSON 语法错误。
		{name: "缺少 name 返回 400", body: `{}`},             // 场景：必填字段缺失。
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			router := newAICCTestRouter(t, &aiccServiceStub{})

			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, "/api/v1/aicc/agents", bytes.NewBufferString(tc.body))
			request.Header.Set("Content-Type", "application/json")
			request = withPrincipal(request, auth.Principal{Role: domain.UserRoleOrgAdmin, OrgID: "org-1", UserID: "admin-1"})
			router.ServeHTTP(recorder, request)

			require.Equal(t, http.StatusBadRequest, recorder.Code)
			assert.Contains(t, recorder.Body.String(), "BAD_REQUEST")
		})
	}
}

// TestAICCHandlerCreateAgentMapsServiceErrors 覆盖创建接口的 service sentinel 错误映射。
func TestAICCHandlerCreateAgentMapsServiceErrors(t *testing.T) {
	cases := []struct {
		name string // 子场景说明
		err  error
		code int
	}{
		{name: "无权限映射为 403", err: service.ErrForbidden, code: http.StatusForbidden},                                   // 场景：普通成员或跨组织管理员创建。
		{name: "参数错误映射为 400", err: fmt.Errorf("%w: 名称不能为空", service.ErrInvalidArgument), code: http.StatusBadRequest}, // 场景：service 业务校验失败。
		{name: "超限映射为 409", err: service.ErrQuotaExceeded, code: http.StatusConflict},                                 // 场景：达到 aicc_agent_limit。
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			router := newAICCTestRouter(t, &aiccServiceStub{createErr: tc.err})

			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, "/api/v1/aicc/agents", bytes.NewBufferString(`{"name":"官网售前"}`))
			request.Header.Set("Content-Type", "application/json")
			request = withPrincipal(request, auth.Principal{Role: domain.UserRoleOrgAdmin, OrgID: "org-1", UserID: "admin-1"})
			router.ServeHTTP(recorder, request)

			require.Equal(t, tc.code, recorder.Code)
		})
	}
}

// TestAICCHandlerBasicRoutes 覆盖列表、详情、更新、启停和删除的基础路由接线。
func TestAICCHandlerBasicRoutes(t *testing.T) {
	cases := []struct {
		name       string // 子场景说明
		method     string
		path       string
		body       string
		wantStatus int
		assertion  func(t *testing.T, svc *aiccServiceStub, body string)
	}{
		{name: "列表路由返回 agents", method: http.MethodGet, path: "/api/v1/aicc/agents?org_id=org-1", wantStatus: http.StatusOK, assertion: func(t *testing.T, svc *aiccServiceStub, body string) {
			assert.Equal(t, "org-1", svc.lastOrgID)
			assert.Contains(t, body, "agents")
		}}, // 场景：平台管理员带 org_id 读取企业智能体列表。
		{name: "详情路由返回 agent", method: http.MethodGet, path: "/api/v1/aicc/agents/agent-1", wantStatus: http.StatusOK, assertion: func(t *testing.T, svc *aiccServiceStub, body string) {
			assert.Equal(t, "agent-1", svc.lastAgentID)
			assert.Contains(t, body, "agent-1")
		}}, // 场景：读取单个智能体。
		{name: "更新路由透传资料", method: http.MethodPatch, path: "/api/v1/aicc/agents/agent-1", body: `{"name":"官网售后","retention_days":90}`, wantStatus: http.StatusOK, assertion: func(t *testing.T, svc *aiccServiceStub, _ string) {
			assert.Equal(t, "agent-1", svc.lastAgentID)
			assert.Equal(t, "官网售后", svc.lastInput.Name)
		}}, // 场景：企业管理员更新智能体资料。
		{name: "启动路由写 start 动作", method: http.MethodPost, path: "/api/v1/aicc/agents/agent-1/start", wantStatus: http.StatusOK, assertion: func(t *testing.T, svc *aiccServiceStub, _ string) {
			assert.Equal(t, "agent-1", svc.lastAgentID)
			assert.Equal(t, "start", svc.lastAction)
		}}, // 场景：企业管理员启动智能体。
		{name: "停止路由写 stop 动作", method: http.MethodPost, path: "/api/v1/aicc/agents/agent-1/stop", wantStatus: http.StatusOK, assertion: func(t *testing.T, svc *aiccServiceStub, _ string) {
			assert.Equal(t, "agent-1", svc.lastAgentID)
			assert.Equal(t, "stop", svc.lastAction)
		}}, // 场景：企业管理员停止智能体。
		{name: "删除路由返回 204", method: http.MethodDelete, path: "/api/v1/aicc/agents/agent-1", wantStatus: http.StatusNoContent, assertion: func(t *testing.T, svc *aiccServiceStub, _ string) {
			assert.Equal(t, "agent-1", svc.lastAgentID)
		}}, // 场景：企业管理员软删除智能体。
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := &aiccServiceStub{
				listResult:   []service.AICCAgentResult{testAICCAgentResult()},
				getResult:    testAICCAgentResult(),
				updateResult: testAICCAgentResult(),
				statusResult: testAICCAgentResult(),
			}
			router := newAICCTestRouter(t, svc)

			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(tc.method, tc.path, bytes.NewBufferString(tc.body))
			if tc.body != "" {
				request.Header.Set("Content-Type", "application/json")
			}
			request = withPrincipal(request, auth.Principal{Role: domain.UserRoleOrgAdmin, OrgID: "org-1", UserID: "admin-1"})
			router.ServeHTTP(recorder, request)

			require.Equal(t, tc.wantStatus, recorder.Code)
			if tc.assertion != nil {
				tc.assertion(t, svc, recorder.Body.String())
			}
		})
	}
}
