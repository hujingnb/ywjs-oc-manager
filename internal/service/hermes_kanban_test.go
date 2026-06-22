package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/integrations/ocops"
)

// fakeKanbanOps 是 kanbanOps 的假实现：记录最后一次调用的入参，返回预设桩值/错误。
// 每个方法只关心被测路径需要的字段，未用到的留零值。
type fakeKanbanOps struct {
	// lastEndpoint 记录最近一次被调用方法收到的 endpoint，便于断言坐标透传。
	lastEndpoint ocops.Endpoint
	// lastBoard / lastID 记录按 board / task id 定位的方法收到的参数。
	lastBoard string
	lastID    string
	// lastStatus / lastAssignee 记录 KanbanList 收到的过滤参数。
	lastStatus   string
	lastAssignee string
	// lastBody / lastResult / lastReason / lastTo 记录写方法收到的自由文本/目标参数。
	lastBody   string
	lastResult string
	lastReason string
	lastTo     string
	// lastCreateReq 记录 CreateTask 构造出的类型化请求体。
	lastCreateReq ocops.KanbanCreateReq
	// called 记录最近一次被调用的方法名，断言「未触达上游」时用。
	called string

	// 各方法的桩返回值。
	caps   ocops.KanbanCapabilities
	boards []ocops.KanbanBoard
	tasks  []ocops.KanbanTask
	detail ocops.KanbanTaskDetail
	runs   []ocops.KanbanTaskRun
	stats  ocops.KanbanStats
	// watchEvents 是 WatchKanban 返回 channel 里预填的事件。
	watchEvents []ocops.KanbanEvent
	// err 为非 nil 时所有方法直接返回它，用于覆盖 mapOcOpsKanbanErr 路径。
	err error
}

func (f *fakeKanbanOps) KanbanCapabilities(_ context.Context, ep ocops.Endpoint) (ocops.KanbanCapabilities, error) {
	f.called, f.lastEndpoint = "capabilities", ep
	return f.caps, f.err
}

func (f *fakeKanbanOps) KanbanBoards(_ context.Context, ep ocops.Endpoint) ([]ocops.KanbanBoard, error) {
	f.called, f.lastEndpoint = "boards", ep
	return f.boards, f.err
}

func (f *fakeKanbanOps) KanbanList(_ context.Context, ep ocops.Endpoint, board, status, assignee string) ([]ocops.KanbanTask, error) {
	f.called, f.lastEndpoint, f.lastBoard, f.lastStatus, f.lastAssignee = "list", ep, board, status, assignee
	return f.tasks, f.err
}

func (f *fakeKanbanOps) KanbanShow(_ context.Context, ep ocops.Endpoint, board, id string) (ocops.KanbanTaskDetail, error) {
	f.called, f.lastEndpoint, f.lastBoard, f.lastID = "show", ep, board, id
	return f.detail, f.err
}

func (f *fakeKanbanOps) KanbanRuns(_ context.Context, ep ocops.Endpoint, board, id string) ([]ocops.KanbanTaskRun, error) {
	f.called, f.lastEndpoint, f.lastBoard, f.lastID = "runs", ep, board, id
	return f.runs, f.err
}

func (f *fakeKanbanOps) KanbanStats(_ context.Context, ep ocops.Endpoint, board string) (ocops.KanbanStats, error) {
	f.called, f.lastEndpoint, f.lastBoard = "stats", ep, board
	return f.stats, f.err
}

func (f *fakeKanbanOps) KanbanCreate(_ context.Context, ep ocops.Endpoint, req ocops.KanbanCreateReq) (ocops.KanbanTaskDetail, error) {
	f.called, f.lastEndpoint, f.lastCreateReq = "create", ep, req
	return f.detail, f.err
}

func (f *fakeKanbanOps) KanbanComment(_ context.Context, ep ocops.Endpoint, board, id, body string) (ocops.KanbanTaskDetail, error) {
	f.called, f.lastEndpoint, f.lastBoard, f.lastID, f.lastBody = "comment", ep, board, id, body
	return f.detail, f.err
}

func (f *fakeKanbanOps) KanbanComplete(_ context.Context, ep ocops.Endpoint, board, id, result string) (ocops.KanbanTaskDetail, error) {
	f.called, f.lastEndpoint, f.lastBoard, f.lastID, f.lastResult = "complete", ep, board, id, result
	return f.detail, f.err
}

