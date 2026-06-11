package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
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
	startID   string
	reopenID  string
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
func (s *skillTicketServiceStub) StartProcessing(_ context.Context, _ auth.Principal, id string) error {
	s.startID = id
	return nil
}
func (s *skillTicketServiceStub) ReopenRejected(_ context.Context, _ auth.Principal, id string) error {
	s.reopenID = id
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

// skillTicketMessageServiceStub 实现消息 handler 依赖,用于验证 text/upload/download HTTP 解析。
type skillTicketMessageServiceStub struct {
	textRes        service.SkillTicketMessageResult
	fileRes        service.SkillTicketMessageResult
	gotTextTicket  string
	gotText        string
	gotFileTicket  string
	gotFileName    string
	gotContentType string
	gotFileData    []byte
	downloadBody   []byte
	downloadName   string
	downloadType   string
	gotDownloadID  string
}

func (s *skillTicketMessageServiceStub) SendText(_ context.Context, _ auth.Principal, ticketID, text string) (service.SkillTicketMessageResult, error) {
	s.gotTextTicket, s.gotText = ticketID, text
	return s.textRes, nil
}

func (s *skillTicketMessageServiceStub) SendFile(_ context.Context, _ auth.Principal, ticketID, fileName, contentType string, data []byte) (service.SkillTicketMessageResult, error) {
	s.gotFileTicket, s.gotFileName, s.gotContentType, s.gotFileData = ticketID, fileName, contentType, data
	return s.fileRes, nil
}

func (s *skillTicketMessageServiceStub) DownloadFile(_ context.Context, _ auth.Principal, ticketID, messageID string) ([]byte, string, string, error) {
	s.gotFileTicket, s.gotDownloadID = ticketID, messageID
	return s.downloadBody, s.downloadName, s.downloadType, nil
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
	msgs := &skillTicketMessageServiceStub{}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	RegisterSkillTicketRoutes(r, NewSkillTicketsHandler(stub, msgs))

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
	msgs := &skillTicketMessageServiceStub{}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	RegisterSkillTicketRoutes(r, NewSkillTicketsHandler(stub, msgs))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/skill-tickets/t1", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, memberReq(req))
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// GET 管理员角标:回 200 + count。
func TestSkillTicketsHandler_Badge(t *testing.T) {
	stub := &skillTicketServiceStub{badge: 3}
	msgs := &skillTicketMessageServiceStub{}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	RegisterSkillTicketRoutes(r, NewSkillTicketsHandler(stub, msgs))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/skill-tickets/badge", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, adminReq(req))
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "3")
}

// POST /skill-tickets/:id/messages:解析 JSON 文本消息并返回 message。
func TestSkillTicketsHandler_SendMessage(t *testing.T) {
	stub := &skillTicketServiceStub{}
	msgs := &skillTicketMessageServiceStub{textRes: service.SkillTicketMessageResult{ID: "m1", Kind: service.MessageKindText, Text: "补充"}}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	RegisterSkillTicketRoutes(r, NewSkillTicketsHandler(stub, msgs))

	body, _ := json.Marshal(SendSkillTicketMessageRequest{Text: "补充"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/skill-tickets/t1/messages", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, memberReq(req))

	assert.Equal(t, http.StatusCreated, rec.Code)
	assert.Equal(t, "t1", msgs.gotTextTicket)
	assert.Equal(t, "补充", msgs.gotText)
	assert.Contains(t, rec.Body.String(), `"message"`)
}

// POST /skill-tickets/:id/messages/upload:解析 multipart file 为单条 image/file 消息。
func TestSkillTicketsHandler_UploadMessage(t *testing.T) {
	stub := &skillTicketServiceStub{}
	msgs := &skillTicketMessageServiceStub{fileRes: service.SkillTicketMessageResult{ID: "m2", Kind: service.MessageKindFile, FileName: "spec.pdf"}}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	RegisterSkillTicketRoutes(r, NewSkillTicketsHandler(stub, msgs))

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fw, _ := w.CreateFormFile("file", "spec.pdf")
	_, _ = fw.Write([]byte("archive"))
	_ = w.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/v1/skill-tickets/t1/messages/upload", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, memberReq(req))

	assert.Equal(t, http.StatusCreated, rec.Code)
	assert.Equal(t, "t1", msgs.gotFileTicket)
	assert.Equal(t, "spec.pdf", msgs.gotFileName)
	assert.Equal(t, []byte("archive"), msgs.gotFileData)
}

// GET /skill-tickets/:id/messages/:msgId/download:流式回传消息文件并设置下载文件名。
func TestSkillTicketsHandler_DownloadMessage(t *testing.T) {
	stub := &skillTicketServiceStub{}
	msgs := &skillTicketMessageServiceStub{downloadBody: []byte("hello"), downloadName: "需求.pdf", downloadType: "application/pdf"}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	RegisterSkillTicketRoutes(r, NewSkillTicketsHandler(stub, msgs))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/skill-tickets/t1/messages/m1/download", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, memberReq(req))

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "hello", rec.Body.String())
	assert.Equal(t, "t1", msgs.gotFileTicket)
	assert.Equal(t, "m1", msgs.gotDownloadID)
	assert.Contains(t, rec.Header().Get("Content-Disposition"), "filename*=UTF-8''")
	assert.Equal(t, "application/pdf", rec.Header().Get("Content-Type"))
}

// POST /skill-tickets/:id/start 与 /reopen:分别映射开始制作和重新受理动作。
func TestSkillTicketsHandler_StartAndReopen(t *testing.T) {
	stub := &skillTicketServiceStub{}
	msgs := &skillTicketMessageServiceStub{}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	RegisterSkillTicketRoutes(r, NewSkillTicketsHandler(stub, msgs))

	startReq := httptest.NewRequest(http.MethodPost, "/api/v1/skill-tickets/t1/start", nil)
	startRec := httptest.NewRecorder()
	r.ServeHTTP(startRec, adminReq(startReq))
	assert.Equal(t, http.StatusNoContent, startRec.Code)
	assert.Equal(t, "t1", stub.startID)

	reopenReq := httptest.NewRequest(http.MethodPost, "/api/v1/skill-tickets/t2/reopen", strings.NewReader(""))
	reopenRec := httptest.NewRecorder()
	r.ServeHTTP(reopenRec, adminReq(reopenReq))
	assert.Equal(t, http.StatusNoContent, reopenRec.Code)
	assert.Equal(t, "t2", stub.reopenID)
}
