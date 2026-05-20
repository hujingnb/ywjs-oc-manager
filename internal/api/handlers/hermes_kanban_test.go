package handlers

import (
	"context"
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

// kanbanServiceStub 是 hermesKanbanService 的可控 stub，用于 handler 单测。
// err 字段控制所有方法是否返回错误；tasks / detail 控制读方法的成功返回值。
type kanbanServiceStub struct {
	tasks    []service.KanbanTask
	detail   service.KanbanTaskDetail
	createIn service.CreateKanbanTaskInput // 记录最后一次 CreateTask 入参
	err      error
}

// ListBoards 返回预设错误，无需测试数据。
func (s *kanbanServiceStub) ListBoards(_ context.Context, _ auth.Principal, _ string) ([]service.KanbanBoard, error) {
	return nil, s.err
}

// ListTasks 返回预设任务列表或错误。
func (s *kanbanServiceStub) ListTasks(_ context.Context, _ auth.Principal, _ string, _ service.KanbanTaskFilter) ([]service.KanbanTask, error) {
	return s.tasks, s.err
}

// ShowTask 返回预设任务详情或错误。
func (s *kanbanServiceStub) ShowTask(_ context.Context, _ auth.Principal, _, _, _ string) (service.KanbanTaskDetail, error) {
	return s.detail, s.err
}

// TaskRuns 返回预设错误，无需测试数据。
func (s *kanbanServiceStub) TaskRuns(_ context.Context, _ auth.Principal, _, _, _ string) ([]service.KanbanTaskRun, error) {
	return nil, s.err
}

// Stats 返回预设错误，无需测试数据。
func (s *kanbanServiceStub) Stats(_ context.Context, _ auth.Principal, _, _ string) (service.KanbanStats, error) {
	return service.KanbanStats{}, s.err
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