func (f *fakeKanbanOps) KanbanBlock(_ context.Context, ep ocops.Endpoint, board, id, reason string) (ocops.KanbanTaskDetail, error) {
	f.called, f.lastEndpoint, f.lastBoard, f.lastID, f.lastReason = "block", ep, board, id, reason
	return f.detail, f.err
}

func (f *fakeKanbanOps) KanbanUnblock(_ context.Context, ep ocops.Endpoint, board, id string) (ocops.KanbanTaskDetail, error) {
	f.called, f.lastEndpoint, f.lastBoard, f.lastID = "unblock", ep, board, id
	return f.detail, f.err
}

func (f *fakeKanbanOps) KanbanArchive(_ context.Context, ep ocops.Endpoint, board, id string) (ocops.KanbanTaskDetail, error) {
	f.called, f.lastEndpoint, f.lastBoard, f.lastID = "archive", ep, board, id
	return f.detail, f.err
}

func (f *fakeKanbanOps) KanbanReassign(_ context.Context, ep ocops.Endpoint, board, id, to string) (ocops.KanbanTaskDetail, error) {
	f.called, f.lastEndpoint, f.lastBoard, f.lastID, f.lastTo = "reassign", ep, board, id, to
	return f.detail, f.err
}

func (f *fakeKanbanOps) KanbanReclaim(_ context.Context, ep ocops.Endpoint, board, id string) (ocops.KanbanTaskDetail, error) {
	f.called, f.lastEndpoint, f.lastBoard, f.lastID = "reclaim", ep, board, id
	return f.detail, f.err
}

// WatchKanban 返回一个预填 watchEvents 后关闭的 channel；err 非 nil 时直接返回错误。
func (f *fakeKanbanOps) WatchKanban(_ context.Context, ep ocops.Endpoint, board string) (<-chan ocops.KanbanEvent, error) {
	f.called, f.lastEndpoint, f.lastBoard = "watch", ep, board
	if f.err != nil {
		return nil, f.err
	}
	ch := make(chan ocops.KanbanEvent, len(f.watchEvents))
	for _, ev := range f.watchEvents {
		ch <- ev
	}
	close(ch)
	return ch, nil
}

// healthyKanbanLoc 返回一个可调用 oc-ops 的正常 app 坐标（Supported 且 BaseURL 非空）。
func healthyKanbanLoc() OcOpsAppLocation {
	return OcOpsAppLocation{
		OrgID:       "org-1",
		OwnerUserID: "u-1",
		Endpoint:    ocops.Endpoint{BaseURL: "http://app-1-ocops:8080"},
		Supported:   true,
	}
}

// kanbanOrgAdmin 是 org-1 的组织管理员 principal。
func kanbanOrgAdmin() auth.Principal {
	return auth.Principal{UserID: "admin-1", OrgID: "org-1", Role: domain.UserRoleOrgAdmin}
}

// TestKanbanListTasksHappy 验证：正常 app 上 ListTasks 透传 endpoint/board 并返回 oc-ops 任务列表。
func TestKanbanListTasksHappy(t *testing.T) {
	ops := &fakeKanbanOps{tasks: []ocops.KanbanTask{{ID: "t_1", Title: "任务一", Status: "running", Assignee: "devops", Priority: 3}}}
	svc := NewHermesKanbanService(ops, &fakeOcOpsResolver{loc: healthyKanbanLoc()})

	tasks, err := svc.ListTasks(context.Background(), kanbanOrgAdmin(), "app-1", KanbanTaskFilter{})
	require.NoError(t, err)
	require.Len(t, tasks, 1)
	assert.Equal(t, "t_1", tasks[0].ID)
	assert.Equal(t, "running", tasks[0].Status)
	// board 空值回退为 default，且 endpoint 透传到 ops
	assert.Equal(t, "default", ops.lastBoard)
	assert.Equal(t, "http://app-1-ocops:8080", ops.lastEndpoint.BaseURL)
}

// TestKanbanListTasksForwardsFilters 验证：合法 status/assignee 过滤参数原样透传给 ops。
func TestKanbanListTasksForwardsFilters(t *testing.T) {
	ops := &fakeKanbanOps{}
	svc := NewHermesKanbanService(ops, &fakeOcOpsResolver{loc: healthyKanbanLoc()})

	_, err := svc.ListTasks(context.Background(), kanbanOrgAdmin(), "app-1", KanbanTaskFilter{Board: "team", Status: "running", Assignee: "devops"})
	require.NoError(t, err)
	// 合法过滤参数应透传给 ops（status 命中白名单、assignee 命中 slug 规范）
	assert.Equal(t, "team", ops.lastBoard)
	assert.Equal(t, "running", ops.lastStatus)
	assert.Equal(t, "devops", ops.lastAssignee)
}

