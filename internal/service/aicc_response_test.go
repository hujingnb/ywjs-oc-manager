package service

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestParseAndValidateAICCResponseEnvelopeAcceptsKnowledgeSource 覆盖企业知识库答复：
// 只有本轮工具审计出现的引用，才能作为面向访客的回答依据。
func TestParseAndValidateAICCResponseEnvelopeAcceptsKnowledgeSource(t *testing.T) {
	audit := AICCResponseToolAudit{"kb-1": {Type: "knowledge", Title: "产品手册", ReferenceID: "kb-1"}}
	reply, err := ParseAndValidateAICCResponse(`{"text":"企业版包含知识库问答。","sources":[{"type":"knowledge","title":"产品手册","reference_id":"kb-1"}],"next_action":"none","flags":{}}`, audit)

	require.NoError(t, err)
	assert.Equal(t, "企业版包含知识库问答。", reply.Text)
	require.Len(t, reply.Sources, 1)
	assert.Equal(t, "kb-1", reply.Sources[0].ReferenceID)
}

// TestParseAndValidateAICCResponseEnvelopeRejectsUnconfirmedEnterpriseNetwork 覆盖企业官网以外的
// 网络信息：即使来源来自本轮工具，也必须明确标为未经企业确认，避免被误认为企业承诺。
func TestParseAndValidateAICCResponseEnvelopeRejectsUnconfirmedEnterpriseNetwork(t *testing.T) {
	audit := AICCResponseToolAudit{"web-1": {Type: "web", Title: "第三方报道", URL: "https://example.com/news", Scope: "enterprise_network", ReferenceID: "web-1", Unconfirmed: true}}
	_, err := ParseAndValidateAICCResponse(`{"text":"公开网络有相关报道。","sources":[{"type":"web","title":"第三方报道","url":"https://example.com/news","scope":"enterprise_network","reference_id":"web-1"}],"next_action":"none","flags":{}}`, audit)

	require.ErrorIs(t, err, ErrAICCResponsePolicy)
}

// TestParseAndValidateAICCResponseEnvelopeRejectsForgedURL 覆盖模型伪造链接：引用 ID 正确
// 也不能用未在工具审计中出现的 URL 替换真实来源。
func TestParseAndValidateAICCResponseEnvelopeRejectsForgedURL(t *testing.T) {
	audit := AICCResponseToolAudit{"web-1": {Type: "web", Title: "公开页面", URL: "https://example.com/real", Scope: "public_network", ReferenceID: "web-1"}}
	_, err := ParseAndValidateAICCResponse(`{"text":"请查看公开页面。","sources":[{"type":"web","title":"公开页面","url":"https://evil.example/fake","scope":"public_network","reference_id":"web-1"}],"next_action":"none","flags":{}}`, audit)

	require.ErrorIs(t, err, ErrAICCResponsePolicy)
}

// TestParseAndValidateAICCResponseEnvelopeAcceptsPublicNetwork 覆盖普通公开网络依据：
// 经过本轮工具审计且 URL、范围完全一致的公开资料可以作为补充信息展示。
func TestParseAndValidateAICCResponseEnvelopeAcceptsPublicNetwork(t *testing.T) {
	audit := AICCResponseToolAudit{"web-1": {Type: "web", Title: "公开页面", URL: "https://example.com/page", Scope: "public_network", ReferenceID: "web-1"}}
	reply, err := ParseAndValidateAICCResponse(`{"text":"公开网络可查到相关介绍。","sources":[{"type":"web","title":"公开页面","url":"https://example.com/page","scope":"public_network","reference_id":"web-1"}],"next_action":"none","flags":{}}`, audit)

	require.NoError(t, err)
	require.Len(t, reply.Sources, 1)
	assert.Equal(t, "public_network", reply.Sources[0].Scope)
}

// TestParseAndValidateAICCResponseEnvelopeRejectsPriceWithoutKnowledge 覆盖企业价格回答：
// 价格属于企业承诺信息，不能仅依赖公开网络或模型记忆。
func TestParseAndValidateAICCResponseEnvelopeRejectsPriceWithoutKnowledge(t *testing.T) {
	_, err := ParseAndValidateAICCResponse(`{"text":"企业版价格是每月 999 元。","sources":[],"next_action":"none","flags":{}}`, nil)

	require.ErrorIs(t, err, ErrAICCResponsePolicy)
}

