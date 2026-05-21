package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/service"
)

// TestMembersCreateForwardsPrincipalAndOrg 验证成员创建转发Principal并组织的预期行为场景。
func TestMembersCreateForwardsPrincipalAndOrg(t *testing.T) {
	svc := &memberServiceStub{
		createResult: service.MemberResult{ID: "user-1", Username: "alice", Role: domain.UserRoleOrgMember, Status: domain.StatusActive},
	}
	router := newMembersTestRouter(t, svc)

	recorder := httptest.NewRecorder()
	body := bytes.NewBufferString(`{"username":"alice","display_name":"Alice","password":"pwd"}`)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/organizations/00000000-0000-0000-0000-000000000101/members", body)
	request.Header.Set("Content-Type", "application/json")
	request = withPrincipal(request, auth.Principal{UserID: "platform-1", Role: domain.UserRolePlatformAdmin})
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusCreated, recorder.Code)
	require.Equal(t, "00000000-0000-0000-0000-000000000101", svc.lastOrgID)
	require.Equal(t, domain.UserRolePlatformAdmin, svc.lastPrincipal.Role)
}

// TestMembersDisableMapsErrorToBadRequest 验证成员禁用映射错误到非法请求的异常或拒绝路径场景。
func TestMembersDisableMapsErrorToBadRequest(t *testing.T) {
	svc := &memberServiceStub{statusErr: service.ErrMemberCreateInvalid}
	router := newMembersTestRouter(t, svc)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/members/u1/disable", nil)
	request = withPrincipal(request, auth.Principal{UserID: "u1", OrgID: "00000000-0000-0000-0000-000000000101", Role: domain.UserRoleOrgAdmin})
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusBadRequest, recorder.Code)
}

// TestMembersGetReturnsBody 验证成员获取返回请求体的成功路径场景。
func TestMembersGetReturnsBody(t *testing.T) {
	svc := &memberServiceStub{getResult: service.MemberResult{ID: "u1", Username: "alice"}}
	router := newMembersTestRouter(t, svc)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/members/u1", nil)
	request = withPrincipal(request, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgMember, OrgID: "org"})
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	var resp struct {
		Member service.MemberResult `json:"member"`
	}
	err := json.Unmarshal(recorder.Body.Bytes(), &resp)
	require.NoError(t, err)
	require.Equal(t, "alice", resp.Member.Username)
}

func newMembersTestRouter(t *testing.T, svc memberService) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	RegisterMemberRoutes(router, NewMembersHandler(svc))
	return router
}

type onboardingServiceStub struct {
	result           service.OnboardMemberResult
	createAppResult  service.CreateAppForMemberResult
	lastOrgID        string
	lastUserID       string
	lastOnboardInput service.OnboardMemberInput
	lastCreateInput  service.CreateAppForMemberInput
	err              error
}

func (s *onboardingServiceStub) OnboardMember(_ context.Context, _ auth.Principal, orgID string, input service.OnboardMemberInput) (service.OnboardMemberResult, error) {
	s.lastOrgID = orgID
	s.lastOnboardInput = input
	if s.err != nil {
		return service.OnboardMemberResult{}, s.err
	}
	return s.result, nil
}

func (s *onboardingServiceStub) CreateAppForMember(_ context.Context, _ auth.Principal, orgID, userID string, input service.CreateAppForMemberInput) (service.CreateAppForMemberResult, error) {
	s.lastOrgID = orgID
	s.lastUserID = userID
	s.lastCreateInput = input
	if s.err != nil {
		return service.CreateAppForMemberResult{}, s.err
	}
	return s.createAppResult, nil
}

// newMembersTestRouterWithOnboarding 给需要触发 onboard 路由的测试构造同时挂 onboarding service 的路由器。
func newMembersTestRouterWithOnboarding(t *testing.T, svc memberService, onboarding onboardingService) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	handler := NewMembersHandler(svc)
	handler.SetOnboardingService(onboarding)
	RegisterMemberRoutes(router, handler)
	return router
}

// TestMembersOnboardMapsNoNodeAvailableTo503 验证成员引导映射无节点可用到503的错误映射或错误记录场景。
func TestMembersOnboardMapsNoNodeAvailableTo503(t *testing.T) {
	onboarding := &onboardingServiceStub{err: service.ErrNoNodeAvailable}
	router := newMembersTestRouterWithOnboarding(t, &memberServiceStub{}, onboarding)

	recorder := httptest.NewRecorder()
	// version_id 为必填字段，需包含在请求体中。
	body := bytes.NewBufferString(`{"username":"alice","display_name":"Alice","password":"pwd","app_name":"alice-bot","version_id":"v-id-1"}`)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/organizations/00000000-0000-0000-0000-000000000101/members/onboard", body)
	request.Header.Set("Content-Type", "application/json")
	request = withPrincipal(request, auth.Principal{UserID: "p1", Role: domain.UserRolePlatformAdmin})
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
	require.Contains(t, recorder.Body.String(), "NO_NODE_AVAILABLE")
}

