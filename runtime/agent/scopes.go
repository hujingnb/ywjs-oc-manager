package main

import (
	"archive/tar"
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// tar 同步硬上限：避免恶意 tar 撑爆磁盘。
const (
	maxKnowledgeTarSize    = 2 * 1024 * 1024 * 1024 // 2 GiB 总大小（与 spec §11.5 工作目录上限同源）
	maxKnowledgeTarEntries = 10000
	maxKnowledgeFileSize   = 100 * 1024 * 1024 // 单文件 100 MiB（应用级单文件 upload）
)

// 工作目录浏览/下载上限：与 spec §11.5 workspace.* 配置一致，
// agent 这层做强制兜底，manager 侧再做一次校验形成两层防御。
const (
	maxWorkspaceDownloadSize = 500 * 1024 * 1024       // 单文件 500 MiB
	maxWorkspaceArchiveSize  = 2 * 1024 * 1024 * 1024  // archive 总 2 GiB
	maxWorkspaceArchiveItems = 10000                   // archive 最多条目
)

// ErrInvalidPath 表示用户输入的相对路径越出 scope 沙箱。
var ErrInvalidPath = errors.New("invalid path")

// resolveScopePath 把 (dataRoot, scope, rel) 三段拼成绝对路径，并强制沙箱：
//
//   - rel 必须是相对路径；前置 "/" 仅作为"根目录标记"被允许（"" / "/" 都返回 scope 根）
//   - 真正的绝对路径（如 "/etc/passwd"、"//server/share"）一律拒绝
//   - rel 任意一段为 ".." 都拒绝
//   - 拼接后的绝对路径必须仍以 dataRoot/scope 为前缀（防御 symlink 之外的逃逸）
//
// 故意不接受 ".." 即使它语义上停留在 scope 内：拒绝 traversal 字面量比依赖 prefix
// 比对更稳妥，避免某些边角拼接漏判。
func resolveScopePath(dataRoot, scope, rel string) (string, error) {
	// "" 或 仅由 / 组成 → scope 根
	if strings.Trim(rel, "/") == "" {
		scopeRoot, err := filepath.Abs(filepath.Join(dataRoot, scope))
		if err != nil {
			return "", err
		}
		return scopeRoot, nil
	}
	if filepath.IsAbs(rel) {
		return "", ErrInvalidPath
	}
	// 检查任何字面 ".." 段（filepath.Clean 会折叠 ..，所以校验必须在 Clean 之前）
	for _, seg := range strings.Split(filepath.ToSlash(rel), "/") {
		if seg == ".." {
			return "", ErrInvalidPath
		}
	}
	cleaned := filepath.Clean(rel)
	scopeRoot, err := filepath.Abs(filepath.Join(dataRoot, scope))
	if err != nil {
		return "", err
	}
	abs, err := filepath.Abs(filepath.Join(scopeRoot, cleaned))
	if err != nil {
		return "", err
	}
	if abs != scopeRoot && !strings.HasPrefix(abs+string(filepath.Separator), scopeRoot+string(filepath.Separator)) {
		return "", ErrInvalidPath
	}
	return abs, nil
}

// newScopesHandler 为 /v1/scopes/ 下的 file API 端点提供统一入口。
//
// 所有端点统一走 bearer 鉴权，避免重复手工套 wrapAuth。
// 子路径分三类：apps/*、orgs/*、cleanup-archives，由对应 handler 函数分派。
// 没匹配到的子路径返回 404。
func newScopesHandler(dataRoot string, agentToken any) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/scopes/apps/", withAgentAuth(agentToken, scopesAppsHandler(dataRoot)))
	mux.HandleFunc("/v1/scopes/orgs/", withAgentAuth(agentToken, scopesOrgsHandler(dataRoot)))
	mux.HandleFunc("/v1/scopes/cleanup-archives", withAgentAuth(agentToken, handleCleanupArchives(dataRoot)))
	return mux
}

