package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"oc-manager/internal/auth"
	"oc-manager/internal/integrations/ocops"
	"oc-manager/internal/service"
)

// stubConversationService 实现 handler 依赖的窄接口。
type stubConversationService struct {
	sessions []ocops.ConversationSession
	err      error
	gotMsg   string
}

func (s *stubConversationService) ListSessions(_ context.Context, _ auth.Principal, _, _ string, _, _ int) ([]ocops.ConversationSession, error) {
	return s.sessions, s.err
}
func (s *stubConversationService) Messages(_ context.Context, _ auth.Principal, _, _ string) ([]ocops.ConversationMessage, error) {
	return nil, s.err
}
func (s *stubConversationService) CreateSession(_ context.Context, _ auth.Principal, _, _ string) (ocops.ConversationSession, error) {
	return ocops.ConversationSession{ID: "new"}, s.err
}
func (s *stubConversationService) DeleteSession(_ context.Context, _ auth.Principal, _, _ string) error {
	return s.err
}
func (s *stubConversationService) Chat(_ context.Context, _ auth.Principal, _, _, msg string) (ocops.ConversationChatResult, error) {
	s.gotMsg = msg
	return ocops.ConversationChatResult{Message: ocops.ConversationMessage{Role: "assistant", Content: "ok"}}, s.err
}
func (s *stubConversationService) ChatStream(_ context.Context, _ auth.Principal, _, _, msg string) (<-chan ocops.ConversationStreamEvent, error) {
	s.gotMsg = msg
	if s.err != nil {
		return nil, s.err
	}
	// 预填一条事件后关闭 channel，模拟流式输出
	ch := make(chan ocops.ConversationStreamEvent, 1)
	ch <- ocops.ConversationStreamEvent{Event: "assistant.delta", Payload: []byte(`{"delta":"hi"}`)}
	close(ch)
	return ch, nil
}
func (s *stubConversationService) Rename(_ context.Context, _ auth.Principal, _, sid, title string) (ocops.ConversationSession, error) {
	return ocops.ConversationSession{ID: sid, Title: title}, s.err
}

func newConvTestRouter(svc conversationHandlerService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	RegisterHermesConversationRoutes(r, NewHermesConversationHandler(svc))
	return r
}

// GET 列会话返回 200 + sessions 包。
func TestHandlerListConversations(t *testing.T) {
	svc := &stubConversationService{sessions: []ocops.ConversationSession{{ID: "s1"}}}
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/apps/app-1/hermes/conversations", nil)
	newConvTestRouter(svc).ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "s1")
}

// 续聊透传 message，返回 assistant 回复。
func TestHandlerChat(t *testing.T) {
	svc := &stubConversationService{}
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/apps/app-1/hermes/conversations/s1/chat",
		strings.NewReader(`{"message":"hi"}`))
	req.Header.Set("Content-Type", "application/json")
	newConvTestRouter(svc).ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "hi", svc.gotMsg)
}

// 无权（service 返回 ErrConversationForbidden）映射 403。
func TestHandlerForbidden(t *testing.T) {
	svc := &stubConversationService{err: service.ErrConversationForbidden}
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/apps/app-1/hermes/conversations", nil)
	newConvTestRouter(svc).ServeHTTP(w, req)
	require.Equal(t, http.StatusForbidden, w.Code)
}

// 重命名会话：PATCH 携带 {"title":"新名"} → 200，响应体含 session.title。
func TestHandlerRename(t *testing.T) {
	svc := &stubConversationService{}
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/apps/app-1/hermes/conversations/s1",
		strings.NewReader(`{"title":"新名"}`))
	req.Header.Set("Content-Type", "application/json")
	newConvTestRouter(svc).ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	// 响应体应含 session 字段且 title 正确
	assert.Contains(t, w.Body.String(), "新名")
	assert.Contains(t, w.Body.String(), "session")
}

// 流式续聊：stub 返回预填 channel，handler 写出 SSE 帧，响应体含 assistant.delta 事件。
func TestHandlerChatStream(t *testing.T) {
	svc := &stubConversationService{}
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/apps/app-1/hermes/conversations/s1/chat/stream",
		strings.NewReader(`{"message":"hi"}`))
	req.Header.Set("Content-Type", "application/json")
	newConvTestRouter(svc).ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	// 响应体应包含事件 JSON 帧（assistant.delta 事件名）
	assert.Contains(t, w.Body.String(), "assistant.delta")
}
