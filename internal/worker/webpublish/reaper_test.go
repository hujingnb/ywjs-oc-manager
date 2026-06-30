package webpublish

import (
	"context"
	"testing"

	"oc-manager/internal/domain"
	"oc-manager/internal/store/sqlc"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeReaperStore struct {
	expired   []sqlc.PublishedSite
	statusSet []sqlc.SetPublishedSiteStatusParams
}

func (f *fakeReaperStore) ListExpiredActiveSites(_ context.Context) ([]sqlc.PublishedSite, error) {
	return f.expired, nil
}
func (f *fakeReaperStore) SetPublishedSiteStatus(_ context.Context, p sqlc.SetPublishedSiteStatusParams) error {
	f.statusSet = append(f.statusSet, p)
	return nil
}

type fakeReaperObj struct{ deleted []string }

func (f *fakeReaperObj) DeletePrefix(_ context.Context, prefix string) error {
	f.deleted = append(f.deleted, prefix)
	return nil
}

// TestReapOnceExpiresAndDeletes 覆盖：每个过期 active 站点被置 expired 并删整站前缀。
func TestReapOnceExpiresAndDeletes(t *testing.T) {
	st := &fakeReaperStore{expired: []sqlc.PublishedSite{
		{ID: "s1", S3Prefix: "published-sites/s1/v2/"},
		{ID: "s2", S3Prefix: "published-sites/s2/v1/"},
	}}
	obj := &fakeReaperObj{}
	r := NewSiteReaper(st, obj)
	require.NoError(t, r.ReapOnce(context.Background()))
	require.Len(t, st.statusSet, 2)
	assert.Equal(t, domain.SiteStatusExpired, st.statusSet[0].Status)
	assert.ElementsMatch(t, []string{"published-sites/s1/", "published-sites/s2/"}, obj.deleted)
}

// TestReapOnceNoExpired 覆盖：无过期站点时不做任何动作。
func TestReapOnceNoExpired(t *testing.T) {
	st := &fakeReaperStore{}
	obj := &fakeReaperObj{}
	require.NoError(t, NewSiteReaper(st, obj).ReapOnce(context.Background()))
	assert.Empty(t, obj.deleted)
}
