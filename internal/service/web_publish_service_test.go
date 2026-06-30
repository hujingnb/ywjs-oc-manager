// Package service - WebPublishService 核心发布逻辑单元测试。
// 覆盖发布（每次新建随机站点）、企业未开通、站点配额超限等核心分支。
package service

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	null "github.com/guregu/null/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"oc-manager/internal/domain"
	"oc-manager/internal/store/sqlc"
)

// fakeWPubStore 实现 WebPublishStore 接口，供测试注入受控数据。
type fakeWPubStore struct {
	// appByHash 按 token hash 返回的 App；若 key 不存在则返回 sql.ErrNoRows。
	appByHash map[string]sqlc.App
	// publishConfig 按 orgID 返回的 OrgWebPublishConfig。
	publishConfig map[string]sqlc.OrgWebPublishConfig
	// siteByHost 按 host 返回的 PublishedSite；若 key 不存在则返回 sql.ErrNoRows。
	siteByHost map[string]sqlc.PublishedSite
	// activeSiteCount 按 orgID 返回活跃站点数量。
	activeSiteCount map[string]int64
	// createdSites 记录 CreatePublishedSite 的调用参数列表。
	createdSites []sqlc.CreatePublishedSiteParams
}

func (f *fakeWPubStore) GetAppByRuntimeTokenHash(ctx context.Context, hash null.String) (sqlc.App, error) {
	// hash.String 是经 HashAppRuntimeToken 处理后的 hex 字符串。
	app, ok := f.appByHash[hash.String]
	if !ok {
		return sqlc.App{}, sql.ErrNoRows
	}
	return app, nil
}

func (f *fakeWPubStore) GetWebPublishConfig(ctx context.Context, orgID string) (sqlc.OrgWebPublishConfig, error) {
	cfg, ok := f.publishConfig[orgID]
	if !ok {
		return sqlc.OrgWebPublishConfig{}, sql.ErrNoRows
	}
	return cfg, nil
}

func (f *fakeWPubStore) GetPublishedSiteByHost(ctx context.Context, host string) (sqlc.PublishedSite, error) {
	site, ok := f.siteByHost[host]
	if !ok {
		return sqlc.PublishedSite{}, sql.ErrNoRows
	}
	return site, nil
}

func (f *fakeWPubStore) CountActiveSitesByOrg(ctx context.Context, orgID string) (int64, error) {
	return f.activeSiteCount[orgID], nil
}

func (f *fakeWPubStore) CreatePublishedSite(ctx context.Context, arg sqlc.CreatePublishedSiteParams) error {
	f.createdSites = append(f.createdSites, arg)
	return nil
}

// ListActiveSites 返回空列表；现有测试不覆盖同步端点，此方法仅满足接口约束。
func (f *fakeWPubStore) ListActiveSites(_ context.Context) ([]sqlc.ListActiveSitesRow, error) {
	return nil, nil
}

// fakeObjStore 实现 publishObjectStore 接口，记录上传的对象与删除的前缀。
type fakeObjStore struct {
	// objects 记录已上传的对象 key → 内容。
	objects map[string][]byte
	// deletedPrefixes 记录 DeletePrefix 调用的前缀列表。
	deletedPrefixes []string
}

func newFakeObjStore() *fakeObjStore {
	return &fakeObjStore{objects: make(map[string][]byte)}
}

func (f *fakeObjStore) PutObject(ctx context.Context, key string, r io.Reader, size int64) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	f.objects[key] = data
	return nil
}

func (f *fakeObjStore) DeletePrefix(ctx context.Context, prefix string) error {
	f.deletedPrefixes = append(f.deletedPrefixes, prefix)
	return nil
}

// seekableAssertObjStore 在 PutObject 时断言 body 必须实现 io.Seeker，且声明 size 等于实际内容长度。
// 这是对「AWS S3 SDK v2 签名需要可 seek body」契约的本地复现：真实 SDK 对不可 seek 的流会报
// "request stream is not seekable" 上传失败；用本 store 即可在单测层捕获该回归而无需真实 S3。
type seekableAssertObjStore struct {
	t       *testing.T
	objects map[string][]byte
}

func newSeekableAssertObjStore(t *testing.T) *seekableAssertObjStore {
	return &seekableAssertObjStore{t: t, objects: make(map[string][]byte)}
}

