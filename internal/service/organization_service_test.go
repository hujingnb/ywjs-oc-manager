package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	null "github.com/guregu/null/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/integrations/newapi"
	"oc-manager/internal/store/sqlc"
)

// TestOrganizationServiceCreateRequiresPlatformAdmin 验证组织服务创建要求平台管理员的预期行为场景。
func TestOrganizationServiceCreateRequiresPlatformAdmin(t *testing.T) {
	svc := NewOrganizationService(&organizationStoreStub{}, &fakeProvisioner{}, mustCipher(t), nil)

	_, err := svc.CreateOrganization(context.Background(), auth.Principal{Role: domain.UserRoleOrgAdmin}, OrganizationInput{Name: "测试组织"})
	require.ErrorIs(t, err, ErrForbidden)
}

// TestOrganizationServiceCreateProvisionsNewAPIUser 校验 CreateOrganization 串联调
// CreateUser → BootstrapUserAccessToken → 加密落 newapi_user_credentials_ciphertext。
func TestOrganizationServiceCreateProvisionsNewAPIUser(t *testing.T) {
	store := &organizationStoreStub{}
	prov := &fakeProvisioner{
		user:        newapi.User{ID: 42, Username: "preset"},
		accessToken: "access-tok-xyz",
	}
	svc := NewOrganizationService(store, prov, mustCipher(t), nil)
	svc.SetVersionValidator(fakeVersionValidator{known: map[string]bool{}})
	svc.hashPassword = fakeHash
	threshold := int32(20)

	result, err := svc.CreateOrganization(context.Background(), auth.Principal{Role: domain.UserRolePlatformAdmin}, OrganizationInput{
		Name:                   "测试组织",
		Code:                   "test-org",
		ContactName:            "张三",
		CreditWarningThreshold: &threshold,
		AdminUsername:          "org-admin",
		AdminDisplayName:       "企业管理员",
		AdminPassword:          "secret-password",
	})
	require.NoError(t, err)
	require.NotNil(t, result.CreditWarningThreshold)
	assert.Equal(t, "测试组织", result.Name)
	assert.Equal(t, int32(20), *result.CreditWarningThreshold)
	assert.Equal(t, "test-org", result.Code)
	assert.Equal(t, "test-org", store.created.Code)
	// 新企业必须同步创建默认关闭的独立 AICC 配置行。
	assert.Equal(t, result.ID, store.aiccConfig.OrgID)
	assert.False(t, store.aiccConfig.Enabled)
	assert.Equal(t, 1, prov.createCalls)
	assert.Equal(t, 1, prov.bootstrapCalls)
	// new-api username 由 org.Code 加随机后缀派生：带 code 前缀，但不等于裸 code。
	assert.True(t, strings.HasPrefix(prov.lastCreate.Username, "test-org-"), "new-api username 应带 code 前缀: %q", prov.lastCreate.Username)
	assert.NotEqual(t, "test-org", prov.lastCreate.Username)
	require.NotEqual(t, "", prov.lastCreate.Password)
	require.True(t, store.updateCalled)
	require.Equal(t, "42", store.updated.NewapiUserID.String)
	require.True(t, store.updated.NewapiUserCredentialsCiphertext.Valid)
	// 解密验证三件套被忠实序列化
	cipher := mustCipher(t)
	plain, err := cipher.Decrypt(store.updated.NewapiUserCredentialsCiphertext.String)
	require.NoError(t, err)
	var creds OrganizationCredentials
	err = json.Unmarshal(plain, &creds)
	require.NoError(t, err)
	require.Equal(t, "access-tok-xyz", creds.AccessToken)
	assert.Equal(t, prov.lastCreate.Username, creds.Username)
	assert.Equal(t, prov.lastCreate.Password, creds.Password)
}

// TestOrganizationServiceNewOrganizationCanEnableAICC 验证新企业从首版本初始化模型后，旧启用接口会保留该模型。
func TestOrganizationServiceNewOrganizationCanEnableAICC(t *testing.T) {
	store := &organizationStoreStub{initialAICCModel: null.StringFrom("model-a")}
	prov := &fakeProvisioner{user: newapi.User{ID: 42}, accessToken: "access-tok-xyz"}
	svc := NewOrganizationService(store, prov, mustCipher(t), nil)
	svc.SetVersionValidator(fakeVersionValidator{known: map[string]bool{"version-a": true}})
	svc.hashPassword = fakeHash

	created, err := svc.CreateOrganization(context.Background(), auth.Principal{Role: domain.UserRolePlatformAdmin}, OrganizationInput{
		Name:                "新企业",
		Code:                "new-org",
		AssistantVersionIDs: []string{"version-a"},
		AdminUsername:       "admin",
		AdminDisplayName:    "管理员",
		AdminPassword:       "secret-password",
	})
	require.NoError(t, err)

	_, err = svc.UpdateAICCConfig(context.Background(), auth.Principal{Role: domain.UserRolePlatformAdmin}, created.ID, AICCConfigInput{Enabled: true})
	require.NoError(t, err)
	assert.True(t, store.updatedAICCConfig.Enabled)
	assert.Equal(t, "model-a", store.updatedAICCConfig.Model.String)
}

// TestCreateOrganizationTruncatesNewAPIDisplayName 验证组织名称超过 new-api 展示名上限时，
// 创建企业仍会使用截断后的展示名创建计费用户，避免上游参数校验导致整笔创建回滚。
func TestCreateOrganizationTruncatesNewAPIDisplayName(t *testing.T) {
	store := &organizationStoreStub{}
	prov := &fakeProvisioner{user: newapi.User{ID: 42}, accessToken: "access-tok-xyz"}
	svc := NewOrganizationService(store, prov, mustCipher(t), nil)
	svc.SetVersionValidator(fakeVersionValidator{known: map[string]bool{}})
	svc.hashPassword = fakeHash
	_, err := svc.CreateOrganization(context.Background(), auth.Principal{Role: domain.UserRolePlatformAdmin}, OrganizationInput{
		Name:             "AICC Namespace Verify Long",
		Code:             "aicc-verify",
		AdminUsername:    "admin",
		AdminDisplayName: "管理员",
		AdminPassword:    "secret-123",
	})
	require.NoError(t, err)
	assert.Equal(t, "AICC Namespace Verif", prov.lastCreate.DisplayName)
}

// TestOrganizationServiceCreateEnsuresKnowledgeDataset 验证组织创建成功后会预创建组织级 RAGFlow dataset。
func TestOrganizationServiceCreateEnsuresKnowledgeDataset(t *testing.T) {
	store := &organizationStoreStub{}
	prov := &fakeProvisioner{user: newapi.User{ID: 42}, accessToken: "access-tok-xyz"}
	kb := &knowledgeDatasetProvisionerStub{}
	svc := NewOrganizationService(store, prov, mustCipher(t), nil)
	svc.SetVersionValidator(fakeVersionValidator{known: map[string]bool{}})
	svc.SetKnowledgeDatasetProvisioner(kb)
	svc.hashPassword = fakeHash

	result, err := svc.CreateOrganization(context.Background(), auth.Principal{Role: domain.UserRolePlatformAdmin}, OrganizationInput{
		Name:             "测试组织",
		Code:             "test-org",
		AdminUsername:    "org-admin",
		AdminDisplayName: "企业管理员",
		AdminPassword:    "secret-password",
	})
	require.NoError(t, err)

	require.Len(t, kb.orgs, 1)
	assert.Equal(t, result.ID, kb.orgs[0].ID)
}

