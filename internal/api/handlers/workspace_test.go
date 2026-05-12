package handlers

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/service"
)

// workspaceServiceStub 实现 workspaceService 接口，仅 stub 测试用到的方法。
type workspaceServiceStub struct {
	listResult  service.WorkspaceListing
	listErr     error
	downloadRC  io.ReadCloser
	downloadErr error
	archiveErr  error
}

func (s *workspaceServiceStub) List(_ context.Context, _ auth.Principal, _, _ string) (service.WorkspaceListing, error) {
	return s.listResult, s.listErr
}

func (s *workspaceServiceStub) Download(_ context.Context, _ auth.Principal, _, _ string) (io.ReadCloser, error) {
	return s.downloadRC, s.downloadErr
}

func (s *workspaceServiceStub) Archive(_ context.Context, _ auth.Principal, _, _ string, _ io.Writer) error {
	return s.archiveErr
}

// newWorkspaceTestRouter 构建用于测试的 gin router + token manager。
func newWorkspaceTestRouter(t *testing.T, svc workspaceService) (*gin.Engine, *auth.TokenManager) {
	t.Helper()
	gin.SetMode(gin.ReleaseMode)
	tokens, err := auth.NewTokenManager("a", "b", time.Minute, time.Hour)
	require.NoError(t, err)
	router := gin.New()
	RegisterWorkspaceRoutes(router, NewWorkspaceHandler(svc, tokens))
	return router, tokens
}

// TestWorkspaceListHappy 验证工作区列表成功路径的成功路径场景。
func TestWorkspaceListHappy(t *testing.T) {
	stub := &workspaceServiceStub{listResult: service.WorkspaceListing{Path: "/", Entries: []service.WorkspaceEntryResult{}}}
	router, tokens := newWorkspaceTestRouter(t, stub)
	token := mustSignAccess(t, tokens, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgMember, OrgID: "org-1"})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/apps/app-1/workspace", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
}

// TestWorkspaceListForbidden 验证工作区列表禁止访问的异常或拒绝路径场景。
func TestWorkspaceListForbidden(t *testing.T) {
	stub := &workspaceServiceStub{listErr: service.ErrWorkspaceForbidden}
	router, tokens := newWorkspaceTestRouter(t, stub)
	token := mustSignAccess(t, tokens, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgMember, OrgID: "org-1"})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/apps/app-2/workspace", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

// TestWorkspaceListNotFound 验证工作区列表未找到的异常或拒绝路径场景。
func TestWorkspaceListNotFound(t *testing.T) {
	stub := &workspaceServiceStub{listErr: service.ErrNotFound}
	router, tokens := newWorkspaceTestRouter(t, stub)
	token := mustSignAccess(t, tokens, auth.Principal{UserID: "u1", Role: domain.UserRolePlatformAdmin})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/apps/missing/workspace", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

// TestWorkspaceDownloadHappy 验证工作区下载成功路径的成功路径场景。
func TestWorkspaceDownloadHappy(t *testing.T) {
	// 返回一个可读的 io.ReadCloser
	rc := io.NopCloser(strings.NewReader("file content"))
	stub := &workspaceServiceStub{downloadRC: rc}
	router, tokens := newWorkspaceTestRouter(t, stub)
	token := mustSignAccess(t, tokens, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgMember, OrgID: "org-1"})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/apps/app-1/workspace/file?path=readme.txt", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "file content", w.Body.String())
}

// TestWorkspaceDownloadMissingPath 验证工作区下载缺失路径的异常或拒绝路径场景。
func TestWorkspaceDownloadMissingPath(t *testing.T) {
	stub := &workspaceServiceStub{}
	router, tokens := newWorkspaceTestRouter(t, stub)
	token := mustSignAccess(t, tokens, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgMember, OrgID: "org-1"})

	w := httptest.NewRecorder()
	// 缺少必填 path 参数
	req := httptest.NewRequest(http.MethodGet, "/api/v1/apps/app-1/workspace/file", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// TestWorkspaceRequiresToken 验证工作区要求令牌的预期行为场景。
func TestWorkspaceRequiresToken(t *testing.T) {
	stub := &workspaceServiceStub{}
	router, _ := newWorkspaceTestRouter(t, stub)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/apps/app-1/workspace", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}
