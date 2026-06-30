// Package handlers 的 runtime_web_publish.go 实现 oc-publish 调用的 runtime 发布端点。
// 接受 X-OC-App-Token 鉴权 + multipart tar.gz 文件 + 可选 slug，转交 WebPublishService 完成发布。
package handlers

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	mlog "oc-manager/internal/log"
	"oc-manager/internal/service"
)

// webPublishRuntimeService 是 RuntimeWebPublishHandler 依赖的最小 service 接口，便于单测注入 stub。
type webPublishRuntimeService interface {
	// Publish 接收应用 token、站点 slug 和 tar.gz 流，完成发布并返回站点 URL 与到期时间。
	Publish(ctx context.Context, appToken, slug string, body io.Reader) (service.PublishResult, error)
}

// RuntimeWebPublishHandler 暴露给 hermes oc-publish 使用的 runtime 发布端点。
// 鉴权凭证使用 X-OC-App-Token 头（与 runtime_knowledge.go 相同的 token header 名称）。
type RuntimeWebPublishHandler struct {
	service webPublishRuntimeService
}

// NewRuntimeWebPublishHandler 构造 RuntimeWebPublishHandler。
func NewRuntimeWebPublishHandler(svc webPublishRuntimeService) *RuntimeWebPublishHandler {
	return &RuntimeWebPublishHandler{service: svc}
}

// RegisterRuntimeWebPublishRoutes 注册 runtime 发布路由。
// 路由不挂用户 JWT 中间件，鉴权由 handler 内联读取 X-OC-App-Token 完成。
func RegisterRuntimeWebPublishRoutes(router gin.IRouter, h *RuntimeWebPublishHandler) {
	router.POST("/api/v1/runtime/web-publish", h.Publish)
}

// Publish 接收 oc-publish 上传的 tar.gz（multipart file 字段）+ 可选 slug，转交 service 完成发布。
//
// @Summary      发布静态站点
// @Description  oc-publish 通过 app runtime token 上传站点 tar.gz，返回对外访问 URL 和到期时间
// @Tags         runtime-web-publish
// @Accept       multipart/form-data
// @Produce      json
// @Param        X-OC-App-Token  header    string  true   "per-app runtime token"
// @Param        slug            formData  string  false  "站点 slug（缺省由 service 分配或沿用已有站点 slug）"
// @Param        file            formData  file    true   "站点目录 tar.gz"
// @Success      200  {object}  service.PublishResult
// @Failure      401  {object}  ErrorResponse
// @Failure      400  {object}  ErrorResponse
// @Failure      503  {object}  ErrorResponse
// @Router       /runtime/web-publish [post]
func (h *RuntimeWebPublishHandler) Publish(c *gin.Context) {
	// X-OC-App-Token 是 Hermes runtime 专用鉴权头，与 runtime_knowledge.go 使用相同 header 名。
	token := c.GetHeader(runtimeKnowledgeTokenHeader)
	if token == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "missing app token"})
		return
	}
	// 取 multipart 中的 file 字段（站点目录的 tar.gz 归档）。
	file, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing file"})
		return
	}
	f, err := file.Open()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "cannot read file"})
		return
	}
	defer f.Close()

	// slug 为可选字段：首次发布时缺省由 service 分配随机 slug，后续更新时沿用已有记录。
	slug := c.PostForm("slug")
	res, err := h.service.Publish(c.Request.Context(), token, slug, f)
	if err != nil {
		// runtime 发布端点由 hermes oc-publish 自动调用，错误经 writeServiceError 脱敏成稳定
		// HTTP 码后调用方只看到兜底文案。这里记录原始错误（含底层 S3/DB/解包原因），
		// 否则线上发布失败无任何可排查线索（观测性兜底，不改变对外响应）。
		// 业务拒绝（slug 占用 / 未开通等）是正常结果而非系统故障，记 Warn；其余视为真实故障记 Error。
		if errors.Is(err, service.ErrConflict) || errors.Is(err, service.ErrWebPublishNotProvisioned) {
			slog.WarnContext(c.Request.Context(), "runtime web-publish 业务拒绝", "slug", slug, mlog.Err(err))
		} else {
			slog.ErrorContext(c.Request.Context(), "runtime web-publish 失败", "slug", slug, mlog.Err(err))
		}
		writeServiceError(c, err)
		return
	}
	c.JSON(http.StatusOK, res)
}
