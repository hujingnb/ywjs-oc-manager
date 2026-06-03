package service

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"time"

	"oc-manager/internal/integrations/storage"
)

// s3LibraryPresignTTL 是读取 skill 库归档预签名 URL 的默认有效期，与 S3SkillBlobStore 保持一致。
const s3LibraryPresignTTL = 15 * time.Minute

// S3LibraryBlobStore 把 skill 库归档存到 S3 兼容对象存储。
// 与 FSLibraryBlobStore 实现同一 LibraryBlobStore 接口，生产环境通过配置选择。
type S3LibraryBlobStore struct {
	objects storage.ObjectStore
	ttl     time.Duration // 预签名 GET URL 有效期
}

// NewS3LibraryBlobStore 构造 S3 实现；objects 与 S3SkillBlobStore 共用同一 ObjectStore 实例。
func NewS3LibraryBlobStore(objects storage.ObjectStore) *S3LibraryBlobStore {
	return &S3LibraryBlobStore{objects: objects, ttl: s3LibraryPresignTTL}
}

// PutLibrarySkill 上传 skill 库归档，返回以 "/" 分隔的相对 key（= S3 对象键）。
// key 格式：library/<source>/<sourceRef>/<version>.<ext>，由 storage.LibrarySkillKey 生成。
func (s *S3LibraryBlobStore) PutLibrarySkill(source, sourceRef, version, ext string, data []byte) (string, error) {
	key := storage.LibrarySkillKey(source, sourceRef, version, ext)
	if err := s.objects.PutObject(context.Background(), key, bytes.NewReader(data), int64(len(data))); err != nil {
		return "", fmt.Errorf("上传 skill 库归档失败: %w", err)
	}
	return key, nil
}

// DeleteLibrarySkill 按相对 key 删除 skill 库归档（DeletePrefix 幂等，key 即 prefix）。
// 调用 DeletePrefix 而非单对象删除，与 S3SkillBlobStore 的删除方式一致。
func (s *S3LibraryBlobStore) DeleteLibrarySkill(relPath string) error {
	if err := s.objects.DeletePrefix(context.Background(), relPath); err != nil {
		return fmt.Errorf("删除 skill 库归档失败: %w", err)
	}
	return nil
}

// OpenLibrarySkill 通过预签名 GET URL + HTTP 下载打开 skill 库归档。
// 复用 httpGet（与 S3SkillBlobStore.OpenSkill 同一包级变量），便于单测注入替换。
func (s *S3LibraryBlobStore) OpenLibrarySkill(relPath string) (io.ReadCloser, error) {
	url, err := s.objects.PresignGet(context.Background(), relPath, s.ttl)
	if err != nil {
		return nil, fmt.Errorf("预签名 skill 库归档失败: %w", err)
	}
	rc, err := httpGet(url)
	if err != nil {
		return nil, fmt.Errorf("下载 skill 库归档失败: %w", err)
	}
	return rc, nil
}

// 编译时断言：S3LibraryBlobStore 实现 LibraryBlobStore 接口。
var _ LibraryBlobStore = (*S3LibraryBlobStore)(nil)
