package storage_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"oc-manager/internal/integrations/storage"
)

// TestS3MultipartRoundTrip 验证 multipart 分片上传全链路（真实 MinIO/S3）：
// CreateMultipartUpload → UploadPart×2 → CompleteMultipartUpload → OpenObject 读回校验 →
// DeleteObject 清理。覆盖「非末片 ≥5MB + 末片可小于 5MB」的分片大小边界。
func TestS3MultipartRoundTrip(t *testing.T) {
	cfg := minioCfgFromEnv(t)
	store := storage.NewS3ObjectStore(cfg)
	ctx := context.Background()

	// 固定前缀：key 与清理基于同一时间戳，避免两次 time.Now() 前缀不一致导致清理失败
	prefix := fmt.Sprintf("kb-uploads/it-%d/", time.Now().UnixNano())
	key := prefix + "probe.bin"

	// 注册清理：断言失败也删测试对象，防止残留
	t.Cleanup(func() { _ = store.DeletePrefix(context.Background(), prefix) })

	// 6MB 非末片（需 ≥5MB）+ 1MB 末片（末片允许 <5MB）
	part1 := make([]byte, 6*1024*1024)
	part2 := make([]byte, 1*1024*1024)
	_, _ = rand.Read(part1)
	_, _ = rand.Read(part2)

	uploadID, err := store.CreateMultipartUpload(ctx, key)
	require.NoError(t, err)
	require.NotEmpty(t, uploadID)

	etag1, err := store.UploadPart(ctx, key, uploadID, 1, bytes.NewReader(part1), int64(len(part1)))
	require.NoError(t, err)
	etag2, err := store.UploadPart(ctx, key, uploadID, 2, bytes.NewReader(part2), int64(len(part2)))
	require.NoError(t, err)

	// 故意乱序传入，验证 CompleteMultipartUpload 内部会按 PartNumber 升序排序
	require.NoError(t, store.CompleteMultipartUpload(ctx, key, uploadID, []storage.MultipartPart{
		{PartNumber: 2, ETag: etag2},
		{PartNumber: 1, ETag: etag1},
	}))

	// 读回校验：大小 = 两片之和，内容 = part1||part2
	reader, size, err := store.OpenObject(ctx, key)
	require.NoError(t, err)
	defer reader.Close()
	assert.Equal(t, int64(len(part1)+len(part2)), size)
	got, err := io.ReadAll(reader)
	require.NoError(t, err)
	assert.Equal(t, append(append([]byte(nil), part1...), part2...), got)

	require.NoError(t, store.DeleteObject(ctx, key))
}

// TestS3MultipartAbort 验证发起分片上传后可正常中止（会话取消/失败路径）。
func TestS3MultipartAbort(t *testing.T) {
	cfg := minioCfgFromEnv(t)
	store := storage.NewS3ObjectStore(cfg)
	ctx := context.Background()

	key := fmt.Sprintf("kb-uploads/it-abort-%d/probe.bin", time.Now().UnixNano())
	uploadID, err := store.CreateMultipartUpload(ctx, key)
	require.NoError(t, err)

	// 传一片但不 Complete，直接 Abort——应成功且不留下可读对象
	part := make([]byte, 5*1024*1024)
	_, _ = rand.Read(part)
	_, err = store.UploadPart(ctx, key, uploadID, 1, bytes.NewReader(part), int64(len(part)))
	require.NoError(t, err)

	require.NoError(t, store.AbortMultipartUpload(ctx, key, uploadID))

	// 中止后对象不应存在
	exists, err := store.ObjectExists(ctx, key)
	require.NoError(t, err)
	assert.False(t, exists)
}
