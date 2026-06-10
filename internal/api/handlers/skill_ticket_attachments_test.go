package handlers

import (
	"bytes"
	"context"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"

	"oc-manager/internal/auth"
	"oc-manager/internal/service"
)

// skillTicketAttachmentServiceStub 实现附件 handler 依赖的接口,隔离 HTTP 层测试。
type skillTicketAttachmentServiceStub struct {
	addRes      service.SkillTicketAttachmentResult
	addErr      error
	gotTicketID string
	gotFileName string
	gotData     []byte
	listRes     []service.SkillTicketAttachmentResult
	openBody    string
	openName    string
	openErr     error
}

// Add 记录入参并返回预设结果,用于验证上传 handler 的 multipart 解析与前置可见性放行。
func (s *skillTicketAttachmentServiceStub) Add(_ context.Context, _ auth.Principal, ticketID, fileName string, data []byte) (service.SkillTicketAttachmentResult, error) {
	s.gotTicketID, s.gotFileName, s.gotData = ticketID, fileName, data
	return s.addRes, s.addErr
}

// List 返回预设列表,用于列表路径测试。
func (s *skillTicketAttachmentServiceStub) List(context.Context, string) ([]service.SkillTicketAttachmentResult, error) {
	return s.listRes, nil
}

// Open 返回预设内容(字符串 reader)与文件名,用于下载路径测试;记录透传的 ticketID 供断言。
func (s *skillTicketAttachmentServiceStub) Open(_ context.Context, ticketID, _ string) (io.ReadCloser, string, error) {
	s.gotTicketID = ticketID
	if s.openErr != nil {
		return nil, "", s.openErr
	}
	return io.NopCloser(strings.NewReader(s.openBody)), s.openName, nil
}

// ticketViewerStub 实现可见性前置接口:Get 返回预设结果或拒绝错误,模拟工单可见 / 不可见。
type ticketViewerStub struct {
	err error
}

// Get 按预设返回成功或 ErrSkillTicketDenied,驱动 handler 的可见性前置分支。
func (s *ticketViewerStub) Get(context.Context, auth.Principal, string) (service.SkillTicketDetailResult, error) {
	return service.SkillTicketDetailResult{}, s.err
}

// TestSkillTicketAttachments_Upload_Visible 验证工单可见时上传成功:
// 前置 Get 放行 → handler 解析 multipart file → Add 收到正确入参 → 返回 201。
func TestSkillTicketAttachments_Upload_Visible(t *testing.T) {
	// 工单可见(Get 返回 nil),附件 service 返回合法结果
	att := &skillTicketAttachmentServiceStub{addRes: service.SkillTicketAttachmentResult{ID: "a1", FileName: "spec.pdf", FileSize: 7}}
	viewer := &ticketViewerStub{}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	RegisterSkillTicketAttachmentRoutes(r, NewSkillTicketAttachmentsHandler(att, viewer))

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fw, _ := w.CreateFormFile("file", "spec.pdf")
	_, _ = fw.Write([]byte("archive"))
	_ = w.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/v1/skill-tickets/t1/attachments", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, memberReq(req))

	// 可见 → 上传成功 201,且 service 收到正确 ticketID/文件名/内容
	assert.Equal(t, http.StatusCreated, rec.Code)
	assert.Equal(t, "t1", att.gotTicketID)
	assert.Equal(t, "spec.pdf", att.gotFileName)
	assert.Equal(t, []byte("archive"), att.gotData)
}

// TestSkillTicketAttachments_Upload_Denied 验证工单不可见时上传被拒:
// 前置 Get 返回 ErrSkillTicketDenied → handler 直接 403,不调用附件 service。
func TestSkillTicketAttachments_Upload_Denied(t *testing.T) {
	// 工单不可见(Get 返回 Denied)
	att := &skillTicketAttachmentServiceStub{}
	viewer := &ticketViewerStub{err: service.ErrSkillTicketDenied}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	RegisterSkillTicketAttachmentRoutes(r, NewSkillTicketAttachmentsHandler(att, viewer))

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fw, _ := w.CreateFormFile("file", "spec.pdf")
	_, _ = fw.Write([]byte("archive"))
	_ = w.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/v1/skill-tickets/t1/attachments", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, memberReq(req))

	// 不可见 → 403,且附件 service 未被调用(ticketID 仍为空)
	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.Empty(t, att.gotTicketID)
}

// TestSkillTicketAttachments_Download 验证下载命中:
// 前置 Get 放行 → Open 返回内容 → handler 200 流式回传 body 与 Content-Disposition。
func TestSkillTicketAttachments_Download(t *testing.T) {
	// 工单可见,附件内容为 "hello"
	att := &skillTicketAttachmentServiceStub{openBody: "hello", openName: "需求.pdf"}
	viewer := &ticketViewerStub{}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	RegisterSkillTicketAttachmentRoutes(r, NewSkillTicketAttachmentsHandler(att, viewer))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/skill-tickets/t1/attachments/a1/download", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, memberReq(req))

	// 命中 → 200 + body,且带 UTF-8 文件名的 Content-Disposition
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "hello", rec.Body.String())
	assert.Contains(t, rec.Header().Get("Content-Disposition"), "filename*=UTF-8''")
	// path 中的工单 id "t1" 必须透传给 Open 做归属校验
	assert.Equal(t, "t1", att.gotTicketID)
}
