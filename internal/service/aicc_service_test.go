package service

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	null "github.com/guregu/null/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/store/sqlc"
)

// fakeAICCStore 是 AICC service 单测使用的最小 store，记录创建入参与返回组织配置。
type fakeAICCStore struct {
	org          sqlc.Organization
	count        int64
	agents       map[string]sqlc.AiccAgent
	createArg    sqlc.CreateAICCAgentParams
	updateArg    sqlc.UpdateAICCAgentProfileParams
	statusArg    sqlc.SetAICCAgentStatusParams
	deletedID    string
	createErr    error
	getErr       error
	listErr      error
	updateErr    error
	statusErr    error
	deleteErr    error
	organization error
}

// GetOrganization 返回测试预置的企业开通配置。
func (f *fakeAICCStore) GetOrganization(_ context.Context, id string) (sqlc.Organization, error) {
	if f.organization != nil {
		return sqlc.Organization{}, f.organization
	}
	if f.org.ID != id {
		return sqlc.Organization{}, sql.ErrNoRows
	}
	return f.org, nil
}

// CountAICCAgentsByOrg 返回测试预置的智能体数量。
func (f *fakeAICCStore) CountAICCAgentsByOrg(_ context.Context, _ string) (int64, error) {
	return f.count, nil
}

// CreateAICCAgent 记录创建参数，并把行写入内存表供后续读取。
func (f *fakeAICCStore) CreateAICCAgent(_ context.Context, arg sqlc.CreateAICCAgentParams) error {
	f.createArg = arg
	if f.createErr != nil {
		return f.createErr
	}
	f.ensureAgents()
	f.agents[arg.ID] = sqlc.AiccAgent{
		ID:                 arg.ID,
		OrgID:              arg.OrgID,
		AppID:              arg.AppID,
		Name:               arg.Name,
		Status:             arg.Status,
		Scenario:           arg.Scenario,
		Greeting:           arg.Greeting,
		AnswerBoundary:     arg.AnswerBoundary,
		PrivacyMode:        arg.PrivacyMode,
		PrivacyText:        arg.PrivacyText,
		RetentionDays:      arg.RetentionDays,
		ThemeJson:          arg.ThemeJson,
		AllowedDomainsJson: arg.AllowedDomainsJson,
		PublicToken:        arg.PublicToken,
		WidgetToken:        arg.WidgetToken,
		CreatedAt:          time.Date(2026, 7, 9, 10, 0, 0, 0, time.UTC),
		UpdatedAt:          time.Date(2026, 7, 9, 10, 0, 0, 0, time.UTC),
	}
	return nil
}

// GetAICCAgent 从内存表读取智能体，用于创建后回读和权限校验。
func (f *fakeAICCStore) GetAICCAgent(_ context.Context, id string) (sqlc.AiccAgent, error) {
	if f.getErr != nil {
		return sqlc.AiccAgent{}, f.getErr
	}
	f.ensureAgents()
	row, ok := f.agents[id]
	if !ok {
		return sqlc.AiccAgent{}, sql.ErrNoRows
	}
	return row, nil
}

// ListAICCAgentsByOrg 返回同企业下的未删除智能体。
func (f *fakeAICCStore) ListAICCAgentsByOrg(_ context.Context, arg sqlc.ListAICCAgentsByOrgParams) ([]sqlc.AiccAgent, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	f.ensureAgents()
	rows := make([]sqlc.AiccAgent, 0, len(f.agents))
	for _, row := range f.agents {
		if row.OrgID == arg.OrgID && !row.DeletedAt.Valid {
			rows = append(rows, row)
		}
	}
	return rows, nil
}

// UpdateAICCAgentProfile 记录更新参数，并同步修改内存行。
func (f *fakeAICCStore) UpdateAICCAgentProfile(_ context.Context, arg sqlc.UpdateAICCAgentProfileParams) error {
	f.updateArg = arg
	if f.updateErr != nil {
		return f.updateErr
	}
	f.ensureAgents()
	row, ok := f.agents[arg.ID]
	if !ok {
		return sql.ErrNoRows
	}
	row.Name = arg.Name
	row.Scenario = arg.Scenario
	row.Greeting = arg.Greeting
	row.AnswerBoundary = arg.AnswerBoundary
	row.PrivacyMode = arg.PrivacyMode
	row.PrivacyText = arg.PrivacyText
	row.RetentionDays = arg.RetentionDays
	row.ThemeJson = arg.ThemeJson
	row.AllowedDomainsJson = arg.AllowedDomainsJson
	f.agents[arg.ID] = row
	return nil
}

