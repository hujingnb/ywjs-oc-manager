package handlers

import (
	"context"
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

// kanbanServiceStub 是 hermesKanbanService 的可控 stub，用于 handler 单测。
// err 字段控制所有方法是否返回错误；tasks / detail / boards / runs / stats 控制读方法的成功返回值。
type kanbanServiceStub struct {
	tasks    []service.KanbanTask
	detail   service.KanbanTaskDetail
	boards   []service.KanbanBoard
	runs     []service.KanbanTaskRun
	stats    service.KanbanStats
	createIn service.CreateKanbanTaskInput // 记录最后一次 CreateTask 入参
	err      error
}

// ListBoards 返回预设 boards 列表或错误。
func (s *kanbanServiceStub) ListBoards(_ context.Context, _ auth.Principal, _ string) ([]service.KanbanBoard, error) {
	return s.boards, s.err
}

// ListTasks 返回预设任务列表或错误。
func (s *kanbanServiceStub) ListTasks(_ context.Context, _ auth.Principal, _ string, _ service.KanbanTaskFilter) ([]service.KanbanTask, error) {
	return s.tasks, s.err
}

// ShowTask 返回预设任务详情或错误。
func (s *kanbanServiceStub) ShowTask(_ context.Context, _ auth.Principal, _, _, _ string) (service.KanbanTaskDetail, error) {
	return s.detail, s.err
}

// TaskRuns 返回预设任务执行历史列表或错误。
func (s *kanbanServiceStub) TaskRuns(_ context.Context, _ auth.Principal, _, _, _ string) ([]service.KanbanTaskRun, error) {
	return s.runs, s.err
}

// Stats 返回预设统计数据或错误。
func (s *kanbanServiceStub) Stats(_ context.Context, _ auth.Principal, _, _ string) (service.KanbanStats, error) {
	return s.stats, s.err
}

// StreamEvents 返回预设错误，不推送任何行。
func (s *kanbanServiceStub) StreamEvents(_ context.Context, _ auth.Principal, _, _ string, _ func(string)) error {
	return s.err
}

// CreateTask 记录入参并返回预设详情或错误。
func (s *kanbanServiceStub) CreateTask(_ context.Context, _ auth.Principal, _ string, in service.CreateKanbanTaskInput) (service.KanbanTaskDetail, error) {
	s.createIn = in
	return s.detail, s.err
}

// Comment 返回预设错误。
func (s *kanbanServiceStub) Comment(_ context.Context, _ auth.Principal, _, _, _, _ string) error {
	return s.err
}

// Complete 返回预设错误。
func (s *kanbanServiceStub) Complete(_ context.Context, _ auth.Principal, _, _, _, _ string) error {
	return s.err
}

// Block 返回预设错误。
func (s *kanbanServiceStub) Block(_ context.Context, _ auth.Principal, _, _, _, _ string) error {
	return s.err
}

// Unblock 返回预设错误。
func (s *kanbanServiceStub) Unblock(_ context.Context, _ auth.Principal, _, _, _ string) error {
	return s.err
}

// Archive 返回预设错误。
func (s *kanbanServiceStub) Archive(_ context.Context, _ auth.Principal, _, _, _ string) error {
	return s.err
}

// Reassign 返回预设错误。
func (s *kanbanServiceStub) Reassign(_ context.Context, _ auth.Principal, _, _, _, _ string) error {
	return s.err
}

// Reclaim 返回预设错误。
func (s *kanbanServiceStub) Reclaim(_ context.Context, _ auth.Principal, _, _, _ string) error {
	return s.err
}

// newKanbanTestRouter 构造挂载了 kanban 路由的测试 router。
func newKanbanTestRouter(t *testing.T, svc hermesKanbanService) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	RegisterHermesKanbanRoutes(r, NewHermesKanbanHandler(svc))
	return r
}

