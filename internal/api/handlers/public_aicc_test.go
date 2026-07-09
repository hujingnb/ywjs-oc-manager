package handlers

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"oc-manager/internal/service"
)

type publicAICCServiceStub struct {
	configResult  service.AICCPublicConfigResult
	configErr     error
	sessionResult service.AICCPublicSessionResult
	sessionErr    error
	messageResult service.AICCPublicMessageResult
	messageErr    error
	consentErr    error

	lastPublicToken string
	lastSessionToken string
	lastMessageInput service.AICCPublicMessageInput
}

func (s *publicAICCServiceStub) PublicConfig(_ context.Context, publicToken string) (service.AICCPublicConfigResult, error) {
	s.lastPublicToken = publicToken
	return s.configResult, s.configErr
}

func (s *publicAICCServiceStub) CreateSession(_ context.Context, publicToken string, input service.AICCPublicSessionInput) (service.AICCPublicSessionResult, error) {
	s.lastPublicToken = publicToken
	return s.sessionResult, s.sessionErr
}

func (s *publicAICCServiceStub) Consent(_ context.Context, sessionToken string) error {
	s.lastSessionToken = sessionToken
	return s.consentErr
}

func (s *publicAICCServiceStub) SendMessage(_ context.Context, input service.AICCPublicMessageInput) (service.AICCPublicMessageResult, error) {
	s.lastMessageInput = input
	return s.messageResult, s.messageErr
}

func newPublicAICCTestRouter(t *testing.T, svc publicAICCService) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	RegisterPublicAICCRoutes(router, NewPublicAICCHandler(svc))
	return router
}

// TestPublicAICCHandlerSendMessage 覆盖公开访客消息入口：session token 来自路径，消息文本来自请求体。
func TestPublicAICCHandlerSendMessage(t *testing.T) {
	svc := &publicAICCServiceStub{messageResult: service.AICCPublicMessageResult{MessageID: "msg-1", Text: "您好"}}
	router := newPublicAICCTestRouter(t, svc)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/public/aicc/sessions/sess-1/messages", bytes.NewBufferString(`{"text":"你好"}`))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "msg-1")
	assert.Equal(t, "sess-1", svc.lastMessageInput.SessionToken)
	assert.Equal(t, "你好", svc.lastMessageInput.Text)
}

// TestPublicAICCHandlerMapsConversationGates 覆盖公开访客消息入口的隐私同意和留资阻断错误映射。
func TestPublicAICCHandlerMapsConversationGates(t *testing.T) {
	cases := []struct {
		name string // 子场景说明
		err  error
		code string
	}{
		{name: "未同意隐私说明返回稳定 code", err: service.ErrAICCConsentRequired, code: "AICC_CONSENT_REQUIRED"}, // 场景：consent_required 模式未同意。
		{name: "缺少必填留资返回稳定 code", err: service.ErrAICCLeadRequired, code: "AICC_LEAD_REQUIRED"},       // 场景：必填字段未完成。
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			router := newPublicAICCTestRouter(t, &publicAICCServiceStub{messageErr: tc.err})

			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, "/api/v1/public/aicc/sessions/sess-1/messages", bytes.NewBufferString(`{"text":"你好"}`))
			request.Header.Set("Content-Type", "application/json")
			router.ServeHTTP(recorder, request)

			require.Equal(t, http.StatusConflict, recorder.Code)
			assert.Contains(t, recorder.Body.String(), tc.code)
		})
	}
}

// TestPublicAICCHandlerCreateSession 覆盖公开创建会话入口：公开 token 来自路径，返回 session token。
func TestPublicAICCHandlerCreateSession(t *testing.T) {
	svc := &publicAICCServiceStub{sessionResult: service.AICCPublicSessionResult{SessionToken: "sess-token", PrivacyMode: "notice", PrivacyNoticeShown: true}}
	router := newPublicAICCTestRouter(t, svc)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/public/aicc/agents/pub/sessions", bytes.NewBufferString(`{"channel":"web_link","source_url":"https://example.com"}`))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusCreated, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "sess-token")
	assert.Equal(t, "pub", svc.lastPublicToken)
}