// TestKanbanListTasksRejectsBadBoard 验证：非法 board slug 被白名单拦截，返回 ErrKanbanBadRequest 且不触达 ops。
func TestKanbanListTasksRejectsBadBoard(t *testing.T) {
	cases := []struct {
		name  string
		board string
	}{
		{"含空格和大写字母的非法 slug", "Bad Board"}, // 场景：board 含空格及大写字母，不符合小写 a-z0-9 规范
		{"含分号空格的注入式非法 slug", "abc; rm"},   // 场景：board 含分号和空格，防止请求注入
	}
	for _, c := range cases {
		// 当前子测试覆盖非法 board slug 格式被拦截、不触达 ops 的边界条件。
		t.Run(c.name, func(t *testing.T) {
			ops := &fakeKanbanOps{}
			svc := NewHermesKanbanService(ops, &fakeOcOpsResolver{loc: healthyKanbanLoc()})

			_, err := svc.ListTasks(context.Background(), kanbanOrgAdmin(), "app-1", KanbanTaskFilter{Board: c.board})
			require.ErrorIs(t, err, ErrKanbanBadRequest)
			assert.Empty(t, ops.called) // 非法输入不应触达 ops
		})
	}
}

// TestKanbanListTasksRejectsBadStatus 验证：非法 status 过滤值被白名单拦截，不触达 ops。
func TestKanbanListTasksRejectsBadStatus(t *testing.T) {
	ops := &fakeKanbanOps{}
	svc := NewHermesKanbanService(ops, &fakeOcOpsResolver{loc: healthyKanbanLoc()})

	_, err := svc.ListTasks(context.Background(), kanbanOrgAdmin(), "app-1", KanbanTaskFilter{Status: "bogus; rm -rf"})
	require.ErrorIs(t, err, ErrKanbanBadRequest)
	assert.Empty(t, ops.called) // 非法输入不应触达 ops
}

// TestKanbanResolveForbidden 验证：非本组织用户访问 Kanban 被拒，且不触达 ops。
func TestKanbanResolveForbidden(t *testing.T) {
	ops := &fakeKanbanOps{}
	svc := NewHermesKanbanService(ops, &fakeOcOpsResolver{loc: healthyKanbanLoc()})
	outsider := auth.Principal{UserID: "x", OrgID: "org-2", Role: domain.UserRoleOrgAdmin}

	_, err := svc.ListTasks(context.Background(), outsider, "app-1", KanbanTaskFilter{})
	require.ErrorIs(t, err, ErrKanbanForbidden)
	assert.Empty(t, ops.called) // 鉴权失败不应触达 ops
}

// TestKanbanResolveStubUnsupported 验证：dev stub 实例（Supported=false）返回 ErrKanbanNotSupported。
func TestKanbanResolveStubUnsupported(t *testing.T) {
	loc := healthyKanbanLoc()
	loc.Supported = false
	ops := &fakeKanbanOps{}
	svc := NewHermesKanbanService(ops, &fakeOcOpsResolver{loc: loc})

	_, err := svc.ListTasks(context.Background(), kanbanOrgAdmin(), "app-1", KanbanTaskFilter{})
	require.ErrorIs(t, err, ErrKanbanNotSupported)
	assert.Empty(t, ops.called) // 不支持的实例不应触达 ops
}

// TestKanbanResolveRuntimeUnavailable 验证：BaseURL 为空（运行时未就绪）返回 ErrKanbanRuntimeUnavailable。
func TestKanbanResolveRuntimeUnavailable(t *testing.T) {
	loc := healthyKanbanLoc()
	loc.Endpoint.BaseURL = ""
	ops := &fakeKanbanOps{}
	svc := NewHermesKanbanService(ops, &fakeOcOpsResolver{loc: loc})

	_, err := svc.ListTasks(context.Background(), kanbanOrgAdmin(), "app-1", KanbanTaskFilter{})
	require.ErrorIs(t, err, ErrKanbanRuntimeUnavailable)
	assert.Empty(t, ops.called) // 运行时不可用不应触达 ops
}

