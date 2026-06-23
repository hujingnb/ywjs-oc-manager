package service

// 知识库分片上传的会话状态存储。
//
// 线上 manager 多副本，分片请求经负载均衡会落到不同 pod，所以「uploadId → S3 UploadId / 已收
// 分片 ETag」这类跨请求状态必须放共享存储（Redis），不能放进程内存。会话用两个 key：
//   - meta（string）：会话元信息 JSON（作用域 / 归属 / 文件名 / 大小 / 暂存 key / S3 UploadId）
//   - parts（hash）：已上传分片，field=分片序号 value=ETag，HSET 逐片原子写入，避免读改写竞态
// 两者都带 TTL，complete/abort 时显式删除；未完成的会话靠 TTL 自动过期回收。

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	redis "github.com/redis/go-redis/v9"

	"oc-manager/internal/integrations/storage"
)

// ErrKnowledgeUploadSessionNotFound 表示分片上传会话不存在或已过期。
var ErrKnowledgeUploadSessionNotFound = errors.New("knowledge upload session not found")

// knowledgeUploadSession 是一次分片上传会话的元信息（不含已上传分片列表，分片单独存 hash）。
type knowledgeUploadSession struct {
	Scope      string `json:"scope"`       // "org" 或 "app"
	OrgID      string `json:"org_id"`      // org 作用域归属组织；app 作用域为空
	AppID      string `json:"app_id"`      // app 作用域归属应用；org 作用域为空
	Filename   string `json:"filename"`    // 原始文件名（已取 base）
	Size       int64  `json:"size"`        // 客户端声明的文件总字节数，用于配额预校验
	Key        string `json:"key"`         // 对象存储暂存 key（kb-uploads/<uploadID>/<filename>）
	S3UploadID string `json:"s3_upload_id"` // 对象存储 multipart 的 UploadId
}

// knowledgeUploadSessions 是 service 依赖的会话存储抽象，便于测试替换为内存实现。
type knowledgeUploadSessions interface {
	// Create 写入会话元信息并设置 TTL。
	Create(ctx context.Context, uploadID string, sess knowledgeUploadSession) error
	// Get 读取会话元信息；不存在返回 ErrKnowledgeUploadSessionNotFound。
	Get(ctx context.Context, uploadID string) (knowledgeUploadSession, error)
	// PutPart 记录一个已上传分片的 ETag（HSET 原子写），并刷新 TTL。
	PutPart(ctx context.Context, uploadID string, partNumber int32, etag string) error
	// ListParts 返回会话已上传的全部分片（未排序，由合并方排序）。
	ListParts(ctx context.Context, uploadID string) ([]storage.MultipartPart, error)
	// Delete 删除会话元信息与分片记录。
	Delete(ctx context.Context, uploadID string) error
}

// RedisKnowledgeUploadSessions 用 Redis 实现 knowledgeUploadSessions。
type RedisKnowledgeUploadSessions struct {
	client redis.Cmdable
	prefix string        // 业务 key 前缀，复用 cfg.Redis.KeyPrefix（如 "ocm:"）
	ttl    time.Duration // 会话存活时长，超时未完成自动回收
}

// NewRedisKnowledgeUploadSessions 构造 Redis 会话存储；ttl<=0 时回退 24h。
func NewRedisKnowledgeUploadSessions(client redis.Cmdable, keyPrefix string, ttl time.Duration) *RedisKnowledgeUploadSessions {
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	return &RedisKnowledgeUploadSessions{client: client, prefix: keyPrefix, ttl: ttl}
}

// 编译时断言：实现 knowledgeUploadSessions 接口。
var _ knowledgeUploadSessions = (*RedisKnowledgeUploadSessions)(nil)

// metaKey / partsKey 拼接会话的两个 Redis key。
func (r *RedisKnowledgeUploadSessions) metaKey(uploadID string) string {
	return r.prefix + "kbupload:" + uploadID + ":meta"
}
func (r *RedisKnowledgeUploadSessions) partsKey(uploadID string) string {
	return r.prefix + "kbupload:" + uploadID + ":parts"
}

// Create 把会话元信息以 JSON 写入 meta key 并设 TTL。
func (r *RedisKnowledgeUploadSessions) Create(ctx context.Context, uploadID string, sess knowledgeUploadSession) error {
	raw, err := json.Marshal(sess)
	if err != nil {
		return fmt.Errorf("序列化分片上传会话失败: %w", err)
	}
	if err := r.client.Set(ctx, r.metaKey(uploadID), raw, r.ttl).Err(); err != nil {
		return fmt.Errorf("写入分片上传会话失败: %w", err)
	}
	return nil
}

// Get 读取并反序列化会话元信息。
func (r *RedisKnowledgeUploadSessions) Get(ctx context.Context, uploadID string) (knowledgeUploadSession, error) {
	raw, err := r.client.Get(ctx, r.metaKey(uploadID)).Bytes()
	if errors.Is(err, redis.Nil) {
		return knowledgeUploadSession{}, ErrKnowledgeUploadSessionNotFound
	}
	if err != nil {
		return knowledgeUploadSession{}, fmt.Errorf("读取分片上传会话失败: %w", err)
	}
	var sess knowledgeUploadSession
	if err := json.Unmarshal(raw, &sess); err != nil {
		return knowledgeUploadSession{}, fmt.Errorf("解析分片上传会话失败: %w", err)
	}
	return sess, nil
}

// PutPart 原子写入分片 ETag 并刷新 meta/parts 的 TTL（防止慢速大文件上传中途过期）。
func (r *RedisKnowledgeUploadSessions) PutPart(ctx context.Context, uploadID string, partNumber int32, etag string) error {
	pk := r.partsKey(uploadID)
	if err := r.client.HSet(ctx, pk, strconv.Itoa(int(partNumber)), etag).Err(); err != nil {
		return fmt.Errorf("记录分片 ETag 失败: %w", err)
	}
	// 续期：上传期间持续刷新两个 key 的 TTL，避免 155s+ 的大文件在传输途中过期。
	r.client.Expire(ctx, pk, r.ttl)
	r.client.Expire(ctx, r.metaKey(uploadID), r.ttl)
	return nil
}

// ListParts 读取分片 hash 并转为 MultipartPart 列表（序号解析失败的字段跳过）。
func (r *RedisKnowledgeUploadSessions) ListParts(ctx context.Context, uploadID string) ([]storage.MultipartPart, error) {
	m, err := r.client.HGetAll(ctx, r.partsKey(uploadID)).Result()
	if err != nil {
		return nil, fmt.Errorf("读取分片列表失败: %w", err)
	}
	parts := make([]storage.MultipartPart, 0, len(m))
	for field, etag := range m {
		pn, convErr := strconv.Atoi(field)
		if convErr != nil {
			continue
		}
		parts = append(parts, storage.MultipartPart{PartNumber: int32(pn), ETag: etag})
	}
	return parts, nil
}

// Delete 删除会话的 meta 与 parts 两个 key。
func (r *RedisKnowledgeUploadSessions) Delete(ctx context.Context, uploadID string) error {
	if err := r.client.Del(ctx, r.metaKey(uploadID), r.partsKey(uploadID)).Err(); err != nil {
		return fmt.Errorf("删除分片上传会话失败: %w", err)
	}
	return nil
}
