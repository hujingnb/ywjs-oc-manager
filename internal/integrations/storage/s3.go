package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	smithyhttp "github.com/aws/smithy-go/transport/http"
)

// S3Config 是构造 S3ObjectStore 所需的标准 S3 接入参数。
type S3Config struct {
	Endpoint        string // S3 端点，如 http://minio.oc-system.svc:9000；指向 MinIO 或云 OSS
	Region          string // 区域，MinIO 任意填（如 us-east-1）
	Bucket          string // app 数据 bucket
	AccessKeyID     string // manager 持有的长期凭证（用于 Put/Presign，并直发给 sidecar 写回）
	SecretAccessKey string // 与 AccessKeyID 配对的长期密钥
	UsePathStyle    bool   // MinIO 必须 path-style 寻址
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

// ListObjects 列出 prefix 下全部对象的相对 key（去掉 prefix）与大小（分页直到取完）。
// 相对 key = 完整对象 key 去掉传入的 prefix 前缀，调用方可据此还原文件层级。
func (s *S3ObjectStore) ListObjects(ctx context.Context, prefix string) ([]ObjectInfo, error) {
	var items []ObjectInfo
	var token *string
	for {
		out, err := s.client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket:            aws.String(s.bucket),
			Prefix:            aws.String(prefix),
			ContinuationToken: token,
		})
		if err != nil {
			return nil, fmt.Errorf("storage: 列举 %s 失败: %w", prefix, err)
		}
		for _, obj := range out.Contents {
			fullKey := aws.ToString(obj.Key)
			// 去掉前缀，保留相对路径
			relKey := fullKey[len(prefix):]
			size := int64(0)
			if obj.Size != nil {
				size = *obj.Size
			}
			items = append(items, ObjectInfo{Key: relKey, Size: size})
		}
		if aws.ToBool(out.IsTruncated) && out.NextContinuationToken != nil {
			token = out.NextContinuationToken
			continue
		}
		break
	}
	return items, nil
}

// listKeys 列出 prefix 下全部对象 key（分页直到取完）。
func (s *S3ObjectStore) listKeys(ctx context.Context, prefix string) ([]string, error) {
	var keys []string
	var token *string
	for {
		out, err := s.client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket:            aws.String(s.bucket),
			Prefix:            aws.String(prefix),
			ContinuationToken: token,
		})
		if err != nil {
			return nil, fmt.Errorf("storage: 列举 %s 失败: %w", prefix, err)
		}
		for _, obj := range out.Contents {
			keys = append(keys, aws.ToString(obj.Key))
		}
		if aws.ToBool(out.IsTruncated) && out.NextContinuationToken != nil {
			token = out.NextContinuationToken
			continue
		}
		break
	}
	return keys, nil
}

// DeletePrefix 删除 prefix 下全部对象；空前缀视为非法（防误删整桶）。
func (s *S3ObjectStore) DeletePrefix(ctx context.Context, prefix string) error {
	if prefix == "" {
		return fmt.Errorf("storage: DeletePrefix 拒绝空前缀")
	}
	keys, err := s.listKeys(ctx, prefix)
	if err != nil {
		return err
	}
	// excludePrefix 为空表示不跳过任何对象，直接全量删除
	return s.deleteKeysExcluding(ctx, keys, "")
}

// MovePrefix 把 srcPrefix 下对象逐个 CopyObject 到 dstPrefix 对应相对路径，再删除源。
// 用于删除 app 前把数据移入归档前缀（apps/<id>/* → apps/<id>/archive/*）。空前缀视为非法。
func (s *S3ObjectStore) MovePrefix(ctx context.Context, srcPrefix, dstPrefix string) error {
	if srcPrefix == "" || dstPrefix == "" {
		return fmt.Errorf("storage: MovePrefix 拒绝空前缀")
	}
	keys, err := s.listKeys(ctx, srcPrefix)
	if err != nil {
		return err
	}
	for _, k := range keys {
		rel := k[len(srcPrefix):] // 去掉源前缀，保留相对路径
		dstKey := dstPrefix + rel
		// CopySource 需 URL 编码（key 可能含空格/非 ASCII 的用户文件名），保留 / 分隔符
		copySource := (&url.URL{Path: s.bucket + "/" + k}).EscapedPath()
		if _, err := s.client.CopyObject(ctx, &s3.CopyObjectInput{
			Bucket:     aws.String(s.bucket),
			CopySource: aws.String(copySource),
			Key:        aws.String(dstKey),
		}); err != nil {
			return fmt.Errorf("storage: 复制 %s→%s 失败: %w", k, dstKey, err)
		}
	}
	// 复制完成后删源；复用 copy 阶段取到的 keys，避免二次 list（消除 TOCTOU 窗口）。
	// 若 dstPrefix 是 srcPrefix 的子目录（归档场景），删源时排除已归档目标
	return s.deleteKeysExcluding(ctx, keys, dstPrefix)
}

// deleteKeysExcluding 删除传入的 keys，但跳过落在 excludePrefix 内的（避免删掉刚归档的目标）。
// excludePrefix 为空时不跳过任何对象。直接复用调用方已取到的 keys，不再二次 list。
func (s *S3ObjectStore) deleteKeysExcluding(ctx context.Context, keys []string, excludePrefix string) error {
	for _, k := range keys {
		if excludePrefix != "" && len(k) >= len(excludePrefix) && k[:len(excludePrefix)] == excludePrefix {
			continue // 跳过归档目标自身
		}
		if _, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
			Bucket: aws.String(s.bucket),
			Key:    aws.String(k),
		}); err != nil {
			return fmt.Errorf("storage: 删除对象 %s 失败: %w", k, err)
		}
	}
	return nil
}
