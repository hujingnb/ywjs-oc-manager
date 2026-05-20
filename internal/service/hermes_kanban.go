// Package service —— hermes_kanban.go 实现 Hermes Kanban 任务看板能力。
// manager 不持有 kanban 数据，全部通过在 hermes 容器内执行 `hermes kanban`
// CLI 并解析 --json 输出获得；写操作同样走 CLI verb。
package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"oc-manager/internal/auth"
	"oc-manager/internal/integrations/runtime"
	"oc-manager/internal/store/sqlc"
)

// kanbanExecer 抽象在容器内执行命令的能力，便于单测注入假实现。
type kanbanExecer interface {
	ContainerExecJSON(ctx context.Context, nodeID, containerID string, cmd []string) (runtime.ExecJSONResult, error)
	ContainerExecStream(ctx context.Context, nodeID, containerID string, cmd []string) (runtime.ExecStreamHandle, error)
}

// kanbanAppLocator 把 appID 解析为执行 kanban CLI 所需的运行时坐标。
type kanbanAppLocator interface {
	// LocateApp 返回 app 的归属信息与运行时坐标。
	// stub 表示该 app 运行的是 dev stub 镜像；containerID 为空表示容器未运行。
	LocateApp(ctx context.Context, appID string) (KanbanAppLocation, error)
}

// KanbanAppLocation 是执行 kanban CLI 所需的全部 app 运行时信息。
type KanbanAppLocation struct {
	OrgID       string // app 归属组织，用于权限判断
	OwnerUserID string // app 拥有者，用于 org_member 权限判断
	NodeID      string // app 所在 runtime node
	ContainerID string // hermes 容器 ID，空表示未运行
	Stub        bool   // 是否 dev stub 镜像
}

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

// HermesKanbanService 暴露 Kanban 看板的读写能力。
type HermesKanbanService struct {
	execer  kanbanExecer
	locator kanbanAppLocator
}

// NewHermesKanbanService 构造 service。
func NewHermesKanbanService(execer kanbanExecer, locator kanbanAppLocator) *HermesKanbanService {
	return &HermesKanbanService{execer: execer, locator: locator}
}

// resolve 解析 appID 并做读权限校验，返回执行坐标。
func (s *HermesKanbanService) resolve(ctx context.Context, principal auth.Principal, appID string) (KanbanAppLocation, error) {
	loc, err := s.locator.LocateApp(ctx, appID)
	if err != nil {
		return KanbanAppLocation{}, err
	}
	if !auth.CanViewAppKanban(principal, loc.OrgID, loc.OwnerUserID) {
		return KanbanAppLocation{}, ErrKanbanForbidden
	}
	if loc.Stub {
		return KanbanAppLocation{}, ErrKanbanNotSupported
	}
	if strings.TrimSpace(loc.ContainerID) == "" {
		return KanbanAppLocation{}, ErrKanbanRuntimeUnavailable
	}
	return loc, nil
}

