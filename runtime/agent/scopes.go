package main

import (
	"archive/tar"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// tar 同步硬上限：避免恶意 tar 撑爆磁盘。
const (
	maxKnowledgeTarSize    = 2 * 1024 * 1024 * 1024 // 2 GiB 总大小（与 spec §11.5 工作目录上限同源）
	maxKnowledgeTarEntries = 10000
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
// 子路径分两类：apps/* 与 orgs/*，由对应 handler 函数分派 action。
// 没匹配到的子路径返回 404。
func newScopesHandler(dataRoot, agentToken string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/scopes/apps/", withAgentAuth(agentToken, scopesAppsHandler(dataRoot)))
	mux.HandleFunc("/v1/scopes/orgs/", withAgentAuth(agentToken, scopesOrgsHandler(dataRoot)))
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
		case action == "knowledge/sync" && r.Method == http.MethodPost:
			handleKnowledgeSync(w, r, dataRoot, filepath.Join("apps", appID, "knowledge"))
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

// handleAppInit 创建 apps/<appID>/{knowledge,workspace,state,logs} 4 个子目录。
// 操作幂等：MkdirAll 在目录已存在时 no-op。
func handleAppInit(w http.ResponseWriter, _ *http.Request, dataRoot, appID string) {
	for _, sub := range []string{"knowledge", "workspace", "state", "logs"} {
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
