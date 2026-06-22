package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/guregu/null/v5"

	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/store/sqlc"
)

// MemberStore 抽象成员服务所需的数据访问能力。
// 仅暴露当前实现需要的方法，便于在单元测试中使用内存桩，避免引入完整 sqlc 依赖。
type MemberStore interface {
	GetOrganization(ctx context.Context, id string) (sqlc.Organization, error)
	CreateUser(ctx context.Context, arg sqlc.CreateUserParams) error
	GetUser(ctx context.Context, id string) (sqlc.User, error)
	GetUserByUsername(ctx context.Context, username string) (sqlc.User, error)
	// ListUsersByOrgWithActiveApp 列出成员及其当前未软删实例的 id/name，
	// 用于成员列表上区分「需要补建」与「已绑定」两种状态。
	ListUsersByOrgWithActiveApp(ctx context.Context, arg sqlc.ListUsersByOrgWithActiveAppParams) ([]sqlc.ListUsersByOrgWithActiveAppRow, error)
	UpdateUserProfile(ctx context.Context, arg sqlc.UpdateUserProfileParams) error
	SetUserStatus(ctx context.Context, arg sqlc.SetUserStatusParams) error
	UpdateUserPassword(ctx context.Context, arg sqlc.UpdateUserPasswordParams) error

	// 以下方法用于成员删除联动应用软删；store 实现已经具备这些查询。
	GetActiveAppByOwner(ctx context.Context, ownerUserID string) (sqlc.App, error)
	SoftDeleteApp(ctx context.Context, id string) error
	CreateJob(ctx context.Context, arg sqlc.CreateJobParams) error
	CreateAuditLog(ctx context.Context, arg sqlc.CreateAuditLogParams) error
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
	// Username 仅创建成员时使用，更新资料时不会修改登录账号。
	Username string
	// DisplayName 是成员展示名，创建和更新都要求非空。
	DisplayName string
	// Password 仅创建成员或重置密码时使用，写库前会 hash。
	Password string
	// Role 为空时创建成员使用默认 org_member；更新时为空表示保持原角色。
	Role string
}

// MemberResult 是对外返回的成员视图，剥离了密码等敏感字段。
type MemberResult struct {
	// ID 是成员用户 UUID。
	ID string `json:"id"`
	// OrgID 是成员所属企业 UUID；platform_admin 可能为空。
	OrgID string `json:"org_id,omitempty"`
	// Username 是登录账号名。
	Username string `json:"username"`
	// DisplayName 是前端展示名。
	DisplayName string `json:"display_name"`
	// Role 是成员角色，限定为 org_admin 或 org_member。
	Role string `json:"role"`
	// Status 是成员状态；disabled 会阻止登录并设置 users.deleted_at。
	Status string `json:"status"`
	// ActiveAppID 是该成员当前未软删实例的 UUID；nil 表示成员名下没有活跃实例。
	// 仅在 ListMembers 列表返回里有值，单条 GetMember 等接口保持 nil。
	ActiveAppID *string `json:"active_app_id,omitempty"`
	// ActiveAppName 是该成员当前活跃实例的展示名；nil 与 ActiveAppID 同步。
	ActiveAppName *string `json:"active_app_name,omitempty"`
}

// CreateMember 创建组织成员。
// 仅本组织管理员可创建；平台管理员只保留成员观察能力，不直接创建组织成员。
// 当前版本仅支持创建 org_admin 或 org_member 角色，平台管理员账号通过种子数据维护。
func (s *MemberService) CreateMember(ctx context.Context, principal auth.Principal, orgID string, input MemberInput) (MemberResult, error) {
	role := strings.TrimSpace(input.Role)
	if role == "" {
		role = s.defaultRole
	}
	if role != domain.UserRoleOrgAdmin && role != domain.UserRoleOrgMember {
		return MemberResult{}, fmt.Errorf("%w: 不支持的角色", ErrMemberCreateInvalid)
	}
	if !auth.CanManageMember(principal, orgID) {
		return MemberResult{}, ErrForbidden
	}
	if input.Username == "" || input.Password == "" || input.DisplayName == "" {
		return MemberResult{}, fmt.Errorf("%w: 用户名、密码和显示名不能为空", ErrMemberCreateInvalid)
	}

	org, err := s.store.GetOrganization(ctx, orgID)
	if errors.Is(err, sql.ErrNoRows) {
		return MemberResult{}, ErrNotFound
	}
	if err != nil {
		return MemberResult{}, fmt.Errorf("查询企业失败: %w", err)
	}
	if org.Status != domain.StatusActive {
		return MemberResult{}, fmt.Errorf("%w: 企业已停用", ErrMemberCreateInvalid)
	}

	hashed, err := s.hashPassword(input.Password)
	if err != nil {
		return MemberResult{}, fmt.Errorf("生成密码 hash 失败: %w", err)
	}
	// CreateUser 为 :exec；预先生成 ID，写入后通过 GetUser 读回。
	userID := newUUID()
	if err := s.store.CreateUser(ctx, sqlc.CreateUserParams{
		ID:           userID,
		OrgID:        null.StringFrom(org.ID),
		Username:     input.Username,
		PasswordHash: hashed,
		DisplayName:  input.DisplayName,
		Role:         role,
		Status:       domain.StatusActive,
	}); err != nil {
		return MemberResult{}, fmt.Errorf("创建成员失败: %w", err)
	}
	user, err := s.store.GetUser(ctx, userID)
	if err != nil {
		return MemberResult{}, fmt.Errorf("读取新建成员失败: %w", err)
	}
	return toMemberResult(user), nil
}

