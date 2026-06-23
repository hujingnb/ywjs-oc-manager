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
