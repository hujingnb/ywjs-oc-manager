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
	detailResult   service.AICCPublicSessionDetailResult
	sessionErr     error
	detailErr      error
	imageResult    service.AICCPublicImageResult
	imageErr       error
	messageResult  service.AICCPublicMessageResult
	messageErr     error
	leadResult     service.AICCPublicLeadValuesResult
	leadErr        error
	feedbackResult service.AICCPublicFeedbackResult
	feedbackErr    error
	resolveResult  service.AICCPublicResolutionResult
	resolveErr     error
	consentErr     error

	lastPublicToken   string
	lastConfigChannel string
	lastSessionToken  string
	lastSessionInput  service.AICCPublicSessionInput
	lastImageInput    service.AICCPublicImageInput
	lastMessageInput  service.AICCPublicMessageInput
	lastLeadInput     service.AICCPublicLeadValuesInput
	lastFeedbackInput service.AICCPublicFeedbackInput
	lastResolveToken  string
}

func (s *publicAICCServiceStub) PublicConfig(_ context.Context, publicToken, channel string) (service.AICCPublicConfigResult, error) {
	s.lastPublicToken = publicToken
	s.lastConfigChannel = channel
	return s.configResult, s.configErr
}

func (s *publicAICCServiceStub) CreateSession(_ context.Context, publicToken string, input service.AICCPublicSessionInput) (service.AICCPublicSessionResult, error) {
	s.lastPublicToken = publicToken
	s.lastSessionInput = input
	return s.sessionResult, s.sessionErr
}

func (s *publicAICCServiceStub) GetSession(_ context.Context, sessionToken string) (service.AICCPublicSessionDetailResult, error) {
	s.lastSessionToken = sessionToken
	return s.detailResult, s.detailErr
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

func (s *publicAICCServiceStub) ResolveSession(_ context.Context, sessionToken string) (service.AICCPublicResolutionResult, error) {
	s.lastResolveToken = sessionToken
	return s.resolveResult, s.resolveErr
}

func newPublicAICCTestRouter(t *testing.T, svc publicAICCService) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	RegisterPublicAICCRoutes(router, NewPublicAICCHandler(svc))
	return router
}

// TestPublicAICCHandlerConfigPassesChannel 覆盖公开配置入口：网页挂件 iframe 通过 query
// 传入渠道，handler 必须透传给 service 才能按 widget_token 查找智能体。
func TestPublicAICCHandlerConfigPassesChannel(t *testing.T) {
	svc := &publicAICCServiceStub{configResult: service.AICCPublicConfigResult{Name: "售前接待"}}
	router := newPublicAICCTestRouter(t, svc)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/public/aicc/agents/widget-token/config?channel=web_widget", nil)
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "售前接待")
	assert.Equal(t, "widget-token", svc.lastPublicToken)
	assert.Equal(t, "web_widget", svc.lastConfigChannel)
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

// TestPublicAICCHandlerGetSession 覆盖公开会话刷新恢复：
// 访客持有 session token 时可读取本会话消息，用于刷新页面后恢复对话内容。
func TestPublicAICCHandlerGetSession(t *testing.T) {
	svc := &publicAICCServiceStub{detailResult: service.AICCPublicSessionDetailResult{
		Messages: []service.AICCMessageResult{{ID: "msg-1", Direction: "visitor", Text: "报价多少"}},
	}}
	router := newPublicAICCTestRouter(t, svc)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/public/aicc/sessions/sess-1", nil)
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	assert.Equal(t, "sess-1", svc.lastSessionToken)
	assert.Contains(t, recorder.Body.String(), "报价多少")
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

// TestPublicAICCHandlerResolveSession 覆盖公开会话级已解决入口：
// session token 只来自路径，不再要求绑定某条助手消息。
func TestPublicAICCHandlerResolveSession(t *testing.T) {
	svc := &publicAICCServiceStub{resolveResult: service.AICCPublicResolutionResult{ResolutionStatus: "resolved"}}
	router := newPublicAICCTestRouter(t, svc)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/public/aicc/sessions/sess-1/resolve", nil)
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "resolved")
	assert.Equal(t, "sess-1", svc.lastResolveToken)
}

