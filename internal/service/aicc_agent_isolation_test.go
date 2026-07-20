package service

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/guregu/null/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"oc-manager/internal/domain"
	"oc-manager/internal/store/sqlc"
)

// fakeAICCAgentTxRunner 用内存快照模拟 AICC 管理事务，回调失败时恢复资料、知识、任务与审计。
type fakeAICCAgentTxRunner struct {
	store  *fakeAICCStore
	before func()
}

// WithAICCTx 仅在回调成功时保留 fake store 变更，覆盖整组配置不会部分提交的业务约束。
func (r *fakeAICCAgentTxRunner) WithAICCTx(ctx context.Context, fn func(AICCStore) error) error {
	if r.before != nil {
		r.before()
	}
	agents := cloneAICCAgents(r.store.agents)
	knowledge := cloneAICCKnowledge(r.store.knowledge)
	addKnowledge := append([]sqlc.AddAICCAgentKnowledgeParams(nil), r.store.addKnowledge...)
	jobs := append([]sqlc.CreateJobParams(nil), r.store.jobs...)
	audits := append([]sqlc.CreateAuditLogParams(nil), r.store.audits...)
	if err := fn(r.store); err != nil {
		r.store.agents = agents
		r.store.knowledge = knowledge
		r.store.addKnowledge = addKnowledge
		r.store.jobs = jobs
		r.store.audits = audits
		return err
	}
	return nil
}

// TestCreateAgentRevalidatesIndustryKnowledgeInsideTransaction 验证进入创建事务后授权已撤销时回滚 profile 并补偿隐藏 app。
func TestCreateAgentRevalidatesIndustryKnowledgeInsideTransaction(t *testing.T) {
	store := seededAICCStore()
	store.agents = map[string]sqlc.AiccAgent{}
	store.knowledge = map[string][]sqlc.AiccAgentKnowledge{}
	apps := &fakeAICCHiddenAppCreator{appID: "app-hidden-1"}
	svc := NewAICCService(store, apps)
	svc.SetTxRunner(&fakeAICCAgentTxRunner{store: store, before: func() {
		store.organizationIndustryBases["org-1"] = nil
	}})

	_, err := svc.CreateAgent(context.Background(), aiccOrgAdmin(), AICCAgentInput{Name: "官网客服", IndustryKnowledgeBaseIDs: []string{"industry-1"}})

	require.ErrorIs(t, err, ErrInvalidArgument)
	assert.Equal(t, 1, store.lockedIndustryListCalls)
	assert.Empty(t, store.agents)
	assert.Empty(t, store.knowledge)
	assert.Empty(t, store.jobs)
	assert.Empty(t, store.audits)
	assert.Equal(t, "app-hidden-1", apps.rollbackID)
}

// TestUpdateAgentRevalidatesIndustryKnowledgeInsideTransaction 验证编辑事务开始后授权失效时不提交资料、关联或重启任务。
func TestUpdateAgentRevalidatesIndustryKnowledgeInsideTransaction(t *testing.T) {
	store := seededAICCStore()
	row := store.agents["agent-1"]
	row.Status = domain.AICCAgentStatusActive
	store.agents["agent-1"] = row
	svc := NewAICCService(store, &fakeAICCHiddenAppCreator{})
	svc.SetTxRunner(&fakeAICCAgentTxRunner{store: store, before: func() {
		store.organizationIndustryBases["org-1"] = nil
	}})

	_, err := svc.UpdateAgent(context.Background(), aiccOrgAdmin(), "agent-1", AICCAgentInput{Name: "不应提交", Persona: "新的人设", IndustryKnowledgeBaseIDs: []string{"industry-1"}})

	require.ErrorIs(t, err, ErrInvalidArgument)
	assert.Equal(t, 1, store.lockedIndustryListCalls)
	assert.Equal(t, "官网售前", store.agents["agent-1"].Name)
	assert.False(t, store.agents["agent-1"].Persona.Valid)
	assert.Equal(t, null.StringFrom("industry-1"), store.knowledge["agent-1"][1].IndustryKnowledgeBaseID)
	assert.Empty(t, store.jobs)
}

// TestDeleteAgentDeletesVersionUnboundHiddenApp 验证删除客服可通过基础 app 查询清理 NULL version_id 隐藏应用，避免孤儿运行时。
func TestDeleteAgentDeletesVersionUnboundHiddenApp(t *testing.T) {
	aiccStore := seededAICCStore()
	appService, appStore := newAppServiceWithStore(t)
	app := appStore.mustSeedApp(t)
	app.ID = "app-hidden-1"
	app.OrgID = "org-1"
	app.AppType = string(domain.AppTypeAICC)
	app.VersionID = null.String{}
	appStore.app = app
	appStore.getAppWithVersionErr = sql.ErrNoRows
	svc := NewAICCService(aiccStore, appService)

	err := svc.DeleteAgent(context.Background(), aiccOrgAdmin(), "agent-1")

	require.NoError(t, err)
	assert.True(t, appStore.app.DeletedAt.Valid)
	assert.Equal(t, "agent-1", aiccStore.deletedID)
	require.Len(t, appStore.jobs, 1)
	assert.Equal(t, domain.JobTypeAppDelete, appStore.jobs[0].Type)
}