// TestProvisionNewAPIUserPersistsUsername 校验组织创建链路把 new-api 侧实际 username
// 显式落到 organizations.newapi_username 字段。
func TestProvisionNewAPIUserPersistsUsername(t *testing.T) {
	store := &organizationStoreStub{}
	prov := &fakeProvisioner{
		user:        newapi.User{ID: 42, Username: "preset"},
		accessToken: "access-tok-xyz",
	}
	svc := NewOrganizationService(store, prov, mustCipher(t), nil)
	svc.SetVersionValidator(fakeVersionValidator{known: map[string]bool{}})
	svc.hashPassword = fakeHash

	_, err := svc.CreateOrganization(context.Background(), auth.Principal{Role: domain.UserRolePlatformAdmin}, OrganizationInput{
		Name:             "测试组织",
		Code:             "test-org",
		AdminUsername:    "org-admin",
		AdminDisplayName: "企业管理员",
		AdminPassword:    "secret-password",
	})
	require.NoError(t, err)

	// 必须确实调用了 SetOrganizationNewAPIUser，否则 username 写入路径根本没被验证。
	require.True(t, store.updateCalled)
	require.True(t, store.updated.NewapiUsername.Valid, "NewapiUsername 必须 Valid，否则会落 NULL")
	// 关键断言：落库的 newapi_username 与实际传给 new-api CreateUser 的 username 完全一致。
	assert.Equal(t, prov.lastCreate.Username, store.updated.NewapiUsername.String)
	assert.True(t, strings.HasPrefix(store.updated.NewapiUsername.String, "test-org-"))
	assert.NotEqual(t, "test-org", store.updated.NewapiUsername.String)
}

// TestOrganizationServiceCreateAlsoCreatesOrgAdmin 验证组织服务创建Also创建组织管理员的成功路径场景。
func TestOrganizationServiceCreateAlsoCreatesOrgAdmin(t *testing.T) {
	store := &organizationStoreStub{}
	prov := &fakeProvisioner{user: newapi.User{ID: 42}, accessToken: "access-tok-xyz"}
	svc := NewOrganizationService(store, prov, mustCipher(t), nil)
	svc.SetVersionValidator(fakeVersionValidator{known: map[string]bool{}})
	svc.hashPassword = fakeHash

	_, err := svc.CreateOrganization(context.Background(), auth.Principal{Role: domain.UserRolePlatformAdmin}, OrganizationInput{
		Name:             "测试组织",
		Code:             "test-org",
		AdminUsername:    "org-admin",
		AdminDisplayName: "企业管理员",
		AdminPassword:    "secret-password",
	})
	require.NoError(t, err)

	require.True(t, store.createUserCalled)
	assert.Equal(t, store.org.ID, store.createdUser.OrgID.String)
	assert.Equal(t, "org-admin", store.createdUser.Username)
	assert.Equal(t, "企业管理员", store.createdUser.DisplayName)
	assert.Equal(t, "hashed:secret-password", store.createdUser.PasswordHash)
	assert.Equal(t, domain.UserRoleOrgAdmin, store.createdUser.Role)
	assert.Equal(t, domain.StatusActive, store.createdUser.Status)
}

// TestOrganizationServiceCreateRollbackOnProvisioningFailure 校验 BootstrapUserAccessToken
// 失败时回滚 manager 端组织行（HardDeleteOrganization 被调用）。
func TestOrganizationServiceCreateRollbackOnProvisioningFailure(t *testing.T) {
	store := &organizationStoreStub{}
	prov := &fakeProvisioner{
		user:           newapi.User{ID: 42},
		bootstrapError: errors.New("login 失败"),
	}
	svc := NewOrganizationService(store, prov, mustCipher(t), nil)
	svc.SetVersionValidator(fakeVersionValidator{known: map[string]bool{}})
	svc.hashPassword = fakeHash

	_, err := svc.CreateOrganization(context.Background(), auth.Principal{Role: domain.UserRolePlatformAdmin}, OrganizationInput{
		Name:             "测试组织",
		Code:             "test-org",
		AdminUsername:    "org-admin",
		AdminDisplayName: "企业管理员",
		AdminPassword:    "secret-password",
	})
	require.Error(t, err)
	require.True(t, store.hardDeleted)
}

// TestCreateOrganizationRequiresValidCode 验证创建组织要求合法标识的预期行为场景。
func TestCreateOrganizationRequiresValidCode(t *testing.T) {
	store := &organizationStoreStub{}
	svc := NewOrganizationService(store, &fakeProvisioner{}, mustCipher(t), nil)
	// 代码校验在版本校验前执行，注入空 allowlist 校验器保持一致性。
	svc.SetVersionValidator(fakeVersionValidator{known: map[string]bool{}})
	svc.hashPassword = fakeHash

	for _, code := range []string{"", "ab", "-bad", "bad-", "Bad Org", "bad_org"} {
		_, err := svc.CreateOrganization(context.Background(), auth.Principal{Role: domain.UserRolePlatformAdmin}, OrganizationInput{
			Name:             "测试组织",
			Code:             code,
			AdminUsername:    "admin",
			AdminDisplayName: "管理员",
			AdminPassword:    "secret-password",
		})
		require.ErrorIs(t, err, ErrMemberCreateInvalid, "code=%q", code)
	}
}

// TestCreateOrganizationNormalizesCode 验证创建组织Normalizes标识的预期行为场景。
func TestCreateOrganizationNormalizesCode(t *testing.T) {
	store := &organizationStoreStub{}
	prov := &fakeProvisioner{user: newapi.User{ID: 42}, accessToken: "access-tok-xyz"}
	svc := NewOrganizationService(store, prov, mustCipher(t), nil)
	svc.SetVersionValidator(fakeVersionValidator{known: map[string]bool{}})
	svc.hashPassword = fakeHash

	result, err := svc.CreateOrganization(context.Background(), auth.Principal{Role: domain.UserRolePlatformAdmin}, OrganizationInput{
		Name:             "测试组织",
		Code:             " Test-Org ",
		AdminUsername:    "admin",
		AdminDisplayName: "管理员",
		AdminPassword:    "secret-password",
	})

	require.NoError(t, err)
	assert.Equal(t, "test-org", result.Code)
	assert.Equal(t, "test-org", store.created.Code)
}