// SetAICCAgentStatus 记录状态切换参数，并同步修改内存行。
func (f *fakeAICCStore) SetAICCAgentStatus(_ context.Context, arg sqlc.SetAICCAgentStatusParams) error {
	f.statusArg = arg
	if f.statusErr != nil {
		return f.statusErr
	}
	f.ensureAgents()
	row, ok := f.agents[arg.ID]
	if !ok {
		return sql.ErrNoRows
	}
	row.Status = arg.Status
	f.agents[arg.ID] = row
	return nil
}

// SoftDeleteAICCAgent 记录删除目标，并将内存行标记为删除。
func (f *fakeAICCStore) SoftDeleteAICCAgent(_ context.Context, id string) error {
	f.deletedID = id
	if f.deleteErr != nil {
		return f.deleteErr
	}
	f.ensureAgents()
	row, ok := f.agents[id]
	if !ok {
		return sql.ErrNoRows
	}
	row.Status = domain.AICCAgentStatusDeleted
	row.DeletedAt = null.TimeFrom(time.Date(2026, 7, 9, 11, 0, 0, 0, time.UTC))
	f.agents[id] = row
	return nil
}

func (f *fakeAICCStore) ensureAgents() {
	if f.agents == nil {
		f.agents = map[string]sqlc.AiccAgent{}
	}
}

// fakeAICCHiddenAppCreator 记录隐藏 app 创建请求，并返回预设 app ID。
type fakeAICCHiddenAppCreator struct {
	appID       string
	lastInput   AICCHiddenAppInput
	rollbackID  string
	err         error
	rollbackErr error
}

// CreateHiddenAICCApp 模拟生产隐藏 app 创建链路。
func (f *fakeAICCHiddenAppCreator) CreateHiddenAICCApp(_ context.Context, _ auth.Principal, input AICCHiddenAppInput) (string, error) {
	f.lastInput = input
	if f.err != nil {
		return "", f.err
	}
	if f.appID != "" {
		return f.appID, nil
	}
	return input.AppID, nil
}

// SoftDeleteHiddenAICCApp 记录回滚目标，模拟生产侧软删除隐藏 app。
func (f *fakeAICCHiddenAppCreator) SoftDeleteHiddenAICCApp(_ context.Context, _ auth.Principal, appID string) error {
	f.rollbackID = appID
	return f.rollbackErr
}

func aiccOrgAdmin() auth.Principal {
	return auth.Principal{Role: domain.UserRoleOrgAdmin, OrgID: "org-1", UserID: "admin-1"}
}

func seededAICCStore() *fakeAICCStore {
	return &fakeAICCStore{
		org: sqlc.Organization{ID: "org-1", AiccEnabled: true},
		agents: map[string]sqlc.AiccAgent{
			"agent-1": {
				ID:            "agent-1",
				OrgID:         "org-1",
				AppID:         "app-hidden-1",
				Name:          "官网售前",
				Status:        domain.AICCAgentStatusDraft,
				PrivacyMode:   domain.AICCPrivacyModeNotice,
				RetentionDays: 180,
				CreatedAt:     time.Date(2026, 7, 9, 10, 0, 0, 0, time.UTC),
				UpdatedAt:     time.Date(2026, 7, 9, 10, 0, 0, 0, time.UTC),
			},
		},
	}
}

// TestAICCServiceCreateAgentCreatesHiddenApp 覆盖正常路径：企业管理员创建智能体时自动创建隐藏 app 并绑定。
func TestAICCServiceCreateAgentCreatesHiddenApp(t *testing.T) {
	store := &fakeAICCStore{
		org:   sqlc.Organization{ID: "org-1", AiccEnabled: true},
		count: 0,
	}
	apps := &fakeAICCHiddenAppCreator{appID: "app-hidden-1"}
	svc := NewAICCService(store, apps)

	result, err := svc.CreateAgent(context.Background(), aiccOrgAdmin(), AICCAgentInput{
		Name:          " 官网售前 ",
		Greeting:      "您好，请问想了解什么？",
		PrivacyMode:   domain.AICCPrivacyModeNotice,
		RetentionDays: 180,
	})

	require.NoError(t, err)
	assert.Equal(t, "org-1", apps.lastInput.OrgID)
	assert.Equal(t, "admin-1", apps.lastInput.UserID)
	assert.Equal(t, "官网售前", apps.lastInput.Name)
	assert.Equal(t, "app-hidden-1", store.createArg.AppID)
	assert.Equal(t, "官网售前", result.Name)
	assert.NotEmpty(t, result.PublicToken)
	assert.NotEmpty(t, result.WidgetToken)
}

