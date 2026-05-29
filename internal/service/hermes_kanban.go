// Package service —— hermes_kanban.go 实现 Hermes Kanban 任务看板能力。
// manager 不持有 kanban 数据，所有读写都通过 oc-ops HTTP 客户端调用 app 实例内
// 的 kanban 端点（类型化请求/响应），manager 仅做权限判断与输入校验。
package service

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"oc-manager/internal/auth"
	"oc-manager/internal/integrations/ocops"
)

// boardSlugRe 是 board slug 白名单正则（与 hermes-web-ui normalizeBoardSlug 一致）。
var boardSlugRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

// kanbanStatuses 是合法的任务状态枚举白名单。
var kanbanStatuses = map[string]bool{
	"triage": true, "todo": true, "ready": true, "running": true,
	"blocked": true, "done": true, "archived": true,
}

// kanbanWorkspaceRe 是合法的 workspace 参数校验正则。
// hermes kanban create --workspace 接受 scratch | worktree | dir:<path> 三种形式。
var kanbanWorkspaceRe = regexp.MustCompile(`^(scratch|worktree|dir:.+)$`)

// taskIDRe 是 kanban 任务 ID 白名单（hermes 形如 t_xxxxxxxx）。
var taskIDRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// skillNameRe 是 kanban skill 名称白名单正则（对应 hermes 内置 skill 文件命名约定）。
var skillNameRe = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// HermesKanbanService 暴露 Kanban 看板的读写能力。
type HermesKanbanService struct {
	ops      kanbanOps     // oc-ops 的类型化 kanban 客户端窄接口
	resolver OcOpsResolver // 把 appID 解析为 oc-ops 调用坐标
}

// NewHermesKanbanService 构造 service。
func NewHermesKanbanService(ops kanbanOps, resolver OcOpsResolver) *HermesKanbanService {
	return &HermesKanbanService{ops: ops, resolver: resolver}
}

// resolve 解析 appID、校验读权限，并确保实例可调用 oc-ops。
func (s *HermesKanbanService) resolve(ctx context.Context, principal auth.Principal, appID string) (OcOpsAppLocation, error) {
	loc, err := s.resolver.Resolve(ctx, appID)
	if err != nil {
		return OcOpsAppLocation{}, err
	}
	if !auth.CanViewAppKanban(principal, loc.OrgID, loc.OwnerUserID) {
		return OcOpsAppLocation{}, ErrKanbanForbidden
	}
	// dev stub 实例不含真实 hermes kanban 能力，按不支持处理。
	if !loc.Supported {
		return OcOpsAppLocation{}, ErrKanbanNotSupported
	}
	// 没有可用的 oc-ops 基址说明实例运行时尚未就绪。
	if strings.TrimSpace(loc.Endpoint.BaseURL) == "" {
		return OcOpsAppLocation{}, ErrKanbanRuntimeUnavailable
	}
	return loc, nil
}

// resolveManage 解析 appID 并做写权限校验（比 resolve 多一层 CanManageAppKanban）。
// 注：resolve 内部已含 CanViewAppKanban 读权限检查；此处 CanManageAppKanban 当前
// 与 CanViewAppKanban 等价（均委托 CanViewApp），存在冗余，但有意保留以便将来
// 读写权限分离演化时此处可独立收紧写权限，无需改动调用方。
func (s *HermesKanbanService) resolveManage(ctx context.Context, principal auth.Principal, appID string) (OcOpsAppLocation, error) {
	loc, err := s.resolve(ctx, principal, appID)
	if err != nil {
		return OcOpsAppLocation{}, err
	}
	if !auth.CanManageAppKanban(principal, loc.OrgID, loc.OwnerUserID) {
		return OcOpsAppLocation{}, ErrKanbanForbidden
	}
	return loc, nil
}

// validateBoard 校验 board slug，空值回退到 "default"。
func validateBoard(board string) (string, error) {
	board = strings.TrimSpace(board)
	if board == "" {
		return "default", nil
	}
	if !boardSlugRe.MatchString(board) {
		return "", fmt.Errorf("%w: 非法 board slug", ErrKanbanBadRequest)
	}
	return board, nil
}

// ListBoards 返回实例的所有 kanban board。
func (s *HermesKanbanService) ListBoards(ctx context.Context, principal auth.Principal, appID string) ([]ocops.KanbanBoard, error) {
	loc, err := s.resolve(ctx, principal, appID)
	if err != nil {
		return nil, err
	}
	boards, err := s.ops.KanbanBoards(ctx, loc.Endpoint)
	if err != nil {
		return nil, mapOcOpsKanbanErr(err)
	}
	return boards, nil
}