// TestCreateOrganizationMapsUniqueViolationToConflict 验证创建组织映射UniqueViolation到冲突的异常或拒绝路径场景。
// MySQL 侧通过 error message 含 "Duplicate entry" 来检测唯一约束冲突（替换原 pgconn.PgError Code=23505）。
func TestCreateOrganizationMapsUniqueViolationToConflict(t *testing.T) {
	store := &organizationStoreStub{
		// 模拟 MySQL 唯一约束冲突：错误消息包含 "Duplicate entry"。
		createErr: errors.New("Duplicate entry 'test-org' for key 'organizations.org_code'"),
	}
	svc := NewOrganizationService(store, &fakeProvisioner{}, mustCipher(t), nil)
	svc.SetVersionValidator(fakeVersionValidator{known: map[string]bool{}})
	svc.hashPassword = fakeHash

	_, err := svc.CreateOrganization(context.Background(), auth.Principal{Role: domain.UserRolePlatformAdmin}, OrganizationInput{
		Name:             "测试组织",
		Code:             "test-org",
		AdminUsername:    "admin",
		AdminDisplayName: "管理员",
		AdminPassword:    "secret-password",
	})

	require.ErrorIs(t, err, ErrConflict)
	require.ErrorContains(t, err, "企业名称或企业标识已存在")
}

// TestBuildNewAPIUsername 校验 new-api username 派生规则。
func TestBuildNewAPIUsername(t *testing.T) {
	cases := []struct {
		name string // 子场景说明
		code string // 输入组织 code
	}{
		{name: "短 code 完整保留为前缀", code: "acme"},                      // 4+1+6=11，未超上限
		{name: "13 位 code 恰好用满前缀预算", code: "abcdefghijklm"},         // 13+1+6=20，等于上限
		{name: "超长 code 前缀被截断", code: "abcdefghijklmnopqrstuvwxyz"}, // 26 位，前缀截到 13
		{name: "截断点落在短横线上不产生双横线", code: "abc-def-ghij-klmn"},        // 截到 13 位会以 "-" 结尾
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := buildNewAPIUsername(tc.code)
			require.NoError(t, err)
			// 整体长度受 new-api username max=20 限制。
			assert.LessOrEqual(t, len(got), newapiUsernameMaxLen)
			// 必须带随机后缀，不等于裸 code。
			assert.NotEqual(t, tc.code, got)
			lastDash := strings.LastIndex(got, "-")
			require.Greater(t, lastDash, 0, "派生 username 必须含分隔符")
			prefix, suffix := got[:lastDash], got[lastDash+1:]
			// 后缀长度固定为 newapiUsernameSuffixLen。
			assert.Len(t, suffix, newapiUsernameSuffixLen)
			// 前缀必须是 code 的前缀（截断只缩短长度，不改写字符）。
			assert.True(t, strings.HasPrefix(tc.code, prefix), "前缀 %q 应为 code %q 的前缀", prefix, tc.code)
			// 前缀不以短横线结尾，避免出现 "xxx--suffix"。
			assert.False(t, strings.HasSuffix(prefix, "-"), "前缀不应以短横线结尾: %q", prefix)
		})
	}
}

// TestCreateOrganizationRejectsUnknownVersionID 验证创建组织时传入不存在的助手版本 id 会被拒绝。
func TestCreateOrganizationRejectsUnknownVersionID(t *testing.T) {
	store := &organizationStoreStub{}
	svc := NewOrganizationService(store, &fakeProvisioner{}, mustCipher(t), nil)
	// known 集合为空，任何版本 id 都不存在。
	svc.SetVersionValidator(fakeVersionValidator{known: map[string]bool{}})
	svc.hashPassword = fakeHash

	_, err := svc.CreateOrganization(context.Background(), auth.Principal{Role: domain.UserRolePlatformAdmin}, OrganizationInput{
		Name:                "测试组织",
		Code:                "test-org",
		AdminUsername:       "admin",
		AdminDisplayName:    "管理员",
		AdminPassword:       "admin123",
		AssistantVersionIDs: []string{"nonexistent-version-id"},
	})

	require.ErrorIs(t, err, ErrAssistantVersionInvalid)
	// 未通过版本校验，不应写入数据库。
	assert.False(t, store.createCalled)
}

// TestCreateOrganizationBlocksSaveWithoutVersionValidator 验证版本校验器未装配时拒绝保存组织。
func TestCreateOrganizationBlocksSaveWithoutVersionValidator(t *testing.T) {
	store := &organizationStoreStub{}
	svc := NewOrganizationService(store, &fakeProvisioner{}, mustCipher(t), nil)
	svc.hashPassword = fakeHash

	_, err := svc.CreateOrganization(context.Background(), auth.Principal{Role: domain.UserRolePlatformAdmin}, OrganizationInput{
		Name:             "测试组织",
		Code:             "test-org",
		AdminUsername:    "admin",
		AdminDisplayName: "管理员",
		AdminPassword:    "admin123",
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "版本校验器未配置")
	assert.False(t, store.createCalled)
}

// TestOrganizationServiceGetRestrictsOrgScope 验证组织服务获取Restricts组织scope的预期行为场景。
func TestOrganizationServiceGetRestrictsOrgScope(t *testing.T) {
	store := &organizationStoreStub{org: sqlc.Organization{ID: mustUUID(t, "00000000-0000-0000-0000-000000000101"), Name: "测试组织", Status: domain.StatusActive}}
	svc := NewOrganizationService(store, &fakeProvisioner{}, mustCipher(t), nil)

	_, err := svc.GetOrganization(context.Background(), auth.Principal{Role: domain.UserRoleOrgMember, OrgID: "00000000-0000-0000-0000-000000000999"}, "00000000-0000-0000-0000-000000000101")
	require.ErrorIs(t, err, ErrForbidden)
}

// TestOrganizationServiceSetStatus 验证组织服务Set状态的预期行为场景。
func TestOrganizationServiceSetStatus(t *testing.T) {
	store := &organizationStoreStub{org: sqlc.Organization{ID: mustUUID(t, "00000000-0000-0000-0000-000000000101"), Name: "测试组织", Status: domain.StatusActive}}
	svc := NewOrganizationService(store, &fakeProvisioner{}, mustCipher(t), nil)

	result, err := svc.SetOrganizationStatus(context.Background(), auth.Principal{Role: domain.UserRolePlatformAdmin}, "00000000-0000-0000-0000-000000000101", domain.StatusDisabled)
	require.NoError(t, err)
	require.Equal(t, domain.StatusDisabled, result.Status)
}

// TestOrganizationServiceListIncludesAdminUsername 验证组织列表会带出首个启用组织管理员用户名。
func TestOrganizationServiceListIncludesAdminUsername(t *testing.T) {
	orgID := mustUUID(t, "00000000-0000-0000-0000-000000000101")
	store := &organizationStoreStub{
		org: sqlc.Organization{ID: orgID, Name: "测试组织", Code: "test-org", Status: domain.StatusActive},
		orgAdmin: sqlc.User{
			ID:       mustUUID(t, "00000000-0000-0000-0000-000000000201"),
			OrgID:    null.StringFrom(orgID),
			Username: "org-admin",
			Role:     domain.UserRoleOrgAdmin,
			Status:   domain.StatusActive,
		},
	}
	svc := NewOrganizationService(store, &fakeProvisioner{}, mustCipher(t), nil)

	results, err := svc.ListOrganizations(context.Background(), auth.Principal{Role: domain.UserRolePlatformAdmin}, 50, 0)

	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "org-admin", results[0].AdminUsername)
}

