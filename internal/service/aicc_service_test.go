package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
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
	org                  sqlc.Organization
	count                int64
	agents               map[string]sqlc.AiccAgent
	apps                 map[string]sqlc.App
	settings             map[string]sqlc.AiccAgentSetting
	knowledge            map[string][]sqlc.AiccAgentKnowledge
	versionIndustryBases map[string][]sqlc.IndustryKnowledgeBasis
	sessions             map[string]sqlc.AiccSession
	messages             map[string][]sqlc.AiccMessage
	leads                map[string]sqlc.AiccLead
	leadValues           map[string][]sqlc.ListAICCLeadValuesByLeadRow
	sessionValues        map[string][]sqlc.ListAICCLeadValuesBySessionRow
	leadFields           map[string][]sqlc.AiccLeadField
	todayCount           int64
	unreadCount          int64
	blockedCount         int64
	resolved             int64
	unresolved           int64
	completeLead         int64
	analyticsSummary     AICCAnalyticsSummary
	analyticsTrend       []AICCTrendBucket
	analyticsRegions     []AICCTopItemResult
	analyticsRegionRows  []sqlc.ListAICCRegionsInRangeRow
	topQuestions         []sqlc.ListAICCTopVisitorQuestionsByOrgRow
	topSources           []sqlc.ListAICCTopSourceURLsByOrgRow
	audits               []sqlc.CreateAuditLogParams
	createArg            sqlc.CreateAICCAgentParams
	upsertSettings       *sqlc.UpsertAICCAgentSettingsParams
	addKnowledge         []sqlc.AddAICCAgentKnowledgeParams
	updateArg            sqlc.UpdateAICCAgentProfileParams
	statusArg            sqlc.SetAICCAgentStatusParams
	sessionArg           sqlc.ListAICCSessionsByAgentParams
	analyticsArg         AICCAnalyticsOptions
	completedLeadArg     sqlc.CountAICCCompletedLeadSessionsInRangeParams
	topQuestionsArg      sqlc.ListAICCTopVisitorQuestionsInRangeParams
	topSourcesArg        sqlc.ListAICCTopSourceURLsInRangeParams
	countBlockedArg      string
	readLeadArg          sqlc.MarkAICCLeadReadParams
	createField          sqlc.UpsertAICCLeadFieldParams
	deletedID            string
	createErr            error
	getErr               error
	getSettingsErr       error
	upsertSettingsErr    error
	blockedCountErr      error
	listErr              error
	updateErr            error
	statusErr            error
	deleteErr            error
	sessionsErr          error
	leadsErr             error
	organization         error
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

