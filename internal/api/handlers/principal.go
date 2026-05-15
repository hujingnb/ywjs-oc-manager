package handlers

import (
	"github.com/gin-gonic/gin"

	"oc-manager/internal/auth"
)

// principalFromCtx 是 handler 包内统一的认证主体取出入口。
//
// RequireUserAuth 中间件挂载在 user 路由组上，进入这些 handler 时 ctx 中
// 必然存在 Principal；调用方无需再做 nil / ok 防御。public（健康检查、
// 登录、刷新）与 agent 路由不挂载中间件，那两组 handler 也不应调用本函数。
//
// 抽出包级 helper 主要为了避免每个 endpoint 都写一遍冗长的
// `auth.PrincipalFromContext(c.Request.Context())`，让 handler 函数体保持
// 在"绑请求 → 调 service → 写响应"三件事的薄壳形态。
func principalFromCtx(c *gin.Context) auth.Principal {
	p, _ := auth.PrincipalFromContext(c.Request.Context())
	return p
}
