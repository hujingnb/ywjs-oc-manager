package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	null "github.com/guregu/null/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/store/sqlc"
)

// TestOrganizationServiceUpdateAICCConfigRequiresAvailableModelWhenEnabled 验证启用 AICC 时必须实时校验模型目录。
func TestOrganizationServiceUpdateAICCConfigRequiresAvailableModelWhenEnabled(t *testing.T) {
	store := newOrganizationAICCConfigStoreStub("org-1", "model-old", 7)
	svc := newOrganizationAICCConfigServiceForTest(store, organizationAICCModelValidatorStub{})

	_, err := svc.UpdateAICCConfig(context.Background(), auth.Principal{Role: domain.UserRolePlatformAdmin}, "org-1", AICCConfigInput{
		Enabled: true,
		Model:   " missing-model ",
	})

	require.ErrorIs(t, err, ErrInvalidArgument)
	assert.Equal(t, int32(7), store.aiccConfig.Revision)
	assert.Empty(t, store.jobs)
}

// TestOrganizationServiceUpdateAICCConfigPreservesModelCatalogError 验证目录故障保留系统错误，不能误报为用户模型参数错误。
func TestOrganizationServiceUpdateAICCConfigPreservesModelCatalogError(t *testing.T) {
	store := newOrganizationAICCConfigStoreStub("org-1", "model-old", 7)
	catalogErr := errors.New("new-api unavailable")
	svc := newOrganizationAICCConfigServiceForTest(store, organizationAICCModelValidatorErrorStub{err: catalogErr})

	_, err := svc.UpdateAICCConfig(context.Background(), auth.Principal{Role: domain.UserRolePlatformAdmin}, "org-1", AICCConfigInput{
		Enabled: true,
		Model:   "model-new",
	})

	require.ErrorIs(t, err, catalogErr)
	assert.False(t, errors.Is(err, ErrInvalidArgument))
}

// TestOrganizationServiceUpdateAICCConfigCreatesRolloutOnModelChange 验证模型变化会递增 revision 并原子创建 rollout 任务。
func TestOrganizationServiceUpdateAICCConfigCreatesRolloutOnModelChange(t *testing.T) {
	store := newOrganizationAICCConfigStoreStub("org-1", "model-old", 7)
	svc := newOrganizationAICCConfigServiceForTest(store, organizationAICCModelValidatorStub{"model-new": true})
	notifier := &organizationAICCJobNotifierStub{}
	svc.SetJobNotifier(notifier)

	result, err := svc.UpdateAICCConfig(context.Background(), auth.Principal{Role: domain.UserRolePlatformAdmin}, "org-1", AICCConfigInput{
		Enabled: true,
		Model:   " model-new ",
	})

	require.NoError(t, err)
	assert.Equal(t, "model-new", result.Model)
	assert.Equal(t, int32(8), result.Revision)
	require.Len(t, store.jobs, 1)
	assert.Equal(t, domain.JobTypeAICCModelRollout, store.jobs[0].Type)
	assert.Equal(t, int32(100), store.jobs[0].Priority)
	assert.Equal(t, int32(20), store.jobs[0].MaxAttempts)
	var payload map[string]any
	require.NoError(t, json.Unmarshal(store.jobs[0].PayloadJson, &payload))
	assert.Equal(t, map[string]any{"org_id": "org-1", "target_revision": float64(8)}, payload)
	assert.Equal(t, []string{store.jobs[0].ID}, notifier.jobIDs)
}

// TestOrganizationServiceUpdateAICCConfigDoesNotRolloutWhenModelUnchanged 验证仅修改开关或上限时不递增 revision、不创建 rollout。
func TestOrganizationServiceUpdateAICCConfigDoesNotRolloutWhenModelUnchanged(t *testing.T) {
	store := newOrganizationAICCConfigStoreStub("org-1", "model-a", 7)
	svc := newOrganizationAICCConfigServiceForTest(store, organizationAICCModelValidatorStub{"model-a": true})
	notifier := &organizationAICCJobNotifierStub{}
	svc.SetJobNotifier(notifier)
	limit := int32(5)

	result, err := svc.UpdateAICCConfig(context.Background(), auth.Principal{Role: domain.UserRolePlatformAdmin}, "org-1", AICCConfigInput{
		Enabled:    true,
		Model:      "model-a",
		AgentLimit: &limit,
	})

	require.NoError(t, err)
	assert.Equal(t, int32(7), result.Revision)
	assert.Empty(t, store.jobs)
	assert.Empty(t, notifier.jobIDs)
}

// TestOrganizationServiceUpdateAICCConfigRollsBackWhenJobInsertFails 验证 rollout 任务写入失败时配置和行业授权一起回滚。
func TestOrganizationServiceUpdateAICCConfigRollsBackWhenJobInsertFails(t *testing.T) {
	store := newOrganizationAICCConfigStoreStub("org-1", "model-old", 7)
	store.jobErr = errors.New("insert job failed")
	store.organizationIndustryBases = []sqlc.IndustryKnowledgeBasis{{ID: "kb-old", Name: "旧行业库"}}
	svc := newOrganizationAICCConfigServiceForTest(store, organizationAICCModelValidatorStub{"model-new": true})
	notifier := &organizationAICCJobNotifierStub{}
	svc.SetJobNotifier(notifier)

	_, err := svc.UpdateAICCConfig(context.Background(), auth.Principal{Role: domain.UserRolePlatformAdmin}, "org-1", AICCConfigInput{
		Enabled:                  true,
		Model:                    "model-new",
		IndustryKnowledgeBaseIDs: []string{"kb-new"},
	})

	require.ErrorContains(t, err, "创建 AICC 模型发布任务失败")
	assert.Equal(t, "model-old", store.aiccConfig.Model.String)
	assert.Equal(t, int32(7), store.aiccConfig.Revision)
	assert.Equal(t, []sqlc.IndustryKnowledgeBasis{{ID: "kb-old", Name: "旧行业库"}}, store.organizationIndustryBases)
	assert.Empty(t, notifier.jobIDs)
}

