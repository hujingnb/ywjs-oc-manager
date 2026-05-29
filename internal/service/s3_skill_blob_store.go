package service

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"oc-manager/internal/integrations/storage"
)

// defaultHTTPClient 用于下载预签名 URL 的对象；超时保护避免长挂。
var defaultHTTPClient = &http.Client{Timeout: 60 * time.Second}

// httpGet 是包内可替换的 HTTP GET，返回响应体 ReadCloser；非 2xx 视为错误。
// 抽成变量便于单测注入假实现，避免真发网络请求。
var httpGet = func(url string) (io.ReadCloser, error) {
	resp, err := defaultHTTPClient.Get(url)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		resp.Body.Close()
		return nil, fmt.Errorf("下载 skill 返回状态 %d", resp.StatusCode)
	}
	return resp.Body, nil
}

// SkillPresigner 暴露 skill tar 的预签名读 URL 能力（bootstrap 给 pod 下载用）。
type SkillPresigner interface {
	PresignSkill(ctx context.Context, relPath string, ttl time.Duration) (string, error)
}

// S3SkillBlobStore 用对象存储承载 skill tar 主副本，relPath 即 S3 key
// （布局 versions/<vid>/skills/<name>.tar，与 FSSkillBlobStore 一致，file_path 列语义平滑迁移）。
type S3SkillBlobStore struct {
	objects storage.ObjectStore
	ttl     time.Duration // 预签名默认有效期
}

// NewS3SkillBlobStore 构造基于 S3 的 skill 主副本存储。
func NewS3SkillBlobStore(objects storage.ObjectStore, ttl time.Duration) *S3SkillBlobStore {
	return &S3SkillBlobStore{objects: objects, ttl: ttl}
}

// PutSkill 上传 skill tar，返回相对路径（= S3 key）。
func (s *S3SkillBlobStore) PutSkill(versionID, skillName string, data []byte) (string, error) {
	if err := safeSegment(versionID); err != nil {
		return "", err
	}
	if err := safeSegment(skillName); err != nil {
		return "", err
	}
	key := storage.SkillKey(versionID, skillName)
	if err := s.objects.PutObject(context.Background(), key, bytes.NewReader(data), int64(len(data))); err != nil {
		return "", fmt.Errorf("上传 skill tar 失败: %w", err)
	}
	return key, nil
}

// DeleteSkill 删除 skill tar（按单对象前缀删，幂等）。
func (s *S3SkillBlobStore) DeleteSkill(relPath string) error {
	if err := s.objects.DeletePrefix(context.Background(), relPath); err != nil {
		return fmt.Errorf("删除 skill tar 失败: %w", err)
	}
	return nil
}

// OpenSkill 从 S3 读 skill tar，满足 worker 的 SkillBlobReader（旧推送路径仍用）。
// 用预签名 URL + HTTP GET 读取，避免给该接口再加流式读对象的方法。
func (s *S3SkillBlobStore) OpenSkill(relPath string) (io.ReadCloser, error) {
	url, err := s.objects.PresignGet(context.Background(), relPath, s.ttl)
	if err != nil {
		return nil, fmt.Errorf("预签名 skill 失败: %w", err)
	}
	resp, err := httpGet(url)
	if err != nil {
		return nil, fmt.Errorf("下载 skill tar 失败: %w", err)
	}
	return resp, nil
}

// PresignSkill 生成 skill tar 的预签名读 URL（bootstrap 用）。
func (s *S3SkillBlobStore) PresignSkill(ctx context.Context, relPath string, ttl time.Duration) (string, error) {
	return s.objects.PresignGet(ctx, relPath, ttl)
}

// 编译时断言：实现 SkillBlobStore（PutSkill/DeleteSkill）与 SkillPresigner。
var _ SkillBlobStore = (*S3SkillBlobStore)(nil)
var _ SkillPresigner = (*S3SkillBlobStore)(nil)