// cloneAICCAgents 复制智能体行，避免失败事务修改原始 map 中的可观察状态。
func cloneAICCAgents(source map[string]sqlc.AiccAgent) map[string]sqlc.AiccAgent {
	cloned := make(map[string]sqlc.AiccAgent, len(source))
	for id, row := range source {
		cloned[id] = row
	}
	return cloned
}

// cloneAICCKnowledge 复制每个智能体的知识关联切片，避免删除或追加穿透事务快照。
func cloneAICCKnowledge(source map[string][]sqlc.AiccAgentKnowledge) map[string][]sqlc.AiccAgentKnowledge {
	cloned := make(map[string][]sqlc.AiccAgentKnowledge, len(source))
	for agentID, rows := range source {
		cloned[agentID] = append([]sqlc.AiccAgentKnowledge(nil), rows...)
	}
	return cloned
}

// TestCreateAgentUsesAICCConfigWithoutAssistantVersion 验证创建只依赖企业独立 AICC 配置，助手版本 allowlist 为空不再阻断。
func TestCreateAgentUsesAICCConfigWithoutAssistantVersion(t *testing.T) {
	store := &fakeAICCStore{
		org:    sqlc.Organization{ID: "org-1", AssistantVersionIds: []byte(`[]`)},
		config: sqlc.OrganizationAiccConfig{OrgID: "org-1", Enabled: true, Model: null.StringFrom("gpt-5-mini"), Revision: 1},
	}
	apps := &fakeAICCHiddenAppCreator{appID: "app-hidden-1"}
	svc := NewAICCService(store, apps)
	svc.SetTxRunner(&fakeAICCAgentTxRunner{store: store})

	result, err := svc.CreateAgent(context.Background(), aiccOrgAdmin(), AICCAgentInput{Name: "官网客服"})

	require.NoError(t, err)
	assert.Equal(t, "app-hidden-1", result.AppID)
	assert.JSONEq(t, `[]`, string(store.org.AssistantVersionIds))
}

// TestCreateAgentValidatesPersonaRuneLimit 验证人设按 Unicode 字符而非字节计数，8000 个中文字符可保存，超出一个即拒绝。
func TestCreateAgentValidatesPersonaRuneLimit(t *testing.T) {
	cases := []struct {
		name    string
		persona string
		wantErr bool
	}{
		{name: "八千个中文字符允许创建", persona: strings.Repeat("客", 8000)},                  // 场景：多字节字符位于允许上界。
		{name: "超过八千个中文字符拒绝创建", persona: strings.Repeat("客", 8001), wantErr: true}, // 场景：超过一个 Unicode 字符即拒绝。
	}
	for _, tc := range cases {
		// 每个子测试覆盖一个人设 Unicode 字符计数边界。
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeAICCStore{org: sqlc.Organization{ID: "org-1"}}
			apps := &fakeAICCHiddenAppCreator{}
			svc := NewAICCService(store, apps)

			_, err := svc.CreateAgent(context.Background(), aiccOrgAdmin(), AICCAgentInput{Name: "官网客服", Persona: tc.persona})

			if tc.wantErr {
				require.ErrorIs(t, err, ErrInvalidArgument)
				assert.Empty(t, apps.lastInput.AppID)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.persona, store.createArg.Persona.String)
		})
	}
}

// TestCreateAgentPersistsPersonaAndAuthorizedIndustryKnowledge 验证创建事务保存 trim 后人设、默认企业知识和提交的已授权行业库。
func TestCreateAgentPersistsPersonaAndAuthorizedIndustryKnowledge(t *testing.T) {
	store := seededAICCStore()
	store.agents = map[string]sqlc.AiccAgent{}
	store.knowledge = map[string][]sqlc.AiccAgentKnowledge{}
	apps := &fakeAICCHiddenAppCreator{appID: "app-hidden-1"}
	svc := NewAICCService(store, apps)
	svc.SetTxRunner(&fakeAICCAgentTxRunner{store: store})

	result, err := svc.CreateAgent(context.Background(), aiccOrgAdmin(), AICCAgentInput{
		Name:                     "官网客服",
		Persona:                  "  专业、克制的售前顾问  ",
		IndustryKnowledgeBaseIDs: []string{" industry-2 ", "industry-2"},
	})

	require.NoError(t, err)
	assert.Equal(t, "专业、克制的售前顾问", result.Persona)
	assert.Equal(t, []string{"industry-2"}, result.IndustryKnowledgeBaseIDs)
	assert.Equal(t, null.StringFrom("专业、克制的售前顾问"), store.createArg.Persona)
	rows := store.knowledge[result.ID]
	require.Len(t, rows, 2)
	assert.Equal(t, domain.AICCKnowledgeScopeTypeOrg, rows[0].ScopeType)
	assert.Equal(t, null.StringFrom("industry-2"), rows[1].IndustryKnowledgeBaseID)
}