// TestOrganizationServiceGetAICCConfigAllowsOwnOrgAdminRead 验证企业管理员可以读取本企业独立 AICC 配置。
func TestOrganizationServiceGetAICCConfigAllowsOwnOrgAdminRead(t *testing.T) {
	store := newOrganizationAICCConfigStoreStub("org-1", "model-a", 7)
	store.organizationIndustryBases = []sqlc.IndustryKnowledgeBasis{{ID: "kb-1", Name: "行业知识库"}}
	svc := newOrganizationAICCConfigServiceForTest(store, organizationAICCModelValidatorStub{})

	result, err := svc.GetAICCConfig(context.Background(), auth.Principal{Role: domain.UserRoleOrgAdmin, OrgID: "org-1"}, "org-1")

	require.NoError(t, err)
	assert.Equal(t, "org-1", result.OrgID)
	assert.Equal(t, "model-a", result.Model)
	assert.Equal(t, []IndustryKnowledgeBaseRef{{ID: "kb-1", Name: "行业知识库"}}, result.IndustryKnowledgeBases)
}

// TestOrganizationServiceGetAICCConfigRejectsOtherOrg 验证企业管理员不能读取其他企业的 AICC 配置。
func TestOrganizationServiceGetAICCConfigRejectsOtherOrg(t *testing.T) {
	store := newOrganizationAICCConfigStoreStub("org-2", "model-a", 7)
	svc := newOrganizationAICCConfigServiceForTest(store, organizationAICCModelValidatorStub{})

	_, err := svc.GetAICCConfig(context.Background(), auth.Principal{Role: domain.UserRoleOrgAdmin, OrgID: "org-1"}, "org-2")

	require.ErrorIs(t, err, ErrForbidden)
}

// organizationAICCModelValidatorStub 用内存集合模拟 new-api 实时模型目录。
type organizationAICCModelValidatorStub map[string]bool

// HasModelInCatalog 仅允许测试声明存在的模型通过校验。
func (s organizationAICCModelValidatorStub) HasModelInCatalog(_ context.Context, id string) (bool, error) {
	return s[id], nil
}

// organizationAICCModelValidatorErrorStub 模拟实时模型目录查询故障。
type organizationAICCModelValidatorErrorStub struct {
	err error
}

// organizationAICCJobNotifierStub 记录事务提交后即时通知的任务 ID。
type organizationAICCJobNotifierStub struct {
	jobIDs []string
}

// Enqueue 追加已通知任务，供模型变化、回滚和未变化场景断言。
func (s *organizationAICCJobNotifierStub) Enqueue(_ context.Context, jobID string) error {
	s.jobIDs = append(s.jobIDs, jobID)
	return nil
}

// HasModelInCatalog 返回预设系统错误，供错误分类测试使用。
func (s organizationAICCModelValidatorErrorStub) HasModelInCatalog(context.Context, string) (bool, error) {
	return false, s.err
}

// newOrganizationAICCConfigStoreStub 创建带独立配置行的测试存储。
func newOrganizationAICCConfigStoreStub(orgID, model string, revision int32) *organizationStoreStub {
	return &organizationStoreStub{
		org:        sqlc.Organization{ID: orgID, Name: "测试企业", Code: "test-org", Status: domain.StatusActive},
		aiccConfig: sqlc.OrganizationAiccConfig{OrgID: orgID, Model: null.StringFrom(model), Revision: revision},
	}
}

// newOrganizationAICCConfigServiceForTest 注入模型目录和可回滚事务 runner。
func newOrganizationAICCConfigServiceForTest(store *organizationStoreStub, validator AICCModelValidator) *OrganizationService {
	svc := NewOrganizationService(store, &fakeProvisioner{}, nil, nil)
	svc.SetAICCModelValidator(validator)
	svc.SetAICCConfigTxRunner(&organizationAICCConfigTxRunnerStub{store: store})
	return svc
}

// organizationAICCConfigTxRunnerStub 用克隆快照模拟事务提交，回调失败时丢弃全部写入。
type organizationAICCConfigTxRunnerStub struct {
	store *organizationStoreStub
}

// WithOrganizationAICCConfigTx 仅在回调成功后提交配置、授权和任务变更。
func (r *organizationAICCConfigTxRunnerStub) WithOrganizationAICCConfigTx(ctx context.Context, fn func(OrganizationAICCConfigStore) error) error {
	clone := *r.store
	clone.organizationIndustryBases = append([]sqlc.IndustryKnowledgeBasis(nil), r.store.organizationIndustryBases...)
	clone.jobs = append([]sqlc.CreateJobParams(nil), r.store.jobs...)
	if err := fn(&clone); err != nil {
		return err
	}
	*r.store = clone
	return nil
}
