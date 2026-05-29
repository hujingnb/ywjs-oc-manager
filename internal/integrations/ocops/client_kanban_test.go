// client_kanban_test.go — kanban 14 个非流式客户端方法的 httptest 单元测试。
//
// 每个测试用 httptest.Server 断言方法发出的 HTTP method / path / query / body
// 与契约表一致，并验证响应正确解码或错误正确映射。
// board 统一走 query 参数；写操作的业务字段走 JSON body。
package ocops_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"oc-manager/internal/integrations/ocops"
)

// TestKanbanCapabilities 验证 KanbanCapabilities 发出 GET /oc/kanban/capabilities
// 并把响应正确解码为 KanbanCapabilities 结构体。
func TestKanbanCapabilities(t *testing.T) {
	// 正常路径：server 返回能力信息，断言字段解码正确
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 断言 method 和 path
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "/oc/kanban/capabilities", r.URL.Path)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"contract_version": "1.0",
			"oc_kanban_version": "2026.5",
			"variant": "hermes-v2026.5.16",
			"verbs": ["list","show","create"],
			"features": {"write":true,"watch":true,"runs":true,"stats":true}
		}`))
	}))
	defer srv.Close()

	c, ep := newTestClient(srv)
	caps, err := c.KanbanCapabilities(context.Background(), ep)
	require.NoError(t, err)
	// 断言关键字段解码正确
	assert.Equal(t, "1.0", caps.ContractVersion)
	assert.Equal(t, "hermes-v2026.5.16", caps.Variant)
	assert.Equal(t, []string{"list", "show", "create"}, caps.Verbs)
	assert.True(t, caps.Features.Write)
	assert.True(t, caps.Features.Watch)
}

// TestKanbanBoards 验证 KanbanBoards 发出 GET /oc/kanban/boards
// 并将响应解码为 []KanbanBoard。
func TestKanbanBoards(t *testing.T) {
	// 正常路径：server 返回 2 个 board，断言 slug/name 解码正确
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "/oc/kanban/boards", r.URL.Path)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[
			{"slug":"default","name":"Default Board"},
			{"slug":"release","name":"Release Board"}
		]`))
	}))
	defer srv.Close()

	c, ep := newTestClient(srv)
	boards, err := c.KanbanBoards(context.Background(), ep)
	require.NoError(t, err)
	require.Len(t, boards, 2)
	// 断言各 board 字段正确
	assert.Equal(t, "default", boards[0].Slug)
	assert.Equal(t, "Default Board", boards[0].Name)
	assert.Equal(t, "release", boards[1].Slug)
}

// TestKanbanBoardsUnsupported 验证 stub 镜像（409 UNSUPPORTED）时 KanbanBoards 返回 ErrUnsupported。
func TestKanbanBoardsUnsupported(t *testing.T) {
	// 异常路径：server 返回 409，客户端映射为 ErrUnsupported
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"code":"UNSUPPORTED","message":"此镜像不含真实 hermes"}`))
	}))
	defer srv.Close()

	c, ep := newTestClient(srv)
	_, err := c.KanbanBoards(context.Background(), ep)
	// 必须能通过 errors.Is 识别为 ErrUnsupported
	require.ErrorIs(t, err, ocops.ErrUnsupported)
}

// TestKanbanListBoardQuery 验证 KanbanList 发出 GET /oc/kanban/tasks?board=<board>
// 且 board query 参数正确传递。
func TestKanbanListBoardQuery(t *testing.T) {
	// 正常路径：只传 board，status/assignee 为空时不携带这两个 query 参数
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "/oc/kanban/tasks", r.URL.Path)
		// 断言 board query 参数正确传入
		assert.Equal(t, "default", r.URL.Query().Get("board"))
		// 空字符串 status/assignee 时不应出现在 query
		assert.Empty(t, r.URL.Query().Get("status"))
		assert.Empty(t, r.URL.Query().Get("assignee"))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[{"id":"t_abc","title":"测试任务","assignee":"alice","status":"todo","priority":0,"created_at":1716940800}]`))
	}))
	defer srv.Close()

	c, ep := newTestClient(srv)
	tasks, err := c.KanbanList(context.Background(), ep, "default", "", "")
	require.NoError(t, err)
	require.Len(t, tasks, 1)
	assert.Equal(t, "t_abc", tasks[0].ID)
}

