package service

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"oc-manager/internal/integrations/storage"
)

// fakeObjectStore 是 storage.ObjectStore 的内存假实现，记录写入并支持预签名→内容回放。
type fakeObjectStore struct {
	put       map[string][]byte
	presigned string
}

func newFakeObjectStore() *fakeObjectStore { return &fakeObjectStore{put: map[string][]byte{}} }

func (f *fakeObjectStore) PutObject(_ context.Context, key string, r io.Reader, _ int64) error {
	b, _ := io.ReadAll(r)
	f.put[key] = b
	return nil
}
func (f *fakeObjectStore) PresignGet(_ context.Context, key string, _ time.Duration) (string, error) {
	f.presigned = key
	return "https://presigned.example/" + key, nil
}
func (f *fakeObjectStore) ObjectExists(_ context.Context, key string) (bool, error) {
	_, ok := f.put[key]
	return ok, nil
}
func (f *fakeObjectStore) ListObjects(_ context.Context, _ string) ([]storage.ObjectInfo, error) {
	return nil, nil
}
func (f *fakeObjectStore) MovePrefix(_ context.Context, _, _ string) error { return nil }

// DeletePrefix 按前缀匹配删除，贴近真实 S3ObjectStore 的前缀删语义（而非精确 key 删）。
func (f *fakeObjectStore) DeletePrefix(_ context.Context, p string) error {
	for k := range f.put {
		if strings.HasPrefix(k, p) {
			delete(f.put, k)
		}
	}
	return nil
}

// TestS3SkillBlobStorePutKeyLayout 验证 PutSkill 用 versions/<vid>/skills/<name>.tar 布局。
func TestS3SkillBlobStorePutKeyLayout(t *testing.T) {
	obj := newFakeObjectStore()
	store := NewS3SkillBlobStore(obj, time.Minute)
	// 正常路径：返回的 relPath 即 S3 key，落在 version 维度
	rel, err := store.PutSkill("v1", "weather", []byte("tar-bytes"))
	require.NoError(t, err)
	assert.Equal(t, "versions/v1/skills/weather.tar", rel)
	assert.Equal(t, []byte("tar-bytes"), obj.put["versions/v1/skills/weather.tar"])
}

// TestS3SkillBlobStoreRejectsUnsafeSegment 验证非法版本/技能名被拒（防注入路径段）。
func TestS3SkillBlobStoreRejectsUnsafeSegment(t *testing.T) {
	store := NewS3SkillBlobStore(newFakeObjectStore(), time.Minute)
	// 含分隔符的技能名必须被 safeSegment 拒绝
	_, err := store.PutSkill("v1", "a/b", []byte("x"))
	require.Error(t, err)
	// 含分隔符的版本号同样必须被拒绝（覆盖 versionID 校验路径）
	_, err = store.PutSkill("v/x", "weather", []byte("x"))
	require.Error(t, err)
}

// TestS3SkillBlobStoreOpenSkillReadsViaPresign 验证 OpenSkill 经预签名 URL 读回内容。
func TestS3SkillBlobStoreOpenSkillReadsViaPresign(t *testing.T) {
	obj := newFakeObjectStore()
	store := NewS3SkillBlobStore(obj, time.Minute)
	// 替换包级 httpGet，按预签名 key 回放假内容，避免真实网络
	orig := httpGet
	defer func() { httpGet = orig }()
	httpGet = func(url string) (io.ReadCloser, error) {
		return io.NopCloser(strings.NewReader("downloaded:" + url)), nil
	}
	rc, err := store.OpenSkill("versions/v1/skills/weather.tar")
	require.NoError(t, err)
	defer rc.Close()
	body, _ := io.ReadAll(rc)
	// 预签名 key 应为传入 relPath，下载内容来自该 URL
	assert.Equal(t, "versions/v1/skills/weather.tar", obj.presigned)
	assert.Equal(t, "downloaded:https://presigned.example/versions/v1/skills/weather.tar", string(body))
}
