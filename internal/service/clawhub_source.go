// Package service 的 ClawHub 公共库来源实现。
// ClawHubSource 把 ClawHub 公共库适配为 SkillSource，搜索结果走 Redis TTL 缓存、不落库。
package service

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	redis "github.com/redis/go-redis/v9"

	"oc-manager/internal/auth"
	"oc-manager/internal/integrations/clawhub"
)

// ClawHubSearcher 是 ClawHubSource 依赖的最小搜索能力接口。
// clawhub.ClawHubClient 自然满足此接口，无需额外适配。
type ClawHubSearcher interface {
	// Search 按关键词 q 与游标 cursor 搜索公共库，q 为空时等价列出热门。
	Search(ctx context.Context, q, cursor string) (clawhub.SearchResult, error)
	// GetSkill 取单个 skill 的富详情（完整描述/作者/统计/许可等）。
	GetSkill(ctx context.Context, slug string) (clawhub.SkillDetail, error)
	// ListVersions 列出指定 slug 的全部历史版本（含 changelog/发布时间）。
	ListVersions(ctx context.Context, slug string) ([]clawhub.SkillVersion, error)
	// Download 下载指定版本的 skill 归档原始字节（ClawHub 返回 zip 格式）。
	Download(ctx context.Context, slug, version string) ([]byte, error)
}

// RedisCache 是 ClawHubSource 依赖的最小缓存能力接口。
// *redis.Client 自然满足此接口，注入时可直接传 client 指针。
// 只声明 Get/Set 两个方法，避免强依赖 redis.Cmdable 的全量方法集，降低 fake 实现成本。
type RedisCache interface {
	// Get 从缓存读取字符串值，缺失时 Err() 返回 redis.Nil。
	Get(ctx context.Context, key string) *redis.StringCmd
	// Set 写入字符串值并附 TTL；ttl==0 表示永不过期。
	Set(ctx context.Context, key string, value any, ttl time.Duration) *redis.StatusCmd
}

// ClawHubSource 把 ClawHub 公共库适配为 SkillSource，搜索结果走 Redis TTL 缓存、不落库。
// 缓存策略：先查 Redis，命中直接返回反序列化后的 SkillPage；
// 未命中（redis.Nil）则回源 ClawHub API，转换为统一 SkillEntry 后写入缓存。
// Redis 读异常不致命：降级为回源，保证服务可用性。
type ClawHubSource struct {
	// api 是 ClawHub REST API 调用接口（测试中可替换为 fake）。
	api ClawHubSearcher
	// rdb 是 Redis 缓存接口（测试中可替换为 fake）。
	rdb RedisCache
	// ttl 是缓存条目的存活时长，由配置注入（默认 5 分钟）。
	ttl time.Duration
}

// NewClawHubSource 构造公共库来源。
// api 注入 ClawHub 客户端；rdb 注入 Redis 客户端；ttl 为缓存时长。
func NewClawHubSource(api ClawHubSearcher, rdb RedisCache, ttl time.Duration) *ClawHubSource {
	return &ClawHubSource{api: api, rdb: rdb, ttl: ttl}
}

// Kind 实现 SkillSource，返回来源标识 "clawhub"。
func (s *ClawHubSource) Kind() string { return "clawhub" }