// KanbanTaskFilter 是 ListTasks 的过滤条件。
type KanbanTaskFilter struct {
	Board    string
	Status   string // 空表示不过滤
	Assignee string // 空表示不过滤
}

// ListTasks 返回某 board 的任务列表。
func (s *HermesKanbanService) ListTasks(ctx context.Context, principal auth.Principal, appID string, f KanbanTaskFilter) ([]ocops.KanbanTask, error) {
	loc, err := s.resolve(ctx, principal, appID)
	if err != nil {
		return nil, err
	}
	board, err := validateBoard(f.Board)
	if err != nil {
		return nil, err
	}
	// status 非空时必须命中状态白名单，避免把任意文本透传给上游。
	if f.Status != "" && !kanbanStatuses[f.Status] {
		return nil, fmt.Errorf("%w: 非法 status", ErrKanbanBadRequest)
	}
	// assignee 非空时须符合 board slug 规范（hermes profile 名称约定）。
	if f.Assignee != "" && !boardSlugRe.MatchString(f.Assignee) {
		return nil, fmt.Errorf("%w: 非法 assignee", ErrKanbanBadRequest)
	}
	tasks, err := s.ops.KanbanList(ctx, loc.Endpoint, board, f.Status, f.Assignee)
	if err != nil {
		return nil, mapOcOpsKanbanErr(err)
	}
	return tasks, nil
}

// ShowTask 返回单个任务的完整详情。
func (s *HermesKanbanService) ShowTask(ctx context.Context, principal auth.Principal, appID, board, taskID string) (ocops.KanbanTaskDetail, error) {
	loc, err := s.resolve(ctx, principal, appID)
	if err != nil {
		return ocops.KanbanTaskDetail{}, err
	}
	b, err := validateBoard(board)
	if err != nil {
		return ocops.KanbanTaskDetail{}, err
	}
	if !taskIDRe.MatchString(taskID) {
		return ocops.KanbanTaskDetail{}, fmt.Errorf("%w: 非法 task id", ErrKanbanBadRequest)
	}
	detail, err := s.ops.KanbanShow(ctx, loc.Endpoint, b, taskID)
	if err != nil {
		return ocops.KanbanTaskDetail{}, mapOcOpsKanbanErr(err)
	}
	return detail, nil
}

// TaskRuns 返回任务的历次执行记录。
func (s *HermesKanbanService) TaskRuns(ctx context.Context, principal auth.Principal, appID, board, taskID string) ([]ocops.KanbanTaskRun, error) {
	loc, err := s.resolve(ctx, principal, appID)
	if err != nil {
		return nil, err
	}
	b, err := validateBoard(board)
	if err != nil {
		return nil, err
	}
	if !taskIDRe.MatchString(taskID) {
		return nil, fmt.Errorf("%w: 非法 task id", ErrKanbanBadRequest)
	}
	runs, err := s.ops.KanbanRuns(ctx, loc.Endpoint, b, taskID)
	if err != nil {
		return nil, mapOcOpsKanbanErr(err)
	}
	return runs, nil
}

// Stats 返回某 board 的 per-status 统计。
func (s *HermesKanbanService) Stats(ctx context.Context, principal auth.Principal, appID, board string) (ocops.KanbanStats, error) {
	loc, err := s.resolve(ctx, principal, appID)
	if err != nil {
		return ocops.KanbanStats{}, err
	}
	b, err := validateBoard(board)
	if err != nil {
		return ocops.KanbanStats{}, err
	}
	stats, err := s.ops.KanbanStats(ctx, loc.Endpoint, b)
	if err != nil {
		return ocops.KanbanStats{}, mapOcOpsKanbanErr(err)
	}
	return stats, nil
}

// ————————————————————————————————————————————————————
// Task C3：写 verb
// ————————————————————————————————————————————————————