// TestOrganizationServiceUpdateAICCConfig 验证平台管理员可开通 AICC 并设置智能体上限。
func TestOrganizationServiceUpdateAICCConfig(t *testing.T) {
	store := &organizationStoreStub{
		org:        sqlc.Organization{ID: "org-1", Name: "Org", Code: "org", Status: domain.StatusActive},
		aiccConfig: sqlc.OrganizationAiccConfig{OrgID: "org-1", Model: null.StringFrom("model-a"), Revision: 7},
	}
	svc := NewOrganizationService(store, &fakeProvisioner{}, mustCipher(t), nil)
	limit := int32(5)

	result, err := svc.UpdateAICCConfig(context.Background(), auth.Principal{Role: domain.UserRolePlatformAdmin}, "org-1", AICCConfigInput{
		Enabled:    true,
		AgentLimit: &limit,
	})

	require.NoError(t, err)
	require.True(t, store.updateAICCConfigCalled)
	assert.Equal(t, "org-1", store.updatedAICCConfig.OrgID)
	assert.True(t, store.updatedAICCConfig.Enabled)
	require.True(t, store.updatedAICCConfig.AgentLimit.Valid)
	assert.Equal(t, int64(5), store.updatedAICCConfig.AgentLimit.Int64)
	// 旧接口尚未提交模型字段时必须保留迁移回填的模型和 revision。
	assert.Equal(t, "model-a", store.updatedAICCConfig.Model.String)
	assert.Equal(t, int32(7), store.updatedAICCConfig.Revision)
	assert.True(t, result.AICCEnabled)
	require.NotNil(t, result.AICCAgentLimit)
	assert.Equal(t, int32(5), *result.AICCAgentLimit)
}

// TestOrganizationServiceUpdateAICCConfigUpdatesIndustryKnowledge 验证平台配置的行业库授权会整组替换并在响应中回显。
func TestOrganizationServiceUpdateAICCConfigUpdatesIndustryKnowledge(t *testing.T) {
	store := &organizationStoreStub{org: sqlc.Organization{ID: "org-1", Name: "Org", Code: "org", Status: domain.StatusActive}}
	svc := NewOrganizationService(store, &fakeProvisioner{}, mustCipher(t), nil)

	result, err := svc.UpdateAICCConfig(context.Background(), auth.Principal{Role: domain.UserRolePlatformAdmin}, "org-1", AICCConfigInput{
		Enabled:                  true,
		IndustryKnowledgeBaseIDs: []string{" industry-1 ", "industry-1", "industry-2"},
	})

	require.NoError(t, err)
	assert.Equal(t, []string{"industry-1", "industry-2"}, result.IndustryKnowledgeBaseIDs)
	assert.Equal(t, []sqlc.IndustryKnowledgeBasis{{ID: "industry-1", Name: "industry-1"}, {ID: "industry-2", Name: "industry-2"}}, store.organizationIndustryBases)
	assert.True(t, store.cleanedUnauthorizedAICCKnowledge)
}

// TestOrganizationServiceUpdateAICCConfigRejectsMissingIndustryKnowledge 验证不存在的行业库在替换授权前被拒绝。
func TestOrganizationServiceUpdateAICCConfigRejectsMissingIndustryKnowledge(t *testing.T) {
	store := &organizationStoreStub{org: sqlc.Organization{ID: "org-1", Name: "Org", Code: "org", Status: domain.StatusActive}}
	svc := NewOrganizationService(store, &fakeProvisioner{}, mustCipher(t), nil)

	_, err := svc.UpdateAICCConfig(context.Background(), auth.Principal{Role: domain.UserRolePlatformAdmin}, "org-1", AICCConfigInput{
		Enabled:                  true,
		IndustryKnowledgeBaseIDs: []string{"missing"},
	})

	require.ErrorIs(t, err, ErrInvalidArgument)
	assert.False(t, store.updateAICCConfigCalled)
}

// TestOrganizationServiceUpdateAICCConfigRejectsOrgAdmin 验证企业管理员不能修改平台开通配置。
func TestOrganizationServiceUpdateAICCConfigRejectsOrgAdmin(t *testing.T) {
	store := &organizationStoreStub{org: sqlc.Organization{ID: "org-1", Name: "Org", Code: "org", Status: domain.StatusActive}}
	svc := NewOrganizationService(store, &fakeProvisioner{}, mustCipher(t), nil)

	_, err := svc.UpdateAICCConfig(context.Background(), auth.Principal{Role: domain.UserRoleOrgAdmin, OrgID: "org-1"}, "org-1", AICCConfigInput{Enabled: true})

	require.ErrorIs(t, err, ErrForbidden)
	assert.False(t, store.updateAICCConfigCalled)
}

// TestOrganizationServiceUpdateAICCConfigRejectsNegativeLimit 验证负数智能体上限会被参数校验拒绝。
func TestOrganizationServiceUpdateAICCConfigRejectsNegativeLimit(t *testing.T) {
	store := &organizationStoreStub{org: sqlc.Organization{ID: "org-1", Name: "Org", Code: "org", Status: domain.StatusActive}}
	svc := NewOrganizationService(store, &fakeProvisioner{}, mustCipher(t), nil)
	limit := int32(-1)

	_, err := svc.UpdateAICCConfig(context.Background(), auth.Principal{Role: domain.UserRolePlatformAdmin}, "org-1", AICCConfigInput{
		Enabled:    true,
		AgentLimit: &limit,
	})

	require.ErrorIs(t, err, ErrInvalidArgument)
	assert.False(t, store.updateAICCConfigCalled)
}

// TestOrganizationServiceUpdateAICCConfigClearsLimit 验证 nil 智能体上限会写入 NULL，表示企业 AICC 智能体数量不限。
func TestOrganizationServiceUpdateAICCConfigClearsLimit(t *testing.T) {
	store := &organizationStoreStub{org: sqlc.Organization{ID: "org-1", Name: "Org", Code: "org", Status: domain.StatusActive}}
	svc := NewOrganizationService(store, &fakeProvisioner{}, mustCipher(t), nil)

	result, err := svc.UpdateAICCConfig(context.Background(), auth.Principal{Role: domain.UserRolePlatformAdmin}, "org-1", AICCConfigInput{
		Enabled:    false,
		AgentLimit: nil,
	})

	require.NoError(t, err)
	require.True(t, store.updateAICCConfigCalled)
	assert.False(t, store.updatedAICCConfig.Enabled)
	assert.False(t, store.updatedAICCConfig.AgentLimit.Valid)
	assert.False(t, result.AICCEnabled)
	assert.Nil(t, result.AICCAgentLimit)
}