// TestKanbanListTasksHappy 验证：列任务端点正常返回时，HTTP 状态 200 且响应体含 tasks 字段与任务 ID。
func TestKanbanListTasksHappy(t *testing.T) {
	// stub 返回一个预设任务列表
	stub := &kanbanServiceStub{tasks: []service.KanbanTask{{ID: "t_1", Title: "任务一"}}}
	r := newKanbanTestRouter(t, stub)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/apps/app-1/hermes/kanban/tasks", nil)
	// 注入 org_admin principal，确保鉴权层通过（stub 不做实际权限检查）
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgAdmin, OrgID: "org-1"})
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	// 响应体须含任务 ID
	assert.Contains(t, w.Body.String(), "t_1")
	// 响应体须含顶层 tasks key，确保 JSON 结构符合接口契约
	assert.Contains(t, w.Body.String(), `"tasks"`)
}

// TestKanbanListTasksForbidden 验证：service 返回 ErrKanbanForbidden 时端点返回 403。
func TestKanbanListTasksForbidden(t *testing.T) {
	// stub 固定返回权限错误
	stub := &kanbanServiceStub{err: service.ErrKanbanForbidden}
	r := newKanbanTestRouter(t, stub)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/apps/app-1/hermes/kanban/tasks", nil)
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgMember, OrgID: "org-2"})
	r.ServeHTTP(w, req)

	// 权限错误应映射为 403
	assert.Equal(t, http.StatusForbidden, w.Code)
}

// TestKanbanStubReturns503 验证：service 返回 ErrKanbanNotSupported（stub 镜像）时端点返回 503
// 且响应体含约定的错误码 KANBAN_NOT_SUPPORTED_ON_STUB。
func TestKanbanStubReturns503(t *testing.T) {
	// stub 固定返回 dev 镜像不支持错误
	stub := &kanbanServiceStub{err: service.ErrKanbanNotSupported}
	r := newKanbanTestRouter(t, stub)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/apps/app-1/hermes/kanban/tasks", nil)
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgAdmin, OrgID: "org-1"})
	r.ServeHTTP(w, req)

	// dev stub 镜像不支持看板，应返回 503
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
	// 响应体须含稳定的接口契约错误码
	assert.Contains(t, w.Body.String(), "KANBAN_NOT_SUPPORTED_ON_STUB")
}

// TestKanbanListBoardsHappy 验证：ListBoards 端点在 service 正常返回时，
// HTTP 状态 200 且响应体含顶层 boards key。
func TestKanbanListBoardsHappy(t *testing.T) {
	// stub 预设一个 board，验证正常路径下响应结构正确
	stub := &kanbanServiceStub{boards: []service.KanbanBoard{{Slug: "default", Name: "默认看板"}}}
	r := newKanbanTestRouter(t, stub)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/apps/app-1/hermes/kanban/boards", nil)
	// 注入 org_admin principal，确保鉴权层通过
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgAdmin, OrgID: "org-1"})
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	// 响应体须含顶层 boards key，确保接口契约正确
	assert.Contains(t, w.Body.String(), `"boards"`)
}

// TestKanbanShowTaskHappy 验证：ShowTask 端点在 service 正常返回时，
// HTTP 状态 200 且响应体含顶层 task key。
func TestKanbanShowTaskHappy(t *testing.T) {
	// stub 预设任务详情（ID=t_1），验证单任务查询正常路径
	stub := &kanbanServiceStub{detail: service.KanbanTaskDetail{Task: service.KanbanTask{ID: "t_1", Title: "测试任务"}}}
	r := newKanbanTestRouter(t, stub)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/apps/app-1/hermes/kanban/tasks/t_1", nil)
	// 注入 org_admin principal，确保鉴权层通过
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgAdmin, OrgID: "org-1"})
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	// 响应体须含顶层 task key，确保接口契约正确
	assert.Contains(t, w.Body.String(), `"task"`)
}

// TestKanbanTaskRunsHappy 验证：TaskRuns 端点在 service 正常返回时，
// HTTP 状态 200 且响应体含顶层 runs key。
func TestKanbanTaskRunsHappy(t *testing.T) {
	// stub 预设一条执行历史，验证任务执行历史查询正常路径
	stub := &kanbanServiceStub{runs: []service.KanbanTaskRun{{Profile: "default", Status: "done"}}}
	r := newKanbanTestRouter(t, stub)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/apps/app-1/hermes/kanban/tasks/t_1/runs", nil)
	// 注入 org_admin principal，确保鉴权层通过
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgAdmin, OrgID: "org-1"})
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	// 响应体须含顶层 runs key，确保接口契约正确
	assert.Contains(t, w.Body.String(), `"runs"`)
}