// CreateAuditLog 记录 AICC 管理操作审计参数。
func (f *fakeAICCStore) CreateAuditLog(_ context.Context, arg sqlc.CreateAuditLogParams) error {
	f.audits = append(f.audits, arg)
	return nil
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

// GetAICCAgentSettings 返回智能体运营配置，覆盖历史智能体未生成配置行的兼容路径。
func (f *fakeAICCStore) GetAICCAgentSettings(_ context.Context, agentID string) (sqlc.AiccAgentSetting, error) {
	if f.getSettingsErr != nil {
		return sqlc.AiccAgentSetting{}, f.getSettingsErr
	}
	row, ok := f.settings[agentID]
	if !ok {
		return sqlc.AiccAgentSetting{}, sql.ErrNoRows
	}
	return row, nil
}

// UpsertAICCAgentSettings 记录保存参数，并同步写入内存配置行供回读断言。
func (f *fakeAICCStore) UpsertAICCAgentSettings(_ context.Context, arg sqlc.UpsertAICCAgentSettingsParams) error {
	f.upsertSettings = &arg
	if f.upsertSettingsErr != nil {
		return f.upsertSettingsErr
	}
	if f.settings == nil {
		f.settings = map[string]sqlc.AiccAgentSetting{}
	}
	analyticsConfigJSON := f.settings[arg.AgentID].AnalyticsConfigJson
	f.settings[arg.AgentID] = sqlc.AiccAgentSetting{
		AgentID:                     arg.AgentID,
		MessageLimitPerSession:      arg.MessageLimitPerSession,
		SensitiveWordsJson:          arg.SensitiveWordsJson,
		BlockedVisitorEnabled:       arg.BlockedVisitorEnabled,
		BlockedVisitorThresholdJson: arg.BlockedVisitorThresholdJson,
		SessionResumeTtlMinutes:     arg.SessionResumeTtlMinutes,
		AnalyticsConfigJson:         analyticsConfigJSON,
	}
	return nil
}

// CountActiveAICCBlockedVisitorsByAgent 返回测试预置的有效封禁访客数量，用于 settings 回显统计。
func (f *fakeAICCStore) CountActiveAICCBlockedVisitorsByAgent(_ context.Context, agentID string) (int64, error) {
	f.countBlockedArg = agentID
	if f.blockedCountErr != nil {
		return 0, f.blockedCountErr
	}
	return f.blockedCount, nil
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

// ListAICCAgentKnowledge 返回智能体已挂载知识范围，用于配置回显。
func (f *fakeAICCStore) ListAICCAgentKnowledge(_ context.Context, agentID string) ([]sqlc.AiccAgentKnowledge, error) {
	return append([]sqlc.AiccAgentKnowledge(nil), f.knowledge[agentID]...), nil
}

// GetApp 返回 AICC 隐藏 app，用于按当前版本收敛行业知识库候选范围。
func (f *fakeAICCStore) GetApp(_ context.Context, id string) (sqlc.App, error) {
	row, ok := f.apps[id]
	if !ok {
		return sqlc.App{}, sql.ErrNoRows
	}
	return row, nil
}

// ListIndustryKnowledgeBasesByAssistantVersion 返回当前隐藏 app 版本授权的行业库候选项。
func (f *fakeAICCStore) ListIndustryKnowledgeBasesByAssistantVersion(_ context.Context, versionID string) ([]sqlc.IndustryKnowledgeBasis, error) {
	return append([]sqlc.IndustryKnowledgeBasis(nil), f.versionIndustryBases[versionID]...), nil
}

// DeleteAICCAgentKnowledgeByAgent 清空智能体知识范围，模拟整组替换的第一步。
func (f *fakeAICCStore) DeleteAICCAgentKnowledgeByAgent(_ context.Context, agentID string) error {
	delete(f.knowledge, agentID)
	return nil
}

// AddAICCAgentKnowledge 记录并写入单条知识范围。
func (f *fakeAICCStore) AddAICCAgentKnowledge(_ context.Context, arg sqlc.AddAICCAgentKnowledgeParams) error {
	f.addKnowledge = append(f.addKnowledge, arg)
	if f.knowledge == nil {
		f.knowledge = map[string][]sqlc.AiccAgentKnowledge{}
	}
	f.knowledge[arg.AgentID] = append(f.knowledge[arg.AgentID], sqlc.AiccAgentKnowledge{
		ID:                      arg.ID,
		AgentID:                 arg.AgentID,
		AgentOrgID:              arg.AgentOrgID,
		ScopeType:               arg.ScopeType,
		OrgID:                   arg.OrgID,
		AppID:                   arg.AppID,
		IndustryKnowledgeBaseID: arg.IndustryKnowledgeBaseID,
		RagflowDocumentID:       arg.RagflowDocumentID,
	})
	return nil
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

// ListAICCSessionsByAgent 返回指定智能体的会话列表，用于管理端运营查看。
func (f *fakeAICCStore) ListAICCSessionsByAgent(_ context.Context, arg sqlc.ListAICCSessionsByAgentParams) ([]sqlc.ListAICCSessionsByAgentRow, error) {
	f.sessionArg = arg
	if f.sessionsErr != nil {
		return nil, f.sessionsErr
	}
	rows := make([]sqlc.ListAICCSessionsByAgentRow, 0, len(f.sessions))
	for _, row := range f.sessions {
		if row.AgentID != arg.AgentID {
			continue
		}
		if arg.ResolutionStatus.Valid && row.ResolutionStatus != arg.ResolutionStatus.String {
			continue
		}
		if arg.LeadStatus.Valid && row.LeadStatus != arg.LeadStatus.String {
			continue
		}
		if arg.Channel.Valid && row.Channel != arg.Channel.String {
			continue
		}
		if arg.Region.Valid && strOrEmpty(row.Region) != arg.Region.String {
			continue
		}
		if arg.StartAt.Valid && row.CreatedAt.Before(arg.StartAt.Time) {
			continue
		}
		if arg.EndAt.Valid && !row.CreatedAt.Before(arg.EndAt.Time) {
			continue
		}
		keyword, _ := arg.Keyword.(null.String)
		if keyword.Valid && !strings.Contains(strOrEmpty(row.SourceUrl), keyword.String) && !strings.Contains(strOrEmpty(row.Referrer), keyword.String) {
			continue
		}
		rows = append(rows, f.toAICCSessionListRow(row))
	}
	return rows, nil
}

func (f *fakeAICCStore) toAICCSessionListRow(row sqlc.AiccSession) sqlc.ListAICCSessionsByAgentRow {
	return sqlc.ListAICCSessionsByAgentRow{
		ID:                 row.ID,
		AgentID:            row.AgentID,
		OrgID:              row.OrgID,
		SessionToken:       row.SessionToken,
		Channel:            row.Channel,
		SourceUrl:          row.SourceUrl,
		Referrer:           row.Referrer,
		Region:             row.Region,
		IpHash:             row.IpHash,
		UserAgentHash:      row.UserAgentHash,
		PrivacyNoticeShown: row.PrivacyNoticeShown,
		PrivacyConsentedAt: row.PrivacyConsentedAt,
		ResolutionStatus:   row.ResolutionStatus,
		LeadStatus:         row.LeadStatus,
		LastActiveAt:       row.LastActiveAt,
		ExpiresAt:          row.ExpiresAt,
		CreatedAt:          row.CreatedAt,
		UpdatedAt:          row.UpdatedAt,
		MessageCount:       int64(len(f.messages[row.ID])),
	}
}

// GetAICCSession 按会话 ID 读取详情。
func (f *fakeAICCStore) GetAICCSession(_ context.Context, id string) (sqlc.AiccSession, error) {
	row, ok := f.sessions[id]
	if !ok {
		return sqlc.AiccSession{}, sql.ErrNoRows
	}
	return row, nil
}

// ListAICCMessagesBySession 返回会话内消息镜像。
func (f *fakeAICCStore) ListAICCMessagesBySession(_ context.Context, sessionID string) ([]sqlc.AiccMessage, error) {
	return f.messages[sessionID], nil
}

// ListAICCLeadValuesBySession 返回会话已提交的留资字段值，用于会话详情回显。
func (f *fakeAICCStore) ListAICCLeadValuesBySession(_ context.Context, sessionID string) ([]sqlc.ListAICCLeadValuesBySessionRow, error) {
	return append([]sqlc.ListAICCLeadValuesBySessionRow(nil), f.sessionValues[sessionID]...), nil
}

// ListAICCLeadsByOrg 返回企业线索列表，默认由 SQL 按未读和更新时间排序。
func (f *fakeAICCStore) ListAICCLeadsByOrg(_ context.Context, arg sqlc.ListAICCLeadsByOrgParams) ([]sqlc.AiccLead, error) {
	if f.leadsErr != nil {
		return nil, f.leadsErr
	}
	rows := make([]sqlc.AiccLead, 0, len(f.leads))
	for _, row := range f.leads {
		if row.OrgID == arg.OrgID {
			rows = append(rows, row)
		}
	}
	return rows, nil
}

// ListAllAICCLeadsByOrg 返回企业全部线索，覆盖 CSV 导出不受交互分页上限影响的路径。
func (f *fakeAICCStore) ListAllAICCLeadsByOrg(_ context.Context, arg sqlc.ListAllAICCLeadsByOrgParams) ([]sqlc.AiccLead, error) {
	if f.leadsErr != nil {
		return nil, f.leadsErr
	}
	rows := make([]sqlc.AiccLead, 0, len(f.leads))
	for _, row := range f.leads {
		if row.OrgID == arg.OrgID {
			rows = append(rows, row)
		}
	}
	if arg.Limit > 0 && len(rows) > int(arg.Limit) {
		rows = rows[:arg.Limit]
	}
	return rows, nil
}

// ListAICCLeadValuesByLead 返回线索已沉淀的自定义留资字段值，用于列表展示和 CSV 导出。
func (f *fakeAICCStore) ListAICCLeadValuesByLead(_ context.Context, arg sqlc.ListAICCLeadValuesByLeadParams) ([]sqlc.ListAICCLeadValuesByLeadRow, error) {
	if !arg.LeadID.Valid || !arg.LeadOrgID.Valid {
		return nil, nil
	}
	return append([]sqlc.ListAICCLeadValuesByLeadRow(nil), f.leadValues[arg.LeadID.String]...), nil
}

// MarkAICCLeadRead 记录线索已读参数，并同步内存行。
func (f *fakeAICCStore) MarkAICCLeadRead(_ context.Context, arg sqlc.MarkAICCLeadReadParams) (int64, error) {
	f.readLeadArg = arg
	row, ok := f.leads[arg.ID]
	if !ok || row.OrgID != arg.OrgID {
		return 0, nil
	}
	row.Unread = false
	f.leads[arg.ID] = row
	return 1, nil
}

// ListAICCLeadFieldsByAgent 返回智能体留资字段配置。
func (f *fakeAICCStore) ListAICCLeadFieldsByAgent(_ context.Context, agentID string) ([]sqlc.AiccLeadField, error) {
	rows := make([]sqlc.AiccLeadField, 0, len(f.leadFields[agentID]))
	for _, field := range f.leadFields[agentID] {
		if !field.DeletedAt.Valid {
			rows = append(rows, field)
		}
	}
	return rows, nil
}

// DeactivateAICCLeadFieldsByAgent 停用智能体全部留资字段，历史值仍保留字段锚点。
func (f *fakeAICCStore) DeactivateAICCLeadFieldsByAgent(_ context.Context, agentID string) error {
	for i, field := range f.leadFields[agentID] {
		field.DeletedAt = null.TimeFrom(time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC))
		f.leadFields[agentID][i] = field
	}
	return nil
}

// UpsertAICCLeadField 记录并写入或恢复单个留资字段。
func (f *fakeAICCStore) UpsertAICCLeadField(_ context.Context, arg sqlc.UpsertAICCLeadFieldParams) error {
	f.createField = arg
	if f.leadFields == nil {
		f.leadFields = map[string][]sqlc.AiccLeadField{}
	}
	for i, field := range f.leadFields[arg.AgentID] {
		if field.FieldKey == arg.FieldKey {
			field.Label = arg.Label
			field.FieldType = arg.FieldType
			field.Required = arg.Required
			field.PromptText = arg.PromptText
			field.SortOrder = arg.SortOrder
			field.DeletedAt = null.Time{}
			f.leadFields[arg.AgentID][i] = field
			return nil
		}
	}
	f.leadFields[arg.AgentID] = append(f.leadFields[arg.AgentID], sqlc.AiccLeadField{
		ID:         arg.ID,
		AgentID:    arg.AgentID,
		FieldKey:   arg.FieldKey,
		Label:      arg.Label,
		FieldType:  arg.FieldType,
		Required:   arg.Required,
		PromptText: arg.PromptText,
		SortOrder:  arg.SortOrder,
	})
	return nil
}

// CountAICCTodaySessions 返回测试预置的今日会话数。
func (f *fakeAICCStore) CountAICCTodaySessions(_ context.Context, orgID string) (int64, error) {
	return f.todayCount, nil
}

// CountAICCUnreadLeads 返回测试预置的未读线索数。
func (f *fakeAICCStore) CountAICCUnreadLeads(_ context.Context, orgID string) (int64, error) {
	return f.unreadCount, nil
}

// CountAICCSessionsByResolution 返回测试预置的解决状态统计。
func (f *fakeAICCStore) CountAICCSessionsByResolution(_ context.Context, arg sqlc.CountAICCSessionsByResolutionParams) (int64, error) {
	if arg.ResolutionStatus == domain.AICCResolutionResolved {
		return f.resolved, nil
	}
	if arg.ResolutionStatus == domain.AICCResolutionUnresolved {
		return f.unresolved, nil
	}
	return 0, nil
}

// CountAICCCompletedLeadSessions 返回测试预置的已留资会话数量。
func (f *fakeAICCStore) CountAICCCompletedLeadSessions(_ context.Context, orgID string) (int64, error) {
	return f.completeLead, nil
}

// CountAICCCompletedLeadSessionsInRange 返回筛选窗口内已留资会话数量，并记录过滤条件。
func (f *fakeAICCStore) CountAICCCompletedLeadSessionsInRange(_ context.Context, arg sqlc.CountAICCCompletedLeadSessionsInRangeParams) (int64, error) {
	f.completedLeadArg = arg
	return f.completeLead, nil
}

// CountAICCSessionsByStatusInRange 返回测试预置的时间范围内解决状态汇总。
func (f *fakeAICCStore) CountAICCSessionsByStatusInRange(_ context.Context, arg sqlc.CountAICCSessionsByStatusInRangeParams) (sqlc.CountAICCSessionsByStatusInRangeRow, error) {
	f.analyticsArg.OrgID = arg.OrgID
	f.analyticsArg.AgentID = strOrEmpty(arg.AgentID)
	f.analyticsArg.StartAt = arg.CreatedAt
	f.analyticsArg.EndAt = arg.CreatedAt_2
	return sqlc.CountAICCSessionsByStatusInRangeRow{
		TotalSessions:      f.analyticsSummary.Sessions,
		ResolvedSessions:   f.analyticsSummary.Resolved,
		UnresolvedSessions: f.analyticsSummary.Unresolved,
		UnknownSessions:    f.analyticsSummary.Unknown,
	}, nil
}

// ListAICCSessionTrendByDay 返回日粒度趋势，并记录 service 归一化后的统计粒度。
func (f *fakeAICCStore) ListAICCSessionTrendByDay(_ context.Context, arg sqlc.ListAICCSessionTrendByDayParams) ([]sqlc.ListAICCSessionTrendByDayRow, error) {
	f.analyticsArg.Bucket = "day"
	rows := make([]sqlc.ListAICCSessionTrendByDayRow, 0, len(f.analyticsTrend))
	for _, item := range f.analyticsTrend {
		bucket, err := time.Parse("2006-01-02", item.Bucket)
		if err != nil {
			bucket = time.Time{}
		}
		rows = append(rows, sqlc.ListAICCSessionTrendByDayRow{Bucket: bucket, Count: item.Count})
	}
	return rows, nil
}

// ListAICCSessionTrendByWeek 返回周粒度趋势，并记录 service 归一化后的统计粒度。
func (f *fakeAICCStore) ListAICCSessionTrendByWeek(_ context.Context, arg sqlc.ListAICCSessionTrendByWeekParams) ([]sqlc.ListAICCSessionTrendByWeekRow, error) {
	f.analyticsArg.Bucket = "week"
	rows := make([]sqlc.ListAICCSessionTrendByWeekRow, 0, len(f.analyticsTrend))
	for _, item := range f.analyticsTrend {
		rows = append(rows, sqlc.ListAICCSessionTrendByWeekRow{Bucket: item.Bucket, Count: item.Count})
	}
	return rows, nil
}

// ListAICCRegionsInRange 返回时间范围内地域分布。
func (f *fakeAICCStore) ListAICCRegionsInRange(_ context.Context, arg sqlc.ListAICCRegionsInRangeParams) ([]sqlc.ListAICCRegionsInRangeRow, error) {
	if f.analyticsRegionRows != nil {
		return append([]sqlc.ListAICCRegionsInRangeRow(nil), f.analyticsRegionRows...), nil
	}
	rows := make([]sqlc.ListAICCRegionsInRangeRow, 0, len(f.analyticsRegions))
	for _, item := range f.analyticsRegions {
		rows = append(rows, sqlc.ListAICCRegionsInRangeRow{Label: item.Label, Count: item.Count})
	}
	return rows, nil
}

// ListAICCTopVisitorQuestionsByOrg 返回测试预置的热门问题。
func (f *fakeAICCStore) ListAICCTopVisitorQuestionsByOrg(_ context.Context, arg sqlc.ListAICCTopVisitorQuestionsByOrgParams) ([]sqlc.ListAICCTopVisitorQuestionsByOrgRow, error) {
	return f.topQuestions, nil
}

// ListAICCTopVisitorQuestionsInRange 返回筛选窗口内热门问题，并记录过滤条件。
func (f *fakeAICCStore) ListAICCTopVisitorQuestionsInRange(_ context.Context, arg sqlc.ListAICCTopVisitorQuestionsInRangeParams) ([]sqlc.ListAICCTopVisitorQuestionsInRangeRow, error) {
	f.topQuestionsArg = arg
	rows := make([]sqlc.ListAICCTopVisitorQuestionsInRangeRow, 0, len(f.topQuestions))
	for _, item := range f.topQuestions {
		rows = append(rows, sqlc.ListAICCTopVisitorQuestionsInRangeRow{Question: item.Question, Count: item.Count})
	}
	return rows, nil
}

// ListAICCTopSourceURLsByOrg 返回测试预置的来源页面分布。
func (f *fakeAICCStore) ListAICCTopSourceURLsByOrg(_ context.Context, arg sqlc.ListAICCTopSourceURLsByOrgParams) ([]sqlc.ListAICCTopSourceURLsByOrgRow, error) {
	return f.topSources, nil
}

// ListAICCTopSourceURLsInRange 返回筛选窗口内来源页面分布，并记录过滤条件。
func (f *fakeAICCStore) ListAICCTopSourceURLsInRange(_ context.Context, arg sqlc.ListAICCTopSourceURLsInRangeParams) ([]sqlc.ListAICCTopSourceURLsInRangeRow, error) {
	f.topSourcesArg = arg
	rows := make([]sqlc.ListAICCTopSourceURLsInRangeRow, 0, len(f.topSources))
	for _, item := range f.topSources {
		rows = append(rows, sqlc.ListAICCTopSourceURLsInRangeRow{SourceUrl: item.SourceUrl, Count: item.Count})
	}
	return rows, nil
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
		apps: map[string]sqlc.App{
			"app-hidden-1": {
				ID:        "app-hidden-1",
				OrgID:     "org-1",
				VersionID: null.StringFrom("version-1"),
			},
		},
		knowledge: map[string][]sqlc.AiccAgentKnowledge{
			"agent-1": {
				{ID: "knowledge-org", AgentID: "agent-1", AgentOrgID: "org-1", ScopeType: domain.AICCKnowledgeScopeTypeOrg, OrgID: null.StringFrom("org-1")},
				{ID: "knowledge-industry", AgentID: "agent-1", AgentOrgID: "org-1", ScopeType: domain.AICCKnowledgeScopeTypeIndustry, IndustryKnowledgeBaseID: null.StringFrom("industry-1")},
				{ID: "knowledge-doc", AgentID: "agent-1", AgentOrgID: "org-1", ScopeType: domain.AICCKnowledgeScopeTypeAppDocument, OrgID: null.StringFrom("org-1"), AppID: null.StringFrom("app-hidden-1"), RagflowDocumentID: null.StringFrom("doc-1")},
			},
		},
		versionIndustryBases: map[string][]sqlc.IndustryKnowledgeBasis{
			"version-1": {
				{ID: "industry-1", Name: "售前行业库"},
			},
		},
		sessions: map[string]sqlc.AiccSession{
			"session-1": {
				ID:               "session-1",
				AgentID:          "agent-1",
				OrgID:            "org-1",
				SessionToken:     "session-token",
				Channel:          domain.AICCChannelWebLink,
				ResolutionStatus: domain.AICCResolutionUnknown,
				LeadStatus:       "pending",
				CreatedAt:        time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC),
				UpdatedAt:        time.Date(2026, 7, 9, 12, 5, 0, 0, time.UTC),
			},
		},
		messages: map[string][]sqlc.AiccMessage{
			"session-1": {
				{ID: "msg-1", SessionID: "session-1", AgentID: "agent-1", Direction: "visitor", ContentType: "text", TextContent: null.StringFrom("你好"), CreatedAt: time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)},
				{ID: "msg-2", SessionID: "session-1", AgentID: "agent-1", Direction: "assistant", ContentType: "text", TextContent: null.StringFrom("您好"), CreatedAt: time.Date(2026, 7, 9, 12, 0, 2, 0, time.UTC)},
			},
		},
		leads: map[string]sqlc.AiccLead{
			"lead-1": {
				ID:                 "lead-1",
				OrgID:              "org-1",
				PrimaryContactHash: "hash-1",
				DisplayName:        null.StringFrom("张三"),
				Unread:             true,
				LatestSessionID:    null.StringFrom("session-1"),
				LatestSessionOrgID: null.StringFrom("org-1"),
				CreatedAt:          time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC),
				UpdatedAt:          time.Date(2026, 7, 9, 12, 6, 0, 0, time.UTC),
			},
		},
		leadValues: map[string][]sqlc.ListAICCLeadValuesByLeadRow{
			"lead-1": {
				{
					LeadID:    null.StringFrom("lead-1"),
					SessionID: "session-1",
					FieldID:   "field-phone",
					FieldKey:  "phone",
					Label:     "联系电话",
					FieldType: "phone",
					ValueText: "13800138000",
					CreatedAt: time.Date(2026, 7, 9, 12, 1, 0, 0, time.UTC),
				},
			},
		},
		sessionValues: map[string][]sqlc.ListAICCLeadValuesBySessionRow{
			"session-1": {
				{
					LeadID:    null.StringFrom("lead-1"),
					SessionID: "session-1",
					FieldID:   "field-phone",
					FieldKey:  "phone",
					Label:     "联系电话",
					FieldType: "phone",
					ValueText: "13800138000",
					CreatedAt: time.Date(2026, 7, 9, 12, 1, 0, 0, time.UTC),
				},
			},
		},
		todayCount:   3,
		unreadCount:  1,
		resolved:     2,
		unresolved:   1,
		completeLead: 1,
		analyticsSummary: AICCAnalyticsSummary{
			Sessions:   3,
			Resolved:   2,
			Unresolved: 1,
		},
		topQuestions: []sqlc.ListAICCTopVisitorQuestionsByOrgRow{
			{Question: "报价多少", Count: 4},
			{Question: "如何开票", Count: 2},
		},
		topSources: []sqlc.ListAICCTopSourceURLsByOrgRow{
			{SourceUrl: null.StringFrom("https://example.com/pricing"), Count: 3},
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
		AllowedDomains: []string{
			" https://WWW.Example.com/path ",
			"*.Example.org",
			"www.example.com",
		},
	})

	require.NoError(t, err)
	assert.Equal(t, "org-1", apps.lastInput.OrgID)
	assert.Equal(t, "admin-1", apps.lastInput.UserID)
	assert.Equal(t, "官网售前", apps.lastInput.Name)
	assert.Equal(t, "app-hidden-1", store.createArg.AppID)
	assert.Equal(t, "官网售前", result.Name)
	assert.NotEmpty(t, result.PublicToken)
	assert.NotEmpty(t, result.WidgetToken)
	assert.Equal(t, []string{"www.example.com", "*.example.org"}, result.AllowedDomains)
	assert.JSONEq(t, `["www.example.com","*.example.org"]`, string(store.createArg.AllowedDomainsJson))
	require.Len(t, store.audits, 1)
	assert.Equal(t, "aicc_agent", store.audits[0].TargetType)
	assert.Equal(t, "create", store.audits[0].Action)
	assert.Equal(t, result.ID, store.audits[0].TargetID)
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
		{name: "挂件域名不合法返回参数错误", principal: aiccOrgAdmin(), org: sqlc.Organization{ID: "org-1", AiccEnabled: true}, input: AICCAgentInput{Name: "售前", AllowedDomains: []string{"https://"}}, wantErr: ErrInvalidArgument},                     // 场景：域名白名单必须能解析出主机名。
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

