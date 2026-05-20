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
	// lastCmd 由 ContainerExecJSON 与 ContainerExecStream 共享，记录最后一次调用的 cmd；测试中两者不同时使用。
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

// TestListTasksRejectsBadBoard 验证：非法 board slug 被白名单拦截，返回 ErrKanbanBadRequest 且不下发 CLI。
func TestListTasksRejectsBadBoard(t *testing.T) {
	cases := []struct {
		name  string
		board string
	}{
		{"含空格和大写字母的非法 slug", "Bad Board"},    // 场景：board 含空格及大写字母，不符合小写 a-z0-9 规范
		{"含分号空格的注入式非法 slug", "abc; rm"},      // 场景：board 含分号和空格，防止 CLI 注入
	}
	for _, c := range cases {
		// 当前子测试覆盖非法 board slug 格式被拦截、不触达 execer 的边界条件。
		t.Run(c.name, func(t *testing.T) {
			execer := &fakeKanbanExecer{}
			svc := NewHermesKanbanService(execer, &fakeKanbanLocator{loc: healthyLoc()})

			_, err := svc.ListTasks(context.Background(), kanbanOrgAdmin(), "app-1", KanbanTaskFilter{Board: c.board})
			require.ErrorIs(t, err, ErrKanbanBadRequest)
			assert.Nil(t, execer.lastCmd) // 非法输入不应触达 execer
		})
	}
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

// ————————————————————————————————————————————————————
// C2 遗留读 verb 补测
// ————————————————————————————————————————————————————

// TestShowTaskHappy 验证：ShowTask 解析任务详情 JSON 并正确映射字段；argv 含 show/--json。
func TestShowTaskHappy(t *testing.T) {
	// CLI 返回一个合法的任务详情 JSON，验证字段被正确解析。
	execer := &fakeKanbanExecer{result: runtime.ExecJSONResult{
		ExitCode: 0,
		Stdout: `{"id":"t_abc","title":"详情任务","status":"blocked","assignee":"devops",` +
			`"priority":1,"result":"","workspace_kind":"local","comments":[],"events":[]}`,
	}}
	svc := NewHermesKanbanService(execer, &fakeKanbanLocator{loc: healthyLoc()})

	detail, err := svc.ShowTask(context.Background(), kanbanOrgAdmin(), "app-1", "default", "t_abc")
	require.NoError(t, err)
	// 核心字段解析正确
	assert.Equal(t, "t_abc", detail.ID)
	assert.Equal(t, "详情任务", detail.Title)
	assert.Equal(t, "blocked", detail.Status)
	assert.Equal(t, "local", detail.WorkspaceKind)
	// argv 须含 show、任务 ID 与 --json
	assert.Contains(t, execer.lastCmd, "show")
	assert.Contains(t, execer.lastCmd, "t_abc")
	assert.Contains(t, execer.lastCmd, "--json")
}

// TestShowTaskRejectsBadTaskID 验证：非法 taskID 被白名单拦截，不下发 CLI。
func TestShowTaskRejectsBadTaskID(t *testing.T) {
	// 非法 taskID 包含分号，典型的 shell 注入尝试，应被 taskIDRe 正则拦截。
	execer := &fakeKanbanExecer{}
	svc := NewHermesKanbanService(execer, &fakeKanbanLocator{loc: healthyLoc()})

	_, err := svc.ShowTask(context.Background(), kanbanOrgAdmin(), "app-1", "default", "t_1; rm")
	require.ErrorIs(t, err, ErrKanbanBadRequest)
	// 非法输入不应触达 execer
	assert.Nil(t, execer.lastCmd)
}

// TestListBoardsHappy 验证：ListBoards 解析 boards JSON 数组；argv 含 boards/list。
func TestListBoardsHappy(t *testing.T) {
	// CLI 返回包含一个 board 的 JSON 数组，验证能正确解析 slug/name 字段。
	execer := &fakeKanbanExecer{result: runtime.ExecJSONResult{
		ExitCode: 0,
		Stdout:   `[{"slug":"default","name":"默认看板","description":"主看板","archived":false}]`,
	}}
	svc := NewHermesKanbanService(execer, &fakeKanbanLocator{loc: healthyLoc()})

	boards, err := svc.ListBoards(context.Background(), kanbanOrgAdmin(), "app-1")
	require.NoError(t, err)
	require.Len(t, boards, 1)
	assert.Equal(t, "default", boards[0].Slug)
	assert.Equal(t, "默认看板", boards[0].Name)
	// argv 须含 boards 与 list 子命令
	assert.Contains(t, execer.lastCmd, "boards")
	assert.Contains(t, execer.lastCmd, "list")
}

// TestStatsHappy 验证：Stats 解析 per-status 计数 JSON；status_counts 字段被正确映射。
func TestStatsHappy(t *testing.T) {
	// CLI 返回各状态计数，验证解析为 KanbanStats.StatusCounts map。
	execer := &fakeKanbanExecer{result: runtime.ExecJSONResult{
		ExitCode: 0,
		Stdout:   `{"status_counts":{"todo":2,"running":1,"done":5}}`,
	}}
	svc := NewHermesKanbanService(execer, &fakeKanbanLocator{loc: healthyLoc()})

	stats, err := svc.Stats(context.Background(), kanbanOrgAdmin(), "app-1", "default")
	require.NoError(t, err)
	// 验证各状态计数被正确解析
	assert.Equal(t, 2, stats.StatusCounts["todo"])
	assert.Equal(t, 1, stats.StatusCounts["running"])
	assert.Equal(t, 5, stats.StatusCounts["done"])
}

// ————————————————————————————————————————————————————
// Task C3：写 verb 单测
// ————————————————————————————————————————————————————

// TestCreateTaskHappy 验证：CreateTask 拼出正确 argv 并解析返回详情。
func TestCreateTaskHappy(t *testing.T) {
	// CLI 返回新建任务详情，验证解析正确且 title 作为独立 argv 元素传入。
	execer := &fakeKanbanExecer{result: runtime.ExecJSONResult{
		ExitCode: 0,
		Stdout:   `{"id":"t_new","title":"新任务","status":"todo","assignee":"devops","priority":2}`,
	}}
	svc := NewHermesKanbanService(execer, &fakeKanbanLocator{loc: healthyLoc()})

	detail, err := svc.CreateTask(context.Background(), kanbanOrgAdmin(), "app-1", CreateKanbanTaskInput{
		Title: "新任务", Assignee: "devops", Priority: 2,
	})
	require.NoError(t, err)
	assert.Equal(t, "t_new", detail.ID)
	assert.Equal(t, "todo", detail.Status)
	// 自由文本 title 必须作为独立 argv 元素（不拼 shell），防注入
	assert.Contains(t, execer.lastCmd, "新任务")
	assert.Contains(t, execer.lastCmd, "create")
}

// TestCreateTaskRejectsEmptyTitle 验证：空标题或全空格标题被 ErrKanbanBadRequest 拦截。
func TestCreateTaskRejectsEmptyTitle(t *testing.T) {
	// 空白字符串标题属于非法输入，应在进入 CLI 前被拒绝。
	svc := NewHermesKanbanService(&fakeKanbanExecer{}, &fakeKanbanLocator{loc: healthyLoc()})
	_, err := svc.CreateTask(context.Background(), kanbanOrgAdmin(), "app-1", CreateKanbanTaskInput{
		Title: "  ", Assignee: "devops",
	})
	require.ErrorIs(t, err, ErrKanbanBadRequest)
}

// TestWriteVerbForbiddenForOutsider 验证：非本组织成员不能执行写 verb（Comment）。
func TestWriteVerbForbiddenForOutsider(t *testing.T) {
	// outsider 属于 org-2，与 app 归属的 org-1 不同，应被 resolveManage 拒绝。
	svc := NewHermesKanbanService(&fakeKanbanExecer{}, &fakeKanbanLocator{loc: healthyLoc()})
	outsider := auth.Principal{UserID: "x", OrgID: "org-2", Role: domain.UserRoleOrgAdmin}
	err := svc.Comment(context.Background(), outsider, "app-1", "default", "t_1", "hi")
	require.ErrorIs(t, err, ErrKanbanForbidden)
}

// TestCommentRejectsBadTaskID 验证：Comment 的非法 task id 被白名单拦截，不下发 CLI。
func TestCommentRejectsBadTaskID(t *testing.T) {
	// taskID 含分号是典型注入尝试，应被 taskIDRe 正则拒绝。
	execer := &fakeKanbanExecer{}
	svc := NewHermesKanbanService(execer, &fakeKanbanLocator{loc: healthyLoc()})
	err := svc.Comment(context.Background(), kanbanOrgAdmin(), "app-1", "default", "t_1; rm -rf /", "hi")
	require.ErrorIs(t, err, ErrKanbanBadRequest)
	// 非法 taskID 不应触达 execer
	assert.Nil(t, execer.lastCmd)
}

// TestCompleteHappy 验证：Complete 拼出含 --result 的 argv。
func TestCompleteHappy(t *testing.T) {
	// CLI 完成操作后返回 "ok"（无需解析 JSON），验证 argv 含 complete 及 result 字符串。
	execer := &fakeKanbanExecer{result: runtime.ExecJSONResult{ExitCode: 0, Stdout: "ok"}}
	svc := NewHermesKanbanService(execer, &fakeKanbanLocator{loc: healthyLoc()})
	err := svc.Complete(context.Background(), kanbanOrgAdmin(), "app-1", "default", "t_1", "已完成")
	require.NoError(t, err)
	// argv 须含 complete、任务 ID 与 result 字符串
	assert.Contains(t, execer.lastCmd, "complete")
	assert.Contains(t, execer.lastCmd, "t_1")
	assert.Contains(t, execer.lastCmd, "已完成")
}

// ————————————————————————————————————————————————————
// Task C4：实时事件流单测
// ————————————————————————————————————————————————————

// fakeStreamExecer 是支持流式输出的假 execer，把预设 lines 投递到 channel 后关闭。
type fakeStreamExecer struct {
	// lines 是预设的待投递行。
	lines []string
}

// ContainerExecJSON 在 fakeStreamExecer 中不使用，返回空结果。
func (f *fakeStreamExecer) ContainerExecJSON(_ context.Context, _, _ string, _ []string) (runtime.ExecJSONResult, error) {
	return runtime.ExecJSONResult{}, nil
}

// ContainerExecStream 把 lines 全部写入 channel 后关闭，模拟流式输出。
func (f *fakeStreamExecer) ContainerExecStream(_ context.Context, _, _ string, _ []string) (runtime.ExecStreamHandle, error) {
	ch := make(chan string, len(f.lines))
	for _, l := range f.lines {
		ch <- l
	}
	close(ch)
	return runtime.ExecStreamHandle{
		Lines: ch,
		Err:   func() error { return nil },
		Close: func() {},
	}, nil
}

// TestStreamEventsDeliversLines 验证：StreamEvents 把流式行逐条交给 onLine 回调。
func TestStreamEventsDeliversLines(t *testing.T) {
	// 预设两行 NDJSON，验证全部按顺序传给回调。
	execer := &fakeStreamExecer{lines: []string{`{"kind":"claimed"}`, `{"kind":"heartbeat"}`}}
	svc := NewHermesKanbanService(execer, &fakeKanbanLocator{loc: healthyLoc()})

	var got []string
	err := svc.StreamEvents(context.Background(), kanbanOrgAdmin(), "app-1", "default", func(l string) {
		got = append(got, l)
	})
	require.NoError(t, err)
	// 两行事件应按顺序全部到达
	assert.Equal(t, []string{`{"kind":"claimed"}`, `{"kind":"heartbeat"}`}, got)
}

