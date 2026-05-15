package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/store/sqlc"
)

// jobsStoreStub 实现 JobsStore 接口，仅 stub 测试用到的方法。
type jobsStoreStub struct {
	job    sqlc.Job
	jobErr error
}

func (s *jobsStoreStub) GetJob(_ context.Context, _ pgtype.UUID) (sqlc.Job, error) {
	return s.job, s.jobErr
}

// newJobsTestRouter 构建用于测试的 gin router。
func newJobsTestRouter(t *testing.T, store JobsStore) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	RegisterJobsRoutes(router, NewJobsHandler(store))
	return router
}

// makeTestUUID 构建一个有效的 UUID 字符串（全零）。
const testJobUUID = "00000000-0000-0000-0000-000000000001"

func makeTestJob() sqlc.Job {
	var id pgtype.UUID
	_ = id.Scan(testJobUUID)
	return sqlc.Job{
		ID:          id,
		Type:        "delete_member",
		Status:      "pending",
		Attempts:    0,
		MaxAttempts: 3,
	}
}

// TestJobsGetHappy 验证任务获取成功路径的成功路径场景。
func TestJobsGetHappy(t *testing.T) {
	stub := &jobsStoreStub{job: makeTestJob()}
	router := newJobsTestRouter(t, stub)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/"+testJobUUID, nil)
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRolePlatformAdmin})
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "job")
}

// TestJobsGetForbidden 验证任务获取禁止访问的异常或拒绝路径场景。
func TestJobsGetForbidden(t *testing.T) {
	stub := &jobsStoreStub{job: makeTestJob()}
	router := newJobsTestRouter(t, stub)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/"+testJobUUID, nil)
	// 非平台管理员被 handler 直接拒绝
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgAdmin, OrgID: "org-1"})
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

// TestJobsGetNotFound 验证任务获取未找到的异常或拒绝路径场景。
func TestJobsGetNotFound(t *testing.T) {
	stub := &jobsStoreStub{jobErr: pgx.ErrNoRows}
	router := newJobsTestRouter(t, stub)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/"+testJobUUID, nil)
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRolePlatformAdmin})
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

// TestJobsGetInvalidUUID 验证任务获取非法UUID的异常或拒绝路径场景。
func TestJobsGetInvalidUUID(t *testing.T) {
	stub := &jobsStoreStub{}
	router := newJobsTestRouter(t, stub)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/not-a-uuid", nil)
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRolePlatformAdmin})
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}