// TestAICCSettingsDefaults 覆盖旧机器人无 settings 行的兼容路径：
// 后端必须返回默认运营配置，避免历史机器人打开设置页时报错。
func TestAICCSettingsDefaults(t *testing.T) {
	store := &fakeAICCStore{
		agents: map[string]sqlc.AiccAgent{
			"agent-1": {ID: "agent-1", OrgID: "org-1"},
		},
		getSettingsErr: sql.ErrNoRows,
	}
	svc := NewAICCService(store, nil)

	result, err := svc.GetAgentSettings(context.Background(), aiccOrgAdmin(), "agent-1")

	require.NoError(t, err)
	assert.Equal(t, "agent-1", result.AgentID)
	assert.Equal(t, int32(100), result.MessageLimitPerSession)
	assert.Equal(t, int32(30), result.SessionResumeTTLMinutes)
	assert.True(t, result.BlockedVisitorEnabled)
	assert.Empty(t, result.SensitiveWords)
	assert.Empty(t, result.BlockedVisitorThresholdJSON)
	assert.Nil(t, store.upsertSettings)
}

// TestAICCSettingsUpdateNormalizesInput 覆盖设置保存：
// 敏感词需要去空白、去空项、去重，消息上限和续接时间必须落在业务允许范围内。
func TestAICCSettingsUpdateNormalizesInput(t *testing.T) {
	store := &fakeAICCStore{
		agents: map[string]sqlc.AiccAgent{
			"agent-1": {ID: "agent-1", OrgID: "org-1"},
		},
		settings: map[string]sqlc.AiccAgentSetting{
			"agent-1": {
				AgentID:                     "agent-1",
				MessageLimitPerSession:      100,
				SensitiveWordsJson:          []byte(`[]`),
				BlockedVisitorEnabled:       true,
				BlockedVisitorThresholdJson: []byte(`{"old":1}`),
				SessionResumeTtlMinutes:     30,
				AnalyticsConfigJson:         []byte(`{"window":"7d"}`),
			},
		},
		blockedCount: 2,
	}
	svc := NewAICCService(store, nil)

	result, err := svc.UpdateAgentSettings(context.Background(), aiccOrgAdmin(), "agent-1", AICCAgentSettingsInput{
		MessageLimitPerSession:      50,
		SensitiveWords:              []string{"  违禁词  ", "", "违禁词"},
		BlockedVisitorEnabled:       true,
		BlockedVisitorThresholdJSON: []byte(`{"message_count":3}`),
		SessionResumeTTLMinutes:     60,
	})

	require.NoError(t, err)
	assert.Equal(t, int32(50), result.MessageLimitPerSession)
	assert.Equal(t, []string{"违禁词"}, result.SensitiveWords)
	assert.Equal(t, int32(60), result.SessionResumeTTLMinutes)
	assert.Equal(t, int64(2), result.BlockedVisitorCount)
	assert.Equal(t, float64(3), result.BlockedVisitorThresholdJSON["message_count"])
	assert.Equal(t, "agent-1", store.countBlockedArg)
	require.NotNil(t, store.upsertSettings)
	assert.JSONEq(t, `["违禁词"]`, string(store.upsertSettings.SensitiveWordsJson))
	assert.JSONEq(t, `{"message_count":3}`, string(store.upsertSettings.BlockedVisitorThresholdJson))
	assert.JSONEq(t, `{"window":"7d"}`, string(store.settings["agent-1"].AnalyticsConfigJson))
}