// CreateKanbanTaskInput 是新建任务的输入。
// 基础字段所有可写角色都能填；高级字段仅平台管理员可填，handler 层按角色 strip。
type CreateKanbanTaskInput struct {
	Board    string
	Title    string // 必填
	Body     string
	Assignee string // 必填
	Priority int
	// 以下为高级字段，权限模型：仅平台管理员（platform_admin）可填写；
	// 组织管理员（org_admin）和组织成员（org_member）提交的这些字段在 handler 层
	// 按 principal.Role 被 strip（丢弃），不会进入 service 层。
	// service 层信任已进入 CreateTask 的高级字段已经过 handler 层角色过滤：
	//   - Skills 各元素做字符白名单校验（^[A-Za-z0-9_-]+$）；
	//   - Workspace 对应 --workspace 参数，做 scratch|worktree|dir:<path> 正则校验；
	//   - ParentID 做 taskID 正则校验（见 taskIDRe）。
	Skills     []string // 对应 skills（可重复），每个元素单独校验
	Workspace  string   // 对应 workspace，值形如 scratch / worktree / dir:<path>
	ParentID   string
	MaxRetries int
}

// buildKanbanCreateReq 把 CreateKanbanTaskInput 校验并转成类型化的 ocops.KanbanCreateReq。
// 复用原拼 argv 的全部校验逻辑（标题非空 / assignee slug / priority 范围 / skill 白名单 /
// workspace 格式 / parent taskID），只是把「拼 argv」改为「填请求体」。
// 高级字段（Skills/Workspace/ParentID/MaxRetries）仅平台管理员可填，由 handler 层按
// principal 角色 strip；service 层信任进入此方法的高级字段已经过 handler 角色过滤。
func buildKanbanCreateReq(in CreateKanbanTaskInput) (ocops.KanbanCreateReq, error) {
	board, err := validateBoard(in.Board)
	if err != nil {
		return ocops.KanbanCreateReq{}, err
	}
	// 标题为必填项，空白字符串不允许。
	if strings.TrimSpace(in.Title) == "" {
		return ocops.KanbanCreateReq{}, fmt.Errorf("%w: 标题不能为空", ErrKanbanBadRequest)
	}
	// assignee 必须符合 board slug 规范（hermes 内部 profile 名称约定）。
	if !boardSlugRe.MatchString(in.Assignee) {
		return ocops.KanbanCreateReq{}, fmt.Errorf("%w: 非法 assignee", ErrKanbanBadRequest)
	}
	// priority 合法范围 0-9。
	if in.Priority < 0 || in.Priority > 9 {
		return ocops.KanbanCreateReq{}, fmt.Errorf("%w: priority 越界", ErrKanbanBadRequest)
	}
	req := ocops.KanbanCreateReq{
		Board:    board,
		Title:    in.Title,
		Assignee: in.Assignee,
		Priority: in.Priority,
		Body:     in.Body,
	}
	// Skills 每个元素单独做字符白名单校验后透传。
	for _, sk := range in.Skills {
		if !skillNameRe.MatchString(sk) {
			return ocops.KanbanCreateReq{}, fmt.Errorf("%w: 非法 skill 名称: %s", ErrKanbanBadRequest, sk)
		}
	}
	req.Skills = in.Skills
	// Workspace 接受 scratch / worktree / dir:<path> 形式。
	if in.Workspace != "" {
		if !kanbanWorkspaceRe.MatchString(in.Workspace) {
			return ocops.KanbanCreateReq{}, fmt.Errorf("%w: 非法 workspace 值", ErrKanbanBadRequest)
		}
		req.Workspace = in.Workspace
	}
	if in.ParentID != "" {
		if !taskIDRe.MatchString(in.ParentID) {
			return ocops.KanbanCreateReq{}, fmt.Errorf("%w: 非法 parent id", ErrKanbanBadRequest)
		}
		req.Parent = in.ParentID
	}
	if in.MaxRetries > 0 {
		req.MaxRetries = in.MaxRetries
	}
	return req, nil
}

// CreateTask 创建一个新任务，返回新任务详情。
func (s *HermesKanbanService) CreateTask(ctx context.Context, principal auth.Principal, appID string, in CreateKanbanTaskInput) (ocops.KanbanTaskDetail, error) {
	loc, err := s.resolveManage(ctx, principal, appID)
	if err != nil {
		return ocops.KanbanTaskDetail{}, err
	}
	req, err := buildKanbanCreateReq(in)
	if err != nil {
		return ocops.KanbanTaskDetail{}, err
	}
	detail, err := s.ops.KanbanCreate(ctx, loc.Endpoint, req)
	if err != nil {
		return ocops.KanbanTaskDetail{}, mapOcOpsKanbanErr(err)
	}
	return detail, nil
}

