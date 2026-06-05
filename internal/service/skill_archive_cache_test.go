package service

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"oc-manager/internal/integrations/storage"
)

// archiveCacheCounter 包一个回源计数器，便于断言「命中时不回源」。
type archiveCacheCounter struct {
	calls int    // fetch 闭包被调用次数
	data  []byte // 回源返回的字节
	err   error  // 回源返回的错误（非 nil 时模拟上游失败）
}

func (c *archiveCacheCounter) fetch(_ context.Context) ([]byte, error) {
	c.calls++
	if c.err != nil {
		return nil, c.err
	}
	return c.data, nil
}

// TestSkillArchiveCache_Miss_FetchesAndWrites 未命中：回源一次、写回缓存、返回字节与确定性相对路径。
func TestSkillArchiveCache_Miss_FetchesAndWrites(t *testing.T) {
	blobs := &fakeLibraryBlob{}
	cache := NewSkillArchiveCache(blobs)
	up := &archiveCacheCounter{data: []byte("ZIP")}

	data, rel, err := cache.Fetch(context.Background(), "clawhub", "weather", "1.0", "zip", up.fetch)
	require.NoError(t, err)
	assert.Equal(t, []byte("ZIP"), data)
	// 相对路径为 library/<source>/<ref>/<version>.<ext>
	assert.Equal(t, "library/clawhub/weather/1.0.zip", rel)
	// 未命中应回源一次
	assert.Equal(t, 1, up.calls)
	// 已写回缓存（fakeLibraryBlob.stored 收到对应 key）
	assert.Equal(t, []byte("ZIP"), blobs.stored["library/clawhub/weather/1.0.zip"])
}

// TestSkillArchiveCache_Hit_SkipsUpstream 命中：直接返回缓存字节，绝不回源。
func TestSkillArchiveCache_Hit_SkipsUpstream(t *testing.T) {
	blobs := &fakeLibraryBlob{stored: map[string][]byte{
		"library/clawhub/weather/1.0.zip": []byte("CACHED"),
	}}
	cache := NewSkillArchiveCache(blobs)
	// 回源闭包预置错误：一旦被调用即说明没命中缓存，测试应失败。
	up := &archiveCacheCounter{err: errors.New("上游不该被调用")}

	data, rel, err := cache.Fetch(context.Background(), "clawhub", "weather", "1.0", "zip", up.fetch)
	require.NoError(t, err)
	assert.Equal(t, []byte("CACHED"), data)
	assert.Equal(t, "library/clawhub/weather/1.0.zip", rel)
	// 命中缓存 → 回源 0 次
	assert.Equal(t, 0, up.calls)
}

// TestSkillArchiveCache_UpstreamError_NoWrite 未命中且回源失败：原样返回错误、不写缓存。
func TestSkillArchiveCache_UpstreamError_NoWrite(t *testing.T) {
	blobs := &fakeLibraryBlob{}
	cache := NewSkillArchiveCache(blobs)
	sentinel := errors.New("上游 502")
	up := &archiveCacheCounter{err: sentinel}

	_, _, err := cache.Fetch(context.Background(), "clawhub", "x", "1.0", "zip", up.fetch)
	require.ErrorIs(t, err, sentinel)
	// 回源失败不写缓存
	assert.Nil(t, blobs.stored["library/clawhub/x/1.0.zip"])
}

// nilReturningBlob 是一个「无对象时返回 (nil, nil)」的 LibraryBlobStore 替身，
// 用于验证 Fetch 对此类实现的 rc!=nil 守卫——不 panic、按未命中回源。
type nilReturningBlob struct{ put map[string][]byte }

func (b *nilReturningBlob) PutLibrarySkill(source, ref, version, ext string, data []byte) (string, error) {
	if b.put == nil {
		b.put = map[string][]byte{}
	}
	// 使用 storage.LibrarySkillKey 而非手拼字符串，确保与生产键格式保持一致。
	rel := storage.LibrarySkillKey(source, ref, version, ext)
	b.put[rel] = data
	return rel, nil
}
func (b *nilReturningBlob) DeleteLibrarySkill(string) error { return nil }
func (b *nilReturningBlob) OpenLibrarySkill(string) (io.ReadCloser, error) {
	return nil, nil
} // 故意返回 (nil, nil)

// TestSkillArchiveCache_NilReadCloser_NoPanic OpenLibrarySkill 返回 (nil, nil) 时不 panic，按未命中回源并写回。
func TestSkillArchiveCache_NilReadCloser_NoPanic(t *testing.T) {
	blobs := &nilReturningBlob{}
	cache := NewSkillArchiveCache(blobs)
	up := &archiveCacheCounter{data: []byte("ZIP")}

	data, rel, err := cache.Fetch(context.Background(), "clawhub", "weather", "1.0", "zip", up.fetch)
	require.NoError(t, err)
	assert.Equal(t, []byte("ZIP"), data)
	assert.Equal(t, "library/clawhub/weather/1.0.zip", rel)
	// (nil, nil) 被当作未命中 → 回源一次并写回。
	assert.Equal(t, 1, up.calls)
	assert.Equal(t, []byte("ZIP"), blobs.put["library/clawhub/weather/1.0.zip"])
}

// putFailingBlob 是一个写回必失败、读永远未命中的 LibraryBlobStore 替身，
// 用于验证 Fetch 在写缓存失败时仍以「回源到的字节 + 确定性 key」非致命返回。
type putFailingBlob struct{}

func (putFailingBlob) PutLibrarySkill(_, _, _, _ string, _ []byte) (string, error) {
	return "", errors.New("写对象存储失败")
}
func (putFailingBlob) DeleteLibrarySkill(string) error { return nil }
func (putFailingBlob) OpenLibrarySkill(string) (io.ReadCloser, error) {
	return nil, errors.New("blob not found")
}

// TestSkillArchiveCache_WriteFailure_NonFatal 写回缓存失败时不致命：仍返回回源字节与确定性 key、err 为 nil。
func TestSkillArchiveCache_WriteFailure_NonFatal(t *testing.T) {
	cache := NewSkillArchiveCache(putFailingBlob{})
	up := &archiveCacheCounter{data: []byte("ZIP")}

	data, rel, err := cache.Fetch(context.Background(), "clawhub", "weather", "1.0", "zip", up.fetch)
	require.NoError(t, err)                                  // 写失败不致命
	assert.Equal(t, []byte("ZIP"), data)                    // 仍返回回源字节
	assert.Equal(t, "library/clawhub/weather/1.0.zip", rel) // 确定性 key
	assert.Equal(t, 1, up.calls)                            // 回源一次
}

// TestSkillArchiveCache_FetchAndPersist_WriteFailure_Fatal 持久化场景下写回缓存失败必须上报错误，
// 避免留下指向空对象的 relPath（下游 bootstrap 下发 / Reinstall 强依赖该对象存在）。
func TestSkillArchiveCache_FetchAndPersist_WriteFailure_Fatal(t *testing.T) {
	cache := NewSkillArchiveCache(putFailingBlob{})
	up := &archiveCacheCounter{data: []byte("ZIP")}

	_, _, err := cache.FetchAndPersist(context.Background(), "clawhub", "weather", "1.0", "zip", up.fetch)
	require.Error(t, err)        // 写失败必须上报
	assert.Equal(t, 1, up.calls) // 已回源（写失败发生在回源之后）
}
