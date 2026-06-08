package handlers

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTransferLimitLeavesUploadBodyUnchangedWhenDisabled 验证限速为 0 时不包装请求体，保持历史上传路径无额外行为。
func TestTransferLimitLeavesUploadBodyUnchangedWhenDisabled(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	body := &transferLimitTestReadCloser{Buffer: bytes.NewBufferString("content")}
	c.Request = httptest.NewRequest(http.MethodPost, "/upload", body)

	TransferLimitConfig{}.limitUploadBody(c)

	assert.Same(t, body, c.Request.Body)
}

// TestTransferLimitWrapsUploadBodyWhenEnabled 验证配置上传限速后 handler 会把请求体替换为限速 reader。
func TestTransferLimitWrapsUploadBodyWhenEnabled(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/upload", bytes.NewBufferString("content"))

	TransferLimitConfig{UploadBytesPerSec: 1 << 20}.limitUploadBody(c)

	_, ok := c.Request.Body.(*rateLimitedReadCloser)
	assert.True(t, ok)
}

// TestTransferLimitReadCloserPreservesData 验证限速 reader 不改变读取到的字节内容。
func TestTransferLimitReadCloserPreservesData(t *testing.T) {
	stream := newRateLimitedReadCloser(
		t.Context(),
		io.NopCloser(bytes.NewBufferString("abcdef")),
		1<<20,
	)

	got, err := io.ReadAll(stream)
	require.NoError(t, err)
	require.NoError(t, stream.Close())
	assert.Equal(t, "abcdef", string(got))
}

// TestTransferLimitLeavesDownloadStreamUnchangedWhenDisabled 验证下载限速为 0 时复用原始 stream。
func TestTransferLimitLeavesDownloadStreamUnchangedWhenDisabled(t *testing.T) {
	stream := &transferLimitTestReadCloser{Buffer: bytes.NewBufferString("content")}

	got := (TransferLimitConfig{}).limitDownloadStream(t.Context(), stream)

	assert.Same(t, stream, got)
}

// TestTransferLimitWrapsDownloadStreamWhenEnabled 验证配置下载限速后统一下载路径会使用限速 reader。
func TestTransferLimitWrapsDownloadStreamWhenEnabled(t *testing.T) {
	stream := io.NopCloser(bytes.NewBufferString("content"))

	got := (TransferLimitConfig{DownloadBytesPerSec: 1 << 20}).limitDownloadStream(t.Context(), stream)

	_, ok := got.(*rateLimitedReadCloser)
	assert.True(t, ok)
}

// transferLimitTestReadCloser 让测试可以用 assert.Same 校验接口内的具体指针身份。
type transferLimitTestReadCloser struct {
	// Buffer 提供测试读取内容，同时让该类型以指针形式实现 io.ReadCloser。
	*bytes.Buffer
}

// Close 模拟真实流的关闭动作；测试不需要额外资源释放。
func (r *transferLimitTestReadCloser) Close() error {
	return nil
}