// TestKanbanListStatusAssigneeQuery 验证 KanbanList 传入 status/assignee 时
// 这两个 query 参数正确附加。
func TestKanbanListStatusAssigneeQuery(t *testing.T) {
	// 过滤路径：board=default, status=todo, assignee=alice 三个 query 参数均正确
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 断言三个 query 参数均正确传入
		assert.Equal(t, "default", r.URL.Query().Get("board"))
		assert.Equal(t, "todo", r.URL.Query().Get("status"))
		assert.Equal(t, "alice", r.URL.Query().Get("assignee"))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()

	c, ep := newTestClient(srv)
	tasks, err := c.KanbanList(context.Background(), ep, "default", "todo", "alice")
	require.NoError(t, err)
	assert.Empty(t, tasks)
}

// TestKanbanShow 验证 KanbanShow 发出 GET /oc/kanban/tasks/{id}?board=
// 并将响应解码为 KanbanTaskDetail。
func TestKanbanShow(t *testing.T) {
	// 正常路径：board 走 query，id 走 path；响应解码到 KanbanTaskDetail.Task
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "/oc/kanban/tasks/t_abc123", r.URL.Path)
		// 断言 board 来自 query
		assert.Equal(t, "default", r.URL.Query().Get("board"))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"task": {"id":"t_abc123","title":"测试任务","assignee":"bob","status":"running","priority":1,"created_at":1716940800},
			"comments": [],
			"events": []
		}`))
	}))
	defer srv.Close()

	c, ep := newTestClient(srv)
	detail, err := c.KanbanShow(context.Background(), ep, "default", "t_abc123")
	require.NoError(t, err)
	// 断言任务核心字段解码正确
	assert.Equal(t, "t_abc123", detail.Task.ID)
	assert.Equal(t, "测试任务", detail.Task.Title)
	assert.Equal(t, "running", detail.Task.Status)
}

// TestKanbanShowPathEscape 验证含特殊字符的 task id 经 url.PathEscape 转义后正确发送。
func TestKanbanShowPathEscape(t *testing.T) {
	// 边界：id 含斜杠等特殊字符，PathEscape 后路径不越界
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// url.PathEscape("t/abc") = "t%2Fabc"
		assert.Equal(t, "/oc/kanban/tasks/t%2Fabc", r.URL.RawPath)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"task":{"id":"t/abc","title":"x","assignee":"a","status":"todo","priority":0,"created_at":0}}`))
	}))
	defer srv.Close()

	c, ep := newTestClient(srv)
	detail, err := c.KanbanShow(context.Background(), ep, "default", "t/abc")
	require.NoError(t, err)
	assert.Equal(t, "t/abc", detail.Task.ID)
}

// TestKanbanShowNotFound 验证任务不存在时 KanbanShow 返回 ErrNotFound。
func TestKanbanShowNotFound(t *testing.T) {
	// 异常路径：server 返回 404，应映射为 ErrNotFound
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"code":"NOT_FOUND","message":"任务不存在"}`))
	}))
	defer srv.Close()

	c, ep := newTestClient(srv)
	_, err := c.KanbanShow(context.Background(), ep, "default", "ghost")
	require.ErrorIs(t, err, ocops.ErrNotFound)
}

// TestKanbanRuns 验证 KanbanRuns 发出 GET /oc/kanban/tasks/{id}/runs?board=
// 并解码 []KanbanTaskRun。
func TestKanbanRuns(t *testing.T) {
	// 正常路径：返回 1 条执行记录，断言 path/query/解码正确
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "/oc/kanban/tasks/t_runs1/runs", r.URL.Path)
		assert.Equal(t, "default", r.URL.Query().Get("board"))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[{"profile":"alice","status":"done","started_at":1716940800,"outcome":"success"}]`))
	}))
	defer srv.Close()

	c, ep := newTestClient(srv)
	runs, err := c.KanbanRuns(context.Background(), ep, "default", "t_runs1")
	require.NoError(t, err)
	require.Len(t, runs, 1)
	assert.Equal(t, "alice", runs[0].Profile)
	assert.Equal(t, "success", runs[0].Outcome)
}