// TestMembersOnboardForwardsRequest 验证成员开户路由会把应用名和助手版本 id 等字段传给 service。
func TestMembersOnboardForwardsRequest(t *testing.T) {
	onboarding := &onboardingServiceStub{
		result: service.OnboardMemberResult{
			App:   service.AppResult{ID: "app-1", Name: "alice-bot", Status: domain.AppStatusDraft},
			JobID: "job-1",
		},
	}
	router := newMembersTestRouterWithOnboarding(t, &memberServiceStub{}, onboarding)

	recorder := httptest.NewRecorder()
	// version_id 为必填字段，与 app_name 一同传入；验证两者均透传给 service 入参。
	body := bytes.NewBufferString(`{"username":"alice","display_name":"Alice","password":"pwd","app_name":"alice-bot","version_id":"v-id-onboard"}`)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/organizations/org-1/members/onboard", body)
	request.Header.Set("Content-Type", "application/json")
	request = withPrincipal(request, auth.Principal{UserID: "p1", Role: domain.UserRoleOrgAdmin, OrgID: "org-1"})
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusCreated, recorder.Code)
	assert.Equal(t, "org-1", onboarding.lastOrgID)
	assert.Equal(t, "alice-bot", onboarding.lastOnboardInput.AppName)
	// 确认助手版本 id 被透传到 service 入参，供 service 层校验 allowlist。
	assert.Equal(t, "v-id-onboard", onboarding.lastOnboardInput.VersionID)
}

// TestMembersCreateAppForMemberForwardsRequest 验证已有成员创建实例路由转发组织、成员、应用和助手版本字段。
func TestMembersCreateAppForMemberForwardsRequest(t *testing.T) {
	onboarding := &onboardingServiceStub{
		createAppResult: service.CreateAppForMemberResult{
			App:   service.AppResult{ID: "app-1", Name: "alice-new-bot", Status: domain.AppStatusDraft},
			JobID: "job-1",
		},
	}
	router := newMembersTestRouterWithOnboarding(t, &memberServiceStub{}, onboarding)

	recorder := httptest.NewRecorder()
	// version_id 为必填字段，与应用字段一同传入；验证全部字段透传给 service 入参。
	body := bytes.NewBufferString(`{"app_name":"alice-new-bot","persona_mode":"app_override","app_prompt":"hello","channel_type":"wechat","runtime_node_id":"node-1","version_id":"v-id-create-app"}`)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/organizations/org-1/members/user-1/apps", body)
	request.Header.Set("Content-Type", "application/json")
	request = withPrincipal(request, auth.Principal{UserID: "p1", Role: domain.UserRolePlatformAdmin})
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusCreated, recorder.Code)
	require.Equal(t, "org-1", onboarding.lastOrgID)
	require.Equal(t, "user-1", onboarding.lastUserID)
	require.Equal(t, "alice-new-bot", onboarding.lastCreateInput.AppName)
	require.Contains(t, recorder.Body.String(), `"job_id":"job-1"`)
	// 确认助手版本 id 被透传到 service 入参，供 service 层校验 allowlist。
	require.Equal(t, "v-id-create-app", onboarding.lastCreateInput.VersionID)
}

// TestMembersCreateAppForMemberMapsNoNodeAvailable 验证已有成员创建实例无可用节点时映射为 503。
func TestMembersCreateAppForMemberMapsNoNodeAvailable(t *testing.T) {
	onboarding := &onboardingServiceStub{err: service.ErrNoNodeAvailable}
	router := newMembersTestRouterWithOnboarding(t, &memberServiceStub{}, onboarding)

	recorder := httptest.NewRecorder()
	// version_id 为必填字段，需包含在请求体中以通过 binding 校验。
	body := bytes.NewBufferString(`{"app_name":"alice-new-bot","version_id":"v-id-1"}`)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/organizations/org-1/members/user-1/apps", body)
	request.Header.Set("Content-Type", "application/json")
	request = withPrincipal(request, auth.Principal{UserID: "p1", Role: domain.UserRolePlatformAdmin})
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
	require.Contains(t, recorder.Body.String(), "NO_NODE_AVAILABLE")
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