// TestAICCSettingsUpdateRejectsInvalidRanges 覆盖设置保存的数值边界：
// 消息上限和续接时间必须在数据库约束范围内，避免写库后才暴露 CHECK 错误。
func TestAICCSettingsUpdateRejectsInvalidRanges(t *testing.T) {
	cases := []struct {
		name  string                 // 子场景说明
		input AICCAgentSettingsInput // 待校验的运营配置
	}{
		{name: "消息上限低于下限", input: AICCAgentSettingsInput{MessageLimitPerSession: 0, SessionResumeTTLMinutes: 30}},     // 场景：消息上限必须至少为 1。
		{name: "消息上限超过上限", input: AICCAgentSettingsInput{MessageLimitPerSession: 1001, SessionResumeTTLMinutes: 30}},  // 场景：消息上限不能超过数据库 CHECK 上限。
		{name: "续接时间低于下限", input: AICCAgentSettingsInput{MessageLimitPerSession: 100, SessionResumeTTLMinutes: 0}},    // 场景：会话续接时间必须至少为 1 分钟。
		{name: "续接时间超过上限", input: AICCAgentSettingsInput{MessageLimitPerSession: 100, SessionResumeTTLMinutes: 1441}}, // 场景：会话续接时间不能超过 24 小时。
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeAICCStore{
				agents: map[string]sqlc.AiccAgent{
					"agent-1": {ID: "agent-1", OrgID: "org-1"},
				},
			}
			svc := NewAICCService(store, nil)

			_, err := svc.UpdateAgentSettings(context.Background(), aiccOrgAdmin(), "agent-1", tc.input)

			require.ErrorIs(t, err, ErrInvalidArgument)
			assert.Nil(t, store.upsertSettings)
		})
	}
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
		require.Len(t, store.audits, 1)
		assert.Equal(t, "update", store.audits[0].Action)
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
			require.Len(t, store.audits, 1)
			assert.Equal(t, tc.action, store.audits[0].Action)
		})
	}

	t.Run("delete 软删除智能体", func(t *testing.T) {
		store := seededAICCStore()
		svc := NewAICCService(store, &fakeAICCHiddenAppCreator{})

		err := svc.DeleteAgent(context.Background(), aiccOrgAdmin(), "agent-1")

		require.NoError(t, err)
		assert.Equal(t, "agent-1", store.deletedID)
		require.Len(t, store.audits, 1)
		assert.Equal(t, "delete", store.audits[0].Action)
	})
}