// TestKanbanResolverError 验证：resolver 返回 ErrNotFound 时原样透传（app 不存在）。
func TestKanbanResolverError(t *testing.T) {
	ops := &fakeKanbanOps{}
	svc := NewHermesKanbanService(ops, &fakeOcOpsResolver{err: ErrNotFound})

	_, err := svc.ListTasks(context.Background(), kanbanOrgAdmin(), "app-1", KanbanTaskFilter{})
	require.ErrorIs(t, err, ErrNotFound)
	assert.Empty(t, ops.called) // resolve 失败不应触达 ops
}

// TestKanbanErrorMapping 验证：ops 返回的 ocops 哨兵错误被 mapOcOpsKanbanErr 正确翻译成 service 哨兵错误。
func TestKanbanErrorMapping(t *testing.T) {
	cases := []struct {
		name    string // 测试场景
		opsErr  error  // ops 返回的 ocops 哨兵错误
		wantErr error  // 期望映射到的 service 哨兵错误
	}{
		{"参数非法映射为 BadRequest", ocops.ErrBadRequest, ErrKanbanBadRequest},      // ErrBadRequest → ErrKanbanBadRequest
		{"资源不存在映射为 NotFound", ocops.ErrNotFound, ErrNotFound},                 // ErrNotFound → ErrNotFound
		{"能力不支持映射为 NotSupported", ocops.ErrUnsupported, ErrKanbanNotSupported}, // ErrUnsupported → ErrKanbanNotSupported
		{"输出非法映射为 OutputInvalid", ocops.ErrOutputInvalid, ErrKanbanOutputInvalid}, // ErrOutputInvalid → ErrKanbanOutputInvalid
		{"未知错误兜底为 CLI 错误", errors.New("boom"), ErrKanbanCLI},                  // 未列举错误 → default → ErrKanbanCLI
	}
	for _, c := range cases {
		// 每个子测试覆盖一种 ocops 错误到 service 哨兵错误的映射路径。
		t.Run(c.name, func(t *testing.T) {
			ops := &fakeKanbanOps{err: c.opsErr}
			svc := NewHermesKanbanService(ops, &fakeOcOpsResolver{loc: healthyKanbanLoc()})
			_, err := svc.ListTasks(context.Background(), kanbanOrgAdmin(), "app-1", KanbanTaskFilter{})
			require.ErrorIs(t, err, c.wantErr)
		})
	}
}

// TestKanbanShowTaskHappy 验证：ShowTask 透传 board/taskID 并返回 oc-ops 任务详情。
func TestKanbanShowTaskHappy(t *testing.T) {
	ops := &fakeKanbanOps{detail: ocops.KanbanTaskDetail{Task: ocops.KanbanTask{ID: "t_abc", Title: "详情任务", Status: "blocked"}}}
	svc := NewHermesKanbanService(ops, &fakeOcOpsResolver{loc: healthyKanbanLoc()})

	detail, err := svc.ShowTask(context.Background(), kanbanOrgAdmin(), "app-1", "default", "t_abc")
	require.NoError(t, err)
	assert.Equal(t, "t_abc", detail.Task.ID)
	assert.Equal(t, "blocked", detail.Task.Status)
	// board / taskID 透传给 ops
	assert.Equal(t, "default", ops.lastBoard)
	assert.Equal(t, "t_abc", ops.lastID)
}

// TestKanbanShowTaskRejectsBadTaskID 验证：非法 taskID 被白名单拦截，不触达 ops。
func TestKanbanShowTaskRejectsBadTaskID(t *testing.T) {
	// 非法 taskID 包含分号，典型的注入尝试，应被 taskIDRe 正则拦截。
	ops := &fakeKanbanOps{}
	svc := NewHermesKanbanService(ops, &fakeOcOpsResolver{loc: healthyKanbanLoc()})

	_, err := svc.ShowTask(context.Background(), kanbanOrgAdmin(), "app-1", "default", "t_1; rm")
	require.ErrorIs(t, err, ErrKanbanBadRequest)
	assert.Empty(t, ops.called) // 非法输入不应触达 ops
}