// runCLI 在 hermes 容器内执行一条 kanban 命令并返回 stdout。
// board 是 `hermes kanban` 的全局参数，放在 verb 之前；board 为空则不加（如 boards list）。
// verbArgs 是 verb 及其后的参数，不含 "hermes kanban [--board ...]" 前缀。
func (s *HermesKanbanService) runCLI(ctx context.Context, loc KanbanAppLocation, board string, verbArgs []string) ([]byte, error) {
	cmd := []string{"hermes", "kanban"}
	if board != "" {
		cmd = append(cmd, "--board", board)
	}
	cmd = append(cmd, verbArgs...)
	res, err := s.execer.ContainerExecJSON(ctx, loc.NodeID, loc.ContainerID, cmd)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrKanbanCLI, err)
	}
	if res.ExitCode != 0 {
		msg := strings.TrimSpace(res.Stderr)
		// 按字符（rune）截断，避免在多字节中文字符中间切断产生非法 UTF-8。
		if runes := []rune(msg); len(runes) > 1024 {
			msg = string(runes[:1024])
		}
		return nil, fmt.Errorf("%w: exit %d: %s", ErrKanbanCLI, res.ExitCode, msg)
	}
	return []byte(res.Stdout), nil
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
func (s *HermesKanbanService) ListBoards(ctx context.Context, principal auth.Principal, appID string) ([]KanbanBoard, error) {
	loc, err := s.resolve(ctx, principal, appID)
	if err != nil {
		return nil, err
	}
	// boards 子命令跨 board，不加 --board 全局参数。
	out, err := s.runCLI(ctx, loc, "", []string{"boards", "list", "--all", "--json"})
	if err != nil {
		return nil, err
	}
	var boards []KanbanBoard
	if err := json.Unmarshal(out, &boards); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrKanbanOutputInvalid, err)
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
func (s *HermesKanbanService) ListTasks(ctx context.Context, principal auth.Principal, appID string, f KanbanTaskFilter) ([]KanbanTask, error) {
	loc, err := s.resolve(ctx, principal, appID)
	if err != nil {
		return nil, err
	}
	board, err := validateBoard(f.Board)
	if err != nil {
		return nil, err
	}
	// list 本身无 --board 参数，board 通过 runCLI 作为全局参数注入 `hermes kanban --board <slug>` 位置。
	args := []string{"list", "--json"}
	if f.Status != "" {
		if !kanbanStatuses[f.Status] {
			return nil, fmt.Errorf("%w: 非法 status", ErrKanbanBadRequest)
		}
		args = append(args, "--status", f.Status)
	}
	if f.Assignee != "" {
		if !boardSlugRe.MatchString(f.Assignee) {
			return nil, fmt.Errorf("%w: 非法 assignee", ErrKanbanBadRequest)
		}
		args = append(args, "--assignee", f.Assignee)
	}
	out, err := s.runCLI(ctx, loc, board, args)
	if err != nil {
		return nil, err
	}
	var tasks []KanbanTask
	if err := json.Unmarshal(out, &tasks); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrKanbanOutputInvalid, err)
	}
	return tasks, nil
}

// taskIDRe 是 kanban 任务 ID 白名单（hermes 形如 t_xxxxxxxx）。
var taskIDRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// skillNameRe 是 kanban skill 名称白名单正则（对应 hermes 内置 skill 文件命名约定）。
var skillNameRe = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// ShowTask 返回单个任务的完整详情。
func (s *HermesKanbanService) ShowTask(ctx context.Context, principal auth.Principal, appID, board, taskID string) (KanbanTaskDetail, error) {
	loc, err := s.resolve(ctx, principal, appID)
	if err != nil {
		return KanbanTaskDetail{}, err
	}
	b, err := validateBoard(board)
	if err != nil {
		return KanbanTaskDetail{}, err
	}
	if !taskIDRe.MatchString(taskID) {
		return KanbanTaskDetail{}, fmt.Errorf("%w: 非法 task id", ErrKanbanBadRequest)
	}
	out, err := s.runCLI(ctx, loc, b, []string{"show", taskID, "--json"})
	if err != nil {
		return KanbanTaskDetail{}, err
	}
	var detail KanbanTaskDetail
	if err := json.Unmarshal(out, &detail); err != nil {
		return KanbanTaskDetail{}, fmt.Errorf("%w: %v", ErrKanbanOutputInvalid, err)
	}
	return detail, nil
}

// TaskRuns 返回任务的历次执行记录。
func (s *HermesKanbanService) TaskRuns(ctx context.Context, principal auth.Principal, appID, board, taskID string) ([]KanbanTaskRun, error) {
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
	out, err := s.runCLI(ctx, loc, b, []string{"runs", taskID, "--json"})
	if err != nil {
		return nil, err
	}
	var runs []KanbanTaskRun
	if err := json.Unmarshal(out, &runs); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrKanbanOutputInvalid, err)
	}
	return runs, nil
}

