package service

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/store/sqlc"
)

const (
	testOrgID     = "00000000-0000-0000-0000-0000000000a1"
	testOrg2ID    = "00000000-0000-0000-0000-0000000000a2"
	testAdminUID  = "00000000-0000-0000-0000-0000000000b1"
	testAdmin2UID = "00000000-0000-0000-0000-0000000000b3"
	testMemUID    = "00000000-0000-0000-0000-0000000000b2"
)

// TestMemberServiceCreateRequiresOrgManagement 验证成员服务创建要求组织Management的预期行为场景。
func TestMemberServiceCreateRequiresOrgManagement(t *testing.T) {
	store := newMemberStoreStub(t)
	svc := NewMemberService(store, fakeHash)

	_, err := svc.CreateMember(context.Background(), auth.Principal{Role: domain.UserRoleOrgAdmin, OrgID: testOrg2ID}, testOrgID, MemberInput{
		Username: "alice", DisplayName: "Alice", Password: "password",
	})
	require.ErrorIs(t, err, ErrForbidden)
}

// TestMemberServiceCreateRejectsPlatformAdmin 验证成员服务创建拒绝平台管理员的异常或拒绝路径场景。
func TestMemberServiceCreateRejectsPlatformAdmin(t *testing.T) {
	store := newMemberStoreStub(t)
	svc := NewMemberService(store, fakeHash)

	_, err := svc.CreateMember(context.Background(), platformAdmin(), testOrgID, MemberInput{
		Username: "alice", DisplayName: "Alice", Password: "password",
	})
	require.ErrorIs(t, err, ErrForbidden)
}

// TestMemberServiceCreateRejectsDisabledOrg 验证成员服务创建拒绝禁用组织的异常或拒绝路径场景。
func TestMemberServiceCreateRejectsDisabledOrg(t *testing.T) {
	store := newMemberStoreStub(t)
	org := store.orgs[testOrgID]
	org.Status = domain.StatusDisabled
	store.orgs[testOrgID] = org
	svc := NewMemberService(store, fakeHash)

	_, err := svc.CreateMember(context.Background(), orgAdminPrincipal(), testOrgID, MemberInput{
		Username: "alice", DisplayName: "Alice", Password: "password",
	})
	require.ErrorIs(t, err, ErrMemberCreateInvalid)
}

// TestMemberServiceCreateRejectsInvalidRole 验证成员服务创建拒绝非法角色的异常或拒绝路径场景。
func TestMemberServiceCreateRejectsInvalidRole(t *testing.T) {
	store := newMemberStoreStub(t)
	svc := NewMemberService(store, fakeHash)

	_, err := svc.CreateMember(context.Background(), platformAdmin(), testOrgID, MemberInput{
		Username: "alice", DisplayName: "Alice", Password: "password", Role: domain.UserRolePlatformAdmin,
	})
	require.ErrorIs(t, err, ErrMemberCreateInvalid)
}

// TestMemberServiceCreateAssignsDefaultRoleAndHashesPassword 验证成员服务创建Assigns默认值角色并Hashes密码的边界条件场景。
func TestMemberServiceCreateAssignsDefaultRoleAndHashesPassword(t *testing.T) {
	store := newMemberStoreStub(t)
	svc := NewMemberService(store, fakeHash)

	result, err := svc.CreateMember(context.Background(), orgAdminPrincipal(), testOrgID, MemberInput{
		Username: "alice", DisplayName: "Alice", Password: "password",
	})
	require.NoError(t, err)
	require.Equal(t, domain.UserRoleOrgMember, result.Role)
	if store.lastCreate.PasswordHash == "password" || store.lastCreate.PasswordHash == "" {
		t.Fatalf("password should be hashed, got %q", store.lastCreate.PasswordHash)
	}
	require.Equal(t, domain.StatusActive, store.lastCreate.Status)
}

