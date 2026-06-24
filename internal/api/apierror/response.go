// Package apierror 定义 HTTP 错误响应的统一结构。
//
// 引入独立包是为了让 middleware 与 handlers 双方都能依赖同一个错误响应
// 类型，且不会形成 middleware → handlers 的反向依赖。后续如有 agent 或
// 工作目录代理等独立 handler 包，亦可统一引用本包，避免错误体结构在仓库
// 内分叉。
package apierror

import "github.com/gin-gonic/gin"

// ErrorResponse 是所有 HTTP 错误响应的统一结构。
//
// Code 是机器可读的稳定标识，采用 SCREAMING_SNAKE_CASE，前端用来做 i18n、
// 跳转或条件分支判断；一旦发布即视为接口契约，后续只能新增不能改名。
//
// Message 是面向终端用户展示的中文文案，可能因 handler 语境而异；前端
// 默认展示该字段即可，无需理解 Code 的语义就能给出可读提示。
type ErrorResponse struct {
	// Code 是机器可读的错误标识，例如 NOT_FOUND、APP_NOT_FOUND、
	// NO_NODE_AVAILABLE，统一使用 SCREAMING_SNAKE_CASE 命名。
	Code string `json:"code" example:"APP_NOT_FOUND"`
	// Message 是面向用户的中文文案，前端默认展示该字段。
	Message string `json:"message" example:"应用不存在"`
}

// New 构造一条 ErrorResponse；调用方通常配合 c.JSON / c.AbortWithStatusJSON
// 写回响应。message 必须是已脱敏的对外文案，不要直接传入 service 错误链原文。
func New(code, message string) ErrorResponse {
	return ErrorResponse{Code: code, Message: message}
}

// JSON 按请求 locale 解析 key→message，写回 {code, message}。新代码统一用本函数，
// 取代 c.JSON(status, New(code, "中文"))。args 用于动态占位符消息。
func JSON(c *gin.Context, status int, code string, key MsgKey, args ...any) {
	c.JSON(status, ErrorResponse{Code: code, Message: Localize(key, LocaleFrom(c), args...)})
}
