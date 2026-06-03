package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPlatformSource_Search 验证 PlatformSource 的核心聚合与过滤行为：
// 同 name 的多个版本聚合为一条（取最新版本），q 子串过滤按 name 和 description 匹配。
func TestPlatformSource_Search(t *testing.T) {
	store := newFakePlatformSkillStore()
	svc := NewPlatformSkillService(store, &fakeLibraryBlob{})

	// 上传 weather@1.0 与 weather@2.0，期望聚合后只有一条且版本为 2.0（最新）。
	_, err := svc.Upload(context.Background(), psvcPlatformPrincipal(), PlatformSkillUploadInput{Name: "weather", Version: "1.0", Data: []byte("a")})
	require.NoError(t, err)
	_, err = svc.Upload(context.Background(), psvcPlatformPrincipal(), PlatformSkillUploadInput{Name: "weather", Version: "2.0", Data: []byte("b")})
	require.NoError(t, err)

	// 上传 translate@1.0，用于验证 q 过滤效果（不匹配 "weather"）。
	_, err = svc.Upload(context.Background(), psvcPlatformPrincipal(), PlatformSkillUploadInput{Name: "translate", Version: "1.0", Data: []byte("c")})
	require.NoError(t, err)

	src := NewPlatformSource(svc)

	// q="weather"：只匹配 weather，且聚合成一行，取最新版本 2.0。
	page, err := src.Search(context.Background(), psvcPlatformPrincipal(), "weather", "")
	require.NoError(t, err)
	// q 过滤后只有 weather 一条（translate 被排除）
	require.Len(t, page.Entries, 1)
	assert.Equal(t, "weather", page.Entries[0].Name)
	// platform 来源标识
	assert.Equal(t, "platform", page.Entries[0].Source)
	// platform 的 source_ref 用 name
	assert.Equal(t, "weather", page.Entries[0].SourceRef)
	// 同 name 聚合取最新版本 2.0
	assert.Equal(t, "2.0", page.Entries[0].Version)
	// platform 来源 Downloads 恒为 0
	assert.EqualValues(t, 0, page.Entries[0].Downloads)
	// platform 无游标分页
	assert.Equal(t, "", page.NextCursor)
}

// TestPlatformSource_Search_All 验证 q 为空时返回全部 skill（每个 name 聚合为一条）。
func TestPlatformSource_Search_All(t *testing.T) {
	store := newFakePlatformSkillStore()
	svc := NewPlatformSkillService(store, &fakeLibraryBlob{})

	// 上传 weather@1.0、weather@2.0、translate@1.0，共两个不同 name。
	_, err := svc.Upload(context.Background(), psvcPlatformPrincipal(), PlatformSkillUploadInput{Name: "weather", Version: "1.0", Data: []byte("a")})
	require.NoError(t, err)
	_, err = svc.Upload(context.Background(), psvcPlatformPrincipal(), PlatformSkillUploadInput{Name: "weather", Version: "2.0", Data: []byte("b")})
	require.NoError(t, err)
	_, err = svc.Upload(context.Background(), psvcPlatformPrincipal(), PlatformSkillUploadInput{Name: "translate", Version: "1.0", Data: []byte("c")})
	require.NoError(t, err)

	src := NewPlatformSource(svc)

	// q="" 返回所有 name，聚合后应有 2 条（translate 和 weather 各一条）。
	page, err := src.Search(context.Background(), psvcPlatformPrincipal(), "", "")
	require.NoError(t, err)
	assert.Len(t, page.Entries, 2)
	// 返回结果按 name 排序，translate < weather
	assert.Equal(t, "translate", page.Entries[0].Name)
	assert.Equal(t, "weather", page.Entries[1].Name)
	// weather 取最新版本 2.0
	assert.Equal(t, "2.0", page.Entries[1].Version)
}

// TestPlatformSource_Search_OrgMemberAllowed 验证 org_member 可以浏览平台库市场。
// 市场是只读展示接口，所有已登录用户均可访问；写操作（上传/删除）仍需 platform_admin。
func TestPlatformSource_Search_OrgMemberAllowed(t *testing.T) {
	store := newFakePlatformSkillStore()
	svc := NewPlatformSkillService(store, &fakeLibraryBlob{})
	src := NewPlatformSource(svc)

	// org_member 调用 Search 应成功（市场浏览开放给所有已登录用户，返回空列表而非权限错误）。
	page, err := src.Search(context.Background(), psvcOrgMemberPrincipal(), "", "")
	require.NoError(t, err)
	assert.Empty(t, page.Entries)
}
