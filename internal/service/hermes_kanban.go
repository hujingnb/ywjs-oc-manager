// Package service —— hermes_kanban.go 实现 Hermes Kanban 任务看板能力。
// manager 不持有 kanban 数据，全部通过在 hermes 容器内执行 `hermes kanban`
// CLI 并解析 --json 输出获得；写操作同样走 CLI verb。
package service

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"oc-manager/internal/auth"
	"oc-manager/internal/integrations/runtime"
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
// args 必须已是白名单校验过的 argv 切片（不含 "hermes kanban" 前缀）。
func (s *HermesKanbanService) runCLI(ctx context.Context, loc KanbanAppLocation, args []string) ([]byte, error) {
	cmd := append([]string{"hermes", "kanban"}, args...)
	res, err := s.execer.ContainerExecJSON(ctx, loc.NodeID, loc.ContainerID, cmd)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrKanbanCLI, err)
	}
	if res.ExitCode != 0 {
		msg := strings.TrimSpace(res.Stderr)
		if len(msg) > 4096 {
			msg = msg[:4096]
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
	out, err := s.runCLI(ctx, loc, []string{"boards", "list", "--all", "--json"})
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
	args := []string{"list", "--board", board, "--json"}
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
	out, err := s.runCLI(ctx, loc, args)
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
	out, err := s.runCLI(ctx, loc, []string{"show", taskID, "--board", b, "--json"})
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
	out, err := s.runCLI(ctx, loc, []string{"runs", taskID, "--board", b, "--json"})
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
	out, err := s.runCLI(ctx, loc, []string{"stats", "--board", b, "--json"})
	if err != nil {
		return KanbanStats{}, err
	}
	var stats KanbanStats
	if err := json.Unmarshal(out, &stats); err != nil {
		return KanbanStats{}, fmt.Errorf("%w: %v", ErrKanbanOutputInvalid, err)
	}
	return stats, nil
}
