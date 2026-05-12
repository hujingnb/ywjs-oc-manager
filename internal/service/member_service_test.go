package service

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/stretchr/testify/require"
	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/store/sqlc"
)

const (
	testOrgID    = "00000000-0000-0000-0000-0000000000a1"
	testOrg2ID   = "00000000-0000-0000-0000-0000000000a2"
	testAdminUID = "00000000-0000-0000-0000-0000000000b1"
	testMemUID   = "00000000-0000-0000-0000-0000000000b2"
)

func TestMemberServiceCreateRequiresOrgManagement(t *testing.T) {
	store := newMemberStoreStub(t)
	svc := NewMemberService(store, fakeHash)

	_, err := svc.CreateMember(context.Background(), auth.Principal{Role: domain.UserRoleOrgAdmin, OrgID: testOrg2ID}, testOrgID, MemberInput{
		Username: "alice", DisplayName: "Alice", Password: "password",
	})
	require.ErrorIs(t, err, ErrForbidden)
}

func TestMemberServiceCreateRejectsPlatformAdmin(t *testing.T) {
	store := newMemberStoreStub(t)
	svc := NewMemberService(store, fakeHash)

	_, err := svc.CreateMember(context.Background(), platformAdmin(), testOrgID, MemberInput{
		Username: "alice", DisplayName: "Alice", Password: "password",
	})
	require.ErrorIs(t, err, ErrForbidden)
}

func TestMemberServiceCreateRejectsDisabledOrg(t *testing.T) {
	store := newMemberStoreStub(t)
	store.org.Status = domain.StatusDisabled
	svc := NewMemberService(store, fakeHash)

	_, err := svc.CreateMember(context.Background(), orgAdminPrincipal(), testOrgID, MemberInput{
		Username: "alice", DisplayName: "Alice", Password: "password",
	})
	require.ErrorIs(t, err, ErrMemberCreateInvalid)
}

func TestMemberServiceCreateRejectsInvalidRole(t *testing.T) {
	store := newMemberStoreStub(t)
	svc := NewMemberService(store, fakeHash)

	_, err := svc.CreateMember(context.Background(), platformAdmin(), testOrgID, MemberInput{
		Username: "alice", DisplayName: "Alice", Password: "password", Role: domain.UserRolePlatformAdmin,
	})
	require.ErrorIs(t, err, ErrMemberCreateInvalid)
}

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

func TestMemberServiceListLimitsOrgScope(t *testing.T) {
	store := newMemberStoreStub(t)
	svc := NewMemberService(store, fakeHash)

	_, err := svc.ListMembers(context.Background(), auth.Principal{Role: domain.UserRoleOrgAdmin, OrgID: testOrg2ID}, testOrgID, 0, 0)
	require.ErrorIs(t, err, ErrForbidden)
}

func TestMemberServiceListAppliesDefaultPageSize(t *testing.T) {
	store := newMemberStoreStub(t)
	store.users[testAdminUID] = sqlc.User{
		ID:    mustUUID(t, testAdminUID),
		OrgID: store.org.ID,
		Role:  domain.UserRoleOrgAdmin,
	}
	svc := NewMemberService(store, fakeHash)

	results, err := svc.ListMembers(context.Background(), platformAdmin(), testOrgID, 0, 0)
	require.NoError(t, err)
	require.NotEqual(t, 0, len(results))
	require.Equal(t, int32(50), store.lastList.Limit)
}

func TestMemberServiceListClampsMaxPageSize(t *testing.T) {
	store := newMemberStoreStub(t)
	svc := NewMemberService(store, fakeHash)

	_, err := svc.ListMembers(context.Background(), platformAdmin(), testOrgID, 5000, 0)
	require.NoError(t, err)
	require.Equal(t, int32(200), store.lastList.Limit)
}

func TestMemberServiceGetSelfAccessibleByMember(t *testing.T) {
	store := newMemberStoreStub(t)
	store.users[testMemUID] = sqlc.User{
		ID:       mustUUID(t, testMemUID),
		OrgID:    store.org.ID,
		Username: "bob",
		Role:     domain.UserRoleOrgMember,
		Status:   domain.StatusActive,
	}
	svc := NewMemberService(store, fakeHash)

	result, err := svc.GetMember(context.Background(), auth.Principal{Role: domain.UserRoleOrgMember, OrgID: testOrgID, UserID: testMemUID}, testMemUID)
	require.NoError(t, err)
	require.Equal(t, "bob", result.Username)
}

