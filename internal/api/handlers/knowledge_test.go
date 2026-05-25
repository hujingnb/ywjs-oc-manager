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
	syncStatuses   []service.SyncStatusResult
	syncErr        error
	retryErr       error
	listOrgResult  service.KnowledgeListResult
	listOrgErr     error
	openOrgContent string
	openOrgSize    int64
	openOrgErr     error
	openOrgCalls   int
	openOrgID      string
	openOrgPath    string
	openOrgCloses  int
	saveOrgErr     error
	deleteOrgErr   error
	listAppResult  service.KnowledgeListResult
	listAppErr     error
	openAppContent string
	openAppSize    int64
	openAppErr     error
	openAppCalls   int
	openAppOrgID   string
	openAppID      string
	openAppOwner   string
	openAppPath    string
	openAppCloses  int
	saveAppErr     error
	deleteAppErr   error
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

func (s *knowledgeServiceStub) GetOrgSyncStatus(_ context.Context, _ auth.Principal, _ string) ([]service.SyncStatusResult, error) {
	return s.syncStatuses, s.syncErr
}

func (s *knowledgeServiceStub) RetryOrgNodeSync(_ context.Context, _ auth.Principal, _, _ string) error {
	return s.retryErr
}

func (s *knowledgeServiceStub) ListOrg(_ context.Context, _ auth.Principal, _, _ string) (service.KnowledgeListResult, error) {
	return s.listOrgResult, s.listOrgErr
}

func (s *knowledgeServiceStub) SaveOrgFile(_ context.Context, _ auth.Principal, _, _ string, _ io.Reader, _ int64) error {
	return s.saveOrgErr
}

func (s *knowledgeServiceStub) DeleteOrgFile(_ context.Context, _ auth.Principal, _, _ string) error {
	return s.deleteOrgErr
}

func (s *knowledgeServiceStub) OpenOrgFile(_ context.Context, _ auth.Principal, orgID, relative string) (io.ReadCloser, int64, error) {
	s.openOrgCalls++
	s.openOrgID = orgID
	s.openOrgPath = relative
	if s.openOrgErr != nil {
		return nil, 0, s.openOrgErr
	}
	return &trackedReadCloser{
		Buffer: bytes.NewBufferString(s.openOrgContent),
		onClose: func() {
			s.openOrgCloses++
		},
	}, s.openOrgSize, nil
}

func (s *knowledgeServiceStub) ListApp(_ context.Context, _ auth.Principal, _, _, _, _ string) (service.KnowledgeListResult, error) {
	return s.listAppResult, s.listAppErr
}

func (s *knowledgeServiceStub) SaveAppFile(_ context.Context, _ auth.Principal, _, _, _, _ string, _ io.Reader, _ int64) error {
	return s.saveAppErr
}

func (s *knowledgeServiceStub) DeleteAppFile(_ context.Context, _ auth.Principal, _, _, _, _ string) error {
	return s.deleteAppErr
}

func (s *knowledgeServiceStub) OpenAppFile(_ context.Context, _ auth.Principal, orgID, appID, ownerUserID, relative string) (io.ReadCloser, int64, error) {
	s.openAppCalls++
	s.openAppOrgID = orgID
	s.openAppID = appID
	s.openAppOwner = ownerUserID
	s.openAppPath = relative
	if s.openAppErr != nil {
		return nil, 0, s.openAppErr
	}
	return &trackedReadCloser{
		Buffer: bytes.NewBufferString(s.openAppContent),
		onClose: func() {
			s.openAppCloses++
		},
	}, s.openAppSize, nil
}

// newKnowledgeTestRouter 构建用于测试的 gin router。
func newKnowledgeTestRouter(t *testing.T, svc knowledgeService) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	RegisterKnowledgeRoutes(router, NewKnowledgeHandler(svc))
	return router
}

