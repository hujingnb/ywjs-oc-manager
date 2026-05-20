package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/integrations/runtime"
)

// fakeKanbanExecer 记录最后一次执行的 cmd，并按预设返回结果。
type fakeKanbanExecer struct {
	lastCmd []string
	result  runtime.ExecJSONResult
	err     error
}

func (f *fakeKanbanExecer) ContainerExecJSON(_ context.Context, _, _ string, cmd []string) (runtime.ExecJSONResult, error) {
	f.lastCmd = cmd
	return f.result, f.err
}

func (f *fakeKanbanExecer) ContainerExecStream(_ context.Context, _, _ string, cmd []string) (runtime.ExecStreamHandle, error) {
	f.lastCmd = cmd
	return runtime.ExecStreamHandle{}, f.err
}

// fakeKanbanLocator 返回预设的 app 运行时坐标。
type fakeKanbanLocator struct {
	loc KanbanAppLocation
	err error
}

func (f *fakeKanbanLocator) LocateApp(_ context.Context, _ string) (KanbanAppLocation, error) {
	return f.loc, f.err
}

// healthyLoc 是一个正常运行、可访问的 app 坐标。
func healthyLoc() KanbanAppLocation {
	return KanbanAppLocation{OrgID: "org-1", OwnerUserID: "u-1", NodeID: "n-1", ContainerID: "c-1"}
}

// kanbanOrgAdmin 是 org-1 的组织管理员 principal。
func kanbanOrgAdmin() auth.Principal {
	return auth.Principal{UserID: "admin-1", OrgID: "org-1", Role: domain.UserRoleOrgAdmin}
}

// TestListTasksHappy 验证：正常 app 上 ListTasks 解析 CLI JSON 输出。
func TestListTasksHappy(t *testing.T) {
	execer := &fakeKanbanExecer{result: runtime.ExecJSONResult{
		ExitCode: 0,
		Stdout:   `[{"id":"t_1","title":"任务一","status":"running","assignee":"devops","priority":3}]`,
	}}
	svc := NewHermesKanbanService(execer, &fakeKanbanLocator{loc: healthyLoc()})

	tasks, err := svc.ListTasks(context.Background(), kanbanOrgAdmin(), "app-1", KanbanTaskFilter{})
	require.NoError(t, err)
	require.Len(t, tasks, 1)
	assert.Equal(t, "t_1", tasks[0].ID)
	assert.Equal(t, "running", tasks[0].Status)
	// 校验 argv：board 缺省回退 default、带 --json
	assert.Equal(t, []string{"hermes", "kanban", "list", "--board", "default", "--json"}, execer.lastCmd)
}

// TestListTasksRejectsBadStatus 验证：非法 status 过滤值被白名单拦截，不下发 CLI。
func TestListTasksRejectsBadStatus(t *testing.T) {
	execer := &fakeKanbanExecer{}
	svc := NewHermesKanbanService(execer, &fakeKanbanLocator{loc: healthyLoc()})

	_, err := svc.ListTasks(context.Background(), kanbanOrgAdmin(), "app-1", KanbanTaskFilter{Status: "bogus; rm -rf"})
	require.ErrorIs(t, err, ErrKanbanBadRequest)
	assert.Nil(t, execer.lastCmd) // 非法输入不应触达 execer
}

// TestResolveForbidden 验证：非本组织用户访问 Kanban 被拒。
func TestResolveForbidden(t *testing.T) {
	svc := NewHermesKanbanService(&fakeKanbanExecer{}, &fakeKanbanLocator{loc: healthyLoc()})
	outsider := auth.Principal{UserID: "x", OrgID: "org-2", Role: domain.UserRoleOrgAdmin}

	_, err := svc.ListTasks(context.Background(), outsider, "app-1", KanbanTaskFilter{})
	require.ErrorIs(t, err, ErrKanbanForbidden)
}

// TestResolveStubUnsupported 验证：dev stub 镜像实例返回 ErrKanbanNotSupported。
func TestResolveStubUnsupported(t *testing.T) {
	loc := healthyLoc()
	loc.Stub = true
	svc := NewHermesKanbanService(&fakeKanbanExecer{}, &fakeKanbanLocator{loc: loc})

	_, err := svc.ListTasks(context.Background(), kanbanOrgAdmin(), "app-1", KanbanTaskFilter{})
	require.ErrorIs(t, err, ErrKanbanNotSupported)
}

// TestResolveRuntimeUnavailable 验证：容器未运行返回 ErrKanbanRuntimeUnavailable。
func TestResolveRuntimeUnavailable(t *testing.T) {
	loc := healthyLoc()
	loc.ContainerID = ""
	svc := NewHermesKanbanService(&fakeKanbanExecer{}, &fakeKanbanLocator{loc: loc})

	_, err := svc.ListTasks(context.Background(), kanbanOrgAdmin(), "app-1", KanbanTaskFilter{})
	require.ErrorIs(t, err, ErrKanbanRuntimeUnavailable)
}

// TestRunCLINonZeroExit 验证：CLI 非零退出被包成 ErrKanbanCLI 且带 stderr。
func TestRunCLINonZeroExit(t *testing.T) {
	execer := &fakeKanbanExecer{result: runtime.ExecJSONResult{ExitCode: 2, Stderr: "unknown task"}}
	svc := NewHermesKanbanService(execer, &fakeKanbanLocator{loc: healthyLoc()})

	_, err := svc.ListTasks(context.Background(), kanbanOrgAdmin(), "app-1", KanbanTaskFilter{})
	require.ErrorIs(t, err, ErrKanbanCLI)
	assert.Contains(t, err.Error(), "unknown task")
}

// TestListTasksInvalidJSON 验证：CLI 输出非法 JSON 返回 ErrKanbanOutputInvalid。
func TestListTasksInvalidJSON(t *testing.T) {
	execer := &fakeKanbanExecer{result: runtime.ExecJSONResult{ExitCode: 0, Stdout: "not json"}}
	svc := NewHermesKanbanService(execer, &fakeKanbanLocator{loc: healthyLoc()})

	_, err := svc.ListTasks(context.Background(), kanbanOrgAdmin(), "app-1", KanbanTaskFilter{})
	require.ErrorIs(t, err, ErrKanbanOutputInvalid)
}
