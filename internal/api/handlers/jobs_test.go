package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

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

// newJobsTestRouter 构建用于测试的 gin router + token manager。
func newJobsTestRouter(t *testing.T, store JobsStore) (*gin.Engine, *auth.TokenManager) {
	t.Helper()
	gin.SetMode(gin.ReleaseMode)
	tokens, err := auth.NewTokenManager("a", "b", time.Minute, time.Hour)
	require.NoError(t, err)
	router := gin.New()
	RegisterJobsRoutes(router, NewJobsHandler(store, tokens))
	return router, tokens
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

func TestJobsGetHappy(t *testing.T) {
	stub := &jobsStoreStub{job: makeTestJob()}
	router, tokens := newJobsTestRouter(t, stub)
	token := mustSignAccess(t, tokens, auth.Principal{UserID: "u1", Role: domain.UserRolePlatformAdmin})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/"+testJobUUID, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "job")
}

func TestJobsGetForbidden(t *testing.T) {
	stub := &jobsStoreStub{job: makeTestJob()}
	router, tokens := newJobsTestRouter(t, stub)
	// 非平台管理员被 handler 直接拒绝
	token := mustSignAccess(t, tokens, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgAdmin, OrgID: "org-1"})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/"+testJobUUID, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestJobsGetNotFound(t *testing.T) {
	stub := &jobsStoreStub{jobErr: pgx.ErrNoRows}
	router, tokens := newJobsTestRouter(t, stub)
	token := mustSignAccess(t, tokens, auth.Principal{UserID: "u1", Role: domain.UserRolePlatformAdmin})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/"+testJobUUID, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestJobsGetRequiresToken(t *testing.T) {
	stub := &jobsStoreStub{}
	router, _ := newJobsTestRouter(t, stub)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/"+testJobUUID, nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestJobsGetInvalidUUID(t *testing.T) {
	stub := &jobsStoreStub{}
	router, tokens := newJobsTestRouter(t, stub)
	token := mustSignAccess(t, tokens, auth.Principal{UserID: "u1", Role: domain.UserRolePlatformAdmin})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/not-a-uuid", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}
