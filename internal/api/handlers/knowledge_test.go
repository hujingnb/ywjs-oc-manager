package handlers

import (
	"bytes"
	"context"
	"io"
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

// knowledgeServiceStub 实现 knowledgeService 接口，仅 stub 测试用到的方法。
type knowledgeServiceStub struct {
	listOrgResult service.KnowledgeListResult
	listOrgErr    error
	saveOrgResult service.KnowledgeDocumentResult
	saveOrgErr    error
	reparseOrgErr error
	openContent   string
	openSize      int64
	openName      string
	openErr       error
	openCloses    int
}

func (s *knowledgeServiceStub) ListOrg(_ context.Context, _ auth.Principal, _ string, _, _ int32, _, _ string) (service.KnowledgeListResult, error) {
	return s.listOrgResult, s.listOrgErr
}

func (s *knowledgeServiceStub) SaveOrgFile(_ context.Context, _ auth.Principal, _, _ string, _ io.Reader, _ int64) (service.KnowledgeDocumentResult, error) {
	return s.saveOrgResult, s.saveOrgErr
}

func (s *knowledgeServiceStub) OpenOrgFile(_ context.Context, _ auth.Principal, _, _ string) (io.ReadCloser, int64, string, error) {
	if s.openErr != nil {
		return nil, 0, "", s.openErr
	}
	return &trackedReadCloser{Buffer: bytes.NewBufferString(s.openContent), onClose: func() { s.openCloses++ }}, s.openSize, s.openName, nil
}

func (s *knowledgeServiceStub) DeleteOrgFile(_ context.Context, _ auth.Principal, _, _ string) error {
	return nil
}

func (s *knowledgeServiceStub) ReparseOrgFile(_ context.Context, _ auth.Principal, _, _ string) (service.KnowledgeDocumentResult, error) {
	return service.KnowledgeDocumentResult{}, s.reparseOrgErr
}

func (s *knowledgeServiceStub) ListApp(context.Context, auth.Principal, string, int32, int32, string, string) (service.KnowledgeListResult, error) {
	return service.KnowledgeListResult{}, nil
}

func (s *knowledgeServiceStub) SaveAppFile(context.Context, auth.Principal, string, string, io.Reader, int64) (service.KnowledgeDocumentResult, error) {
	return service.KnowledgeDocumentResult{}, nil
}

func (s *knowledgeServiceStub) OpenAppFile(context.Context, auth.Principal, string, string) (io.ReadCloser, int64, string, error) {
	return io.NopCloser(bytes.NewBuffer(nil)), 0, "file.md", nil
}

func (s *knowledgeServiceStub) DeleteAppFile(context.Context, auth.Principal, string, string) error {
	return nil
}

func (s *knowledgeServiceStub) ReparseAppFile(context.Context, auth.Principal, string, string) (service.KnowledgeDocumentResult, error) {
	return service.KnowledgeDocumentResult{}, nil
}

// trackedReadCloser 用于测试下载流在响应写出后是否被负责写响应的代码关闭。
type trackedReadCloser struct {
	*bytes.Buffer
	onClose func()
}

func (r *trackedReadCloser) Close() error {
	if r.onClose != nil {
		r.onClose()
	}
	return nil
}

func newKnowledgeTestRouter(t *testing.T, svc knowledgeService) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	RegisterKnowledgeRoutes(router, NewKnowledgeHandler(svc))
	return router
}

// TestKnowledgeListOrgFlatContract 验证知识库列表返回扁平 items/total 契约，不再返回旧文件树字段。
func TestKnowledgeListOrgFlatContract(t *testing.T) {
	stub := &knowledgeServiceStub{listOrgResult: service.KnowledgeListResult{
		Items: []service.KnowledgeDocumentResult{{ID: "doc-1", Name: "report.md", ParseStatus: "completed"}},
		Total: 1,
	}}
	router := newKnowledgeTestRouter(t, stub)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/organizations/org-1/knowledge", nil)
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgMember, OrgID: "org-1"})
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"items"`)
	assert.Contains(t, w.Body.String(), `"total"`)
	assert.NotContains(t, w.Body.String(), `"path"`)
	assert.NotContains(t, w.Body.String(), `"entries"`)
}

// TestKnowledgeUploadOrgReturnsDocument 验证上传文件后返回 202 和 queued document 视图。
func TestKnowledgeUploadOrgReturnsDocument(t *testing.T) {
	stub := &knowledgeServiceStub{saveOrgResult: service.KnowledgeDocumentResult{ID: "doc-1", Name: "report.md", ParseStatus: "queued"}}
	router := newKnowledgeTestRouter(t, stub)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/organizations/org-1/knowledge?filename=report.md", bytes.NewBufferString("content"))
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgAdmin, OrgID: "org-1"})
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusAccepted, w.Code)
	assert.Contains(t, w.Body.String(), `"id":"doc-1"`)
	assert.Contains(t, w.Body.String(), `"parse_status":"queued"`)
}

// TestKnowledgeUploadOrgRequiresFilename 验证上传缺少 filename 时在 handler 层直接返回 400。
func TestKnowledgeUploadOrgRequiresFilename(t *testing.T) {
	stub := &knowledgeServiceStub{}
	router := newKnowledgeTestRouter(t, stub)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/organizations/org-1/knowledge", bytes.NewBufferString("content"))
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgAdmin, OrgID: "org-1"})
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// TestKnowledgeReparseOrgRequiresDocumentID 验证重解析必须通过路由携带 documentId，未知 document 映射为 404。
func TestKnowledgeReparseOrgRequiresDocumentID(t *testing.T) {
	stub := &knowledgeServiceStub{reparseOrgErr: service.ErrNotFound}
	router := newKnowledgeTestRouter(t, stub)

	missingRoute := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/organizations/org-1/knowledge//reparse", nil)
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgAdmin, OrgID: "org-1"})
	router.ServeHTTP(missingRoute, req)
	assert.Equal(t, http.StatusNotFound, missingRoute.Code)

	unknownDoc := httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/v1/organizations/org-1/knowledge/doc-404/reparse", nil)
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgAdmin, OrgID: "org-1"})
	router.ServeHTTP(unknownDoc, req)
	assert.Equal(t, http.StatusNotFound, unknownDoc.Code)
}

// TestKnowledgeSyncRoutesRemoved 验证旧同步状态路由已从知识库管理面移除。
func TestKnowledgeSyncRoutesRemoved(t *testing.T) {
	router := newKnowledgeTestRouter(t, &knowledgeServiceStub{})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/organizations/org-1/knowledge/sync-status", nil)
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgAdmin, OrgID: "org-1"})
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

// TestKnowledgeDownloadOrgUsesServiceFilename 验证下载文件名来自 service 返回的 document 元数据，而不是用户输入。
func TestKnowledgeDownloadOrgUsesServiceFilename(t *testing.T) {
	stub := &knowledgeServiceStub{openContent: "hello", openSize: 5, openName: "report.md"}
	router := newKnowledgeTestRouter(t, stub)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/organizations/org-1/knowledge/doc-1/file", nil)
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgMember, OrgID: "org-1"})
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "attachment; filename=report.md", w.Header().Get("Content-Disposition"))
	assert.Equal(t, "hello", w.Body.String())
	assert.Equal(t, 1, stub.openCloses)
}
