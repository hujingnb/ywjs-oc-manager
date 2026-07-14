package service

import (
	"context"
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

	reply, err := svc.ChatAICC(context.Background(), "app-1", "aicc-session-1", "你好")

	require.NoError(t, err)
	assert.Equal(t, "您好，我可以介绍产品和服务。", reply)
	assert.Empty(t, ops.listSource)
	assert.Equal(t, 1, ops.createCalls)
	assert.Equal(t, "web", ops.createReq.Source)
	assert.Equal(t, "AICC aicc-session-1", ops.createReq.Title)
	assert.Equal(t, "new", ops.gotSID)
	assert.Equal(t, "你好", ops.lastReq.Message)
}

// TestAICCPublicHermesChatReturnsTypedOverloadError 覆盖模型上游过载诊断：
// Hermes 可能把失败诊断放进成功响应，公开端必须返回可被 dispatcher 重试的状态错误。
func TestAICCPublicHermesChatReturnsTypedOverloadError(t *testing.T) {
	ops := &fakeConversationOps{chatOut: ocops.ConversationChatResult{Message: ocops.ConversationMessage{Content: "API call failed after 3 retries: HTTP 529."}}}
	loc := OcOpsAppLocation{Supported: true, Endpoint: ocops.Endpoint{BaseURL: "http://runtime"}}
	svc := NewAICCPublicHermesChat(ops, &fakeOcOpsResolver{loc: loc})

	reply, err := svc.ChatAICC(context.Background(), "app-1", "aicc-session-1", "你好")

	require.Error(t, err)
	assert.Empty(t, reply)
	assert.True(t, isAICCRetryable(err))
}

// AICC 公开会话查找 Hermes 会话时不能按 web 过滤：Hermes 创建时接受 web，
// 但持久化后可能回显为 api_server；按 web 过滤会查不到已有 title 并重复创建触发 400。
func TestAICCPublicHermesChatReusesAPIServerSessionByTitle(t *testing.T) {
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

	reply, err := svc.ChatAICC(context.Background(), "app-1", "aicc-session-1", "你好")

	require.NoError(t, err)
	assert.Equal(t, "已复用运行时会话", reply)
	assert.Empty(t, ops.listSource)
	assert.Equal(t, 0, ops.createCalls)
	assert.Equal(t, "runtime-session", ops.gotSID)
}
