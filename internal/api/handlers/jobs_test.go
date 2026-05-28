package handlers

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/store/sqlc"
)

// jobsStoreStub 实现 JobsStore 接口，仅 stub 测试用到的方法。
// ID 字段迁移为 string（MySQL uuid），GetJob/GetApp 均接受 string 参数。
type jobsStoreStub struct {
	job    sqlc.Job
	jobErr error
	app    sqlc.App
	appErr error
}

func (s *jobsStoreStub) GetJob(_ context.Context, _ string) (sqlc.Job, error) {
	return s.job, s.jobErr
}

func (s *jobsStoreStub) GetApp(_ context.Context, _ string) (sqlc.App, error) {
	return s.app, s.appErr
}


// newJobsTestRouter 构建用于测试的 gin router。
func newJobsTestRouter(t *testing.T, store JobsStore) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	RegisterJobsRoutes(router, NewJobsHandler(store))
	return router
}

// testJobUUID 构建一个有效的 UUID 字符串（全零）。
const testJobUUID = "00000000-0000-0000-0000-000000000001"

// makeTestJob 返回一个测试用 sqlc.Job，ID 为 string（MySQL uuid）。
func makeTestJob() sqlc.Job {
	return sqlc.Job{
		ID:          testJobUUID,
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

// TestJobsGetNotFound 验证任务获取未找到的异常或拒绝路径场景。
func TestJobsGetNotFound(t *testing.T) {
	stub := &jobsStoreStub{jobErr: sql.ErrNoRows}
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

// makeAppOwnedBy 构造一个测试用 sqlc.App，组织和 owner 由参数指定。
// OrgID / OwnerUserID 现为 string（MySQL uuid）。
func makeAppOwnedBy(orgID, ownerID string) sqlc.App {
	return sqlc.App{OrgID: orgID, OwnerUserID: ownerID}
}

// makeJobWithAppID 构造一个 payload.app_id 已写入的 sqlc.Job。
func makeJobWithAppID(appID string) sqlc.Job {
	job := makeTestJob()
	job.PayloadJson = []byte(`{"app_id":"` + appID + `"}`)
	return job
}

// TestJobsGetOrgAdminAllowsOwnOrg 验证 org_admin 可以查看本组织 app 关联 job。
// 业务目的：用户在概览页点击立即重启后，JobProgressPanel 必须能正常拉到进度。
func TestJobsGetOrgAdminAllowsOwnOrg(t *testing.T) {
	const appUUID = "11111111-1111-1111-1111-111111111111"
	const orgUUID = "22222222-2222-2222-2222-222222222222"
	const ownerUUID = "33333333-3333-3333-3333-333333333333"

	stub := &jobsStoreStub{
		job: makeJobWithAppID(appUUID),
		app: makeAppOwnedBy(orgUUID, ownerUUID),
	}
	router := newJobsTestRouter(t, stub)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/"+testJobUUID, nil)
	// org_admin + 同 orgID：CanViewApp 应放行。
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgAdmin, OrgID: orgUUID})
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "job")
}

// TestJobsGetPlatformAdminBypassesPayload 验证平台管理员即使 payload 无 app_id 也能查。
// 业务目的：保留平台管理员对所有 job（含将来可能出现的纯组织/平台级 job）的运维可见性。
func TestJobsGetPlatformAdminBypassesPayload(t *testing.T) {
	stub := &jobsStoreStub{job: makeTestJob()} // payload 为零值字节，无 app_id
	router := newJobsTestRouter(t, stub)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/"+testJobUUID, nil)
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRolePlatformAdmin})
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
}

// TestJobsGetOrgMemberAllowsOwnedApp 验证 org_member 可以查看自己拥有的 app 关联 job。
// 业务目的：成员触发自己实例的重启后，必须能看到 JobProgressPanel 进度。
func TestJobsGetOrgMemberAllowsOwnedApp(t *testing.T) {
	const appUUID = "11111111-1111-1111-1111-111111111111"
	const orgUUID = "22222222-2222-2222-2222-222222222222"
	const ownerUUID = "33333333-3333-3333-3333-333333333333"

	stub := &jobsStoreStub{
		job: makeJobWithAppID(appUUID),
		app: makeAppOwnedBy(orgUUID, ownerUUID),
	}
	router := newJobsTestRouter(t, stub)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/"+testJobUUID, nil)
	// org_member + 同 ownerUUID：CanViewApp 应放行。
	req = withPrincipal(req, auth.Principal{UserID: ownerUUID, Role: domain.UserRoleOrgMember, OrgID: orgUUID})
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
}

