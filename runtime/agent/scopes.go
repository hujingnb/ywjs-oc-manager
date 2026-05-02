package main

import (
	"errors"
	"net/http"
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
// 当前为骨架，未实现的子路径返回 501；后续 Task 在 scopesAppsHandler /
// scopesOrgsHandler 里按需补具体处理函数。
func newScopesHandler(dataRoot, agentToken string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/scopes/", withAgentAuth(agentToken, func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotImplemented, "scope endpoint not implemented yet")
	}))
	return mux
}