// TestPublicAICCHandlerMapsConversationGates 覆盖公开访客消息入口的隐私同意和留资阻断错误映射。
func TestPublicAICCHandlerMapsConversationGates(t *testing.T) {
	cases := []struct {
		name   string // 子场景说明
		err    error
		status int
		code   string
	}{
		{name: "未同意隐私说明返回稳定 code", err: service.ErrAICCConsentRequired, status: http.StatusConflict, code: "AICC_CONSENT_REQUIRED"},                  // 场景：consent_required 模式未同意。
		{name: "缺少必填留资返回稳定 code", err: service.ErrAICCLeadRequired, status: http.StatusConflict, code: "AICC_LEAD_REQUIRED"},                         // 场景：必填字段未完成。
		{name: "敏感词拦截返回稳定 code", err: service.ErrAICCSensitiveWord, status: http.StatusBadRequest, code: "AICC_SENSITIVE_WORD"},                      // 场景：访客消息命中敏感词配置。
		{name: "消息上限拦截返回稳定 code", err: service.ErrAICCMessageLimitExceeded, status: http.StatusTooManyRequests, code: "AICC_MESSAGE_LIMIT_EXCEEDED"}, // 场景：当前会话访客消息数已达上限。
		{name: "封禁访客拦截返回稳定 code", err: service.ErrAICCVisitorBlocked, status: http.StatusForbidden, code: "AICC_VISITOR_BLOCKED"},                    // 场景：当前访客命中有效封禁名单。
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			router := newPublicAICCTestRouter(t, &publicAICCServiceStub{messageErr: tc.err})

			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, "/api/v1/public/aicc/sessions/sess-1/messages", bytes.NewBufferString(`{"text":"你好"}`))
			request.Header.Set("Content-Type", "application/json")
			router.ServeHTTP(recorder, request)

			require.Equal(t, tc.status, recorder.Code)
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

// TestPublicAICCHandlerCreateSessionPassesSessionToken 覆盖刷新续接：
// 公开创建会话接口必须把访客端保存的 token 透传给 service。
func TestPublicAICCHandlerCreateSessionPassesSessionToken(t *testing.T) {
	svc := &publicAICCServiceStub{sessionResult: service.AICCPublicSessionResult{SessionToken: "tok", Restored: true}}
	router := newPublicAICCTestRouter(t, svc)

	request := httptest.NewRequest(http.MethodPost, "/api/v1/public/aicc/agents/pub/sessions", bytes.NewBufferString(`{"channel":"web_link","session_token":"tok"}`))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)

	assert.Equal(t, http.StatusCreated, recorder.Code)
	assert.Equal(t, "tok", svc.lastSessionInput.SessionToken)
	assert.Contains(t, recorder.Body.String(), `"restored":true`)
}

// TestPublicAICCHandlerCreateSessionPassesRequestMetadata 覆盖公开会话安全元数据：
// handler 必须把 Origin、客户端地址和 User-Agent 交给 service 做域名白名单与 hash 存储。
func TestPublicAICCHandlerCreateSessionPassesRequestMetadata(t *testing.T) {
	svc := &publicAICCServiceStub{sessionResult: service.AICCPublicSessionResult{SessionToken: "sess-token", PrivacyMode: "notice"}}
	router := newPublicAICCTestRouter(t, svc)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/public/aicc/agents/pub/sessions", bytes.NewBufferString(`{"channel":"web_widget"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "https://shop.example.com")
	request.Header.Set("User-Agent", "AICC Browser")
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusCreated, recorder.Code)
	assert.Equal(t, "web_widget", svc.lastSessionInput.Channel)
	assert.Equal(t, "https://shop.example.com", svc.lastSessionInput.Origin)
	assert.Equal(t, "AICC Browser", svc.lastSessionInput.UserAgent)
	assert.NotEmpty(t, svc.lastSessionInput.RemoteIP)
}

// TestPublicAICCHandlerMapsDomainForbidden 覆盖挂件域名白名单拒绝：返回 403 和稳定错误码。
func TestPublicAICCHandlerMapsDomainForbidden(t *testing.T) {
	router := newPublicAICCTestRouter(t, &publicAICCServiceStub{sessionErr: service.ErrAICCDomainForbidden})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/public/aicc/agents/pub/sessions", bytes.NewBufferString(`{"channel":"web_widget"}`))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusForbidden, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "AICC_DOMAIN_FORBIDDEN")
}

// TestPublicAICCHandlerMapsRateLimited 覆盖匿名入口限流：超限时返回 429 和稳定错误码。
func TestPublicAICCHandlerMapsRateLimited(t *testing.T) {
	router := newPublicAICCTestRouter(t, &publicAICCServiceStub{sessionErr: service.ErrRateLimited})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/public/aicc/agents/pub/sessions", bytes.NewBufferString(`{"channel":"web_link"}`))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusTooManyRequests, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "RATE_LIMITED")
}