// TestKanbanStatsHappy 验证：Stats 端点在 service 正常返回时，
// HTTP 状态 200 且响应体含顶层 stats key。
func TestKanbanStatsHappy(t *testing.T) {
	// stub 预设统计数据，验证统计查询正常路径
	// 使用真实字段名 ByStatus（原 StatusCounts 已按 hermes v0.14.0 契约校准）
	stub := &kanbanServiceStub{stats: service.KanbanStats{ByStatus: map[string]int{"todo": 3, "done": 5}}}
	r := newKanbanTestRouter(t, stub)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/apps/app-1/hermes/kanban/stats", nil)
	// 注入 org_admin principal，确保鉴权层通过
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgAdmin, OrgID: "org-1"})
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	// 响应体须含顶层 stats key，确保接口契约正确
	assert.Contains(t, w.Body.String(), `"stats"`)
}

// TestKanbanCreateStripsAdvancedFieldsForOrgAdmin 验证：
// 组织管理员提交高级字段（skills/workspace/max_retries 等）时，
// handler 层按 principal.Role 将其静默丢弃，不透传给 service。
func TestKanbanCreateStripsAdvancedFieldsForOrgAdmin(t *testing.T) {
	// stub 预设成功返回，detail.ID 用于验证正常路径通过
	stub := &kanbanServiceStub{detail: service.KanbanTaskDetail{Task: service.KanbanTask{ID: "t_new"}}}
	r := newKanbanTestRouter(t, stub)

	// skills 现为数组，workspace 合并为单一字段
	body := `{"title":"x","assignee":"devops","skills":["bash"],"workspace":"worktree","max_retries":5}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/apps/app-1/hermes/kanban/tasks", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	// 组织管理员角色：高级字段应被 strip
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgAdmin, OrgID: "org-1"})
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusCreated, w.Code)
	// 验证 handler 已将高级字段从传入 service 的 input 中剥离
	assert.Empty(t, stub.createIn.Skills, "org_admin 的 skills 应被丢弃")
	assert.Empty(t, stub.createIn.Workspace, "org_admin 的 workspace 应被丢弃")
	assert.Zero(t, stub.createIn.MaxRetries, "org_admin 的 max_retries 应被丢弃")
}

// TestKanbanCreateKeepsAdvancedFieldsForPlatformAdmin 验证：
// 平台管理员提交高级字段时，handler 层原样透传给 service，不做 strip。
func TestKanbanCreateKeepsAdvancedFieldsForPlatformAdmin(t *testing.T) {
	// stub 预设成功返回
	stub := &kanbanServiceStub{detail: service.KanbanTaskDetail{Task: service.KanbanTask{ID: "t_new"}}}
	r := newKanbanTestRouter(t, stub)

	// skills 为数组，workspace 为单一参数
	body := `{"title":"x","assignee":"devops","skills":["bash","grep"],"workspace":"scratch","max_retries":5}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/apps/app-1/hermes/kanban/tasks", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	// 平台管理员角色：高级字段应透传
	req = withPrincipal(req, auth.Principal{UserID: "admin", Role: domain.UserRolePlatformAdmin})
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusCreated, w.Code)
	// 验证高级字段被如实传入 service
	assert.Equal(t, []string{"bash", "grep"}, stub.createIn.Skills, "平台管理员的 skills 应透传")
	assert.Equal(t, "scratch", stub.createIn.Workspace, "平台管理员的 workspace 应透传")
	assert.Equal(t, 5, stub.createIn.MaxRetries, "平台管理员的 max_retries 应透传")
}

