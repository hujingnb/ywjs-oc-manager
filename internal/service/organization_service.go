package service

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/integrations/newapi"
	"oc-manager/internal/store/sqlc"
)

// OrganizationStore 抽象组织管理所需的数据访问能力。
type OrganizationStore interface {
	CreateOrganization(ctx context.Context, arg sqlc.CreateOrganizationParams) (sqlc.Organization, error)
	SetOrganizationNewAPIUser(ctx context.Context, arg sqlc.SetOrganizationNewAPIUserParams) (sqlc.Organization, error)
	HardDeleteOrganization(ctx context.Context, id pgtype.UUID) error
	GetOrganization(ctx context.Context, id pgtype.UUID) (sqlc.Organization, error)
	ListOrganizations(ctx context.Context, arg sqlc.ListOrganizationsParams) ([]sqlc.Organization, error)
	UpdateOrganizationProfile(ctx context.Context, arg sqlc.UpdateOrganizationProfileParams) (sqlc.Organization, error)
	SetOrganizationStatus(ctx context.Context, arg sqlc.SetOrganizationStatusParams) (sqlc.Organization, error)
}

// NewAPIUserProvisioner 抽象组织创建链路所需的 new-api 调用集合，便于测试中替换为 fake。
type NewAPIUserProvisioner interface {
	CreateUser(ctx context.Context, input newapi.CreateUserInput) (newapi.User, error)
	BootstrapUserAccessToken(ctx context.Context, username, password string) (string, error)
}

// OrganizationCredentials 是 organizations.newapi_user_credentials_ciphertext 解密后的明文。
//
// 三件套的角色：
//   - Username / Password：login 用的回退凭据，万一 access_token 被在 new-api UI 重置，
//     manager 还能 re-login 重新生成；
//   - AccessToken：日常 user-scoped 调用走的 Bearer，避免每次都 login。
type OrganizationCredentials struct {
	Username    string `json:"username"`
	Password    string `json:"password"`
	AccessToken string `json:"access_token"`
}

type OrganizationService struct {
	store        OrganizationStore
	provisioner  NewAPIUserProvisioner
	cipher       *auth.Cipher
	usernamePool string // 组织 user 的 username 前缀，便于改写本地与生产分布
}

// NewOrganizationService 创建 organization service。
//
// provisioner / cipher 在生产环境必须非 nil；任意一个 nil 会让 CreateOrganization
// 直接报错（不再像旧版本那样静默落库 newapi_user_id 空字符串）。
func NewOrganizationService(store OrganizationStore, provisioner NewAPIUserProvisioner, cipher *auth.Cipher) *OrganizationService {
	return &OrganizationService{store: store, provisioner: provisioner, cipher: cipher, usernamePool: "org-"}
}

type OrganizationInput struct {
	Name                   string
	ContactName            string
	ContactPhone           string
	Remark                 string
	CreditWarningThreshold *int32
}

type OrganizationResult struct {
	ID                     string `json:"id"`
	Name                   string `json:"name"`
	Status                 string `json:"status"`
	ContactName            string `json:"contact_name,omitempty"`
	ContactPhone           string `json:"contact_phone,omitempty"`
	Remark                 string `json:"remark,omitempty"`
	NewAPIUserID           string `json:"newapi_user_id,omitempty"`
	CreditWarningThreshold *int32 `json:"credit_warning_threshold,omitempty"`
}