// Stats 返回某 board 的 per-status 统计。
func (s *HermesKanbanService) Stats(ctx context.Context, principal auth.Principal, appID, board string) (KanbanStats, error) {
	loc, err := s.resolve(ctx, principal, appID)
	if err != nil {
		return KanbanStats{}, err
	}
	b, err := validateBoard(board)
	if err != nil {
		return KanbanStats{}, err
	}
	out, err := s.runCLI(ctx, loc, b, []string{"stats", "--json"})
	if err != nil {
		return KanbanStats{}, err
	}
	var stats KanbanStats
	if err := json.Unmarshal(out, &stats); err != nil {
		return KanbanStats{}, fmt.Errorf("%w: %v", ErrKanbanOutputInvalid, err)
	}
	return stats, nil
}

// ————————————————————————————————————————————————————
// Task C3：写 verb
// ————————————————————————————————————————————————————

// resolveManage 解析 appID 并做写权限校验（比 resolve 多一层 CanManageAppKanban）。
// 注：resolve 内部已含 CanViewAppKanban 读权限检查；此处 CanManageAppKanban 当前
// 与 CanViewAppKanban 等价（均委托 CanViewApp），存在冗余，但有意保留以便将来
// 读写权限分离演化时此处可独立收紧写权限，无需改动调用方。
func (s *HermesKanbanService) resolveManage(ctx context.Context, principal auth.Principal, appID string) (KanbanAppLocation, error) {
	loc, err := s.resolve(ctx, principal, appID)
	if err != nil {
		return KanbanAppLocation{}, err
	}
	if !auth.CanManageAppKanban(principal, loc.OrgID, loc.OwnerUserID) {
		return KanbanAppLocation{}, ErrKanbanForbidden
	}
	return loc, nil
}

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
	Skills     []string // 对应 --skill（可重复），每个元素单独校验
	Workspace  string   // 对应 --workspace，值形如 scratch / worktree / dir:<path>
	ParentID   string
	MaxRetries int
}

// CreateTask 创建一个新任务，返回新任务详情。
// title / body 等自由文本作为独立 argv 元素传入，不拼 shell，杜绝注入。
// 高级字段（Skills/Workspace/ParentID/MaxRetries）仅平台管理员可填，
// 由 handler 层按 principal 角色 strip；service 层信任进入此方法的高级字段已经过
// handler 角色过滤，故 Skills 每个元素做字符白名单校验（skillNameRe），
// Workspace 做格式校验（kanbanWorkspaceRe），ParentID 做 taskID 正则校验。
func (s *HermesKanbanService) CreateTask(ctx context.Context, principal auth.Principal, appID string, in CreateKanbanTaskInput) (KanbanTaskDetail, error) {
	loc, err := s.resolveManage(ctx, principal, appID)
	if err != nil {
		return KanbanTaskDetail{}, err
	}
	board, err := validateBoard(in.Board)
	if err != nil {
		return KanbanTaskDetail{}, err
	}
	// 标题为必填项，空白字符串不允许。
	if strings.TrimSpace(in.Title) == "" {
		return KanbanTaskDetail{}, fmt.Errorf("%w: 标题不能为空", ErrKanbanBadRequest)
	}
	// assignee 必须符合 board slug 规范（hermes 内部 profile 名称约定）。
	if !boardSlugRe.MatchString(in.Assignee) {
		return KanbanTaskDetail{}, fmt.Errorf("%w: 非法 assignee", ErrKanbanBadRequest)
	}
	// priority 合法范围 0-9。
	if in.Priority < 0 || in.Priority > 9 {
		return KanbanTaskDetail{}, fmt.Errorf("%w: priority 越界", ErrKanbanBadRequest)
	}
	// board 通过 runCLI 作为全局参数注入，verb args 不含 --board。
	args := []string{"create", in.Title, "--assignee", in.Assignee,
		"--priority", fmt.Sprintf("%d", in.Priority), "--json"}
	if in.Body != "" {
		args = append(args, "--body", in.Body)
	}
	// Skills 对应 --skill（可重复），每个 skill 单独校验后追加一个 --skill <name>。
	for _, sk := range in.Skills {
		if !skillNameRe.MatchString(sk) {
			return KanbanTaskDetail{}, fmt.Errorf("%w: 非法 skill 名称: %s", ErrKanbanBadRequest, sk)
		}
		args = append(args, "--skill", sk)
	}
	// Workspace 对应单个 --workspace 参数，接受 scratch / worktree / dir:<path> 形式。
	if in.Workspace != "" {
		if !kanbanWorkspaceRe.MatchString(in.Workspace) {
			return KanbanTaskDetail{}, fmt.Errorf("%w: 非法 workspace 值", ErrKanbanBadRequest)
		}
		args = append(args, "--workspace", in.Workspace)
	}
	if in.ParentID != "" {
		if !taskIDRe.MatchString(in.ParentID) {
			return KanbanTaskDetail{}, fmt.Errorf("%w: 非法 parent id", ErrKanbanBadRequest)
		}
		args = append(args, "--parent", in.ParentID)
	}
	if in.MaxRetries > 0 {
		args = append(args, "--max-retries", fmt.Sprintf("%d", in.MaxRetries))
	}
	out, err := s.runCLI(ctx, loc, board, args)
	if err != nil {
		return KanbanTaskDetail{}, err
	}
	// create --json 输出的是扁平任务对象（与 list 元素格式相同），
	// 而非 show 输出的 {task:{...}} 包装结构，需先解到 KanbanTask 再包装。
	var task KanbanTask
	if err := json.Unmarshal(out, &task); err != nil {
		return KanbanTaskDetail{}, fmt.Errorf("%w: %v", ErrKanbanOutputInvalid, err)
	}
	return KanbanTaskDetail{Task: task}, nil
}

