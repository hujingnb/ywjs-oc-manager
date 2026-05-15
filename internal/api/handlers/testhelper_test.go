// Package handlers 的测试公共辅助。
//
// 单测复用：所有 handler 测试不再各自模拟 bearer token 校验，而是构造请求时
// 调用 withPrincipal 把已认证主体写入 c.Request.Context()，模拟
// RequireUserAuth 中间件已经放行的状态；缺 token / 无效 token 的 401 路径
// 集中由 internal/api/middleware/auth_test.go 覆盖。
package handlers

import (
	"net/http"

	"oc-manager/internal/auth"
)

// withPrincipal 把 Principal 注入 request 的 context，返回新的请求对象。
// handler 测试在调用 ServeHTTP 之前用它替代中间件实际挂载的注入流程，
// 既避免在每个 test 文件重复签发真实 JWT，也保证 handler 拿主体的方式与
// 生产路径完全一致（都走 auth.PrincipalFromContext）。
func withPrincipal(req *http.Request, p auth.Principal) *http.Request {
	return req.WithContext(auth.WithPrincipal(req.Context(), p))
}
