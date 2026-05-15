package auth

import "context"

// principalContextKey 是 Principal 在 context.Context 中的存储键。
//
// 采用未导出 struct 类型作为 ctx key 是 Go 社区的常规惯例，目的在于：
//   - 避免使用字符串键时跨包的命名冲突；
//   - 防止外部调用方通过同名字符串伪造或意外读取主体；
//   - 让 linter 能在静态检查时识别 ctx 注入路径。
type principalContextKey struct{}

// WithPrincipal 把已认证主体写入 ctx，由 RequireUserAuth 中间件调用一次，
// 下游 handler 与 service 通过 PrincipalFromContext 读出。
//
// 业务代码不应在请求路径中途修改 Principal；如需基于另一主体调用 service
// （例如 onboarding 内部的代账户操作），应显式传 Principal 形参，而不是
// 重新覆盖 ctx 上的主体，避免日志与审计串号。
func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, principalContextKey{}, p)
}

// PrincipalFromContext 从 ctx 读出主体。
//
// 中间件挂载后，user 路由组进入 handler 时该值恒存在；handler 可放心
// 忽略 ok 返回值。public / agent 路由组不挂载中间件，调用方读出时 ok=false，
// 调用方需要自行决定后续逻辑（一般是不需要主体；如需要则属于路由分组错误）。
func PrincipalFromContext(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalContextKey{}).(Principal)
	return p, ok
}