// TestJobsGetOrgAdminBlocksOtherOrg 验证 org_admin 看不到别组织 app 关联 job。
// 业务目的：保留 CanViewApp 的跨组织隔离语义，避免通过 job_id 探测别组织运维状态。
func TestJobsGetOrgAdminBlocksOtherOrg(t *testing.T) {
	const appUUID = "11111111-1111-1111-1111-111111111111"
	const appOrgUUID = "22222222-2222-2222-2222-222222222222"
	const ownerUUID = "33333333-3333-3333-3333-333333333333"
	const otherOrgUUID = "44444444-4444-4444-4444-444444444444"

	stub := &jobsStoreStub{
		job: makeJobWithAppID(appUUID),
		app: makeAppOwnedBy(appOrgUUID, ownerUUID),
	}
	router := newJobsTestRouter(t, stub)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/"+testJobUUID, nil)
	// org_admin 但 orgID 不是 app 所在组织：CanViewApp 拒绝。
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgAdmin, OrgID: otherOrgUUID})
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

// TestJobsGetOrgMemberBlocksOtherOwnersApp 验证 org_member 看不到同组织内别人拥有的 app 关联 job。
// 业务目的：org_member 仅能看自己拥有的 app；同组织内借 job_id 窥探他人实例运维状态应被拒绝。
func TestJobsGetOrgMemberBlocksOtherOwnersApp(t *testing.T) {
	const appUUID = "11111111-1111-1111-1111-111111111111"
	const orgUUID = "22222222-2222-2222-2222-222222222222"
	const otherOwnerUUID = "33333333-3333-3333-3333-333333333333"

	stub := &jobsStoreStub{
		job: makeJobWithAppID(appUUID),
		app: makeAppOwnedBy(orgUUID, otherOwnerUUID),
	}
	router := newJobsTestRouter(t, stub)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/"+testJobUUID, nil)
	// org_member 在同组织但 UserID 不是 app owner：CanViewApp 拒绝。
	req = withPrincipal(req, auth.Principal{UserID: "u-self", Role: domain.UserRoleOrgMember, OrgID: orgUUID})
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

// TestJobsGetNonAdminWithoutPayloadAppID 验证 payload 无 app_id 时非平台管理员被拒绝。
// 业务目的：兜底保护——未来若出现纯组织/平台级 job，避免因为 payload 字段缺失被误放行。
func TestJobsGetNonAdminWithoutPayloadAppID(t *testing.T) {
	stub := &jobsStoreStub{job: makeTestJob()} // payload 为空 []byte，反序列化得到 AppID=""
	router := newJobsTestRouter(t, stub)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/"+testJobUUID, nil)
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgAdmin, OrgID: "org-x"})
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

// TestJobsGetInvalidPayloadAppID 验证 payload.app_id 不是合法 UUID 时返回 404。
// 业务目的：payload 内存放脏数据时按未找到处理，避免泄漏 payload 结构细节给探测者。
func TestJobsGetInvalidPayloadAppID(t *testing.T) {
	stub := &jobsStoreStub{}
	stub.job = makeTestJob()
	stub.job.PayloadJson = []byte(`{"app_id":"not-a-uuid"}`)
	router := newJobsTestRouter(t, stub)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/"+testJobUUID, nil)
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgAdmin, OrgID: "org-1"})
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

// TestJobsGetAppNotFound 验证 payload.app_id 合法但 app 已被删除时返回 404。
// 业务目的：app 已被真删除而 job 仍存留是脏数据场景，统一按未找到处理。
func TestJobsGetAppNotFound(t *testing.T) {
	const appUUID = "11111111-1111-1111-1111-111111111111"
	stub := &jobsStoreStub{
		job:    makeJobWithAppID(appUUID),
		appErr: sql.ErrNoRows,
	}
	router := newJobsTestRouter(t, stub)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/"+testJobUUID, nil)
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgAdmin, OrgID: "org-1"})
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

// TestJobsGetAppLookupError 验证 GetApp 返回非 NotFound 错误时返回 500。
// 业务目的：DB 故障等底层错误暴露为 500，避免被误判为权限或资源缺失。
func TestJobsGetAppLookupError(t *testing.T) {
	const appUUID = "11111111-1111-1111-1111-111111111111"
	stub := &jobsStoreStub{
		job:    makeJobWithAppID(appUUID),
		appErr: errors.New("db boom"),
	}
	router := newJobsTestRouter(t, stub)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/"+testJobUUID, nil)
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgAdmin, OrgID: "org-1"})
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}