// TestCreateMemberAllowsSameUsernameAcrossDifferentOrganizations 验证创建成员允许相同Username跨不同组织的预期行为场景。
func TestCreateMemberAllowsSameUsernameAcrossDifferentOrganizations(t *testing.T) {
	store := newMemberStoreStub(t)
	store.orgs[testOrg2ID] = sqlc.Organization{ID: mustUUID(t, testOrg2ID), Name: "另一个组织", Status: domain.StatusActive}
	svc := NewMemberService(store, fakeHash)

	first, err := svc.CreateMember(context.Background(), orgAdminPrincipal(), testOrgID, MemberInput{
		Username:    "admin",
		DisplayName: "组织一管理员",
		Password:    "password-123",
		Role:        domain.UserRoleOrgAdmin,
	})
	require.NoError(t, err)
	require.Equal(t, "admin", first.Username)

	secondPrincipal := auth.Principal{Role: domain.UserRoleOrgAdmin, OrgID: testOrg2ID, UserID: testAdmin2UID}
	second, err := svc.CreateMember(context.Background(), secondPrincipal, testOrg2ID, MemberInput{
		Username:    "admin",
		DisplayName: "组织二管理员",
		Password:    "password-123",
		Role:        domain.UserRoleOrgAdmin,
	})
	require.NoError(t, err)
	require.Equal(t, "admin", second.Username)
}

// TestMemberServiceListLimitsOrgScope 验证成员服务列表限制组织scope的边界条件场景。
func TestMemberServiceListLimitsOrgScope(t *testing.T) {
	store := newMemberStoreStub(t)
	svc := NewMemberService(store, fakeHash)

	_, err := svc.ListMembers(context.Background(), auth.Principal{Role: domain.UserRoleOrgAdmin, OrgID: testOrg2ID}, testOrgID, 0, 0)
	require.ErrorIs(t, err, ErrForbidden)
}

// TestMemberServiceListAppliesDefaultPageSize 验证成员服务列表应用默认值分页Size的边界条件场景。
func TestMemberServiceListAppliesDefaultPageSize(t *testing.T) {
	store := newMemberStoreStub(t)
	store.users[testAdminUID] = sqlc.User{
		ID:    mustUUID(t, testAdminUID),
		OrgID: store.orgs[testOrgID].ID,
		Role:  domain.UserRoleOrgAdmin,
	}
	svc := NewMemberService(store, fakeHash)

	results, err := svc.ListMembers(context.Background(), platformAdmin(), testOrgID, 0, 0)
	require.NoError(t, err)
	require.NotEqual(t, 0, len(results))
	require.Equal(t, int32(50), store.lastListWithApp.Limit)
}

// TestMemberServiceListClampsMaxPageSize 验证成员服务列表限制最大分页Size的边界条件场景。
func TestMemberServiceListClampsMaxPageSize(t *testing.T) {
	store := newMemberStoreStub(t)
	svc := NewMemberService(store, fakeHash)

	_, err := svc.ListMembers(context.Background(), platformAdmin(), testOrgID, 5000, 0)
	require.NoError(t, err)
	require.Equal(t, int32(200), store.lastListWithApp.Limit)
}

