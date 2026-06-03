// Package clawhub 是 ClawHub skill 市场（openclaw/clawhub）的只读 REST 客户端。
// 公开 API 无需鉴权；本包只做浏览/搜索/下载，缓存与聚合在 service 层。
package clawhub

// Skill 是 ClawHub 列表/搜索/详情返回的单个 skill 元数据。
// 字段名以 ClawHub openapi.json 为准，未知字段忽略（json 默认行为）。
type Skill struct {
	Slug        string `json:"slug"`        // 库内唯一标识，用作 source_ref
	Name        string `json:"name"`        // SKILL.md name
	Description string `json:"description"` // 功能描述
	Version     string `json:"version"`     // 最新版本（latest）
	Downloads   int64  `json:"downloads"`   // 下载量（仅供展示）
}

// SearchResult 是 /api/v1/search 与 /api/v1/skills 的列表响应（含游标分页）。
type SearchResult struct {
	Skills     []Skill `json:"skills"`
	NextCursor string  `json:"next_cursor"` // 下一页游标，空字符串表示末页
}

// SkillVersion 是 /api/v1/skills/{slug}/versions 的单个版本项。
type SkillVersion struct {
	Version string `json:"version"` // 语义化版本号，如 "1.2.0"
}
