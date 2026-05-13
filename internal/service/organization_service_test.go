package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
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
	svc.SetModelValidator(orgModelValidatorStub{models: []string{"qwen2.5:7b"}})
	svc.hashPassword = fakeHash
	threshold := int32(20)

	result, err := svc.CreateOrganization(context.Background(), auth.Principal{Role: domain.UserRolePlatformAdmin}, OrganizationInput{
		Name:                   "测试组织",
		Code:                   "test-org",
		ContactName:            "张三",
		CreditWarningThreshold: &threshold,
		AdminUsername:          "org-admin",
		AdminDisplayName:       "组织管理员",
		AdminPassword:          "secret-password",
		EnabledModels:          []string{"qwen2.5:7b"},
	})
	require.NoError(t, err)
	require.NotNil(t, result.CreditWarningThreshold)
	assert.Equal(t, "测试组织", result.Name)
	assert.Equal(t, int32(20), *result.CreditWarningThreshold)
	assert.Equal(t, "test-org", result.Code)
	assert.Equal(t, "test-org", store.created.Code)
	assert.Equal(t, 1, prov.createCalls)
	assert.Equal(t, 1, prov.bootstrapCalls)
	assert.Equal(t, "test-org", prov.lastCreate.Username)
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
	assert.Equal(t, []string{"qwen2.5:7b"}, result.EnabledModels)
	assert.JSONEq(t, `["qwen2.5:7b"]`, string(store.created.EnabledModels))
}

// TestOrganizationServiceCreateAlsoCreatesOrgAdmin 验证组织服务创建Also创建组织管理员的成功路径场景。
func TestOrganizationServiceCreateAlsoCreatesOrgAdmin(t *testing.T) {
	store := &organizationStoreStub{}
	prov := &fakeProvisioner{user: newapi.User{ID: 42}, accessToken: "access-tok-xyz"}
	svc := NewOrganizationService(store, prov, mustCipher(t), nil)
	svc.SetModelValidator(orgModelValidatorStub{models: []string{"qwen2.5:7b"}})
	svc.hashPassword = fakeHash

	_, err := svc.CreateOrganization(context.Background(), auth.Principal{Role: domain.UserRolePlatformAdmin}, OrganizationInput{
		Name:             "测试组织",
		Code:             "test-org",
		AdminUsername:    "org-admin",
		AdminDisplayName: "组织管理员",
		AdminPassword:    "secret-password",
		EnabledModels:    []string{"qwen2.5:7b"},
	})
	require.NoError(t, err)

	require.True(t, store.createUserCalled)
	assert.Equal(t, store.org.ID, store.createdUser.OrgID)
	assert.Equal(t, "org-admin", store.createdUser.Username)
	assert.Equal(t, "组织管理员", store.createdUser.DisplayName)
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
	svc.SetModelValidator(orgModelValidatorStub{models: []string{"qwen2.5:7b"}})
	svc.hashPassword = fakeHash

	_, err := svc.CreateOrganization(context.Background(), auth.Principal{Role: domain.UserRolePlatformAdmin}, OrganizationInput{
		Name:             "测试组织",
		Code:             "test-org",
		AdminUsername:    "org-admin",
		AdminDisplayName: "组织管理员",
		AdminPassword:    "secret-password",
		EnabledModels:    []string{"qwen2.5:7b"},
	})
	require.Error(t, err)
	require.True(t, store.hardDeleted)
}

// TestCreateOrganizationRequiresValidCode 验证创建组织要求合法标识的预期行为场景。
func TestCreateOrganizationRequiresValidCode(t *testing.T) {
	store := &organizationStoreStub{}
	svc := NewOrganizationService(store, &fakeProvisioner{}, mustCipher(t), nil)
	svc.SetModelValidator(orgModelValidatorStub{models: []string{"qwen2.5:7b"}})
	svc.hashPassword = fakeHash

	for _, code := range []string{"", "ab", "-bad", "bad-", "Bad Org", "bad_org"} {
		_, err := svc.CreateOrganization(context.Background(), auth.Principal{Role: domain.UserRolePlatformAdmin}, OrganizationInput{
			Name:             "测试组织",
			Code:             code,
			AdminUsername:    "admin",
			AdminDisplayName: "管理员",
			AdminPassword:    "secret-password",
			EnabledModels:    []string{"qwen2.5:7b"},
		})
		require.ErrorIs(t, err, ErrMemberCreateInvalid, "code=%q", code)
	}
}

