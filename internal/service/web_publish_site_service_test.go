// Package service - WebPublishSiteService 站点管理单元测试。
// 覆盖下线（Takedown）企业归属权限校验和 S3 前缀删除，以及续期（Renew）按企业 ttl 延后过期时间。
package service

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/store/sqlc"
)

// fakeSiteStore 实现 WebPublishSiteStore 接口，供测试注入受控数据。
type fakeSiteStore struct {
	// siteByID 按站点 ID 返回的 PublishedSite；若 key 不存在则返回 sql.ErrNoRows。
	siteByID map[string]sqlc.PublishedSite
	// webPublishConfig 按 orgID 返回的 OrgWebPublishConfig。
	webPublishConfig map[string]sqlc.OrgWebPublishConfig
	// sitesByOrg 按 orgID 返回的站点列表。
	sitesByOrg map[string][]sqlc.PublishedSite

	// 记录调用参数，供断言使用。
	statusCalls []sqlc.SetPublishedSiteStatusParams
	renewCalls  []sqlc.RenewPublishedSiteParams
}

// GetPublishedSiteByID 按 ID 查站点；ID 不存在时返回 sql.ErrNoRows 的错误。
func (f *fakeSiteStore) GetPublishedSiteByID(ctx context.Context, id string) (sqlc.PublishedSite, error) {
	site, ok := f.siteByID[id]
	if !ok {
		// 返回与真实 sqlc 相同的 "no rows" 错误。
		return sqlc.PublishedSite{}, sql.ErrNoRows
	}
	return site, nil
}

// ListSitesByOrg 返回企业下所有站点列表。
func (f *fakeSiteStore) ListSitesByOrg(ctx context.Context, orgID string) ([]sqlc.PublishedSite, error) {
	return f.sitesByOrg[orgID], nil
}

// GetWebPublishConfig 按 orgID 查 web-publish 配置；不存在返回 sql.ErrNoRows 错误。
func (f *fakeSiteStore) GetWebPublishConfig(ctx context.Context, orgID string) (sqlc.OrgWebPublishConfig, error) {
	cfg, ok := f.webPublishConfig[orgID]
	if !ok {
		return sqlc.OrgWebPublishConfig{}, sql.ErrNoRows
	}
	return cfg, nil
}

// SetPublishedSiteStatus 记录调用参数。
func (f *fakeSiteStore) SetPublishedSiteStatus(ctx context.Context, arg sqlc.SetPublishedSiteStatusParams) error {
	f.statusCalls = append(f.statusCalls, arg)
	return nil
}

// RenewPublishedSite 记录调用参数。
func (f *fakeSiteStore) RenewPublishedSite(ctx context.Context, arg sqlc.RenewPublishedSiteParams) error {
	f.renewCalls = append(f.renewCalls, arg)
	return nil
}

// fakeSiteObj 实现 siteObjectStore 接口，记录删除的前缀。
type fakeSiteObj struct {
	// deletedPrefixes 记录 DeletePrefix 调用的前缀列表，供断言使用。
	deletedPrefixes []string
}

// DeletePrefix 记录被删除的前缀。
func (f *fakeSiteObj) DeletePrefix(ctx context.Context, prefix string) error {
	f.deletedPrefixes = append(f.deletedPrefixes, prefix)
	return nil
}

// fixedSiteNow 是测试用固定时间点，保证 ExpiresAt 可断言。
var fixedSiteNow = time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)