func TestMemberServiceGetMemberRejectsCrossUserAccess(t *testing.T) {
	store := newMemberStoreStub(t)
	store.users[testMemUID] = sqlc.User{
		ID:    mustUUID(t, testMemUID),
		OrgID: store.org.ID,
		Role:  domain.UserRoleOrgMember,
	}
	svc := NewMemberService(store, fakeHash)

	_, err := svc.GetMember(context.Background(), auth.Principal{Role: domain.UserRoleOrgMember, OrgID: testOrgID, UserID: testAdminUID}, testMemUID)
	require.ErrorIs(t, err, ErrForbidden)
}

func TestMemberServiceUpdateProfileSelfAllowed(t *testing.T) {
	store := newMemberStoreStub(t)
	store.users[testMemUID] = sqlc.User{
		ID:          mustUUID(t, testMemUID),
		OrgID:       store.org.ID,
		Role:        domain.UserRoleOrgMember,
		DisplayName: "Bob",
	}
	svc := NewMemberService(store, fakeHash)

	result, err := svc.UpdateMemberProfile(context.Background(), auth.Principal{Role: domain.UserRoleOrgMember, OrgID: testOrgID, UserID: testMemUID}, testMemUID, MemberInput{DisplayName: "Bobby"})
	require.NoError(t, err)
	require.Equal(t, "Bobby", result.DisplayName)
}

func TestMemberServiceUpdateRoleRequiresAdmin(t *testing.T) {
	store := newMemberStoreStub(t)
	store.users[testMemUID] = sqlc.User{
		ID:    mustUUID(t, testMemUID),
		OrgID: store.org.ID,
		Role:  domain.UserRoleOrgMember,
	}
	svc := NewMemberService(store, fakeHash)

	_, err := svc.UpdateMemberProfile(context.Background(), auth.Principal{Role: domain.UserRoleOrgMember, OrgID: testOrgID, UserID: testMemUID}, testMemUID, MemberInput{
		DisplayName: "Bob", Role: domain.UserRoleOrgAdmin,
	})
	require.ErrorIs(t, err, ErrForbidden)
}

func TestMemberServiceSetStatusBlocksSelfDisable(t *testing.T) {
	store := newMemberStoreStub(t)
	store.users[testAdminUID] = sqlc.User{
		ID:    mustUUID(t, testAdminUID),
		OrgID: store.org.ID,
		Role:  domain.UserRoleOrgAdmin,
	}
	svc := NewMemberService(store, fakeHash)

	_, err := svc.SetMemberStatus(context.Background(), auth.Principal{Role: domain.UserRoleOrgAdmin, OrgID: testOrgID, UserID: testAdminUID}, testAdminUID, domain.StatusDisabled)
	require.ErrorIs(t, err, ErrMemberCreateInvalid)
}

func TestMemberServiceResetPasswordRequiresAdmin(t *testing.T) {
	store := newMemberStoreStub(t)
	store.users[testMemUID] = sqlc.User{
		ID:    mustUUID(t, testMemUID),
		OrgID: store.org.ID,
		Role:  domain.UserRoleOrgMember,
	}
	svc := NewMemberService(store, fakeHash)

	if err := svc.ResetMemberPassword(context.Background(), auth.Principal{Role: domain.UserRoleOrgMember, OrgID: testOrgID, UserID: testMemUID}, testMemUID, "new-pass"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("ResetMemberPassword() error = %v, want ErrForbidden", err)
	}
}

