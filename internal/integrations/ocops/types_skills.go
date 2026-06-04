// types_skills.go — ocops 包 skill 市场相关类型定义。
//
// SkillInfo 对应 oc-ops GET /oc/skills 返回列表中的单项，
// 描述一个 skill 的名称与管理状态。
package ocops

// SkillInfo 是 oc-ops GET /oc/skills 返回的单个 skill 状态。
type SkillInfo struct {
	// Name 是 skill 的唯一标识名称，如 "weather"。
	Name string `json:"name"`
	// Managed 表示该 skill 是否由 oc-ops 平台管理（即非用户手动放置）。
	Managed bool `json:"managed"`
	// Builtin 表示该 skill 是否为内置 skill（随镜像预装，不可删除）。
	Builtin bool `json:"builtin"`
	// Description 是 skill 介绍（取自 SKILL.md frontmatter description），供详情页展示。
	Description string `json:"description"`
}
