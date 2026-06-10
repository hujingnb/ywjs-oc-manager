package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"

	"oc-manager/internal/auth"
	"oc-manager/internal/service"
)

// customSkillServiceStub 实现交付 handler 依赖的接口,隔离 HTTP 层测试。
type customSkillServiceStub struct {
	deliverRes service.CustomSkillResult
	deliverErr error
	gotInput   service.DeliverCustomSkillInput
}

// Deliver 记录实际入参并返回预设结果,用于验证 handler 对 multipart 字段(ticket_id/targets/file)的解析。
func (s *customSkillServiceStub) Deliver(_ context.Context, _ auth.Principal, in service.DeliverCustomSkillInput) (service.CustomSkillResult, error) {
	s.gotInput = in
	return s.deliverRes, s.deliverErr
}

// TestCustomSkillsHandler_Deliver 验证 POST multipart 交付:
// handler 正确解析 ticket_id/description/targets(JSON 数组)/file 字段,成功时返回 201 与 service 结果。
func TestCustomSkillsHandler_Deliver(t *testing.T) {
	// 预设 service 返回一个合法交付结果
	stub := &customSkillServiceStub{deliverRes: service.CustomSkillResult{ID: "cs1", Name: "weekly", Version: "20260610-000000", TicketID: "t1"}}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	RegisterCustomSkillRoutes(r, NewCustomSkillsHandler(stub))

	// 构造 multipart 请求体:ticket_id/description/targets(JSON)+ file 归档
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	_ = w.WriteField("ticket_id", "t1")
	_ = w.WriteField("description", "周报技能")
	targets, _ := json.Marshal([]CustomSkillTargetDTO{{OrgID: "org-1", Audience: "all_org"}})
	_ = w.WriteField("targets", string(targets))
	fw, _ := w.CreateFormFile("file", "weekly.tar")
	_, _ = fw.Write([]byte("archive"))
	_ = w.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/v1/custom-skills/deliver", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, adminReq(req))

	// 交付成功应返回 201,且 service 收到正确解析后的入参
	assert.Equal(t, http.StatusCreated, rec.Code)
	assert.Equal(t, "t1", stub.gotInput.TicketID)
	assert.Equal(t, "周报技能", stub.gotInput.Description)
	assert.Equal(t, []byte("archive"), stub.gotInput.Data)
	assert.Len(t, stub.gotInput.Targets, 1)
	assert.Equal(t, "org-1", stub.gotInput.Targets[0].OrgID)
	assert.Equal(t, "all_org", stub.gotInput.Targets[0].Audience)
}

// TestCustomSkillsHandler_Deliver_NameMismatch 验证交付时技能名与工单锁定不一致:
// handler 将 ErrCustomSkillNameMismatch 正确映射为 409 Conflict。
func TestCustomSkillsHandler_Deliver_NameMismatch(t *testing.T) {
	// service 返回技能名不一致哨兵错误
	stub := &customSkillServiceStub{deliverErr: service.ErrCustomSkillNameMismatch}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	RegisterCustomSkillRoutes(r, NewCustomSkillsHandler(stub))

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	_ = w.WriteField("ticket_id", "t1")
	targets, _ := json.Marshal([]CustomSkillTargetDTO{{OrgID: "org-1", Audience: "all_org"}})
	_ = w.WriteField("targets", string(targets))
	fw, _ := w.CreateFormFile("file", "weekly.tar")
	_, _ = fw.Write([]byte("archive"))
	_ = w.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/v1/custom-skills/deliver", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, adminReq(req))

	// 技能名不一致时应返回 409
	assert.Equal(t, http.StatusConflict, rec.Code)
}