// TestKanbanListBoardsHappy 验证：ListBoards 返回 oc-ops boards 列表并透传 endpoint。
func TestKanbanListBoardsHappy(t *testing.T) {
	ops := &fakeKanbanOps{boards: []ocops.KanbanBoard{{Slug: "default", Name: "默认看板"}}}
	svc := NewHermesKanbanService(ops, &fakeOcOpsResolver{loc: healthyKanbanLoc()})

	boards, err := svc.ListBoards(context.Background(), kanbanOrgAdmin(), "app-1")
	require.NoError(t, err)
	require.Len(t, boards, 1)
	assert.Equal(t, "default", boards[0].Slug)
	assert.Equal(t, "boards", ops.called)
}

// TestKanbanStatsHappy 验证：Stats 返回 oc-ops 统计数据，by_status 计数正确映射。
func TestKanbanStatsHappy(t *testing.T) {
	ops := &fakeKanbanOps{stats: ocops.KanbanStats{ByStatus: map[string]int{"todo": 2, "running": 1, "done": 5}, Now: 1779267460}}
	svc := NewHermesKanbanService(ops, &fakeOcOpsResolver{loc: healthyKanbanLoc()})

	stats, err := svc.Stats(context.Background(), kanbanOrgAdmin(), "app-1", "default")
	require.NoError(t, err)
	assert.Equal(t, 2, stats.ByStatus["todo"])
	assert.Equal(t, 5, stats.ByStatus["done"])
	assert.Equal(t, int64(1779267460), stats.Now)
}

// ————————————————————————————————————————————————————
// 写 verb 单测
// ————————————————————————————————————————————————————

// TestKanbanCreateTaskHappy 验证：CreateTask 构造正确的 KanbanCreateReq 并返回 oc-ops 详情。
func TestKanbanCreateTaskHappy(t *testing.T) {
	ops := &fakeKanbanOps{detail: ocops.KanbanTaskDetail{Task: ocops.KanbanTask{ID: "t_new", Title: "新任务", Status: "ready"}}}
	svc := NewHermesKanbanService(ops, &fakeOcOpsResolver{loc: healthyKanbanLoc()})

	detail, err := svc.CreateTask(context.Background(), kanbanOrgAdmin(), "app-1", CreateKanbanTaskInput{
		Title: "新任务", Assignee: "devops", Priority: 2, Body: "描述",
	})
	require.NoError(t, err)
	assert.Equal(t, "t_new", detail.Task.ID)
	// 校验构造出的请求体：board 回退 default、各基础字段透传
	assert.Equal(t, "default", ops.lastCreateReq.Board)
	assert.Equal(t, "新任务", ops.lastCreateReq.Title)
	assert.Equal(t, "devops", ops.lastCreateReq.Assignee)
	assert.Equal(t, 2, ops.lastCreateReq.Priority)
	assert.Equal(t, "描述", ops.lastCreateReq.Body)
}

// TestKanbanCreateTaskMapsAdvancedFields 验证：高级字段（skills/workspace/parent/max_retries）正确映射进请求体。
func TestKanbanCreateTaskMapsAdvancedFields(t *testing.T) {
	ops := &fakeKanbanOps{detail: ocops.KanbanTaskDetail{Task: ocops.KanbanTask{ID: "t_new"}}}
	svc := NewHermesKanbanService(ops, &fakeOcOpsResolver{loc: healthyKanbanLoc()})

	_, err := svc.CreateTask(context.Background(), kanbanOrgAdmin(), "app-1", CreateKanbanTaskInput{
		Title: "新任务", Assignee: "devops",
		Skills: []string{"go-dev"}, Workspace: "scratch", ParentID: "t_parent", MaxRetries: 3,
	})
	require.NoError(t, err)
	// 高级字段须原样映射到 KanbanCreateReq（注意 ParentID → Parent）
	assert.Equal(t, []string{"go-dev"}, ops.lastCreateReq.Skills)
	assert.Equal(t, "scratch", ops.lastCreateReq.Workspace)
	assert.Equal(t, "t_parent", ops.lastCreateReq.Parent)
	assert.Equal(t, 3, ops.lastCreateReq.MaxRetries)
}

// TestKanbanCreateTaskRejectsEmptyTitle 验证：空标题或全空格标题被 ErrKanbanBadRequest 拦截，不触达 ops。
func TestKanbanCreateTaskRejectsEmptyTitle(t *testing.T) {
	// 空白字符串标题属于非法输入，应在构造请求体前被拒绝。
	ops := &fakeKanbanOps{}
	svc := NewHermesKanbanService(ops, &fakeOcOpsResolver{loc: healthyKanbanLoc()})
	_, err := svc.CreateTask(context.Background(), kanbanOrgAdmin(), "app-1", CreateKanbanTaskInput{
		Title: "  ", Assignee: "devops",
	})
	require.ErrorIs(t, err, ErrKanbanBadRequest)
	assert.Empty(t, ops.called) // 非法输入不应触达 ops
}

