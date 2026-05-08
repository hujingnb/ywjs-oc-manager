package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/integrations/newapi"
	"oc-manager/internal/store/sqlc"
)

func TestOrganizationServiceCreateRequiresPlatformAdmin(t *testing.T) {
	svc := NewOrganizationService(&organizationStoreStub{}, &fakeProvisioner{}, mustCipher(t), nil)

	_, err := svc.CreateOrganization(context.Background(), auth.Principal{Role: domain.UserRoleOrgAdmin}, OrganizationInput{Name: "测试组织"})
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("CreateOrganization() error = %v, want ErrForbidden", err)
	}
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
	threshold := int32(20)

	result, err := svc.CreateOrganization(context.Background(), auth.Principal{Role: domain.UserRolePlatformAdmin}, OrganizationInput{
		Name:                   "测试组织",
		ContactName:            "张三",
		CreditWarningThreshold: &threshold,
	})
	if err != nil {
		t.Fatalf("CreateOrganization() error = %v", err)
	}
	if result.Name != "测试组织" || result.CreditWarningThreshold == nil || *result.CreditWarningThreshold != 20 {
		t.Fatalf("organization = %+v", result)
	}
	if prov.createCalls != 1 || prov.bootstrapCalls != 1 {
		t.Fatalf("provisioner calls create=%d bootstrap=%d, want 1/1", prov.createCalls, prov.bootstrapCalls)
	}
	if !strings.HasPrefix(prov.lastCreate.Username, "org-") {
		t.Fatalf("username 应以 org- 前缀: %q", prov.lastCreate.Username)
	}
	if prov.lastCreate.Password == "" {
		t.Fatalf("password 不应为空")
	}
	if !store.updateCalled {
		t.Fatalf("SetOrganizationNewAPIUser 未被调用")
	}
	if store.updated.NewapiUserID.String != "42" {
		t.Fatalf("updated newapi_user_id = %q, want 42", store.updated.NewapiUserID.String)
	}
	if !store.updated.NewapiUserCredentialsCiphertext.Valid {
		t.Fatalf("ciphertext 应被写入")
	}
	// 解密验证三件套被忠实序列化
	cipher := mustCipher(t)
	plain, err := cipher.Decrypt(store.updated.NewapiUserCredentialsCiphertext.String)
	if err != nil {
		t.Fatalf("解密失败: %v", err)
	}
	var creds OrganizationCredentials
	if err := json.Unmarshal(plain, &creds); err != nil {
		t.Fatalf("解析凭据 JSON 失败: %v", err)
	}
	if creds.AccessToken != "access-tok-xyz" {
		t.Fatalf("creds.AccessToken = %q, want access-tok-xyz", creds.AccessToken)
	}
	if creds.Username != prov.lastCreate.Username || creds.Password != prov.lastCreate.Password {
		t.Fatalf("creds 三件套不一致: %+v vs created %+v", creds, prov.lastCreate)
	}
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

	_, err := svc.CreateOrganization(context.Background(), auth.Principal{Role: domain.UserRolePlatformAdmin}, OrganizationInput{Name: "测试组织"})
	if err == nil {
		t.Fatalf("CreateOrganization() 应返回失败")
	}
	if !store.hardDeleted {
		t.Fatalf("失败路径应触发 HardDeleteOrganization 回滚")
	}
}

func TestOrganizationServiceGetRestrictsOrgScope(t *testing.T) {
	store := &organizationStoreStub{org: sqlc.Organization{ID: mustUUID(t, "00000000-0000-0000-0000-000000000101"), Name: "测试组织", Status: domain.StatusActive}}
	svc := NewOrganizationService(store, &fakeProvisioner{}, mustCipher(t), nil)

	_, err := svc.GetOrganization(context.Background(), auth.Principal{Role: domain.UserRoleOrgMember, OrgID: "00000000-0000-0000-0000-000000000999"}, "00000000-0000-0000-0000-000000000101")
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("GetOrganization() error = %v, want ErrForbidden", err)
	}
}

func TestOrganizationServiceSetStatus(t *testing.T) {
	store := &organizationStoreStub{org: sqlc.Organization{ID: mustUUID(t, "00000000-0000-0000-0000-000000000101"), Name: "测试组织", Status: domain.StatusActive}}
	svc := NewOrganizationService(store, &fakeProvisioner{}, mustCipher(t), nil)

	result, err := svc.SetOrganizationStatus(context.Background(), auth.Principal{Role: domain.UserRolePlatformAdmin}, "00000000-0000-0000-0000-000000000101", domain.StatusDisabled)
	if err != nil {
		t.Fatalf("SetOrganizationStatus() error = %v", err)
	}
	if result.Status != domain.StatusDisabled {
		t.Fatalf("status = %q, want disabled", result.Status)
	}
}

func mustCipher(t *testing.T) *auth.Cipher {
	t.Helper()
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	c, err := auth.NewCipher(key)
	if err != nil {
		t.Fatalf("初始化 cipher 失败: %v", err)
	}
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
	org          sqlc.Organization
	created      sqlc.CreateOrganizationParams
	updated      sqlc.SetOrganizationNewAPIUserParams
	updateCalled bool
	hardDeleted  bool
}

func (s *organizationStoreStub) CreateOrganization(_ context.Context, arg sqlc.CreateOrganizationParams) (sqlc.Organization, error) {
	s.created = arg
	id, _ := parseUUID("00000000-0000-0000-0000-000000000101")
	created := sqlc.Organization{
		ID:                     id,
		Name:                   arg.Name,
		Status:                 arg.Status,
		ContactName:            arg.ContactName,
		CreditWarningThreshold: arg.CreditWarningThreshold,
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

func (s *organizationStoreStub) UpdateOrganizationProfile(_ context.Context, arg sqlc.UpdateOrganizationProfileParams) (sqlc.Organization, error) {
	s.org.Name = arg.Name
	s.org.ContactName = arg.ContactName
	return s.org, nil
}

func (s *organizationStoreStub) SetOrganizationStatus(_ context.Context, arg sqlc.SetOrganizationStatusParams) (sqlc.Organization, error) {
	s.org.Status = arg.Status
	return s.org, nil
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

	_, err := svc.CreateOrganization(context.Background(), auth.Principal{Role: domain.UserRolePlatformAdmin}, OrganizationInput{Name: "v102-orphan-test"})
	if err == nil {
		t.Fatal("期望 CreateOrganization 失败")
	}
	if !prov.deleteUserCalled {
		t.Errorf("期望调用 DeleteUser 清理孤儿")
	}
	if prov.deleteUserUserID != 42 {
		t.Errorf("DeleteUser userID=%d，期望 42", prov.deleteUserUserID)
	}
	if len(auditor.events) == 0 {
		t.Errorf("期望至少 1 条 audit 事件，实际 %d", len(auditor.events))
	}
}

// TestCreateOrganization_CreateUserFailureNoDeleteUser 校验 CreateUser 失败时不调
// DeleteUser（此时无 new-api 孤儿 user 需要清理）。
func TestCreateOrganization_CreateUserFailureNoDeleteUser(t *testing.T) {
	auditor := &fakeFailAuditor{}
	prov := &fakeProvisioner{
		createError: errors.New("create 500"),
	}
	svc := NewOrganizationService(&organizationStoreStub{}, prov, mustCipher(t), auditor)

	_, _ = svc.CreateOrganization(context.Background(), auth.Principal{Role: domain.UserRolePlatformAdmin}, OrganizationInput{Name: "v102-create-fail"})
	if prov.deleteUserCalled {
		t.Errorf("CreateUser 失败时不应调 DeleteUser（无孤儿）")
	}
}