// TestKanbanCommentHappy 验证：评论端点在 service 成功时返回 204 No Content。
func TestKanbanCommentHappy(t *testing.T) {
	// stub 无错误，模拟评论成功
	stub := &kanbanServiceStub{}
	r := newKanbanTestRouter(t, stub)

	body := `{"board":"default","body":"一条评论"}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/apps/app-1/hermes/kanban/tasks/t_1/comment", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgAdmin, OrgID: "org-1"})
	r.ServeHTTP(w, req)

	// 写操作成功应返回 204 No Content
	assert.Equal(t, http.StatusNoContent, w.Code)
}

// TestKanbanUnblockEmptyBody 验证：unblock 端点不发 body 时应返回 204，而非 400。
// body 为可选（KanbanBoardRequest 无必填字段），空请求体不应被误判为绑定错误。
func TestKanbanUnblockEmptyBody(t *testing.T) {
	// stub 无错误，模拟 unblock 成功
	stub := &kanbanServiceStub{}
	r := newKanbanTestRouter(t, stub)

	w := httptest.NewRecorder()
	// 不携带请求体，模拟前端省略 body 的场景
	req := httptest.NewRequest(http.MethodPost, "/api/v1/apps/app-1/hermes/kanban/tasks/t_1/unblock", nil)
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgAdmin, OrgID: "org-1"})
	r.ServeHTTP(w, req)

	// 空 body 不应触发 400，成功应返回 204 No Content
	assert.Equal(t, http.StatusNoContent, w.Code)
}

// TestKanbanArchiveEmptyBody 验证：archive 端点不发 body 时应返回 204，而非 400。
// body 为可选（KanbanBoardRequest 无必填字段），空请求体不应被误判为绑定错误。
func TestKanbanArchiveEmptyBody(t *testing.T) {
	// stub 无错误，模拟 archive 成功
	stub := &kanbanServiceStub{}
	r := newKanbanTestRouter(t, stub)

	w := httptest.NewRecorder()
	// 不携带请求体，模拟前端省略 body 的场景
	req := httptest.NewRequest(http.MethodPost, "/api/v1/apps/app-1/hermes/kanban/tasks/t_1/archive", nil)
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgAdmin, OrgID: "org-1"})
	r.ServeHTTP(w, req)

	// 空 body 不应触发 400，成功应返回 204 No Content
	assert.Equal(t, http.StatusNoContent, w.Code)
}

// TestKanbanReclaimEmptyBody 验证：reclaim 端点不发 body 时应返回 204，而非 400。
// body 为可选（KanbanBoardRequest 无必填字段），空请求体不应被误判为绑定错误。
func TestKanbanReclaimEmptyBody(t *testing.T) {
	// stub 无错误，模拟 reclaim 成功
	stub := &kanbanServiceStub{}
	r := newKanbanTestRouter(t, stub)

	w := httptest.NewRecorder()
	// 不携带请求体，模拟前端省略 body 的场景
	req := httptest.NewRequest(http.MethodPost, "/api/v1/apps/app-1/hermes/kanban/tasks/t_1/reclaim", nil)
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgAdmin, OrgID: "org-1"})
	r.ServeHTTP(w, req)

	// 空 body 不应触发 400，成功应返回 204 No Content
	assert.Equal(t, http.StatusNoContent, w.Code)
}

// TestKanbanCompleteEmptyBody 验证：complete 端点不发 body 时应返回 204，而非 400。
// body 为可选（KanbanCompleteRequest 无必填字段），空请求体不应被误判为绑定错误。
func TestKanbanCompleteEmptyBody(t *testing.T) {
	// stub 无错误，模拟 complete 成功
	stub := &kanbanServiceStub{}
	r := newKanbanTestRouter(t, stub)

	w := httptest.NewRecorder()
	// 不携带请求体，模拟前端省略 body 的场景
	req := httptest.NewRequest(http.MethodPost, "/api/v1/apps/app-1/hermes/kanban/tasks/t_1/complete", nil)
	req = withPrincipal(req, auth.Principal{UserID: "u1", Role: domain.UserRoleOrgAdmin, OrgID: "org-1"})
	r.ServeHTTP(w, req)

	// 空 body 不应触发 400，成功应返回 204 No Content
	assert.Equal(t, http.StatusNoContent, w.Code)
}