// TestOrganizationServiceUpdateAICCConfigMapsMissingOrg 验证更新后回读不到企业时映射为 ErrNotFound。
func TestOrganizationServiceUpdateAICCConfigMapsMissingOrg(t *testing.T) {
	store := &organizationStoreStub{org: sqlc.Organization{ID: "org-1", Name: "Org", Code: "org", Status: domain.StatusActive}}
	svc := NewOrganizationService(store, &fakeProvisioner{}, mustCipher(t), nil)

	_, err := svc.UpdateAICCConfig(context.Background(), auth.Principal{Role: domain.UserRolePlatformAdmin}, "missing-org", AICCConfigInput{Enabled: true})

	require.ErrorIs(t, err, ErrNotFound)
}

// TestOrganizationServiceUpdateAICCConfigWrapsStoreError 验证数据库更新失败时保留底层错误上下文。
func TestOrganizationServiceUpdateAICCConfigWrapsStoreError(t *testing.T) {
	storeErr := errors.New("db unavailable")
	store := &organizationStoreStub{updateAICCConfigErr: storeErr}
	svc := NewOrganizationService(store, &fakeProvisioner{}, mustCipher(t), nil)

	_, err := svc.UpdateAICCConfig(context.Background(), auth.Principal{Role: domain.UserRolePlatformAdmin}, "org-1", AICCConfigInput{Enabled: true})

	require.ErrorIs(t, err, storeErr)
	require.ErrorContains(t, err, "更新企业 AICC 配置失败")
}

func mustCipher(t *testing.T) *auth.Cipher {
	t.Helper()
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	c, err := auth.NewCipher(key)
	require.NoError(t, err)
	return c
}

// fakeProvisioner 是 NewAPIUserProvisioner 的内存实现。
type fakeProvisioner struct {
	user           newapi.User
	createError    error
	accessToken    string
	bootstrapError error

	createCalls    int
	bootstrapCalls int
	lastCreate     newapi.CreateUserInput

	deleteUserCalled bool
	deleteUserUserID int64
	deleteUserErr    error
}

func (p *fakeProvisioner) CreateUser(_ context.Context, input newapi.CreateUserInput) (newapi.User, error) {
	p.createCalls++
	p.lastCreate = input
	if p.createError != nil {
		return newapi.User{}, p.createError
	}
	user := p.user
	if user.Username == "" {
		user.Username = input.Username
	}
	return user, nil
}

func (p *fakeProvisioner) BootstrapUserAccessToken(_ context.Context, _, _ string) (string, error) {
	p.bootstrapCalls++
	if p.bootstrapError != nil {
		return "", p.bootstrapError
	}
	return p.accessToken, nil
}

func (p *fakeProvisioner) DeleteUser(_ context.Context, userID int64) error {
	p.deleteUserCalled = true
	p.deleteUserUserID = userID
	return p.deleteUserErr
}

type organizationStoreStub struct {
	org                              sqlc.Organization
	aiccConfig                       sqlc.OrganizationAiccConfig
	initialAICCModel                 null.String
	orgAdmin                         sqlc.User
	created                          sqlc.CreateOrganizationParams
	updated                          sqlc.SetOrganizationNewAPIUserParams
	createdUser                      sqlc.CreateUserParams
	createErr                        error
	createCalled                     bool
	updateCalled                     bool
	updateProfileCalled              bool
	updateAICCConfigCalled           bool
	updateAICCConfigErr              error
	createUserCalled                 bool
	hardDeleted                      bool
	updatedProfile                   sqlc.UpdateOrganizationProfileParams
	updatedAICCConfig                sqlc.UpdateOrganizationAICCConfigParams
	organizationIndustryBases        []sqlc.IndustryKnowledgeBasis
	cleanedUnauthorizedAICCKnowledge bool
}

// CreateOrganizationAICCConfig 为测试中新建企业建立默认关闭配置。
func (s *organizationStoreStub) CreateOrganizationAICCConfig(_ context.Context, orgID string) error {
	s.aiccConfig = sqlc.OrganizationAiccConfig{OrgID: orgID, Model: s.initialAICCModel, Revision: 1}
	return nil
}

// GetOrganizationAICCConfig 返回测试桩保存的独立企业配置。
func (s *organizationStoreStub) GetOrganizationAICCConfig(_ context.Context, orgID string) (sqlc.OrganizationAiccConfig, error) {
	if s.aiccConfig.OrgID == "" {
		s.aiccConfig = sqlc.OrganizationAiccConfig{OrgID: orgID, Revision: 1}
	}
	if s.aiccConfig.OrgID != orgID {
		return sqlc.OrganizationAiccConfig{}, sql.ErrNoRows
	}
	return s.aiccConfig, nil
}

func (s *organizationStoreStub) CreateOrganization(_ context.Context, arg sqlc.CreateOrganizationParams) error {
	s.created = arg
	s.createCalled = true
	if s.createErr != nil {
		return s.createErr
	}
	// 使用 service 传入的 arg.ID（由 service 调用 newUUID() 生成），供后续 GetOrganization(orgID) 读回。
	created := sqlc.Organization{
		ID:                            arg.ID,
		Name:                          arg.Name,
		Code:                          arg.Code,
		Status:                        arg.Status,
		ContactName:                   arg.ContactName,
		CreditWarningThreshold:        arg.CreditWarningThreshold,
		KnowledgeQuotaBytes:           arg.KnowledgeQuotaBytes,
		DefaultAppKnowledgeQuotaBytes: arg.DefaultAppKnowledgeQuotaBytes,
		AssistantVersionIds:           arg.AssistantVersionIds,
	}
	s.org = created
	return nil
}

// GetOrganization 创建后通过 GetOrganization 读回；写入成功后 stub 返回 org。
func (s *organizationStoreStub) GetOrganization(_ context.Context, id string) (sqlc.Organization, error) {
	if s.org.ID == "" || s.org.ID != id {
		return sqlc.Organization{}, sql.ErrNoRows
	}
	return s.org, nil
}

func (s *organizationStoreStub) SetOrganizationNewAPIUser(_ context.Context, arg sqlc.SetOrganizationNewAPIUserParams) error {
	s.updated = arg
	s.updateCalled = true
	s.org.NewapiUserID = arg.NewapiUserID
	s.org.NewapiUserCredentialsCiphertext = arg.NewapiUserCredentialsCiphertext
	s.org.NewapiUsername = arg.NewapiUsername
	return nil
}

func (s *organizationStoreStub) CreateUser(_ context.Context, arg sqlc.CreateUserParams) error {
	s.createdUser = arg
	s.createUserCalled = true
	return nil
}

func (s *organizationStoreStub) HardDeleteOrganization(_ context.Context, _ string) error {
	s.hardDeleted = true
	return nil
}

func (s *organizationStoreStub) ListOrganizations(_ context.Context, _ sqlc.ListOrganizationsParams) ([]sqlc.Organization, error) {
	return []sqlc.Organization{s.org}, nil
}