// Comment 给任务追加一条评论。body 为自由文本，作为独立 argv 传入，不拼 shell。
func (s *HermesKanbanService) Comment(ctx context.Context, principal auth.Principal, appID, board, taskID, body string) error {
	loc, err := s.resolveManage(ctx, principal, appID)
	if err != nil {
		return err
	}
	b, err := validateBoard(board)
	if err != nil {
		return err
	}
	if !taskIDRe.MatchString(taskID) {
		return fmt.Errorf("%w: 非法 task id", ErrKanbanBadRequest)
	}
	// 评论内容不能为空。
	if strings.TrimSpace(body) == "" {
		return fmt.Errorf("%w: 评论内容不能为空", ErrKanbanBadRequest)
	}
	// body 作为 positional text 参数，board 通过 runCLI 全局参数注入。
	_, err = s.runCLI(ctx, loc, b, []string{"comment", taskID, body})
	return err
}

// Complete 把任务标记为已完成。result 可选；不为空时附加 --result 传递执行摘要。
func (s *HermesKanbanService) Complete(ctx context.Context, principal auth.Principal, appID, board, taskID, result string) error {
	loc, err := s.resolveManage(ctx, principal, appID)
	if err != nil {
		return err
	}
	b, err := validateBoard(board)
	if err != nil {
		return err
	}
	if !taskIDRe.MatchString(taskID) {
		return fmt.Errorf("%w: 非法 task id", ErrKanbanBadRequest)
	}
	args := []string{"complete", taskID}
	// result 为可选自由文本，非空时作为独立 argv 附加；board 通过 runCLI 全局参数注入。
	if result != "" {
		args = append(args, "--result", result)
	}
	_, err = s.runCLI(ctx, loc, b, args)
	return err
}

