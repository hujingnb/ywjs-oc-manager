package handlers

import (
	"context"
	"io"

	"github.com/gin-gonic/gin"
	"golang.org/x/time/rate"
)

const transferLimitBurstBytes int64 = 32 * 1024

// TransferLimitConfig 是 handler 层使用的单请求传输限速配置；0 表示对应方向不限速。
type TransferLimitConfig struct {
	// UploadBytesPerSec 限制客户端上传到 manager 的单请求读取速率。
	UploadBytesPerSec int64
	// DownloadBytesPerSec 限制 manager 下载响应写给客户端的单请求读取速率。
	DownloadBytesPerSec int64
}

// limitUploadBody 在上传入口把请求体替换为限速 reader；未启用限速时不改变请求体。
func (c TransferLimitConfig) limitUploadBody(ctx *gin.Context) {
	if c.UploadBytesPerSec <= 0 || ctx.Request == nil || ctx.Request.Body == nil {
		return
	}
	ctx.Request.Body = newRateLimitedReadCloser(ctx.Request.Context(), ctx.Request.Body, c.UploadBytesPerSec)
}

// limitDownloadStream 在下载入口包装原始文件流；未启用限速时直接返回原始流。
func (c TransferLimitConfig) limitDownloadStream(ctx context.Context, stream io.ReadCloser) io.ReadCloser {
	if c.DownloadBytesPerSec <= 0 || stream == nil {
		return stream
	}
	return newRateLimitedReadCloser(ctx, stream, c.DownloadBytesPerSec)
}

// rateLimitedReadCloser 基于读取字节数等待 token，不缓存整文件，适合上传 body 和下载 stream 复用。
type rateLimitedReadCloser struct {
	// ctx 传递请求取消信号，避免客户端断开后 WaitN 继续阻塞。
	ctx context.Context
	// stream 是底层上传 body 或下载文件流，Close 语义保持由原始流负责。
	stream io.ReadCloser
	// limiter 按读取字节数消费 token，保证限速作用在流式读取过程。
	limiter *rate.Limiter
	// burstBytes 限制单次 WaitN 的 token 数，避免大块读取超过 rate limiter burst。
	burstBytes int
}

// newRateLimitedReadCloser 构造限速流。bytesPerSec 必须为正数；调用方负责在配置校验阶段拒绝负数。
func newRateLimitedReadCloser(ctx context.Context, stream io.ReadCloser, bytesPerSec int64) io.ReadCloser {
	burst := transferBurst(bytesPerSec)
	return &rateLimitedReadCloser{
		ctx:        ctx,
		stream:     stream,
		limiter:    rate.NewLimiter(rate.Limit(bytesPerSec), burst),
		burstBytes: burst,
	}
}

// Read 先把单次底层读取限制在 burst 内，等待成功后才把字节复制给调用方。
func (r *rateLimitedReadCloser) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return r.stream.Read(p)
	}
	readSize := len(p)
	if readSize > r.burstBytes {
		readSize = r.burstBytes
	}
	buf := make([]byte, readSize)
	n, err := r.stream.Read(buf)
	if n > 0 {
		if waitErr := r.wait(n); waitErr != nil {
			return 0, waitErr
		}
		copy(p, buf[:n])
	}
	return n, err
}

// Close 透传到底层流，保持上传 body 和下载 stream 原有的资源释放行为。
func (r *rateLimitedReadCloser) Close() error {
	return r.stream.Close()
}

// wait 将大块读取拆成不超过 burst 的多次等待，兼容 rate limiter 对 WaitN 的 burst 限制。
func (r *rateLimitedReadCloser) wait(n int) error {
	remaining := n
	for remaining > 0 {
		chunk := remaining
		if chunk > r.burstBytes {
			chunk = r.burstBytes
		}
		if err := r.limiter.WaitN(r.ctx, chunk); err != nil {
			return err
		}
		remaining -= chunk
	}
	return nil
}

// transferBurst 根据限速值选择 burst，低速配置下避免 burst 大于每秒 token 数。
func transferBurst(bytesPerSec int64) int {
	if bytesPerSec <= 1 {
		return 1
	}
	if bytesPerSec < transferLimitBurstBytes {
		return int(bytesPerSec)
	}
	return int(transferLimitBurstBytes)
}