// TestKanbanStats 验证 KanbanStats 发出 GET /oc/kanban/stats?board=
// 并解码 KanbanStats。
func TestKanbanStats(t *testing.T) {
	// 正常路径：board query 参数正确，by_status 字段解码正确
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "/oc/kanban/stats", r.URL.Path)
		assert.Equal(t, "default", r.URL.Query().Get("board"))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"by_status":{"todo":3,"running":1,"done":5}}`))
	}))
	defer srv.Close()

	c, ep := newTestClient(srv)
	stats, err := c.KanbanStats(context.Background(), ep, "default")
	require.NoError(t, err)
	// 断言各状态计数正确
	assert.Equal(t, 3, stats.ByStatus["todo"])
	assert.Equal(t, 1, stats.ByStatus["running"])
	assert.Equal(t, 5, stats.ByStatus["done"])
}

// TestKanbanCreate 验证 KanbanCreate 发出 POST /oc/kanban/tasks，
// body 字段与 server 端白名单（board/title/assignee/priority/body/skills/workspace/parent/max_retries）对齐，
// 响应解码为 KanbanTaskDetail。
func TestKanbanCreate(t *testing.T) {
	// 正常路径：断言 POST body 包含 board/title/assignee，响应 TaskDetail 正确解码
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/oc/kanban/tasks", r.URL.Path)
		// Content-Type 必须是 application/json
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"task":{"id":"t_new","title":"新任务","assignee":"alice","status":"triage","priority":0,"created_at":1716940800}
		}`))
	}))
	defer srv.Close()

	c, ep := newTestClient(srv)
	req := ocops.KanbanCreateReq{
		Board:    "default",
		Title:    "新任务",
		Assignee: "alice",
		Body:     "详细描述",
	}
	detail, err := c.KanbanCreate(context.Background(), ep, req)
	require.NoError(t, err)
	assert.Equal(t, "t_new", detail.Task.ID)
	// 断言 body 字段名与 server 端白名单一致
	assert.Equal(t, "default", gotBody["board"])
	assert.Equal(t, "新任务", gotBody["title"])
	assert.Equal(t, "alice", gotBody["assignee"])
	assert.Equal(t, "详细描述", gotBody["body"])
}

// TestKanbanCreateWithSkills 验证 KanbanCreateReq.Skills 字段序列化为 JSON 数组。
func TestKanbanCreateWithSkills(t *testing.T) {
	// 正常路径：skills 字段为字符串数组，server 端应收到 JSON 数组
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"task":{"id":"t_sk","title":"x","assignee":"a","status":"triage","priority":0,"created_at":0}}`))
	}))
	defer srv.Close()

	c, ep := newTestClient(srv)
	req := ocops.KanbanCreateReq{
		Board:    "default",
		Title:    "技能任务",
		Assignee: "bob",
		Skills:   []string{"golang", "python"},
	}
	_, err := c.KanbanCreate(context.Background(), ep, req)
	require.NoError(t, err)
	// 断言 skills 字段为 JSON 数组，包含正确的元素
	skills, ok := gotBody["skills"].([]any)
	require.True(t, ok, "skills 应为 JSON 数组")
	assert.Equal(t, []any{"golang", "python"}, skills)
}

// TestKanbanComment 验证 KanbanComment 发出 POST /oc/kanban/tasks/{id}/comment，
// body 中包含正确的 board/body 字段（与 server 端 kanban_comment handler 对齐）。
func TestKanbanComment(t *testing.T) {
	// 正常路径：board 走 body.board，评论文本走 body.body；响应解码到 KanbanTaskDetail
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/oc/kanban/tasks/t_cmt1/comment", r.URL.Path)
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"task":{"id":"t_cmt1","title":"任务","assignee":"alice","status":"todo","priority":0,"created_at":0},"comments":[{"author":"alice","body":"好的","created_at":1716940800}]}`))
	}))
	defer srv.Close()

	c, ep := newTestClient(srv)
	detail, err := c.KanbanComment(context.Background(), ep, "default", "t_cmt1", "好的")
	require.NoError(t, err)
	// 断言任务 ID 解码正确
	assert.Equal(t, "t_cmt1", detail.Task.ID)
	// 断言 body 字段 board/body 名称与 server 端 kanban_comment 对齐
	assert.Equal(t, "default", gotBody["board"])
	assert.Equal(t, "好的", gotBody["body"])
}