// Block 把任务标记为阻塞，reason 说明阻塞原因，不能为空。
func (s *HermesKanbanService) Block(ctx context.Context, principal auth.Principal, appID, board, taskID, reason string) error {
	loc, err := s.resolveManage(ctx, principal, appID)
	if err != nil {
		return err
	}
	b, err := validateBoard(board)
	if err != nil {
		return err
	}
	if !taskIDRe.MatchString(taskID) {
		return fmt.Errorf("%w: 非法 task id", ErrKanbanBadRequest)
	}
	// 阻塞原因不能为空（CLI 要求必填）。
	if strings.TrimSpace(reason) == "" {
		return fmt.Errorf("%w: 阻塞原因不能为空", ErrKanbanBadRequest)
	}
	// reason 作为 positional 参数，board 通过 runCLI 全局参数注入。
	_, err = s.runCLI(ctx, loc, b, []string{"block", taskID, reason})
	return err
}

// Unblock 解除任务的阻塞状态。
func (s *HermesKanbanService) Unblock(ctx context.Context, principal auth.Principal, appID, board, taskID string) error {
	loc, err := s.resolveManage(ctx, principal, appID)
	if err != nil {
		return err
	}
	b, err := validateBoard(board)
	if err != nil {
		return err
	}
	if !taskIDRe.MatchString(taskID) {
		return fmt.Errorf("%w: 非法 task id", ErrKanbanBadRequest)
	}
	_, err = s.runCLI(ctx, loc, b, []string{"unblock", taskID})
	return err
}

// Archive 归档任务。
func (s *HermesKanbanService) Archive(ctx context.Context, principal auth.Principal, appID, board, taskID string) error {
	loc, err := s.resolveManage(ctx, principal, appID)
	if err != nil {
		return err
	}
	b, err := validateBoard(board)
	if err != nil {
		return err
	}
	if !taskIDRe.MatchString(taskID) {
		return fmt.Errorf("%w: 非法 task id", ErrKanbanBadRequest)
	}
	_, err = s.runCLI(ctx, loc, b, []string{"archive", taskID})
	return err
}

// Reassign 把任务重新分配给指定 profile。profile 必须符合 board slug 规范。
func (s *HermesKanbanService) Reassign(ctx context.Context, principal auth.Principal, appID, board, taskID, profile string) error {
	loc, err := s.resolveManage(ctx, principal, appID)
	if err != nil {
		return err
	}
	b, err := validateBoard(board)
	if err != nil {
		return err
	}
	if !taskIDRe.MatchString(taskID) {
		return fmt.Errorf("%w: 非法 task id", ErrKanbanBadRequest)
	}
	// profile 用于标识 hermes worker 配置，格式与 board slug 一致。
	if !boardSlugRe.MatchString(profile) {
		return fmt.Errorf("%w: 非法 profile", ErrKanbanBadRequest)
	}
	// profile 是 positional 参数（不是 --to），board 通过 runCLI 全局参数注入。
	_, err = s.runCLI(ctx, loc, b, []string{"reassign", taskID, profile})
	return err
}

// Reclaim 把任务重置为等待认领状态（撤销当前 assignee）。
func (s *HermesKanbanService) Reclaim(ctx context.Context, principal auth.Principal, appID, board, taskID string) error {
	loc, err := s.resolveManage(ctx, principal, appID)
	if err != nil {
		return err
	}
	b, err := validateBoard(board)
	if err != nil {
		return err
	}
	if !taskIDRe.MatchString(taskID) {
		return fmt.Errorf("%w: 非法 task id", ErrKanbanBadRequest)
	}
	_, err = s.runCLI(ctx, loc, b, []string{"reclaim", taskID})
	return err
}

// ————————————————————————————————————————————————————
// Task C4：实时事件流
// ————————————————————————————————————————————————————