// scopesAppsHandler 处理 /v1/scopes/apps/<appID>/<action>... 路径。
// action 由后续 Task 注册（init/knowledge/workspace/archive 等）。
func scopesAppsHandler(dataRoot string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rest := strings.TrimPrefix(r.URL.Path, "/v1/scopes/apps/")
		parts := strings.SplitN(rest, "/", 2)
		if len(parts) < 2 || parts[0] == "" {
			writeError(w, http.StatusBadRequest, "missing app id or action")
			return
		}
		appID, action := parts[0], parts[1]
		if !isValidScopeID(appID) {
			writeError(w, http.StatusBadRequest, "invalid app id")
			return
		}
		switch {
		case action == "init" && r.Method == http.MethodPost:
			handleAppInit(w, r, dataRoot, appID)
		case action == "runtime/file" && r.Method == http.MethodPut:
			// Hermes 时代 runtime 配置文件 sandbox = apps/<appID>/.hermes/。
			// manager 通过该 endpoint 上传 SOUL.md / config.yaml / .env / skills/*/SKILL.md,
			// 节点 agent 写到 dataRoot/apps/<appID>/.hermes/<relPath>;容器启动时由
			// dataRoot/apps/<appID>/.hermes 全量 bind mount 到 /opt/data。
			// 历史:OpenClaw 时代沙箱根曾为 apps/<appID>/openclaw-config/(file-level mount)。
			handleKnowledgeFileUpload(w, r, dataRoot, filepath.Join("apps", appID, ".hermes"))
		case action == "runtime/file" && r.Method == http.MethodDelete:
			// Hermes 时代删除 apps/<appID>/.hermes/ 下的文件 / 子目录。
			handleKnowledgeFileDelete(w, r, dataRoot, filepath.Join("apps", appID, ".hermes"))
		case action == "knowledge/sync" && r.Method == http.MethodPost:
			handleKnowledgeSync(w, r, dataRoot, filepath.Join("apps", appID, "knowledge"))
		case action == "knowledge/file" && r.Method == http.MethodPut:
			handleKnowledgeFileUpload(w, r, dataRoot, filepath.Join("apps", appID, "knowledge"))
		case action == "knowledge/file" && r.Method == http.MethodDelete:
			handleKnowledgeFileDelete(w, r, dataRoot, filepath.Join("apps", appID, "knowledge"))
		case action == "workspace" && r.Method == http.MethodGet:
			// Hermes 容器把整个 .hermes 挂载到 /opt/data,workspace 子目录因此
			// 落在节点 apps/<id>/.hermes/workspace。manager workspace API 必须读
			// 同一物理路径,不再是 OpenClaw 时代独立 mount 的 apps/<id>/workspace。
			handleWorkspaceList(w, r, dataRoot, filepath.Join("apps", appID, ".hermes", "workspace"))
		case action == "workspace/download" && r.Method == http.MethodGet:
			handleWorkspaceDownload(w, r, dataRoot, filepath.Join("apps", appID, ".hermes", "workspace"))
		case action == "workspace/archive" && r.Method == http.MethodGet:
			handleWorkspaceArchive(w, r, dataRoot, filepath.Join("apps", appID, ".hermes", "workspace"))
		case action == "archive" && r.Method == http.MethodPost:
			handleAppArchive(w, r, dataRoot, appID)
		case action == "sessions" && r.Method == http.MethodDelete:
			// 配置变更(改 model/prompt/知识库/重启等)后清空 .hermes/sessions/,
			// 让 Hermes 启动新 session 时重新 snapshot system_prompt。
			// Hermes 在 session 启动时把 system_prompt 冻结存进 SQLite,后续 SOUL.md
			// 改动对老 session 不生效——必须清 session 才能让最新配置进入对话。
			handleAppSessionsClear(w, r, dataRoot, appID)
		default:
			writeError(w, http.StatusNotFound, "unknown action")
		}
	}
}

// scopesOrgsHandler 处理 /v1/scopes/orgs/<orgID>/<action>... 路径。
func scopesOrgsHandler(dataRoot string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rest := strings.TrimPrefix(r.URL.Path, "/v1/scopes/orgs/")
		parts := strings.SplitN(rest, "/", 2)
		if len(parts) < 2 || parts[0] == "" {
			writeError(w, http.StatusBadRequest, "missing org id or action")
			return
		}
		orgID, action := parts[0], parts[1]
		if !isValidScopeID(orgID) {
			writeError(w, http.StatusBadRequest, "invalid org id")
			return
		}
		switch {
		case action == "knowledge/sync" && r.Method == http.MethodPost:
			handleKnowledgeSync(w, r, dataRoot, filepath.Join("orgs", orgID, "knowledge"))
		case action == "knowledge/file" && r.Method == http.MethodPut:
			handleKnowledgeFileUpload(w, r, dataRoot, filepath.Join("orgs", orgID, "knowledge"))
		case action == "knowledge/file" && r.Method == http.MethodDelete:
			handleKnowledgeFileDelete(w, r, dataRoot, filepath.Join("orgs", orgID, "knowledge"))
		default:
			writeError(w, http.StatusNotFound, "unknown action")
		}
	}
}

