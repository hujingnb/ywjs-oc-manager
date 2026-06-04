// Package service 的 skill 市场来源抽象。
// SkillSource 是可扩展的来源接口，目前有 PlatformSource（平台库）与 ClawHubSource（公共库，Plan 4 实现）。
package service

import (
	"context"
	"sort"
	"strings"

	"oc-manager/internal/auth"
)

// SkillEntry 是市场里一个 skill 的统一展示条目（跨来源）。
// Downloads 仅对 clawhub 来源有意义，platform 来源恒为 0。
type SkillEntry struct {
	// Source 是来源标识，取值为 "platform" 或 "clawhub"。
	Source string `json:"source"`
	// SourceRef 是回源标识：platform 用 name，clawhub 用 slug。
	SourceRef string `json:"source_ref"`
	// Name 是 skill 名称。
	Name string `json:"name"`
	// Description 是 skill 简短描述。
	Description string `json:"description"`
	// Version 是展示的最新版本号。
	Version string `json:"version"`
	// Downloads 是下载次数，仅 clawhub 有意义，platform 为 0。
	Downloads int64 `json:"downloads"`
}

// SkillPage 是一页市场结果（含下一页游标，platform 无游标故 NextCursor 为空）。
type SkillPage struct {
	// Entries 是本页的 skill 条目列表。
	Entries []SkillEntry `json:"entries"`
	// NextCursor 是下一页游标，platform 来源始终为空。
	NextCursor string `json:"next_cursor"`
}

// SkillSource 是单个 skill 来源的浏览/搜索能力接口。
// 目前由 PlatformSource（平台库）与 ClawHubSource（公共库）各自实现。
type SkillSource interface {
	// Kind 返回来源标识（platform | clawhub）。
	Kind() string
	// Search 按关键词 q（空=全列）与游标 cursor 返回一页条目。
	Search(ctx context.Context, principal auth.Principal, q, cursor string) (SkillPage, error)
	// Versions 返回指定 skill（ref：platform=name，clawhub=slug）的全部历史版本号，
	// 按版本从新到旧排序，供详情页展示版本列表。
	Versions(ctx context.Context, principal auth.Principal, ref string) ([]string, error)
}

// platformSkillLister 是 PlatformSource 所需的平台库查询能力最小接口。
// 使用接口而非直接依赖 *PlatformSkillService，便于单元测试注入 stub。
type platformSkillLister interface {
	// ListForMarket 返回全部平台库 skill，市场展示用（所有已登录用户均可调用）。
	ListForMarket(ctx context.Context, principal auth.Principal) ([]PlatformSkillResult, error)
}

// PlatformSource 把平台库（platform_skills）适配为 SkillSource。
// 按 name 聚合所有版本并保留最新版本（版本字符串降序最大值），支持 q 子串过滤。
type PlatformSource struct {
	svc platformSkillLister
}

// NewPlatformSource 构造平台库来源适配器。
func NewPlatformSource(svc platformSkillLister) *PlatformSource {
	return &PlatformSource{svc: svc}
}

// Kind 实现 SkillSource，返回 "platform"。
func (s *PlatformSource) Kind() string { return "platform" }

// Versions 列出平台库中 name=ref 的全部版本号，按版本字符串从大到小排序（最新在前）。
// 与 Search 取最新版本的比较口径一致（字符串比较，对同位数版本足够）。
func (s *PlatformSource) Versions(ctx context.Context, principal auth.Principal, ref string) ([]string, error) {
	rows, err := s.svc.ListForMarket(ctx, principal)
	if err != nil {
		return nil, err
	}
	versions := make([]string, 0)
	for _, r := range rows {
		// 只收同名 skill 的版本。
		if r.Name == ref {
			versions = append(versions, r.Version)
		}
	}
	// 降序排列（最新版本在前），与前端「版本列表第一个为最新」预期一致。
	sort.Sort(sort.Reverse(sort.StringSlice(versions)))
	return versions, nil
}

// Search 列出平台库 skill，按 name 聚合并取最新版本，按 q 子串过滤名称与描述。
// platform 无游标分页，cursor 参数被忽略，NextCursor 恒为空。
// 聚合规则：同 name 的所有行中，保留 version 字符串最大的那一条。
// 实际部署时 ListPlatformSkills 按 name ASC, created_at DESC 排序，版本单调递增；
// 但为保证单元测试（fakePlatformSkillStore 不保证排序）的正确性，此处显式取最大版本。
// 使用 ListForMarket 而非 List，确保非平台管理员（如 org_member）也能浏览市场。
func (s *PlatformSource) Search(ctx context.Context, principal auth.Principal, q, _ string) (SkillPage, error) {
	rows, err := s.svc.ListForMarket(ctx, principal)
	if err != nil {
		return SkillPage{}, err
	}
	// best 存储每个 name 当前遍历到的最新版本条目。
	// 逐行比较版本字符串，保留最大值（对 semver major 版本差距的场景足够，如 "1.0" vs "2.0"）。
	best := map[string]PlatformSkillResult{}
	for _, r := range rows {
		// q 子串过滤：同时匹配 name 和 description 字段（任一包含即保留）。
		if q != "" && !strings.Contains(r.Name, q) && !strings.Contains(r.Description, q) {
			continue
		}
		prev, ok := best[r.Name]
		if !ok || r.Version > prev.Version {
			// 尚未记录该 name，或当前行版本更新，更新最优条目。
			best[r.Name] = r
		}
	}
	// 按 name 排序确保输出顺序稳定，便于调用方和测试断言。
	names := make([]string, 0, len(best))
	for n := range best {
		names = append(names, n)
	}
	sort.Strings(names)
	entries := make([]SkillEntry, 0, len(names))
	for _, n := range names {
		r := best[n]
		entries = append(entries, SkillEntry{
			// platform 来源的 SourceRef 用 name 作为回源标识。
			Source:      "platform",
			SourceRef:   r.Name,
			Name:        r.Name,
			Description: r.Description,
			Version:     r.Version,
			Downloads:   0,
		})
	}
	return SkillPage{Entries: entries, NextCursor: ""}, nil
}

// 编译期断言：PlatformSource 必须实现 SkillSource 接口。
var _ SkillSource = (*PlatformSource)(nil)
