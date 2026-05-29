package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	smithyhttp "github.com/aws/smithy-go/transport/http"
)

// S3Config 是构造 S3ObjectStore / STSCredentialIssuer 所需的标准 S3 接入参数。
type S3Config struct {
	Endpoint        string // S3 端点，如 http://minio.oc-system.svc:9000；指向 MinIO 或云 OSS
	Region          string // 区域，MinIO 任意填（如 us-east-1）
	Bucket          string // app 数据 bucket
	AccessKeyID     string // manager 持有的长期凭证（用于 Put/Presign/STS 调用方）
	SecretAccessKey string
	UsePathStyle    bool // MinIO 必须 path-style 寻址
	// STSRoleARN 是 AssumeRole 的目标 role ARN；MinIO 下可为占位（策略由内联 policy 决定）。
	STSRoleARN string
}

// S3ObjectStore 用 aws-sdk-go-v2 标准 S3 客户端实现 ObjectStore。
type S3ObjectStore struct {
	client *s3.Client
	bucket string
}

// NewS3ObjectStore 用静态凭证构造标准 S3 客户端（BaseEndpoint + path-style，兼容 MinIO 与云 OSS）。
func NewS3ObjectStore(cfg S3Config) *S3ObjectStore {
	client := s3.New(s3.Options{
		BaseEndpoint: aws.String(cfg.Endpoint),
		Region:       cfg.Region,
		UsePathStyle: cfg.UsePathStyle,
		Credentials: credentials.NewStaticCredentialsProvider(
			cfg.AccessKeyID, cfg.SecretAccessKey, ""),
	})
	return &S3ObjectStore{client: client, bucket: cfg.Bucket}
}

// 编译时断言：S3ObjectStore 实现 ObjectStore 接口。
// MovePrefix/DeletePrefix 留待 Task 3 实现，此处先用 panic 占位。
var _ ObjectStore = (*S3ObjectStore)(nil)

// PutObject 上传对象；size>=0 时填 ContentLength，<0 时交由 SDK 处理（会缓冲）。
func (s *S3ObjectStore) PutObject(ctx context.Context, key string, r io.Reader, size int64) error {
	in := &s3.PutObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
		Body:   r,
	}
	if size >= 0 {
		in.ContentLength = aws.Int64(size)
	}
	if _, err := s.client.PutObject(ctx, in); err != nil {
		return fmt.Errorf("storage: 上传对象 %s 失败: %w", key, err)
	}
	return nil
}

// PresignGet 生成预签名 GET URL；离线签名，不校验对象是否存在。
func (s *S3ObjectStore) PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error) {
	presign := s3.NewPresignClient(s.client)
	req, err := presign.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	}, s3.WithPresignExpires(ttl))
	if err != nil {
		return "", fmt.Errorf("storage: 预签名 %s 失败: %w", key, err)
	}
	return req.URL, nil
}

// ObjectExists 用 HeadObject 判断对象存在；404 归为不存在（非错误）。
func (s *S3ObjectStore) ObjectExists(ctx context.Context, key string) (bool, error) {
	_, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err == nil {
		return true, nil
	}
	// 标准 S3：对象不存在返回 404 NotFound / NoSuchKey；其它错误透出
	var notFound *types.NotFound
	if errors.As(err, &notFound) {
		return false, nil
	}
	var respErr *smithyhttp.ResponseError
	if errors.As(err, &respErr) && respErr.HTTPStatusCode() == 404 {
		return false, nil
	}
	return false, fmt.Errorf("storage: HeadObject %s 失败: %w", key, err)
}

// MovePrefix 把 srcPrefix 下所有对象复制到 dstPrefix 再删除源（删除归档用）。
// 留待 Task 3 实现。
func (s *S3ObjectStore) MovePrefix(_ context.Context, _, _ string) error {
	panic("storage: MovePrefix 未实现，留待 Task 3")
}

// DeletePrefix 删除 prefix 下所有对象。
// 留待 Task 3 实现。
func (s *S3ObjectStore) DeletePrefix(_ context.Context, _ string) error {
	panic("storage: DeletePrefix 未实现，留待 Task 3")
}
