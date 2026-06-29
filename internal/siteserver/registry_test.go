package siteserver

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestRegistryReplaceAndLookup 覆盖：Replace 整体替换快照后可按 host 查到，不在快照中的 host 查不到。
func TestRegistryReplaceAndLookup(t *testing.T) {
	r := NewRegistry()
	r.Replace(map[string]Entry{
		"blog.apps.example.com": {SiteID: "s1", S3Prefix: "published-sites/s1/v1/", Status: "active"},
	})
	e, ok := r.Lookup("blog.apps.example.com")
	assert.True(t, ok)
	assert.Equal(t, "s1", e.SiteID)

	_, ok = r.Lookup("unknown.apps.example.com")
	assert.False(t, ok)
}

// TestRegistryReplaceIsAtomicSwap 覆盖：第二次 Replace 整体换新，旧 host 不再可查（下线/过期站点下一次同步后即从路由消失）。
func TestRegistryReplaceIsAtomicSwap(t *testing.T) {
	r := NewRegistry()
	r.Replace(map[string]Entry{"a.example.com": {SiteID: "a", Status: "active"}})
	r.Replace(map[string]Entry{"b.example.com": {SiteID: "b", Status: "active"}})
	_, ok := r.Lookup("a.example.com")
	assert.False(t, ok, "旧快照的 host 应在整体替换后消失")
	_, ok = r.Lookup("b.example.com")
	assert.True(t, ok)
}