// TestKanbanCreateTaskRejectsBadAssignee 验证：非法 assignee（含大写字母和空格）被拦截。
func TestKanbanCreateTaskRejectsBadAssignee(t *testing.T) {
	// 非法 assignee 不符合 board slug 规范，应在构造请求体前被拒绝。
	ops := &fakeKanbanOps{}
	svc := NewHermesKanbanService(ops, &fakeOcOpsResolver{loc: healthyKanbanLoc()})
	_, err := svc.CreateTask(context.Background(), kanbanOrgAdmin(), "app-1", CreateKanbanTaskInput{
		Title: "新任务", Assignee: "Bad Assignee",
	})
	require.ErrorIs(t, err, ErrKanbanBadRequest)
	// 错误文案须包含可照做的格式要求，便于用户自助纠正（不能只回笼统的「非法 assignee」）。
	assert.Contains(t, err.Error(), "只能由小写字母")
	assert.Empty(t, ops.called) // 非法 assignee 不应触达 ops
}

// TestKanbanCreateTaskRejectsBadWorkspace 验证：非法 workspace 值被格式校验拦截。
func TestKanbanCreateTaskRejectsBadWorkspace(t *testing.T) {
	// --workspace 只接受 scratch / worktree / dir:<path> 三种形式，bogus 非法。
	ops := &fakeKanbanOps{}
	svc := NewHermesKanbanService(ops, &fakeOcOpsResolver{loc: healthyKanbanLoc()})
	_, err := svc.CreateTask(context.Background(), kanbanOrgAdmin(), "app-1", CreateKanbanTaskInput{
		Title: "新任务", Assignee: "devops", Workspace: "bogus",
	})
	require.ErrorIs(t, err, ErrKanbanBadRequest)
	assert.Empty(t, ops.called) // 非法 workspace 值不应触达 ops
}

// TestKanbanWriteVerbForbiddenForOutsider 验证：非本组织成员不能执行写 verb（Comment），不触达 ops。
func TestKanbanWriteVerbForbiddenForOutsider(t *testing.T) {
	// outsider 属于 org-2，与 app 归属的 org-1 不同，应被 resolveManage 拒绝。
	ops := &fakeKanbanOps{}
	svc := NewHermesKanbanService(ops, &fakeOcOpsResolver{loc: healthyKanbanLoc()})
	outsider := auth.Principal{UserID: "x", OrgID: "org-2", Role: domain.UserRoleOrgAdmin}
	_, err := svc.Comment(context.Background(), outsider, "app-1", "default", "t_1", "hi")
	require.ErrorIs(t, err, ErrKanbanForbidden)
	assert.Empty(t, ops.called) // 鉴权失败不应触达 ops
}

// TestKanbanCommentRejectsBadTaskID 验证：Comment 的非法 task id 被白名单拦截，不触达 ops。
func TestKanbanCommentRejectsBadTaskID(t *testing.T) {
	// taskID 含分号是典型注入尝试，应被 taskIDRe 正则拒绝。
	ops := &fakeKanbanOps{}
	svc := NewHermesKanbanService(ops, &fakeOcOpsResolver{loc: healthyKanbanLoc()})
	_, err := svc.Comment(context.Background(), kanbanOrgAdmin(), "app-1", "default", "t_1; rm -rf /", "hi")
	require.ErrorIs(t, err, ErrKanbanBadRequest)
	assert.Empty(t, ops.called) // 非法 taskID 不应触达 ops
}

// TestKanbanCommentRejectsEmptyBody 验证：Comment 空评论内容被 ErrKanbanBadRequest 拦截。
func TestKanbanCommentRejectsEmptyBody(t *testing.T) {
	// 评论内容为空白字符串不满足必填要求，应在触达 ops 前被拒绝。
	ops := &fakeKanbanOps{}
	svc := NewHermesKanbanService(ops, &fakeOcOpsResolver{loc: healthyKanbanLoc()})
	_, err := svc.Comment(context.Background(), kanbanOrgAdmin(), "app-1", "default", "t_1", "  ")
	require.ErrorIs(t, err, ErrKanbanBadRequest)
	assert.Empty(t, ops.called) // 空评论不应触达 ops
}