// TestCreateOrganizationNormalizesCode 验证创建组织Normalizes标识的预期行为场景。
func TestCreateOrganizationNormalizesCode(t *testing.T) {
	store := &organizationStoreStub{}
	prov := &fakeProvisioner{user: newapi.User{ID: 42}, accessToken: "access-tok-xyz"}
	svc := NewOrganizationService(store, prov, mustCipher(t), nil)
	svc.SetModelValidator(orgModelValidatorStub{models: []string{"qwen2.5:7b"}})
	svc.hashPassword = fakeHash

	result, err := svc.CreateOrganization(context.Background(), auth.Principal{Role: domain.UserRolePlatformAdmin}, OrganizationInput{
		Name:             "测试组织",
		Code:             " Test-Org ",
		AdminUsername:    "admin",
		AdminDisplayName: "管理员",
		AdminPassword:    "secret-password",
		EnabledModels:    []string{"qwen2.5:7b"},
	})

	require.NoError(t, err)
	assert.Equal(t, "test-org", result.Code)
	assert.Equal(t, "test-org", store.created.Code)
}

// TestCreateOrganizationMapsUniqueViolationToConflict 验证创建组织映射UniqueViolation到冲突的异常或拒绝路径场景。
func TestCreateOrganizationMapsUniqueViolationToConflict(t *testing.T) {
	store := &organizationStoreStub{
		createErr: &pgconn.PgError{Code: "23505"},
	}
	svc := NewOrganizationService(store, &fakeProvisioner{}, mustCipher(t), nil)
	svc.SetModelValidator(orgModelValidatorStub{models: []string{"qwen2.5:7b"}})
	svc.hashPassword = fakeHash

	_, err := svc.CreateOrganization(context.Background(), auth.Principal{Role: domain.UserRolePlatformAdmin}, OrganizationInput{
		Name:             "测试组织",
		Code:             "test-org",
		AdminUsername:    "admin",
		AdminDisplayName: "管理员",
		AdminPassword:    "secret-password",
		EnabledModels:    []string{"qwen2.5:7b"},
	})

	require.ErrorIs(t, err, ErrConflict)
}

// TestCreateOrganizationRequiresEnabledModels 验证创建组织必须通过实时模型列表校验。
func TestCreateOrganizationRequiresEnabledModels(t *testing.T) {
	store := &organizationStoreStub{}
	svc := NewOrganizationService(store, &fakeProvisioner{}, mustCipher(t), nil)
	svc.SetModelValidator(orgModelValidatorStub{err: fmt.Errorf("%w: 至少选择一个可用模型", ErrMemberCreateInvalid)})
	svc.hashPassword = fakeHash

	_, err := svc.CreateOrganization(context.Background(), auth.Principal{Role: domain.UserRolePlatformAdmin}, OrganizationInput{
		Name:             "测试组织",
		Code:             "test-org",
		AdminUsername:    "admin",
		AdminDisplayName: "管理员",
		AdminPassword:    "admin123",
	})

	require.ErrorIs(t, err, ErrMemberCreateInvalid)
	assert.False(t, store.createCalled)
}