// TestMemberServiceListExposesActiveApp 验证 ListMembers 返回每个成员当前关联的活跃实例。
// 三类场景必须同时覆盖：有活跃实例、无活跃实例、实例被软删的成员都应正确还原。
func TestMemberServiceListExposesActiveApp(t *testing.T) {
	// withApp：拥有未软删 app 的成员；列表应返回 active_app_id/name 指针。
	// noApp：组织成员只创建了用户，未来需要补建；返回值两字段为 nil。
	// deletedApp：成员名下唯一的 app 已被软删，等同于「无实例」状态。
	store := newMemberStoreStub(t)
	orgUUID := store.orgs[testOrgID].ID

	withAppID := mustUUID(t, "00000000-0000-0000-0000-0000000000c1")
	noAppID := mustUUID(t, "00000000-0000-0000-0000-0000000000c2")
	deletedID := mustUUID(t, "00000000-0000-0000-0000-0000000000c3")
	store.users[uuidToString(withAppID)] = sqlc.User{ID: withAppID, OrgID: orgUUID, Username: "with-app", DisplayName: "有实例的成员", Role: domain.UserRoleOrgMember, Status: domain.StatusActive}
	store.users[uuidToString(noAppID)] = sqlc.User{ID: noAppID, OrgID: orgUUID, Username: "no-app", DisplayName: "无实例的成员", Role: domain.UserRoleOrgMember, Status: domain.StatusActive}
	store.users[uuidToString(deletedID)] = sqlc.User{ID: deletedID, OrgID: orgUUID, Username: "deleted-app", DisplayName: "实例被删的成员", Role: domain.UserRoleOrgMember, Status: domain.StatusActive}

	activeAppID := mustUUID(t, "00000000-0000-0000-0000-0000000000d1")
	deletedAppID := mustUUID(t, "00000000-0000-0000-0000-0000000000d2")
	store.apps[uuidToString(activeAppID)] = sqlc.App{ID: activeAppID, OrgID: orgUUID, OwnerUserID: withAppID, Name: "现役实例"}
	store.apps[uuidToString(deletedAppID)] = sqlc.App{ID: deletedAppID, OrgID: orgUUID, OwnerUserID: deletedID, Name: "已删实例", DeletedAt: pgtype.Timestamptz{Valid: true}}

	svc := NewMemberService(store, fakeHash)
	results, err := svc.ListMembers(context.Background(), platformAdmin(), testOrgID, 0, 0)
	require.NoError(t, err)
	require.Len(t, results, 3)

	byUsername := map[string]MemberResult{}
	for _, r := range results {
		byUsername[r.Username] = r
	}

	// 有活跃实例：active_app_id 指向 activeAppID 字符串，active_app_name 为应用名。
	withAppResult := byUsername["with-app"]
	require.NotNil(t, withAppResult.ActiveAppID)
	assert.Equal(t, uuidToString(activeAppID), *withAppResult.ActiveAppID)
	require.NotNil(t, withAppResult.ActiveAppName)
	assert.Equal(t, "现役实例", *withAppResult.ActiveAppName)

	// 没创建实例：两字段为 nil 指针，前端据此显示「无实例」+ 补建按钮。
	noAppResult := byUsername["no-app"]
	assert.Nil(t, noAppResult.ActiveAppID)
	assert.Nil(t, noAppResult.ActiveAppName)

	// 实例被软删：active_app_id/name 也为 nil；与「从未创建」语义一致。
	deletedAppResult := byUsername["deleted-app"]
	assert.Nil(t, deletedAppResult.ActiveAppID)
	assert.Nil(t, deletedAppResult.ActiveAppName)
}

// TestMemberServiceGetSelfAccessibleByMember 验证成员服务获取自身Accessible通过成员的预期行为场景。
func TestMemberServiceGetSelfAccessibleByMember(t *testing.T) {
	store := newMemberStoreStub(t)
	store.users[testMemUID] = sqlc.User{
		ID:       mustUUID(t, testMemUID),
		OrgID:    store.orgs[testOrgID].ID,
		Username: "bob",
		Role:     domain.UserRoleOrgMember,
		Status:   domain.StatusActive,
	}
	svc := NewMemberService(store, fakeHash)

	result, err := svc.GetMember(context.Background(), auth.Principal{Role: domain.UserRoleOrgMember, OrgID: testOrgID, UserID: testMemUID}, testMemUID)
	require.NoError(t, err)
	require.Equal(t, "bob", result.Username)
}

// TestMemberServiceGetMemberRejectsCrossUserAccess 验证成员服务获取成员拒绝跨用户Access的异常或拒绝路径场景。
func TestMemberServiceGetMemberRejectsCrossUserAccess(t *testing.T) {
	store := newMemberStoreStub(t)
	store.users[testMemUID] = sqlc.User{
		ID:    mustUUID(t, testMemUID),
		OrgID: store.orgs[testOrgID].ID,
		Role:  domain.UserRoleOrgMember,
	}
	svc := NewMemberService(store, fakeHash)

	_, err := svc.GetMember(context.Background(), auth.Principal{Role: domain.UserRoleOrgMember, OrgID: testOrgID, UserID: testAdminUID}, testMemUID)
	require.ErrorIs(t, err, ErrForbidden)
}