// TestTakedownSetsDisabledAndDeletesPrefix 测试企业管理员下线本企业站点：
// 应调用 SetPublishedSiteStatus 置为 disabled，并删除整站前缀（published-sites/<id>/）。
func TestTakedownSetsDisabledAndDeletesPrefix(t *testing.T) {
	const siteID = "site-abc"
	const orgID = "org-1"

	store := &fakeSiteStore{
		siteByID: map[string]sqlc.PublishedSite{
			siteID: {
				ID:       siteID,
				OrgID:    orgID,
				Host:     "mysite.example.com",
				Status:   domain.SiteStatusActive,
				S3Prefix: "published-sites/site-abc/v2/",
			},
		},
	}
	obj := &fakeSiteObj{}

	// 注入固定时钟（Takedown 不使用时钟，但保持与 Renew 对称的构造方式）。
	svc := NewWebPublishSiteService(store, obj, func() time.Time { return fixedSiteNow })

	// 组织管理员对本组织站点发起下线操作。
	p := auth.Principal{Role: domain.UserRoleOrgAdmin, OrgID: orgID}
	err := svc.Takedown(context.Background(), p, siteID)
	require.NoError(t, err)

	// 必须调用一次 SetPublishedSiteStatus，且状态置为 disabled。
	require.Len(t, store.statusCalls, 1)
	assert.Equal(t, domain.SiteStatusDisabled, store.statusCalls[0].Status)
	assert.Equal(t, siteID, store.statusCalls[0].ID)

	// 必须删除整站根前缀（所有版本），而非当前版本的 S3Prefix。
	require.Len(t, obj.deletedPrefixes, 1)
	assert.Equal(t, "published-sites/site-abc/", obj.deletedPrefixes[0])
}

// TestTakedownDeniedCrossOrg 测试跨企业下线场景：
// org-1 的管理员尝试下线归属于 org-OTHER 的站点，应被拒绝并返回 ErrForbidden。
func TestTakedownDeniedCrossOrg(t *testing.T) {
	const siteID = "site-other"
	const siteOrg = "org-OTHER"

	store := &fakeSiteStore{
		siteByID: map[string]sqlc.PublishedSite{
			siteID: {
				ID:    siteID,
				OrgID: siteOrg, // 站点归属于另一个企业。
				Host:  "other.example.com",
			},
		},
	}
	obj := &fakeSiteObj{}

	svc := NewWebPublishSiteService(store, obj, func() time.Time { return fixedSiteNow })

	// org-1 的管理员尝试跨企业下线——CanManageOrg 应拒绝。
	p := auth.Principal{Role: domain.UserRoleOrgAdmin, OrgID: "org-1"}
	err := svc.Takedown(context.Background(), p, siteID)

	// 必须返回 ErrForbidden，不得调用任何写入操作。
	require.ErrorIs(t, err, ErrForbidden)
	assert.Empty(t, store.statusCalls, "跨企业下线不应写入状态")
	assert.Empty(t, obj.deletedPrefixes, "跨企业下线不应删除对象")
}

// TestRenewExtendsExpiry 测试续期场景：
// Renew 应按企业 site_ttl_days 计算新到期时间，并调用 RenewPublishedSite 写入。
func TestRenewExtendsExpiry(t *testing.T) {
	const siteID = "site-renew"
	const orgID = "org-1"
	const ttlDays = 14

	store := &fakeSiteStore{
		siteByID: map[string]sqlc.PublishedSite{
			siteID: {
				ID:    siteID,
				OrgID: orgID,
				Host:  "renew.example.com",
			},
		},
		webPublishConfig: map[string]sqlc.OrgWebPublishConfig{
			orgID: {
				OrgID:       orgID,
				SiteTtlDays: ttlDays,
			},
		},
	}
	obj := &fakeSiteObj{}

	svc := NewWebPublishSiteService(store, obj, func() time.Time { return fixedSiteNow })

	// 企业管理员续期本企业站点。
	p := auth.Principal{Role: domain.UserRoleOrgAdmin, OrgID: orgID}
	err := svc.Renew(context.Background(), p, siteID)
	require.NoError(t, err)

	// 必须调用一次 RenewPublishedSite，且新过期时间 = fixedSiteNow + ttlDays 天。
	require.Len(t, store.renewCalls, 1)
	expectedExpiry := fixedSiteNow.Add(time.Duration(ttlDays) * 24 * time.Hour)
	assert.Equal(t, expectedExpiry, store.renewCalls[0].ExpiresAt)
	assert.Equal(t, siteID, store.renewCalls[0].ID)
}