// CreateOrganization 创建组织：先 INSERT manager 端记录，再串联调 new-api 创业务 user 并落凭据密文。
//
// 失败路径：任何步骤报错（new-api 调用 / 加密 / DB 更新）都先 HardDeleteOrganization
// 把 manager 端孤儿记录回滚，再返回原错误。new-api 端的孤儿 user 不在此处清理，
// 留给后续后台对账（详见 spec §10）。
func (s *OrganizationService) CreateOrganization(ctx context.Context, principal auth.Principal, input OrganizationInput) (OrganizationResult, error) {
	if principal.Role != domain.UserRolePlatformAdmin {
		return OrganizationResult{}, ErrForbidden
	}
	if s.provisioner == nil || s.cipher == nil {
		return OrganizationResult{}, fmt.Errorf("organization service 未装配 newapi provisioner / cipher，无法创建组织")
	}

	org, err := s.store.CreateOrganization(ctx, sqlc.CreateOrganizationParams{
		Name:                   input.Name,
		Status:                 domain.StatusActive,
		ContactName:            textValue(input.ContactName),
		ContactPhone:           textValue(input.ContactPhone),
		Remark:                 textValue(input.Remark),
		CreditWarningThreshold: int4Ptr(input.CreditWarningThreshold),
	})
	if err != nil {
		return OrganizationResult{}, fmt.Errorf("创建组织失败: %w", err)
	}

	// 失败时回滚刚刚 INSERT 的 manager 行；rollback 自身失败只记入返回错误，不掩盖原因。
	commit, err := s.provisionNewAPIUser(ctx, &org)
	if err != nil {
		if rollbackErr := s.store.HardDeleteOrganization(ctx, org.ID); rollbackErr != nil {
			return OrganizationResult{}, fmt.Errorf("%w；回滚组织行失败: %v", err, rollbackErr)
		}
		return OrganizationResult{}, err
	}
	org = commit
	return toOrganizationResult(org), nil
}

// provisionNewAPIUser 在 new-api 创建对应业务 user，登录拿 access_token，加密落库。
//
// 流程：
//  1. 生成 username（"org-" + org.id 前 8 字节）与 32 字节随机 password；
//  2. CreateUser → 回查 user_id；
//  3. BootstrapUserAccessToken → 拿 access_token；
//  4. 加密 JSON {username, password, access_token}；
//  5. SetOrganizationNewAPIUser 写回 newapi_user_id + 密文。
func (s *OrganizationService) provisionNewAPIUser(ctx context.Context, org *sqlc.Organization) (sqlc.Organization, error) {
	username := s.deriveUsername(org.ID)
	password, err := generateUserPassword()
	if err != nil {
		return sqlc.Organization{}, fmt.Errorf("生成 new-api 密码失败: %w", err)
	}

	user, err := s.provisioner.CreateUser(ctx, newapi.CreateUserInput{
		Username:    username,
		Password:    password,
		DisplayName: org.Name,
	})
	if err != nil {
		return sqlc.Organization{}, fmt.Errorf("调用 new-api 创建用户失败: %w", err)
	}
	if user.ID == 0 {
		return sqlc.Organization{}, fmt.Errorf("new-api CreateUser 返回 user_id=0")
	}

	accessToken, err := s.provisioner.BootstrapUserAccessToken(ctx, username, password)
	if err != nil {
		return sqlc.Organization{}, fmt.Errorf("调用 new-api 登录拿 access_token 失败: %w", err)
	}

	credPayload, err := json.Marshal(OrganizationCredentials{
		Username:    username,
		Password:    password,
		AccessToken: accessToken,
	})
	if err != nil {
		return sqlc.Organization{}, fmt.Errorf("序列化 new-api 凭据失败: %w", err)
	}
	ciphertext, err := s.cipher.Encrypt(credPayload)
	if err != nil {
		return sqlc.Organization{}, fmt.Errorf("加密 new-api 凭据失败: %w", err)
	}

	updated, err := s.store.SetOrganizationNewAPIUser(ctx, sqlc.SetOrganizationNewAPIUserParams{
		ID:                              org.ID,
		NewapiUserID:                    pgtype.Text{String: strconv.FormatInt(user.ID, 10), Valid: true},
		NewapiUserCredentialsCiphertext: pgtype.Text{String: ciphertext, Valid: true},
	})
	if err != nil {
		return sqlc.Organization{}, fmt.Errorf("写入 new-api user 信息失败: %w", err)
	}
	return updated, nil
}

