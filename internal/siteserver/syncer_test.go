package siteserver

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeListClient 是 SiteListClient 的替身：按序返回预置结果/错误。
type fakeListClient struct {
	rets [][]SiteRecord
	errs []error
	call int
}

func (f *fakeListClient) ListActiveSites(_ context.Context) ([]SiteRecord, error) {
	i := f.call
	f.call++
	if i < len(f.errs) && f.errs[i] != nil {
		return nil, f.errs[i]
	}
	return f.rets[i], nil
}

// TestSyncOnceBuildsSnapshot 覆盖：一次同步把 active 站点写入注册表，按 host 可查。
func TestSyncOnceBuildsSnapshot(t *testing.T) {
	reg := NewRegistry()
	cl := &fakeListClient{rets: [][]SiteRecord{{
		{Host: "blog.example.com", SiteID: "s1", S3Prefix: "published-sites/s1/v1/", Status: "active"},
	}}}
	s := NewSyncer(cl, reg, 0)
	require.NoError(t, s.syncOnce(context.Background()))
	e, ok := reg.Lookup("blog.example.com")
	require.True(t, ok)
	assert.Equal(t, "s1", e.SiteID)
}

// TestSyncFailureKeepsOldSnapshot 覆盖：拉取失败时不清空注册表，继续用上次成功快照。
func TestSyncFailureKeepsOldSnapshot(t *testing.T) {
	reg := NewRegistry()
	cl := &fakeListClient{
		rets: [][]SiteRecord{{{Host: "blog.example.com", SiteID: "s1", S3Prefix: "p/", Status: "active"}}, nil},
		errs: []error{nil, errors.New("manager down")},
	}
	s := NewSyncer(cl, reg, 0)
	require.NoError(t, s.syncOnce(context.Background())) // 首次成功
	require.Error(t, s.syncOnce(context.Background()))   // 二次失败
	_, ok := reg.Lookup("blog.example.com")
	assert.True(t, ok, "同步失败不应清空注册表")
}

// TestSyncFiltersNonActive 覆盖：非 active 记录被过滤，不进路由（双保险）。
func TestSyncFiltersNonActive(t *testing.T) {
	reg := NewRegistry()
	cl := &fakeListClient{rets: [][]SiteRecord{{
		{Host: "a.example.com", SiteID: "a", S3Prefix: "pa/", Status: "active"},
		{Host: "b.example.com", SiteID: "b", S3Prefix: "pb/", Status: "disabled"},
	}}}
	s := NewSyncer(cl, reg, 0)
	require.NoError(t, s.syncOnce(context.Background()))
	_, ok := reg.Lookup("a.example.com")
	assert.True(t, ok)
	_, ok = reg.Lookup("b.example.com")
	assert.False(t, ok, "非 active 站点不应进入路由")
}