// TestAICCServiceGetAgentKnowledge 覆盖知识范围回显：返回企业知识库开关、行业库和当前客服知识库入口。
func TestAICCServiceGetAgentKnowledge(t *testing.T) {
	svc := NewAICCService(seededAICCStore(), &fakeAICCHiddenAppCreator{})

	result, err := svc.GetAgentKnowledge(context.Background(), aiccOrgAdmin(), "agent-1")

	require.NoError(t, err)
	assert.Equal(t, "agent-1", result.AgentID)
	assert.Equal(t, "app-hidden-1", result.AppID)
	assert.True(t, result.UseOrgKnowledge)
	assert.Equal(t, []string{"industry-1"}, result.IndustryKnowledgeBaseIDs)
	assert.Empty(t, result.AppDocumentIDs)
}

// TestAICCServiceListAgentKnowledgeOptions 覆盖 AICC 知识候选项：企业管理员只选择行业库，当前客服知识库默认启用。
func TestAICCServiceListAgentKnowledgeOptions(t *testing.T) {
	svc := NewAICCService(seededAICCStore(), &fakeAICCHiddenAppCreator{})

	result, err := svc.ListAgentKnowledgeOptions(context.Background(), aiccOrgAdmin(), "agent-1")

	require.NoError(t, err)
	assert.Equal(t, []AICCKnowledgeOption{{ID: "industry-1", Name: "售前行业库"}}, result.IndustryKnowledgeBases)
	assert.Empty(t, result.AppDocuments)
}

