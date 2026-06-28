package handlers

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"unicode"

	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"

	"oc-manager/internal/api/apierror"
	redactlog "oc-manager/internal/log"
	"oc-manager/internal/service"
)

var jsonFieldNames = map[string]string{
	"AgentID":          "agent_id",
	"AgentToken":       "agent_token",
	"AdminDisplayName": "admin_display_name",
	"AdminPassword":    "admin_password",
	"AdminUsername":    "admin_username",
	"AppName":          "app_name",
	"BaseURL":          "base_url",
	"Code":             "code",
	"CreditAmount":     "credit_amount",
	"DisplayName":      "display_name",
	"Name":             "name",
	"NewPassword":      "new_password",
	"NodeID":           "node_id",
	"OldPassword":      "old_password",
	"Password":         "password",
	"RefreshToken":     "refresh_token",
	"SystemPrompt":     "system_prompt",
	"Username":         "username",
}

// mappedServiceErrorRules 是 handler 层共用的 service sentinel 错误映射。
// 只放跨接口语义稳定的规则；接口特有的 404/403 文案仍保留在各自 handler 中。
var mappedServiceErrorRules = []serviceErrorRule{
	safeErrorRule(service.ErrAgentTokenInvalid, http.StatusUnauthorized, "AGENT_TOKEN_INVALID"),
	validationErrorRule(service.ErrEnrollInputInvalid, http.StatusBadRequest, "ENROLL_INVALID"),
	validationErrorRule(service.ErrMemberCreateInvalid, http.StatusBadRequest, "MEMBER_INVALID"),
	// 通用资源不存在，映射为 404。
	{target: service.ErrNotFound, statusCode: http.StatusNotFound, code: "NOT_FOUND", msgKey: apierror.MsgNotFound},
	// 任务看板相关 sentinel error 映射。
	{target: service.ErrKanbanForbidden, statusCode: http.StatusForbidden, code: "KANBAN_FORBIDDEN", msgKey: apierror.MsgKanbanForbidden},
	{target: service.ErrKanbanRuntimeUnavailable, statusCode: http.StatusServiceUnavailable, code: "RUNTIME_NOT_AVAILABLE", msgKey: apierror.MsgRuntimeNotAvailable},
	{target: service.ErrKanbanNotSupported, statusCode: http.StatusServiceUnavailable, code: "KANBAN_NOT_SUPPORTED_ON_STUB", msgKey: apierror.MsgKanbanNotSupported},
	// 用 validationErrorRule：剥离 sentinel 前缀后把具体字段原因（如「assignee 只能由
	// 小写字母…」）回给调用方，避免一律返回笼统的「任务看板请求参数非法」让用户无法定位。
	validationErrorRule(service.ErrKanbanBadRequest, http.StatusBadRequest, "KANBAN_BAD_REQUEST"),
	{target: service.ErrKanbanCLI, statusCode: http.StatusBadGateway, code: "KANBAN_CLI_ERROR", safe: true},
	{target: service.ErrKanbanOutputInvalid, statusCode: http.StatusBadGateway, code: "KANBAN_OUTPUT_INVALID", msgKey: apierror.MsgHermesIncompatible},
	// Hermes Cron 相关 sentinel error 映射。
	{target: service.ErrCronForbidden, statusCode: http.StatusForbidden, code: "CRON_FORBIDDEN", msgKey: apierror.MsgCronForbidden},
	{target: service.ErrCronRuntimeUnavailable, statusCode: http.StatusServiceUnavailable, code: "RUNTIME_NOT_AVAILABLE", msgKey: apierror.MsgRuntimeNotAvailable},
	{target: service.ErrCronNotSupported, statusCode: http.StatusServiceUnavailable, code: "CRON_NOT_SUPPORTED_ON_STUB", msgKey: apierror.MsgCronNotSupported},
	{target: service.ErrCronBadRequest, statusCode: http.StatusBadRequest, code: "CRON_BAD_REQUEST", msgKey: apierror.MsgCronBadRequest},
	{target: service.ErrCronCLI, statusCode: http.StatusBadGateway, code: "CRON_CLI_ERROR", safe: true},
	{target: service.ErrCronOutputInvalid, statusCode: http.StatusBadGateway, code: "CRON_OUTPUT_INVALID", msgKey: apierror.MsgCronOutputInvalid},
	// 实例会话 sentinel error 映射（语义与 kanban/cron 同模式）。
	{target: service.ErrConversationForbidden, statusCode: http.StatusForbidden, code: "CONVERSATION_FORBIDDEN", msgKey: apierror.MsgConversationForbidden},
	{target: service.ErrConversationRuntimeUnavailable, statusCode: http.StatusServiceUnavailable, code: "RUNTIME_NOT_AVAILABLE", msgKey: apierror.MsgRuntimeNotAvailable},
	{target: service.ErrConversationNotSupported, statusCode: http.StatusServiceUnavailable, code: "CONVERSATION_NOT_SUPPORTED_ON_STUB", msgKey: apierror.MsgConversationNotSupported},
	validationErrorRule(service.ErrConversationBadRequest, http.StatusBadRequest, "CONVERSATION_BAD_REQUEST"),
	{target: service.ErrConversationCLI, statusCode: http.StatusBadGateway, code: "CONVERSATION_CLI_ERROR", safe: true},
	{target: service.ErrConversationOutputInvalid, statusCode: http.StatusBadGateway, code: "CONVERSATION_OUTPUT_INVALID", msgKey: apierror.MsgHermesIncompatible},
	// 对话文件错误映射。
	{target: service.ErrConversationFileForbidden, statusCode: http.StatusForbidden, code: "CONVERSATION_FILE_FORBIDDEN", msgKey: apierror.MsgConversationForbidden},
	{target: service.ErrConversationFileNotFound, statusCode: http.StatusNotFound, code: "CONVERSATION_FILE_NOT_FOUND", msgKey: apierror.MsgNotFound},
	{target: service.ErrConversationFileUnsupported, statusCode: http.StatusBadRequest, code: "CONVERSATION_FILE_UNSUPPORTED", msgKey: apierror.MsgConversationFileUnsupported},
	{target: service.ErrConversationFileTooLarge, statusCode: http.StatusRequestEntityTooLarge, code: "CONVERSATION_FILE_TOO_LARGE", msgKey: apierror.MsgConversationFileTooLarge},
}