// isValidScopeID 校验 app/org 标识符不含路径分隔符或父级跳转字符。
// 只接受字母、数字、连字符、下划线，长度 1~64。manager 侧 app_id / org_id
// 是 UUID 字符串，足够覆盖。
func isValidScopeID(id string) bool {
	if len(id) == 0 || len(id) > 64 {
		return false
	}
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '-' || r == '_':
		default:
			return false
		}
	}
	return true
}

// handleAppInit 创建 apps/<appID>/{.hermes,knowledge} 2 个子目录。
//   - .hermes:Hermes 时代 runtime 数据根。manager 通过 /runtime/file PUT 写入
//     SOUL.md / config.yaml / .env / skills/<...>;容器启动时由该目录全量
//     bind mount 到 /opt/data。其余 workspace/sessions/logs/cron/memories 等
//     由 Hermes 启动时自动创建,manager 无需预建。
//   - knowledge:legacy OpenClaw 时代知识库 sync 目标(Hermes 时代知识库走
//     skills 机制,manager 不再调用 /knowledge/* endpoint,但保留目录以兼容
//     旧调用方)。
//
// 操作幂等:MkdirAll 在目录已存在时 no-op。
func handleAppInit(w http.ResponseWriter, _ *http.Request, dataRoot, appID string) {
	for _, sub := range []string{".hermes", "knowledge"} {
		dir := filepath.Join(dataRoot, "apps", appID, sub)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	writeJSON(w, map[string]any{"ok": true, "app_id": appID})
}

// handleKnowledgeSync 接收 tar 流并把 scopeRel（如 "apps/<id>/knowledge"）
// 的内容整体替换为 tar 解压结果。
//
// 流程：解压到同级 .sync-* 临时目录 → 原子 rename 替换旧目录 → 删旧。
// 失败时 tmp 目录 RemoveAll，不影响旧目录。
//
// 安全：
//   - 总字节上限 maxKnowledgeTarSize；超限断开请求
//   - 条目数上限 maxKnowledgeTarEntries
//   - tar 内每个 entry 名拒绝绝对路径与含 .. 段
//   - 跳过非常规文件（symlink / device / fifo），仅写目录与普通文件
func handleKnowledgeSync(w http.ResponseWriter, r *http.Request, dataRoot, scopeRel string) {
	scopeAbs, err := resolveScopePath(dataRoot, scopeRel, "")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	parent := filepath.Dir(scopeAbs)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	tmpDir, err := os.MkdirTemp(parent, ".sync-*")
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.RemoveAll(tmpDir)
		}
	}()

	// 限制总大小：多 1 字节用来检测溢出
	limit := io.LimitReader(r.Body, maxKnowledgeTarSize+1)
	tr := tar.NewReader(limit)
	totalRead := int64(0)
	count := 0
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			writeError(w, http.StatusBadRequest, "tar parse: "+err.Error())
			return
		}
		count++
		if count > maxKnowledgeTarEntries {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("too many entries (max %d)", maxKnowledgeTarEntries))
			return
		}
		// entry 名安全校验
		name := filepath.ToSlash(filepath.Clean(hdr.Name))
		if filepath.IsAbs(hdr.Name) || strings.HasPrefix(name, "../") || strings.Contains(name, "/../") || name == ".." {
			writeError(w, http.StatusBadRequest, "invalid entry path: "+hdr.Name)
			return
		}
		dest := filepath.Join(tmpDir, name)
		// 二次防御：dest 必须仍在 tmpDir 内
		if !strings.HasPrefix(dest+string(filepath.Separator), tmpDir+string(filepath.Separator)) && dest != tmpDir {
			writeError(w, http.StatusBadRequest, "entry escapes scope: "+hdr.Name)
			return
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(dest, 0o755); err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			f, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
			if err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			n, err := io.Copy(f, tr)
			_ = f.Close()
			if err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			totalRead += n
			if totalRead > maxKnowledgeTarSize {
				writeError(w, http.StatusBadRequest, fmt.Sprintf("tar exceeds size limit (max %d bytes)", maxKnowledgeTarSize))
				return
			}
		default:
			// symlink/device/fifo 全部跳过
		}
	}

	// 原子替换旧目录：先把旧目录改名挪走 → rename tmp 为目标 → 删旧
	if _, err := os.Stat(scopeAbs); err == nil {
		stale, err := os.MkdirTemp(parent, ".stale-*")
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		// 把旧目录内容挪进 stale
		if err := os.Rename(scopeAbs, filepath.Join(stale, "old")); err != nil {
			_ = os.RemoveAll(stale)
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		go func(p string) { _ = os.RemoveAll(p) }(stale)
	}
	if err := os.Rename(tmpDir, scopeAbs); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	cleanup = false
	writeJSON(w, map[string]any{"ok": true, "entries": count, "bytes": totalRead})
}