// TestAICCServiceListAgentKnowledgeOptionsRejectsCrossOrg 覆盖跨企业主体不能读取候选项。
func TestAICCServiceListAgentKnowledgeOptionsRejectsCrossOrg(t *testing.T) {
	svc := NewAICCService(seededAICCStore(), &fakeAICCHiddenAppCreator{})

	_, err := svc.ListAgentKnowledgeOptions(context.Background(), auth.Principal{Role: domain.UserRoleOrgAdmin, OrgID: "org-2"}, "agent-1")

	require.ErrorIs(t, err, ErrForbidden)
}

// TestAICCServiceReplaceAgentKnowledge 覆盖知识范围整组保存：输入会 trim、去重并按企业/行业 scope 写回。
func TestAICCServiceReplaceAgentKnowledge(t *testing.T) {
	store := seededAICCStore()
	svc := NewAICCService(store, &fakeAICCHiddenAppCreator{})

	result, err := svc.ReplaceAgentKnowledge(context.Background(), aiccOrgAdmin(), "agent-1", AICCKnowledgeInput{
		UseOrgKnowledge:          true,
		IndustryKnowledgeBaseIDs: []string{" industry-2 ", "industry-2"},
	})

	require.NoError(t, err)
	assert.True(t, result.UseOrgKnowledge)
	assert.Equal(t, []string{"industry-2"}, result.IndustryKnowledgeBaseIDs)
	assert.Empty(t, result.AppDocumentIDs)
	require.Len(t, store.addKnowledge, 2)
	assert.Equal(t, domain.AICCKnowledgeScopeTypeOrg, store.addKnowledge[0].ScopeType)
	assert.Equal(t, null.StringFrom("org-1"), store.addKnowledge[0].OrgID)
	assert.Equal(t, domain.AICCKnowledgeScopeTypeIndustry, store.addKnowledge[1].ScopeType)
	require.Len(t, store.audits, 1)
	assert.Equal(t, "update_knowledge", store.audits[0].Action)
}

// TestAICCServiceReplaceAgentKnowledgeValidation 覆盖知识范围保存的权限和参数边界。
func TestAICCServiceReplaceAgentKnowledgeValidation(t *testing.T) {
	cases := []struct {
		name      string         // 子场景说明
		principal auth.Principal // 调用主体
		input     AICCKnowledgeInput
		wantErr   error
	}{
		{name: "平台管理员不可保存", principal: auth.Principal{Role: domain.UserRolePlatformAdmin}, input: AICCKnowledgeInput{UseOrgKnowledge: true}, wantErr: ErrForbidden}, // 场景：平台只读排障不能改企业知识范围。
		{name: "行业库 ID 为空返回参数错误", principal: aiccOrgAdmin(), input: AICCKnowledgeInput{IndustryKnowledgeBaseIDs: []string{" "}}, wantErr: ErrInvalidArgument},       // 场景：空 ID 不应进入数据库外键校验。
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := NewAICCService(seededAICCStore(), &fakeAICCHiddenAppCreator{})

			_, err := svc.ReplaceAgentKnowledge(context.Background(), tc.principal, "agent-1", tc.input)

			require.ErrorIs(t, err, tc.wantErr)
		})
	}
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

// TestAICCServiceListSessionsRequiresAgentViewPermission 覆盖会话列表：
// 先读取智能体归属做权限校验，再返回该智能体下的会话运营视图。
func TestAICCServiceListSessionsRequiresAgentViewPermission(t *testing.T) {
	svc := NewAICCService(seededAICCStore(), &fakeAICCHiddenAppCreator{})

	results, err := svc.ListSessions(context.Background(), aiccOrgAdmin(), "agent-1", AICCSessionListOptions{Limit: 0, Offset: -1})

	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "session-1", results[0].ID)
	assert.Equal(t, domain.AICCChannelWebLink, results[0].Channel)
}

// TestAICCServiceListSessionsHidesEmptySessions 覆盖空会话过滤：
// 公开页或挂件历史上可能留下 0 消息会话，管理端列表不能展示这类无运营价值的数据。
func TestAICCServiceListSessionsHidesEmptySessions(t *testing.T) {
	store := seededAICCStore()
	store.sessions["empty-session"] = sqlc.AiccSession{
		ID:               "empty-session",
		AgentID:          "agent-1",
		OrgID:            "org-1",
		SessionToken:     "empty-token",
		Channel:          domain.AICCChannelWebWidget,
		ResolutionStatus: domain.AICCResolutionUnknown,
		LeadStatus:       domain.AICCLeadStatusPending,
		CreatedAt:        time.Date(2026, 7, 9, 13, 0, 0, 0, time.UTC),
	}
	svc := NewAICCService(store, &fakeAICCHiddenAppCreator{})

	results, err := svc.ListSessions(context.Background(), aiccOrgAdmin(), "agent-1", AICCSessionListOptions{})

	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "session-1", results[0].ID)
	assert.Equal(t, int64(2), results[0].MessageCount)
}

