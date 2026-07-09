package service

import (
	"context"
	"database/sql"
	"testing"
	"time"

	null "github.com/guregu/null/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"oc-manager/internal/domain"
	"oc-manager/internal/store/sqlc"
)

var aiccPublicTestNow = time.Date(2026, 7, 9, 10, 0, 0, 0, time.UTC)

// TestAICCPublicChatRequiresConsent 覆盖隐私强同意模式：未同意前拒绝访客继续聊天。
func TestAICCPublicChatRequiresConsent(t *testing.T) {
	store := &fakeAICCPublicStore{
		org:     sqlc.Organization{ID: "org-1", AiccEnabled: true},
		agent:   sqlc.AiccAgent{ID: "agent-1", OrgID: "org-1", Status: domain.AICCAgentStatusActive, PrivacyMode: domain.AICCPrivacyModeConsentRequired},
		session: sqlc.AiccSession{ID: "session-1", AgentID: "agent-1", OrgID: "org-1", SessionToken: "tok", ExpiresAt: aiccPublicTestNow.Add(time.Hour)},
	}
	svc := NewAICCPublicService(store, &fakeAICCHermesChat{})
	svc.now = func() time.Time { return aiccPublicTestNow }

	_, err := svc.SendMessage(context.Background(), AICCPublicMessageInput{SessionToken: "tok", Text: "你好"})

	require.ErrorIs(t, err, ErrAICCConsentRequired)
}

// TestAICCPublicChatRequiresLeadFields 覆盖必填留资阻断：必填字段未完成时不能继续提问。
func TestAICCPublicChatRequiresLeadFields(t *testing.T) {
	store := &fakeAICCPublicStore{
		org:                sqlc.Organization{ID: "org-1", AiccEnabled: true},
		agent:              sqlc.AiccAgent{ID: "agent-1", OrgID: "org-1", Status: domain.AICCAgentStatusActive, PrivacyMode: domain.AICCPrivacyModeNotice},
		session:            sqlc.AiccSession{ID: "session-1", AgentID: "agent-1", OrgID: "org-1", SessionToken: "tok", PrivacyNoticeShown: true, ExpiresAt: aiccPublicTestNow.Add(time.Hour)},
		requiredLeadFields: []sqlc.AiccLeadField{{ID: "field-phone", Required: true, FieldKey: "phone", Label: "手机号"}},
	}
	svc := NewAICCPublicService(store, &fakeAICCHermesChat{})
	svc.now = func() time.Time { return aiccPublicTestNow }

	_, err := svc.SendMessage(context.Background(), AICCPublicMessageInput{SessionToken: "tok", Text: "报价多少"})

	require.ErrorIs(t, err, ErrAICCLeadRequired)
}

// TestAICCPublicChatStoresVisitorAndAssistantMessages 覆盖正常路径：访客消息转发 hermes 后保存问答镜像。
func TestAICCPublicChatStoresVisitorAndAssistantMessages(t *testing.T) {
	store := &fakeAICCPublicStore{
		org:     sqlc.Organization{ID: "org-1", AiccEnabled: true},
		agent:   sqlc.AiccAgent{ID: "agent-1", OrgID: "org-1", AppID: "app-1", Status: domain.AICCAgentStatusActive, PrivacyMode: domain.AICCPrivacyModeNotice},
		session: sqlc.AiccSession{ID: "session-1", AgentID: "agent-1", OrgID: "org-1", SessionToken: "tok", PrivacyNoticeShown: true, ExpiresAt: aiccPublicTestNow.Add(time.Hour)},
	}
	chat := &fakeAICCHermesChat{reply: "您好，这是报价说明。"}
	svc := NewAICCPublicService(store, chat)
	svc.now = func() time.Time { return aiccPublicTestNow }

	result, err := svc.SendMessage(context.Background(), AICCPublicMessageInput{SessionToken: "tok", Text: "报价多少"})

	require.NoError(t, err)
	assert.Equal(t, "您好，这是报价说明。", result.Text)
	assert.Equal(t, 2, len(store.createdMessages))
	assert.Equal(t, "app-1", chat.appID)
	assert.Equal(t, "报价多少", chat.text)
}

