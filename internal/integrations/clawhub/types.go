// Package clawhub 是 ClawHub skill 市场（clawhubcn.com）的只读 REST 客户端。
// 公开 API 无需鉴权；本包只做浏览/搜索/下载，缓存与聚合在 service 层。
package clawhub

import "encoding/json"

// Skill 是对外暴露的 skill 元数据（service / ClawHubSource 消费）。
// 字段语义稳定，与真实 ClawHub（clawhubcn.com）原始 JSON 的字段名解耦：
// 真实站点用 displayName/summary/tags.latest/stats.downloads 等嵌套命名，
// 由下方 UnmarshalJSON 统一映射到这套扁平字段。
type Skill struct {
	Slug        string // 库内唯一标识，用作 source_ref
	Name        string // 展示名（clawhubcn: displayName）
	Description string // 功能描述（clawhubcn: summary）
	Version     string // 最新版本（clawhubcn: tags.latest，回退 latestVersion.version）
	Downloads   int64  // 下载量（clawhubcn: stats.downloads，仅供展示）
}

// clawhubSkillRaw 镜像 clawhubcn.com 列表/详情里单个 skill 的原始 JSON 结构。
// 真实示例：{"slug","displayName","summary","tags":{"latest":"3.0.21"},
//            "stats":{"downloads":457304},"latestVersion":{"version":"3.0.21"}}
type clawhubSkillRaw struct {
	Slug        string `json:"slug"`
	DisplayName string `json:"displayName"`
	Summary     string `json:"summary"`
	Tags        struct {
		Latest string `json:"latest"`
	} `json:"tags"`
	Stats struct {
		Downloads int64 `json:"downloads"`
	} `json:"stats"`
	LatestVersion struct {
		Version string `json:"version"`
	} `json:"latestVersion"`
}

// UnmarshalJSON 把 clawhubcn 原始 skill JSON 映射到对外 Skill 的扁平字段。
func (s *Skill) UnmarshalJSON(data []byte) error {
	var raw clawhubSkillRaw
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	s.Slug = raw.Slug
	s.Name = raw.DisplayName
	s.Description = raw.Summary
	// 版本优先取 tags.latest；列表项缺该字段时回退 latestVersion.version。
	s.Version = raw.Tags.Latest
	if s.Version == "" {
		s.Version = raw.LatestVersion.Version
	}
	s.Downloads = raw.Stats.Downloads
	return nil
}

// SearchResult 是 /api/v1/skills 与 /api/v1/search 的列表响应。
// clawhubcn 用 {"items":[...],"nextCursor":...}（注意非 skills/next_cursor）。
type SearchResult struct {
	Skills     []Skill `json:"items"`
	NextCursor string  `json:"nextCursor"` // 下一页游标，空或 null 表示末页
}

// SkillVersion 是 /api/v1/skills/{slug}/versions 的 items 里单个版本项。
type SkillVersion struct {
	Version string `json:"version"` // 语义化版本号，如 "1.2.0"
}
