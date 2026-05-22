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
	svc.SetVersionValidator(fakeVersionValidator{known: map[string]bool{}})
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
		ModelID:                "qwen2.5:7b",
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
	// model_id 直接透传，不再校验；版本校验器通过则创建成功。
	assert.Equal(t, "qwen2.5:7b", result.ModelID)
	assert.Equal(t, "qwen2.5:7b", store.created.ModelID)
}

// TestProvisionNewAPIUserPersistsUsername 校验组织创建链路把 new-api 侧 username
// （当前等于 org.Code）显式落到 organizations.newapi_username 字段，供 usage 查询
// 直接读取，防止"username 与 code 同值"这一隐式约定回归——一旦 username 生成策略
// 变化（例如加随机后缀），下游再走"凭据密文解密"或"运行时查 new-api"会代价过高。
func TestProvisionNewAPIUserPersistsUsername(t *testing.T) {
	// 用与 TestOrganizationServiceCreateProvisionsNewAPIUser 相同的桩件组合，
	// 保证只额外校验 NewapiUsername 这一字段，避免与其他用例语义重叠。
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
		AdminDisplayName: "组织管理员",
		AdminPassword:    "secret-password",
		ModelID:          "qwen2.5:7b",
	})
	require.NoError(t, err)

	// 必须确实调用了 SetOrganizationNewAPIUser，否则 username 写入路径根本没被验证。
	require.True(t, store.updateCalled)
	// 关键断言：NewapiUsername 显式置为有效值，且与 org.Code 同值，保证 usage
	// service 直接读 organizations.newapi_username 即可定位 new-api 侧账号。
	require.True(t, store.updated.NewapiUsername.Valid, "NewapiUsername 必须 Valid，否则会落 NULL")
	assert.Equal(t, "test-org", store.updated.NewapiUsername.String)
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
		AdminDisplayName: "组织管理员",
		AdminPassword:    "secret-password",
		ModelID:          "qwen2.5:7b",
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
	svc.SetVersionValidator(fakeVersionValidator{known: map[string]bool{}})
	svc.hashPassword = fakeHash

	_, err := svc.CreateOrganization(context.Background(), auth.Principal{Role: domain.UserRolePlatformAdmin}, OrganizationInput{
		Name:             "测试组织",
		Code:             "test-org",
		AdminUsername:    "org-admin",
		AdminDisplayName: "组织管理员",
		AdminPassword:    "secret-password",
		ModelID:          "qwen2.5:7b",
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
			ModelID:          "qwen2.5:7b",
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
		ModelID:          "qwen2.5:7b",
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
	svc.SetVersionValidator(fakeVersionValidator{known: map[string]bool{}})
	svc.hashPassword = fakeHash

	_, err := svc.CreateOrganization(context.Background(), auth.Principal{Role: domain.UserRolePlatformAdmin}, OrganizationInput{
		Name:             "测试组织",
		Code:             "test-org",
		AdminUsername:    "admin",
		AdminDisplayName: "管理员",
		AdminPassword:    "secret-password",
		ModelID:          "qwen2.5:7b",
	})

	require.ErrorIs(t, err, ErrConflict)
}

// TestCreateOrganizationRejectsUnknownVersionID 验证创建组织时传入不存在的助手版本 id 会被拒绝，
// 保证 allowlist 中只能包含系统已有的版本，防止引用幽灵 id。
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

// TestCreateOrganizationBlocksSaveWithoutVersionValidator 验证版本校验器未装配时拒绝保存组织，
// 防止在没有可用版本目录的情况下写入无法验证的助手版本 allowlist。
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

// TestUpdateOrganizationPreservesModelID 验证更新组织基础资料时，model_id 始终保留数据库中原有值，
// 不再接受外部传入的 model_id 覆盖（model_id 变更由后续独立接口负责）。
func TestUpdateOrganizationPreservesModelID(t *testing.T) {
	store := &organizationStoreStub{}
	org := store.mustSeedOrganization(t, "test-org", "qwen2.5:7b")
	svc := NewOrganizationService(store, &fakeProvisioner{}, mustCipher(t), nil)
	// 不注入版本 allowlist，版本 allowlist 未设置时保留原值。
	svc.SetVersionValidator(fakeVersionValidator{known: map[string]bool{}})

	// 即使传入了 ModelIDSet 和新的 ModelID，结果仍保留原模型。
	result, err := svc.UpdateOrganization(context.Background(), auth.Principal{Role: domain.UserRolePlatformAdmin}, uuidToString(org.ID), OrganizationInput{
		Name:       "测试组织改名",
		ModelID:    "deepseek-r1:14b",
		ModelIDSet: true,
	})

	require.NoError(t, err)
	assert.Equal(t, "测试组织改名", result.Name)
	// model_id 应保持原值，不被外部传入值覆盖。
	assert.Equal(t, "qwen2.5:7b", result.ModelID)
	assert.Equal(t, "qwen2.5:7b", store.updatedProfile.ModelID)
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
		ModelID:                arg.ModelID,
		AssistantVersionIds:    arg.AssistantVersionIds,
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
	s.org.ModelID = arg.ModelID
	s.org.AssistantVersionIds = arg.AssistantVersionIds
	return s.org, nil
}

func (s *organizationStoreStub) SetOrganizationStatus(_ context.Context, arg sqlc.SetOrganizationStatusParams) (sqlc.Organization, error) {
	s.org.Status = arg.Status
	return s.org, nil
}

func (s *organizationStoreStub) mustSeedOrganization(t *testing.T, code string, modelID string) sqlc.Organization {
	t.Helper()
	org := sqlc.Organization{
		ID:      mustUUID(t, "00000000-0000-0000-0000-000000000101"),
		Name:    "测试组织",
		Code:    code,
		Status:  domain.StatusActive,
		ModelID: modelID,
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

// recordingOrgModelValidator 记录 service 传入的模型列表，覆盖 handler/service 入参透传路径。
type recordingOrgModelValidator struct {
	input  []string
	models []string
}

// ValidateModelIDs 返回预设模型并保存原始入参，便于测试断言请求模型没有被丢弃。
func (s *recordingOrgModelValidator) ValidateModelIDs(_ context.Context, input []string) ([]string, error) {
	s.input = append([]string(nil), input...)
	return s.models, nil
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
		AdminDisplayName: "组织管理员",
		AdminPassword:    "secret-password",
		ModelID:          "qwen2.5:7b",
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
	svc.SetVersionValidator(fakeVersionValidator{known: map[string]bool{}})
	svc.hashPassword = fakeHash

	_, _ = svc.CreateOrganization(context.Background(), auth.Principal{Role: domain.UserRolePlatformAdmin}, OrganizationInput{
		Name:             "v102-create-fail",
		Code:             "test-org",
		AdminUsername:    "org-admin",
		AdminDisplayName: "组织管理员",
		AdminPassword:    "secret-password",
		ModelID:          "qwen2.5:7b",
	})
	assert.False(t, prov.deleteUserCalled)
}

// TestCreateOrganizationWithVersionIDs 验证创建组织时传入合法的助手版本 id allowlist，
// 成功后 OrganizationResult.AssistantVersionIDs 应反映传入的有效 id 列表。
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
	org := store.mustSeedOrganization(t, "test-org", "qwen2.5:7b")
	svc := NewOrganizationService(store, &fakeProvisioner{}, mustCipher(t), nil)
	// 注入新版本 id。
	svc.SetVersionValidator(fakeVersionValidator{known: map[string]bool{
		"ver-new": true,
	}})

	result, err := svc.UpdateOrganization(context.Background(), auth.Principal{Role: domain.UserRolePlatformAdmin}, uuidToString(org.ID), OrganizationInput{
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
