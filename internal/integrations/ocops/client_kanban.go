// client_kanban.go — ocops 包 kanban 的 14 个非流式类型化客户端方法。
//
// 每个方法对应 oc-ops server 的一个 kanban 端点，内部统一调 c.DoJSON，
// 调用方无需关心 HTTP method / path / query string 拼装细节。
// path 参数（task id）用 url.PathEscape 转义，防止含特殊字符时路径越界。
// board 统一走 query 参数（server 侧从 query 读取）；写操作的业务字段走 JSON body。
package ocops

import (
	"context"
	"net/http"
	"net/url"
)

// KanbanCreateReq 是 POST /oc/kanban/tasks 的请求体。
// JSON 字段名与 server 端 kanban_create 白名单完全对齐：
//
//	board/title/assignee/priority/body/skills/workspace/parent/max_retries
//
// 未填写的可选字段以零值序列化时会被 omitempty 忽略，server 侧仅透传已出现的键。
type KanbanCreateReq struct {
	// Board 是目标 board slug（必填），如 "default"。
	Board string `json:"board"`
	// Title 是任务标题（必填）。
	Title string `json:"title"`
	// Assignee 是被分配的 hermes profile 名称（必填）。
	Assignee string `json:"assignee"`
	// Priority 是任务优先级（0-9），0 为默认。
	Priority int `json:"priority,omitempty"`
	// Body 是任务描述，可留空。
	Body string `json:"body,omitempty"`
	// Skills 是任务所需技能列表，可留空。
	Skills []string `json:"skills,omitempty"`
	// Workspace 是 workspace 标识，可留空。
	Workspace string `json:"workspace,omitempty"`
	// Parent 是父任务 ID，可留空。
	Parent string `json:"parent,omitempty"`
	// MaxRetries 是最大重试次数，0 表示不限。
	MaxRetries int `json:"max_retries,omitempty"`
}

// KanbanCapabilities 查询 kanban 能力信息：返回契约版本、支持的 verb 与 feature 开关。
// GET /oc/kanban/capabilities
func (c *Client) KanbanCapabilities(ctx context.Context, ep Endpoint) (KanbanCapabilities, error) {
	var out KanbanCapabilities
	err := c.DoJSON(ctx, ep, http.MethodGet, "/oc/kanban/capabilities", nil, &out)
	return out, err
}

// KanbanBoards 列出所有可用 board；stub 镜像（无真实 hermes）时返回 ErrUnsupported。
// GET /oc/kanban/boards
func (c *Client) KanbanBoards(ctx context.Context, ep Endpoint) ([]KanbanBoard, error) {
	var out []KanbanBoard
	err := c.DoJSON(ctx, ep, http.MethodGet, "/oc/kanban/boards", nil, &out)
	return out, err
}

// KanbanList 列出 board 任务列表；status/assignee 可选过滤，空字符串表示不过滤。
// GET /oc/kanban/tasks?board=&status=&assignee=
func (c *Client) KanbanList(ctx context.Context, ep Endpoint, board, status, assignee string) ([]KanbanTask, error) {
	// 构造 query 参数：board 必带，status/assignee 非空才加入
	q := url.Values{}
	q.Set("board", board)
	if status != "" {
		q.Set("status", status)
	}
	if assignee != "" {
		q.Set("assignee", assignee)
	}
	var out []KanbanTask
	err := c.DoJSON(ctx, ep, http.MethodGet, "/oc/kanban/tasks?"+q.Encode(), nil, &out)
	return out, err
}

// KanbanShow 查询单个任务详情；board 走 query，id 走 path；任务不存在时返回 ErrNotFound。
// GET /oc/kanban/tasks/{id}?board=
func (c *Client) KanbanShow(ctx context.Context, ep Endpoint, board, id string) (KanbanTaskDetail, error) {
	q := url.Values{}
	q.Set("board", board)
	var out KanbanTaskDetail
	err := c.DoJSON(ctx, ep, http.MethodGet,
		"/oc/kanban/tasks/"+url.PathEscape(id)+"?"+q.Encode(), nil, &out)
	return out, err
}

// KanbanRuns 查询任务历次执行记录；board 走 query，id 走 path。
// GET /oc/kanban/tasks/{id}/runs?board=
func (c *Client) KanbanRuns(ctx context.Context, ep Endpoint, board, id string) ([]KanbanTaskRun, error) {
	q := url.Values{}
	q.Set("board", board)
	var out []KanbanTaskRun
	err := c.DoJSON(ctx, ep, http.MethodGet,
		"/oc/kanban/tasks/"+url.PathEscape(id)+"/runs?"+q.Encode(), nil, &out)
	return out, err
}

// KanbanStats 查询 board 统计信息（各状态计数等）；board 走 query。
// GET /oc/kanban/stats?board=
func (c *Client) KanbanStats(ctx context.Context, ep Endpoint, board string) (KanbanStats, error) {
	q := url.Values{}
	q.Set("board", board)
	var out KanbanStats
	err := c.DoJSON(ctx, ep, http.MethodGet, "/oc/kanban/stats?"+q.Encode(), nil, &out)
	return out, err
}