// PutObject 断言 r 可 seek（含可回到起点），并校验 size 与实际读到的字节数一致。
func (f *seekableAssertObjStore) PutObject(_ context.Context, key string, r io.Reader, size int64) error {
	// 必须可 seek，否则真实 AWS SDK 计算 payload hash 时会失败。
	seeker, ok := r.(io.Seeker)
	require.True(f.t, ok, "PutObject 的 body 必须实现 io.Seeker（S3 SDK 签名要求），key=%s", key)
	// 验证可回到起点（SDK 计算 hash 后会 seek 回 0 再发送 body）。
	_, err := seeker.Seek(0, io.SeekStart)
	require.NoError(f.t, err)
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	// 声明的 size 必须等于实际内容长度（修复前 totalSize/size 用可伪造的 hdr.Size）。
	require.Equal(f.t, int64(len(data)), size, "PutObject size 须等于实际内容长度，key=%s", key)
	f.objects[key] = data
	return nil
}

func (f *seekableAssertObjStore) DeletePrefix(_ context.Context, _ string) error { return nil }

// makeTarGz 构造一个内含指定文件的 tar.gz 归档字节，供测试使用。
func makeTarGz(t *testing.T, files map[string]string) io.Reader {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	for name, content := range files {
		body := []byte(content)
		hdr := &tar.Header{
			Name: name,
			Mode: 0644,
			Size: int64(len(body)),
			Typeflag: tar.TypeReg,
		}
		require.NoError(t, tw.WriteHeader(hdr))
		_, err := tw.Write(body)
		require.NoError(t, err)
	}
	require.NoError(t, tw.Close())
	require.NoError(t, gw.Close())
	return &buf
}

// 固定时间点，用于注入 Now 函数保证测试结果可预测。
var fixedNow = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// makeReadyCfg 构造一个已就绪的 OrgWebPublishConfig。
func makeReadyCfg(orgID string) sqlc.OrgWebPublishConfig {
	return sqlc.OrgWebPublishConfig{
		OrgID:              orgID,
		Enabled:            true,
		BaseDomain:         "example.com",
		SiteTtlDays:        7,
		MaxSites:           10,
		ProvisioningStatus: domain.ProvisioningReady,
	}
}

// TestPublishFirstTime 测试首次发布场景：无已有站点行 → 随机 slug → 创建新行，上传到 v1 前缀，返回 URL + 过期时间。
func TestPublishFirstTime(t *testing.T) {
	const orgID = "org-1"
	const appID = "app-1"
	const token = "test-token-first"

	store := &fakeWPubStore{
		appByHash: map[string]sqlc.App{
			HashAppRuntimeToken(token): {ID: appID, OrgID: orgID},
		},
		publishConfig: map[string]sqlc.OrgWebPublishConfig{
			orgID: makeReadyCfg(orgID),
		},
		// siteByHost 空：首次发布，host 尚不存在。
		siteByHost:      map[string]sqlc.PublishedSite{},
		activeSiteCount: map[string]int64{orgID: 0},
	}
	obj := newFakeObjStore()

	svc := NewWebPublishService(store, obj, WebPublishServiceConfig{
		// 注入固定 slug 生成函数，确保测试路径可预测。
		SlugGen: func() string { return "rand123" },
		// 注入固定时钟，确保 ExpiresAt 可断言。
		Now: func() time.Time { return fixedNow },
	})

	// 发布：服务端随机分配 slug。
	result, err := svc.Publish(context.Background(), token, makeTarGz(t, map[string]string{
		"index.html": "<html>hello</html>",
	}))
	require.NoError(t, err)

	// 返回 URL 应包含 slug.base_domain。
	assert.Equal(t, "https://rand123.example.com", result.URL)
	// ExpiresAt 应等于 now + 7 天。
	assert.Equal(t, fixedNow.Add(7*24*time.Hour), result.ExpiresAt)

	// 应调用一次 CreatePublishedSite。
	require.Len(t, store.createdSites, 1)

	created := store.createdSites[0]
	// 创建行的字段应与生成的 host/slug/version/prefix 一致。
	assert.Equal(t, "rand123.example.com", created.Host)
	assert.Equal(t, "rand123", created.Slug)
	assert.Equal(t, "v1", created.CurrentVersion)
	assert.True(t, strings.HasPrefix(created.S3Prefix, "published-sites/"))
	assert.True(t, strings.HasSuffix(created.S3Prefix, "/v1/"))
	assert.Equal(t, appID, created.AppID)
	assert.Equal(t, orgID, created.OrgID)

	// 对象存储中应有 index.html 的上传记录。
	var found bool
	for k := range obj.objects {
		if strings.HasSuffix(k, "index.html") {
			found = true
			break
		}
	}
	assert.True(t, found, "index.html 应已上传至对象存储")
}

