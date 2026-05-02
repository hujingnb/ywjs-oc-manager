package main

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
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
		// 后续 Task 注册 knowledge/sync 等
		default:
			_ = action
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