// handleKnowledgeFileUpload 把单文件写入 scope 目录的 ?path= 指定子路径。
// body 为文件原始字节，最多 100 MiB。
func handleKnowledgeFileUpload(w http.ResponseWriter, r *http.Request, dataRoot, scopeRel string) {
	rel := r.URL.Query().Get("path")
	if rel == "" {
		writeError(w, http.StatusBadRequest, "missing ?path=")
		return
	}
	scopeRoot, err := resolveScopePath(dataRoot, scopeRel, "")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	dest, err := resolveScopePath(dataRoot, scopeRel, rel)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if dest == scopeRoot {
		writeError(w, http.StatusBadRequest, "?path= 不能指向 scope 根")
		return
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	limit := io.LimitReader(r.Body, maxKnowledgeFileSize+1)
	tmp := dest + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	n, err := io.Copy(f, limit)
	_ = f.Close()
	if err != nil {
		_ = os.Remove(tmp)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if n > maxKnowledgeFileSize {
		_ = os.Remove(tmp)
		writeError(w, http.StatusBadRequest, fmt.Sprintf("file exceeds size limit (max %d bytes)", maxKnowledgeFileSize))
		return
	}
	if err := os.Rename(tmp, dest); err != nil {
		_ = os.Remove(tmp)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]any{"ok": true, "bytes": n, "path": rel})
}

// workspaceEntry 是 list 接口返回的 entry 结构。
type workspaceEntry struct {
	Name       string `json:"name"`
	Type       string `json:"type"` // file | dir
	Size       int64  `json:"size,omitempty"`
	ModifiedAt string `json:"modified_at"`
}

// handleWorkspaceList 列举 workspace 子目录的内容。
// path 为相对 workspace 根的子路径，缺省为根。
func handleWorkspaceList(w http.ResponseWriter, r *http.Request, dataRoot, scopeRel string) {
	rel := r.URL.Query().Get("path")
	target, err := resolveScopePath(dataRoot, scopeRel, rel)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	fi, err := os.Stat(target)
	if err != nil {
		if os.IsNotExist(err) {
			// workspace 目录可能尚未创建，返回空列表
			writeJSON(w, map[string]any{"path": "/" + strings.TrimLeft(rel, "/"), "entries": []workspaceEntry{}})
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !fi.IsDir() {
		writeError(w, http.StatusBadRequest, "path is not a directory")
		return
	}
	entries, err := os.ReadDir(target)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]workspaceEntry, 0, len(entries))
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			continue
		}
		// 跳过非常规文件（symlink / device / fifo），只暴露 file 与 dir
		if !(info.Mode().IsRegular() || info.IsDir()) {
			continue
		}
		entry := workspaceEntry{
			Name:       e.Name(),
			ModifiedAt: info.ModTime().UTC().Format("2006-01-02T15:04:05Z"),
		}
		if info.IsDir() {
			entry.Type = "dir"
		} else {
			entry.Type = "file"
			entry.Size = info.Size()
		}
		out = append(out, entry)
	}
	writeJSON(w, map[string]any{"path": "/" + strings.TrimLeft(rel, "/"), "entries": out})
}