// TestPublishAlwaysCreatesNewRandomSite 回归测试：每次发布都创建全新随机站点，不做原地更新。
// 连续两次发布应得到两个不同的随机 slug、两条独立的 CreatePublishedSite 记录（互不覆盖）。
func TestPublishAlwaysCreatesNewRandomSite(t *testing.T) {
	const orgID = "org-1"
	const appID = "app-1"
	const token = "test-token-new-each"

	store := &fakeWPubStore{
		appByHash:       map[string]sqlc.App{HashAppRuntimeToken(token): {ID: appID, OrgID: orgID}},
		publishConfig:   map[string]sqlc.OrgWebPublishConfig{orgID: makeReadyCfg(orgID)},
		siteByHost:      map[string]sqlc.PublishedSite{}, // 无任何已有站点
		activeSiteCount: map[string]int64{orgID: 0},
	}
	obj := newFakeObjStore()
	// SlugGen 每次返回不同值，模拟服务端随机分配。
	seq := 0
	svc := NewWebPublishService(store, obj, WebPublishServiceConfig{
		SlugGen: func() string { seq++; return fmt.Sprintf("rand%d", seq) },
		Now:     func() time.Time { return fixedNow },
	})

	// 第一次发布。
	r1, err := svc.Publish(context.Background(), token, makeTarGz(t, map[string]string{"index.html": "<html>v1</html>"}))
	require.NoError(t, err)
	// 第二次发布（相当于「改了内容再发」）。
	r2, err := svc.Publish(context.Background(), token, makeTarGz(t, map[string]string{"index.html": "<html>v2</html>"}))
	require.NoError(t, err)

	// 两次得到不同的随机地址，互不覆盖。
	assert.Equal(t, "https://rand1.example.com", r1.URL)
	assert.Equal(t, "https://rand2.example.com", r2.URL)
	assert.NotEqual(t, r1.URL, r2.URL, "每次发布都应是新的随机地址")
	// 两条独立的创建记录（没有走更新路径覆盖第一条）。
	require.Len(t, store.createdSites, 2)
	assert.Equal(t, "rand1", store.createdSites[0].Slug)
	assert.Equal(t, "rand2", store.createdSites[1].Slug)
}

// TestPublishUploadsSeekableBody 回归测试：unpackToPrefix 上传单个文件时，传给 PutObject 的 body
// 必须可 seek（AWS S3 SDK v2 签名要求），且声明 size 等于实际内容长度。修复前直接把不可 seek 的
// io.LimitReader 传给 PutObject，真实 S3 报 "request stream is not seekable" 导致任何站点发布失败。
func TestPublishUploadsSeekableBody(t *testing.T) {
	const orgID = "org-seek"
	const appID = "app-seek"
	const token = "test-token-seek"

	store := &fakeWPubStore{
		appByHash:       map[string]sqlc.App{HashAppRuntimeToken(token): {ID: appID, OrgID: orgID}},
		publishConfig:   map[string]sqlc.OrgWebPublishConfig{orgID: makeReadyCfg(orgID)},
		siteByHost:      map[string]sqlc.PublishedSite{},
		activeSiteCount: map[string]int64{orgID: 0},
	}
	// 用断言可 seek 的 store：若 service 传不可 seek 的 body，require 会立即 fail。
	obj := newSeekableAssertObjStore(t)
	svc := NewWebPublishService(store, obj, WebPublishServiceConfig{
		SlugGen: func() string { return "seek1" },
		Now:     func() time.Time { return fixedNow },
	})

	// 发布含两个文件的站点，覆盖多 entry 顺序上传。
	_, err := svc.Publish(context.Background(), token, makeTarGz(t, map[string]string{
		"index.html":     "<html>hello seekable</html>",
		"assets/app.css": "body{color:red}",
	}))
	require.NoError(t, err)

	// 两个文件都应上传成功，且内容与原文一致（间接验证 seek 回 0 后读到完整 body）。
	var idx, css string
	for k, v := range obj.objects {
		if strings.HasSuffix(k, "index.html") {
			idx = string(v)
		}
		if strings.HasSuffix(k, "assets/app.css") {
			css = string(v)
		}
	}
	assert.Equal(t, "<html>hello seekable</html>", idx)
	assert.Equal(t, "body{color:red}", css)
}

