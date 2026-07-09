package service

import (
	"archive/tar"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"strings"
	"time"

	"oc-manager/internal/auth"
	"oc-manager/internal/integrations/storage"
	"oc-manager/internal/store/sqlc"
)

// WorkspaceStore 抽象 workspace service 需要的查询能力。
type WorkspaceStore interface {
	GetApp(ctx context.Context, id string) (sqlc.App, error)
}

// WorkspaceService 把 manager 的工作目录访问代理到 S3（spec-A2a）。
//
// 当前接口都是只读：列表、下载、归档；删除/写入由 worker handler 在专门的运维任务里完成，
// 避免 manager 直接对运行中的容器写入工作目录引发并发问题。
type WorkspaceService struct {
	store      WorkspaceStore
	objects    storage.ObjectStore
	presignTTL time.Duration
}

// NewWorkspaceService 创建 workspace 服务。
// objects 为 S3 对象存储，presignTTL 为预签名 URL 有效期。
func NewWorkspaceService(store WorkspaceStore, objects storage.ObjectStore, presignTTL time.Duration) *WorkspaceService {
	if presignTTL <= 0 {
		presignTTL = 15 * time.Minute
	}
	return &WorkspaceService{store: store, objects: objects, presignTTL: presignTTL}
}

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
	// ModTime 为文件创建时间。来源是 S3 对象的 LastModified——S3 无独立创建时间字段，
	// 而工作目录产物写入后基本不再变更，故以最后修改时间等同创建时间对外展示。
	// 目录由对象层级推断、无对应对象，保持零值，前端按 IsDir 不予展示。
	ModTime time.Time `json:"mod_time"`
}

// List 列出指定应用工作目录下的文件。
//
// 数据源改为 S3：列举 apps/<appID>/workspace/<relative>/ 下的对象，
// 仅返回当前层级的直接子条目（文件或目录）。目录通过相对 key 中包含 "/" 推断。
//
// keyword 非空时进入模糊搜索模式：忽略 relative，递归列举整个工作目录树，
// 对文件完整相对路径做大小写不敏感子串匹配，返回带完整路径的命中文件（见 searchWorkspace）。
func (s *WorkspaceService) List(ctx context.Context, principal auth.Principal, appID, relative, keyword string) (WorkspaceListing, error) {
	_, err := s.loadAuthorizedApp(ctx, principal, appID)
	if err != nil {
		return WorkspaceListing{}, err
	}
	relPath, err := cleanWorkspaceRelative(relative, true)
	if err != nil {
		return WorkspaceListing{}, err
	}
	if s.objects == nil {
		return WorkspaceListing{}, ErrWorkspaceMissing
	}

	// 构造 S3 列举前缀：apps/<appID>/workspace/<relPath>/
	// relPath 为空时直接列 workspace/ 根目录
	wsPrefix := storage.AppPrefix(appID) + "workspace/"

	// 关键字非空时走递归搜索：搜索范围是整个工作目录而非当前层级，因此忽略 relPath。
	if kw := strings.TrimSpace(keyword); kw != "" {
		return s.searchWorkspace(ctx, wsPrefix, kw)
	}

	listPrefix := wsPrefix
	if relPath != "" {
		listPrefix = wsPrefix + relPath + "/"
	}

	infos, err := s.objects.ListObjects(ctx, listPrefix)
	if err != nil {
		return WorkspaceListing{}, fmt.Errorf("查询工作目录失败: %w", err)
	}

	// 从 S3 对象平铺列表中提取当前层级的直接子条目。
	// 相对 key（已去 listPrefix）若含 "/" 则属于子目录，取第一个 "/" 前的路径段作为目录名；
	// 否则为文件，直接用相对 key 作文件名。
	// 用 map 对目录去重（同一目录下可能有多个对象）。
	seen := make(map[string]bool)
	var entries []WorkspaceEntryResult
	for _, info := range infos {
		if info.Key == "" {
			// 跳过与 listPrefix 完全相同的"目录占位"对象（key 去掉前缀后为空）
			continue
		}
		idx := strings.Index(info.Key, "/")
		if idx >= 0 {
			// 路径中含 "/"：说明此条目属于某个子目录，取第一段作为目录名
			dirName := info.Key[:idx]
			if dirName == "" || seen[dirName] {
				continue
			}
			seen[dirName] = true
			entryPath := joinSlash(relPath, dirName)
			entries = append(entries, WorkspaceEntryResult{
				Path:  entryPath,
				Name:  dirName,
				Size:  0, // 目录无大小
				IsDir: true,
			})
		} else {
			// 无 "/"：直接文件
			fileName := info.Key
			if seen[fileName] {
				continue
			}
			seen[fileName] = true
			entryPath := joinSlash(relPath, fileName)
			entries = append(entries, WorkspaceEntryResult{
				Path:    entryPath,
				Name:    fileName,
				Size:    info.Size,
				IsDir:   false,
				ModTime: info.LastModified,
			})
		}
	}

	// 对外路径与 relPath 对齐；根目录返回 "/"，子目录返回 relPath 本身
	displayPath := relPath
	if displayPath == "" {
		displayPath = "/"
	}
	return WorkspaceListing{Path: displayPath, Entries: entries}, nil
}