// TestMemberServiceUpdateProfileSelfAllowed 验证成员服务更新Profile自身Allowed的预期行为场景。
func TestMemberServiceUpdateProfileSelfAllowed(t *testing.T) {
	store := newMemberStoreStub(t)
	store.users[testMemUID] = sqlc.User{
		ID:          mustUUID(t, testMemUID),
		OrgID:       store.orgs[testOrgID].ID,
		Role:        domain.UserRoleOrgMember,
		DisplayName: "Bob",
	}
	svc := NewMemberService(store, fakeHash)

	result, err := svc.UpdateMemberProfile(context.Background(), auth.Principal{Role: domain.UserRoleOrgMember, OrgID: testOrgID, UserID: testMemUID}, testMemUID, MemberInput{DisplayName: "Bobby"})
	require.NoError(t, err)
	require.Equal(t, "Bobby", result.DisplayName)
}

// TestMemberServiceUpdateRoleRequiresAdmin 验证成员服务更新角色要求管理员的预期行为场景。
func TestMemberServiceUpdateRoleRequiresAdmin(t *testing.T) {
	store := newMemberStoreStub(t)
	store.users[testMemUID] = sqlc.User{
		ID:    mustUUID(t, testMemUID),
		OrgID: store.orgs[testOrgID].ID,
		Role:  domain.UserRoleOrgMember,
	}
	svc := NewMemberService(store, fakeHash)

	_, err := svc.UpdateMemberProfile(context.Background(), auth.Principal{Role: domain.UserRoleOrgMember, OrgID: testOrgID, UserID: testMemUID}, testMemUID, MemberInput{
		DisplayName: "Bob", Role: domain.UserRoleOrgAdmin,
	})
	require.ErrorIs(t, err, ErrForbidden)
}

// TestMemberServiceSetStatusBlocksSelfDisable 验证成员服务Set状态Blocks自身禁用的预期行为场景。
func TestMemberServiceSetStatusBlocksSelfDisable(t *testing.T) {
	store := newMemberStoreStub(t)
	store.users[testAdminUID] = sqlc.User{
		ID:    mustUUID(t, testAdminUID),
		OrgID: store.orgs[testOrgID].ID,
		Role:  domain.UserRoleOrgAdmin,
	}
	svc := NewMemberService(store, fakeHash)

	_, err := svc.SetMemberStatus(context.Background(), auth.Principal{Role: domain.UserRoleOrgAdmin, OrgID: testOrgID, UserID: testAdminUID}, testAdminUID, domain.StatusDisabled)
	require.ErrorIs(t, err, ErrMemberCreateInvalid)
}

// TestMemberServiceResetPasswordRequiresAdmin 验证成员服务Reset密码要求管理员的预期行为场景。
func TestMemberServiceResetPasswordRequiresAdmin(t *testing.T) {
	store := newMemberStoreStub(t)
	store.users[testMemUID] = sqlc.User{
		ID:    mustUUID(t, testMemUID),
		OrgID: store.orgs[testOrgID].ID,
		Role:  domain.UserRoleOrgMember,
	}
	svc := NewMemberService(store, fakeHash)

	if err := svc.ResetMemberPassword(context.Background(), auth.Principal{Role: domain.UserRoleOrgMember, OrgID: testOrgID, UserID: testMemUID}, testMemUID, "new-pass"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("ResetMemberPassword() error = %v, want ErrForbidden", err)
	}
}

// TestMemberServiceResetPasswordSucceeds 验证成员服务Reset密码Succeeds的成功路径场景。
func TestMemberServiceResetPasswordSucceeds(t *testing.T) {
	store := newMemberStoreStub(t)
	store.users[testMemUID] = sqlc.User{
		ID:    mustUUID(t, testMemUID),
		OrgID: store.orgs[testOrgID].ID,
		Role:  domain.UserRoleOrgMember,
	}
	svc := NewMemberService(store, fakeHash)

	err := svc.ResetMemberPassword(context.Background(), orgAdminPrincipal(), testMemUID, "new-pass")
	require.NoError(t, err)
	if store.lastPwdUpdate.PasswordHash == "" || store.lastPwdUpdate.PasswordHash == "new-pass" {
		t.Fatalf("password not hashed, got %q", store.lastPwdUpdate.PasswordHash)
	}
}