// TestKanbanCompleteHappy 验证：Complete 透传 result 自由文本并返回 TaskDetail。
func TestKanbanCompleteHappy(t *testing.T) {
	ops := &fakeKanbanOps{detail: ocops.KanbanTaskDetail{Task: ocops.KanbanTask{ID: "t_1", Status: "done"}}}
	svc := NewHermesKanbanService(ops, &fakeOcOpsResolver{loc: healthyKanbanLoc()})

	detail, err := svc.Complete(context.Background(), kanbanOrgAdmin(), "app-1", "default", "t_1", "已完成")
	require.NoError(t, err)
	assert.Equal(t, "t_1", detail.Task.ID)
	// result 自由文本应透传给 ops
	assert.Equal(t, "已完成", ops.lastResult)
	assert.Equal(t, "t_1", ops.lastID)
}

// TestKanbanBlockRejectsEmptyReason 验证：Block 传空 reason 时返回 ErrKanbanBadRequest，不触达 ops。
func TestKanbanBlockRejectsEmptyReason(t *testing.T) {
	// 阻塞原因为空字符串，不满足必填要求，应在触达 ops 前被拒绝。
	ops := &fakeKanbanOps{}
	svc := NewHermesKanbanService(ops, &fakeOcOpsResolver{loc: healthyKanbanLoc()})

	_, err := svc.Block(context.Background(), kanbanOrgAdmin(), "app-1", "default", "t_1", "")
	require.ErrorIs(t, err, ErrKanbanBadRequest)
	assert.Empty(t, ops.called) // 非法输入不应触达 ops
}

// TestKanbanBlockHappy 验证：Block 透传 reason 并返回 TaskDetail。
func TestKanbanBlockHappy(t *testing.T) {
	ops := &fakeKanbanOps{detail: ocops.KanbanTaskDetail{Task: ocops.KanbanTask{ID: "t_1", Status: "blocked"}}}
	svc := NewHermesKanbanService(ops, &fakeOcOpsResolver{loc: healthyKanbanLoc()})

	_, err := svc.Block(context.Background(), kanbanOrgAdmin(), "app-1", "default", "t_1", "等待依赖")
	require.NoError(t, err)
	assert.Equal(t, "block", ops.called)
	assert.Equal(t, "等待依赖", ops.lastReason)
}

// TestKanbanReassignRejectsBadProfile 验证：Reassign 传非法 profile 返回 ErrKanbanBadRequest，不触达 ops。
func TestKanbanReassignRejectsBadProfile(t *testing.T) {
	// 非法 profile 不符合 board slug 规范（boardSlugRe），应在触达 ops 前被拒绝。
	ops := &fakeKanbanOps{}
	svc := NewHermesKanbanService(ops, &fakeOcOpsResolver{loc: healthyKanbanLoc()})

	_, err := svc.Reassign(context.Background(), kanbanOrgAdmin(), "app-1", "default", "t_1", "Bad Profile")
	require.ErrorIs(t, err, ErrKanbanBadRequest)
	assert.Empty(t, ops.called) // 非法 profile 不应触达 ops
}

// TestKanbanReassignHappy 验证：Reassign 把目标 profile 透传到 ops 的 to 参数。
func TestKanbanReassignHappy(t *testing.T) {
	ops := &fakeKanbanOps{detail: ocops.KanbanTaskDetail{Task: ocops.KanbanTask{ID: "t_1"}}}
	svc := NewHermesKanbanService(ops, &fakeOcOpsResolver{loc: healthyKanbanLoc()})

	_, err := svc.Reassign(context.Background(), kanbanOrgAdmin(), "app-1", "default", "t_1", "devops")
	require.NoError(t, err)
	assert.Equal(t, "reassign", ops.called)
	assert.Equal(t, "devops", ops.lastTo)
}

// TestKanbanArchiveHappy 验证：Archive 透传 board/taskID 并返回 TaskDetail。
func TestKanbanArchiveHappy(t *testing.T) {
	ops := &fakeKanbanOps{detail: ocops.KanbanTaskDetail{Task: ocops.KanbanTask{ID: "t_1", Status: "archived"}}}
	svc := NewHermesKanbanService(ops, &fakeOcOpsResolver{loc: healthyKanbanLoc()})

	_, err := svc.Archive(context.Background(), kanbanOrgAdmin(), "app-1", "default", "t_1")
	require.NoError(t, err)
	assert.Equal(t, "archive", ops.called)
	assert.Equal(t, "t_1", ops.lastID)
}

