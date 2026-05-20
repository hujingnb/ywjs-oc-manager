package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/service"
)

// cronServiceStub 是 hermesCronService 的可控 stub，用于验证 handler 的 HTTP 绑定、
// 角色过滤和稳定响应结构，不依赖真实 runtime 容器。
type cronServiceStub struct {
	job      service.CronJob
	jobs     []service.CronJob
	status   service.CronStatus
	caps     service.CronCapabilities
	runs     []service.CronRunEntry
	output   service.CronRunOutput
	createIn service.CreateCronJobInput // 记录 CreateJob 最后一次入参，验证 handler 过滤逻辑。
	updateIn service.UpdateCronJobInput // 记录 UpdateJob 最后一次入参，便于后续扩展更新端点测试。
	updated  bool                       // 记录 UpdateJob 是否被调用，验证 handler 层前置拒绝逻辑。
	err      error
}

// Capabilities 返回预设能力或错误。
func (s *cronServiceStub) Capabilities(_ context.Context, _ auth.Principal, _ string) (service.CronCapabilities, error) {
	return s.caps, s.err
}

// Status 返回预设调度器状态或错误。
func (s *cronServiceStub) Status(_ context.Context, _ auth.Principal, _ string) (service.CronStatus, error) {
	return s.status, s.err
}

// ListJobs 返回预设任务列表或错误。
func (s *cronServiceStub) ListJobs(_ context.Context, _ auth.Principal, _ string, _ service.CronJobFilter) ([]service.CronJob, error) {
	return s.jobs, s.err
}

// ShowJob 返回预设任务或错误。
func (s *cronServiceStub) ShowJob(_ context.Context, _ auth.Principal, _, _ string) (service.CronJob, error) {
	return s.job, s.err
}

// CreateJob 记录入参并返回预设任务或错误。
func (s *cronServiceStub) CreateJob(_ context.Context, _ auth.Principal, _ string, in service.CreateCronJobInput) (service.CronJob, error) {
	s.createIn = in
	return s.job, s.err
}

// UpdateJob 记录入参并返回预设任务或错误。
func (s *cronServiceStub) UpdateJob(_ context.Context, _ auth.Principal, _, _ string, in service.UpdateCronJobInput) (service.CronJob, error) {
	s.updated = true
	s.updateIn = in
	return s.job, s.err
}

// DeleteJob 返回预设错误。
func (s *cronServiceStub) DeleteJob(_ context.Context, _ auth.Principal, _, _ string) error {
	return s.err
}

// PauseJob 返回预设任务或错误。
func (s *cronServiceStub) PauseJob(_ context.Context, _ auth.Principal, _, _ string) (service.CronJob, error) {
	return s.job, s.err
}

// ResumeJob 返回预设任务或错误。
func (s *cronServiceStub) ResumeJob(_ context.Context, _ auth.Principal, _, _ string) (service.CronJob, error) {
	return s.job, s.err
}

// RunJob 返回预设任务或错误。
func (s *cronServiceStub) RunJob(_ context.Context, _ auth.Principal, _, _ string) (service.CronJob, error) {
	return s.job, s.err
}

// History 返回预设执行历史或错误。
func (s *cronServiceStub) History(_ context.Context, _ auth.Principal, _, _ string) ([]service.CronRunEntry, error) {
	return s.runs, s.err
}

// Output 返回预设输出内容或错误。
func (s *cronServiceStub) Output(_ context.Context, _ auth.Principal, _, _, _ string) (service.CronRunOutput, error) {
	return s.output, s.err
}

// newCronTestRouter 构造挂载了 cron 路由的测试 router。
func newCronTestRouter(t *testing.T, svc hermesCronService) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	RegisterHermesCronRoutes(r, NewHermesCronHandler(svc))
	return r
}

