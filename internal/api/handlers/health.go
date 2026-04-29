package handlers

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// HealthResponse 是健康检查接口返回给容器编排和浏览器调试的稳定结构。
// 该接口不暴露数据库、Redis 或密钥细节，避免健康检查被滥用于探测内部配置。
type HealthResponse struct {
	Status string `json:"status"`
	Time   string `json:"time"`
}

// RegisterHealthRoutes 注册不需要认证的基础健康检查路由。
func RegisterHealthRoutes(router gin.IRouter) {
	router.GET("/healthz", func(ctx *gin.Context) {
		ctx.JSON(http.StatusOK, HealthResponse{
			Status: "ok",
			Time:   time.Now().UTC().Format(time.RFC3339),
		})
	})
}
