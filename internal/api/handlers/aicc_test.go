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
	createResult    service.AICCAgentResult
	createErr       error
	listResult      []service.AICCAgentResult
	listErr         error
	getResult       service.AICCAgentResult
	getErr          error
	updateResult    service.AICCAgentResult
	updateErr       error
	statusResult    service.AICCAgentResult
	statusErr       error
	deleteErr       error
	settingsResult  service.AICCAgentSettingsResult
	settingsErr     error
	sessionsResult  []service.AICCSessionResult
	sessionResult   service.AICCSessionDetailResult
	leadsResult     []service.AICCLeadResult
	fieldsResult    []service.AICCLeadFieldResult
	knowledgeResult service.AICCKnowledgeResult
	analyticsResult service.AICCAnalyticsResult
	markLeadErr     error

	lastPrincipal auth.Principal
	lastInput     service.AICCAgentInput
	lastOrgID     string
	lastAgentID   string
	lastSessionID string
	lastLeadID    string
	lastAction    string
	lastFields    []service.AICCLeadFieldInput
	lastSettings  service.AICCAgentSettingsInput
	lastKnowledge service.AICCKnowledgeInput
	lastSessions  service.AICCSessionListOptions
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

// GetAgentSettings 记录智能体 ID 并返回预设运营配置。
func (s *aiccServiceStub) GetAgentSettings(_ context.Context, principal auth.Principal, agentID string) (service.AICCAgentSettingsResult, error) {
	s.lastPrincipal = principal
	s.lastAgentID = agentID
	return s.settingsResult, s.settingsErr
}

// UpdateAgentSettings 记录运营配置请求并返回预设结果。
func (s *aiccServiceStub) UpdateAgentSettings(_ context.Context, principal auth.Principal, agentID string, input service.AICCAgentSettingsInput) (service.AICCAgentSettingsResult, error) {
	s.lastPrincipal = principal
	s.lastAgentID = agentID
	s.lastSettings = input
	if s.settingsResult.AgentID != "" {
		return s.settingsResult, s.settingsErr
	}
	return service.AICCAgentSettingsResult{
		AgentID:                 agentID,
		MessageLimitPerSession:  input.MessageLimitPerSession,
		SensitiveWords:          input.SensitiveWords,
		BlockedVisitorEnabled:   input.BlockedVisitorEnabled,
		SessionResumeTTLMinutes: input.SessionResumeTTLMinutes,
	}, s.settingsErr
}

// ListSessions 记录智能体 ID 并返回预设会话摘要。
func (s *aiccServiceStub) ListSessions(_ context.Context, principal auth.Principal, agentID string, options service.AICCSessionListOptions) ([]service.AICCSessionResult, error) {
	s.lastPrincipal = principal
	s.lastAgentID = agentID
	s.lastSessions = options
	return s.sessionsResult, nil
}

// GetSession 记录会话 ID 并返回预设详情。
func (s *aiccServiceStub) GetSession(_ context.Context, principal auth.Principal, sessionID string) (service.AICCSessionDetailResult, error) {
	s.lastPrincipal = principal
	s.lastSessionID = sessionID
	return s.sessionResult, nil
}

// ListLeads 记录企业 ID 并返回预设线索列表。
func (s *aiccServiceStub) ListLeads(_ context.Context, principal auth.Principal, orgID string, _, _ int32) ([]service.AICCLeadResult, error) {
	s.lastPrincipal = principal
	s.lastOrgID = orgID
	return s.leadsResult, nil
}

// ExportLeads 记录企业 ID 并返回预设全量线索。
func (s *aiccServiceStub) ExportLeads(_ context.Context, principal auth.Principal, orgID string) ([]service.AICCLeadResult, error) {
	s.lastPrincipal = principal
	s.lastOrgID = orgID
	return s.leadsResult, nil
}

// MarkLeadRead 记录线索 ID 并返回预设错误。
func (s *aiccServiceStub) MarkLeadRead(_ context.Context, principal auth.Principal, leadID string) error {
	s.lastPrincipal = principal
	s.lastLeadID = leadID
	return s.markLeadErr
}

// ListLeadFields 记录智能体 ID 并返回预设留资字段。
func (s *aiccServiceStub) ListLeadFields(_ context.Context, principal auth.Principal, agentID string) ([]service.AICCLeadFieldResult, error) {
	s.lastPrincipal = principal
	s.lastAgentID = agentID
	return s.fieldsResult, nil
}