func (s *organizationStoreStub) GetOrgAdminByOrg(_ context.Context, id null.String) (sqlc.User, error) {
	if s.orgAdmin.ID == "" || s.orgAdmin.OrgID.String != id.String {
		return sqlc.User{}, sql.ErrNoRows
	}
	return s.orgAdmin, nil
}

func (s *organizationStoreStub) UpdateOrganizationProfile(_ context.Context, arg sqlc.UpdateOrganizationProfileParams) error {
	s.updatedProfile = arg
	s.updateProfileCalled = true
	s.org.Name = arg.Name
	s.org.ContactName = arg.ContactName
	s.org.KnowledgeQuotaBytes = arg.KnowledgeQuotaBytes
	s.org.DefaultAppKnowledgeQuotaBytes = arg.DefaultAppKnowledgeQuotaBytes
	s.org.AssistantVersionIds = arg.AssistantVersionIds
	return nil
}

func (s *organizationStoreStub) SetOrganizationStatus(_ context.Context, arg sqlc.SetOrganizationStatusParams) error {
	s.org.Status = arg.Status
	return nil
}

func (s *organizationStoreStub) UpdateOrganizationAICCConfig(_ context.Context, arg sqlc.UpdateOrganizationAICCConfigParams) error {
	if s.updateAICCConfigErr != nil {
		return s.updateAICCConfigErr
	}
	s.updatedAICCConfig = arg
	s.updateAICCConfigCalled = true
	s.aiccConfig = sqlc.OrganizationAiccConfig{OrgID: arg.OrgID, Enabled: arg.Enabled, Model: arg.Model, AgentLimit: arg.AgentLimit, Revision: arg.Revision}
	return nil
}

// GetIndustryKnowledgeBase 模拟平台行业库仍可用，供 AICC 授权配置的存在性校验使用。
func (s *organizationStoreStub) GetIndustryKnowledgeBase(_ context.Context, id string) (sqlc.IndustryKnowledgeBasis, error) {
	if id == "missing" {
		return sqlc.IndustryKnowledgeBasis{}, sql.ErrNoRows
	}
	return sqlc.IndustryKnowledgeBasis{ID: id, Name: id}, nil
}

// ReplaceOrganizationIndustryKnowledgeBases 清空测试桩中的企业行业库授权。
func (s *organizationStoreStub) ReplaceOrganizationIndustryKnowledgeBases(_ context.Context, _ string) error {
	s.organizationIndustryBases = nil
	return nil
}

// AddOrganizationIndustryKnowledgeBase 追加测试桩授权项，并模拟真实查询的一行写入结果。
func (s *organizationStoreStub) AddOrganizationIndustryKnowledgeBase(_ context.Context, arg sqlc.AddOrganizationIndustryKnowledgeBaseParams) (int64, error) {
	s.organizationIndustryBases = append(s.organizationIndustryBases, sqlc.IndustryKnowledgeBasis{ID: arg.IndustryKnowledgeBaseID, Name: arg.IndustryKnowledgeBaseID})
	return 1, nil
}

// DeleteAICCAgentIndustryKnowledgeNotAuthorizedByOrg 记录失效关联清理已触发。
func (s *organizationStoreStub) DeleteAICCAgentIndustryKnowledgeNotAuthorizedByOrg(_ context.Context, _ string) error {
	s.cleanedUnauthorizedAICCKnowledge = true
	return nil
}

// ListOrganizationIndustryKnowledgeBases 返回测试桩中企业已授权的行业库，用于响应回显。
func (s *organizationStoreStub) ListOrganizationIndustryKnowledgeBases(_ context.Context, _ string) ([]sqlc.IndustryKnowledgeBasis, error) {
	return append([]sqlc.IndustryKnowledgeBasis(nil), s.organizationIndustryBases...), nil
}

func (s *organizationStoreStub) mustSeedOrganization(t *testing.T, code string) sqlc.Organization {
	t.Helper()
	org := sqlc.Organization{
		ID:     mustUUID(t, "00000000-0000-0000-0000-000000000101"),
		Name:   "测试组织",
		Code:   code,
		Status: domain.StatusActive,
	}
	s.org = org
	return org
}

// fakeVersionValidator 是 OrganizationVersionValidator 的内存桩：known 集合内的 id 通过，其余报错。
type fakeVersionValidator struct {
	known map[string]bool
}

func (f fakeVersionValidator) ValidateAssistantVersionIDs(_ context.Context, ids []string) ([]string, error) {
	out := []string{}
	seen := map[string]bool{}
	for _, id := range ids {
		if id == "" || seen[id] {
			continue
		}
		if !f.known[id] {
			return nil, fmt.Errorf("%w: 版本 %s 不存在", ErrAssistantVersionInvalid, id)
		}
		seen[id] = true
		out = append(out, id)
	}
	return out, nil
}

// fakeFailAuditor 实现 NewAPIFailureAuditor，仅记录失败事件，供测试断言审计是否被触发。
type fakeFailAuditor struct {
	events []NewAPIFailureContext
}

func (f *fakeFailAuditor) RecordNewAPIFailure(_ context.Context, fc NewAPIFailureContext) {
	f.events = append(f.events, fc)
}

// TestCreateOrganization_BootstrapTokenFailureTriggersDeleteUserAndAudit 校验
// BootstrapUserAccessToken 失败时调用 DeleteUser 清理孤儿，并写 audit 事件。
func TestCreateOrganization_BootstrapTokenFailureTriggersDeleteUserAndAudit(t *testing.T) {
	auditor := &fakeFailAuditor{}
	prov := &fakeProvisioner{
		user:           newapi.User{ID: 42},
		bootstrapError: errors.New("login 5xx"),
	}
	svc := NewOrganizationService(&organizationStoreStub{}, prov, mustCipher(t), auditor)
	svc.SetVersionValidator(fakeVersionValidator{known: map[string]bool{}})
	svc.hashPassword = fakeHash

	_, err := svc.CreateOrganization(context.Background(), auth.Principal{Role: domain.UserRolePlatformAdmin}, OrganizationInput{
		Name:             "v102-orphan-test",
		Code:             "test-org",
		AdminUsername:    "org-admin",
		AdminDisplayName: "企业管理员",
		AdminPassword:    "secret-password",
	})
	require.Error(t, err)
	require.True(t, prov.deleteUserCalled)
	assert.Equal(t, int64(42), prov.deleteUserUserID)
	assert.NotEqual(t, 0, len(auditor.events))
	// 回滚路径写的审计事件一律不带 OrgID：组织行随后被 HardDeleteOrganization 回滚删除。
	for i, ev := range auditor.events {
		assert.Empty(t, ev.OrgID, "回滚路径审计事件[%d] 不应带 OrgID", i)
	}
}

