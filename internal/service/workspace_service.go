package service

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"oc-manager/internal/auth"
	"oc-manager/internal/integrations/runtime"
	"oc-manager/internal/store/sqlc"
)

// WorkspaceStore 抽象 workspace service 需要的查询能力。
type WorkspaceStore interface {
	GetApp(ctx context.Context, id pgtype.UUID) (sqlc.App, error)
}

// WorkspaceService 把 manager 的工作目录访问代理到 runtime agent。
//
// 当前接口都是只读：列表、下载、归档；删除/写入由 worker handler 在专门的运维任务里完成，
// 避免 manager 直接对运行中的容器写入工作目录引发并发问题。
type WorkspaceService struct {
	store    WorkspaceStore
	adapter  runtime.Adapter
	dataRoot string
}

// NewWorkspaceService 创建 workspace 服务。
// dataRoot 为 agent 上工作目录的相对前缀，所有路径都会拼到该前缀下。
func NewWorkspaceService(store WorkspaceStore, adapter runtime.Adapter, dataRoot string) *WorkspaceService {
	if dataRoot == "" {
		dataRoot = "/var/lib/oc-agent/workspace"
	}
	return &WorkspaceService{store: store, adapter: adapter, dataRoot: dataRoot}
}

// 与 workspace 相关的错误。
var (
	ErrWorkspaceForbidden = errors.New("无权访问工作目录")
	ErrWorkspaceMissing   = errors.New("应用未关联节点或 adapter 未配置")
)

// WorkspaceListing 是列表接口的返回值。
type WorkspaceListing struct {
	Path    string                 `json:"path"`
	Entries []WorkspaceEntryResult `json:"entries"`
}

// WorkspaceEntryResult 是对外的条目视图。
type WorkspaceEntryResult struct {
	Path  string `json:"path"`
	Name  string `json:"name"`
	Size  int64  `json:"size"`
	IsDir bool   `json:"is_dir"`
}

// List 列出指定应用工作目录下的文件。
func (s *WorkspaceService) List(ctx context.Context, principal auth.Principal, appID, relative string) (WorkspaceListing, error) {
	app, err := s.loadAuthorizedApp(ctx, principal, appID)
	if err != nil {
		return WorkspaceListing{}, err
	}
	if s.adapter == nil || !app.RuntimeNodeID.Valid {
		return WorkspaceListing{}, ErrWorkspaceMissing
	}
	target := s.resolvePath(uuidToString(app.OrgID), uuidToString(app.ID), relative)
	listing, err := s.adapter.ListFiles(ctx, uuidToString(app.RuntimeNodeID), target)
	if err != nil {
		return WorkspaceListing{}, fmt.Errorf("查询工作目录失败: %w", err)
	}
	out := make([]WorkspaceEntryResult, 0, len(listing.Entries))
	for _, entry := range listing.Entries {
		out = append(out, WorkspaceEntryResult{Path: entry.Path, Name: entry.Name, Size: entry.Size, IsDir: entry.IsDir})
	}
	return WorkspaceListing{Path: target, Entries: out}, nil
}

// Download 下载工作目录中的文件，调用方负责关闭返回流。
func (s *WorkspaceService) Download(ctx context.Context, principal auth.Principal, appID, relative string) (io.ReadCloser, error) {
	app, err := s.loadAuthorizedApp(ctx, principal, appID)
	if err != nil {
		return nil, err
	}
	if s.adapter == nil || !app.RuntimeNodeID.Valid {
		return nil, ErrWorkspaceMissing
	}
	target := s.resolvePath(uuidToString(app.OrgID), uuidToString(app.ID), relative)
	return s.adapter.DownloadFile(ctx, uuidToString(app.RuntimeNodeID), target)
}

// Archive 把工作目录打包为 tar.gz 流。
func (s *WorkspaceService) Archive(ctx context.Context, principal auth.Principal, appID, relative string) (io.ReadCloser, error) {
	app, err := s.loadAuthorizedApp(ctx, principal, appID)
	if err != nil {
		return nil, err
	}
	if s.adapter == nil || !app.RuntimeNodeID.Valid {
		return nil, ErrWorkspaceMissing
	}
	target := s.resolvePath(uuidToString(app.OrgID), uuidToString(app.ID), relative)
	return s.adapter.ArchiveDirectory(ctx, uuidToString(app.RuntimeNodeID), target)
}

func (s *WorkspaceService) loadAuthorizedApp(ctx context.Context, principal auth.Principal, appID string) (sqlc.App, error) {
	id, err := parseUUID(appID)
	if err != nil {
		return sqlc.App{}, ErrNotFound
	}
	app, err := s.store.GetApp(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return sqlc.App{}, ErrNotFound
	}
	if err != nil {
		return sqlc.App{}, fmt.Errorf("查询应用失败: %w", err)
	}
	if !canViewApp(principal, app) {
		return sqlc.App{}, ErrWorkspaceForbidden
	}
	return app, nil
}

func (s *WorkspaceService) resolvePath(orgID, appID, relative string) string {
	parts := []string{s.dataRoot, "org", orgID, "app", appID}
	if relative != "" {
		parts = append(parts, relative)
	}
	return joinSlash(parts...)
}

func joinSlash(parts ...string) string {
	if len(parts) == 0 {
		return ""
	}
	out := parts[0]
	for _, p := range parts[1:] {
		if p == "" {
			continue
		}
		out = trimTrailingSlash(out) + "/" + trimLeadingSlash(p)
	}
	return out
}

func trimTrailingSlash(s string) string {
	for len(s) > 0 && s[len(s)-1] == '/' {
		s = s[:len(s)-1]
	}
	return s
}

func trimLeadingSlash(s string) string {
	for len(s) > 0 && s[0] == '/' {
		s = s[1:]
	}
	return s
}
