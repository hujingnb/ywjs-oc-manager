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
	configResult   service.AICCPublicConfigResult
	configErr      error
	sessionResult  service.AICCPublicSessionResult
	sessionErr     error
	imageResult    service.AICCPublicImageResult
	imageErr       error
	messageResult  service.AICCPublicMessageResult
	messageErr     error
	leadResult     service.AICCPublicLeadValuesResult
	leadErr        error
	feedbackResult service.AICCPublicFeedbackResult
	feedbackErr    error
	consentErr     error

	lastPublicToken   string
	lastSessionToken  string
	lastImageInput    service.AICCPublicImageInput
	lastMessageInput  service.AICCPublicMessageInput
	lastLeadInput     service.AICCPublicLeadValuesInput
	lastFeedbackInput service.AICCPublicFeedbackInput
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

func (s *publicAICCServiceStub) UploadImage(_ context.Context, input service.AICCPublicImageInput) (service.AICCPublicImageResult, error) {
	s.lastImageInput = input
	return s.imageResult, s.imageErr
}

func (s *publicAICCServiceStub) SendMessage(_ context.Context, input service.AICCPublicMessageInput) (service.AICCPublicMessageResult, error) {
	s.lastMessageInput = input
	return s.messageResult, s.messageErr
}

func (s *publicAICCServiceStub) SubmitLeadValues(_ context.Context, input service.AICCPublicLeadValuesInput) (service.AICCPublicLeadValuesResult, error) {
	s.lastLeadInput = input
	return s.leadResult, s.leadErr
}

func (s *publicAICCServiceStub) SubmitFeedback(_ context.Context, input service.AICCPublicFeedbackInput) (service.AICCPublicFeedbackResult, error) {
	s.lastFeedbackInput = input
	return s.feedbackResult, s.feedbackErr
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

// TestPublicAICCHandlerUploadImage 覆盖公开图片上传入口：session token 来自路径，文件名来自 query。
func TestPublicAICCHandlerUploadImage(t *testing.T) {
	svc := &publicAICCServiceStub{imageResult: service.AICCPublicImageResult{ImageFileID: "image-1", Mime: "image/png", Size: 12}}
	router := newPublicAICCTestRouter(t, svc)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/public/aicc/sessions/sess-1/images?filename=a.png", bytes.NewBufferString("image-bytes"))
	request.Header.Set("Content-Type", "application/octet-stream")
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "image-1")
	assert.Equal(t, "sess-1", svc.lastImageInput.SessionToken)
	assert.Equal(t, "a.png", svc.lastImageInput.Filename)
}

// TestPublicAICCHandlerSubmitLeadValues 覆盖公开留资入口：session token 来自路径，字段值来自请求体。
func TestPublicAICCHandlerSubmitLeadValues(t *testing.T) {
	svc := &publicAICCServiceStub{leadResult: service.AICCPublicLeadValuesResult{LeadStatus: "complete"}}
	router := newPublicAICCTestRouter(t, svc)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/public/aicc/sessions/sess-1/lead-values", bytes.NewBufferString(`{"values":{"phone":"13800000000"}}`))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "complete")
	assert.Equal(t, "sess-1", svc.lastLeadInput.SessionToken)
	assert.Equal(t, "13800000000", svc.lastLeadInput.Values["phone"])
}

// TestPublicAICCHandlerSubmitFeedback 覆盖公开反馈入口：session token/message id 来自路径，helpful 来自请求体。
func TestPublicAICCHandlerSubmitFeedback(t *testing.T) {
	svc := &publicAICCServiceStub{feedbackResult: service.AICCPublicFeedbackResult{ResolutionStatus: "resolved"}}
	router := newPublicAICCTestRouter(t, svc)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/public/aicc/sessions/sess-1/messages/msg-1/feedback", bytes.NewBufferString(`{"helpful":true}`))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "resolved")
	assert.Equal(t, "sess-1", svc.lastFeedbackInput.SessionToken)
	assert.Equal(t, "msg-1", svc.lastFeedbackInput.MessageID)
	assert.True(t, svc.lastFeedbackInput.Helpful)
}

// TestPublicAICCHandlerSubmitFeedbackRequiresHelpful 覆盖反馈入口：缺少 helpful 时不能默认为没帮助。
func TestPublicAICCHandlerSubmitFeedbackRequiresHelpful(t *testing.T) {
	router := newPublicAICCTestRouter(t, &publicAICCServiceStub{})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/public/aicc/sessions/sess-1/messages/msg-1/feedback", bytes.NewBufferString(`{}`))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusBadRequest, recorder.Code)
}

// TestPublicAICCHandlerMapsConversationGates 覆盖公开访客消息入口的隐私同意和留资阻断错误映射。
func TestPublicAICCHandlerMapsConversationGates(t *testing.T) {
	cases := []struct {
		name string // 子场景说明
		err  error
		code string
	}{
		{name: "未同意隐私说明返回稳定 code", err: service.ErrAICCConsentRequired, code: "AICC_CONSENT_REQUIRED"}, // 场景：consent_required 模式未同意。
		{name: "缺少必填留资返回稳定 code", err: service.ErrAICCLeadRequired, code: "AICC_LEAD_REQUIRED"},        // 场景：必填字段未完成。
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

// TestPublicAICCHandlerMapsInvalidMessage 覆盖反馈入口：不可反馈消息返回稳定 code。
func TestPublicAICCHandlerMapsInvalidMessage(t *testing.T) {
	router := newPublicAICCTestRouter(t, &publicAICCServiceStub{feedbackErr: service.ErrAICCInvalidMessage})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/public/aicc/sessions/sess-1/messages/msg-1/feedback", bytes.NewBufferString(`{"helpful":false}`))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusNotFound, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "AICC_INVALID_MESSAGE")
}

// TestPublicAICCHandlerMapsImageTooLarge 覆盖公开图片上传：超过限制时返回 413 而非 500。
func TestPublicAICCHandlerMapsImageTooLarge(t *testing.T) {
	router := newPublicAICCTestRouter(t, &publicAICCServiceStub{imageErr: service.ErrConversationFileTooLarge})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/public/aicc/sessions/sess-1/images?filename=a.png", bytes.NewBufferString("image-bytes"))
	request.Header.Set("Content-Type", "application/octet-stream")
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusRequestEntityTooLarge, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "CONVERSATION_FILE_TOO_LARGE")
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