// TestKanbanComplete 验证 KanbanComplete 发出 POST /oc/kanban/tasks/{id}/complete，
// body 包含 board/result 字段。
func TestKanbanComplete(t *testing.T) {
	// 正常路径：board/result 均走 JSON body，result 非空时应出现在 body
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/oc/kanban/tasks/t_done/complete", r.URL.Path)
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"task":{"id":"t_done","title":"任务","assignee":"alice","status":"done","priority":0,"created_at":0}}`))
	}))
	defer srv.Close()

	c, ep := newTestClient(srv)
	detail, err := c.KanbanComplete(context.Background(), ep, "default", "t_done", "任务已完成")
	require.NoError(t, err)
	assert.Equal(t, "t_done", detail.Task.ID)
	// 断言 board 和 result 字段名与 server 端 kanban_complete handler 对齐
	assert.Equal(t, "default", gotBody["board"])
	assert.Equal(t, "任务已完成", gotBody["result"])
}

// TestKanbanCompleteNoResult 验证 result 为空时不出现在请求体（omitempty）。
func TestKanbanCompleteNoResult(t *testing.T) {
	// 边界：result 空字符串时 omitempty 不序列化此字段，server 侧 body.get("result") or None 处理
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"task":{"id":"t_d2","title":"x","assignee":"a","status":"done","priority":0,"created_at":0}}`))
	}))
	defer srv.Close()

	c, ep := newTestClient(srv)
	_, err := c.KanbanComplete(context.Background(), ep, "default", "t_d2", "")
	require.NoError(t, err)
	// result 为空字符串时 omitempty 不序列化，body 中不应有 result 字段
	_, hasResult := gotBody["result"]
	assert.False(t, hasResult, "result 为空时不应出现在请求体")
}

// TestKanbanBlock 验证 KanbanBlock 发出 POST /oc/kanban/tasks/{id}/block，
// body 包含 board/reason 字段（与 server 端 kanban_block handler 对齐）。
func TestKanbanBlock(t *testing.T) {
	// 正常路径：board/reason 均走 JSON body；响应解码到 KanbanTaskDetail
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/oc/kanban/tasks/t_blk/block", r.URL.Path)
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"task":{"id":"t_blk","title":"任务","assignee":"alice","status":"blocked","priority":0,"created_at":0}}`))
	}))
	defer srv.Close()

	c, ep := newTestClient(srv)
	detail, err := c.KanbanBlock(context.Background(), ep, "default", "t_blk", "等待外部依赖")
	require.NoError(t, err)
	assert.Equal(t, "t_blk", detail.Task.ID)
	// 断言 board/reason 字段名与 server 端 kanban_block handler 一致
	assert.Equal(t, "default", gotBody["board"])
	assert.Equal(t, "等待外部依赖", gotBody["reason"])
}

// TestKanbanUnblock 验证 KanbanUnblock 发出 POST /oc/kanban/tasks/{id}/unblock，
// body 只包含 board 字段。
func TestKanbanUnblock(t *testing.T) {
	// 正常路径：只需 board 走 JSON body，无额外参数
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/oc/kanban/tasks/t_ublk/unblock", r.URL.Path)
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"task":{"id":"t_ublk","title":"任务","assignee":"alice","status":"ready","priority":0,"created_at":0}}`))
	}))
	defer srv.Close()

	c, ep := newTestClient(srv)
	detail, err := c.KanbanUnblock(context.Background(), ep, "default", "t_ublk")
	require.NoError(t, err)
	assert.Equal(t, "t_ublk", detail.Task.ID)
	// 断言 board 字段名与 server 端 kanban_unblock handler 一致
	assert.Equal(t, "default", gotBody["board"])
}