// TestAICCPublicChatStopsWhenOrgDisabled 覆盖平台关闭企业 AICC 后，已有访客会话也不能继续发送消息。
func TestAICCPublicChatStopsWhenOrgDisabled(t *testing.T) {
	store := &fakeAICCPublicStore{
		org:     sqlc.Organization{ID: "org-1", AiccEnabled: false},
		agent:   sqlc.AiccAgent{ID: "agent-1", OrgID: "org-1", AppID: "app-1", Status: domain.AICCAgentStatusActive, PrivacyMode: domain.AICCPrivacyModeNotice},
		session: sqlc.AiccSession{ID: "session-1", AgentID: "agent-1", OrgID: "org-1", SessionToken: "tok", PrivacyNoticeShown: true, ExpiresAt: aiccPublicTestNow.Add(time.Hour)},
	}
	svc := NewAICCPublicService(store, &fakeAICCHermesChat{})
	svc.now = func() time.Time { return aiccPublicTestNow }

	_, err := svc.SendMessage(context.Background(), AICCPublicMessageInput{SessionToken: "tok", Text: "报价多少"})

	require.ErrorIs(t, err, ErrAICCOffline)
}

// TestAICCPublicChatRejectsExpiredSession 覆盖会话过期边界：过期 session token 不能继续发送消息。
func TestAICCPublicChatRejectsExpiredSession(t *testing.T) {
	store := &fakeAICCPublicStore{
		org:     sqlc.Organization{ID: "org-1", AiccEnabled: true},
		agent:   sqlc.AiccAgent{ID: "agent-1", OrgID: "org-1", AppID: "app-1", Status: domain.AICCAgentStatusActive, PrivacyMode: domain.AICCPrivacyModeNotice},
		session: sqlc.AiccSession{ID: "session-1", AgentID: "agent-1", OrgID: "org-1", SessionToken: "tok", PrivacyNoticeShown: true, ExpiresAt: aiccPublicTestNow.Add(-time.Minute)},
	}
	svc := NewAICCPublicService(store, &fakeAICCHermesChat{})
	svc.now = func() time.Time { return aiccPublicTestNow }

	_, err := svc.SendMessage(context.Background(), AICCPublicMessageInput{SessionToken: "tok", Text: "报价多少"})

	require.ErrorIs(t, err, ErrAICCInvalidSession)
}

// TestAICCPublicChatRejectsImageMessage 覆盖当前文字切片边界：图片消息未实现前不能写入空文本消息。
func TestAICCPublicChatRejectsImageMessage(t *testing.T) {
	store := &fakeAICCPublicStore{
		org:     sqlc.Organization{ID: "org-1", AiccEnabled: true},
		agent:   sqlc.AiccAgent{ID: "agent-1", OrgID: "org-1", AppID: "app-1", Status: domain.AICCAgentStatusActive, PrivacyMode: domain.AICCPrivacyModeNotice},
		session: sqlc.AiccSession{ID: "session-1", AgentID: "agent-1", OrgID: "org-1", SessionToken: "tok", PrivacyNoticeShown: true, ExpiresAt: aiccPublicTestNow.Add(time.Hour)},
	}
	svc := NewAICCPublicService(store, &fakeAICCHermesChat{})
	svc.now = func() time.Time { return aiccPublicTestNow }

	_, err := svc.SendMessage(context.Background(), AICCPublicMessageInput{SessionToken: "tok", ImageFileID: "image-1"})

	require.ErrorIs(t, err, ErrInvalidArgument)
	assert.Empty(t, store.createdMessages)
}

// TestAICCPublicCreateSessionCreatesExpiringSession 覆盖公开会话创建：active 智能体会生成 session token，
// 隐私 notice 模式会记录已展示隐私说明，过期时间跟随智能体 retention_days。
func TestAICCPublicCreateSessionCreatesExpiringSession(t *testing.T) {
	store := &fakeAICCPublicStore{
		org: sqlc.Organization{ID: "org-1", AiccEnabled: true},
		agent: sqlc.AiccAgent{
			ID:            "agent-1",
			OrgID:         "org-1",
			Status:        domain.AICCAgentStatusActive,
			PrivacyMode:   domain.AICCPrivacyModeNotice,
			RetentionDays: 30,
			PublicToken:   "pub",
		},
	}
	svc := NewAICCPublicService(store, &fakeAICCHermesChat{})
	svc.now = func() time.Time { return aiccPublicTestNow }

	result, err := svc.CreateSession(context.Background(), "pub", AICCPublicSessionInput{Channel: domain.AICCChannelWebLink, SourceURL: "https://example.com/pricing"})

	require.NoError(t, err)
	assert.NotEmpty(t, result.SessionToken)
	assert.Equal(t, "agent-1", store.createdSession.AgentID)
	assert.Equal(t, "org-1", store.createdSession.OrgID)
	assert.True(t, store.createdSession.PrivacyNoticeShown)
	assert.Equal(t, time.Date(2026, 8, 8, 10, 0, 0, 0, time.UTC), store.createdSession.ExpiresAt)
}

