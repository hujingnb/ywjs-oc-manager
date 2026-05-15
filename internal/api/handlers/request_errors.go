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
	"Code":             "code",
	"CreditAmount":     "credit_amount",
	"DisplayName":      "display_name",
	"Name":             "name",
	"NodeID":           "node_id",
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
}

// writeBindError 将 Gin 的 JSON 绑定错误转成面向调用方的 400 文案。
// 该函数只暴露请求体层面的字段名、类型和 JSON 格式问题，不返回 Go 结构体名或底层解析细节。
func writeBindError(c *gin.Context, err error) {
	c.JSON(http.StatusBadRequest, apierror.New("BAD_REQUEST", bindErrorMessage(err)))
}

type serviceErrorRule struct {
	target     error
	statusCode int
	code       string
	message    string
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
// 未命中公共规则时，使用调用方传入的兜底状态码和文案，保留接口自身的默认错误语义。
func writeMappedServiceError(c *gin.Context, err error, fallbackStatus int, fallbackMessage string) {
	for _, rule := range mappedServiceErrorRules {
		if !errors.Is(err, rule.target) {
			continue
		}
		message := rule.message
		if rule.safe {
			message = redactlog.SafeErrorMessage(err)
		}
		if rule.validation {
			message = validationServiceMessage(err, rule.target)
		}
		c.JSON(rule.statusCode, apierror.New(rule.code, message))
		return
	}
	c.JSON(fallbackStatus, apierror.New("INTERNAL", fallbackMessage))
}

// bindErrorMessage 归一化请求体绑定错误，避免所有接口都返回含糊的“请求参数不完整”。
func bindErrorMessage(err error) string {
	if err == nil {
		return "请求参数格式错误"
	}
	if errors.Is(err, io.EOF) {
		return "请求体不能为空"
	}
	var syntaxErr *json.SyntaxError
	if errors.As(err, &syntaxErr) {
		return "请求体不是合法 JSON"
	}
	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &typeErr) {
		field := typeErr.Field
		if field == "" {
			field = typeErr.Value
		}
		return "请求参数类型错误: " + field
	}
	var validationErrs validator.ValidationErrors
	if errors.As(err, &validationErrs) {
		return validationErrorMessage(validationErrs)
	}
	return "请求参数格式错误"
}

// validationErrorMessage 汇总 validator 返回的字段级错误。
// 当前 DTO 主要使用 required tag，因此优先给出缺失字段列表；其它 tag 保留字段名提示。
func validationErrorMessage(validationErrs validator.ValidationErrors) string {
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
		return "缺少必填参数: " + strings.Join(missing, ", ")
	}
	return "请求参数校验失败: " + strings.Join(invalid, ", ")
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
