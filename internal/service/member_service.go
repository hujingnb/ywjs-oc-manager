package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/store/sqlc"
)

// ErrMemberCreateInvalid 在创建成员的输入未通过业务校验时返回，handler 据此映射为 400。
var ErrMemberCreateInvalid = errors.New("成员资料不合法")

// MemberStore 抽象成员服务所需的数据访问能力。
// 仅暴露当前实现需要的方法，便于在单元测试中使用内存桩，避免引入完整 sqlc 依赖。
type MemberStore interface {
	GetOrganization(ctx context.Context, id pgtype.UUID) (sqlc.Organization, error)
	CreateUser(ctx context.Context, arg sqlc.CreateUserParams) (sqlc.User, error)
	GetUser(ctx context.Context, id pgtype.UUID) (sqlc.User, error)
	GetUserByUsername(ctx context.Context, username string) (sqlc.User, error)
	ListUsersByOrg(ctx context.Context, arg sqlc.ListUsersByOrgParams) ([]sqlc.User, error)
	UpdateUserProfile(ctx context.Context, arg sqlc.UpdateUserProfileParams) (sqlc.User, error)
	SetUserStatus(ctx context.Context, arg sqlc.SetUserStatusParams) (sqlc.User, error)
	UpdateUserPassword(ctx context.Context, arg sqlc.UpdateUserPasswordParams) (sqlc.User, error)

	// 以下方法用于成员删除联动应用软删；store 实现已经具备这些查询。
	GetActiveAppByOwner(ctx context.Context, ownerUserID pgtype.UUID) (sqlc.App, error)
	SoftDeleteApp(ctx context.Context, id pgtype.UUID) (sqlc.App, error)
	CreateJob(ctx context.Context, arg sqlc.CreateJobParams) (sqlc.Job, error)
	CreateAuditLog(ctx context.Context, arg sqlc.CreateAuditLogParams) (sqlc.AuditLog, error)
}

// PasswordHasher 抽象密码 hash 函数，便于测试中替换为快路径。
type PasswordHasher func(password string) (string, error)

// MemberService 提供组织成员的 CRUD、状态切换和密码重置等基础能力。
// 涉及创建成员后联动初始化应用、渠道绑定的复合事务由 task 5.2 在更高一层组合实现。
type MemberService struct {
	store          MemberStore
	hashPassword   PasswordHasher
	defaultRole    string
	maxPageSize    int32
	defaultPageNum int32
}

// NewMemberService 创建成员服务，调用方负责注入 hash 实现。
// 默认页大小和上限对所有列表接口生效，避免恶意请求拉取整张用户表。
func NewMemberService(store MemberStore, hash PasswordHasher) *MemberService {
	return &MemberService{
		store:          store,
		hashPassword:   hash,
		defaultRole:    domain.UserRoleOrgMember,
		maxPageSize:    200,
		defaultPageNum: 50,
	}
}

// MemberInput 表示成员创建/更新的入参。
// Username 仅创建时有效；更新接口不允许修改用户名以避免影响审计追溯。
type MemberInput struct {
	Username    string
	DisplayName string
	Password    string
	Role        string
}