// TestAICCServiceCreateAgentValidation 覆盖创建智能体的权限、开通状态、数量上限和参数边界。
func TestAICCServiceCreateAgentValidation(t *testing.T) {
	cases := []struct {
		name      string         // 子场景说明
		principal auth.Principal // 调用主体
		org       sqlc.Organization
		count     int64
		input     AICCAgentInput
		wantErr   error
	}{
		{name: "空名称返回参数错误", principal: aiccOrgAdmin(), org: sqlc.Organization{ID: "org-1", AiccEnabled: true}, input: AICCAgentInput{Name: "   "}, wantErr: ErrInvalidArgument},                                                              // 场景：名称 trim 后为空。
		{name: "保留期小于下限返回参数错误", principal: aiccOrgAdmin(), org: sqlc.Organization{ID: "org-1", AiccEnabled: true}, input: AICCAgentInput{Name: "售前", RetentionDays: -1}, wantErr: ErrInvalidArgument},                                        // 场景：保留期不能小于 1 天。
		{name: "保留期超过上限返回参数错误", principal: aiccOrgAdmin(), org: sqlc.Organization{ID: "org-1", AiccEnabled: true}, input: AICCAgentInput{Name: "售前", RetentionDays: 3651}, wantErr: ErrInvalidArgument},                                      // 场景：保留期不能超过迁移约束上限。
		{name: "未开通企业返回无权限", principal: aiccOrgAdmin(), org: sqlc.Organization{ID: "org-1", AiccEnabled: false}, input: AICCAgentInput{Name: "售前"}, wantErr: ErrForbidden},                                                                   // 场景：平台未给企业开通 AICC。
		{name: "达到企业上限返回配额错误", principal: aiccOrgAdmin(), org: sqlc.Organization{ID: "org-1", AiccEnabled: true, AiccAgentLimit: null.IntFrom(1)}, count: 1, input: AICCAgentInput{Name: "售前"}, wantErr: ErrQuotaExceeded},                   // 场景：当前数量已达到 aicc_agent_limit。
		{name: "跨组织管理员返回无权限", principal: auth.Principal{Role: domain.UserRoleOrgAdmin, OrgID: "org-2", UserID: "admin-2"}, org: sqlc.Organization{ID: "org-1", AiccEnabled: true}, input: AICCAgentInput{Name: "售前"}, wantErr: ErrForbidden}, // 场景：企业管理员只能管理本企业。
		{name: "普通成员返回无权限", principal: auth.Principal{Role: domain.UserRoleOrgMember, OrgID: "org-1", UserID: "member-1"}, org: sqlc.Organization{ID: "org-1", AiccEnabled: true}, input: AICCAgentInput{Name: "售前"}, wantErr: ErrForbidden}, // 场景：普通成员无 AICC 管理入口。
		{name: "平台管理员管理返回无权限", principal: auth.Principal{Role: domain.UserRolePlatformAdmin, UserID: "platform-1"}, org: sqlc.Organization{ID: "org-1", AiccEnabled: true}, input: AICCAgentInput{Name: "售前"}, wantErr: ErrForbidden},        // 场景：平台管理员仅只读不能管理智能体。
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeAICCStore{org: tc.org, count: tc.count}
			svc := NewAICCService(store, &fakeAICCHiddenAppCreator{})

			_, err := svc.CreateAgent(context.Background(), tc.principal, tc.input)

			require.ErrorIs(t, err, tc.wantErr)
		})
	}
}

// TestAICCServiceReadPermission 覆盖读权限：平台管理员和本企业管理员可读，普通成员不可读。
func TestAICCServiceReadPermission(t *testing.T) {
	cases := []struct {
		name      string         // 子场景说明
		principal auth.Principal // 调用主体
		wantErr   error
	}{
		{name: "平台管理员可只读查看", principal: auth.Principal{Role: domain.UserRolePlatformAdmin, UserID: "platform-1"}},                                // 场景：平台排障读取任意企业 AICC。
		{name: "本企业管理员可查看", principal: aiccOrgAdmin()},                                                                                           // 场景：企业管理员读取本企业 AICC。
		{name: "普通成员不可查看", principal: auth.Principal{Role: domain.UserRoleOrgMember, OrgID: "org-1", UserID: "member-1"}, wantErr: ErrForbidden}, // 场景：普通成员无 AICC 入口。
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := NewAICCService(seededAICCStore(), &fakeAICCHiddenAppCreator{})

			result, err := svc.GetAgent(context.Background(), tc.principal, "agent-1")

			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, "agent-1", result.ID)
		})
	}
}

// TestAICCServiceListAgentsUsesViewPermission 覆盖列表读取权限和分页归一化。
func TestAICCServiceListAgentsUsesViewPermission(t *testing.T) {
	svc := NewAICCService(seededAICCStore(), &fakeAICCHiddenAppCreator{})

	results, err := svc.ListAgents(context.Background(), auth.Principal{Role: domain.UserRolePlatformAdmin}, "org-1", 0, -1)

	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "agent-1", results[0].ID)
}

