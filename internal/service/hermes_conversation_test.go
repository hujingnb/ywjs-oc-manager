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
	sessions    []ocops.ConversationSession
	chatOut     ocops.ConversationChatResult
	gotSID      string
	lastReq     ocops.ConversationChatReq // 记录最后一次 SessionChat/SessionChatStream 的请求，供富化断言
	createCalls int
	createReq   ocops.ConversationCreateReq
}

func (f *fakeConversationOps) ListSessions(_ context.Context, _ ocops.Endpoint, _ string, _, _ int) ([]ocops.ConversationSession, error) {
	return f.sessions, nil
}
func (f *fakeConversationOps) SessionMessages(_ context.Context, _ ocops.Endpoint, sid string) ([]ocops.ConversationMessage, error) {
	f.gotSID = sid
	return nil, nil
}
func (f *fakeConversationOps) CreateSession(_ context.Context, _ ocops.Endpoint, req ocops.ConversationCreateReq) (ocops.ConversationSession, error) {
	f.createCalls++
	f.createReq = req
	return ocops.ConversationSession{ID: "new"}, nil
}
func (f *fakeConversationOps) DeleteSession(_ context.Context, _ ocops.Endpoint, _ string) error {
	return nil
}
func (f *fakeConversationOps) SessionChat(_ context.Context, _ ocops.Endpoint, sid string, req ocops.ConversationChatReq) (ocops.ConversationChatResult, error) {
	f.gotSID = sid
	f.lastReq = req
	return f.chatOut, nil
}
func (f *fakeConversationOps) SessionChatStream(_ context.Context, _ ocops.Endpoint, sid string, req ocops.ConversationChatReq) (<-chan ocops.ConversationStreamEvent, error) {
	f.gotSID = sid
	f.lastReq = req
	// 返回一个预填单条事件后关闭的 channel，供调用方断言
	ch := make(chan ocops.ConversationStreamEvent, 1)
	ch <- ocops.ConversationStreamEvent{Event: "assistant.delta", Payload: []byte(`{"delta":"hi"}`)}
	close(ch)
	return ch, nil
}
func (f *fakeConversationOps) UpdateSessionTitle(_ context.Context, _ ocops.Endpoint, sid, title string) (ocops.ConversationSession, error) {
	f.gotSID = sid
	return ocops.ConversationSession{ID: sid, Title: title}, nil
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

// 重命名会话（授权用户 + 正常路径）：返回含新标题的会话对象。
func TestConversationServiceRename(t *testing.T) {
	ops := &fakeConversationOps{}
	loc := OcOpsAppLocation{OrgID: "o1", OwnerUserID: "u1", Supported: true, Endpoint: ocops.Endpoint{BaseURL: "http://x"}}
	svc := NewHermesConversationService(ops, &fakeOcOpsResolver{loc: loc})
	// 实例主（org_member 且为 OwnerUserID），有写权限
	p := auth.Principal{Role: domain.UserRoleOrgMember, OrgID: "o1", UserID: "u1"}
	out, err := svc.Rename(context.Background(), p, "app-1", "s1", "新标题")
	require.NoError(t, err)
	// ops 应返回含新标题的会话，sid 透传正确
	assert.Equal(t, "s1", out.ID)
	assert.Equal(t, "新标题", out.Title)
}

// 空白标题被 service 校验拦截，返回 ErrConversationBadRequest，不透传上游。
func TestConversationServiceRenameEmpty(t *testing.T) {
	ops := &fakeConversationOps{}
	loc := OcOpsAppLocation{OrgID: "o1", OwnerUserID: "u1", Supported: true, Endpoint: ocops.Endpoint{BaseURL: "http://x"}}
	svc := NewHermesConversationService(ops, &fakeOcOpsResolver{loc: loc})
	p := auth.Principal{Role: domain.UserRoleOrgMember, OrgID: "o1", UserID: "u1"}
	// 空白标题应被 service 层拦截，不透传 oc-ops
	_, err := svc.Rename(context.Background(), p, "app-1", "s1", "   ")
	require.ErrorIs(t, err, ErrConversationBadRequest)
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

// fileResolverFunc 把函数适配成 ConversationFileResolver 接口，便于在测试中内联注入解析逻辑。
type fileResolverFunc func(ctx context.Context, appID, sid, fileID string) (string, string, string, error)

// ResolveFileURL 实现 ConversationFileResolver 接口，直接委派内部函数。
func (f fileResolverFunc) ResolveFileURL(ctx context.Context, appID, sid, fileID string) (string, string, string, error) {
	return f(ctx, appID, sid, fileID)
}

// 发送含 input_file part 的多模态消息：service 把 file_id 富化为预签名 file_url 等元数据后转 oc-ops。
// 覆盖正常路径：文字 part 原样保留，文件 part 被补全 file_url/filename/mime。
func TestChatEnrichesFileParts(t *testing.T) {
	ops := &fakeConversationOps{}
	loc := OcOpsAppLocation{OrgID: "o1", OwnerUserID: "u1", Supported: true, Endpoint: ocops.Endpoint{BaseURL: "http://x"}}
	svc := NewHermesConversationService(ops, &fakeOcOpsResolver{loc: loc})
	// 注入解析器：断言收到的 file_id，并返回固定预签名 URL 与元数据
	svc.SetFileResolver(fileResolverFunc(func(ctx context.Context, appID, sid, fileID string) (string, string, string, error) {
		assert.Equal(t, "f1", fileID)
		return "https://s3/x", "a.pdf", "application/pdf", nil
	}))
	p := auth.Principal{Role: domain.UserRoleOrgMember, OrgID: "o1", UserID: "u1"}
	// 多模态消息：第 0 个文字 part + 第 1 个文件 part（仅含 file_id）
	msg := []any{
		map[string]any{"type": "text", "text": "看看这个"},
		map[string]any{"type": "input_file", "file_id": "f1"},
	}
	_, err := svc.Chat(context.Background(), p, "app1", "s1", msg)
	require.NoError(t, err)
	// 转发给 oc-ops 的 Message 应仍是 parts 数组，文件 part 已被富化
	parts, ok := ops.lastReq.Message.([]any)
	require.True(t, ok)
	fp := parts[1].(map[string]any)
	assert.Equal(t, "https://s3/x", fp["file_url"])
	assert.Equal(t, "a.pdf", fp["filename"])
	assert.Equal(t, "application/pdf", fp["mime"])
}

// 纯文件、无文字也允许：放宽空消息校验，只要有 input_file part 即视为有内容。
func TestChatAllowsFileOnly(t *testing.T) {
	ops := &fakeConversationOps{}
	loc := OcOpsAppLocation{OrgID: "o1", OwnerUserID: "u1", Supported: true, Endpoint: ocops.Endpoint{BaseURL: "http://x"}}
	svc := NewHermesConversationService(ops, &fakeOcOpsResolver{loc: loc})
	svc.SetFileResolver(fileResolverFunc(func(ctx context.Context, a, s, f string) (string, string, string, error) {
		return "https://s3/x", "a.pdf", "application/pdf", nil
	}))
	p := auth.Principal{Role: domain.UserRoleOrgMember, OrgID: "o1", UserID: "u1"}
	// 仅含一个文件 part、无任何文字，应被接受
	_, err := svc.Chat(context.Background(), p, "app1", "s1",
		[]any{map[string]any{"type": "input_file", "file_id": "f1"}})
	require.NoError(t, err)
}

// input_file 缺 file_id 时富化拒绝，返回 ErrConversationBadRequest。
func TestChatRejectsFilePartWithoutID(t *testing.T) {
	ops := &fakeConversationOps{}
	loc := OcOpsAppLocation{OrgID: "o1", OwnerUserID: "u1", Supported: true, Endpoint: ocops.Endpoint{BaseURL: "http://x"}}
	svc := NewHermesConversationService(ops, &fakeOcOpsResolver{loc: loc})
	svc.SetFileResolver(fileResolverFunc(func(ctx context.Context, a, s, f string) (string, string, string, error) {
		return "https://s3/x", "a.pdf", "application/pdf", nil
	}))
	p := auth.Principal{Role: domain.UserRoleOrgMember, OrgID: "o1", UserID: "u1"}
	// 文件 part 缺少 file_id，应在富化阶段被拒
	_, err := svc.Chat(context.Background(), p, "app1", "s1",
		[]any{map[string]any{"type": "input_file"}})
	require.ErrorIs(t, err, ErrConversationBadRequest)
}