// Comment 给任务追加一条评论。body 为自由文本，由 oc-ops 透传给上游。
func (s *HermesKanbanService) Comment(ctx context.Context, principal auth.Principal, appID, board, taskID, body string) (ocops.KanbanTaskDetail, error) {
	loc, err := s.resolveManage(ctx, principal, appID)
	if err != nil {
		return ocops.KanbanTaskDetail{}, err
	}
	b, err := validateBoard(board)
	if err != nil {
		return ocops.KanbanTaskDetail{}, err
	}
	if !taskIDRe.MatchString(taskID) {
		return ocops.KanbanTaskDetail{}, fmt.Errorf("%w: 非法 task id", ErrKanbanBadRequest)
	}
	// 评论内容不能为空。
	if strings.TrimSpace(body) == "" {
		return ocops.KanbanTaskDetail{}, fmt.Errorf("%w: 评论内容不能为空", ErrKanbanBadRequest)
	}
	detail, err := s.ops.KanbanComment(ctx, loc.Endpoint, b, taskID, body)
	if err != nil {
		return ocops.KanbanTaskDetail{}, mapOcOpsKanbanErr(err)
	}
	return detail, nil
}

// Complete 把任务标记为已完成。result 可选；不为空时传递执行摘要。
func (s *HermesKanbanService) Complete(ctx context.Context, principal auth.Principal, appID, board, taskID, result string) (ocops.KanbanTaskDetail, error) {
	loc, err := s.resolveManage(ctx, principal, appID)
	if err != nil {
		return ocops.KanbanTaskDetail{}, err
	}
	b, err := validateBoard(board)
	if err != nil {
		return ocops.KanbanTaskDetail{}, err
	}
	if !taskIDRe.MatchString(taskID) {
		return ocops.KanbanTaskDetail{}, fmt.Errorf("%w: 非法 task id", ErrKanbanBadRequest)
	}
	detail, err := s.ops.KanbanComplete(ctx, loc.Endpoint, b, taskID, result)
	if err != nil {
		return ocops.KanbanTaskDetail{}, mapOcOpsKanbanErr(err)
	}
	return detail, nil
}

// Block 把任务标记为阻塞，reason 说明阻塞原因，不能为空。
func (s *HermesKanbanService) Block(ctx context.Context, principal auth.Principal, appID, board, taskID, reason string) (ocops.KanbanTaskDetail, error) {
	loc, err := s.resolveManage(ctx, principal, appID)
	if err != nil {
		return ocops.KanbanTaskDetail{}, err
	}
	b, err := validateBoard(board)
	if err != nil {
		return ocops.KanbanTaskDetail{}, err
	}
	if !taskIDRe.MatchString(taskID) {
		return ocops.KanbanTaskDetail{}, fmt.Errorf("%w: 非法 task id", ErrKanbanBadRequest)
	}
	// 阻塞原因不能为空（CLI 要求必填）。
	if strings.TrimSpace(reason) == "" {
		return ocops.KanbanTaskDetail{}, fmt.Errorf("%w: 阻塞原因不能为空", ErrKanbanBadRequest)
	}
	detail, err := s.ops.KanbanBlock(ctx, loc.Endpoint, b, taskID, reason)
	if err != nil {
		return ocops.KanbanTaskDetail{}, mapOcOpsKanbanErr(err)
	}
	return detail, nil
}

// Unblock 解除任务的阻塞状态。
func (s *HermesKanbanService) Unblock(ctx context.Context, principal auth.Principal, appID, board, taskID string) (ocops.KanbanTaskDetail, error) {
	loc, err := s.resolveManage(ctx, principal, appID)
	if err != nil {
		return ocops.KanbanTaskDetail{}, err
	}
	b, err := validateBoard(board)
	if err != nil {
		return ocops.KanbanTaskDetail{}, err
	}
	if !taskIDRe.MatchString(taskID) {
		return ocops.KanbanTaskDetail{}, fmt.Errorf("%w: 非法 task id", ErrKanbanBadRequest)
	}
	detail, err := s.ops.KanbanUnblock(ctx, loc.Endpoint, b, taskID)
	if err != nil {
		return ocops.KanbanTaskDetail{}, mapOcOpsKanbanErr(err)
	}
	return detail, nil
}

