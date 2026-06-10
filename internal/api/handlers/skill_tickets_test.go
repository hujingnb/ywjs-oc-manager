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

	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/service"
)

// skillTicketServiceStub 实现 handler 依赖接口,隔离 HTTP 层测试。
type skillTicketServiceStub struct {
	submitRes service.SkillTicketResult
	submitErr error
	gotSubmit service.SubmitSkillTicketInput
	detailRes service.SkillTicketDetailResult
	detailErr error
	badge     int64
}

func (s *skillTicketServiceStub) Submit(_ context.Context, _ auth.Principal, in service.SubmitSkillTicketInput) (service.SkillTicketResult, error) {
	s.gotSubmit = in
	return s.submitRes, s.submitErr
}
func (s *skillTicketServiceStub) ListMine(context.Context, auth.Principal) ([]service.SkillTicketResult, error) {
	return nil, nil
}
func (s *skillTicketServiceStub) ListAll(context.Context, auth.Principal) ([]service.SkillTicketResult, error) {
	return nil, nil
}
func (s *skillTicketServiceStub) Get(context.Context, auth.Principal, string) (service.SkillTicketDetailResult, error) {
	return s.detailRes, s.detailErr
}
func (s *skillTicketServiceStub) AddComment(context.Context, auth.Principal, string, string) (service.SkillTicketCommentResult, error) {
	return service.SkillTicketCommentResult{}, nil
}
func (s *skillTicketServiceStub) UpdateStatus(context.Context, auth.Principal, string, string) error {
	return nil
}
func (s *skillTicketServiceStub) SetQuote(context.Context, auth.Principal, string, int64) error {
	return nil
}
func (s *skillTicketServiceStub) Reject(context.Context, auth.Principal, string, string) error {
	return nil
}
func (s *skillTicketServiceStub) PendingBadgeCount(context.Context, auth.Principal) (int64, error) {
	return s.badge, nil
}

func memberReq(req *http.Request) *http.Request {
	return withPrincipal(req, auth.Principal{UserID: "u-mem", OrgID: "org-1", Role: domain.UserRoleOrgMember})
}
func adminReq(req *http.Request) *http.Request {
	return withPrincipal(req, auth.Principal{UserID: "u-admin", Role: domain.UserRolePlatformAdmin})
}

// POST /skill-tickets:解析 JSON 并回 201 + service 结果。
func TestSkillTicketsHandler_Submit(t *testing.T) {
	stub := &skillTicketServiceStub{submitRes: service.SkillTicketResult{ID: "t1", Title: "周报", Status: "pending"}}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	RegisterSkillTicketRoutes(r, NewSkillTicketsHandler(stub))

	body, _ := json.Marshal(SubmitSkillTicketRequest{Title: "周报", Description: "每周汇总"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/skill-tickets", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, memberReq(req))

	assert.Equal(t, http.StatusCreated, rec.Code)
	assert.Equal(t, "周报", stub.gotSubmit.Title)
	assert.Equal(t, "每周汇总", stub.gotSubmit.Description)
}

// GET 详情未找到 → 404。
func TestSkillTicketsHandler_Get_NotFound(t *testing.T) {
	stub := &skillTicketServiceStub{detailErr: service.ErrSkillTicketNotFound}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	RegisterSkillTicketRoutes(r, NewSkillTicketsHandler(stub))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/skill-tickets/t1", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, memberReq(req))
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// GET 管理员角标:回 200 + count。
func TestSkillTicketsHandler_Badge(t *testing.T) {
	stub := &skillTicketServiceStub{badge: 3}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	RegisterSkillTicketRoutes(r, NewSkillTicketsHandler(stub))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/skill-tickets/badge", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, adminReq(req))
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "3")
}
