// store.go 定义本包的两个核心抽象接口与关联类型：
// - ObjectStore：S3 对象读写（上传、预签名、存在性检测、前缀搬运/删除）
// - STSIssuer：STS AssumeRole 签发限定到 app prefix 的临时读写凭证
// 具体实现位于 s3.go（基于 aws-sdk-go-v2），本文件不引入任何 SDK 依赖。
package storage

import (
	"context"
	"io"
	"time"
)

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
	// MovePrefix 把 srcPrefix 下所有对象复制到 dstPrefix 再删除源（删除归档用）。
	MovePrefix(ctx context.Context, srcPrefix, dstPrefix string) error
	// DeletePrefix 删除 prefix 下所有对象。
	DeletePrefix(ctx context.Context, prefix string) error
}

// TempCredentials 是 STS AssumeRole 签发的临时写凭证（标准 S3 协议字段）。
type TempCredentials struct {
	AccessKeyID     string    // 临时 access key
	SecretAccessKey string    // 临时 secret
	SessionToken    string    // 会话 token（标准 S3 临时凭证必需）
	ExpiresAt       time.Time // 过期时间；pod 须在此之前重调 bootstrap 续期
}

// STSIssuer 用标准 STS AssumeRole 签发限定到 app prefix 的临时写凭证。
type STSIssuer interface {
	// AssumeAppRole 签发临时写凭证，授予对 appPrefix（如 "apps/<id>/"）下对象的读写权限（用于 sidecar 同步），ttl 为有效期。
	AssumeAppRole(ctx context.Context, appPrefix string, ttl time.Duration) (TempCredentials, error)
}
