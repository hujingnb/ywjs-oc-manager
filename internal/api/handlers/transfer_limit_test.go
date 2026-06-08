package handlers

import (
	"bytes"
	"context"
	"errors"
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

// TestTransferLimitReadCloserDoesNotExposeBytesWhenContextCanceled 验证等待 token 失败时不向调用方暴露已预读的字节。
func TestTransferLimitReadCloserDoesNotExposeBytesWhenContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	stream := newRateLimitedReadCloser(
		ctx,
		io.NopCloser(bytes.NewBufferString("abc")),
		1,
	)
	buf := []byte("---")

	n, err := stream.Read(buf)

	require.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, 0, n)
	assert.Equal(t, "---", string(buf))
}

// TestTransferLimitReadCloserPropagatesReadErrorWithoutBytes 验证底层流未读到字节时会直接返回原始读取错误。
func TestTransferLimitReadCloserPropagatesReadErrorWithoutBytes(t *testing.T) {
	readErr := errors.New("read failed")
	stream := newRateLimitedReadCloser(
		t.Context(),
		&transferLimitErrorReadCloser{err: readErr},
		1<<20,
	)

	n, err := stream.Read(make([]byte, 8))

	require.ErrorIs(t, err, readErr)
	assert.Equal(t, 0, n)
}

// TestTransferLimitReadCloserPreservesPartialReadWithError 验证底层流返回部分字节和错误时，等待成功后保留字节并返回原始错误。
func TestTransferLimitReadCloserPreservesPartialReadWithError(t *testing.T) {
	readErr := errors.New("partial read failed")
	stream := newRateLimitedReadCloser(
		t.Context(),
		&transferLimitPartialErrorReadCloser{
			data: []byte("abc"),
			err:  readErr,
		},
		1<<20,
	)
	buf := make([]byte, 8)

	n, err := stream.Read(buf)

	require.ErrorIs(t, err, readErr)
	assert.Equal(t, 3, n)
	assert.Equal(t, "abc", string(buf[:n]))
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

// transferLimitErrorReadCloser 模拟底层流在未读到任何字节时直接失败。
type transferLimitErrorReadCloser struct {
	// err 是测试期望被原样传播的读取错误。
	err error
}

// Read 返回 0 字节和预设错误，覆盖无部分数据的失败路径。
func (r *transferLimitErrorReadCloser) Read(p []byte) (int, error) {
	return 0, r.err
}

// Close 模拟失败流的关闭动作；测试不需要额外资源释放。
func (r *transferLimitErrorReadCloser) Close() error {
	return nil
}

// transferLimitPartialErrorReadCloser 模拟底层流同时返回部分数据和错误的边界行为。
type transferLimitPartialErrorReadCloser struct {
	// data 是底层流在返回错误前已经读到的字节。
	data []byte
	// err 是读取部分字节后需要继续向上传播的底层错误。
	err error
}

// Read 先复制部分数据再返回错误，模拟 io.Reader 允许的 n > 0 && err != nil 场景。
func (r *transferLimitPartialErrorReadCloser) Read(p []byte) (int, error) {
	n := copy(p, r.data)
	return n, r.err
}

// Close 模拟部分失败流的关闭动作；测试不需要额外资源释放。
func (r *transferLimitPartialErrorReadCloser) Close() error {
	return nil
}