// TestCreateOrganization_CreateUserFailureNoDeleteUser 校验 CreateUser 失败时不调 DeleteUser。
func TestCreateOrganization_CreateUserFailureNoDeleteUser(t *testing.T) {
	auditor := &fakeFailAuditor{}
	prov := &fakeProvisioner{
		createError: errors.New("create 500"),
	}
	svc := NewOrganizationService(&organizationStoreStub{}, prov, mustCipher(t), auditor)
	svc.SetVersionValidator(fakeVersionValidator{known: map[string]bool{}})
	svc.hashPassword = fakeHash

	_, _ = svc.CreateOrganization(context.Background(), auth.Principal{Role: domain.UserRolePlatformAdmin}, OrganizationInput{
		Name:             "v102-create-fail",
		Code:             "test-org",
		AdminUsername:    "org-admin",
		AdminDisplayName: "企业管理员",
		AdminPassword:    "secret-password",
	})
	assert.False(t, prov.deleteUserCalled)
}

// TestCreateOrganizationWithVersionIDs 验证创建组织时传入合法的助手版本 id allowlist。
func TestCreateOrganizationWithVersionIDs(t *testing.T) {
	store := &organizationStoreStub{}
	prov := &fakeProvisioner{user: newapi.User{ID: 42}, accessToken: "tok"}
	svc := NewOrganizationService(store, prov, mustCipher(t), nil)
	// 注入两个已知版本 id。
	svc.SetVersionValidator(fakeVersionValidator{known: map[string]bool{
		"ver-aaa": true,
		"ver-bbb": true,
	}})
	svc.hashPassword = fakeHash

	result, err := svc.CreateOrganization(context.Background(), auth.Principal{Role: domain.UserRolePlatformAdmin}, OrganizationInput{
		Name:                "测试组织",
		Code:                "test-org",
		AdminUsername:       "admin",
		AdminDisplayName:    "管理员",
		AdminPassword:       "admin123",
		AssistantVersionIDs: []string{"ver-aaa", "ver-bbb"},
	})

	require.NoError(t, err)
	// 结果中应包含传入的版本 id。
	assert.Equal(t, []string{"ver-aaa", "ver-bbb"}, result.AssistantVersionIDs)
	// 数据库写入的 JSON 字节应能反序列化为相同列表。
	var stored []string
	require.NoError(t, json.Unmarshal(store.created.AssistantVersionIds, &stored))
	assert.Equal(t, []string{"ver-aaa", "ver-bbb"}, stored)
}

// TestUpdateOrganizationWithVersionIDsSet 验证更新组织时显式传入 AssistantVersionIDsSet=true，
// 新的 allowlist 经校验后被写入，旧 allowlist 不再保留。
func TestUpdateOrganizationWithVersionIDsSet(t *testing.T) {
	store := &organizationStoreStub{}
	// 初始化组织，预置旧 allowlist（可以为空或已有值）。
	org := store.mustSeedOrganization(t, "test-org")
	svc := NewOrganizationService(store, &fakeProvisioner{}, mustCipher(t), nil)
	// 注入新版本 id。
	svc.SetVersionValidator(fakeVersionValidator{known: map[string]bool{
		"ver-new": true,
	}})

	result, err := svc.UpdateOrganization(context.Background(), auth.Principal{Role: domain.UserRolePlatformAdmin}, org.ID, OrganizationInput{
		Name:                   "测试组织改名",
		AssistantVersionIDs:    []string{"ver-new"},
		AssistantVersionIDsSet: true,
	})

	require.NoError(t, err)
	// allowlist 应更新为新传入值。
	assert.Equal(t, []string{"ver-new"}, result.AssistantVersionIDs)
	// 数据库写入值验证。
	var stored []string
	require.NoError(t, json.Unmarshal(store.updatedProfile.AssistantVersionIds, &stored))
	assert.Equal(t, []string{"ver-new"}, stored)
}

// TestCreateOrganization_PersistsKnowledgeQuotaBytes 验证创建企业时知识库容量写入 CreateOrganizationParams。
func TestCreateOrganization_PersistsKnowledgeQuotaBytes(t *testing.T) {
	store := &organizationStoreStub{}
	prov := &fakeProvisioner{user: newapi.User{ID: 42}, accessToken: "access-tok-xyz"}
	svc := NewOrganizationService(store, prov, mustCipher(t), nil)
	svc.SetVersionValidator(fakeVersionValidator{known: map[string]bool{}})
	svc.hashPassword = fakeHash
	quota := int64(2 * 1024 * 1024 * 1024)

	_, err := svc.CreateOrganization(context.Background(), auth.Principal{Role: domain.UserRolePlatformAdmin}, OrganizationInput{
		Name:                "测试组织",
		Code:                "test-org",
		KnowledgeQuotaBytes: &quota,
		AdminUsername:       "org-admin",
		AdminDisplayName:    "企业管理员",
		AdminPassword:       "secret-password",
	})
	require.NoError(t, err)
	assert.Equal(t, quota, store.created.KnowledgeQuotaBytes)
}

// TestCreateOrganization_DefaultsKnowledgeQuotaBytes 验证创建企业未传容量时默认 1GB。
func TestCreateOrganization_DefaultsKnowledgeQuotaBytes(t *testing.T) {
	store := &organizationStoreStub{}
	prov := &fakeProvisioner{user: newapi.User{ID: 42}, accessToken: "access-tok-xyz"}
	svc := NewOrganizationService(store, prov, mustCipher(t), nil)
	svc.SetVersionValidator(fakeVersionValidator{known: map[string]bool{}})
	svc.hashPassword = fakeHash

	_, err := svc.CreateOrganization(context.Background(), auth.Principal{Role: domain.UserRolePlatformAdmin}, OrganizationInput{
		Name:             "测试组织",
		Code:             "test-org",
		AdminUsername:    "org-admin",
		AdminDisplayName: "企业管理员",
		AdminPassword:    "secret-password",
	})
	require.NoError(t, err)
	assert.Equal(t, KnowledgeQuotaDefaultBytes, store.created.KnowledgeQuotaBytes)
}