// TestKnowledgeGetOrgSyncStatusHappy 验证知识库获取组织同步状态成功路径的成功路径场景。
func TestKnowledgeGetOrgSyncStatusHappy(t *testing.T) {
	stub := &knowledgeServiceStub{syncStatuses: []service.SyncStatusResult{{NodeID: "n1", Status: "ok"}}}
	router := newKnowledgeTestRouter(t, stub)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/organizations/org-1/knowledge/sync-status", nil)
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgAdmin, OrgID: "org-1"})
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "statuses")
}

// TestKnowledgeGetOrgSyncStatusForbidden 验证知识库获取组织同步状态禁止访问的异常或拒绝路径场景。
func TestKnowledgeGetOrgSyncStatusForbidden(t *testing.T) {
	stub := &knowledgeServiceStub{syncErr: service.ErrKnowledgeForbidden}
	router := newKnowledgeTestRouter(t, stub)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/organizations/org-1/knowledge/sync-status", nil)
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgMember, OrgID: "org-1"})
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

// TestKnowledgeListOrgHappy 验证知识库列表组织成功路径的成功路径场景。
func TestKnowledgeListOrgHappy(t *testing.T) {
	stub := &knowledgeServiceStub{listOrgResult: service.KnowledgeListResult{}}
	router := newKnowledgeTestRouter(t, stub)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/organizations/org-1/knowledge", nil)
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgAdmin, OrgID: "org-1"})
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
}

// TestKnowledgeDownloadOrgHappy 验证组织知识库下载成功返回二进制内容。
func TestKnowledgeDownloadOrgHappy(t *testing.T) {
	stub := &knowledgeServiceStub{openOrgContent: "hello", openOrgSize: 5}
	router := newKnowledgeTestRouter(t, stub)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/organizations/org-1/knowledge/file?path=docs/readme.md", nil)
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgMember, OrgID: "org-1"})
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "application/octet-stream", w.Header().Get("Content-Type"))
	assert.Equal(t, "attachment; filename=readme.md", w.Header().Get("Content-Disposition"))
	assert.Equal(t, "5", w.Header().Get("Content-Length"))
	assert.Equal(t, "hello", w.Body.String())
	assert.Equal(t, 1, stub.openOrgCalls)
	assert.Equal(t, "org-1", stub.openOrgID)
	assert.Equal(t, "docs/readme.md", stub.openOrgPath)
	assert.Equal(t, 1, stub.openOrgCloses)
}

// TestKnowledgeDownloadOrgMissingPath 验证组织知识库下载缺少 path 时返回 400。
func TestKnowledgeDownloadOrgMissingPath(t *testing.T) {
	stub := &knowledgeServiceStub{}
	router := newKnowledgeTestRouter(t, stub)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/organizations/org-1/knowledge/file", nil)
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgMember, OrgID: "org-1"})
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Equal(t, 0, stub.openOrgCalls)
}

// TestKnowledgeSaveOrgHappy 验证知识库保存组织成功路径的成功路径场景。
func TestKnowledgeSaveOrgHappy(t *testing.T) {
	stub := &knowledgeServiceStub{}
	router := newKnowledgeTestRouter(t, stub)

	w := httptest.NewRecorder()
	body := bytes.NewBufferString("file content")
	req := httptest.NewRequest(http.MethodPost, "/api/v1/organizations/org-1/knowledge?path=docs/readme.txt", body)
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgAdmin, OrgID: "org-1"})
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNoContent, w.Code)
}