// KanbanCreate 创建新任务，返回完整 TaskDetail（server 侧写后调 show 重读）。
// POST /oc/kanban/tasks，body 为 KanbanCreateReq。
func (c *Client) KanbanCreate(ctx context.Context, ep Endpoint, req KanbanCreateReq) (KanbanTaskDetail, error) {
	var out KanbanTaskDetail
	err := c.DoJSON(ctx, ep, http.MethodPost, "/oc/kanban/tasks", req, &out)
	return out, err
}

// KanbanComment 给任务添加评论，返回更新后的 TaskDetail。
// POST /oc/kanban/tasks/{id}/comment，body {"board":"...","body":"..."}
func (c *Client) KanbanComment(ctx context.Context, ep Endpoint, board, id, body string) (KanbanTaskDetail, error) {
	// 服务端从 body 读取 board（默认 default）和 body 文本（评论内容）
	reqBody := struct {
		Board string `json:"board"`
		Body  string `json:"body"`
	}{Board: board, Body: body}
	var out KanbanTaskDetail
	err := c.DoJSON(ctx, ep, http.MethodPost,
		"/oc/kanban/tasks/"+url.PathEscape(id)+"/comment", reqBody, &out)
	return out, err
}

// KanbanComplete 标记任务完成，返回更新后的 TaskDetail。
// POST /oc/kanban/tasks/{id}/complete，body {"board":"...","result":"..."}（result 可选）
func (c *Client) KanbanComplete(ctx context.Context, ep Endpoint, board, id, result string) (KanbanTaskDetail, error) {
	// server 端：board 来自 body.board，result 可为 None（空字符串时忽略）
	reqBody := struct {
		Board  string `json:"board"`
		Result string `json:"result,omitempty"`
	}{Board: board, Result: result}
	var out KanbanTaskDetail
	err := c.DoJSON(ctx, ep, http.MethodPost,
		"/oc/kanban/tasks/"+url.PathEscape(id)+"/complete", reqBody, &out)
	return out, err
}

// KanbanBlock 阻塞任务（设置 blocked 状态），返回更新后的 TaskDetail。
// POST /oc/kanban/tasks/{id}/block，body {"board":"...","reason":"..."}
func (c *Client) KanbanBlock(ctx context.Context, ep Endpoint, board, id, reason string) (KanbanTaskDetail, error) {
	// server 端：board/reason 均来自 JSON body
	reqBody := struct {
		Board  string `json:"board"`
		Reason string `json:"reason"`
	}{Board: board, Reason: reason}
	var out KanbanTaskDetail
	err := c.DoJSON(ctx, ep, http.MethodPost,
		"/oc/kanban/tasks/"+url.PathEscape(id)+"/block", reqBody, &out)
	return out, err
}

// KanbanUnblock 解除任务阻塞，返回更新后的 TaskDetail。
// POST /oc/kanban/tasks/{id}/unblock，body {"board":"..."}
func (c *Client) KanbanUnblock(ctx context.Context, ep Endpoint, board, id string) (KanbanTaskDetail, error) {
	// server 端：仅需 board 来自 JSON body
	reqBody := struct {
		Board string `json:"board"`
	}{Board: board}
	var out KanbanTaskDetail
	err := c.DoJSON(ctx, ep, http.MethodPost,
		"/oc/kanban/tasks/"+url.PathEscape(id)+"/unblock", reqBody, &out)
	return out, err
}

// KanbanArchive 归档任务，返回更新后的 TaskDetail。
// POST /oc/kanban/tasks/{id}/archive，body {"board":"..."}
func (c *Client) KanbanArchive(ctx context.Context, ep Endpoint, board, id string) (KanbanTaskDetail, error) {
	// server 端：board 来自 JSON body（默认 default）
	reqBody := struct {
		Board string `json:"board"`
	}{Board: board}
	var out KanbanTaskDetail
	err := c.DoJSON(ctx, ep, http.MethodPost,
		"/oc/kanban/tasks/"+url.PathEscape(id)+"/archive", reqBody, &out)
	return out, err
}

// KanbanReassign 将任务重新分配给指定 profile，返回更新后的 TaskDetail。
// POST /oc/kanban/tasks/{id}/reassign，body {"board":"...","to":"..."}
func (c *Client) KanbanReassign(ctx context.Context, ep Endpoint, board, id, to string) (KanbanTaskDetail, error) {
	// server 端：board/to 均来自 JSON body
	reqBody := struct {
		Board string `json:"board"`
		To    string `json:"to"`
	}{Board: board, To: to}
	var out KanbanTaskDetail
	err := c.DoJSON(ctx, ep, http.MethodPost,
		"/oc/kanban/tasks/"+url.PathEscape(id)+"/reassign", reqBody, &out)
	return out, err
}

// KanbanReclaim 撤销任务认领（重置分配），返回更新后的 TaskDetail。
// POST /oc/kanban/tasks/{id}/reclaim，body {"board":"..."}
func (c *Client) KanbanReclaim(ctx context.Context, ep Endpoint, board, id string) (KanbanTaskDetail, error) {
	// server 端：board 来自 JSON body（默认 default）
	reqBody := struct {
		Board string `json:"board"`
	}{Board: board}
	var out KanbanTaskDetail
	err := c.DoJSON(ctx, ep, http.MethodPost,
		"/oc/kanban/tasks/"+url.PathEscape(id)+"/reclaim", reqBody, &out)
	return out, err
}