// TestCreateAgentRejectsDisabledAICCConfig 验证独立配置关闭或缺少模型时，在创建隐藏 app 与 profile 前拒绝请求。
func TestCreateAgentRejectsDisabledAICCConfig(t *testing.T) {
	cases := []struct {
		name   string
		config sqlc.OrganizationAiccConfig
	}{
		{name: "独立开关关闭", config: sqlc.OrganizationAiccConfig{OrgID: "org-1", Enabled: false, Model: null.StringFrom("gpt-5-mini"), Revision: 1}}, // 场景：企业未开通 AICC。
		{name: "独立模型为空", config: sqlc.OrganizationAiccConfig{OrgID: "org-1", Enabled: true, Revision: 1}},                                        // 场景：开关打开但没有可用模型。
	}
	for _, tc := range cases {
		// 每个子测试验证一种不可创建的独立配置状态，且不得产生外部或数据库写入。
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeAICCStore{org: sqlc.Organization{ID: "org-1"}, config: tc.config}
			apps := &fakeAICCHiddenAppCreator{}
			svc := NewAICCService(store, apps)

			_, err := svc.CreateAgent(context.Background(), aiccOrgAdmin(), AICCAgentInput{Name: "官网客服"})

			require.ErrorIs(t, err, ErrForbidden)
			assert.Empty(t, apps.lastInput.AppID)
			assert.Empty(t, store.agents)
		})
	}
}

// TestCreateAgentRejectsUnauthorizedIndustryKnowledge 验证未授权行业库在创建隐藏 app 和 profile 前被拒绝。
func TestCreateAgentRejectsUnauthorizedIndustryKnowledge(t *testing.T) {
	store := seededAICCStore()
	store.agents = map[string]sqlc.AiccAgent{}
	apps := &fakeAICCHiddenAppCreator{}
	svc := NewAICCService(store, apps)

	_, err := svc.CreateAgent(context.Background(), aiccOrgAdmin(), AICCAgentInput{Name: "官网客服", IndustryKnowledgeBaseIDs: []string{"industry-3"}})

	require.ErrorIs(t, err, ErrInvalidArgument)
	assert.Empty(t, apps.lastInput.AppID)
	assert.Empty(t, apps.rollbackID)
	assert.Empty(t, store.agents)
}

// TestUpdateAgentUsesLockedTransactionSnapshot 验证资料更新在事务内锁定并重读智能体与知识，避免并发状态切换或知识保存被事务外旧快照覆盖。
func TestUpdateAgentUsesLockedTransactionSnapshot(t *testing.T) {
	store := seededAICCStore()
	row := store.agents["agent-1"]
	row.Status = domain.AICCAgentStatusDraft
	store.agents["agent-1"] = row
	svc := NewAICCService(store, &fakeAICCHiddenAppCreator{})
	svc.SetTxRunner(&fakeAICCAgentTxRunner{store: store, before: func() {
		current := store.agents["agent-1"]
		current.Status = domain.AICCAgentStatusActive
		current.Persona = null.StringFrom("事务内旧人设")
		store.agents["agent-1"] = current
		store.knowledge["agent-1"] = []sqlc.AiccAgentKnowledge{{AgentID: "agent-1", ScopeType: domain.AICCKnowledgeScopeTypeOrg, OrgID: null.StringFrom("org-1")}}
	}})

	_, err := svc.UpdateAgent(context.Background(), aiccOrgAdmin(), "agent-1", AICCAgentInput{Name: row.Name, Persona: "新的人设"})

	require.NoError(t, err)
	assert.Equal(t, 1, store.lockedAgentCalls)
	require.Len(t, store.jobs, 1)
	assert.Equal(t, domain.JobTypeAppRestartContainer, store.jobs[0].Type)
	assert.True(t, toAICCKnowledgeResult(store.agents["agent-1"], store.knowledge["agent-1"]).UseOrgKnowledge)
}

