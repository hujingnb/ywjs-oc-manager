package service

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

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
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("CreateMember() error = %v, want ErrForbidden", err)
	}
}

func TestMemberServiceCreateRejectsDisabledOrg(t *testing.T) {
	store := newMemberStoreStub(t)
	store.org.Status = domain.StatusDisabled
	svc := NewMemberService(store, fakeHash)

	_, err := svc.CreateMember(context.Background(), platformAdmin(), testOrgID, MemberInput{
		Username: "alice", DisplayName: "Alice", Password: "password",
	})
	if !errors.Is(err, ErrMemberCreateInvalid) {
		t.Fatalf("CreateMember() error = %v, want ErrMemberCreateInvalid", err)
	}
}

func TestMemberServiceCreateRejectsInvalidRole(t *testing.T) {
	store := newMemberStoreStub(t)
	svc := NewMemberService(store, fakeHash)

	_, err := svc.CreateMember(context.Background(), platformAdmin(), testOrgID, MemberInput{
		Username: "alice", DisplayName: "Alice", Password: "password", Role: domain.UserRolePlatformAdmin,
	})
	if !errors.Is(err, ErrMemberCreateInvalid) {
		t.Fatalf("CreateMember() error = %v, want ErrMemberCreateInvalid", err)
	}
}

func TestMemberServiceCreateAssignsDefaultRoleAndHashesPassword(t *testing.T) {
	store := newMemberStoreStub(t)
	svc := NewMemberService(store, fakeHash)

	result, err := svc.CreateMember(context.Background(), platformAdmin(), testOrgID, MemberInput{
		Username: "alice", DisplayName: "Alice", Password: "password",
	})
	if err != nil {
		t.Fatalf("CreateMember() error = %v", err)
	}
	if result.Role != domain.UserRoleOrgMember {
		t.Fatalf("role = %s, want %s", result.Role, domain.UserRoleOrgMember)
	}
	if store.lastCreate.PasswordHash == "password" || store.lastCreate.PasswordHash == "" {
		t.Fatalf("password should be hashed, got %q", store.lastCreate.PasswordHash)
	}
	if store.lastCreate.Status != domain.StatusActive {
		t.Fatalf("status = %s, want active", store.lastCreate.Status)
	}
}

func TestMemberServiceListLimitsOrgScope(t *testing.T) {
	store := newMemberStoreStub(t)
	svc := NewMemberService(store, fakeHash)

	_, err := svc.ListMembers(context.Background(), auth.Principal{Role: domain.UserRoleOrgAdmin, OrgID: testOrg2ID}, testOrgID, 0, 0)
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("ListMembers() error = %v, want ErrForbidden", err)
	}
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
	if err != nil {
		t.Fatalf("ListMembers() error = %v", err)
	}
	if len(results) == 0 {
		t.Fatalf("expected at least one member, got 0")
	}
	if store.lastList.Limit != 50 {
		t.Fatalf("default limit = %d, want 50", store.lastList.Limit)
	}
}

func TestMemberServiceListClampsMaxPageSize(t *testing.T) {
	store := newMemberStoreStub(t)
	svc := NewMemberService(store, fakeHash)

	if _, err := svc.ListMembers(context.Background(), platformAdmin(), testOrgID, 5000, 0); err != nil {
		t.Fatalf("ListMembers() error = %v", err)
	}
	if store.lastList.Limit != 200 {
		t.Fatalf("limit = %d, want clamped to 200", store.lastList.Limit)
	}
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
	if err != nil {
		t.Fatalf("GetMember() error = %v", err)
	}
	if result.Username != "bob" {
		t.Fatalf("username = %s, want bob", result.Username)
	}
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
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("GetMember() error = %v, want ErrForbidden", err)
	}
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
	if err != nil {
		t.Fatalf("UpdateMemberProfile() error = %v", err)
	}
	if result.DisplayName != "Bobby" {
		t.Fatalf("display name = %s, want Bobby", result.DisplayName)
	}
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
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("UpdateMemberProfile() error = %v, want ErrForbidden", err)
	}
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
	if !errors.Is(err, ErrMemberCreateInvalid) {
		t.Fatalf("SetMemberStatus() error = %v, want ErrMemberCreateInvalid", err)
	}
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

	if err := svc.ResetMemberPassword(context.Background(), platformAdmin(), testMemUID, "new-pass"); err != nil {
		t.Fatalf("ResetMemberPassword() error = %v", err)
	}
	if store.lastPwdUpdate.PasswordHash == "" || store.lastPwdUpdate.PasswordHash == "new-pass" {
		t.Fatalf("password not hashed, got %q", store.lastPwdUpdate.PasswordHash)
	}
}

// fakeHash 在测试中用前缀代替真实 Argon2id，避免单测耗时。
func fakeHash(password string) (string, error) { return "hashed:" + password, nil }

func platformAdmin() auth.Principal {
	return auth.Principal{Role: domain.UserRolePlatformAdmin, UserID: "00000000-0000-0000-0000-000000000001"}
}

type memberStoreStub struct {
	t             *testing.T
	org           sqlc.Organization
	users         map[string]sqlc.User
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