// StreamEvents 在 hermes 容器内执行 `kanban watch` 并把每行 NDJSON 投递到回调。
// 该方法阻塞直到 ctx 取消、流结束或出错。board watch 覆盖整个看板所有任务事件。
// 该方法是只读监听，仅需读权限（CanViewAppKanban），故用 resolve 而非 resolveManage，
// 并非遗漏——watch 不产生任何写操作，不需要写权限。
func (s *HermesKanbanService) StreamEvents(ctx context.Context, principal auth.Principal, appID, board string, onLine func(line string)) error {
	loc, err := s.resolve(ctx, principal, appID)
	if err != nil {
		return err
	}
	b, err := validateBoard(board)
	if err != nil {
		return err
	}
	// watch 子命令不支持 --json 也不支持自身的 --board；
	// board 通过全局参数注入到 `hermes kanban --board <slug>` 位置。
	cmd := []string{"hermes", "kanban", "--board", b, "watch"}
	handle, err := s.execer.ContainerExecStream(ctx, loc.NodeID, loc.ContainerID, cmd)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrKanbanCLI, err)
	}
	defer handle.Close()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case line, ok := <-handle.Lines:
			if !ok {
				// channel 关闭表示流结束，检查是否有底层错误。
				if e := handle.Err(); e != nil {
					return fmt.Errorf("%w: %v", ErrKanbanCLI, e)
				}
				return nil
			}
			onLine(line)
		}
	}
}

// ————————————————————————————————————————————————————
// Task D5：KanbanAppLocatorFromStore —— 基于 app store 解析运行时坐标
// ————————————————————————————————————————————————————

// kanbanAppStore 是 KanbanAppLocatorFromStore 依赖的最小 app 查询能力。
// 只声明 GetApp，避免依赖整个 Querier 接口，便于单测注入假实现。
type kanbanAppStore interface {
	GetApp(ctx context.Context, id pgtype.UUID) (sqlc.App, error)
}

// KanbanAppLocatorFromStore 基于 app store 把 appID（UUID 字符串）解析为
// Kanban 执行坐标（KanbanAppLocation），供 HermesKanbanService 使用。
type KanbanAppLocatorFromStore struct {
	store kanbanAppStore
}

// NewKanbanAppLocatorFromStore 构造 locator。
func NewKanbanAppLocatorFromStore(store kanbanAppStore) *KanbanAppLocatorFromStore {
	return &KanbanAppLocatorFromStore{store: store}
}

// LocateApp 查询 app 行并组装 KanbanAppLocation。
// appID 必须是有效的 UUID 字符串，否则返回 ErrKanbanBadRequest。
// app 不存在返回 ErrNotFound。
func (l *KanbanAppLocatorFromStore) LocateApp(ctx context.Context, appID string) (KanbanAppLocation, error) {
	// parseUUID 是 service 包已有的 string→pgtype.UUID 辅助函数（pgtype.go）。
	id, err := parseUUID(appID)
	if err != nil {
		return KanbanAppLocation{}, fmt.Errorf("%w: 非法 app id", ErrKanbanBadRequest)
	}
	app, err := l.store.GetApp(ctx, id)
	if err != nil {
		// pgx.ErrNoRows 表示 app 记录真实不存在，映射为 ErrNotFound（404）。
		// 其他错误（网络、超时、约束异常等）属于 DB 故障，透传原始错误，
		// 由上层兜底映射为 500，避免将 DB 故障误报为资源不存在。
		if errors.Is(err, pgx.ErrNoRows) {
			return KanbanAppLocation{}, ErrNotFound
		}
		return KanbanAppLocation{}, fmt.Errorf("查询 app 失败: %w", err)
	}
	loc := KanbanAppLocation{
		// uuidToString 是 service 包已有的 pgtype.UUID→string 辅助函数（pgtype.go）。
		OrgID:       uuidToString(app.OrgID),
		OwnerUserID: uuidToString(app.OwnerUserID),
		NodeID:      uuidToString(app.RuntimeNodeID),
	}
	// ContainerID 是可空字段（pgtype.Text），仅在有效时填充。
	if app.ContainerID.Valid {
		loc.ContainerID = app.ContainerID.String
	}
	// stub 判定：dev stub 镜像 tag 约定以 -dev 结尾（hermes-runtime:hermes-main-dev）。
	// 精确方案（读容器内 /etc/oc-image.json）留作后续；后缀判定已足以触发降级提示。
	loc.Stub = strings.HasSuffix(app.RuntimeImageRef, "-dev")
	return loc, nil
}