// TestReplaceAgentKnowledgeLocksAgentInsideTransaction 验证知识整组替换先锁定智能体行，与资料更新和状态切换按同一行串行。
func TestReplaceAgentKnowledgeLocksAgentInsideTransaction(t *testing.T) {
	store := seededAICCStore()
	svc := NewAICCService(store, &fakeAICCHiddenAppCreator{})
	svc.SetTxRunner(&fakeAICCAgentTxRunner{store: store})

	_, err := svc.ReplaceAgentKnowledge(context.Background(), aiccOrgAdmin(), "agent-1", AICCKnowledgeInput{UseOrgKnowledge: true})

	require.NoError(t, err)
	assert.Equal(t, 1, store.lockedAgentCalls)
}

// TestUpdateAgentPersistsPersonaAndIndustryKnowledgeAtomically 验证编辑资料和知识同事务提交，任一写入失败均不保留部分更新。
func TestUpdateAgentPersistsPersonaAndIndustryKnowledgeAtomically(t *testing.T) {
	t.Run("成功时整组提交", func(t *testing.T) {
		store := seededAICCStore()
		svc := NewAICCService(store, &fakeAICCHiddenAppCreator{})
		svc.SetTxRunner(&fakeAICCAgentTxRunner{store: store})

		result, err := svc.UpdateAgent(context.Background(), aiccOrgAdmin(), "agent-1", AICCAgentInput{Name: "官网售后", Persona: "售后专家", IndustryKnowledgeBaseIDs: []string{"industry-2"}})

		require.NoError(t, err)
		assert.Equal(t, "售后专家", result.Persona)
		assert.Equal(t, []string{"industry-2"}, result.IndustryKnowledgeBaseIDs)
	})

	t.Run("知识写入失败时资料回滚", func(t *testing.T) {
		store := seededAICCStore()
		store.addKnowledgeErr = errors.New("knowledge insert failed")
		svc := NewAICCService(store, &fakeAICCHiddenAppCreator{})
		svc.SetTxRunner(&fakeAICCAgentTxRunner{store: store})

		_, err := svc.UpdateAgent(context.Background(), aiccOrgAdmin(), "agent-1", AICCAgentInput{Name: "不应提交", Persona: "不应提交", IndustryKnowledgeBaseIDs: []string{"industry-2"}})

		require.Error(t, err)
		assert.Equal(t, "官网售前", store.agents["agent-1"].Name)
		assert.False(t, store.agents["agent-1"].Persona.Valid)
		assert.Equal(t, null.StringFrom("industry-1"), store.knowledge["agent-1"][1].IndustryKnowledgeBaseID)
	})
}

// TestUpdateAgentRestartsRunningAgentWhenPromptChanges 验证运行中客服的人设、场景或回答边界变化会原子创建一次重启任务，并在提交后通知 worker。
func TestUpdateAgentRestartsRunningAgentWhenPromptChanges(t *testing.T) {
	store := seededAICCStore()
	row := store.agents["agent-1"]
	row.Status = domain.AICCAgentStatusActive
	store.agents["agent-1"] = row
	notifier := &fakeNotifier{}
	svc := NewAICCService(store, &fakeAICCHiddenAppCreator{})
	svc.SetTxRunner(&fakeAICCAgentTxRunner{store: store})
	svc.SetJobNotifier(notifier)

	_, err := svc.UpdateAgent(context.Background(), aiccOrgAdmin(), "agent-1", AICCAgentInput{Name: row.Name, Persona: "新的人设", Scenario: "新场景", AnswerBoundary: "新边界"})

	require.NoError(t, err)
	require.Len(t, store.jobs, 1)
	assert.Equal(t, domain.JobTypeAppRestartContainer, store.jobs[0].Type)
	assert.JSONEq(t, `{"app_id":"app-hidden-1"}`, string(store.jobs[0].PayloadJson))
	assert.Equal(t, store.jobs[0].ID, notifier.lastJobID)
}

// TestUpdateAgentDoesNotRestartWhenOnlyGreetingChanges 验证仅名称、欢迎语、隐私或知识范围变化不会重启运行中客服。
func TestUpdateAgentDoesNotRestartWhenOnlyGreetingChanges(t *testing.T) {
	store := seededAICCStore()
	row := store.agents["agent-1"]
	row.Status = domain.AICCAgentStatusActive
	store.agents["agent-1"] = row
	svc := NewAICCService(store, &fakeAICCHiddenAppCreator{})
	svc.SetTxRunner(&fakeAICCAgentTxRunner{store: store})

	_, err := svc.UpdateAgent(context.Background(), aiccOrgAdmin(), "agent-1", AICCAgentInput{Name: "新名称", Greeting: "新的欢迎语", PrivacyText: "新的隐私说明", IndustryKnowledgeBaseIDs: []string{"industry-2"}})

	require.NoError(t, err)
	assert.Empty(t, store.jobs)
}