// TestAICCPublicConsentRejectsInvalidSession 覆盖隐私同意接口：无效或已过期 token 不能伪造成功响应。
func TestAICCPublicConsentRejectsInvalidSession(t *testing.T) {
	store := &fakeAICCPublicStore{
		session: sqlc.AiccSession{ID: "session-1", SessionToken: "tok", ExpiresAt: aiccPublicTestNow.Add(time.Hour)},
	}
	svc := NewAICCPublicService(store, &fakeAICCHermesChat{})
	svc.now = func() time.Time { return aiccPublicTestNow }

	err := svc.Consent(context.Background(), "bad-token")

	require.ErrorIs(t, err, ErrAICCInvalidSession)
}

// TestAICCPublicConfigStopsWhenOrgDisabled 覆盖公开配置读取：企业被平台关闭 AICC 后公开入口返回下线。
func TestAICCPublicConfigStopsWhenOrgDisabled(t *testing.T) {
	store := &fakeAICCPublicStore{
		org: sqlc.Organization{ID: "org-1", AiccEnabled: false},
		agent: sqlc.AiccAgent{
			ID:          "agent-1",
			OrgID:       "org-1",
			Status:      domain.AICCAgentStatusActive,
			PrivacyMode: domain.AICCPrivacyModeNotice,
			PublicToken: "pub",
		},
	}
	svc := NewAICCPublicService(store, &fakeAICCHermesChat{})

	_, err := svc.PublicConfig(context.Background(), "pub")

	require.ErrorIs(t, err, ErrAICCOffline)
}

type fakeAICCPublicStore struct {
	org                sqlc.Organization
	agent              sqlc.AiccAgent
	session            sqlc.AiccSession
	requiredLeadFields []sqlc.AiccLeadField
	createdSession     sqlc.CreateAICCSessionParams
	createdMessages    []sqlc.CreateAICCMessageParams
}

func (f *fakeAICCPublicStore) GetOrganization(_ context.Context, id string) (sqlc.Organization, error) {
	if f.org.ID != id {
		return sqlc.Organization{}, sql.ErrNoRows
	}
	return f.org, nil
}

func (f *fakeAICCPublicStore) GetAICCAgent(_ context.Context, id string) (sqlc.AiccAgent, error) {
	if f.agent.ID != id {
		return sqlc.AiccAgent{}, sql.ErrNoRows
	}
	return f.agent, nil
}

func (f *fakeAICCPublicStore) GetAICCAgentByPublicToken(_ context.Context, token string) (sqlc.AiccAgent, error) {
	if f.agent.PublicToken != token {
		return sqlc.AiccAgent{}, sql.ErrNoRows
	}
	return f.agent, nil
}

func (f *fakeAICCPublicStore) GetAICCSessionByToken(_ context.Context, token string) (sqlc.AiccSession, error) {
	if f.session.SessionToken != token {
		return sqlc.AiccSession{}, sql.ErrNoRows
	}
	return f.session, nil
}

func (f *fakeAICCPublicStore) CreateAICCSession(_ context.Context, arg sqlc.CreateAICCSessionParams) error {
	f.createdSession = arg
	f.session = sqlc.AiccSession{
		ID:                 arg.ID,
		AgentID:            arg.AgentID,
		OrgID:              arg.OrgID,
		SessionToken:       arg.SessionToken,
		Channel:            arg.Channel,
		SourceUrl:          arg.SourceUrl,
		Referrer:           arg.Referrer,
		PrivacyNoticeShown: arg.PrivacyNoticeShown,
		ExpiresAt:          arg.ExpiresAt,
	}
	return nil
}

func (f *fakeAICCPublicStore) MarkAICCSessionConsented(_ context.Context, sessionToken string) (int64, error) {
	if f.session.SessionToken != sessionToken || !f.session.ExpiresAt.After(aiccPublicTestNow) {
		return 0, nil
	}
	f.session.PrivacyConsentedAt = null.TimeFrom(time.Date(2026, 7, 9, 10, 0, 0, 0, time.UTC))
	return 1, nil
}

func (f *fakeAICCPublicStore) CreateAICCMessage(_ context.Context, arg sqlc.CreateAICCMessageParams) error {
	f.createdMessages = append(f.createdMessages, arg)
	return nil
}

func (f *fakeAICCPublicStore) ListRequiredAICCLeadFieldsMissing(_ context.Context, _ string) ([]sqlc.AiccLeadField, error) {
	return f.requiredLeadFields, nil
}

type fakeAICCHermesChat struct {
	reply string
	appID string
	text  string
}

func (f *fakeAICCHermesChat) ChatAICC(_ context.Context, appID, _ string, text string) (string, error) {
	f.appID = appID
	f.text = text
	return f.reply, nil
}