// writeBindError 将 Gin 的 JSON 绑定错误转成面向调用方的 400 文案。
// 该函数只暴露请求体层面的字段名、类型和 JSON 格式问题，不返回 Go 结构体名或底层解析细节。
// 文案走 catalog 按请求 locale 输出；带占位符的模板（类型错误 / 缺失字段）以 args 携带动态字段名。
func writeBindError(c *gin.Context, err error) {
	key, args := bindErrorMessage(err)
	apierror.JSON(c, http.StatusBadRequest, "BAD_REQUEST", key, args...)
}

type serviceErrorRule struct {
	target     error
	statusCode int
	code       string
	// msgKey 指向 catalog 中的静态文案；safe / validation 规则运行期动态生成原因，故留空。
	msgKey     apierror.MsgKey
	safe       bool
	validation bool
}

// safeErrorRule 声明允许把脱敏后的 service 错误原因返回给调用方的映射规则。
func safeErrorRule(target error, statusCode int, code string) serviceErrorRule {
	return serviceErrorRule{target: target, statusCode: statusCode, code: code, safe: true}
}

// validationErrorRule 声明业务校验错误映射规则，并剥离 sentinel 前缀后返回具体原因。
func validationErrorRule(target error, statusCode int, code string) serviceErrorRule {
	return serviceErrorRule{target: target, statusCode: statusCode, code: code, validation: true}
}

