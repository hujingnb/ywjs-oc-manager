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
//
//	"stats":{"downloads":457304},"latestVersion":{"version":"3.0.21"}}
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
	Version   string `json:"version"`   // 语义化版本号，如 "1.2.0"
	Changelog string `json:"changelog"` // 该版本更新说明（clawhubcn 多为空，有的有内容）
	CreatedAt int64  `json:"createdAt"` // 发布时间戳（epoch 毫秒）
}

// SkillDetail 是单个 skill 的富详情（详情页用），在 Skill 展示字段之外补充
// 完整描述、作者、统计、许可、关键词与时间。从 clawhubcn 详情端点
// {skill, latestVersion, owner, metadata} 解析——其中 metadata.summary 是未截断的
// 完整描述（skill.summary 被截断到 160 字符）。
type SkillDetail struct {
	Slug         string   // 库内唯一标识
	Name         string   // 展示名
	Description  string   // 完整描述（metadata.summary 优先，回退 skill.summary）
	Version      string   // 最新版本
	Downloads    int64    // 下载量
	Stars        int64    // 星标数
	Installs     int64    // 累计安装数
	Comments     int64    // 评论数
	License      string   // 许可证
	Keywords     []string // 关键词
	CreatedAt    string   // 创建时间（ISO 字符串，来自 metadata.createdAt）
	UpdatedAt    string   // 更新时间（ISO 字符串，来自 metadata.updatedAt）
	AuthorName   string   // 作者展示名（owner.displayName）
	AuthorHandle string   // 作者 handle（owner.handle）
	AuthorAvatar string   // 作者头像 URL（owner.image）
}

// clawhubDetailRaw 镜像 clawhubcn 详情端点 {skill, latestVersion, owner, metadata} 的原始结构。
type clawhubDetailRaw struct {
	Skill struct {
		Slug        string `json:"slug"`
		DisplayName string `json:"displayName"`
		Summary     string `json:"summary"`
		Tags        struct {
			Latest string `json:"latest"`
		} `json:"tags"`
		Stats struct {
			Downloads       int64 `json:"downloads"`
			Stars           int64 `json:"stars"`
			InstallsAllTime int64 `json:"installsAllTime"`
			Comments        int64 `json:"comments"`
		} `json:"stats"`
	} `json:"skill"`
	LatestVersion struct {
		Version string `json:"version"`
		License string `json:"license"`
	} `json:"latestVersion"`
	Owner struct {
		DisplayName string `json:"displayName"`
		Handle      string `json:"handle"`
		Image       string `json:"image"`
	} `json:"owner"`
	Metadata struct {
		Summary   string   `json:"summary"`  // 完整描述（未截断）
		License   string   `json:"License"`  // 注意大写 L
		Keywords  []string `json:"Keywords"` // 注意大写 K
		CreatedAt string   `json:"createdAt"`
		UpdatedAt string   `json:"updatedAt"`
	} `json:"metadata"`
}