// TestKanbanArchive 验证 KanbanArchive 发出 POST /oc/kanban/tasks/{id}/archive，
// body 只包含 board 字段。
func TestKanbanArchive(t *testing.T) {
	// 正常路径：归档任务，body 仅含 board；响应包含 archived 状态
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/oc/kanban/tasks/t_arc/archive", r.URL.Path)
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"task":{"id":"t_arc","title":"归档任务","assignee":"alice","status":"archived","priority":0,"created_at":0}}`))
	}))
	defer srv.Close()

	c, ep := newTestClient(srv)
	detail, err := c.KanbanArchive(context.Background(), ep, "default", "t_arc")
	require.NoError(t, err)
	assert.Equal(t, "t_arc", detail.Task.ID)
	// 断言 board 字段名与 server 端 kanban_archive handler 一致
	assert.Equal(t, "default", gotBody["board"])
}

// TestKanbanReassign 验证 KanbanReassign 发出 POST /oc/kanban/tasks/{id}/reassign，
// body 包含 board/to 字段（与 server 端 kanban_reassign handler 对齐）。
func TestKanbanReassign(t *testing.T) {
	// 正常路径：board/to 均走 JSON body；to 是目标 assignee profile 名
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/oc/kanban/tasks/t_rea/reassign", r.URL.Path)
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"task":{"id":"t_rea","title":"任务","assignee":"bob","status":"todo","priority":0,"created_at":0}}`))
	}))
	defer srv.Close()

	c, ep := newTestClient(srv)
	detail, err := c.KanbanReassign(context.Background(), ep, "default", "t_rea", "bob")
	require.NoError(t, err)
	assert.Equal(t, "t_rea", detail.Task.ID)
	// 断言 board/to 字段名与 server 端 kanban_reassign handler 一致
	assert.Equal(t, "default", gotBody["board"])
	assert.Equal(t, "bob", gotBody["to"])
}

// TestKanbanReclaim 验证 KanbanReclaim 发出 POST /oc/kanban/tasks/{id}/reclaim，
// body 只包含 board 字段。
func TestKanbanReclaim(t *testing.T) {
	// 正常路径：撤销认领，body 仅含 board
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/oc/kanban/tasks/t_rcl/reclaim", r.URL.Path)
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"task":{"id":"t_rcl","title":"任务","assignee":"alice","status":"triage","priority":0,"created_at":0}}`))
	}))
	defer srv.Close()

	c, ep := newTestClient(srv)
	detail, err := c.KanbanReclaim(context.Background(), ep, "default", "t_rcl")
	require.NoError(t, err)
	assert.Equal(t, "t_rcl", detail.Task.ID)
	// 断言 board 字段名与 server 端 kanban_reclaim handler 一致
	assert.Equal(t, "default", gotBody["board"])
}

// TestKanbanListUnsupported 验证 KanbanList 在 stub 镜像（409）时返回 ErrUnsupported。
// 覆盖任意写/读 kanban 端点在 stub 镜像下的错误映射路径。
func TestKanbanListUnsupported(t *testing.T) {
	// 异常路径：stub 镜像返回 409 UNSUPPORTED，客户端映射为 ErrUnsupported
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"code":"UNSUPPORTED","message":"此镜像不含真实 hermes，kanban 操作不可用"}`))
	}))
	defer srv.Close()

	c, ep := newTestClient(srv)
	_, err := c.KanbanList(context.Background(), ep, "default", "", "")
	// 必须通过 errors.Is 识别为 ErrUnsupported
	require.ErrorIs(t, err, ocops.ErrUnsupported)
}