// fakeHash 在测试中用前缀代替真实 Argon2id，避免单测耗时。
func fakeHash(password string) (string, error) { return "hashed:" + password, nil }

// TestDeleteMember_SoftDeletesAndEnqueuesAppDelete 验证删除成员软删除Deletes并Enqueues应用删除的预期行为场景。
func TestDeleteMember_SoftDeletesAndEnqueuesAppDelete(t *testing.T) {
	stub := newMemberStoreStub(t)
	target := sqlc.User{
		ID:     mustUUID(t, "00000000-0000-0000-0000-0000000000aa"),
		OrgID:  stub.orgs[testOrgID].ID,
		Status: domain.StatusActive,
		Role:   domain.UserRoleOrgMember,
	}
	stub.users[uuidToString(target.ID)] = target
	app := sqlc.App{
		ID:          mustUUID(t, "00000000-0000-0000-0000-0000000000bb"),
		OrgID:       stub.orgs[testOrgID].ID,
		OwnerUserID: target.ID,
		Status:      domain.AppStatusRunning,
	}
	stub.apps[uuidToString(app.ID)] = app

	notifier := &fakeNotifier{}
	svc := NewMemberService(stub, fakeHash)
	err := svc.DeleteMember(context.Background(), orgAdminPrincipal(), uuidToString(target.ID), notifier)
	require.NoError(t, err)
	require.Equal(t, domain.StatusDisabled, stub.users[uuidToString(target.ID)].Status)
	require.Equal(t, 1, len(stub.softDeleted))
	if len(stub.jobs) != 1 || stub.jobs[0].Type != domain.JobTypeAppDelete {
		t.Fatalf("jobs = %+v", stub.jobs)
	}
	require.True(t, stub.auditWritten)
	require.NotEqual(t, "", notifier.lastJobID)
}

// TestDeleteMember_NoAppStillSoftDeletesUser 验证删除成员无应用仍然软删除Deletes用户的预期行为场景。
func TestDeleteMember_NoAppStillSoftDeletesUser(t *testing.T) {
	stub := newMemberStoreStub(t)
	target := sqlc.User{
		ID:     mustUUID(t, "00000000-0000-0000-0000-0000000000ab"),
		OrgID:  stub.orgs[testOrgID].ID,
		Status: domain.StatusActive,
		Role:   domain.UserRoleOrgMember,
	}
	stub.users[uuidToString(target.ID)] = target
	svc := NewMemberService(stub, fakeHash)
	err := svc.DeleteMember(context.Background(), orgAdminPrincipal(), uuidToString(target.ID), nil)
	require.NoError(t, err)
	require.Equal(t, 0, len(stub.jobs))
}

// TestDeleteMember_RejectsSelfDeletion 验证删除成员拒绝自身Deletion的异常或拒绝路径场景。
func TestDeleteMember_RejectsSelfDeletion(t *testing.T) {
	stub := newMemberStoreStub(t)
	target := sqlc.User{
		ID:     mustUUID(t, testAdminUID), // 场景：组织管理员删除自己的账号应被拒绝。
		OrgID:  stub.orgs[testOrgID].ID,
		Status: domain.StatusActive,
		Role:   domain.UserRoleOrgAdmin,
	}
	stub.users[uuidToString(target.ID)] = target
	svc := NewMemberService(stub, fakeHash)
	err := svc.DeleteMember(context.Background(), orgAdminPrincipal(), uuidToString(target.ID), nil)
	require.ErrorIs(t, err, ErrMemberCreateInvalid)
}