// handleWorkspaceDownload 流式下载单个普通文件。
// 拒绝 symlink / 目录 / 非常规文件。
func handleWorkspaceDownload(w http.ResponseWriter, r *http.Request, dataRoot, scopeRel string) {
	rel := r.URL.Query().Get("path")
	if rel == "" {
		writeError(w, http.StatusBadRequest, "missing ?path=")
		return
	}
	target, err := resolveScopePath(dataRoot, scopeRel, rel)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	fi, err := os.Lstat(target)
	if err != nil {
		if os.IsNotExist(err) {
			writeError(w, http.StatusNotFound, "file not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !fi.Mode().IsRegular() {
		writeError(w, http.StatusBadRequest, "path is not a regular file")
		return
	}
	if fi.Size() > maxWorkspaceDownloadSize {
		writeError(w, http.StatusRequestEntityTooLarge,
			fmt.Sprintf("file exceeds download limit (max %d bytes)", maxWorkspaceDownloadSize))
		return
	}
	f, err := os.Open(target)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", fmt.Sprintf("%d", fi.Size()))
	w.Header().Set("Content-Disposition",
		fmt.Sprintf(`attachment; filename="%s"`, filepath.Base(target)))
	_, _ = io.Copy(w, f)
}

// handleWorkspaceArchive 把指定子目录流式打成 zip 返回给客户端。
func handleWorkspaceArchive(w http.ResponseWriter, r *http.Request, dataRoot, scopeRel string) {
	rel := r.URL.Query().Get("path")
	target, err := resolveScopePath(dataRoot, scopeRel, rel)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	fi, err := os.Stat(target)
	if err != nil {
		if os.IsNotExist(err) {
			writeError(w, http.StatusNotFound, "directory not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !fi.IsDir() {
		writeError(w, http.StatusBadRequest, "archive only supports directories")
		return
	}

	zipName := filepath.Base(target)
	if zipName == "" || zipName == "." {
		zipName = "workspace"
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition",
		fmt.Sprintf(`attachment; filename="%s.zip"`, zipName))

	zw := zip.NewWriter(w)
	totalBytes := int64(0)
	totalItems := 0
	walkErr := filepath.Walk(target, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if path == target {
			return nil
		}
		// 跳过非常规非目录文件
		if !(info.Mode().IsRegular() || info.IsDir()) {
			return nil
		}
		totalItems++
		if totalItems > maxWorkspaceArchiveItems {
			return fmt.Errorf("archive entries exceed limit (max %d)", maxWorkspaceArchiveItems)
		}
		entryName, err := filepath.Rel(target, path)
		if err != nil {
			return err
		}
		entryName = filepath.ToSlash(entryName)
		hdr := &zip.FileHeader{Name: entryName, Method: zip.Deflate, Modified: info.ModTime()}
		hdr.SetMode(info.Mode())
		if info.IsDir() {
			hdr.Name = entryName + "/"
			_, err := zw.CreateHeader(hdr)
			return err
		}
		writer, err := zw.CreateHeader(hdr)
		if err != nil {
			return err
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		n, err := io.Copy(writer, f)
		if err != nil {
			return err
		}
		totalBytes += n
		if totalBytes > maxWorkspaceArchiveSize {
			return fmt.Errorf("archive bytes exceed limit (max %d)", maxWorkspaceArchiveSize)
		}
		return nil
	})
	if walkErr != nil {
		// 错误时关闭 zip 输出，客户端 stream 会断开。
		_ = zw.Close()
		// 已经发出过 200 头，无法改 status；但可以关闭连接。
		return
	}
	_ = zw.Close()
}

// handleAppArchive 把 apps/{appID}/ 整目录 mv 到 archived/{appID}-{timestamp}/。
// manager 在应用软删除流程中调此端点。
// 应用目录不存在视为成功（幂等）。
func handleAppArchive(w http.ResponseWriter, _ *http.Request, dataRoot, appID string) {
	src := filepath.Join(dataRoot, "apps", appID)
	if _, err := os.Stat(src); err != nil {
		if os.IsNotExist(err) {
			writeJSON(w, map[string]any{"ok": true, "skipped": true})
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	archivedRoot := filepath.Join(dataRoot, "archived")
	if err := os.MkdirAll(archivedRoot, 0o755); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	ts := nowFunc().UTC().Format("20060102T150405Z")
	dest := filepath.Join(archivedRoot, appID+"-"+ts)
	if err := os.Rename(src, dest); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]any{"ok": true, "archived_to": dest})
}

// handleAppSessionsClear 清空指定 app 的 Hermes 会话相关存储,使 Hermes
// 下次启动新 session 时 snapshot 最新 SOUL.md(含最新知识库 / 模型 / persona)。
//
// Hermes 的 session 存储分两处:
//  1. .hermes/state.db (+ -shm / -wal):SQLite session history / 元数据,
//     这是 system_prompt 冻结存储的位置——配置变更后必须清掉才能让新
//     SOUL.md 进入新会话。
//  2. .hermes/sessions/:request_dump、文件级 sessions.json 等附属文件。
//
// 调用前提:Hermes 容器必须先停止(SQLite 持有文件锁,运行中删 state.db
// 会损坏数据库)。worker 端 AppRestartContainerHandler 已按 stop → clear → start
// 顺序编排。
//
// 幂等:任一文件不存在视为成功(返回 cleared 列表中只列实际清掉的项)。
func handleAppSessionsClear(w http.ResponseWriter, _ *http.Request, dataRoot, appID string) {
	hermesHome := filepath.Join(dataRoot, "apps", appID, ".hermes")
	ts := nowFunc().UTC().Format("20060102T150405Z")
	cleared := make([]string, 0, 4)
	// 1) 整体 rename sessions/ 子目录,异步 RemoveAll 避免大目录阻塞 HTTP。
	sessionsDir := filepath.Join(hermesHome, "sessions")
	if _, err := os.Stat(sessionsDir); err == nil {
		staged := filepath.Join(hermesHome, ".sessions-cleared-"+ts)
		if err := os.Rename(sessionsDir, staged); err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("rename sessions: %v", err))
			return
		}
		go func(p string) { _ = os.RemoveAll(p) }(staged)
		cleared = append(cleared, "sessions/")
	} else if !os.IsNotExist(err) {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("stat sessions: %v", err))
		return
	}
	// 2) 删 state.db / -shm / -wal 三件套(SQLite WAL 模式三个文件)。
	for _, suffix := range []string{"state.db", "state.db-shm", "state.db-wal"} {
		p := filepath.Join(hermesHome, suffix)
		if err := os.Remove(p); err == nil {
			cleared = append(cleared, suffix)
		} else if !os.IsNotExist(err) {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("remove %s: %v", suffix, err))
			return
		}
	}
	writeJSON(w, map[string]any{"ok": true, "cleared": cleared, "cleared_at": ts})
}