// TestPublishSkipsAbsolutePathEntry 测试归档含绝对路径 entry（如 /etc/passwd）时被跳过：
// 不产生双斜杠脏 key，正常普通文件仍被上传，发布成功。
func TestPublishSkipsAbsolutePathEntry(t *testing.T) {
	const orgID = "org-abs"
	const appID = "app-abs"
	const token = "test-token-abs"

	store := &fakeWPubStore{
		appByHash: map[string]sqlc.App{
			HashAppRuntimeToken(token): {ID: appID, OrgID: orgID},
		},
		publishConfig: map[string]sqlc.OrgWebPublishConfig{
			orgID: makeReadyCfg(orgID),
		},
		siteByHost:      map[string]sqlc.PublishedSite{},
		activeSiteCount: map[string]int64{orgID: 0},
	}
	obj := newFakeObjStore()

	svc := NewWebPublishService(store, obj, WebPublishServiceConfig{
		SlugGen: func() string { return "absslug" },
		Now:     func() time.Time { return fixedNow },
	})

	// 归档同时含绝对路径 entry 与正常文件，绝对路径应被跳过。
	_, err := svc.Publish(context.Background(), token, makeTarGz(t, map[string]string{
		"/etc/passwd": "root:x:0:0",
		"index.html":  "<html>ok</html>",
	}))
	require.NoError(t, err)

	// 不应存在任何以绝对路径拼接而成的脏 key（含 "//" 或以 "etc/passwd" 结尾）。
	for k := range obj.objects {
		assert.NotContains(t, k, "//", "不应出现双斜杠脏 key: %s", k)
		assert.NotContains(t, k, "etc/passwd", "绝对路径 entry 应被跳过: %s", k)
	}
	// 正常文件 index.html 应被上传。
	var found bool
	for k := range obj.objects {
		if strings.HasSuffix(k, "index.html") {
			found = true
		}
	}
	assert.True(t, found, "正常文件 index.html 应已上传")
}

// TestPublishNotProvisioned 测试企业未开通 web-publish 场景：ProvisioningStatus != ready → 拒绝发布。
func TestPublishNotProvisioned(t *testing.T) {
	const orgID = "org-unprovisioned"
	const token = "test-token-noprov"

	store := &fakeWPubStore{
		appByHash: map[string]sqlc.App{
			HashAppRuntimeToken(token): {ID: "app-x", OrgID: orgID},
		},
		publishConfig: map[string]sqlc.OrgWebPublishConfig{
			orgID: {
				OrgID:              orgID,
				Enabled:            true,
				BaseDomain:         "example.com",
				SiteTtlDays:        7,
				MaxSites:           10,
				// provisioning 中，尚未就绪。
				ProvisioningStatus: domain.ProvisioningInProgress,
			},
		},
		siteByHost:      map[string]sqlc.PublishedSite{},
		activeSiteCount: map[string]int64{orgID: 0},
	}
	obj := newFakeObjStore()

	svc := NewWebPublishService(store, obj, WebPublishServiceConfig{
		SlugGen: func() string { return "rnd" },
		Now:     func() time.Time { return fixedNow },
	})

	_, err := svc.Publish(context.Background(), token, makeTarGz(t, map[string]string{
		"index.html": "hi",
	}))
	// 应拒绝发布，并返回说明企业未开通的错误。
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrWebPublishNotProvisioned) || strings.Contains(err.Error(), "未开通"),
		"应返回企业未开通错误，got: %v", err)
}

// TestPublishQuotaExceeded 测试站点配额超限场景：当前活跃站点数已达 MaxSites → 拒绝新建站点。
func TestPublishQuotaExceeded(t *testing.T) {
	const orgID = "org-quota"
	const token = "test-token-quota"

	store := &fakeWPubStore{
		appByHash: map[string]sqlc.App{
			HashAppRuntimeToken(token): {ID: "app-q", OrgID: orgID},
		},
		publishConfig: map[string]sqlc.OrgWebPublishConfig{
			orgID: {
				OrgID:              orgID,
				Enabled:            true,
				BaseDomain:         "example.com",
				SiteTtlDays:        7,
				MaxSites:           3, // 最多 3 个站点。
				ProvisioningStatus: domain.ProvisioningReady,
			},
		},
		// host 不存在，触发新建流程，但配额已满。
		siteByHost:      map[string]sqlc.PublishedSite{},
		activeSiteCount: map[string]int64{orgID: 3}, // 已有 3 个，等于 MaxSites。
	}
	obj := newFakeObjStore()

	svc := NewWebPublishService(store, obj, WebPublishServiceConfig{
		SlugGen: func() string { return "newslug" },
		Now:     func() time.Time { return fixedNow },
	})

	_, err := svc.Publish(context.Background(), token, makeTarGz(t, map[string]string{
		"index.html": "hi",
	}))
	// 应返回含"配额"的错误，提示已达站点上限。
	require.Error(t, err)
	assert.Contains(t, err.Error(), "配额")
	// 必须包 ErrConflict，使 handler 映射为 409 而非不透明 500（配额上限是业务结果非系统故障）。
	require.ErrorIs(t, err, ErrConflict)
}