// TestDeleteMember_OrgMemberCannotDeleteOthers 验证删除成员组织成员Cannot删除其他s的预期行为场景。
func TestDeleteMember_OrgMemberCannotDeleteOthers(t *testing.T) {
	stub := newMemberStoreStub(t)
	target := sqlc.User{
		ID:     mustUUID(t, "00000000-0000-0000-0000-0000000000ad"),
		OrgID:  stub.orgs[testOrgID].ID,
		Status: domain.StatusActive,
	}
	stub.users[uuidToString(target.ID)] = target
	svc := NewMemberService(stub, fakeHash)
	err := svc.DeleteMember(context.Background(),
		auth.Principal{Role: domain.UserRoleOrgMember, OrgID: uuidToString(stub.orgs[testOrgID].ID), UserID: "other"},
		uuidToString(target.ID), nil)
	require.ErrorIs(t, err, ErrForbidden)
}

func platformAdmin() auth.Principal {
	return auth.Principal{Role: domain.UserRolePlatformAdmin, UserID: "00000000-0000-0000-0000-000000000001"}
}

func orgAdminPrincipal() auth.Principal {
	return auth.Principal{Role: domain.UserRoleOrgAdmin, OrgID: testOrgID, UserID: testAdminUID}
}

type memberStoreStub struct {
	t                  *testing.T
	orgs               map[string]sqlc.Organization
	users              map[string]sqlc.User
	usersByOrgUsername map[string]sqlc.User
	apps               map[string]sqlc.App
	jobs               []sqlc.CreateJobParams
	auditWritten       bool
	softDeleted        []string
	lastCreate         sqlc.CreateUserParams
	lastListWithApp    sqlc.ListUsersByOrgWithActiveAppParams
	lastPwdUpdate      sqlc.UpdateUserPasswordParams
}

func newMemberStoreStub(t *testing.T) *memberStoreStub {
	t.Helper()
	org := sqlc.Organization{ID: mustUUID(t, testOrgID), Status: domain.StatusActive, Name: "测试组织"}
	return &memberStoreStub{
		t:                  t,
		orgs:               map[string]sqlc.Organization{testOrgID: org},
		users:              map[string]sqlc.User{},
		usersByOrgUsername: map[string]sqlc.User{},
		apps:               map[string]sqlc.App{},
	}
}

func (s *memberStoreStub) GetOrganization(_ context.Context, id pgtype.UUID) (sqlc.Organization, error) {
	org, ok := s.orgs[uuidToString(id)]
	if !ok {
		return sqlc.Organization{}, pgx.ErrNoRows
	}
	return org, nil
}

func (s *memberStoreStub) CreateUser(_ context.Context, arg sqlc.CreateUserParams) (sqlc.User, error) {
	s.lastCreate = arg
	key := uuidToString(arg.OrgID) + "/" + arg.Username
	if _, exists := s.usersByOrgUsername[key]; exists {
		return sqlc.User{}, errors.New("duplicate username in organization")
	}
	id := mustUUID(s.t, "00000000-0000-0000-0000-0000000000ff")
	id.Bytes[15] = byte(len(s.users) + 1)
	user := sqlc.User{
		ID:           id,
		OrgID:        arg.OrgID,
		Username:     arg.Username,
		PasswordHash: arg.PasswordHash,
		DisplayName:  arg.DisplayName,
		Role:         arg.Role,
		Status:       arg.Status,
	}
	s.usersByOrgUsername[key] = user
	s.users[uuidToString(user.ID)] = user
	return user, nil
}

func (s *memberStoreStub) GetUser(_ context.Context, id pgtype.UUID) (sqlc.User, error) {
	user, ok := s.users[uuidToString(id)]
	if !ok {
		return sqlc.User{}, pgx.ErrNoRows
	}
	return user, nil
}

func (s *memberStoreStub) GetUserByUsername(_ context.Context, username string) (sqlc.User, error) {
	for _, user := range s.users {
		if user.Username == username {
			return user, nil
		}
	}
	return sqlc.User{}, pgx.ErrNoRows
}

