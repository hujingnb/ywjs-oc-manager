package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// ConfigHandler 提供登录前可读的平台级前端配置（无需鉴权）。
// 当前仅下发国际化默认语言与受支持语言集合，供前端登录页初始化界面语言。
type ConfigHandler struct {
	defaultLocale    string
	supportedLocales []string
}

// NewConfigHandler 创建公开配置 handler。defaultLocale 来自 manager 配置文件 i18n.default_locale。
func NewConfigHandler(defaultLocale string, supportedLocales []string) *ConfigHandler {
	return &ConfigHandler{defaultLocale: defaultLocale, supportedLocales: supportedLocales}
}

// RegisterPublicConfigRoutes 注册公开配置路由（public 分组，无 Bearer token）。
func RegisterPublicConfigRoutes(router gin.IRouter, handler *ConfigHandler) {
	router.GET("/api/v1/config", handler.Get)
}

// Get 返回平台公开前端配置。
//
// @Summary      公开前端配置
// @Description  登录前可读的平台级配置：默认界面语言与受支持语言集合
// @Tags         config
// @Produce      json
// @Success      200  {object}  PublicConfigResponse
// @Router       /config [get]
func (h *ConfigHandler) Get(c *gin.Context) {
	c.JSON(http.StatusOK, PublicConfigResponse{
		DefaultLocale:    h.defaultLocale,
		SupportedLocales: h.supportedLocales,
	})
}