// searchWorkspace 递归列举整个工作目录并按关键字模糊匹配文件。
//
// 匹配对象是文件相对工作目录根的完整路径（大小写不敏感子串），关键字既能命中文件名，
// 也能命中所在子目录名，便于在多级目录中快速定位。结果只含文件：S3 中目录没有独立对象，
// 无法稳定作为搜索结果返回；Path 字段保留完整相对路径，前端据此展示位置并定位下载。
// 返回 Path 固定为 "/"，与前端「根目录视角下的完整相对路径」下载逻辑保持一致。
func (s *WorkspaceService) searchWorkspace(ctx context.Context, wsPrefix, keyword string) (WorkspaceListing, error) {
	infos, err := s.objects.ListObjects(ctx, wsPrefix)
	if err != nil {
		return WorkspaceListing{}, fmt.Errorf("查询工作目录失败: %w", err)
	}
	lowerKey := strings.ToLower(keyword)
	var entries []WorkspaceEntryResult
	for _, info := range infos {
		if info.Key == "" {
			// 跳过与 wsPrefix 完全相同的目录占位对象
			continue
		}
		if !strings.Contains(strings.ToLower(info.Key), lowerKey) {
			continue
		}
		// 取最后一个 "/" 后的片段作为展示文件名，无 "/" 时整体即文件名
		name := info.Key
		if idx := strings.LastIndex(info.Key, "/"); idx >= 0 {
			name = info.Key[idx+1:]
		}
		entries = append(entries, WorkspaceEntryResult{
			Path:    info.Key,
			Name:    name,
			Size:    info.Size,
			IsDir:   false,
			ModTime: info.LastModified,
		})
	}
	return WorkspaceListing{Path: "/", Entries: entries}, nil
}

