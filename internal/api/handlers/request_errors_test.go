package handlers

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"oc-manager/internal/api/apierror"
	"oc-manager/internal/service"
)

// TestWriteMappedServiceErrorUsesValidationMessage 验证统一错误映射会剥离 sentinel 前缀并返回业务校验原因。
func TestWriteMappedServiceErrorUsesValidationMessage(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	err := fmt.Errorf("%w: 企业标识必须为 3-32 位小写字母、数字或短横线", service.ErrMemberCreateInvalid)

	writeMappedServiceError(c, err, http.StatusInternalServerError, apierror.MsgInternal)

	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Contains(t, recorder.Body.String(), "企业标识必须")
	require.NotContains(t, recorder.Body.String(), service.ErrMemberCreateInvalid.Error())
}

// TestWriteMappedServiceErrorSurfacesKanbanReason 验证 kanban 请求参数非法会把具体字段原因
// 回给调用方（剥离 sentinel 前缀），而不是一律返回笼统的「任务看板请求参数非法」。
func TestWriteMappedServiceErrorSurfacesKanbanReason(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	// 模拟 service 层 buildKanbanCreateReq 对非法 assignee 抛出的带原因错误。
	err := fmt.Errorf("%w: assignee 只能由小写字母、数字、下划线（_）或连字符（-）组成，且需以小写字母或数字开头", service.ErrKanbanBadRequest)

	writeMappedServiceError(c, err, http.StatusInternalServerError, apierror.MsgInternal)

	require.Equal(t, http.StatusBadRequest, recorder.Code)
	// 响应须含具体原因，且不再暴露 sentinel 自身文案。
	require.Contains(t, recorder.Body.String(), "assignee 只能由小写字母")
	require.NotContains(t, recorder.Body.String(), service.ErrKanbanBadRequest.Error())
}

// TestWriteMappedServiceErrorUsesFallback 验证统一错误映射在未命中规则时返回调用方指定的兜底状态码和文案。
func TestWriteMappedServiceErrorUsesFallback(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)

	writeMappedServiceError(c, fmt.Errorf("database timeout"), http.StatusBadGateway, apierror.MsgInternal)

	require.Equal(t, http.StatusBadGateway, recorder.Code)
	// 兜底文案按请求 locale 从 catalog 解析；测试上下文未设 locale，回落 en。
	require.Contains(t, recorder.Body.String(), apierror.Localize(apierror.MsgInternal, apierror.LocaleFrom(c)))
}
