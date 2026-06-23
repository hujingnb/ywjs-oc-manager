package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/integrations/ocops"
)

// fakeConversationOps 是 conversationOps 的假实现，记录入参并返回预设值。
type fakeConversationOps struct {
	sessions []ocops.ConversationSession
	chatOut  ocops.ConversationChatResult
	gotSID   string
}

func (f *fakeConversationOps) ListSessions(_ context.Context, _ ocops.Endpoint, _ string, _, _ int) ([]ocops.ConversationSession, error) {
	return f.sessions, nil
}
func (f *fakeConversationOps) SessionMessages(_ context.Context, _ ocops.Endpoint, sid string) ([]ocops.ConversationMessage, error) {
	f.gotSID = sid
	return nil, nil
}
func (f *fakeConversationOps) CreateSession(_ context.Context, _ ocops.Endpoint, _ ocops.ConversationCreateReq) (ocops.ConversationSession, error) {
	return ocops.ConversationSession{ID: "new"}, nil
}
func (f *fakeConversationOps) DeleteSession(_ context.Context, _ ocops.Endpoint, _ string) error {
	return nil
}
func (f *fakeConversationOps) SessionChat(_ context.Context, _ ocops.Endpoint, sid string, _ ocops.ConversationChatReq) (ocops.ConversationChatResult, error) {
	f.gotSID = sid
	return f.chatOut, nil
}
func (f *fakeConversationOps) SessionChatStream(_ context.Context, _ ocops.Endpoint, sid string, _ ocops.ConversationChatReq) (<-chan ocops.ConversationStreamEvent, error) {
	f.gotSID = sid
	// 返回一个预填单条事件后关闭的 channel，供调用方断言
	ch := make(chan ocops.ConversationStreamEvent, 1)
	ch <- ocops.ConversationStreamEvent{Event: "assistant.delta", Payload: []byte(`{"delta":"hi"}`)}
	close(ch)
	return ch, nil
}

// 有权用户可列会话，透传 ops 返回。
func TestConversationServiceList(t *testing.T) {
	ops := &fakeConversationOps{sessions: []ocops.ConversationSession{{ID: "s1"}}}
	loc := OcOpsAppLocation{OrgID: "o1", OwnerUserID: "u1", Supported: true, Endpoint: ocops.Endpoint{BaseURL: "http://x"}}
	svc := NewHermesConversationService(ops, &fakeOcOpsResolver{loc: loc})
	// 实例主（org_member 且为 OwnerUserID），应有查看权限
	p := auth.Principal{Role: domain.UserRoleOrgMember, OrgID: "o1", UserID: "u1"}
	out, err := svc.ListSessions(context.Background(), p, "app-1", "", 50, 0)
	require.NoError(t, err)
	assert.Equal(t, "s1", out[0].ID)
}

// 无权用户被拒。
func TestConversationServiceForbidden(t *testing.T) {
	ops := &fakeConversationOps{}
	loc := OcOpsAppLocation{OrgID: "o1", OwnerUserID: "u1", Supported: true, Endpoint: ocops.Endpoint{BaseURL: "http://x"}}
	svc := NewHermesConversationService(ops, &fakeOcOpsResolver{loc: loc})
	// 非实例主（UserID 为 u2 而非 u1），应被拒绝
	p := auth.Principal{Role: domain.UserRoleOrgMember, OrgID: "o1", UserID: "u2"}
	_, err := svc.ListSessions(context.Background(), p, "app-1", "", 50, 0)
	require.ErrorIs(t, err, ErrConversationForbidden)
}

// 续聊空消息被校验拒绝。
func TestConversationServiceChatEmpty(t *testing.T) {
	ops := &fakeConversationOps{}
	loc := OcOpsAppLocation{OrgID: "o1", OwnerUserID: "u1", Supported: true, Endpoint: ocops.Endpoint{BaseURL: "http://x"}}
	svc := NewHermesConversationService(ops, &fakeOcOpsResolver{loc: loc})
	p := auth.Principal{Role: domain.UserRoleOrgMember, OrgID: "o1", UserID: "u1"}
	// 消息为空白字符，应被 service 层拦截返回 ErrConversationBadRequest
	_, err := svc.Chat(context.Background(), p, "app-1", "s1", "   ")
	require.ErrorIs(t, err, ErrConversationBadRequest)
}

// 流式续聊（正常路径）：有权用户获得事件 channel，channel 包含预设事件。
func TestConversationServiceChatStream(t *testing.T) {
	ops := &fakeConversationOps{}
	loc := OcOpsAppLocation{OrgID: "o1", OwnerUserID: "u1", Supported: true, Endpoint: ocops.Endpoint{BaseURL: "http://x"}}
	svc := NewHermesConversationService(ops, &fakeOcOpsResolver{loc: loc})
	// 实例主（org_member 且为 OwnerUserID），应有管理（写）权限
	p := auth.Principal{Role: domain.UserRoleOrgMember, OrgID: "o1", UserID: "u1"}
	ch, err := svc.ChatStream(context.Background(), p, "app-1", "s1", "hello")
	require.NoError(t, err)
	// 从 channel 读出所有事件，断言含预设事件
	var events []ocops.ConversationStreamEvent
	for ev := range ch {
		events = append(events, ev)
	}
	require.Len(t, events, 1)
	assert.Equal(t, "assistant.delta", events[0].Event)
}

// 流式续聊空消息被校验拒绝，返回 ErrConversationBadRequest。
func TestConversationServiceChatStreamEmpty(t *testing.T) {
	ops := &fakeConversationOps{}
	loc := OcOpsAppLocation{OrgID: "o1", OwnerUserID: "u1", Supported: true, Endpoint: ocops.Endpoint{BaseURL: "http://x"}}
	svc := NewHermesConversationService(ops, &fakeOcOpsResolver{loc: loc})
	p := auth.Principal{Role: domain.UserRoleOrgMember, OrgID: "o1", UserID: "u1"}
	// 空白消息应被 service 层拦截，不透传上游
	_, err := svc.ChatStream(context.Background(), p, "app-1", "s1", "")
	require.ErrorIs(t, err, ErrConversationBadRequest)
}