// Download 下载工作目录中的文件，通过预签名 URL 获取内容后以流返回，调用方负责关闭。
func (s *WorkspaceService) Download(ctx context.Context, principal auth.Principal, appID, relative string) (io.ReadCloser, error) {
	_, err := s.loadAuthorizedApp(ctx, principal, appID)
	if err != nil {
		return nil, err
	}
	relPath, err := cleanWorkspaceRelative(relative, false)
	if err != nil {
		return nil, err
	}
	if s.objects == nil {
		return nil, ErrWorkspaceMissing
	}

	// 构造 S3 对象 key：apps/<appID>/workspace/<relPath>
	key := storage.AppPrefix(appID) + "workspace/" + relPath
	url, err := s.objects.PresignGet(ctx, key, s.presignTTL)
	if err != nil {
		return nil, fmt.Errorf("生成下载链接失败: %w", err)
	}

	// 通过预签名 URL 发起 HTTP GET 获取对象内容流
	resp, err := http.Get(url) //nolint:noctx // presign URL 已包含鉴权信息，ctx 传递无效
	if err != nil {
		return nil, fmt.Errorf("下载工作区文件失败: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("下载工作区文件失败: HTTP %d", resp.StatusCode)
	}
	return resp.Body, nil
}

// Archive 把工作目录打包为 tar 流写到 w。
// 逐一列举 apps/<appID>/workspace/<relative>/ 下对象，预签名下载后写入 tar。
// 返回值使用具名返回以便 defer 捕获 tar.Close 错误：tar.Writer.Close 写入 EOF 块，
// 丢弃该错误会导致截断的 tar 被误当完整包处理。
func (s *WorkspaceService) Archive(ctx context.Context, principal auth.Principal, appID, relative string, w io.Writer) (err error) {
	_, err = s.loadAuthorizedApp(ctx, principal, appID)
	if err != nil {
		return err
	}
	relPath, err := cleanWorkspaceRelative(relative, true)
	if err != nil {
		return err
	}
	if s.objects == nil {
		return ErrWorkspaceMissing
	}

	// 构造 S3 列举前缀
	wsPrefix := storage.AppPrefix(appID) + "workspace/"
	listPrefix := wsPrefix
	if relPath != "" {
		listPrefix = wsPrefix + relPath + "/"
	}

	infos, err := s.objects.ListObjects(ctx, listPrefix)
	if err != nil {
		return fmt.Errorf("列举工作区对象失败: %w", err)
	}

	tw := tar.NewWriter(w)
	defer func() {
		// tar.Close 写入 EOF 终止块；仅当函数本身未出错时才把 Close 错误作为最终错误
		// 传播，避免覆盖循环内更具体的失败原因。
		if cerr := tw.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("关闭 tar 流失败: %w", cerr)
		}
	}()

	for _, info := range infos {
		if info.Key == "" {
			continue
		}
		// 完整 S3 key = listPrefix + info.Key
		fullKey := listPrefix + info.Key

		// 预签名 URL 逐个下载
		dlURL, err := s.objects.PresignGet(ctx, fullKey, s.presignTTL)
		if err != nil {
			return fmt.Errorf("生成下载链接失败（%s）: %w", info.Key, err)
		}
		resp, err := http.Get(dlURL) //nolint:noctx
		if err != nil {
			return fmt.Errorf("下载工作区文件失败（%s）: %w", info.Key, err)
		}
		if resp.StatusCode != http.StatusOK {
			_ = resp.Body.Close()
			return fmt.Errorf("下载工作区文件失败（%s）: HTTP %d", info.Key, resp.StatusCode)
		}

		// 写入 tar：header 使用相对于 relPath 的路径，保留目录结构
		hdr := &tar.Header{
			Name: info.Key,
			Mode: 0644,
			Size: info.Size,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			_ = resp.Body.Close()
			return fmt.Errorf("写入 tar header 失败（%s）: %w", info.Key, err)
		}
		if _, err := io.Copy(tw, resp.Body); err != nil {
			_ = resp.Body.Close()
			return fmt.Errorf("写入 tar 内容失败（%s）: %w", info.Key, err)
		}
		_ = resp.Body.Close()
	}
	return nil
}

func (s *WorkspaceService) loadAuthorizedApp(ctx context.Context, principal auth.Principal, appID string) (sqlc.App, error) {
	// appID 直接作为字符串传入；不存在时 store 返回 sql.ErrNoRows。
	app, err := s.store.GetApp(ctx, appID)
	if errors.Is(err, sql.ErrNoRows) {
		return sqlc.App{}, ErrNotFound
	}
	if err != nil {
		return sqlc.App{}, fmt.Errorf("查询应用失败: %w", err)
	}
	if app.AiccHidden {
		return sqlc.App{}, ErrNotFound
	}
	// app.OrgID / app.OwnerUserID 已是 string，直接传入权限校验。
	if !auth.CanViewApp(principal, app.OrgID, app.OwnerUserID) {
		return sqlc.App{}, ErrWorkspaceForbidden
	}
	return app, nil
}

func cleanWorkspaceRelative(relative string, allowRoot bool) (string, error) {
	value := strings.TrimSpace(relative)
	if value == "" || value == "/" {
		if allowRoot {
			return "", nil
		}
		return "", ErrWorkspaceBadPath
	}
	if path.IsAbs(value) {
		return "", ErrWorkspaceBadPath
	}
	cleaned := path.Clean(value)
	if cleaned == "." {
		if allowRoot {
			return "", nil
		}
		return "", ErrWorkspaceBadPath
	}
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", ErrWorkspaceBadPath
	}
	return cleaned, nil
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
