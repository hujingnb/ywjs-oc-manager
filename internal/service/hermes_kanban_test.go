package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/integrations/runtime"
)

// okEnvelope 把一段 data JSON 包成 oc-kanban 成功信封，供 fake execer 返回。
func okEnvelope(dataJSON string) string {
	return `{"ok":true,"data":` + dataJSON + `}`
}

// errEnvelope 把契约错误码包成 oc-kanban 失败信封。
func errEnvelope(code, message string) string {
	return `{"ok":false,"error":{"code":"` + code + `","message":"` + message + `"}}`
}

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
	ch := make(chan string)
	close(ch)
	return runtime.ExecStreamHandle{
		Lines: ch,
		Err:   func() error { return nil },
		Close: func() {},
	}, f.err
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

// TestListTasksHappy 验证：正常 app 上 ListTasks 解析 oc-kanban 信封 JSON 输出。
func TestListTasksHappy(t *testing.T) {
	execer := &fakeKanbanExecer{result: runtime.ExecJSONResult{
		ExitCode: 0,
		Stdout:   okEnvelope(`[{"id":"t_1","title":"任务一","status":"running","assignee":"devops","priority":3}]`),
	}}
	svc := NewHermesKanbanService(execer, &fakeKanbanLocator{loc: healthyLoc()})

	tasks, err := svc.ListTasks(context.Background(), kanbanOrgAdmin(), "app-1", KanbanTaskFilter{})
	require.NoError(t, err)
	require.Len(t, tasks, 1)
	assert.Equal(t, "t_1", tasks[0].ID)
	assert.Equal(t, "running", tasks[0].Status)
	// 校验 argv：oc-kanban list --board default（board 作为 flag，不再是全局参数位置）
	assert.Equal(t, []string{"oc-kanban", "list", "--board", "default"}, execer.lastCmd)
}