// handleCleanupArchives 删除 archived/ 下 mtime 超过 retention_days 天的子目录。
// retention_days 必须 > 0，缺省 30。
func handleCleanupArchives(dataRoot string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		days := 30
		if v := r.URL.Query().Get("retention_days"); v != "" {
			parsed, err := parsePositiveInt(v)
			if err != nil {
				writeError(w, http.StatusBadRequest, "invalid retention_days")
				return
			}
			days = parsed
		}
		archivedRoot := filepath.Join(dataRoot, "archived")
		entries, err := os.ReadDir(archivedRoot)
		if err != nil {
			if os.IsNotExist(err) {
				writeJSON(w, map[string]any{"ok": true, "removed": 0})
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		cutoff := nowFunc().Add(-time.Duration(days) * 24 * time.Hour)
		removed := 0
		for _, e := range entries {
			info, err := e.Info()
			if err != nil {
				continue
			}
			if info.ModTime().After(cutoff) {
				continue
			}
			path := filepath.Join(archivedRoot, e.Name())
			if err := os.RemoveAll(path); err != nil {
				continue
			}
			removed++
		}
		writeJSON(w, map[string]any{"ok": true, "removed": removed})
	}
}

// nowFunc 与 parsePositiveInt 是 archive/cleanup helper；nowFunc 在测试中被替换。
var nowFunc = time.Now

func parsePositiveInt(s string) (int, error) {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, errors.New("not a number")
		}
		n = n*10 + int(c-'0')
		if n > 100000 {
			return 0, errors.New("too large")
		}
	}
	if n <= 0 {
		return 0, errors.New("must be positive")
	}
	return n, nil
}

// handleKnowledgeFileDelete 删除单文件或子目录。
// 不存在视为成功（幂等）。
func handleKnowledgeFileDelete(w http.ResponseWriter, r *http.Request, dataRoot, scopeRel string) {
	rel := r.URL.Query().Get("path")
	if rel == "" {
		writeError(w, http.StatusBadRequest, "missing ?path=")
		return
	}
	scopeRoot, err := resolveScopePath(dataRoot, scopeRel, "")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	dest, err := resolveScopePath(dataRoot, scopeRel, rel)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if dest == scopeRoot {
		writeError(w, http.StatusBadRequest, "?path= 不能指向 scope 根")
		return
	}
	if err := os.RemoveAll(dest); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]any{"ok": true, "path": rel})
}