func TestMemberServiceResetPasswordSucceeds(t *testing.T) {
	store := newMemberStoreStub(t)
	store.users[testMemUID] = sqlc.User{
		ID:    mustUUID(t, testMemUID),
		OrgID: store.org.ID,
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

func TestDeleteMember_SoftDeletesAndEnqueuesAppDelete(t *testing.T) {
	stub := newMemberStoreStub(t)
	target := sqlc.User{
		ID:     mustUUID(t, "00000000-0000-0000-0000-0000000000aa"),
		OrgID:  stub.org.ID,
		Status: domain.StatusActive,
		Role:   domain.UserRoleOrgMember,
	}
	stub.users[uuidToString(target.ID)] = target
	app := sqlc.App{
		ID:          mustUUID(t, "00000000-0000-0000-0000-0000000000bb"),
		OrgID:       stub.org.ID,
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

func TestDeleteMember_NoAppStillSoftDeletesUser(t *testing.T) {
	stub := newMemberStoreStub(t)
	target := sqlc.User{
		ID:     mustUUID(t, "00000000-0000-0000-0000-0000000000ab"),
		OrgID:  stub.org.ID,
		Status: domain.StatusActive,
		Role:   domain.UserRoleOrgMember,
	}
	stub.users[uuidToString(target.ID)] = target
	svc := NewMemberService(stub, fakeHash)
	err := svc.DeleteMember(context.Background(), orgAdminPrincipal(), uuidToString(target.ID), nil)
	require.NoError(t, err)
	require.Equal(t, 0, len(stub.jobs))
}

func TestDeleteMember_RejectsSelfDeletion(t *testing.T) {
	stub := newMemberStoreStub(t)
	target := sqlc.User{
		ID:     mustUUID(t, "00000000-0000-0000-0000-000000000001"), // 与 platformAdmin 同 ID
		OrgID:  stub.org.ID,
		Status: domain.StatusActive,
	}
	stub.users[uuidToString(target.ID)] = target
	svc := NewMemberService(stub, fakeHash)
	err := svc.DeleteMember(context.Background(), platformAdmin(), uuidToString(target.ID), nil)
	require.Error(t, err)
}

func TestDeleteMember_OrgMemberCannotDeleteOthers(t *testing.T) {
	stub := newMemberStoreStub(t)
	target := sqlc.User{
		ID:     mustUUID(t, "00000000-0000-0000-0000-0000000000ad"),
		OrgID:  stub.org.ID,
		Status: domain.StatusActive,
	}
	stub.users[uuidToString(target.ID)] = target
	svc := NewMemberService(stub, fakeHash)
	err := svc.DeleteMember(context.Background(),
		auth.Principal{Role: domain.UserRoleOrgMember, OrgID: uuidToString(stub.org.ID), UserID: "other"},
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
	t             *testing.T
	org           sqlc.Organization
	users         map[string]sqlc.User
	apps          map[string]sqlc.App
	jobs          []sqlc.CreateJobParams
	auditWritten  bool
	softDeleted   []string
	lastCreate    sqlc.CreateUserParams
	lastList      sqlc.ListUsersByOrgParams
	lastPwdUpdate sqlc.UpdateUserPasswordParams
}

func newMemberStoreStub(t *testing.T) *memberStoreStub {
	t.Helper()
	return &memberStoreStub{
		t:     t,
		org:   sqlc.Organization{ID: mustUUID(t, testOrgID), Status: domain.StatusActive, Name: "测试组织"},
		users: map[string]sqlc.User{},
		apps:  map[string]sqlc.App{},
	}
}

func (s *memberStoreStub) GetOrganization(_ context.Context, id pgtype.UUID) (sqlc.Organization, error) {
	if id != s.org.ID {
		return sqlc.Organization{}, pgx.ErrNoRows
	}
	return s.org, nil
}

func (s *memberStoreStub) CreateUser(_ context.Context, arg sqlc.CreateUserParams) (sqlc.User, error) {
	s.lastCreate = arg
	user := sqlc.User{
		ID:           mustUUID(s.t, "00000000-0000-0000-0000-0000000000ff"),
		OrgID:        arg.OrgID,
		Username:     arg.Username,
		PasswordHash: arg.PasswordHash,
		DisplayName:  arg.DisplayName,
		Role:         arg.Role,
		Status:       arg.Status,
	}
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

func (s *memberStoreStub) ListUsersByOrg(_ context.Context, arg sqlc.ListUsersByOrgParams) ([]sqlc.User, error) {
	s.lastList = arg
	results := make([]sqlc.User, 0, len(s.users))
	for _, user := range s.users {
		if user.OrgID == arg.OrgID {
			results = append(results, user)
		}
	}
	return results, nil
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
