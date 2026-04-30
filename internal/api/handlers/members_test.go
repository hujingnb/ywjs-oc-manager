package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/service"
)

func TestMembersListRequiresToken(t *testing.T) {
	router, _ := newMembersTestRouter(t, &memberServiceStub{})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/organizations/00000000-0000-0000-0000-000000000101/members", nil)
	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", recorder.Code)
	}
}

func TestMembersCreateForwardsPrincipalAndOrg(t *testing.T) {
	svc := &memberServiceStub{
		createResult: service.MemberResult{ID: "user-1", Username: "alice", Role: domain.UserRoleOrgMember, Status: domain.StatusActive},
	}
	router, tokens := newMembersTestRouter(t, svc)
	token := mustSignAccess(t, tokens, auth.Principal{UserID: "platform-1", Role: domain.UserRolePlatformAdmin})

	recorder := httptest.NewRecorder()
	body := bytes.NewBufferString(`{"username":"alice","display_name":"Alice","password":"pwd"}`)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/organizations/00000000-0000-0000-0000-000000000101/members", body)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+token)
	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201, body=%s", recorder.Code, recorder.Body.String())
	}
	if svc.lastOrgID != "00000000-0000-0000-0000-000000000101" {
		t.Fatalf("orgID = %s, want path value", svc.lastOrgID)
	}
	if svc.lastPrincipal.Role != domain.UserRolePlatformAdmin {
		t.Fatalf("principal = %+v", svc.lastPrincipal)
	}
}

func TestMembersDisableMapsErrorToBadRequest(t *testing.T) {
	svc := &memberServiceStub{statusErr: service.ErrMemberCreateInvalid}
	router, tokens := newMembersTestRouter(t, svc)
	token := mustSignAccess(t, tokens, auth.Principal{UserID: "u1", OrgID: "00000000-0000-0000-0000-000000000101", Role: domain.UserRoleOrgAdmin})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/members/u1/disable", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestMembersGetReturnsBody(t *testing.T) {
	svc := &memberServiceStub{getResult: service.MemberResult{ID: "u1", Username: "alice"}}
	router, tokens := newMembersTestRouter(t, svc)
	token := mustSignAccess(t, tokens, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgMember, OrgID: "org"})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/members/u1", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	var resp struct {
		Member service.MemberResult `json:"member"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Member.Username != "alice" {
		t.Fatalf("member = %+v", resp.Member)
	}
}

func newMembersTestRouter(t *testing.T, svc memberService) (*gin.Engine, *auth.TokenManager) {
	t.Helper()
	gin.SetMode(gin.ReleaseMode)
	tokens, err := auth.NewTokenManager("a", "b", time.Minute, time.Hour)
	if err != nil {
		t.Fatalf("NewTokenManager() error = %v", err)
	}
	router := gin.New()
	RegisterMemberRoutes(router, NewMembersHandler(svc, tokens))
	return router, tokens
}

func mustSignAccess(t *testing.T, tokens *auth.TokenManager, principal auth.Principal) string {
	t.Helper()
	token, err := tokens.SignAccessToken(principal)
	if err != nil {
		t.Fatalf("SignAccessToken() error = %v", err)
	}
	return token
}

type memberServiceStub struct {
	createResult  service.MemberResult
	listResult    []service.MemberResult
	getResult     service.MemberResult
	updateResult  service.MemberResult
	statusResult  service.MemberResult
	statusErr     error
	resetErr      error
	lastPrincipal auth.Principal
	lastOrgID     string
	lastUserID    string
}

func (s *memberServiceStub) CreateMember(_ context.Context, principal auth.Principal, orgID string, _ service.MemberInput) (service.MemberResult, error) {
	s.lastPrincipal = principal
	s.lastOrgID = orgID
	return s.createResult, nil
}

func (s *memberServiceStub) ListMembers(_ context.Context, principal auth.Principal, orgID string, _, _ int32) ([]service.MemberResult, error) {
	s.lastPrincipal = principal
	s.lastOrgID = orgID
	return s.listResult, nil
}

func (s *memberServiceStub) GetMember(_ context.Context, principal auth.Principal, userID string) (service.MemberResult, error) {
	s.lastPrincipal = principal
	s.lastUserID = userID
	return s.getResult, nil
}

func (s *memberServiceStub) UpdateMemberProfile(_ context.Context, principal auth.Principal, userID string, _ service.MemberInput) (service.MemberResult, error) {
	s.lastPrincipal = principal
	s.lastUserID = userID
	return s.updateResult, nil
}

func (s *memberServiceStub) SetMemberStatus(_ context.Context, principal auth.Principal, userID, _ string) (service.MemberResult, error) {
	s.lastPrincipal = principal
	s.lastUserID = userID
	if s.statusErr != nil {
		return service.MemberResult{}, s.statusErr
	}
	return s.statusResult, nil
}

func (s *memberServiceStub) ResetMemberPassword(_ context.Context, principal auth.Principal, userID, _ string) error {
	s.lastPrincipal = principal
	s.lastUserID = userID
	return s.resetErr
}

func (s *memberServiceStub) DeleteMember(_ context.Context, principal auth.Principal, userID string, _ service.JobNotifier) error {
	s.lastPrincipal = principal
	s.lastUserID = userID
	return nil
}
