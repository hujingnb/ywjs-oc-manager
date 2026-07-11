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
	assert.Equal(t, "web", ops.listSource)
	assert.Equal(t, 1, ops.createCalls)
	assert.Equal(t, "web", ops.createReq.Source)
	assert.Equal(t, "AICC aicc-session-1", ops.createReq.Title)
	assert.Equal(t, "new", ops.gotSID)
	assert.Equal(t, "你好", ops.lastReq.Message)
}