// TestAICCServiceListSessionsAppliesFilters 覆盖会话列表筛选：解决状态、留资状态、渠道和来源关键词会传入查询层。
func TestAICCServiceListSessionsAppliesFilters(t *testing.T) {
	store := seededAICCStore()
	store.sessions["session-1"] = sqlc.AiccSession{
		ID:               "session-1",
		AgentID:          "agent-1",
		OrgID:            "org-1",
		Channel:          domain.AICCChannelWebWidget,
		SourceUrl:        null.StringFrom("https://example.com/pricing"),
		ResolutionStatus: domain.AICCResolutionUnresolved,
		LeadStatus:       domain.AICCLeadStatusComplete,
	}
	svc := NewAICCService(store, &fakeAICCHiddenAppCreator{})

	results, err := svc.ListSessions(context.Background(), aiccOrgAdmin(), "agent-1", AICCSessionListOptions{
		ResolutionStatus: " unresolved ",
		LeadStatus:       domain.AICCLeadStatusComplete,
		Channel:          domain.AICCChannelWebWidget,
		Keyword:          "pricing",
	})

	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, null.StringFrom(domain.AICCResolutionUnresolved), store.sessionArg.ResolutionStatus)
	assert.Equal(t, null.StringFrom(domain.AICCLeadStatusComplete), store.sessionArg.LeadStatus)
	assert.Equal(t, null.StringFrom(domain.AICCChannelWebWidget), store.sessionArg.Channel)
	assert.Equal(t, null.StringFrom("pricing"), store.sessionArg.Keyword)
}

// TestAICCServiceListSessionsRejectsInvalidFilters 覆盖会话筛选参数边界：未知状态和渠道不进入查询层。
func TestAICCServiceListSessionsRejectsInvalidFilters(t *testing.T) {
	cases := []struct {
		name    string // 子场景说明
		options AICCSessionListOptions
	}{
		{name: "未知解决状态", options: AICCSessionListOptions{ResolutionStatus: "done"}}, // 场景：前端或调用方传入非枚举解决状态。
		{name: "未知留资状态", options: AICCSessionListOptions{LeadStatus: "ready"}},      // 场景：前端或调用方传入非枚举留资状态。
		{name: "未知渠道", options: AICCSessionListOptions{Channel: "sms"}},             // 场景：当前仅允许 web_link/web_widget/voice。
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := NewAICCService(seededAICCStore(), &fakeAICCHiddenAppCreator{})

			_, err := svc.ListSessions(context.Background(), aiccOrgAdmin(), "agent-1", tc.options)

			require.ErrorIs(t, err, ErrInvalidArgument)
		})
	}
}

// TestAICCServiceGetSessionReturnsMessages 覆盖会话详情：企业管理员只能查看本企业会话，
// 响应应同时包含会话摘要和按时间排序的消息镜像。
func TestAICCServiceGetSessionReturnsMessages(t *testing.T) {
	svc := NewAICCService(seededAICCStore(), &fakeAICCHiddenAppCreator{})

	result, err := svc.GetSession(context.Background(), aiccOrgAdmin(), "session-1")

	require.NoError(t, err)
	assert.Equal(t, "session-1", result.Session.ID)
	require.Len(t, result.Messages, 2)
	assert.Equal(t, "msg-1", result.Messages[0].ID)
	assert.Equal(t, "你好", result.Messages[0].Text)
	assert.Equal(t, int64(2), result.Session.MessageCount)
	require.Len(t, result.LeadValues, 1)
	assert.Equal(t, "phone", result.LeadValues[0].FieldKey)
	assert.Equal(t, "13800138000", result.LeadValues[0].Value)
}

// TestAICCServiceListLeadsAndMarkRead 覆盖线索运营列表和已读标记：
// 企业管理员只能操作本企业线索，平台管理员可传 orgID 做只读排障。
func TestAICCServiceListLeadsAndMarkRead(t *testing.T) {
	store := seededAICCStore()
	svc := NewAICCService(store, &fakeAICCHiddenAppCreator{})

	leads, err := svc.ListLeads(context.Background(), aiccOrgAdmin(), "", 50, 0)
	require.NoError(t, err)
	require.Len(t, leads, 1)
	assert.Equal(t, "张三", leads[0].DisplayName)
	assert.True(t, leads[0].Unread)
	require.Len(t, leads[0].Values, 1)
	assert.Equal(t, "联系电话", leads[0].Values[0].Label)
	assert.Equal(t, "13800138000", leads[0].Values[0].Value)

	err = svc.MarkLeadRead(context.Background(), aiccOrgAdmin(), "lead-1")

	require.NoError(t, err)
	assert.Equal(t, "lead-1", store.readLeadArg.ID)
	assert.Equal(t, "org-1", store.readLeadArg.OrgID)
	assert.False(t, store.leads["lead-1"].Unread)

	err = svc.MarkLeadRead(context.Background(), aiccOrgAdmin(), "missing-lead")
	require.ErrorIs(t, err, ErrNotFound)
}

// TestAICCServiceExportLeadsBypassesInteractivePaging 覆盖 CSV 导出依赖的全量线索查询：
// 即使企业线索超过管理列表单页上限，也必须全部返回给导出层。
func TestAICCServiceExportLeadsBypassesInteractivePaging(t *testing.T) {
	store := seededAICCStore()
	store.leads = map[string]sqlc.AiccLead{}
	for i := 0; i < 201; i++ {
		// 第 201 条线索用于证明导出不受 normalizeAICCPaging 的 200 条上限截断。
		id := fmt.Sprintf("lead-%03d", i)
		store.leads[id] = sqlc.AiccLead{ID: id, OrgID: "org-1", DisplayName: null.StringFrom(id), Unread: true}
	}
	svc := NewAICCService(store, &fakeAICCHiddenAppCreator{})

	leads, err := svc.ExportLeads(context.Background(), aiccOrgAdmin(), "")

	require.NoError(t, err)
	assert.Len(t, leads, 201)
	for _, lead := range leads {
		assert.Empty(t, lead.Values)
	}
}

// TestAICCServiceReplaceLeadFields 覆盖管理端整组保存留资字段：
// 旧字段会被清空，新字段按归一化后的 key、类型和顺序写回。
func TestAICCServiceReplaceLeadFields(t *testing.T) {
	store := seededAICCStore()
	store.leadFields = map[string][]sqlc.AiccLeadField{
		"agent-1": {
			{ID: "old-field", AgentID: "agent-1", FieldKey: "old", Label: "旧字段", FieldType: domain.AICCLeadFieldTypeText},
		},
	}
	svc := NewAICCService(store, &fakeAICCHiddenAppCreator{})

	fields, err := svc.ReplaceLeadFields(context.Background(), aiccOrgAdmin(), "agent-1", []AICCLeadFieldInput{
		// 手机号字段是公开页发送消息前的必填联系方式。
		{FieldKey: " phone ", Label: " 联系电话 ", FieldType: domain.AICCLeadFieldTypePhone, Required: true},
	})

	require.NoError(t, err)
	require.Len(t, fields, 1)
	assert.Equal(t, "phone", fields[0].FieldKey)
	assert.Equal(t, "联系电话", fields[0].Label)
	assert.Equal(t, domain.AICCLeadFieldTypePhone, fields[0].FieldType)
	assert.True(t, fields[0].Required)
	require.Len(t, store.audits, 1)
	assert.Equal(t, "update_lead_fields", store.audits[0].Action)
}

