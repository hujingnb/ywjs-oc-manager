package siteserver

import (
	"context"
	"io"

	"oc-manager/internal/integrations/storage"
)

// ObjectReader 是 handler 读取站点文件所需的最小能力。
// 生产实现为 *storage.S3ObjectStore（已实现该方法签名）；单测用内存 fake。
type ObjectReader interface {
	// GetObject 流式读取对象；不存在返回 storage.ErrObjectNotFound。
	GetObject(ctx context.Context, key string) (io.ReadCloser, int64, error)
}

// ErrObjectNotFound 别名，便于本包与单测引用同一哨兵。
var ErrObjectNotFound = storage.ErrObjectNotFound