// TestKnowledgeSaveOrgMissingPath 验证知识库保存组织缺失路径的异常或拒绝路径场景。
func TestKnowledgeSaveOrgMissingPath(t *testing.T) {
	stub := &knowledgeServiceStub{}
	router := newKnowledgeTestRouter(t, stub)

	w := httptest.NewRecorder()
	body := bytes.NewBufferString("file content")
	// 缺少必填 path 参数
	req := httptest.NewRequest(http.MethodPost, "/api/v1/organizations/org-1/knowledge", body)
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgAdmin, OrgID: "org-1"})
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// TestKnowledgeRetryOrgSyncHappy 验证知识库重试组织同步成功路径的成功路径场景。
func TestKnowledgeRetryOrgSyncHappy(t *testing.T) {
	stub := &knowledgeServiceStub{}
	router := newKnowledgeTestRouter(t, stub)

	w := httptest.NewRecorder()
	body := bytes.NewBufferString(`{"node_id":"n1"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/organizations/org-1/knowledge/sync-status/retry", body)
	req.Header.Set("Content-Type", "application/json")
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgAdmin, OrgID: "org-1"})
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusAccepted, w.Code)
}

// TestKnowledgeListAppHappy 验证知识库列表应用成功路径的成功路径场景。
func TestKnowledgeListAppHappy(t *testing.T) {
	stub := &knowledgeServiceStub{listAppResult: service.KnowledgeListResult{}}
	router := newKnowledgeTestRouter(t, stub)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/apps/app-1/knowledge?org_id=org-1&owner_user_id=u1", nil)
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgMember, OrgID: "org-1"})
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
}

// TestKnowledgeDownloadAppHappy 验证实例知识库下载成功返回二进制内容。
func TestKnowledgeDownloadAppHappy(t *testing.T) {
	stub := &knowledgeServiceStub{openAppContent: "app", openAppSize: 3}
	router := newKnowledgeTestRouter(t, stub)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/apps/app-1/knowledge/file?org_id=org-1&owner_user_id=u1&path=app.md", nil)
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgMember, OrgID: "org-1"})
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "application/octet-stream", w.Header().Get("Content-Type"))
	assert.Equal(t, "attachment; filename=app.md", w.Header().Get("Content-Disposition"))
	assert.Equal(t, "3", w.Header().Get("Content-Length"))
	assert.Equal(t, "app", w.Body.String())
	assert.Equal(t, 1, stub.openAppCalls)
	assert.Equal(t, "org-1", stub.openAppOrgID)
	assert.Equal(t, "app-1", stub.openAppID)
	assert.Equal(t, "u1", stub.openAppOwner)
	assert.Equal(t, "app.md", stub.openAppPath)
	assert.Equal(t, 1, stub.openAppCloses)
}

// TestKnowledgeDownloadAppMissingParams 验证实例知识库下载缺少任一必填参数时返回 400。
func TestKnowledgeDownloadAppMissingParams(t *testing.T) {
	cases := []struct {
		name string
		url  string
	}{
		{name: "缺少 org_id", url: "/api/v1/apps/app-1/knowledge/file?owner_user_id=u1&path=app.md"},    // 场景：缺少组织归属参数。
		{name: "缺少 owner_user_id", url: "/api/v1/apps/app-1/knowledge/file?org_id=org-1&path=app.md"}, // 场景：缺少应用所有者参数。
		{name: "缺少 path", url: "/api/v1/apps/app-1/knowledge/file?org_id=org-1&owner_user_id=u1"},     // 场景：缺少文件路径参数。
	}
	for _, tc := range cases {
		// 当前子测试覆盖 tc.name 描述的实例知识库下载参数校验场景。
		t.Run(tc.name, func(t *testing.T) {
			stub := &knowledgeServiceStub{}
			router := newKnowledgeTestRouter(t, stub)

			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, tc.url, nil)
			req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgMember, OrgID: "org-1"})
			router.ServeHTTP(w, req)

			assert.Equal(t, http.StatusBadRequest, w.Code)
			assert.Equal(t, 0, stub.openAppCalls)
		})
	}
}

// TestKnowledgeListAppMissingParams 验证知识库列表应用缺失参数的异常或拒绝路径场景。
func TestKnowledgeListAppMissingParams(t *testing.T) {
	stub := &knowledgeServiceStub{}
	router := newKnowledgeTestRouter(t, stub)

	w := httptest.NewRecorder()
	// 缺少 owner_user_id
	req := httptest.NewRequest(http.MethodGet, "/api/v1/apps/app-1/knowledge?org_id=org-1", nil)
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgMember, OrgID: "org-1"})
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}
