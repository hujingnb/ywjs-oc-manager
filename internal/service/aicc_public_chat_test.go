package service

import (
	"context"
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"oc-manager/internal/integrations/ocops"
)

// AICC 公开会话不能把自身 UUID 当作 Hermes sid；应先创建 Hermes 会话再续聊。
func TestAICCPublicHermesChatCreatesHermesSessionBeforeChat(t *testing.T) {
	ops := &fakeConversationOps{
		chatOut: ocops.ConversationChatResult{
			Message: ocops.ConversationMessage{Content: "您好，我可以介绍产品和服务。"},
		},
	}
	loc := OcOpsAppLocation{Supported: true, Endpoint: ocops.Endpoint{BaseURL: "http://runtime"}}
	svc := NewAICCPublicHermesChat(ops, &fakeOcOpsResolver{loc: loc})

	reply, err := svc.ChatAICC(context.Background(), AICCInboundTurn{AppID: "app-1", SessionID: "aicc-session-1", TurnID: "turn-1", Text: "你好"})

	require.NoError(t, err)
	assert.Equal(t, "您好，我可以介绍产品和服务。", reply.Text)
	assert.Empty(t, ops.listSource)
	assert.Equal(t, 1, ops.createCalls)
	assert.Equal(t, "web", ops.createReq.Source)
	assert.Equal(t, "AICC turn turn-1", ops.createReq.Title)
	assert.Equal(t, "new", ops.gotSID)
	assert.Contains(t, ops.lastReq.Message, "<current_visitor_message>\n你好")
}

// TestAICCPublicHermesChatReturnsTypedOverloadError 覆盖模型上游过载诊断：
// Hermes 可能把失败诊断放进成功响应，公开端必须返回可被 dispatcher 重试的状态错误。
func TestAICCPublicHermesChatReturnsTypedOverloadError(t *testing.T) {
	ops := &fakeConversationOps{chatOut: ocops.ConversationChatResult{Message: ocops.ConversationMessage{Content: "API call failed after 3 retries: HTTP 529."}}}
	loc := OcOpsAppLocation{Supported: true, Endpoint: ocops.Endpoint{BaseURL: "http://runtime"}}
	svc := NewAICCPublicHermesChat(ops, &fakeOcOpsResolver{loc: loc})

	reply, err := svc.ChatAICC(context.Background(), AICCInboundTurn{AppID: "app-1", SessionID: "aicc-session-1", TurnID: "turn-1", Text: "你好"})

	require.Error(t, err)
	assert.Empty(t, reply)
	assert.True(t, isAICCRetryable(err))
}

// TestAICCPublicHermesChatPreservesHTTPOverloadStatus 覆盖 oc-ops HTTP 错误映射：
// 真实 429/503/529 必须保留为 dispatcher 可识别的重试错误，不能退化成通用 CLI 错误。
func TestAICCPublicHermesChatPreservesHTTPOverloadStatus(t *testing.T) {
	ops := &fakeConversationOps{chatErr: &ocops.HTTPStatusError{StatusCode: 503, Err: ocops.ErrCLI}}
	loc := OcOpsAppLocation{Supported: true, Endpoint: ocops.Endpoint{BaseURL: "http://runtime"}}
	svc := NewAICCPublicHermesChat(ops, &fakeOcOpsResolver{loc: loc})

	_, err := svc.ChatAICC(context.Background(), AICCInboundTurn{AppID: "app-1", SessionID: "aicc-session-1", TurnID: "turn-1", Text: "你好"})

	require.Error(t, err)
	assert.True(t, isAICCRetryable(err))
	assert.ErrorIs(t, err, ErrConversationCLI)
}

// TestAICCPublicHermesChatPreservesNetworkTimeout 覆盖运行时网络超时：
// 底层 net.Error 必须穿透会话映射，以便 dispatcher 将短暂网络故障安排重试。
func TestAICCPublicHermesChatPreservesNetworkTimeout(t *testing.T) {
	ops := &fakeConversationOps{chatErr: &net.DNSError{IsTimeout: true}}
	loc := OcOpsAppLocation{Supported: true, Endpoint: ocops.Endpoint{BaseURL: "http://runtime"}}
	svc := NewAICCPublicHermesChat(ops, &fakeOcOpsResolver{loc: loc})

	_, err := svc.ChatAICC(context.Background(), AICCInboundTurn{AppID: "app-1", SessionID: "aicc-session-1", TurnID: "turn-1", Text: "你好"})

	require.Error(t, err)
	assert.True(t, isAICCRetryable(err))
}

// TestMapOcOpsConversationErrKeepsGenericCLIContract 覆盖普通 Hermes 调用契约：
// 即使 oc-ops 返回 503，非 AICC 的通用映射仍必须保持 ErrConversationCLI（handler 映射 502）。
func TestMapOcOpsConversationErrKeepsGenericCLIContract(t *testing.T) {
	err := mapOcOpsConversationErr(&ocops.HTTPStatusError{StatusCode: 503, Err: ocops.ErrCLI})

	assert.ErrorIs(t, err, ErrConversationCLI)
	assert.False(t, isAICCRetryable(err))
}

// AICC 每轮都必须创建独立 Hermes 会话，不能复用容器内的历史会话状态。
func TestAICCPublicHermesChatCreatesIndependentSessionForEveryTurn(t *testing.T) {
	ops := &fakeConversationOps{
		sessions: []ocops.ConversationSession{
			{ID: "runtime-session", Source: "api_server", Title: "AICC aicc-session-1"},
		},
		chatOut: ocops.ConversationChatResult{
			Message: ocops.ConversationMessage{Content: "已复用运行时会话"},
		},
	}
	loc := OcOpsAppLocation{Supported: true, Endpoint: ocops.Endpoint{BaseURL: "http://runtime"}}
	svc := NewAICCPublicHermesChat(ops, &fakeOcOpsResolver{loc: loc})

	reply, err := svc.ChatAICC(context.Background(), AICCInboundTurn{AppID: "app-1", SessionID: "aicc-session-1", TurnID: "turn-2", Text: "你好"})

	require.NoError(t, err)
	assert.Equal(t, "已复用运行时会话", reply.Text)
	assert.Empty(t, ops.listSource)
	assert.Equal(t, 1, ops.createCalls)
	assert.Equal(t, "new", ops.gotSID)
}