// ListUsersByOrgWithActiveApp 模拟 sqlc 的 LEFT JOIN：先取本组织全部 users，
// 再为每个 user 查找 apps 表中未软删的实例。apps_owner_active 约束保证最多一个。
func (s *memberStoreStub) ListUsersByOrgWithActiveApp(_ context.Context, arg sqlc.ListUsersByOrgWithActiveAppParams) ([]sqlc.ListUsersByOrgWithActiveAppRow, error) {
	s.lastListWithApp = arg
	rows := make([]sqlc.ListUsersByOrgWithActiveAppRow, 0, len(s.users))
	for _, user := range s.users {
		if user.OrgID != arg.OrgID {
			continue
		}
		row := sqlc.ListUsersByOrgWithActiveAppRow{
			ID:           user.ID,
			OrgID:        user.OrgID,
			Username:     user.Username,
			PasswordHash: user.PasswordHash,
			DisplayName:  user.DisplayName,
			Role:         user.Role,
			Status:       user.Status,
			LastLoginAt:  user.LastLoginAt,
			CreatedAt:    user.CreatedAt,
			UpdatedAt:    user.UpdatedAt,
			DeletedAt:    user.DeletedAt,
		}
		for _, app := range s.apps {
			if app.OwnerUserID == user.ID && !app.DeletedAt.Valid {
				row.ActiveAppID = app.ID
				row.ActiveAppName = pgtype.Text{String: app.Name, Valid: true}
				break
			}
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func (s *memberStoreStub) UpdateUserProfile(_ context.Context, arg sqlc.UpdateUserProfileParams) (sqlc.User, error) {
	user, ok := s.users[uuidToString(arg.ID)]
	if !ok {
		return sqlc.User{}, pgx.ErrNoRows
	}
	user.DisplayName = arg.DisplayName
	user.Role = arg.Role
	s.users[uuidToString(arg.ID)] = user
	return user, nil
}

func (s *memberStoreStub) SetUserStatus(_ context.Context, arg sqlc.SetUserStatusParams) (sqlc.User, error) {
	user, ok := s.users[uuidToString(arg.ID)]
	if !ok {
		return sqlc.User{}, pgx.ErrNoRows
	}
	user.Status = arg.Status
	s.users[uuidToString(arg.ID)] = user
	return user, nil
}

func (s *memberStoreStub) UpdateUserPassword(_ context.Context, arg sqlc.UpdateUserPasswordParams) (sqlc.User, error) {
	s.lastPwdUpdate = arg
	user, ok := s.users[uuidToString(arg.ID)]
	if !ok {
		return sqlc.User{}, pgx.ErrNoRows
	}
	user.PasswordHash = arg.PasswordHash
	s.users[uuidToString(arg.ID)] = user
	return user, nil
}

func (s *memberStoreStub) GetActiveAppByOwner(_ context.Context, ownerUserID pgtype.UUID) (sqlc.App, error) {
	for _, app := range s.apps {
		if app.OwnerUserID == ownerUserID && !app.DeletedAt.Valid {
			return app, nil
		}
	}
	return sqlc.App{}, pgx.ErrNoRows
}

func (s *memberStoreStub) SoftDeleteApp(_ context.Context, id pgtype.UUID) (sqlc.App, error) {
	app, ok := s.apps[uuidToString(id)]
	if !ok {
		return sqlc.App{}, pgx.ErrNoRows
	}
	app.DeletedAt = pgtype.Timestamptz{Valid: true}
	s.apps[uuidToString(id)] = app
	s.softDeleted = append(s.softDeleted, uuidToString(id))
	return app, nil
}

func (s *memberStoreStub) CreateJob(_ context.Context, arg sqlc.CreateJobParams) (sqlc.Job, error) {
	s.jobs = append(s.jobs, arg)
	return sqlc.Job{ID: mustUUID(s.t, "00000000-0000-0000-0000-000000004001"), Type: arg.Type}, nil
}

func (s *memberStoreStub) CreateAuditLog(_ context.Context, _ sqlc.CreateAuditLogParams) (sqlc.AuditLog, error) {
	s.auditWritten = true
	return sqlc.AuditLog{}, nil
}