// TestCreateOrganizationBlocksSaveWithoutModelValidator 验证模型校验器未装配时拒绝保存组织模型。
func TestCreateOrganizationBlocksSaveWithoutModelValidator(t *testing.T) {
	store := &organizationStoreStub{}
	svc := NewOrganizationService(store, &fakeProvisioner{}, mustCipher(t), nil)
	svc.hashPassword = fakeHash

	_, err := svc.CreateOrganization(context.Background(), auth.Principal{Role: domain.UserRolePlatformAdmin}, OrganizationInput{
		Name:             "测试组织",
		Code:             "test-org",
		AdminUsername:    "admin",
		AdminDisplayName: "管理员",
		AdminPassword:    "admin123",
		EnabledModels:    []string{"qwen2.5:7b"},
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "模型校验器未配置")
	assert.False(t, store.createCalled)
}

// TestUpdateOrganizationRejectsRemovingModelInUse 验证不能移除仍被未删除实例使用的模型。
func TestUpdateOrganizationRejectsRemovingModelInUse(t *testing.T) {
	store := &organizationStoreStub{}
	org := store.mustSeedOrganization(t, "test-org", []string{"qwen2.5:7b", "deepseek-r1:14b"})
	store.modelUsage = []sqlc.CountActiveAppsByOrgAndModelsRow{{ModelID: "qwen2.5:7b", AppCount: 1}}
	svc := NewOrganizationService(store, &fakeProvisioner{}, mustCipher(t), nil)
	svc.SetModelValidator(orgModelValidatorStub{models: []string{"deepseek-r1:14b"}})

	_, err := svc.UpdateOrganization(context.Background(), auth.Principal{Role: domain.UserRolePlatformAdmin}, uuidToString(org.ID), OrganizationInput{
		Name:          "测试组织",
		EnabledModels: []string{"deepseek-r1:14b"},
	})

	require.ErrorIs(t, err, ErrConflict)
	assert.Contains(t, err.Error(), "qwen2.5:7b")
	assert.Equal(t, []string{"qwen2.5:7b"}, store.modelUsageArg.ModelIds)
	assert.False(t, store.updateProfileCalled)
}

// TestUpdateOrganizationPersistsEnabledModels 验证更新组织会保存实时校验后的模型 allowlist。
func TestUpdateOrganizationPersistsEnabledModels(t *testing.T) {
	store := &organizationStoreStub{}
	org := store.mustSeedOrganization(t, "test-org", []string{"qwen2.5:7b"})
	svc := NewOrganizationService(store, &fakeProvisioner{}, mustCipher(t), nil)
	svc.SetModelValidator(orgModelValidatorStub{models: []string{"deepseek-r1:14b"}})

	result, err := svc.UpdateOrganization(context.Background(), auth.Principal{Role: domain.UserRolePlatformAdmin}, uuidToString(org.ID), OrganizationInput{
		Name:          "测试组织改名",
		EnabledModels: []string{"deepseek-r1:14b"},
	})

	require.NoError(t, err)
	assert.Equal(t, []string{"deepseek-r1:14b"}, result.EnabledModels)
	assert.JSONEq(t, `["deepseek-r1:14b"]`, string(store.updatedProfile.EnabledModels))
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

// TestOrganizationServiceListIncludesAdminUsername 验证组织列表会带出首个启用组织管理员用户名，
// 供平台管理员复制组织登录信息时使用；管理员密码明文不会从 hash 中恢复。
func TestOrganizationServiceListIncludesAdminUsername(t *testing.T) {
	orgID := mustUUID(t, "00000000-0000-0000-0000-000000000101")
	store := &organizationStoreStub{
		org: sqlc.Organization{ID: orgID, Name: "测试组织", Code: "test-org", Status: domain.StatusActive},
		orgAdmin: sqlc.User{
			ID:       mustUUID(t, "00000000-0000-0000-0000-000000000201"),
			OrgID:    orgID,
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

// fakeProvisioner 是 NewAPIUserProvisioner 的内存实现：返回预置 user 与 access_token，
// 也支持注入失败以走回滚路径。lastCreate 记录最后一次 CreateUser 入参，断言 username 派生 / password 生成。
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
	org                 sqlc.Organization
	orgAdmin            sqlc.User
	created             sqlc.CreateOrganizationParams
	updated             sqlc.SetOrganizationNewAPIUserParams
	createdUser         sqlc.CreateUserParams
	createErr           error
	createCalled        bool
	updateCalled        bool
	updateProfileCalled bool
	createUserCalled    bool
	hardDeleted         bool
	updatedProfile      sqlc.UpdateOrganizationProfileParams
	modelUsage          []sqlc.CountActiveAppsByOrgAndModelsRow
	modelUsageArg       sqlc.CountActiveAppsByOrgAndModelsParams
}

func (s *organizationStoreStub) CreateOrganization(_ context.Context, arg sqlc.CreateOrganizationParams) (sqlc.Organization, error) {
	s.created = arg
	s.createCalled = true
	if s.createErr != nil {
		return sqlc.Organization{}, s.createErr
	}
	id, _ := parseUUID("00000000-0000-0000-0000-000000000101")
	created := sqlc.Organization{
		ID:                     id,
		Name:                   arg.Name,
		Code:                   arg.Code,
		Status:                 arg.Status,
		ContactName:            arg.ContactName,
		CreditWarningThreshold: arg.CreditWarningThreshold,
		EnabledModels:          arg.EnabledModels,
	}
	s.org = created
	return created, nil
}

func (s *organizationStoreStub) SetOrganizationNewAPIUser(_ context.Context, arg sqlc.SetOrganizationNewAPIUserParams) (sqlc.Organization, error) {
	s.updated = arg
	s.updateCalled = true
	out := s.org
	out.NewapiUserID = arg.NewapiUserID
	out.NewapiUserCredentialsCiphertext = arg.NewapiUserCredentialsCiphertext
	return out, nil
}

func (s *organizationStoreStub) CreateUser(_ context.Context, arg sqlc.CreateUserParams) (sqlc.User, error) {
	s.createdUser = arg
	s.createUserCalled = true
	id, _ := parseUUID("00000000-0000-0000-0000-000000000201")
	return sqlc.User{
		ID:           id,
		OrgID:        arg.OrgID,
		Username:     arg.Username,
		PasswordHash: arg.PasswordHash,
		DisplayName:  arg.DisplayName,
		Role:         arg.Role,
		Status:       arg.Status,
	}, nil
}

func (s *organizationStoreStub) HardDeleteOrganization(_ context.Context, _ pgtype.UUID) error {
	s.hardDeleted = true
	return nil
}

func (s *organizationStoreStub) GetOrganization(_ context.Context, id pgtype.UUID) (sqlc.Organization, error) {
	if !s.org.ID.Valid || s.org.ID != id {
		return sqlc.Organization{}, pgx.ErrNoRows
	}
	return s.org, nil
}

func (s *organizationStoreStub) ListOrganizations(_ context.Context, _ sqlc.ListOrganizationsParams) ([]sqlc.Organization, error) {
	return []sqlc.Organization{s.org}, nil
}

func (s *organizationStoreStub) GetOrgAdminByOrg(_ context.Context, id pgtype.UUID) (sqlc.User, error) {
	if !s.orgAdmin.ID.Valid || s.orgAdmin.OrgID != id {
		return sqlc.User{}, pgx.ErrNoRows
	}
	return s.orgAdmin, nil
}

func (s *organizationStoreStub) UpdateOrganizationProfile(_ context.Context, arg sqlc.UpdateOrganizationProfileParams) (sqlc.Organization, error) {
	s.updatedProfile = arg
	s.updateProfileCalled = true
	s.org.Name = arg.Name
	s.org.ContactName = arg.ContactName
	s.org.EnabledModels = arg.EnabledModels
	return s.org, nil
}

func (s *organizationStoreStub) SetOrganizationStatus(_ context.Context, arg sqlc.SetOrganizationStatusParams) (sqlc.Organization, error) {
	s.org.Status = arg.Status
	return s.org, nil
}

func (s *organizationStoreStub) CountActiveAppsByOrgAndModels(_ context.Context, arg sqlc.CountActiveAppsByOrgAndModelsParams) ([]sqlc.CountActiveAppsByOrgAndModelsRow, error) {
	s.modelUsageArg = arg
	return s.modelUsage, nil
}

func (s *organizationStoreStub) mustSeedOrganization(t *testing.T, code string, models []string) sqlc.Organization {
	t.Helper()
	data, err := json.Marshal(models)
	require.NoError(t, err)
	org := sqlc.Organization{
		ID:            mustUUID(t, "00000000-0000-0000-0000-000000000101"),
		Name:          "测试组织",
		Code:          code,
		Status:        domain.StatusActive,
		EnabledModels: data,
	}
	s.org = org
	return org
}

type orgModelValidatorStub struct {
	models []string
	err    error
}

func (s orgModelValidatorStub) ValidateModelIDs(context.Context, []string) ([]string, error) {
	return s.models, s.err
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
	svc.SetModelValidator(orgModelValidatorStub{models: []string{"qwen2.5:7b"}})
	svc.hashPassword = fakeHash

	_, err := svc.CreateOrganization(context.Background(), auth.Principal{Role: domain.UserRolePlatformAdmin}, OrganizationInput{
		Name:             "v102-orphan-test",
		Code:             "test-org",
		AdminUsername:    "org-admin",
		AdminDisplayName: "组织管理员",
		AdminPassword:    "secret-password",
		EnabledModels:    []string{"qwen2.5:7b"},
	})
	require.Error(t, err)
	require.True(t, prov.deleteUserCalled)
	assert.Equal(t, int64(42), prov.deleteUserUserID)
	assert.NotEqual(t, 0, len(auditor.events))
}

// TestCreateOrganization_CreateUserFailureNoDeleteUser 校验 CreateUser 失败时不调
// DeleteUser（此时无 new-api 孤儿 user 需要清理）。
func TestCreateOrganization_CreateUserFailureNoDeleteUser(t *testing.T) {
	auditor := &fakeFailAuditor{}
	prov := &fakeProvisioner{
		createError: errors.New("create 500"),
	}
	svc := NewOrganizationService(&organizationStoreStub{}, prov, mustCipher(t), auditor)
	svc.SetModelValidator(orgModelValidatorStub{models: []string{"qwen2.5:7b"}})
	svc.hashPassword = fakeHash

	_, _ = svc.CreateOrganization(context.Background(), auth.Principal{Role: domain.UserRolePlatformAdmin}, OrganizationInput{
		Name:             "v102-create-fail",
		Code:             "test-org",
		AdminUsername:    "org-admin",
		AdminDisplayName: "组织管理员",
		AdminPassword:    "secret-password",
		EnabledModels:    []string{"qwen2.5:7b"},
	})
	assert.False(t, prov.deleteUserCalled)
}