// deriveUsername 基于组织 uuid 生成稳定 username（"org-" + uuid.String() 前 8 位）。
//
// 取前 8 位是为了：
//   - 在 new-api UI 列表里仍然可读；
//   - 避免完整 UUID 触发 new-api 对 username 长度的校验；
//   - 同一 org.id 重复调用结果稳定（虽然本流程只在 INSERT 后执行一次）。
func (s *OrganizationService) deriveUsername(orgID pgtype.UUID) string {
	if !orgID.Valid {
		// 极端兜底：org.id 应当永远 Valid，但走到这里也不要 panic。
		return s.usernamePool + strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	}
	full := uuid.UUID(orgID.Bytes).String()
	return s.usernamePool + strings.ReplaceAll(full[:8], "-", "")
}

// generateUserPassword 生成 16 字符随机密码。
//
// 长度选 16：
//   - new-api `model.User.Password` 字段绑了 `validate:"max=20"` 等校验，超过会被拒；
//   - 16 字符 base32 等效熵 ≈ 80 bits，对内部 server-to-server 凭据足够；
//   - 用 base32 不用 hex 是为了输出无 +/=、不需要 URL 编码的字符。
func generateUserPassword() (string, error) {
	raw := make([]byte, 10)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return strings.TrimRight(base32.StdEncoding.EncodeToString(raw), "="), nil
}

// ListOrganizations 列出未删除组织；第一版仅平台管理员可访问全量组织。
func (s *OrganizationService) ListOrganizations(ctx context.Context, principal auth.Principal, limit, offset int32) ([]OrganizationResult, error) {
	if principal.Role != domain.UserRolePlatformAdmin {
		return nil, ErrForbidden
	}
	orgs, err := s.store.ListOrganizations(ctx, sqlc.ListOrganizationsParams{Limit: limit, Offset: offset})
	if err != nil {
		return nil, fmt.Errorf("查询组织列表失败: %w", err)
	}
	return toOrganizationResults(orgs), nil
}

// GetOrganization 根据角色限制组织访问范围。
func (s *OrganizationService) GetOrganization(ctx context.Context, principal auth.Principal, orgID string) (OrganizationResult, error) {
	if principal.Role != domain.UserRolePlatformAdmin && principal.OrgID != orgID {
		return OrganizationResult{}, ErrForbidden
	}
	id, err := parseUUID(orgID)
	if err != nil {
		return OrganizationResult{}, ErrNotFound
	}
	org, err := s.store.GetOrganization(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return OrganizationResult{}, ErrNotFound
	}
	if err != nil {
		return OrganizationResult{}, fmt.Errorf("查询组织失败: %w", err)
	}
	return toOrganizationResult(org), nil
}

