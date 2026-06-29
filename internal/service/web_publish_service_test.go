// Package service - WebPublishService 核心发布逻辑单元测试。
// 覆盖首次发布、原地更新、slug 归属冲突、企业未开通、站点配额超限五个核心分支。
package service

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"database/sql"
	"errors"
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
	// updatedVersions 记录 UpdatePublishedSiteVersion 的调用参数列表。
	updatedVersions []sqlc.UpdatePublishedSiteVersionParams
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

func (f *fakeWPubStore) UpdatePublishedSiteVersion(ctx context.Context, arg sqlc.UpdatePublishedSiteVersionParams) error {
	f.updatedVersions = append(f.updatedVersions, arg)
	return nil
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

	// 传空 slug 触发随机 slug 生成分支。
	result, err := svc.Publish(context.Background(), token, "", makeTarGz(t, map[string]string{
		"index.html": "<html>hello</html>",
	}))
	require.NoError(t, err)

	// 返回 URL 应包含 slug.base_domain。
	assert.Equal(t, "https://rand123.example.com", result.URL)
	// ExpiresAt 应等于 now + 7 天。
	assert.Equal(t, fixedNow.Add(7*24*time.Hour), result.ExpiresAt)

	// 应调用一次 CreatePublishedSite，而非 UpdatePublishedSiteVersion。
	require.Len(t, store.createdSites, 1)
	assert.Empty(t, store.updatedVersions)

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

// TestPublishUpdateInPlace 测试原地更新场景：同一 app 再次发布同 slug → 不创建新行，
// 切到 v2 前缀，重置 TTL，删除旧 v1 前缀。
func TestPublishUpdateInPlace(t *testing.T) {
	const orgID = "org-1"
	const appID = "app-1"
	const siteID = "site-uuid-123"
	const token = "test-token-update"
	const slug = "mysite"
	const host = "mysite.example.com"
	const oldPrefix = "published-sites/site-uuid-123/v1/"

	store := &fakeWPubStore{
		appByHash: map[string]sqlc.App{
			HashAppRuntimeToken(token): {ID: appID, OrgID: orgID},
		},
		publishConfig: map[string]sqlc.OrgWebPublishConfig{
			orgID: makeReadyCfg(orgID),
		},
		// 已有同一 app 拥有的站点记录。
		siteByHost: map[string]sqlc.PublishedSite{
			host: {
				ID:             siteID,
				OrgID:          orgID,
				AppID:          appID, // 同一 app，不触发归属冲突。
				Host:           host,
				Slug:           slug,
				CurrentVersion: "v1",
				S3Prefix:       oldPrefix,
				Status:         "active",
			},
		},
		activeSiteCount: map[string]int64{orgID: 1},
	}
	obj := newFakeObjStore()

	svc := NewWebPublishService(store, obj, WebPublishServiceConfig{
		SlugGen: func() string { return "should-not-be-called" },
		Now:     func() time.Time { return fixedNow },
	})

	// 传入已有 slug，触发原地更新分支。
	result, err := svc.Publish(context.Background(), token, slug, makeTarGz(t, map[string]string{
		"index.html": "<html>v2</html>",
	}))
	require.NoError(t, err)

	// URL 应与旧站点相同（host 不变）。
	assert.Equal(t, "https://"+host, result.URL)
	// ExpiresAt 应重置为 now + 7 天。
	assert.Equal(t, fixedNow.Add(7*24*time.Hour), result.ExpiresAt)

	// 不应创建新行，只应更新现有行。
	assert.Empty(t, store.createdSites)
	require.Len(t, store.updatedVersions, 1)

	updated := store.updatedVersions[0]
	// 版本应从 v1 升到 v2。
	assert.Equal(t, "v2", updated.CurrentVersion)
	assert.Equal(t, siteID, updated.ID)
	assert.True(t, strings.HasSuffix(updated.S3Prefix, "/v2/"))

	// 旧版本前缀应被删除。
	require.Len(t, obj.deletedPrefixes, 1)
	assert.Equal(t, oldPrefix, obj.deletedPrefixes[0])
}

// TestPublishSlugTakenByOtherApp 测试 slug 归属冲突场景：目标 host 已被其它 app 占用 → 返回含"已占用"的错误。
func TestPublishSlugTakenByOtherApp(t *testing.T) {
	const orgID = "org-1"
	const appID = "app-1"
	const otherAppID = "app-other"
	const token = "test-token-taken"
	const slug = "taken"
	const host = "taken.example.com"

	store := &fakeWPubStore{
		appByHash: map[string]sqlc.App{
			HashAppRuntimeToken(token): {ID: appID, OrgID: orgID},
		},
		publishConfig: map[string]sqlc.OrgWebPublishConfig{
			orgID: makeReadyCfg(orgID),
		},
		// host 已被另一个 app 占用。
		siteByHost: map[string]sqlc.PublishedSite{
			host: {
				ID:    "site-other",
				AppID: otherAppID, // 不同 app，触发归属冲突检查。
				Host:  host,
				Slug:  slug,
			},
		},
		activeSiteCount: map[string]int64{orgID: 1},
	}
	obj := newFakeObjStore()

	svc := NewWebPublishService(store, obj, WebPublishServiceConfig{
		SlugGen: func() string { return "rand-unused" },
		Now:     func() time.Time { return fixedNow },
	})

	_, err := svc.Publish(context.Background(), token, slug, makeTarGz(t, map[string]string{
		"index.html": "hi",
	}))
	// 应返回含"已占用"的错误，提示调用方 slug 已被其他实例持有。
	require.Error(t, err)
	assert.Contains(t, err.Error(), "已占用")
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

	_, err := svc.Publish(context.Background(), token, "myslug", makeTarGz(t, map[string]string{
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

	_, err := svc.Publish(context.Background(), token, "newslug", makeTarGz(t, map[string]string{
		"index.html": "hi",
	}))
	// 应返回含"配额"的错误，提示已达站点上限。
	require.Error(t, err)
	assert.Contains(t, err.Error(), "配额")
}