// ListMembers 分页列出指定组织的成员。
// 平台管理员可以查看任意组织；组织管理员仅能查看本组织；普通成员无成员视角。
func (s *MemberService) ListMembers(ctx context.Context, principal auth.Principal, orgID string, limit, offset int32) ([]MemberResult, error) {
	if !auth.CanListMembers(principal, orgID) {
		return nil, ErrForbidden
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
	rows, err := s.store.ListUsersByOrgWithActiveApp(ctx, sqlc.ListUsersByOrgWithActiveAppParams{
		OrgID:  null.StringFrom(orgID),
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		return nil, fmt.Errorf("查询成员列表失败: %w", err)
	}
	return toMemberResultsWithApp(rows), nil
}

// GetMember 查询单个成员。
// 平台管理员可查任意成员；组织角色仅能查询本组织成员，普通成员只能查询自己。
func (s *MemberService) GetMember(ctx context.Context, principal auth.Principal, userID string) (MemberResult, error) {
	user, err := s.store.GetUser(ctx, userID)
	if errors.Is(err, sql.ErrNoRows) {
		return MemberResult{}, ErrNotFound
	}
	if err != nil {
		return MemberResult{}, fmt.Errorf("查询成员失败: %w", err)
	}
	if !auth.CanViewMember(principal, strOrEmpty(user.OrgID), user.ID) {
		return MemberResult{}, ErrForbidden
	}
	return toMemberResult(user), nil
}

// UpdateMemberProfile 更新成员显示名和角色。
// 普通成员仅能修改自己的显示名；调整角色需要本组织管理员权限。
func (s *MemberService) UpdateMemberProfile(ctx context.Context, principal auth.Principal, userID string, input MemberInput) (MemberResult, error) {
	user, err := s.store.GetUser(ctx, userID)
	if errors.Is(err, sql.ErrNoRows) {
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
		if !auth.CanManageMember(principal, strOrEmpty(user.OrgID)) {
			return MemberResult{}, ErrForbidden
		}
		if role != domain.UserRoleOrgAdmin && role != domain.UserRoleOrgMember {
			return MemberResult{}, fmt.Errorf("%w: 不支持的角色", ErrMemberCreateInvalid)
		}
	} else {
		if !auth.CanEditMember(principal, strOrEmpty(user.OrgID), user.ID) {
			return MemberResult{}, ErrForbidden
		}
	}
	if input.DisplayName == "" {
		return MemberResult{}, fmt.Errorf("%w: 显示名不能为空", ErrMemberCreateInvalid)
	}
	// UpdateUserProfile 为 :exec；写入后重新读取最新数据。
	if err := s.store.UpdateUserProfile(ctx, sqlc.UpdateUserProfileParams{
		ID:          user.ID,
		DisplayName: input.DisplayName,
		Role:        role,
	}); err != nil {
		return MemberResult{}, fmt.Errorf("更新成员失败: %w", err)
	}
	updated, err := s.store.GetUser(ctx, user.ID)
	if err != nil {
		return MemberResult{}, fmt.Errorf("读取更新后成员失败: %w", err)
	}
	return toMemberResult(updated), nil
}

// SetMemberStatus 启用或禁用成员。
// 仅本组织管理员可执行；不能禁用自己以避免锁定唯一管理员。
func (s *MemberService) SetMemberStatus(ctx context.Context, principal auth.Principal, userID, status string) (MemberResult, error) {
	if status != domain.StatusActive && status != domain.StatusDisabled {
		return MemberResult{}, fmt.Errorf("非法成员状态: %s", status)
	}
	user, err := s.store.GetUser(ctx, userID)
	if errors.Is(err, sql.ErrNoRows) {
		return MemberResult{}, ErrNotFound
	}
	if err != nil {
		return MemberResult{}, fmt.Errorf("查询成员失败: %w", err)
	}
	if !auth.CanManageMember(principal, strOrEmpty(user.OrgID)) {
		return MemberResult{}, ErrForbidden
	}
	if principal.UserID == user.ID && status == domain.StatusDisabled {
		return MemberResult{}, fmt.Errorf("%w: 不能禁用自己", ErrMemberCreateInvalid)
	}
	// users.deleted_at 在本项目中表示下线时间戳，由 SetUserStatus 随 status 自动维护。
	// SetUserStatus 为 :exec；写入后重新读取最新数据。
	if err := s.store.SetUserStatus(ctx, sqlc.SetUserStatusParams{ID: user.ID, Status: status}); err != nil {
		return MemberResult{}, fmt.Errorf("更新成员状态失败: %w", err)
	}
	updated, err := s.store.GetUser(ctx, user.ID)
	if err != nil {
		return MemberResult{}, fmt.Errorf("读取状态更新后成员失败: %w", err)
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
	user, err := s.store.GetUser(ctx, userID)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("查询成员失败: %w", err)
	}
	if !auth.CanManageMember(principal, strOrEmpty(user.OrgID)) {
		return ErrForbidden
	}
	if principal.UserID == user.ID {
		return fmt.Errorf("%w: 不能删除自己", ErrMemberCreateInvalid)
	}
	if err := s.store.SetUserStatus(ctx, sqlc.SetUserStatusParams{ID: user.ID, Status: domain.StatusDisabled}); err != nil {
		return fmt.Errorf("禁用成员失败: %w", err)
	}

	// 查找该成员名下未删除的应用；找不到时跳过应用删除。
	app, err := s.store.GetActiveAppByOwner(ctx, user.ID)
	hasApp := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("查询成员应用失败: %w", err)
	}
	// 第一版每个未删除成员账号最多拥有一个未删除应用，因此 cascadeCount 仅为 0 / 1；
	// 写进 audit 详情让运维一眼看出删除成员是否带走应用。
	cascadeCount := 0
	if hasApp {
		cascadeCount = 1
		if err := s.store.SoftDeleteApp(ctx, app.ID); err != nil {
			return fmt.Errorf("软删应用失败: %w", err)
		}
		// 入队 app_delete worker job 处理容器/api_key 回收。
		payload := []byte(`{"app_id":"` + app.ID + `"}`)
		jobID := newUUID()
		if err := s.store.CreateJob(ctx, sqlc.CreateJobParams{
			ID:          jobID,
			Type:        domain.JobTypeAppDelete,
			Priority:    100,
			RunAfter:    time.Now(),
			MaxAttempts: 3,
			PayloadJson: payload,
		}); err != nil {
			return fmt.Errorf("创建 app_delete job 失败: %w", err)
		}
		if notifier != nil {
			_ = notifier.Enqueue(ctx, jobID)
		}
	}

	// ActorID、OrgID 由字符串直接转 null.String。
	// metadata 存储结构化参数：cascade_count（级联删除的应用数量），供前端按语言渲染详情。
	deleteMeta, _ := json.Marshal(map[string]any{
		"cascade_count": cascadeCount,
	})
	if err := s.store.CreateAuditLog(ctx, sqlc.CreateAuditLogParams{
		ID:           newUUID(),
		ActorID:      null.StringFrom(principal.UserID),
		ActorRole:    principal.Role,
		OrgID:        user.OrgID,
		TargetType:   "user",
		TargetID:     user.ID,
		Action:       "delete_member",
		Result:       "succeeded",
		MetadataJson: deleteMeta,
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
	user, err := s.store.GetUser(ctx, userID)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("查询成员失败: %w", err)
	}
	if !auth.CanManageMember(principal, strOrEmpty(user.OrgID)) {
		return ErrForbidden
	}
	hashed, err := s.hashPassword(newPassword)
	if err != nil {
		return fmt.Errorf("生成密码 hash 失败: %w", err)
	}
	if err := s.store.UpdateUserPassword(ctx, sqlc.UpdateUserPasswordParams{ID: user.ID, PasswordHash: hashed}); err != nil {
		return fmt.Errorf("更新成员密码失败: %w", err)
	}
	return nil
}

func toMemberResult(user sqlc.User) MemberResult {
	return MemberResult{
		ID:          user.ID,
		OrgID:       strOrEmpty(user.OrgID),
		Username:    user.Username,
		DisplayName: user.DisplayName,
		Role:        user.Role,
		Status:      user.Status,
	}
}

// toMemberResultsWithApp 把 sqlc 的 LEFT JOIN 行映射为 MemberResult。
// 仅当 active_app_id 在数据库层有值时才把指针填上，避免误判「无实例」为空字符串。
func toMemberResultsWithApp(rows []sqlc.ListUsersByOrgWithActiveAppRow) []MemberResult {
	results := make([]MemberResult, 0, len(rows))
	for _, row := range rows {
		result := MemberResult{
			ID:          row.ID,
			OrgID:       strOrEmpty(row.OrgID),
			Username:    row.Username,
			DisplayName: row.DisplayName,
			Role:        row.Role,
			Status:      row.Status,
		}
		if row.ActiveAppID.Valid {
			id := row.ActiveAppID.String
			result.ActiveAppID = &id
		}
		if row.ActiveAppName.Valid {
			name := row.ActiveAppName.String
			result.ActiveAppName = &name
		}
		results = append(results, result)
	}
	return results
}