// ReplaceLeadFields 记录整组留资字段入参并返回预设字段列表。
func (s *aiccServiceStub) ReplaceLeadFields(_ context.Context, principal auth.Principal, agentID string, fields []service.AICCLeadFieldInput) ([]service.AICCLeadFieldResult, error) {
	s.lastPrincipal = principal
	s.lastAgentID = agentID
	s.lastFields = fields
	if s.fieldsResult != nil {
		return s.fieldsResult, nil
	}
	results := make([]service.AICCLeadFieldResult, 0, len(fields))
	for _, field := range fields {
		results = append(results, service.AICCLeadFieldResult{
			FieldKey:  field.FieldKey,
			Label:     field.Label,
			FieldType: field.FieldType,
			Required:  field.Required,
		})
	}
	return results, nil
}

// GetAgentKnowledge 记录智能体 ID 并返回预设知识范围。
func (s *aiccServiceStub) GetAgentKnowledge(_ context.Context, principal auth.Principal, agentID string) (service.AICCKnowledgeResult, error) {
	s.lastPrincipal = principal
	s.lastAgentID = agentID
	return s.knowledgeResult, nil
}

// ReplaceAgentKnowledge 记录知识范围入参并返回预设配置。
func (s *aiccServiceStub) ReplaceAgentKnowledge(_ context.Context, principal auth.Principal, agentID string, input service.AICCKnowledgeInput) (service.AICCKnowledgeResult, error) {
	s.lastPrincipal = principal
	s.lastAgentID = agentID
	s.lastKnowledge = input
	if s.knowledgeResult.AgentID != "" {
		return s.knowledgeResult, nil
	}
	return service.AICCKnowledgeResult{
		AgentID:                  agentID,
		AppID:                    "app-hidden-1",
		UseOrgKnowledge:          input.UseOrgKnowledge,
		IndustryKnowledgeBaseIDs: input.IndustryKnowledgeBaseIDs,
		AppDocumentIDs:           input.AppDocumentIDs,
	}, nil
}