// TestHermesCronCreateStripsAdvancedFieldsForOrgAdmin 验证：
// 非平台管理员提交高级字段时，handler 会在调用 service 前丢弃 Skills/Model/Provider/BaseURL。
func TestHermesCronCreateStripsAdvancedFieldsForOrgAdmin(t *testing.T) {
	// stub 返回新建后的任务，确保请求走到 service 并可检查记录的入参。
	stub := &cronServiceStub{job: service.CronJob{ID: "job_1", Name: "日报"}}
	r := newCronTestRouter(t, stub)

	body := `{"name":"日报","schedule":"0 9 * * *","prompt":"生成日报","skills":["bash"],"model":"gpt-5","provider":"openai","base_url":"https://example.test","workdir":"scratch"}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/apps/app-1/hermes/cron/jobs", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	// 组织管理员角色：高级模型和技能字段应被静默剥离，避免绕过 UI 注入运行参数。
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgAdmin, OrgID: "org-1"})
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusCreated, w.Code)
	assert.Empty(t, stub.createIn.Skills)
	assert.Empty(t, stub.createIn.Model)
	assert.Empty(t, stub.createIn.Provider)
	assert.Empty(t, stub.createIn.BaseURL)
}

// TestHermesCronCreateKeepsAdvancedFieldsForPlatformAdmin 验证：
// 平台管理员提交高级字段时，handler 原样透传给 service。
func TestHermesCronCreateKeepsAdvancedFieldsForPlatformAdmin(t *testing.T) {
	// stub 返回新建后的任务，平台管理员路径需要保留所有高级字段。
	stub := &cronServiceStub{job: service.CronJob{ID: "job_1", Name: "日报"}}
	r := newCronTestRouter(t, stub)

	body := `{"name":"日报","schedule":"0 9 * * *","prompt":"生成日报","skills":["bash","grep"],"model":"gpt-5","provider":"openai","base_url":"https://example.test"}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/apps/app-1/hermes/cron/jobs", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	// 平台管理员角色：高级字段是运维能力，应完整传递给 service。
	req = withPrincipal(req, auth.Principal{UserID: "admin", Role: domain.UserRolePlatformAdmin})
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusCreated, w.Code)
	assert.Equal(t, []string{"bash", "grep"}, stub.createIn.Skills)
	assert.Equal(t, "gpt-5", stub.createIn.Model)
	assert.Equal(t, "openai", stub.createIn.Provider)
	assert.Equal(t, "https://example.test", stub.createIn.BaseURL)
}

// TestHermesCronNotSupportedErrorCode 验证：
// service 返回 ErrCronNotSupported 时，HTTP 响应包含稳定错误码 CRON_NOT_SUPPORTED_ON_STUB。
func TestHermesCronNotSupportedErrorCode(t *testing.T) {
	// stub 固定返回 dev 镜像不支持 Cron 的错误。
	stub := &cronServiceStub{err: service.ErrCronNotSupported}
	r := newCronTestRouter(t, stub)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/apps/app-1/hermes/cron/status", nil)
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgAdmin, OrgID: "org-1"})
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusServiceUnavailable, w.Code)
	assert.Contains(t, w.Body.String(), "CRON_NOT_SUPPORTED_ON_STUB")
}

// TestHermesCronHistoryReturnsRuns 验证：
// GET /jobs/:jobId/history 成功时返回顶层 runs 数组，保持前端消费契约稳定。
func TestHermesCronHistoryReturnsRuns(t *testing.T) {
	// stub 预设一条运行历史，用于验证响应结构和字段内容。
	stub := &cronServiceStub{runs: []service.CronRunEntry{{
		JobID:     "job_1",
		FileName:  "2026-05-21T090000.md",
		Size:      42,
		HasOutput: true,
		Status:    "done",
	}}}
	r := newCronTestRouter(t, stub)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/apps/app-1/hermes/cron/jobs/job_1/history", nil)
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgAdmin, OrgID: "org-1"})
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var got struct {
		Runs []service.CronRunEntry `json:"runs"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	require.Len(t, got.Runs, 1)
	assert.Equal(t, "job_1", got.Runs[0].JobID)
	assert.Equal(t, "2026-05-21T090000.md", got.Runs[0].FileName)
}

// TestHermesCronUpdateClearRepeatReturnsBadRequest 验证：
// 当前 runtime 没有稳定的 repeat 清空语义，handler 必须返回明确 400，不能静默透传成 no-op。
func TestHermesCronUpdateClearRepeatReturnsBadRequest(t *testing.T) {
	// stub 若被调用会记录 updated=true；该场景应在 handler 层直接拒绝。
	stub := &cronServiceStub{job: service.CronJob{ID: "job_1", Name: "日报"}}
	r := newCronTestRouter(t, stub)

	body := `{"clear_repeat":true}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/apps/app-1/hermes/cron/jobs/job_1", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgAdmin, OrgID: "org-1"})
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "CRON_BAD_REQUEST")
	assert.False(t, stub.updated, "clear_repeat 不支持时不应继续调用 service")
}