// Search 先查 Redis 缓存，命中则直接返回；未命中则回源 ClawHub 并写缓存。
// 缓存 key 格式：skill-market:clawhub:<q>:<cursor>，区分不同搜索入参。
// principal 参数在 clawhub 来源无鉴权语义，仅为满足 SkillSource 接口而接收。
func (s *ClawHubSource) Search(ctx context.Context, _ auth.Principal, q, cursor string) (SkillPage, error) {
	// 缓存 key 包含 q 与 cursor，确保不同搜索条件互不干扰。
	key := "skill-market:clawhub:" + q + ":" + cursor

	// 先查缓存：命中时反序列化 JSON 并直接返回，避免回源 API 调用。
	if val, err := s.rdb.Get(ctx, key).Result(); err == nil {
		var page SkillPage
		if json.Unmarshal([]byte(val), &page) == nil {
			return page, nil
		}
		// JSON 解析失败（缓存内容损坏）：降级回源，不中断请求。
	} else if !errors.Is(err, redis.Nil) {
		// redis.Nil 表示 key 不存在（正常未命中），其它错误属网络/连接异常，降级回源。
		// 显式丢弃错误，保证 Redis 故障时服务仍可用。
		_ = err
	}

	// 缓存未命中，回源 ClawHub API。
	res, err := s.api.Search(ctx, q, cursor)
	if err != nil {
		return SkillPage{}, err
	}

	// 把 ClawHub 响应的 Skill 列表转换为统一 SkillEntry 格式。
	// source_ref 使用 slug（ClawHub 的唯一标识），Downloads 透传展示用途。
	page := SkillPage{
		NextCursor: res.NextCursor,
		Entries:    make([]SkillEntry, 0, len(res.Skills)),
	}
	for _, sk := range res.Skills {
		page.Entries = append(page.Entries, SkillEntry{
			Source:      "clawhub",
			SourceRef:   sk.Slug,
			Name:        sk.Name,
			Description: sk.Description,
			Version:     sk.Version,
			Downloads:   sk.Downloads,
		})
	}

	// 写缓存：序列化 SkillPage 写入 Redis，TTL 由配置决定（默认 5 分钟）。
	// 写缓存失败不影响本次请求返回，仅记录丢弃（下次请求仍可回源）。
	if raw, err := json.Marshal(page); err == nil {
		_ = s.rdb.Set(ctx, key, raw, s.ttl).Err()
	}

	return page, nil
}

// Detail 返回公共库 skill 的富详情（直接回源 GetSkill，不走缓存——详情页低频操作，
// 且需反映上游完整描述/统计的最新值）。映射 clawhub.SkillDetail 到统一 SkillDetailResult。
func (s *ClawHubSource) Detail(ctx context.Context, _ auth.Principal, ref string) (SkillDetailResult, error) {
	d, err := s.api.GetSkill(ctx, ref)
	if err != nil {
		return SkillDetailResult{}, err
	}
	return SkillDetailResult{
		Name:         d.Name,
		Source:       "clawhub",
		SourceRef:    d.Slug,
		Description:  d.Description,
		Version:      d.Version,
		Downloads:    d.Downloads,
		Stars:        d.Stars,
		Installs:     d.Installs,
		Comments:     d.Comments,
		License:      d.License,
		Keywords:     d.Keywords,
		CreatedAt:    d.CreatedAt,
		UpdatedAt:    d.UpdatedAt,
		AuthorName:   d.AuthorName,
		AuthorHandle: d.AuthorHandle,
		AuthorAvatar: d.AuthorAvatar,
	}, nil
}

// Versions 列出公共库中 slug=ref 的全部历史版本（含 changelog/发布时间），直接回源。
func (s *ClawHubSource) Versions(ctx context.Context, _ auth.Principal, ref string) ([]SkillVersionResult, error) {
	vs, err := s.api.ListVersions(ctx, ref)
	if err != nil {
		return nil, err
	}
	// clawhubcn 已按版本从新到旧返回，透传 version + changelog + 发布时间。
	out := make([]SkillVersionResult, 0, len(vs))
	for _, v := range vs {
		out = append(out, SkillVersionResult{Version: v.Version, Changelog: v.Changelog, PublishedAt: v.CreatedAt})
	}
	return out, nil
}

// Download 取公共库 slug=ref、version 的归档原始字节，扩展名固定为 zip（ClawHub 归档格式）。
// 直接回源 ClawHub /download，不走缓存（下载低频且为二进制大对象）。
func (s *ClawHubSource) Download(ctx context.Context, ref, version string) ([]byte, string, error) {
	data, err := s.api.Download(ctx, ref, version)
	if err != nil {
		return nil, "", err
	}
	return data, "zip", nil
}

// 编译期断言：ClawHubSource 必须实现 SkillSource 接口。
var _ SkillSource = (*ClawHubSource)(nil)