// TestUpdateOrganization_PreservesKnowledgeQuotaWhenOmitted 验证编辑企业未传容量时保留原值。
func TestUpdateOrganization_PreservesKnowledgeQuotaWhenOmitted(t *testing.T) {
	store := &organizationStoreStub{}
	org := store.mustSeedOrganization(t, "test-org")
	store.org.KnowledgeQuotaBytes = 3 * 1024 * 1024 * 1024
	svc := NewOrganizationService(store, &fakeProvisioner{}, mustCipher(t), nil)
	svc.SetVersionValidator(fakeVersionValidator{known: map[string]bool{}})

	_, err := svc.UpdateOrganization(context.Background(), auth.Principal{Role: domain.UserRolePlatformAdmin}, org.ID, OrganizationInput{
		Name:                   store.org.Name,
		AssistantVersionIDsSet: false,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(3*1024*1024*1024), store.updatedProfile.KnowledgeQuotaBytes)
}

// TestCreateOrganization_PersistsMaxInstanceCount 验证创建企业时实例上限透传到 CreateOrganizationParams。
// 覆盖正常路径：平台管理员传入正整数上限，service 应原样写库。
func TestCreateOrganization_PersistsMaxInstanceCount(t *testing.T) {
	store := &organizationStoreStub{}
	// new-api provisioner 需返回有效 user_id，否则创建链路在派生 new-api 用户时报错。
	prov := &fakeProvisioner{user: newapi.User{ID: 42}, accessToken: "access-tok-xyz"}
	svc := NewOrganizationService(store, prov, mustCipher(t), nil)
	svc.SetVersionValidator(fakeVersionValidator{known: map[string]bool{}})
	svc.hashPassword = fakeHash

	limit := int32(5)
	_, err := svc.CreateOrganization(context.Background(), auth.Principal{Role: domain.UserRolePlatformAdmin}, OrganizationInput{
		Name: "限额企业", Code: "limited-org",
		AdminUsername: "admin", AdminDisplayName: "管理员", AdminPassword: "secret-123",
		MaxInstanceCount: &limit,
	})

	require.NoError(t, err)
	require.True(t, store.created.MaxInstanceCount.Valid) // 上限有效值应写库
	assert.Equal(t, int64(5), store.created.MaxInstanceCount.Int64)
}

// TestUpdateOrganization_PersistsMaxInstanceCount 验证编辑企业时实例上限透传到 UpdateOrganizationProfileParams。
// 同时覆盖「上限可低于当前实例数」语义：service 编辑路径不校验当前实例数，原样保存。
func TestUpdateOrganization_PersistsMaxInstanceCount(t *testing.T) {
	store := &organizationStoreStub{}
	store.mustSeedOrganization(t, "limited-org")
	prov := &fakeProvisioner{}
	svc := NewOrganizationService(store, prov, mustCipher(t), nil)

	limit := int32(3)
	_, err := svc.UpdateOrganization(context.Background(), auth.Principal{Role: domain.UserRolePlatformAdmin}, store.org.ID, OrganizationInput{
		Name: "限额企业", MaxInstanceCount: &limit,
	})

	require.NoError(t, err)
	require.True(t, store.updatedProfile.MaxInstanceCount.Valid)
	assert.Equal(t, int64(3), store.updatedProfile.MaxInstanceCount.Int64)
}

// TestCreateOrganization_PersistsDefaultAppKnowledgeQuota 验证创建企业时"个人知识库默认配额"写入 CreateOrganizationParams。
// 覆盖正常路径：平台管理员显式传入正整数默认配额，service 应原样写库。
func TestCreateOrganization_PersistsDefaultAppKnowledgeQuota(t *testing.T) {
	store := &organizationStoreStub{}
	prov := &fakeProvisioner{user: newapi.User{ID: 42}, accessToken: "access-tok-xyz"}
	svc := NewOrganizationService(store, prov, mustCipher(t), nil)
	svc.SetVersionValidator(fakeVersionValidator{known: map[string]bool{}})
	svc.hashPassword = fakeHash
	quota := int64(5 * 1024 * 1024 * 1024) // 5GB，区别于 1GB 默认以确认确实来自入参

	_, err := svc.CreateOrganization(context.Background(), auth.Principal{Role: domain.UserRolePlatformAdmin}, OrganizationInput{
		Name:                          "测试组织",
		Code:                          "test-org",
		DefaultAppKnowledgeQuotaBytes: &quota,
		AdminUsername:                 "org-admin",
		AdminDisplayName:              "企业管理员",
		AdminPassword:                 "secret-password",
	})
	require.NoError(t, err)
	assert.Equal(t, quota, store.created.DefaultAppKnowledgeQuotaBytes)
}

// TestCreateOrganization_DefaultsAppKnowledgeQuota 验证创建企业未传"个人知识库默认配额"时回落 1GB 默认。
// 覆盖边界：nil 入参走 normalizeKnowledgeQuotaBytes，写入 KnowledgeQuotaDefaultBytes。
func TestCreateOrganization_DefaultsAppKnowledgeQuota(t *testing.T) {
	store := &organizationStoreStub{}
	prov := &fakeProvisioner{user: newapi.User{ID: 42}, accessToken: "access-tok-xyz"}
	svc := NewOrganizationService(store, prov, mustCipher(t), nil)
	svc.SetVersionValidator(fakeVersionValidator{known: map[string]bool{}})
	svc.hashPassword = fakeHash

	_, err := svc.CreateOrganization(context.Background(), auth.Principal{Role: domain.UserRolePlatformAdmin}, OrganizationInput{
		Name:             "测试组织",
		Code:             "test-org",
		AdminUsername:    "org-admin",
		AdminDisplayName: "企业管理员",
		AdminPassword:    "secret-password",
	})
	require.NoError(t, err)
	assert.Equal(t, KnowledgeQuotaDefaultBytes, store.created.DefaultAppKnowledgeQuotaBytes)
}

// TestCreateOrganization_RejectsNonPositiveDefaultAppKnowledgeQuota 验证显式传入非正数默认配额时返回参数错误。
// 覆盖异常路径：0 值经 normalizeKnowledgeQuotaBytes 校验应被拒绝。
func TestCreateOrganization_RejectsNonPositiveDefaultAppKnowledgeQuota(t *testing.T) {
	store := &organizationStoreStub{}
	prov := &fakeProvisioner{user: newapi.User{ID: 42}, accessToken: "access-tok-xyz"}
	svc := NewOrganizationService(store, prov, mustCipher(t), nil)
	svc.SetVersionValidator(fakeVersionValidator{known: map[string]bool{}})
	svc.hashPassword = fakeHash
	invalid := int64(0)

	_, err := svc.CreateOrganization(context.Background(), auth.Principal{Role: domain.UserRolePlatformAdmin}, OrganizationInput{
		Name:                          "测试组织",
		Code:                          "test-org",
		DefaultAppKnowledgeQuotaBytes: &invalid,
		AdminUsername:                 "org-admin",
		AdminDisplayName:              "企业管理员",
		AdminPassword:                 "secret-password",
	})
	require.ErrorIs(t, err, ErrMemberCreateInvalid)
}

// TestUpdateOrganization_PreservesDefaultAppKnowledgeQuotaWhenOmitted 验证编辑企业未传默认配额时保留原值。
// 覆盖边界：nil 入参不覆盖数据库既有 default_app_knowledge_quota_bytes。
func TestUpdateOrganization_PreservesDefaultAppKnowledgeQuotaWhenOmitted(t *testing.T) {
	store := &organizationStoreStub{}
	org := store.mustSeedOrganization(t, "test-org")
	store.org.DefaultAppKnowledgeQuotaBytes = 7 * 1024 * 1024 * 1024
	svc := NewOrganizationService(store, &fakeProvisioner{}, mustCipher(t), nil)
	svc.SetVersionValidator(fakeVersionValidator{known: map[string]bool{}})

	_, err := svc.UpdateOrganization(context.Background(), auth.Principal{Role: domain.UserRolePlatformAdmin}, org.ID, OrganizationInput{
		Name:                   store.org.Name,
		AssistantVersionIDsSet: false,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(7*1024*1024*1024), store.updatedProfile.DefaultAppKnowledgeQuotaBytes)
}