// UpdateOrganization 更新组织基础资料；生命周期状态必须走 enable/disable。
func (s *OrganizationService) UpdateOrganization(ctx context.Context, principal auth.Principal, orgID string, input OrganizationInput) (OrganizationResult, error) {
	if principal.Role != domain.UserRolePlatformAdmin {
		return OrganizationResult{}, ErrForbidden
	}
	id, err := parseUUID(orgID)
	if err != nil {
		return OrganizationResult{}, ErrNotFound
	}
	org, err := s.store.UpdateOrganizationProfile(ctx, sqlc.UpdateOrganizationProfileParams{
		ID:                     id,
		Name:                   input.Name,
		ContactName:            textValue(input.ContactName),
		ContactPhone:           textValue(input.ContactPhone),
		Remark:                 textValue(input.Remark),
		CreditWarningThreshold: int4Ptr(input.CreditWarningThreshold),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return OrganizationResult{}, ErrNotFound
	}
	if err != nil {
		return OrganizationResult{}, fmt.Errorf("更新组织失败: %w", err)
	}
	return toOrganizationResult(org), nil
}

// SetOrganizationStatus 启用或禁用组织；软删除后续由删除流程单独处理。
func (s *OrganizationService) SetOrganizationStatus(ctx context.Context, principal auth.Principal, orgID, status string) (OrganizationResult, error) {
	if principal.Role != domain.UserRolePlatformAdmin {
		return OrganizationResult{}, ErrForbidden
	}
	if status != domain.StatusActive && status != domain.StatusDisabled {
		return OrganizationResult{}, fmt.Errorf("非法组织状态: %s", status)
	}
	id, err := parseUUID(orgID)
	if err != nil {
		return OrganizationResult{}, ErrNotFound
	}
	org, err := s.store.SetOrganizationStatus(ctx, sqlc.SetOrganizationStatusParams{ID: id, Status: status})
	if errors.Is(err, pgx.ErrNoRows) {
		return OrganizationResult{}, ErrNotFound
	}
	if err != nil {
		return OrganizationResult{}, fmt.Errorf("更新组织状态失败: %w", err)
	}
	return toOrganizationResult(org), nil
}

// DecryptOrganizationCredentials 把组织的 newapi_user_credentials_ciphertext 还原为明文凭据。
//
// 给 worker handler / 其它 service 用：拿到三件套后构造 newapi.UserScopedClient 调 user-scoped
// 接口（创建 token / 拿完整 sk- / 改 status）。cipher 为 nil 直接返回错误，避免静默退化。
func DecryptOrganizationCredentials(org sqlc.Organization, cipher *auth.Cipher) (OrganizationCredentials, error) {
	if cipher == nil {
		return OrganizationCredentials{}, fmt.Errorf("cipher 未配置，无法解密 new-api 凭据")
	}
	if !org.NewapiUserCredentialsCiphertext.Valid || org.NewapiUserCredentialsCiphertext.String == "" {
		return OrganizationCredentials{}, fmt.Errorf("组织 %s 未持有 new-api 凭据密文", uuidToString(org.ID))
	}
	plain, err := cipher.Decrypt(org.NewapiUserCredentialsCiphertext.String)
	if err != nil {
		return OrganizationCredentials{}, fmt.Errorf("解密 new-api 凭据失败: %w", err)
	}
	var creds OrganizationCredentials
	if err := json.Unmarshal(plain, &creds); err != nil {
		return OrganizationCredentials{}, fmt.Errorf("解析 new-api 凭据失败: %w", err)
	}
	if creds.Username == "" || creds.Password == "" || creds.AccessToken == "" {
		return OrganizationCredentials{}, fmt.Errorf("组织 %s 的 new-api 凭据三件套不完整", uuidToString(org.ID))
	}
	return creds, nil
}

func toOrganizationResults(orgs []sqlc.Organization) []OrganizationResult {
	results := make([]OrganizationResult, 0, len(orgs))
	for _, org := range orgs {
		results = append(results, toOrganizationResult(org))
	}
	return results
}

func toOrganizationResult(org sqlc.Organization) OrganizationResult {
	return OrganizationResult{
		ID:                     uuidToString(org.ID),
		Name:                   org.Name,
		Status:                 org.Status,
		ContactName:            textString(org.ContactName),
		ContactPhone:           textString(org.ContactPhone),
		Remark:                 textString(org.Remark),
		NewAPIUserID:           textString(org.NewapiUserID),
		CreditWarningThreshold: int4Pointer(org.CreditWarningThreshold),
	}
}

func textValue(value string) pgtype.Text {
	return pgtype.Text{String: value, Valid: value != ""}
}

func textString(value pgtype.Text) string {
	if !value.Valid {
		return ""
	}
	return value.String
}

func int4Ptr(value *int32) pgtype.Int4 {
	if value == nil {
		return pgtype.Int4{}
	}
	return pgtype.Int4{Int32: *value, Valid: true}
}

func int4Pointer(value pgtype.Int4) *int32 {
	if !value.Valid {
		return nil
	}
	result := value.Int32
	return &result
}