// writeMappedServiceError 按顺序匹配 service sentinel error，并写入对应 HTTP 响应。
// 静态规则按请求 locale 输出 catalog 文案；safe / validation 规则保留运行期动态原因（脱敏 /
// 剥离 sentinel 前缀）。未命中任何规则时，用调用方传入的兜底状态码与 fallbackKey 兜底。
func writeMappedServiceError(c *gin.Context, err error, fallbackStatus int, fallbackKey apierror.MsgKey) {
	for _, rule := range mappedServiceErrorRules {
		if !errors.Is(err, rule.target) {
			continue
		}
		switch {
		case rule.safe:
			c.JSON(rule.statusCode, apierror.New(rule.code, redactlog.SafeErrorMessage(err)))
		case rule.validation:
			c.JSON(rule.statusCode, apierror.New(rule.code, validationServiceMessage(err, rule.target)))
		default:
			apierror.JSON(c, rule.statusCode, rule.code, rule.msgKey)
		}
		return
	}
	apierror.JSON(c, fallbackStatus, "INTERNAL", fallbackKey)
}

// bindErrorMessage 归一化请求体绑定错误，避免所有接口都返回含糊的“请求参数不完整”。
// 返回 catalog MsgKey 与格式化所需 args（无占位符的模板 args 为 nil），由调用方按 locale 输出。
func bindErrorMessage(err error) (apierror.MsgKey, []any) {
	if err == nil {
		return apierror.MsgBadRequestGeneric, nil
	}
	if errors.Is(err, io.EOF) {
		return apierror.MsgEmptyBody, nil
	}
	var syntaxErr *json.SyntaxError
	if errors.As(err, &syntaxErr) {
		return apierror.MsgInvalidJSON, nil
	}
	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &typeErr) {
		field := typeErr.Field
		if field == "" {
			field = typeErr.Value
		}
		return apierror.MsgInvalidType, []any{field}
	}
	var validationErrs validator.ValidationErrors
	if errors.As(err, &validationErrs) {
		return validationErrorMessage(validationErrs)
	}
	return apierror.MsgBadRequestGeneric, nil
}

// validationErrorMessage 汇总 validator 返回的字段级错误。
// 当前 DTO 主要使用 required tag，因此优先给出缺失字段列表（MsgMissingRequiredFields）；
// 其它 tag 走 MsgValidationFailed。字段名列表以 args 形式作为模板占位符填充。
func validationErrorMessage(validationErrs validator.ValidationErrors) (apierror.MsgKey, []any) {
	missing := make([]string, 0, len(validationErrs))
	invalid := make([]string, 0, len(validationErrs))
	for _, fieldErr := range validationErrs {
		name := jsonFieldName(fieldErr)
		if fieldErr.Tag() == "required" {
			missing = append(missing, name)
			continue
		}
		invalid = append(invalid, name)
	}
	if len(missing) > 0 {
		return apierror.MsgMissingRequiredFields, []any{strings.Join(missing, ", ")}
	}
	return apierror.MsgValidationFailed, []any{strings.Join(invalid, ", ")}
}

// jsonFieldName 将 validator 的 Go 字段名映射为对外契约中的 json tag 名。
// 如果找不到 tag，则回退到 validator 字段名，避免空字段名让调用方无法定位问题。
func jsonFieldName(fieldErr validator.FieldError) string {
	fieldName := fieldErr.StructField()
	if name, ok := jsonFieldNames[fieldName]; ok {
		return name
	}
	if fieldName == "" {
		return fieldErr.Field()
	}
	return lowerSnake(fieldName)
}

// lowerSnake 将少数未登记字段从 Go 命名转换为 JSON 常用的 snake_case，作为字段名兜底。
func lowerSnake(value string) string {
	var out strings.Builder
	for i, r := range value {
		if unicode.IsUpper(r) {
			if i > 0 {
				out.WriteByte('_')
			}
			out.WriteRune(unicode.ToLower(r))
			continue
		}
		out.WriteRune(r)
	}
	return out.String()
}

// validationServiceMessage 从 sentinel error 包装链中提取可展示的业务校验原因。
// service 层使用 "%w: 具体原因" 保留 errors.Is 能力；HTTP 响应只需要具体原因本身。
func validationServiceMessage(err error, sentinel error) string {
	message := redactlog.SafeErrorMessage(err)
	prefix := sentinel.Error() + ": "
	if strings.HasPrefix(message, prefix) {
		return strings.TrimSpace(strings.TrimPrefix(message, prefix))
	}
	return message
}
