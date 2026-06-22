// store.go 定义本包的核心抽象接口与关联类型：
// - ObjectStore：S3 对象读写（上传、预签名、存在性检测、前缀搬运/删除）
// 具体实现位于 s3.go（基于 aws-sdk-go-v2），本文件不引入任何 SDK 依赖。
package storage

import (
	"context"
	"io"
	"time"
)

// ObjectInfo 是列举操作返回的单个对象元信息。
type ObjectInfo struct {
	Key          string    // 相对调用方传入 prefix 的 key（已去掉 prefix 前缀）
	Size         int64     // 对象字节数
	LastModified time.Time // 对象最后修改时间；S3 无独立创建时间，工作目录产物写入后基本不变，可作创建时间展示
}

// ObjectStore 是标准 S3 对象读写抽象。实现见 s3.go（aws-sdk-go-v2）。
// 所有 key 均为 bucket 内的对象键（不含 bucket 名）。
type ObjectStore interface {
	// PutObject 上传一个对象；size 为内容字节数（<0 表示未知，由实现决定是否缓冲）。
	PutObject(ctx context.Context, key string, r io.Reader, size int64) error
	// PresignGet 为 key 生成有效期 ttl 的预签名 GET URL（pod 只读下载用）。
	// 对象不存在不报错（预签名是离线签名，URL 使用时才校验存在）。
	PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error)
	// ObjectExists 判断对象是否存在（bootstrap 决定是否给出 restore URL）。
	ObjectExists(ctx context.Context, key string) (bool, error)
	// ListObjects 列出 prefix 下所有对象的相对 key（已去掉 prefix）与大小，供 workspace 浏览。
	// prefix 末尾应带 "/"，返回结果中相对 key 同样以 "/" 分隔，直接反映 S3 对象层级。
	ListObjects(ctx context.Context, prefix string) ([]ObjectInfo, error)
	// MovePrefix 把 srcPrefix 下所有对象复制到 dstPrefix 再删除源（移至归档前缀用（删除前归档））。
	MovePrefix(ctx context.Context, srcPrefix, dstPrefix string) error
	// DeletePrefix 删除 prefix 下所有对象。
	DeletePrefix(ctx context.Context, prefix string) error
}