// TestKanbanCapabilitiesHappy 验证：Capabilities 返回 oc-ops 能力数据并透传 endpoint。
func TestKanbanCapabilitiesHappy(t *testing.T) {
	ops := &fakeKanbanOps{caps: ocops.KanbanCapabilities{ContractVersion: "1.0", Verbs: []string{"list", "show", "create"}}}
	svc := NewHermesKanbanService(ops, &fakeOcOpsResolver{loc: healthyKanbanLoc()})

	caps, err := svc.Capabilities(context.Background(), kanbanOrgAdmin(), "app-1")
	require.NoError(t, err)
	assert.Equal(t, "1.0", caps.ContractVersion)
	assert.Contains(t, caps.Verbs, "create")
}

// ————————————————————————————————————————————————————
// 实时事件流单测
// ————————————————————————————————————————————————————

// TestKanbanWatchEventsForwardsEvents 验证：WatchEvents 鉴权通过后返回 ops 的事件 channel，事件按序到达。
func TestKanbanWatchEventsForwardsEvents(t *testing.T) {
	// 预填两条事件，验证 WatchEvents 把 ops 返回的 channel 原样透出，调用方可逐条消费。
	ops := &fakeKanbanOps{watchEvents: []ocops.KanbanEvent{{TaskID: "t_1", Kind: "created"}, {TaskID: "t_1", Kind: "status_changed"}}}
	svc := NewHermesKanbanService(ops, &fakeOcOpsResolver{loc: healthyKanbanLoc()})

	ch, err := svc.WatchEvents(context.Background(), kanbanOrgAdmin(), "app-1", "default")
	require.NoError(t, err)
	var got []string
	for ev := range ch {
		got = append(got, ev.Kind)
	}
	// 两条事件应按顺序全部到达
	assert.Equal(t, []string{"created", "status_changed"}, got)
	// board 透传给 ops（空值回退 default）
	assert.Equal(t, "default", ops.lastBoard)
}

// TestKanbanWatchEventsForbidden 验证：非本组织用户订阅事件流被拒，不触达 ops。
func TestKanbanWatchEventsForbidden(t *testing.T) {
	// outsider 属于 org-2，应在建立事件流前被 resolve 拒绝。
	ops := &fakeKanbanOps{}
	svc := NewHermesKanbanService(ops, &fakeOcOpsResolver{loc: healthyKanbanLoc()})
	outsider := auth.Principal{UserID: "x", OrgID: "org-2", Role: domain.UserRoleOrgAdmin}

	_, err := svc.WatchEvents(context.Background(), outsider, "app-1", "default")
	require.ErrorIs(t, err, ErrKanbanForbidden)
	assert.Empty(t, ops.called) // 鉴权失败不应触达 ops
}

// TestKanbanWatchEventsRejectsBadBoard 验证：WatchEvents 非法 board slug 被拦截，不触达 ops。
func TestKanbanWatchEventsRejectsBadBoard(t *testing.T) {
	// 非法 board slug 应在建立事件流前被 validateBoard 拒绝。
	ops := &fakeKanbanOps{}
	svc := NewHermesKanbanService(ops, &fakeOcOpsResolver{loc: healthyKanbanLoc()})

	_, err := svc.WatchEvents(context.Background(), kanbanOrgAdmin(), "app-1", "Bad Board")
	require.ErrorIs(t, err, ErrKanbanBadRequest)
	assert.Empty(t, ops.called) // 非法 board 不应触达 ops
}

// TestKanbanWatchEventsMapsError 验证：WatchKanban 返回 ocops 哨兵错误时被 mapOcOpsKanbanErr 翻译。
func TestKanbanWatchEventsMapsError(t *testing.T) {
	// ops 在建立事件流时返回 UNSUPPORTED，应被翻译为 ErrKanbanNotSupported。
	ops := &fakeKanbanOps{err: ocops.ErrUnsupported}
	svc := NewHermesKanbanService(ops, &fakeOcOpsResolver{loc: healthyKanbanLoc()})

	_, err := svc.WatchEvents(context.Background(), kanbanOrgAdmin(), "app-1", "default")
	require.ErrorIs(t, err, ErrKanbanNotSupported)
}