// MemberResult 是对外返回的成员视图，剥离了密码等敏感字段。
type MemberResult struct {
	ID          string `json:"id"`
	OrgID       string `json:"org_id,omitempty"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	Role        string `json:"role"`
	Status      string `json:"status"`
}

// CreateMember 创建组织成员。
// 平台管理员可在任意启用组织内创建；组织管理员仅能在自己组织内创建。
// 当前版本仅支持创建 org_admin 或 org_member 角色，平台管理员账号通过种子数据维护。
func (s *MemberService) CreateMember(ctx context.Context, principal auth.Principal, orgID string, input MemberInput) (MemberResult, error) {
	role := strings.TrimSpace(input.Role)
	if role == "" {
		role = s.defaultRole
	}
	if role != domain.UserRoleOrgAdmin && role != domain.UserRoleOrgMember {
		return MemberResult{}, fmt.Errorf("%w: 不支持的角色", ErrMemberCreateInvalid)
	}
	if !canManageOrg(principal, orgID) {
		return MemberResult{}, ErrForbidden
	}
	if input.Username == "" || input.Password == "" || input.DisplayName == "" {
		return MemberResult{}, fmt.Errorf("%w: 用户名、密码和显示名不能为空", ErrMemberCreateInvalid)
	}

	orgUUID, err := parseUUID(orgID)
	if err != nil {
		return MemberResult{}, ErrNotFound
	}
	org, err := s.store.GetOrganization(ctx, orgUUID)
	if errors.Is(err, pgx.ErrNoRows) {
		return MemberResult{}, ErrNotFound
	}
	if err != nil {
		return MemberResult{}, fmt.Errorf("查询组织失败: %w", err)
	}
	if org.Status != domain.StatusActive {
		return MemberResult{}, fmt.Errorf("%w: 组织已停用", ErrMemberCreateInvalid)
	}

	hashed, err := s.hashPassword(input.Password)
	if err != nil {
		return MemberResult{}, fmt.Errorf("生成密码 hash 失败: %w", err)
	}
	user, err := s.store.CreateUser(ctx, sqlc.CreateUserParams{
		OrgID:        org.ID,
		Username:     input.Username,
		PasswordHash: hashed,
		DisplayName:  input.DisplayName,
		Role:         role,
		Status:       domain.StatusActive,
	})
	if err != nil {
		return MemberResult{}, fmt.Errorf("创建成员失败: %w", err)
	}
	return toMemberResult(user), nil
}

// ListMembers 分页列出指定组织的成员。
// 平台管理员可以查看任意组织；组织内角色仅能查看自己的组织。
func (s *MemberService) ListMembers(ctx context.Context, principal auth.Principal, orgID string, limit, offset int32) ([]MemberResult, error) {
	if !canViewOrg(principal, orgID) {
		return nil, ErrForbidden
	}
	id, err := parseUUID(orgID)
	if err != nil {
		return nil, ErrNotFound
	}
	if limit <= 0 {
		limit = s.defaultPageNum
	}
	if limit > s.maxPageSize {
		limit = s.maxPageSize
	}
	if offset < 0 {
		offset = 0
	}
	users, err := s.store.ListUsersByOrg(ctx, sqlc.ListUsersByOrgParams{
		OrgID:  id,
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		return nil, fmt.Errorf("查询成员列表失败: %w", err)
	}
	return toMemberResults(users), nil
}

// GetMember 查询单个成员。
// 平台管理员可查任意成员；组织角色仅能查询本组织成员，普通成员只能查询自己。
func (s *MemberService) GetMember(ctx context.Context, principal auth.Principal, userID string) (MemberResult, error) {
	id, err := parseUUID(userID)
	if err != nil {
		return MemberResult{}, ErrNotFound
	}
	user, err := s.store.GetUser(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return MemberResult{}, ErrNotFound
	}
	if err != nil {
		return MemberResult{}, fmt.Errorf("查询成员失败: %w", err)
	}
	if !canAccessMember(principal, user) {
		return MemberResult{}, ErrForbidden
	}
	return toMemberResult(user), nil
}

// UpdateMemberProfile 更新成员显示名和角色。
// 普通成员仅能修改自己的显示名；调整角色需要组织管理员或平台管理员权限。
func (s *MemberService) UpdateMemberProfile(ctx context.Context, principal auth.Principal, userID string, input MemberInput) (MemberResult, error) {
	id, err := parseUUID(userID)
	if err != nil {
		return MemberResult{}, ErrNotFound
	}
	user, err := s.store.GetUser(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return MemberResult{}, ErrNotFound
	}
	if err != nil {
		return MemberResult{}, fmt.Errorf("查询成员失败: %w", err)
	}
	role := input.Role
	if role == "" {
		role = user.Role
	}
	roleChanging := role != user.Role
	if roleChanging {
		if !canManageMember(principal, user) {
			return MemberResult{}, ErrForbidden
		}
		if role != domain.UserRoleOrgAdmin && role != domain.UserRoleOrgMember {
			return MemberResult{}, fmt.Errorf("%w: 不支持的角色", ErrMemberCreateInvalid)
		}
	} else {
		if !canEditOwnProfile(principal, user) {
			return MemberResult{}, ErrForbidden
		}
	}
	if input.DisplayName == "" {
		return MemberResult{}, fmt.Errorf("%w: 显示名不能为空", ErrMemberCreateInvalid)
	}
	updated, err := s.store.UpdateUserProfile(ctx, sqlc.UpdateUserProfileParams{
		ID:          user.ID,
		DisplayName: input.DisplayName,
		Role:        role,
	})
	if err != nil {
		return MemberResult{}, fmt.Errorf("更新成员失败: %w", err)
	}
	return toMemberResult(updated), nil
}

// SetMemberStatus 启用或禁用成员。
// 仅组织管理员或平台管理员可执行；不能禁用自己以避免锁定唯一管理员。
func (s *MemberService) SetMemberStatus(ctx context.Context, principal auth.Principal, userID, status string) (MemberResult, error) {
	if status != domain.StatusActive && status != domain.StatusDisabled {
		return MemberResult{}, fmt.Errorf("非法成员状态: %s", status)
	}
	id, err := parseUUID(userID)
	if err != nil {
		return MemberResult{}, ErrNotFound
	}
	user, err := s.store.GetUser(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return MemberResult{}, ErrNotFound
	}
	if err != nil {
		return MemberResult{}, fmt.Errorf("查询成员失败: %w", err)
	}
	if !canManageMember(principal, user) {
		return MemberResult{}, ErrForbidden
	}
	if principal.UserID == uuidToString(user.ID) && status == domain.StatusDisabled {
		return MemberResult{}, fmt.Errorf("%w: 不能禁用自己", ErrMemberCreateInvalid)
	}
	updated, err := s.store.SetUserStatus(ctx, sqlc.SetUserStatusParams{ID: user.ID, Status: status})
	if err != nil {
		return MemberResult{}, fmt.Errorf("更新成员状态失败: %w", err)
	}
	return toMemberResult(updated), nil
}

// DeleteMember 软删成员并联动其名下应用。
//
// 流程：
//  1. 把 user.status = 'disabled'（保留行用于审计追溯，不真正删除 users 行）；
//  2. 若该成员有未删除的应用（GetActiveAppByOwner 命中）则 SoftDeleteApp + 入队 app_delete job；
//  3. 写一条 audit log。
//
// 操作限制：管理员不能删除自己，避免误锁定；普通成员无权删除。
func (s *MemberService) DeleteMember(ctx context.Context, principal auth.Principal, userID string, notifier JobNotifier) error {
	id, err := parseUUID(userID)
	if err != nil {
		return ErrNotFound
	}
	user, err := s.store.GetUser(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("查询成员失败: %w", err)
	}
	if !canManageMember(principal, user) {
		return ErrForbidden
	}
	if principal.UserID == uuidToString(user.ID) {
		return fmt.Errorf("%w: 不能删除自己", ErrMemberCreateInvalid)
	}
	if _, err := s.store.SetUserStatus(ctx, sqlc.SetUserStatusParams{ID: user.ID, Status: domain.StatusDisabled}); err != nil {
		return fmt.Errorf("禁用成员失败: %w", err)
	}

	// 查找该成员名下未删除的应用；找不到时跳过应用删除。
	app, err := s.store.GetActiveAppByOwner(ctx, user.ID)
	hasApp := err == nil
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("查询成员应用失败: %w", err)
	}
	if hasApp {
		if _, err := s.store.SoftDeleteApp(ctx, app.ID); err != nil {
			return fmt.Errorf("软删应用失败: %w", err)
		}
		// 入队 app_delete worker job 处理容器/api_key 回收。
		payload := []byte(`{"app_id":"` + uuidToString(app.ID) + `"}`)
		job, err := s.store.CreateJob(ctx, sqlc.CreateJobParams{
			Type:        domain.JobTypeAppDelete,
			Priority:    100,
			RunAfter:    pgtype.Timestamptz{Time: time.Now(), Valid: true},
			MaxAttempts: 3,
			PayloadJson: payload,
		})
		if err != nil {
			return fmt.Errorf("创建 app_delete job 失败: %w", err)
		}
		if notifier != nil {
			_ = notifier.Enqueue(ctx, uuidToString(job.ID))
		}
	}

	actorUUID, _ := optionalUUID(principal.UserID)
	if _, err := s.store.CreateAuditLog(ctx, sqlc.CreateAuditLogParams{
		ActorID:    actorUUID,
		ActorRole:  principal.Role,
		OrgID:      user.OrgID,
		TargetType: "user",
		TargetID:   uuidToString(user.ID),
		Action:     "delete_member",
		Result:     "succeeded",
	}); err != nil {
		return fmt.Errorf("写入审计日志失败: %w", err)
	}
	return nil
}

// ResetMemberPassword 由管理员强制重置成员密码。
// 普通成员修改自己密码走单独的修改密码流程，需要旧密码校验。
func (s *MemberService) ResetMemberPassword(ctx context.Context, principal auth.Principal, userID, newPassword string) error {
	if newPassword == "" {
		return fmt.Errorf("%w: 新密码不能为空", ErrMemberCreateInvalid)
	}
	id, err := parseUUID(userID)
	if err != nil {
		return ErrNotFound
	}
	user, err := s.store.GetUser(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("查询成员失败: %w", err)
	}
	if !canManageMember(principal, user) {
		return ErrForbidden
	}
	hashed, err := s.hashPassword(newPassword)
	if err != nil {
		return fmt.Errorf("生成密码 hash 失败: %w", err)
	}
	if _, err := s.store.UpdateUserPassword(ctx, sqlc.UpdateUserPasswordParams{ID: user.ID, PasswordHash: hashed}); err != nil {
		return fmt.Errorf("更新成员密码失败: %w", err)
	}
	return nil
}

// canManageOrg 判断主体是否可以管理指定组织（创建/编辑成员、调整状态等写操作）。
func canManageOrg(principal auth.Principal, orgID string) bool {
	switch principal.Role {
	case domain.UserRolePlatformAdmin:
		return true
	case domain.UserRoleOrgAdmin:
		return principal.OrgID == orgID
	default:
		return false
	}
}

// canViewOrg 判断主体是否可以查看指定组织内的资源（读路径）。
func canViewOrg(principal auth.Principal, orgID string) bool {
	if principal.Role == domain.UserRolePlatformAdmin {
		return true
	}
	return principal.OrgID == orgID
}

// canAccessMember 判断主体是否可以读取目标成员的明细。
// 普通成员只能查看自己；组织管理员可查看本组织成员；平台管理员可查所有人。
func canAccessMember(principal auth.Principal, user sqlc.User) bool {
	switch principal.Role {
	case domain.UserRolePlatformAdmin:
		return true
	case domain.UserRoleOrgAdmin:
		return principal.OrgID == uuidToOptionalString(user.OrgID)
	case domain.UserRoleOrgMember:
		return principal.UserID == uuidToString(user.ID)
	default:
		return false
	}
}

// canManageMember 判断主体能否对目标成员执行写操作（角色调整、状态切换、密码重置）。
func canManageMember(principal auth.Principal, user sqlc.User) bool {
	switch principal.Role {
	case domain.UserRolePlatformAdmin:
		return true
	case domain.UserRoleOrgAdmin:
		return principal.OrgID == uuidToOptionalString(user.OrgID)
	default:
		return false
	}
}

// canEditOwnProfile 判断主体是否可以编辑自身资料，对应非角色调整的更新路径。
func canEditOwnProfile(principal auth.Principal, user sqlc.User) bool {
	if canManageMember(principal, user) {
		return true
	}
	return principal.UserID == uuidToString(user.ID)
}

func toMemberResults(users []sqlc.User) []MemberResult {
	results := make([]MemberResult, 0, len(users))
	for _, user := range users {
		results = append(results, toMemberResult(user))
	}
	return results
}

func toMemberResult(user sqlc.User) MemberResult {
	return MemberResult{
		ID:          uuidToString(user.ID),
		OrgID:       uuidToOptionalString(user.OrgID),
		Username:    user.Username,
		DisplayName: user.DisplayName,
		Role:        user.Role,
		Status:      user.Status,
	}
}