// TestListTasksRejectsBadBoard 验证：非法 board slug 被白名单拦截，返回 ErrKanbanBadRequest 且不下发 CLI。
func TestListTasksRejectsBadBoard(t *testing.T) {
	cases := []struct {
		name  string
		board string
	}{
		{"含空格和大写字母的非法 slug", "Bad Board"}, // 场景：board 含空格及大写字母，不符合小写 a-z0-9 规范
		{"含分号空格的注入式非法 slug", "abc; rm"},   // 场景：board 含分号和空格，防止 CLI 注入
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

// TestKanbanErrorCodeMapping 验证：oc-kanban 失败信封的错误码被正确映射成 service 哨兵错误。
func TestKanbanErrorCodeMapping(t *testing.T) {
	cases := []struct {
		name    string // 测试场景
		code    string // oc-kanban 错误码
		wantErr error  // 期望映射到的哨兵错误
	}{
		{"参数非法映射为 BadRequest", "BAD_REQUEST", ErrKanbanBadRequest},      // BAD_REQUEST → ErrKanbanBadRequest
		{"资源不存在映射为 NotFound", "NOT_FOUND", ErrNotFound},                 // NOT_FOUND → ErrNotFound
		{"能力不支持映射为 NotSupported", "UNSUPPORTED", ErrKanbanNotSupported}, // UNSUPPORTED → ErrKanbanNotSupported
		{"hermes 执行失败映射为 CLI 错误", "HERMES_CLI_FAILED", ErrKanbanCLI},    // HERMES_CLI_FAILED → ErrKanbanCLI
		{"内部错误映射为输出非法", "INTERNAL", ErrKanbanOutputInvalid},             // INTERNAL → ErrKanbanOutputInvalid
		{"未知错误码兜底为 CLI 错误", "UNKNOWN_CODE", ErrKanbanCLI},               // 未知 code → default 分支 → ErrKanbanCLI
	}
	for _, c := range cases {
		// 每个子测试覆盖一种错误码到哨兵错误的映射路径。
		t.Run(c.name, func(t *testing.T) {
			execer := &fakeKanbanExecer{result: runtime.ExecJSONResult{
				ExitCode: 1, Stdout: errEnvelope(c.code, "失败详情"),
			}}
			svc := NewHermesKanbanService(execer, &fakeKanbanLocator{loc: healthyLoc()})
			_, err := svc.ListTasks(context.Background(), kanbanOrgAdmin(), "app-1", KanbanTaskFilter{})
			require.ErrorIs(t, err, c.wantErr)
		})
	}
}

// TestListTasksInvalidJSON 验证：stdout 非合法信封 JSON，信封解析失败，返回 ErrKanbanOutputInvalid。
func TestListTasksInvalidJSON(t *testing.T) {
	execer := &fakeKanbanExecer{result: runtime.ExecJSONResult{ExitCode: 0, Stdout: "not json"}}
	svc := NewHermesKanbanService(execer, &fakeKanbanLocator{loc: healthyLoc()})

	_, err := svc.ListTasks(context.Background(), kanbanOrgAdmin(), "app-1", KanbanTaskFilter{})
	require.ErrorIs(t, err, ErrKanbanOutputInvalid)
}

// ————————————————————————————————————————————————————
// C2 遗留读 verb 补测
// ————————————————————————————————————————————————————

// TestShowTaskHappy 验证：ShowTask 解析 oc-kanban 信封中的任务详情 JSON 并正确映射字段；argv 含 show/--id。
// oc-kanban show 输出的任务核心字段嵌在顶层 "task" 子对象里，而非直接平铺。
func TestShowTaskHappy(t *testing.T) {
	// oc-kanban 返回成功信封，data 段为真实 show 结构：任务字段在 task 子对象内，同时含 comments/events 数组。
	showJSON := `{"task":{"id":"t_abc","title":"详情任务","status":"blocked","assignee":"devops",` +
		`"priority":1,"workspace_kind":"scratch","created_at":1779267436,"skills":[]}` +
		`,"latest_summary":null,"parents":[],"children":[],"comments":[],"events":[]}`
	execer := &fakeKanbanExecer{result: runtime.ExecJSONResult{
		ExitCode: 0,
		Stdout:   okEnvelope(showJSON),
	}}
	svc := NewHermesKanbanService(execer, &fakeKanbanLocator{loc: healthyLoc()})

	detail, err := svc.ShowTask(context.Background(), kanbanOrgAdmin(), "app-1", "default", "t_abc")
	require.NoError(t, err)
	// 核心字段通过 detail.Task 访问（show 输出的 task 子对象）
	assert.Equal(t, "t_abc", detail.Task.ID)
	assert.Equal(t, "详情任务", detail.Task.Title)
	assert.Equal(t, "blocked", detail.Task.Status)
	assert.Equal(t, "scratch", detail.Task.WorkspaceKind)
	// argv 须含 show 与 --id（oc-kanban 用 --id 而非 positional taskID）
	assert.Contains(t, execer.lastCmd, "show")
	assert.Contains(t, execer.lastCmd, "t_abc")
	assert.Contains(t, execer.lastCmd, "--id")
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

// TestListBoardsHappy 验证：ListBoards 解析 oc-kanban 信封中的 boards JSON 数组；argv 为 oc-kanban boards。
func TestListBoardsHappy(t *testing.T) {
	// oc-kanban 返回成功信封，data 段为包含一个 board 的 JSON 数组，验证能正确解析 slug/name 字段。
	execer := &fakeKanbanExecer{result: runtime.ExecJSONResult{
		ExitCode: 0,
		Stdout:   okEnvelope(`[{"slug":"default","name":"默认看板","description":"主看板","archived":false}]`),
	}}
	svc := NewHermesKanbanService(execer, &fakeKanbanLocator{loc: healthyLoc()})

	boards, err := svc.ListBoards(context.Background(), kanbanOrgAdmin(), "app-1")
	require.NoError(t, err)
	require.Len(t, boards, 1)
	assert.Equal(t, "default", boards[0].Slug)
	assert.Equal(t, "默认看板", boards[0].Name)
	// argv 须为 oc-kanban boards（不再含 list 子命令）
	assert.Equal(t, []string{"oc-kanban", "boards"}, execer.lastCmd)
}

// TestStatsHappy 验证：Stats 解析 oc-kanban 信封中的 per-status 计数 JSON；by_status 字段被正确映射。
// 真实 CLI 输出字段名为 by_status（非 status_counts），已按 hermes v0.14.0 校准。
func TestStatsHappy(t *testing.T) {
	// oc-kanban 返回成功信封，data 段为真实 stats 结构：by_status 含各状态计数，by_assignee 含各 assignee 计数。
	execer := &fakeKanbanExecer{result: runtime.ExecJSONResult{
		ExitCode: 0,
		Stdout:   okEnvelope(`{"by_status":{"todo":2,"running":1,"done":5},"by_assignee":{"default":{"todo":2}},"oldest_ready_age_seconds":0,"now":1779267460}`),
	}}
	svc := NewHermesKanbanService(execer, &fakeKanbanLocator{loc: healthyLoc()})

	stats, err := svc.Stats(context.Background(), kanbanOrgAdmin(), "app-1", "default")
	require.NoError(t, err)
	// 验证各状态计数通过 ByStatus 字段正确解析
	assert.Equal(t, 2, stats.ByStatus["todo"])
	assert.Equal(t, 1, stats.ByStatus["running"])
	assert.Equal(t, 5, stats.ByStatus["done"])
	// 验证时间戳字段
	assert.Equal(t, int64(1779267460), stats.Now)
}

// ————————————————————————————————————————————————————
// Task C3：写 verb 单测
// ————————————————————————————————————————————————————

// TestCreateTaskHappy 验证：CreateTask 拼出正确 argv 并解析 oc-kanban 返回的 TaskDetail。
func TestCreateTaskHappy(t *testing.T) {
	// oc-kanban create 返回完整 TaskDetail（task 子对象 + 关联数组）。
	detailJSON := `{"task":{"id":"t_new","title":"新任务","status":"ready","assignee":"devops",` +
		`"priority":2,"created_at":1779267436,"skills":[]},"latest_summary":null,` +
		`"parents":[],"children":[],"comments":[],"events":[]}`
	execer := &fakeKanbanExecer{result: runtime.ExecJSONResult{
		ExitCode: 0, Stdout: okEnvelope(detailJSON),
	}}
	svc := NewHermesKanbanService(execer, &fakeKanbanLocator{loc: healthyLoc()})

	detail, err := svc.CreateTask(context.Background(), kanbanOrgAdmin(), "app-1", CreateKanbanTaskInput{
		Title: "新任务", Assignee: "devops", Priority: 2,
	})
	require.NoError(t, err)
	// 任务核心字段在 detail.Task 子对象内
	assert.Equal(t, "t_new", detail.Task.ID)
	assert.Equal(t, "ready", detail.Task.Status)
	// 自由文本 title 作为 --title 的独立 argv 值（不拼 shell），防注入
	assert.Contains(t, execer.lastCmd, "create")
	assert.Contains(t, execer.lastCmd, "新任务")
	assert.Equal(t, "oc-kanban", execer.lastCmd[0])
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
	_, err := svc.Comment(context.Background(), outsider, "app-1", "default", "t_1", "hi")
	require.ErrorIs(t, err, ErrKanbanForbidden)
}

// TestCommentRejectsBadTaskID 验证：Comment 的非法 task id 被白名单拦截，不下发 CLI。
func TestCommentRejectsBadTaskID(t *testing.T) {
	// taskID 含分号是典型注入尝试，应被 taskIDRe 正则拒绝。
	execer := &fakeKanbanExecer{}
	svc := NewHermesKanbanService(execer, &fakeKanbanLocator{loc: healthyLoc()})
	_, err := svc.Comment(context.Background(), kanbanOrgAdmin(), "app-1", "default", "t_1; rm -rf /", "hi")
	require.ErrorIs(t, err, ErrKanbanBadRequest)
	// 非法 taskID 不应触达 execer
	assert.Nil(t, execer.lastCmd)
}

// TestCompleteHappy 验证：Complete 拼出含 --result 的 argv，并返回 TaskDetail。
func TestCompleteHappy(t *testing.T) {
	// oc-kanban complete 返回完整 TaskDetail，验证 argv 含 complete 及 result 字符串。
	detailJSON := `{"task":{"id":"t_1","title":"完成任务","status":"done","assignee":"devops","priority":0,"created_at":1779267436,"skills":[]},"latest_summary":null,"parents":[],"children":[],"comments":[],"events":[]}`
	execer := &fakeKanbanExecer{result: runtime.ExecJSONResult{ExitCode: 0, Stdout: okEnvelope(detailJSON)}}
	svc := NewHermesKanbanService(execer, &fakeKanbanLocator{loc: healthyLoc()})
	detail, err := svc.Complete(context.Background(), kanbanOrgAdmin(), "app-1", "default", "t_1", "已完成")
	require.NoError(t, err)
	// argv 须含 complete、任务 ID 与 result 字符串
	assert.Contains(t, execer.lastCmd, "complete")
	assert.Contains(t, execer.lastCmd, "t_1")
	assert.Contains(t, execer.lastCmd, "已完成")
	// 返回的 detail 应包含任务信息
	assert.Equal(t, "t_1", detail.Task.ID)
}

// ————————————————————————————————————————————————————
// Task C4：实时事件流单测
// ————————————————————————————————————————————————————

// fakeStreamExecer 是支持流式输出的假 execer，把预设 lines 投递到 channel 后关闭。
type fakeStreamExecer struct {
	// lines 是预设的待投递行。
	lines []string
	// lastCmd 记录最后一次 ContainerExecStream 收到的 cmd，用于 argv 断言。
	lastCmd []string
}

// ContainerExecJSON 在 fakeStreamExecer 中不使用，返回空结果。
func (f *fakeStreamExecer) ContainerExecJSON(_ context.Context, _, _ string, _ []string) (runtime.ExecJSONResult, error) {
	return runtime.ExecJSONResult{}, nil
}

// ContainerExecStream 把 lines 全部写入 channel 后关闭，模拟流式输出；同时记录 cmd 用于断言。
func (f *fakeStreamExecer) ContainerExecStream(_ context.Context, _, _ string, cmd []string) (runtime.ExecStreamHandle, error) {
	f.lastCmd = cmd
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

// TestStreamEventsDeliversLines 验证：StreamEvents 把流式行逐条交给 onLine 回调；argv 符合 oc-kanban watch 约定。
func TestStreamEventsDeliversLines(t *testing.T) {
	// 预设两行 NDJSON，验证全部按顺序传给回调，且 argv 符合 oc-kanban watch --board <slug> 约定。
	execer := &fakeStreamExecer{lines: []string{`{"kind":"claimed"}`, `{"kind":"heartbeat"}`}}
	svc := NewHermesKanbanService(execer, &fakeKanbanLocator{loc: healthyLoc()})

	var got []string
	err := svc.StreamEvents(context.Background(), kanbanOrgAdmin(), "app-1", "default", func(l string) {
		got = append(got, l)
	})
	require.NoError(t, err)
	// 两行事件应按顺序全部到达
	assert.Equal(t, []string{`{"kind":"claimed"}`, `{"kind":"heartbeat"}`}, got)
	// argv 必须符合 oc-kanban 契约：oc-kanban watch --board default
	assert.Equal(t, []string{"oc-kanban", "watch", "--board", "default"}, execer.lastCmd)
}

// ————————————————————————————————————————————————————
// 修复 4：写 verb 补充测试
// ————————————————————————————————————————————————————

// TestBlockRejectsEmptyReason 验证：Block 传空 reason 时返回 ErrKanbanBadRequest，且不下发 CLI。
func TestBlockRejectsEmptyReason(t *testing.T) {
	// 阻塞原因为空字符串，不满足 CLI 必填要求，应在进入 execer 前被拒绝。
	execer := &fakeKanbanExecer{}
	svc := NewHermesKanbanService(execer, &fakeKanbanLocator{loc: healthyLoc()})

	_, err := svc.Block(context.Background(), kanbanOrgAdmin(), "app-1", "default", "t_1", "")
	require.ErrorIs(t, err, ErrKanbanBadRequest)
	// 非法输入不应触达 execer
	assert.Nil(t, execer.lastCmd)
}

// TestBlockHappy 验证：Block 正常调用时无错误，且 argv 含 block/taskID/reason；命令以 oc-kanban 开头。
func TestBlockHappy(t *testing.T) {
	// oc-kanban block 返回完整 TaskDetail，验证 argv 正确拼出阻塞命令。
	// oc-kanban 中 reason 改用 --reason flag（不再是 positional），--board 是 verb 后的 flag。
	detailJSON := `{"task":{"id":"t_1","title":"任务","status":"blocked","assignee":"devops","priority":0,"created_at":1779267436,"skills":[]},"latest_summary":null,"parents":[],"children":[],"comments":[],"events":[]}`
	execer := &fakeKanbanExecer{result: runtime.ExecJSONResult{ExitCode: 0, Stdout: okEnvelope(detailJSON)}}
	svc := NewHermesKanbanService(execer, &fakeKanbanLocator{loc: healthyLoc()})

	_, err := svc.Block(context.Background(), kanbanOrgAdmin(), "app-1", "default", "t_1", "等待依赖")
	require.NoError(t, err)
	// argv 须含 block、任务 ID 与阻塞原因
	assert.Equal(t, "oc-kanban", execer.lastCmd[0])
	assert.Contains(t, execer.lastCmd, "block")
	assert.Contains(t, execer.lastCmd, "t_1")
	assert.Contains(t, execer.lastCmd, "等待依赖")
}

// TestReassignRejectsBadProfile 验证：Reassign 传非法 profile（含大写字母和空格）返回 ErrKanbanBadRequest。
func TestReassignRejectsBadProfile(t *testing.T) {
	// 非法 profile 不符合 board slug 规范（boardSlugRe），应在进入 execer 前被拒绝。
	execer := &fakeKanbanExecer{}
	svc := NewHermesKanbanService(execer, &fakeKanbanLocator{loc: healthyLoc()})

	_, err := svc.Reassign(context.Background(), kanbanOrgAdmin(), "app-1", "default", "t_1", "Bad Profile")
	require.ErrorIs(t, err, ErrKanbanBadRequest)
	// 非法 profile 不应触达 execer
	assert.Nil(t, execer.lastCmd)
}

// TestReassignHappy 验证：Reassign 正常调用时 argv 含 reassign 及 --to flag。
func TestReassignHappy(t *testing.T) {
	// oc-kanban reassign 返回完整 TaskDetail，profile 改用 --to flag（不再是 positional 参数）。
	detailJSON := `{"task":{"id":"t_1","title":"任务","status":"ready","assignee":"devops","priority":0,"created_at":1779267436,"skills":[]},"latest_summary":null,"parents":[],"children":[],"comments":[],"events":[]}`
	execer := &fakeKanbanExecer{result: runtime.ExecJSONResult{ExitCode: 0, Stdout: okEnvelope(detailJSON)}}
	svc := NewHermesKanbanService(execer, &fakeKanbanLocator{loc: healthyLoc()})

	_, err := svc.Reassign(context.Background(), kanbanOrgAdmin(), "app-1", "default", "t_1", "devops")
	require.NoError(t, err)
	// argv 须含 reassign 和目标 profile，且使用 --to flag（oc-kanban 契约）
	assert.Contains(t, execer.lastCmd, "reassign")
	assert.Contains(t, execer.lastCmd, "devops")
	assert.Contains(t, execer.lastCmd, "--to")
}

// TestArchiveHappy 验证：Archive 正常调用时 argv 含 archive 及任务 ID，返回 TaskDetail。
func TestArchiveHappy(t *testing.T) {
	// oc-kanban archive 返回完整 TaskDetail，验证 archive 命令正确拼出。
	detailJSON := `{"task":{"id":"t_1","title":"任务","status":"archived","assignee":"devops","priority":0,"created_at":1779267436,"skills":[]},"latest_summary":null,"parents":[],"children":[],"comments":[],"events":[]}`
	execer := &fakeKanbanExecer{result: runtime.ExecJSONResult{ExitCode: 0, Stdout: okEnvelope(detailJSON)}}
	svc := NewHermesKanbanService(execer, &fakeKanbanLocator{loc: healthyLoc()})

	_, err := svc.Archive(context.Background(), kanbanOrgAdmin(), "app-1", "default", "t_1")
	require.NoError(t, err)
	// argv 须含 archive 及任务 ID
	assert.Contains(t, execer.lastCmd, "archive")
	assert.Contains(t, execer.lastCmd, "t_1")
}

// TestCreateTaskRejectsBadAssignee 验证：CreateTask 传非法 assignee（含大写字母和空格）返回 ErrKanbanBadRequest。
func TestCreateTaskRejectsBadAssignee(t *testing.T) {
	// 非法 assignee 不符合 board slug 规范，应在进入 execer 前被拒绝。
	execer := &fakeKanbanExecer{}
	svc := NewHermesKanbanService(execer, &fakeKanbanLocator{loc: healthyLoc()})

	_, err := svc.CreateTask(context.Background(), kanbanOrgAdmin(), "app-1", CreateKanbanTaskInput{
		Title:    "新任务",
		Assignee: "Bad Assignee", // 含大写字母和空格，非法
	})
	require.ErrorIs(t, err, ErrKanbanBadRequest)
	// 非法 assignee 不应触达 execer
	assert.Nil(t, execer.lastCmd)
}

// TestCreateTaskRejectsBadWorkspace 验证：CreateTask 传非法 workspace 值返回 ErrKanbanBadRequest。
func TestCreateTaskRejectsBadWorkspace(t *testing.T) {
	// 平台管理员场景：直接构造含非法 Workspace 的输入，应被格式校验拦截。
	// --workspace 只接受 scratch / worktree / dir:<path> 三种形式。
	execer := &fakeKanbanExecer{}
	svc := NewHermesKanbanService(execer, &fakeKanbanLocator{loc: healthyLoc()})

	_, err := svc.CreateTask(context.Background(), kanbanOrgAdmin(), "app-1", CreateKanbanTaskInput{
		Title:     "新任务",
		Assignee:  "devops",
		Workspace: "bogus", // 不符合 scratch|worktree|dir:<path> 格式
	})
	require.ErrorIs(t, err, ErrKanbanBadRequest)
	// 非法 workspace 值不应触达 execer
	assert.Nil(t, execer.lastCmd)
}

// ————————————————————————————————————————————————————
// 修复 6：StreamEvents ctx 取消路径测试
// ————————————————————————————————————————————————————

// fakeBlockingStreamExecer 是流式 execer 的假实现，其 Lines channel 永不关闭，
// 用于测试 ctx 取消路径——StreamEvents 应在 ctx 取消后退出并返回 context.Canceled。
type fakeBlockingStreamExecer struct{}

// ContainerExecJSON 在此 fake 中不使用，返回空结果。
func (f *fakeBlockingStreamExecer) ContainerExecJSON(_ context.Context, _, _ string, _ []string) (runtime.ExecJSONResult, error) {
	return runtime.ExecJSONResult{}, nil
}

// ContainerExecStream 返回一个永不关闭、永不写入的 Lines channel，模拟长时间 hang 的流。
func (f *fakeBlockingStreamExecer) ContainerExecStream(_ context.Context, _, _ string, _ []string) (runtime.ExecStreamHandle, error) {
	ch := make(chan string) // 永不关闭、永不写入
	return runtime.ExecStreamHandle{
		Lines: ch,
		Err:   func() error { return nil },
		Close: func() {},
	}, nil
}

// TestStreamEventsCancelled 验证：ctx 取消后 StreamEvents 及时退出并返回 context.Canceled。
func TestStreamEventsCancelled(t *testing.T) {
	// 使用永不关闭的流，验证 ctx 取消是唯一退出路径，防止测试死锁。
	svc := NewHermesKanbanService(&fakeBlockingStreamExecer{}, &fakeKanbanLocator{loc: healthyLoc()})

	ctx, cancel := context.WithCancel(context.Background())

	// 用带超时的 channel 接收 StreamEvents 返回值，避免测试死锁。
	errCh := make(chan error, 1)
	go func() {
		errCh <- svc.StreamEvents(ctx, kanbanOrgAdmin(), "app-1", "default", func(_ string) {})
	}()

	// 取消 ctx，StreamEvents 应在合理时间内返回。
	cancel()

	select {
	case err := <-errCh:
		// StreamEvents 应返回 context.Canceled
		require.True(t, errors.Is(err, context.Canceled), "期望 context.Canceled，实际: %v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("StreamEvents 在 ctx 取消后未及时退出，疑似死锁")
	}
}

// TestCapabilitiesHappy 验证：Capabilities 解析 oc-kanban capabilities 信封并映射字段。
func TestCapabilitiesHappy(t *testing.T) {
	// oc-kanban capabilities 返回契约版本、verb 清单与 feature 开关。
	capsJSON := `{"contract_version":"1.0","oc_kanban_version":"1","hermes_version":"v0.14.0",` +
		`"variant":"hermes-v2026.5.16","verbs":["list","show","create"],` +
		`"features":{"write":true,"watch":true,"runs":true,"stats":true}}`
	execer := &fakeKanbanExecer{result: runtime.ExecJSONResult{
		ExitCode: 0, Stdout: okEnvelope(capsJSON),
	}}
	svc := NewHermesKanbanService(execer, &fakeKanbanLocator{loc: healthyLoc()})

	caps, err := svc.Capabilities(context.Background(), kanbanOrgAdmin(), "app-1")
	require.NoError(t, err)
	assert.Equal(t, "1.0", caps.ContractVersion)
	assert.True(t, caps.Features.Write)
	assert.Contains(t, caps.Verbs, "create")
	// 验证 OCKanbanVersion 与 Variant 字段被正确解析（mock 数据中分别为 "1" 和 "hermes-v2026.5.16"）。
	assert.Equal(t, "1", caps.OCKanbanVersion)
	assert.Equal(t, "hermes-v2026.5.16", caps.Variant)
	// argv 为 oc-kanban capabilities
	assert.Equal(t, []string{"oc-kanban", "capabilities"}, execer.lastCmd)
}