// Analytics 记录企业 ID 并返回预设统计。
func (s *aiccServiceStub) Analytics(_ context.Context, principal auth.Principal, orgID string) (service.AICCAnalyticsResult, error) {
	s.lastPrincipal = principal
	s.lastOrgID = orgID
	return s.analyticsResult, nil
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
	request := httptest.NewRequest(http.MethodPost, "/api/v1/aicc/agents", bytes.NewBufferString(`{"name":"官网售前","greeting":"你好","privacy_mode":"notice","retention_days":180,"allowed_domains":["www.example.com","*.example.org"]}`))
	request.Header.Set("Content-Type", "application/json")
	request = withPrincipal(request, auth.Principal{Role: domain.UserRoleOrgAdmin, OrgID: "org-1", UserID: "admin-1"})
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusCreated, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "agent-1")
	assert.Equal(t, domain.UserRoleOrgAdmin, svc.lastPrincipal.Role)
	assert.Equal(t, "官网售前", svc.lastInput.Name)
	assert.Equal(t, int32(180), svc.lastInput.RetentionDays)
	assert.Equal(t, []string{"www.example.com", "*.example.org"}, svc.lastInput.AllowedDomains)
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
		{name: "版本未授权映射为 400", err: service.ErrVersionNotInAllowlist, code: http.StatusBadRequest},                    // 场景：企业未配置可用助手版本，隐藏 app 无法选择初始化版本。
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
		{name: "更新路由透传资料", method: http.MethodPatch, path: "/api/v1/aicc/agents/agent-1", body: `{"name":"官网售后","retention_days":90,"allowed_domains":["support.example.com"]}`, wantStatus: http.StatusOK, assertion: func(t *testing.T, svc *aiccServiceStub, _ string) {
			assert.Equal(t, "agent-1", svc.lastAgentID)
			assert.Equal(t, "官网售后", svc.lastInput.Name)
			assert.Equal(t, []string{"support.example.com"}, svc.lastInput.AllowedDomains)
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
		{name: "读取知识范围路由返回 knowledge", method: http.MethodGet, path: "/api/v1/aicc/agents/agent-1/knowledge", wantStatus: http.StatusOK, assertion: func(t *testing.T, svc *aiccServiceStub, body string) {
			assert.Equal(t, "agent-1", svc.lastAgentID)
			assert.Contains(t, body, "knowledge")
		}}, // 场景：企业管理员回显智能体可检索的知识范围。
		{name: "保存知识范围路由绑定配置", method: http.MethodPut, path: "/api/v1/aicc/agents/agent-1/knowledge", body: `{"use_org_knowledge":true,"industry_knowledge_base_ids":["industry-1"],"app_document_ids":["doc-1"]}`, wantStatus: http.StatusOK, assertion: func(t *testing.T, svc *aiccServiceStub, body string) {
			assert.Equal(t, "agent-1", svc.lastAgentID)
			assert.True(t, svc.lastKnowledge.UseOrgKnowledge)
			assert.Equal(t, []string{"industry-1"}, svc.lastKnowledge.IndustryKnowledgeBaseIDs)
			assert.Equal(t, []string{"doc-1"}, svc.lastKnowledge.AppDocumentIDs)
			assert.Contains(t, body, "knowledge")
		}}, // 场景：企业管理员整组保存企业、行业和专属文档范围。
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := &aiccServiceStub{
				listResult:   []service.AICCAgentResult{testAICCAgentResult()},
				getResult:    testAICCAgentResult(),
				updateResult: testAICCAgentResult(),
				statusResult: testAICCAgentResult(),
				knowledgeResult: service.AICCKnowledgeResult{
					AgentID:                  "agent-1",
					AppID:                    "app-hidden-1",
					UseOrgKnowledge:          true,
					IndustryKnowledgeBaseIDs: []string{"industry-1"},
					AppDocumentIDs:           []string{"doc-1"},
				},
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

// TestAICCHandlerSettingsRoutes 覆盖 AICC 运营配置路由：
// handler 必须绑定 settings 请求体并把 agentId 透传给 service。
func TestAICCHandlerSettingsRoutes(t *testing.T) {
	svc := &aiccServiceStub{
		settingsResult: service.AICCAgentSettingsResult{
			AgentID:                     "agent-1",
			MessageLimitPerSession:      80,
			SensitiveWords:              []string{"违禁词"},
			BlockedVisitorEnabled:       true,
			BlockedVisitorThresholdJSON: map[string]any{"message_count": float64(3)},
			SessionResumeTTLMinutes:     45,
		},
	}
	router := newAICCTestRouter(t, svc)

	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/aicc/agents/agent-1/settings", nil)
	getReq = withPrincipal(getReq, auth.Principal{Role: domain.UserRoleOrgAdmin, OrgID: "org-1", UserID: "admin-1"})
	getRec := httptest.NewRecorder()
	router.ServeHTTP(getRec, getReq)

	require.Equal(t, http.StatusOK, getRec.Code)
	assert.Equal(t, "agent-1", svc.lastAgentID)
	assert.Contains(t, getRec.Body.String(), `"settings"`)
	assert.Contains(t, getRec.Body.String(), `"message_limit_per_session":80`)
	assert.Contains(t, getRec.Body.String(), `"blocked_visitor_threshold_json":{"message_count":3}`)

	putReq := httptest.NewRequest(http.MethodPut, "/api/v1/aicc/agents/agent-1/settings", bytes.NewBufferString(`{"message_limit_per_session":80,"sensitive_words":["违禁词"],"blocked_visitor_enabled":true,"blocked_visitor_threshold_json":{"message_count":3},"session_resume_ttl_minutes":45}`))
	putReq.Header.Set("Content-Type", "application/json")
	putReq = withPrincipal(putReq, auth.Principal{Role: domain.UserRoleOrgAdmin, OrgID: "org-1", UserID: "admin-1"})
	putRec := httptest.NewRecorder()
	router.ServeHTTP(putRec, putReq)

	require.Equal(t, http.StatusOK, putRec.Code)
	assert.Equal(t, "agent-1", svc.lastAgentID)
	assert.Equal(t, int32(80), svc.lastSettings.MessageLimitPerSession)
	assert.Equal(t, []string{"违禁词"}, svc.lastSettings.SensitiveWords)
	assert.True(t, svc.lastSettings.BlockedVisitorEnabled)
	assert.JSONEq(t, `{"message_count":3}`, string(svc.lastSettings.BlockedVisitorThresholdJSON))
	assert.Equal(t, int32(45), svc.lastSettings.SessionResumeTTLMinutes)
}

// TestAICCHandlerOperationsRoutes 覆盖 AICC 会话、线索、统计和导出路由接线。
func TestAICCHandlerOperationsRoutes(t *testing.T) {
	now := time.Date(2026, 7, 10, 9, 0, 0, 0, time.UTC)
	cases := []struct {
		name       string // 子场景说明
		method     string
		path       string
		wantStatus int
		assertion  func(t *testing.T, svc *aiccServiceStub, recorder *httptest.ResponseRecorder)
	}{
		{name: "会话列表路由返回 sessions", method: http.MethodGet, path: "/api/v1/aicc/agents/agent-1/sessions?resolution_status=unresolved&lead_status=complete&channel=web_widget&keyword=pricing", wantStatus: http.StatusOK, assertion: func(t *testing.T, svc *aiccServiceStub, recorder *httptest.ResponseRecorder) {
			assert.Equal(t, "agent-1", svc.lastAgentID)
			assert.Equal(t, "unresolved", svc.lastSessions.ResolutionStatus)
			assert.Equal(t, "complete", svc.lastSessions.LeadStatus)
			assert.Equal(t, "web_widget", svc.lastSessions.Channel)
			assert.Equal(t, "pricing", svc.lastSessions.Keyword)
			assert.Contains(t, recorder.Body.String(), "sessions")
		}}, // 场景：企业管理员查看某智能体会话列表。
		{name: "会话详情路由返回 session 和 messages", method: http.MethodGet, path: "/api/v1/aicc/sessions/session-1", wantStatus: http.StatusOK, assertion: func(t *testing.T, svc *aiccServiceStub, recorder *httptest.ResponseRecorder) {
			assert.Equal(t, "session-1", svc.lastSessionID)
			assert.Contains(t, recorder.Body.String(), "messages")
		}}, // 场景：企业管理员查看单个会话详情。
		{name: "线索列表路由返回 leads", method: http.MethodGet, path: "/api/v1/aicc/leads?org_id=org-1", wantStatus: http.StatusOK, assertion: func(t *testing.T, svc *aiccServiceStub, recorder *httptest.ResponseRecorder) {
			assert.Equal(t, "org-1", svc.lastOrgID)
			assert.Contains(t, recorder.Body.String(), "leads")
		}}, // 场景：平台只读或企业管理员查看线索列表。
		{name: "线索已读路由返回 204", method: http.MethodPost, path: "/api/v1/aicc/leads/lead-1/read", wantStatus: http.StatusNoContent, assertion: func(t *testing.T, svc *aiccServiceStub, _ *httptest.ResponseRecorder) {
			assert.Equal(t, "lead-1", svc.lastLeadID)
		}}, // 场景：企业管理员把线索标记为已读。
		{name: "统计路由返回 analytics", method: http.MethodGet, path: "/api/v1/aicc/analytics", wantStatus: http.StatusOK, assertion: func(t *testing.T, svc *aiccServiceStub, recorder *httptest.ResponseRecorder) {
			assert.Contains(t, recorder.Body.String(), "today_sessions")
		}}, // 场景：企业管理员查看运营统计卡片。
		{name: "线索导出路由返回 CSV", method: http.MethodGet, path: "/api/v1/aicc/leads/export", wantStatus: http.StatusOK, assertion: func(t *testing.T, svc *aiccServiceStub, recorder *httptest.ResponseRecorder) {
			assert.Contains(t, recorder.Header().Get("Content-Type"), "text/csv")
			assert.Contains(t, recorder.Header().Get("Content-Disposition"), "aicc-leads.csv")
			assert.Contains(t, recorder.Body.String(), "lead_id,display_name,unread,updated_at,联系电话")
			assert.Contains(t, recorder.Body.String(), "'=HYPERLINK")
			assert.Contains(t, recorder.Body.String(), "'=13800138000")
		}}, // 场景：企业管理员导出线索 CSV。
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := &aiccServiceStub{
				sessionsResult: []service.AICCSessionResult{{ID: "session-1", AgentID: "agent-1", OrgID: "org-1", Channel: domain.AICCChannelWebLink, CreatedAt: now, UpdatedAt: now}},
				sessionResult: service.AICCSessionDetailResult{
					Session:  service.AICCSessionResult{ID: "session-1", AgentID: "agent-1", OrgID: "org-1", Channel: domain.AICCChannelWebLink, CreatedAt: now, UpdatedAt: now},
					Messages: []service.AICCMessageResult{{ID: "msg-1", Direction: domain.AICCMessageDirectionVisitor, ContentType: domain.AICCMessageContentTypeText, Text: "你好", CreatedAt: now}},
				},
				leadsResult:     []service.AICCLeadResult{{ID: "lead-1", OrgID: "org-1", DisplayName: "=HYPERLINK(\"https://example.com\")", Unread: true, Values: []service.AICCLeadValueResult{{FieldKey: "phone", Label: "联系电话", Value: "=13800138000"}}, UpdatedAt: now}},
				analyticsResult: service.AICCAnalyticsResult{TodaySessions: 3, UnreadLeads: 1},
			}
			router := newAICCTestRouter(t, svc)

			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(tc.method, tc.path, nil)
			request = withPrincipal(request, auth.Principal{Role: domain.UserRoleOrgAdmin, OrgID: "org-1", UserID: "admin-1"})
			router.ServeHTTP(recorder, request)

			require.Equal(t, tc.wantStatus, recorder.Code)
			tc.assertion(t, svc, recorder)
		})
	}
}