// Archive 归档任务。
func (s *HermesKanbanService) Archive(ctx context.Context, principal auth.Principal, appID, board, taskID string) (ocops.KanbanTaskDetail, error) {
	loc, err := s.resolveManage(ctx, principal, appID)
	if err != nil {
		return ocops.KanbanTaskDetail{}, err
	}
	b, err := validateBoard(board)
	if err != nil {
		return ocops.KanbanTaskDetail{}, err
	}
	if !taskIDRe.MatchString(taskID) {
		return ocops.KanbanTaskDetail{}, fmt.Errorf("%w: 非法 task id", ErrKanbanBadRequest)
	}
	detail, err := s.ops.KanbanArchive(ctx, loc.Endpoint, b, taskID)
	if err != nil {
		return ocops.KanbanTaskDetail{}, mapOcOpsKanbanErr(err)
	}
	return detail, nil
}

// Reassign 把任务重新分配给指定 profile。profile 必须符合 board slug 规范。
func (s *HermesKanbanService) Reassign(ctx context.Context, principal auth.Principal, appID, board, taskID, profile string) (ocops.KanbanTaskDetail, error) {
	loc, err := s.resolveManage(ctx, principal, appID)
	if err != nil {
		return ocops.KanbanTaskDetail{}, err
	}
	b, err := validateBoard(board)
	if err != nil {
		return ocops.KanbanTaskDetail{}, err
	}
	if !taskIDRe.MatchString(taskID) {
		return ocops.KanbanTaskDetail{}, fmt.Errorf("%w: 非法 task id", ErrKanbanBadRequest)
	}
	// profile 用于标识 hermes worker 配置，格式与 board slug 一致。
	if !boardSlugRe.MatchString(profile) {
		return ocops.KanbanTaskDetail{}, fmt.Errorf("%w: 非法 profile", ErrKanbanBadRequest)
	}
	detail, err := s.ops.KanbanReassign(ctx, loc.Endpoint, b, taskID, profile)
	if err != nil {
		return ocops.KanbanTaskDetail{}, mapOcOpsKanbanErr(err)
	}
	return detail, nil
}

// Reclaim 把任务重置为等待认领状态（撤销当前 assignee）。
func (s *HermesKanbanService) Reclaim(ctx context.Context, principal auth.Principal, appID, board, taskID string) (ocops.KanbanTaskDetail, error) {
	loc, err := s.resolveManage(ctx, principal, appID)
	if err != nil {
		return ocops.KanbanTaskDetail{}, err
	}
	b, err := validateBoard(board)
	if err != nil {
		return ocops.KanbanTaskDetail{}, err
	}
	if !taskIDRe.MatchString(taskID) {
		return ocops.KanbanTaskDetail{}, fmt.Errorf("%w: 非法 task id", ErrKanbanBadRequest)
	}
	detail, err := s.ops.KanbanReclaim(ctx, loc.Endpoint, b, taskID)
	if err != nil {
		return ocops.KanbanTaskDetail{}, mapOcOpsKanbanErr(err)
	}
	return detail, nil
}

// Capabilities 探测实例 oc-kanban 的契约版本与可用能力。
// 仅需读权限，故用 resolve（与读 verb 一致）。stub 实例由 resolve
// 拦截返回 ErrKanbanNotSupported，前端按既有 stub 降级处理。
func (s *HermesKanbanService) Capabilities(ctx context.Context, principal auth.Principal, appID string) (ocops.KanbanCapabilities, error) {
	loc, err := s.resolve(ctx, principal, appID)
	if err != nil {
		return ocops.KanbanCapabilities{}, err
	}
	caps, err := s.ops.KanbanCapabilities(ctx, loc.Endpoint)
	if err != nil {
		return ocops.KanbanCapabilities{}, mapOcOpsKanbanErr(err)
	}
	return caps, nil
}

// ————————————————————————————————————————————————————
// Task C4：实时事件流
// ————————————————————————————————————————————————————

// WatchEvents 订阅某 board 的实时事件流。先 resolve + 鉴权 + 校验 board，再返回
// oc-ops 的 KanbanEvent 事件 channel，由调用方（handler）逐条转成 SSE 写出。
// board watch 覆盖整个看板所有任务事件。该方法是只读监听，仅需读权限
// （CanViewAppKanban），故用 resolve 而非 resolveManage——watch 不产生任何写操作。
func (s *HermesKanbanService) WatchEvents(ctx context.Context, principal auth.Principal, appID, board string) (<-chan ocops.KanbanEvent, error) {
	loc, err := s.resolve(ctx, principal, appID)
	if err != nil {
		return nil, err
	}
	b, err := validateBoard(board)
	if err != nil {
		return nil, err
	}
	ch, err := s.ops.WatchKanban(ctx, loc.Endpoint, b)
	if err != nil {
		return nil, mapOcOpsKanbanErr(err)
	}
	return ch, nil
}
