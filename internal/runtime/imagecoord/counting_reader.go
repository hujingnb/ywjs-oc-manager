package imagecoord

import "io"

// countingReader 透传 Read,把累计已读字节通过 onProgress 回调上报。
//
// 用于 manager -> agent 镜像 tar 上传过程:把 archive io.Reader 包一层,
// 让 LoadImage 在内部 io.Copy 时,每读一块就同步把 syncing_image 阶段
// 的字节级进度推给 Coordinator,由后者节流后广播。
//
// 单 goroutine 使用,无需加锁;onProgress 自身若有锁,由调用方负责。
type countingReader struct {
	r          io.Reader
	count      int64
	onProgress func(int64)
}

// newCountingReader 构造一个透传读取器。onProgress 允许为 nil(等价于纯 io.Reader)。
func newCountingReader(r io.Reader, onProgress func(int64)) *countingReader {
	return &countingReader{r: r, onProgress: onProgress}
}

// Read 透传底层 r.Read,累加成功读取的字节数并触发回调。
// 注意:回调在每次成功读取后同步调用,如果上游产生密集小包,可能高频触发。
// 调用方(Coordinator.doSync)在回调里只做一次 chan send + publish,不做重活。
func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	if n > 0 {
		c.count += int64(n)
		if c.onProgress != nil {
			c.onProgress(c.count)
		}
	}
	return n, err
}
