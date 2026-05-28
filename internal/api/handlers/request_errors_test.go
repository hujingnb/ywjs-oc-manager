package handlers

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"oc-manager/internal/service"
)

// TestWriteMappedServiceErrorUsesValidationMessage 验证统一错误映射会剥离 sentinel 前缀并返回业务校验原因。
func TestWriteMappedServiceErrorUsesValidationMessage(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	err := fmt.Errorf("%w: 企业标识必须为 3-32 位小写字母、数字或短横线", service.ErrMemberCreateInvalid)

	writeMappedServiceError(c, err, http.StatusInternalServerError, "服务暂时不可用")

	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Contains(t, recorder.Body.String(), "企业标识必须")
	require.NotContains(t, recorder.Body.String(), service.ErrMemberCreateInvalid.Error())
}

// TestWriteMappedServiceErrorUsesFallback 验证统一错误映射在未命中规则时返回调用方指定的兜底状态码和文案。
func TestWriteMappedServiceErrorUsesFallback(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)

	writeMappedServiceError(c, fmt.Errorf("database timeout"), http.StatusBadGateway, "服务暂时不可用")

	require.Equal(t, http.StatusBadGateway, recorder.Code)
	require.Contains(t, recorder.Body.String(), "服务暂时不可用")
}
