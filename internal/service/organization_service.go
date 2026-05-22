package service

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/integrations/newapi"
	"oc-manager/internal/store/sqlc"
)

var organizationCodePattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{1,30}[a-z0-9])$`)

// NewAPIFailureContext 描述 OrganizationService 内 new-api 调用失败的上下文，
// 供注入的 NewAPIFailureAuditor 写 audit_logs。
type NewAPIFailureContext struct {
	// ActorID 是触发外部调用的 manager 用户 ID，用于审计归因。
	ActorID string
	// ActorRole 是触发时的角色快照，避免后续角色变化影响审计解释。
	ActorRole string
	// OrgID 是失败影响到的组织 ID；组织创建早期失败时使用刚插入的 manager 行。
	OrgID string
	// Endpoint 是失败的 new-api 端点或内部步骤名称。
	Endpoint string
	// Err 是原始错误，审计辅助会做安全化处理后写入 metadata。
	Err error
}

// NewAPIFailureAuditor 抽象 new-api 失败审计写入能力，避免 service 直接依赖 audit 包
// （audit 包反向依赖 service.AuditEvent，会形成导入环）。
// *audit.NewAPIAuditHelper 通过适配器满足此接口。
type NewAPIFailureAuditor interface {
	RecordNewAPIFailure(ctx context.Context, fc NewAPIFailureContext)
}

// OrganizationVersionValidator 抽象「校验一组助手版本 id 都存在」的能力。
type OrganizationVersionValidator interface {
	ValidateAssistantVersionIDs(ctx context.Context, ids []string) ([]string, error)
}

// OrganizationStore 抽象组织管理所需的数据访问能力。
type OrganizationStore interface {
	CreateOrganization(ctx context.Context, arg sqlc.CreateOrganizationParams) (sqlc.Organization, error)
	SetOrganizationNewAPIUser(ctx context.Context, arg sqlc.SetOrganizationNewAPIUserParams) (sqlc.Organization, error)
	CreateUser(ctx context.Context, arg sqlc.CreateUserParams) (sqlc.User, error)
	HardDeleteOrganization(ctx context.Context, id pgtype.UUID) error
	GetOrganization(ctx context.Context, id pgtype.UUID) (sqlc.Organization, error)
	ListOrganizations(ctx context.Context, arg sqlc.ListOrganizationsParams) ([]sqlc.Organization, error)
	GetOrgAdminByOrg(ctx context.Context, id pgtype.UUID) (sqlc.User, error)
	UpdateOrganizationProfile(ctx context.Context, arg sqlc.UpdateOrganizationProfileParams) (sqlc.Organization, error)
	SetOrganizationStatus(ctx context.Context, arg sqlc.SetOrganizationStatusParams) (sqlc.Organization, error)
}

// NewAPIUserProvisioner 抽象组织创建链路所需的 new-api 调用集合，便于测试中替换为 fake。
type NewAPIUserProvisioner interface {
	CreateUser(ctx context.Context, input newapi.CreateUserInput) (newapi.User, error)
	BootstrapUserAccessToken(ctx context.Context, username, password string) (string, error)
	DeleteUser(ctx context.Context, userID int64) error // OOS-1 孤儿清理：CreateOrganization 失败时 best-effort 调用
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

// OrganizationService 负责组织生命周期、new-api 用户 provisioning 和组织管理员初始化。
// 创建组织跨 manager DB 与 new-api 两个系统，失败时通过 best-effort 清理和审计降低孤儿资源风险。
type OrganizationService struct {
	// store 封装 manager 端组织和管理员账号的持久化操作。
	store OrganizationStore
	// provisioner 封装 new-api 用户创建、token bootstrap 和失败清理。
	provisioner NewAPIUserProvisioner
	// cipher 加密 organizations.newapi_user_credentials_ciphertext 中的 new-api 明文凭据。
	cipher *auth.Cipher
	// failAuditor 记录 new-api 失败；nil 时跳过审计，主要用于单元测试或最小装配。
	failAuditor NewAPIFailureAuditor // 新增；nil 时跳过 new-api 失败审计写入
	// versionValidator 校验一组助手版本 id 都存在且未删除；未配置时禁止保存版本 allowlist。
	versionValidator OrganizationVersionValidator
	// hashPassword 仅用于创建组织管理员，测试中可替换为快 hash。
	hashPassword PasswordHasher
}

// NewOrganizationService 构造组织服务。
// provisioner / cipher 必填；failAuditor 可为 nil（生产装配应注入满足 NewAPIFailureAuditor 的实现）。
func NewOrganizationService(store OrganizationStore, provisioner NewAPIUserProvisioner, cipher *auth.Cipher, failAuditor NewAPIFailureAuditor) *OrganizationService {
	return &OrganizationService{
		store:       store,
		provisioner: provisioner,
		cipher:      cipher,
		failAuditor: failAuditor,
		hashPassword: func(password string) (string, error) {
			return auth.HashPassword(password, auth.DefaultPasswordParams)
		},
	}
}

// SetVersionValidator 注入助手版本 allowlist 校验器。
func (s *OrganizationService) SetVersionValidator(v OrganizationVersionValidator) {
	s.versionValidator = v
}

// OrganizationInput 是组织创建和更新的统一入参。
// Admin* 字段仅 CreateOrganization 使用，更新组织资料时会被忽略。
type OrganizationInput struct {
	// Name 是组织展示名。
	Name string
	// Code 是组织登录标识，创建后不可修改；仅允许小写字母、数字和短横线。
	Code string
	// ContactName 是业务联系人姓名。
	ContactName string
	// ContactPhone 是业务联系人电话。
	ContactPhone string
	// Remark 是平台管理员维护的内部备注。
	Remark string
	// CreditWarningThreshold 是组织余额预警阈值；nil 会写入 NULL，表示不设置预警阈值。
	CreditWarningThreshold *int32
	// AssistantVersionIDs 是该组织可用的助手版本 id 列表（allowlist）。
	AssistantVersionIDs []string
	// AssistantVersionIDsSet 标记更新请求是否显式传入了 allowlist。
	AssistantVersionIDsSet bool
	// AdminUsername 是创建组织时初始化的 org_admin 账号名。
	AdminUsername string
	// AdminDisplayName 是初始化 org_admin 的显示名。
	AdminDisplayName string
	// AdminPassword 是初始化 org_admin 的明文密码，写库前会 hash。
	AdminPassword string
}

// OrganizationResult 是组织对外响应视图，包含必要的 new-api 绑定状态。
type OrganizationResult struct {
	// ID 是 manager 组织 UUID。
	ID string `json:"id"`
	// Name 是组织展示名。
	Name string `json:"name"`
	// Code 是组织登录标识，用于组织用户登录时定位租户。
	Code string `json:"code"`
	// Status 是组织状态，active / disabled 决定成员是否可登录。
	Status string `json:"status"`
	// ContactName 是业务联系人姓名。
	ContactName string `json:"contact_name,omitempty"`
	// ContactPhone 是业务联系人电话。
	ContactPhone string `json:"contact_phone,omitempty"`
	// Remark 是平台管理员维护的内部备注。
	Remark string `json:"remark,omitempty"`
	// NewAPIUserID 是组织在 new-api 侧的用户 ID，缺失时充值和用量接口不可用。
	NewAPIUserID string `json:"newapi_user_id,omitempty"`
	// CreditWarningThreshold 是组织余额预警阈值。
	CreditWarningThreshold *int32 `json:"credit_warning_threshold,omitempty"`
	// AdminUsername 是组织首个可用管理员账号名，用于平台管理员复制登录信息。
	AdminUsername string `json:"admin_username,omitempty"`
	// AssistantVersionIDs 是该组织可用的助手版本 id 列表（allowlist）。
	AssistantVersionIDs []string `json:"assistant_version_ids"`
}

// CreateOrganization 创建组织：先 INSERT manager 端记录，再串联调 new-api 创业务 user 并落凭据密文。
//
// 失败路径：任何步骤报错时——
//   - 已创建的 new-api user 调 DeleteUser best-effort 清理（OOS-1）；
//   - 原失败原因 + 清理失败（如有）通过 auditHelper 落 audit_logs（OOS-3）；
//   - manager 端组织行 HardDeleteOrganization 回滚。
func (s *OrganizationService) CreateOrganization(ctx context.Context, principal auth.Principal, input OrganizationInput) (OrganizationResult, error) {
	// 组织创建会在 new-api 侧创建真实计费主体，只允许平台管理员触发。
	if principal.Role != domain.UserRolePlatformAdmin {
		return OrganizationResult{}, ErrForbidden
	}
	if s.provisioner == nil || s.cipher == nil {
		return OrganizationResult{}, fmt.Errorf("organization service 未装配 newapi provisioner / cipher，无法创建组织")
	}
	if input.AdminUsername == "" || input.AdminDisplayName == "" || input.AdminPassword == "" {
		return OrganizationResult{}, fmt.Errorf("%w: 管理员用户名、显示名和密码不能为空", ErrMemberCreateInvalid)
	}
	code, err := normalizeOrganizationCode(input.Code)
	if err != nil {
		return OrganizationResult{}, err
	}
	// 校验助手版本 allowlist 中的每个 id 都存在且未删除。
	// versionValidator 未装配时直接拒绝，避免写入未经版本目录确认的 id。
	if s.versionValidator == nil {
		return OrganizationResult{}, fmt.Errorf("版本校验器未配置，无法保存组织可用版本")
	}
	cleanVersionIDs, err := s.versionValidator.ValidateAssistantVersionIDs(ctx, input.AssistantVersionIDs)
	if err != nil {
		return OrganizationResult{}, err
	}
	versionIDsJSON, err := json.Marshal(cleanVersionIDs)
	if err != nil {
		return OrganizationResult{}, fmt.Errorf("序列化组织可用版本失败: %w", err)
	}
	adminPasswordHash, err := s.hashPassword(input.AdminPassword)
	if err != nil {
		return OrganizationResult{}, fmt.Errorf("生成管理员密码 hash 失败: %w", err)
	}

	org, err := s.store.CreateOrganization(ctx, sqlc.CreateOrganizationParams{
		Name:                   input.Name,
		Code:                   code,
		Status:                 domain.StatusActive,
		ContactName:            textValue(input.ContactName),
		ContactPhone:           textValue(input.ContactPhone),
		Remark:                 textValue(input.Remark),
		CreditWarningThreshold: int4Ptr(input.CreditWarningThreshold),
		AssistantVersionIds:    versionIDsJSON,
	})
	if err != nil {
		if isUniqueViolation(err) {
			return OrganizationResult{}, fmt.Errorf("%w: 组织名称或组织标识已存在", ErrConflict)
		}
		return OrganizationResult{}, fmt.Errorf("创建组织失败: %w", err)
	}

	// 失败时回滚刚刚 INSERT 的 manager 行；rollback 自身失败只记入返回错误，不掩盖原因。
	commit, createdUserID, err := s.provisionNewAPIUser(ctx, &org)
	if err != nil {
		orgIDStr := uuidToString(org.ID)
		// OOS-1：best-effort 调 DeleteUser 清理 new-api 孤儿 user。
		// 使用独立的短超时 ctx，避免原 ctx 取消导致清理也被中止。
		// slog.WarnContext 仍传原 ctx，让 trace_id 自动注入（A-4 SetRequestIDExtractor 已配）。
		if createdUserID != nil {
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cleanupCancel()
			if delErr := s.provisioner.DeleteUser(cleanupCtx, *createdUserID); delErr != nil {
				slog.WarnContext(ctx, "best-effort 清理 newapi user 失败",
					"newapi_user_id", *createdUserID,
					"error", delErr,
				)
				// OOS-3：DeleteUser 自身失败也写审计
				if s.failAuditor != nil {
					s.failAuditor.RecordNewAPIFailure(ctx, NewAPIFailureContext{
						ActorID:   principal.UserID,
						ActorRole: principal.Role,
						OrgID:     orgIDStr,
						Endpoint:  fmt.Sprintf("DELETE /api/user/%d", *createdUserID),
						Err:       delErr,
					})
				}
			}
		}
		// OOS-3：原失败原因写审计（区别于 DeleteUser 失败）
		if s.failAuditor != nil {
			s.failAuditor.RecordNewAPIFailure(ctx, NewAPIFailureContext{
				ActorID:   principal.UserID,
				ActorRole: principal.Role,
				OrgID:     orgIDStr,
				Endpoint:  "CreateOrganization",
				Err:       err,
			})
		}
		// manager 端回滚保留
		if rollbackErr := s.store.HardDeleteOrganization(ctx, org.ID); rollbackErr != nil {
			return OrganizationResult{}, fmt.Errorf("%w；回滚组织行失败: %v", err, rollbackErr)
		}
		return OrganizationResult{}, err
	}
	org = commit
	if _, err := s.store.CreateUser(ctx, sqlc.CreateUserParams{
		OrgID:        org.ID,
		Username:     input.AdminUsername,
		PasswordHash: adminPasswordHash,
		DisplayName:  input.AdminDisplayName,
		Role:         domain.UserRoleOrgAdmin,
		Status:       domain.StatusActive,
	}); err != nil {
		orgIDStr := uuidToString(org.ID)
		if createdUserID != nil {
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cleanupCancel()
			if delErr := s.provisioner.DeleteUser(cleanupCtx, *createdUserID); delErr != nil {
				slog.WarnContext(ctx, "best-effort 清理 newapi user 失败",
					"newapi_user_id", *createdUserID,
					"error", delErr,
				)
				if s.failAuditor != nil {
					s.failAuditor.RecordNewAPIFailure(ctx, NewAPIFailureContext{
						ActorID:   principal.UserID,
						ActorRole: principal.Role,
						OrgID:     orgIDStr,
						Endpoint:  fmt.Sprintf("DELETE /api/user/%d", *createdUserID),
						Err:       delErr,
					})
				}
			}
		}
		wrappedErr := fmt.Errorf("创建组织管理员失败: %w", err)
		if s.failAuditor != nil {
			s.failAuditor.RecordNewAPIFailure(ctx, NewAPIFailureContext{
				ActorID:   principal.UserID,
				ActorRole: principal.Role,
				OrgID:     orgIDStr,
				Endpoint:  "CreateOrganizationAdmin",
				Err:       wrappedErr,
			})
		}
		if rollbackErr := s.store.HardDeleteOrganization(ctx, org.ID); rollbackErr != nil {
			return OrganizationResult{}, fmt.Errorf("%w；回滚组织行失败: %v", wrappedErr, rollbackErr)
		}
		return OrganizationResult{}, wrappedErr
	}
	result := toOrganizationResult(org)
	result.AdminUsername = input.AdminUsername
	return result, nil
}

// normalizeOrganizationCode 统一组织标识格式，避免大小写或空白导致同一标识多种写法。
func normalizeOrganizationCode(value string) (string, error) {
	code := strings.ToLower(strings.TrimSpace(value))
	if !organizationCodePattern.MatchString(code) {
		return "", fmt.Errorf("%w: 组织标识必须为 3-32 位小写字母、数字或短横线，且不能以短横线开头或结尾", ErrMemberCreateInvalid)
	}
	return code, nil
}

// isUniqueViolation 判断底层 PostgreSQL 错误是否为唯一约束冲突。
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// provisionNewAPIUser 在 new-api 创建对应业务 user，登录拿 access_token，加密落库。
//
// 返回值第二个 *int64 是"已创建的 new-api user_id"——
//   - CreateUser 之前任意失败 → nil（无孤儿）
//   - CreateUser 之后任意失败 → 非 nil（调用方负责 best-effort 调 DeleteUser 清理孤儿）
func (s *OrganizationService) provisionNewAPIUser(ctx context.Context, org *sqlc.Organization) (sqlc.Organization, *int64, error) {
	username := org.Code
	password, err := generateUserPassword()
	if err != nil {
		return sqlc.Organization{}, nil, fmt.Errorf("生成 new-api 密码失败: %w", err)
	}

	user, err := s.provisioner.CreateUser(ctx, newapi.CreateUserInput{
		Username:    username,
		Password:    password,
		DisplayName: org.Name,
	})
	if err != nil {
		return sqlc.Organization{}, nil, fmt.Errorf("调用 new-api 创建用户失败: %w", err)
	}
	if user.ID == 0 {
		return sqlc.Organization{}, nil, fmt.Errorf("new-api CreateUser 返回 user_id=0")
	}
	createdUserID := user.ID

	accessToken, err := s.provisioner.BootstrapUserAccessToken(ctx, username, password)
	if err != nil {
		return sqlc.Organization{}, &createdUserID, fmt.Errorf("调用 new-api 登录拿 access_token 失败: %w", err)
	}

	credPayload, err := json.Marshal(OrganizationCredentials{
		Username:    username,
		Password:    password,
		AccessToken: accessToken,
	})
	if err != nil {
		return sqlc.Organization{}, &createdUserID, fmt.Errorf("序列化 new-api 凭据失败: %w", err)
	}
	ciphertext, err := s.cipher.Encrypt(credPayload)
	if err != nil {
		return sqlc.Organization{}, &createdUserID, fmt.Errorf("加密 new-api 凭据失败: %w", err)
	}

	updated, err := s.store.SetOrganizationNewAPIUser(ctx, sqlc.SetOrganizationNewAPIUserParams{
		ID:                              org.ID,
		NewapiUserID:                    pgtype.Text{String: strconv.FormatInt(user.ID, 10), Valid: true},
		NewapiUserCredentialsCiphertext: pgtype.Text{String: ciphertext, Valid: true},
		// 同步落 new-api 侧 username（当前等于 org.Code），供 usage service 直接读
		// organizations.newapi_username 定位 new-api 账号，避免运行时反查或解密凭据。
		NewapiUsername: pgtype.Text{String: username, Valid: true},
	})
	if err != nil {
		return sqlc.Organization{}, &createdUserID, fmt.Errorf("写入 new-api user 信息失败: %w", err)
	}
	return updated, &createdUserID, nil
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
	return s.toOrganizationResultsWithAdminUsernames(ctx, orgs), nil
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
	return s.toOrganizationResultWithAdminUsername(ctx, org), nil
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
	current, err := s.store.GetOrganization(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return OrganizationResult{}, ErrNotFound
	}
	if err != nil {
		return OrganizationResult{}, fmt.Errorf("查询组织失败: %w", err)
	}
	// 处理助手版本 allowlist：显式传入时校验并更新，否则保留原有值。
	var versionIDsJSON []byte
	if input.AssistantVersionIDsSet {
		if s.versionValidator == nil {
			return OrganizationResult{}, fmt.Errorf("版本校验器未配置，无法保存组织可用版本")
		}
		cleanVersionIDs, err := s.versionValidator.ValidateAssistantVersionIDs(ctx, input.AssistantVersionIDs)
		if err != nil {
			return OrganizationResult{}, err
		}
		versionIDsJSON, err = json.Marshal(cleanVersionIDs)
		if err != nil {
			return OrganizationResult{}, fmt.Errorf("序列化组织可用版本失败: %w", err)
		}
	} else {
		// 未显式传入时原样保留数据库中已有的版本 allowlist。
		versionIDsJSON = current.AssistantVersionIds
	}
	org, err := s.store.UpdateOrganizationProfile(ctx, sqlc.UpdateOrganizationProfileParams{
		ID:                     id,
		Name:                   input.Name,
		ContactName:            textValue(input.ContactName),
		ContactPhone:           textValue(input.ContactPhone),
		Remark:                 textValue(input.Remark),
		CreditWarningThreshold: int4Ptr(input.CreditWarningThreshold),
		AssistantVersionIds:    versionIDsJSON,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return OrganizationResult{}, ErrNotFound
	}
	if err != nil {
		return OrganizationResult{}, fmt.Errorf("更新组织失败: %w", err)
	}
	return s.toOrganizationResultWithAdminUsername(ctx, org), nil
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
	return s.toOrganizationResultWithAdminUsername(ctx, org), nil
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

// toOrganizationResultsWithAdminUsernames 在组织基础视图上补充管理员用户名。
// 密码明文只在创建请求里短暂出现，数据库只保存 hash，因此响应不会也不能包含管理员密码。
func (s *OrganizationService) toOrganizationResultsWithAdminUsernames(ctx context.Context, orgs []sqlc.Organization) []OrganizationResult {
	results := toOrganizationResults(orgs)
	for idx, org := range orgs {
		results[idx].AdminUsername = s.getOrgAdminUsername(ctx, org.ID)
	}
	return results
}

// toOrganizationResultWithAdminUsername 为单组织响应补充管理员用户名；查不到管理员时保留空值，
// 避免一个历史异常组织阻断组织资料、状态切换等主流程。
func (s *OrganizationService) toOrganizationResultWithAdminUsername(ctx context.Context, org sqlc.Organization) OrganizationResult {
	result := toOrganizationResult(org)
	result.AdminUsername = s.getOrgAdminUsername(ctx, org.ID)
	return result
}

// getOrgAdminUsername 查询组织下最早创建且未下线的 org_admin。
// pgx.ErrNoRows 表示组织尚无可用管理员，返回空字符串即可由前端显示提示。
func (s *OrganizationService) getOrgAdminUsername(ctx context.Context, orgID pgtype.UUID) string {
	user, err := s.store.GetOrgAdminByOrg(ctx, orgID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ""
	}
	if err != nil {
		slog.WarnContext(ctx, "查询组织管理员用户名失败", "org_id", uuidToString(orgID), "error", err)
		return ""
	}
	return user.Username
}

func toOrganizationResult(org sqlc.Organization) OrganizationResult {
	// 解析助手版本 allowlist，列为空时兜底为空 slice，避免前端收到 null。
	versionIDs := []string{}
	if len(org.AssistantVersionIds) > 0 {
		if err := json.Unmarshal(org.AssistantVersionIds, &versionIDs); err != nil {
			// 组织 assistant_version_ids 列由本服务统一以 JSON 数组写入，理论上不会损坏；
			// 真出现损坏时记日志而不是静默吞掉，避免组织列表/详情无声降级。
			slog.Warn("解析组织 assistant_version_ids 失败", "org_id", uuidToString(org.ID), "error", err)
			versionIDs = []string{}
		}
	}
	return OrganizationResult{
		ID:                     uuidToString(org.ID),
		Name:                   org.Name,
		Code:                   org.Code,
		Status:                 org.Status,
		ContactName:            textString(org.ContactName),
		ContactPhone:           textString(org.ContactPhone),
		Remark:                 textString(org.Remark),
		NewAPIUserID:           textString(org.NewapiUserID),
		CreditWarningThreshold: int4Pointer(org.CreditWarningThreshold),
		AssistantVersionIDs:    versionIDs,
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