// TestAICCServiceAnalyticsUsesViewPermission 覆盖统计卡片：只返回当前企业今日会话和未读线索数量。
func TestAICCServiceAnalyticsUsesViewPermission(t *testing.T) {
	svc := NewAICCService(seededAICCStore(), &fakeAICCHiddenAppCreator{})

	result, err := svc.Analytics(context.Background(), aiccOrgAdmin(), AICCAnalyticsOptions{})

	require.NoError(t, err)
	assert.Equal(t, int64(3), result.TodaySessions)
	assert.Equal(t, int64(1), result.UnreadLeads)
	assert.Equal(t, int64(2), result.ResolvedSessions)
	assert.Equal(t, int64(1), result.UnresolvedSessions)
	assert.Equal(t, int64(1), result.CompletedLeadSessions)
	assert.Equal(t, []AICCTopItemResult{{Label: "报价多少", Count: 4}, {Label: "如何开票", Count: 2}}, result.TopQuestions)
	assert.Equal(t, []AICCTopItemResult{{Label: "https://example.com/pricing", Count: 3}}, result.TopSources)
}

// TestAICCAnalyticsWithRangeAndBucket 覆盖统计看板：
// service 必须把时间范围和 day/week 粒度传给 store，并返回趋势、地域和未解决率。
func TestAICCAnalyticsWithRangeAndBucket(t *testing.T) {
	start := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 7, 8, 0, 0, 0, 0, time.UTC)
	store := seededAICCStore()
	store.analyticsTrend = []AICCTrendBucket{{Bucket: "2026-07-01", Count: 3}}
	store.analyticsRegions = []AICCTopItemResult{{Label: "上海", Count: 2}}
	store.analyticsSummary = AICCAnalyticsSummary{Sessions: 5, Resolved: 2, Unresolved: 1, Unknown: 2}
	store.completeLead = 4
	svc := NewAICCService(store, nil)

	result, err := svc.Analytics(context.Background(), aiccOrgAdmin(), AICCAnalyticsOptions{
		OrgID: "org-1", AgentID: "agent-1", StartAt: start, EndAt: end, Bucket: "day",
	})

	require.NoError(t, err)
	assert.Equal(t, "org-1", store.analyticsArg.OrgID)
	assert.Equal(t, "agent-1", store.analyticsArg.AgentID)
	assert.Equal(t, start, store.analyticsArg.StartAt)
	assert.Equal(t, end, store.analyticsArg.EndAt)
	assert.Equal(t, "day", store.analyticsArg.Bucket)
	assert.Equal(t, "org-1", store.completedLeadArg.OrgID)
	assert.Equal(t, null.StringFrom("agent-1"), store.completedLeadArg.AgentID)
	assert.Equal(t, start, store.completedLeadArg.CreatedAt)
	assert.Equal(t, end, store.completedLeadArg.CreatedAt_2)
	assert.Equal(t, "org-1", store.topQuestionsArg.OrgID)
	assert.Equal(t, null.StringFrom("agent-1"), store.topQuestionsArg.AgentID)
	assert.Equal(t, start, store.topQuestionsArg.CreatedAt)
	assert.Equal(t, end, store.topQuestionsArg.CreatedAt_2)
	assert.Equal(t, "org-1", store.topSourcesArg.OrgID)
	assert.Equal(t, null.StringFrom("agent-1"), store.topSourcesArg.AgentID)
	assert.Equal(t, start, store.topSourcesArg.CreatedAt)
	assert.Equal(t, end, store.topSourcesArg.CreatedAt_2)
	assert.Equal(t, int64(5), result.TotalSessions)
	assert.Equal(t, int64(4), result.CompletedLeadSessions)
	assert.Equal(t, int64(2), result.UnknownSessions)
	assert.Equal(t, float64(1)/float64(3), result.UnresolvedRate)
	assert.Equal(t, []AICCTrendBucket{{Bucket: "2026-07-01", Count: 3}}, result.SessionTrend)
	require.Len(t, result.Regions, 1)
	assert.Equal(t, "上海", result.Regions[0].Label)
}

// TestAICCAnalyticsRegionLabelBytes 覆盖 MySQL 文本表达式扫描边界：
// sqlc 将地域 label 生成为 interface{} 时，service 必须把 []byte 转成 UTF-8 文本。
func TestAICCAnalyticsRegionLabelBytes(t *testing.T) {
	store := seededAICCStore()
	store.analyticsRegionRows = []sqlc.ListAICCRegionsInRangeRow{
		{Label: []byte("上海"), Count: 2}, // 场景：MySQL driver 将 CAST/COALESCE 文本表达式扫描为 []byte。
	}
	svc := NewAICCService(store, nil)

	result, err := svc.Analytics(context.Background(), aiccOrgAdmin(), AICCAnalyticsOptions{})

	require.NoError(t, err)
	require.Len(t, result.Regions, 1)
	assert.Equal(t, "上海", result.Regions[0].Label)
	assert.Equal(t, int64(2), result.Regions[0].Count)
}

// TestAICCAnalyticsRejectsInvalidBucket 覆盖统计粒度校验：
// 只允许空值、day 或 week，避免未知粒度落入错误 SQL 分支。
func TestAICCAnalyticsRejectsInvalidBucket(t *testing.T) {
	svc := NewAICCService(seededAICCStore(), nil)

	_, err := svc.Analytics(context.Background(), aiccOrgAdmin(), AICCAnalyticsOptions{Bucket: "month"})

	require.ErrorIs(t, err, ErrInvalidArgument)
}

// TestAICCAnalyticsRejectsRangeOver180Days 覆盖统计时间窗口上限：
// 后台统计最多查询 180 天，避免运营页面触发大范围聚合。
func TestAICCAnalyticsRejectsRangeOver180Days(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(181 * 24 * time.Hour)
	svc := NewAICCService(seededAICCStore(), nil)

	_, err := svc.Analytics(context.Background(), aiccOrgAdmin(), AICCAnalyticsOptions{StartAt: start, EndAt: end})

	require.ErrorIs(t, err, ErrInvalidArgument)
}

// TestAICCAnalyticsZeroDenominatorRate 覆盖未解决率边界：
// 当已解决与未解决会话都为 0 时，未解决率应返回 0 避免 NaN。
func TestAICCAnalyticsZeroDenominatorRate(t *testing.T) {
	store := seededAICCStore()
	store.analyticsSummary = AICCAnalyticsSummary{Sessions: 2, Unknown: 2}
	svc := NewAICCService(store, nil)

	result, err := svc.Analytics(context.Background(), aiccOrgAdmin(), AICCAnalyticsOptions{})

	require.NoError(t, err)
	assert.Equal(t, float64(0), result.UnresolvedRate)
}