// TestParseAndValidateAICCResponseEnvelopeRejectsOperationalClaim 覆盖越权操作完成声称：
// 客服只能说明信息，不能声称已替访客创建、修改或执行外部操作。
func TestParseAndValidateAICCResponseEnvelopeRejectsOperationalClaim(t *testing.T) {
	_, err := ParseAndValidateAICCResponse(`{"text":"我已为您创建账号。","sources":[],"next_action":"none","flags":{}}`, nil)

	require.True(t, errors.Is(err, ErrAICCResponsePolicy))
}

// TestParseAndValidateAICCResponseEnvelopeRejectsIllegalNextAction 覆盖动作白名单：
// 模型不得借结构化字段诱导页面执行未定义动作。
func TestParseAndValidateAICCResponseEnvelopeRejectsIllegalNextAction(t *testing.T) {
	_, err := ParseAndValidateAICCResponse(`{"text":"您好。","sources":[],"next_action":"open_payment","flags":{}}`, nil)

	require.ErrorIs(t, err, ErrAICCResponsePolicy)
}

// TestParseAndValidateAICCResponseEnvelopeRejectsUnknownFlag 覆盖 wire schema 的 flags 边界：
// 结构化回复不得夹带任何未定义的页面控制字段。
func TestParseAndValidateAICCResponseEnvelopeRejectsUnknownFlag(t *testing.T) {
	_, err := ParseAndValidateAICCResponse(`{"text":"您好。","sources":[],"next_action":"none","flags":{"open_payment":true}}`, nil)

	require.ErrorIs(t, err, ErrAICCResponsePolicy)
}

// TestParseAndValidateAICCResponseEnvelopeRequiresSourcesAndFlags 覆盖严格 wire schema：
// 即使来源为空，模型也必须显式输出 sources 和 flags，不能以 null 或省略绕过结构约束。
func TestParseAndValidateAICCResponseEnvelopeRequiresSourcesAndFlags(t *testing.T) {
	for _, raw := range []string{
		`{"text":"您好。","next_action":"none","flags":{}}`,                // 缺少 sources。
		`{"text":"您好。","sources":null,"next_action":"none","flags":{}}`, // sources 不能为 null。
		`{"text":"您好。","sources":[],"next_action":"none"}`,              // 缺少 flags。
		`{"text":"您好。","sources":[],"next_action":"none","flags":null}`, // flags 不能为 null。
	} {
		_, err := ParseAndValidateAICCResponse(raw, nil)
		require.ErrorIs(t, err, ErrAICCResponsePolicy)
	}
}

// TestParseAndValidateAICCResponseEnvelopeRejectsEnterpriseNetworkWhenKnowledgeExists 覆盖冲突裁决：
// 企业知识已经命中时，答复不得再采纳未经企业确认的企业网络材料。
func TestParseAndValidateAICCResponseEnvelopeRejectsEnterpriseNetworkWhenKnowledgeExists(t *testing.T) {
	audit := AICCResponseToolAudit{
		"kb-1":  {Type: "knowledge", Title: "企业手册", ReferenceID: "kb-1"},
		"web-1": {Type: "web", Title: "第三方报道", URL: "https://example.com/news", Scope: "enterprise_network", ReferenceID: "web-1", Unconfirmed: true},
	}
	_, err := ParseAndValidateAICCResponse(`{"text":"请以企业手册为准。","sources":[{"type":"knowledge","title":"企业手册","reference_id":"kb-1"},{"type":"web","title":"第三方报道","url":"https://example.com/news","scope":"enterprise_network","reference_id":"web-1","unconfirmed":true}],"next_action":"none","flags":{}}`, audit)

	require.ErrorIs(t, err, ErrAICCResponsePolicy)
}

// TestParseAndValidateAICCResponseEnvelopeRejectsChineseOperationalVariants 覆盖常见中文操作完成声称：
// 客服不能因账号开通或重置密码等表达而伪装完成外部操作。
func TestParseAndValidateAICCResponseEnvelopeRejectsChineseOperationalVariants(t *testing.T) {
	for _, text := range []string{"已为您开通账号。", "已为你重置密码。", "您的密码已重置。"} {
		_, err := ParseAndValidateAICCResponse(`{"text":"`+text+`","sources":[],"next_action":"none","flags":{}}`, nil)
		require.ErrorIs(t, err, ErrAICCResponsePolicy)
	}
}