// TestAICCServiceUpdateAgentRequiresManagePermission 覆盖更新路径：本企业管理员可更新，平台管理员只读不可写。
func TestAICCServiceUpdateAgentRequiresManagePermission(t *testing.T) {
	t.Run("本企业管理员可更新资料", func(t *testing.T) {
		store := seededAICCStore()
		svc := NewAICCService(store, &fakeAICCHiddenAppCreator{})

		result, err := svc.UpdateAgent(context.Background(), aiccOrgAdmin(), "agent-1", AICCAgentInput{Name: "官网售后", RetentionDays: 90, PrivacyMode: domain.AICCPrivacyModeConsentRequired})

		require.NoError(t, err)
		assert.Equal(t, "官网售后", result.Name)
		assert.Equal(t, domain.AICCPrivacyModeConsentRequired, store.updateArg.PrivacyMode)
	})

	t.Run("平台管理员不可更新资料", func(t *testing.T) {
		svc := NewAICCService(seededAICCStore(), &fakeAICCHiddenAppCreator{})

		_, err := svc.UpdateAgent(context.Background(), auth.Principal{Role: domain.UserRolePlatformAdmin}, "agent-1", AICCAgentInput{Name: "官网售后"})

		require.ErrorIs(t, err, ErrForbidden)
	})
}

// TestAICCServiceStatusAndDelete 覆盖启动、停止和删除的状态写入。
func TestAICCServiceStatusAndDelete(t *testing.T) {
	cases := []struct {
		name       string // 子场景说明
		action     string
		wantStatus string
	}{
		{name: "start 写入 active 状态", action: "start", wantStatus: domain.AICCAgentStatusActive}, // 场景：企业管理员启用智能体。
		{name: "stop 写入 paused 状态", action: "stop", wantStatus: domain.AICCAgentStatusPaused},   // 场景：企业管理员停用智能体。
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := seededAICCStore()
			svc := NewAICCService(store, &fakeAICCHiddenAppCreator{})

			result, err := svc.SetAgentStatus(context.Background(), aiccOrgAdmin(), "agent-1", tc.action)

			require.NoError(t, err)
			assert.Equal(t, tc.wantStatus, result.Status)
			assert.Equal(t, tc.wantStatus, store.statusArg.Status)
		})
	}

	t.Run("delete 软删除智能体", func(t *testing.T) {
		store := seededAICCStore()
		svc := NewAICCService(store, &fakeAICCHiddenAppCreator{})

		err := svc.DeleteAgent(context.Background(), aiccOrgAdmin(), "agent-1")

		require.NoError(t, err)
		assert.Equal(t, "agent-1", store.deletedID)
	})
}

// TestAICCServiceMapsMissingAgent 覆盖底层 sql.ErrNoRows 被转换为 service.ErrNotFound。
func TestAICCServiceMapsMissingAgent(t *testing.T) {
	store := seededAICCStore()
	store.getErr = sql.ErrNoRows
	svc := NewAICCService(store, &fakeAICCHiddenAppCreator{})

	_, err := svc.GetAgent(context.Background(), aiccOrgAdmin(), "missing")

	require.ErrorIs(t, err, ErrNotFound)
}

// TestAICCServiceWrapsHiddenAppCreatorError 覆盖隐藏 app 创建失败时中止智能体创建。
func TestAICCServiceWrapsHiddenAppCreatorError(t *testing.T) {
	store := &fakeAICCStore{org: sqlc.Organization{ID: "org-1", AiccEnabled: true}}
	svc := NewAICCService(store, &fakeAICCHiddenAppCreator{err: errors.New("boom")})

	_, err := svc.CreateAgent(context.Background(), aiccOrgAdmin(), AICCAgentInput{Name: "售前"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "创建 AICC 隐藏 app 失败")
}

// TestAICCServiceRollsBackHiddenAppWhenAgentCreateFails 覆盖异常路径：隐藏 app 已创建但智能体写入失败时，
// service 应软删除隐藏 app，避免留下没有 aicc_agents 绑定的后台实例。
func TestAICCServiceRollsBackHiddenAppWhenAgentCreateFails(t *testing.T) {
	store := &fakeAICCStore{
		org:       sqlc.Organization{ID: "org-1", AiccEnabled: true},
		createErr: errors.New("insert failed"),
	}
	apps := &fakeAICCHiddenAppCreator{appID: "app-hidden-rollback"}
	svc := NewAICCService(store, apps)

	_, err := svc.CreateAgent(context.Background(), aiccOrgAdmin(), AICCAgentInput{Name: "售前"})

	require.Error(t, err)
	assert.Equal(t, "app-hidden-rollback", apps.rollbackID)
}
