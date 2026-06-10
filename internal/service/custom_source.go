package service

import (
	"context"
	"strings"

	"oc-manager/internal/auth"
	"oc-manager/internal/domain"
	"oc-manager/internal/store/sqlc"
)

// CustomSourceStore 是 CustomSource 所需的可见性查询能力（按 org+受众+申请人过滤定制技能）。
type CustomSourceStore interface {
	ListVisibleCustomSkills(ctx context.Context, arg sqlc.ListVisibleCustomSkillsParams) ([]sqlc.ListVisibleCustomSkillsRow, error)
}

// CustomSource 把定制技能接入市场来源抽象，作为第三个来源 "custom"。
// 与 platform/clawhub 不同：Search/Detail 按 principal 的 org_id+角色 过滤目标范围，
// 只返回对该主体可见的定制技能。
type CustomSource struct {
	store CustomSourceStore
}

// NewCustomSource 构造 custom 来源。
func NewCustomSource(store CustomSourceStore) *CustomSource {
	return &CustomSource{store: store}
}

// Kind 实现 SkillSource，返回 "custom"。
func (s *CustomSource) Kind() string { return "custom" }

// visibleParams 把 principal 翻译成可见性查询参数：is_admin 仅 org_admin 为 1，
// 用于命中 org_admins 受众。IsAdmin 字段 sqlc 生成为 interface{}，此处赋 int64 与 SQL 中 `? = 1` 比较一致。
func visibleParams(p auth.Principal) sqlc.ListVisibleCustomSkillsParams {
	var isAdmin interface{} = int64(0)
	if p.Role == domain.UserRoleOrgAdmin {
		isAdmin = int64(1)
	}
	return sqlc.ListVisibleCustomSkillsParams{OrgID: p.OrgID, IsAdmin: isAdmin, UserID: p.UserID}
}

// Search 返回对 principal 可见的定制技能（同名取最新版本），q 子串过滤 name/description（大小写不敏感）。
// 可见性查询已按 name ASC, created_at DESC 排序，故同名首条即最新，用 seen 去重保留最新一条。
func (s *CustomSource) Search(ctx context.Context, principal auth.Principal, q, _ string) (SkillPage, error) {
	rows, err := s.store.ListVisibleCustomSkills(ctx, visibleParams(principal))
	if err != nil {
		return SkillPage{}, err
	}
	seen := map[string]struct{}{}
	ql := strings.ToLower(strings.TrimSpace(q))
	var entries []SkillEntry
	for _, r := range rows {
		if _, ok := seen[r.Name]; ok {
			continue // 查询已 created_at DESC，首条即最新，后续同名跳过
		}
		if ql != "" && !strings.Contains(strings.ToLower(r.Name), ql) && !strings.Contains(strings.ToLower(r.Description), ql) {
			continue
		}
		seen[r.Name] = struct{}{}
		entries = append(entries, SkillEntry{
			Source:        "custom",
			SourceRef:     r.Name,
			Name:          r.Name,
			Description:   r.Description,
			Version:       r.Version,
			RequesterName: r.RequesterUsername,
			Audience:      r.Audience,
		})
	}
	return SkillPage{Entries: entries}, nil
}

// Detail 返回单个可见定制技能详情：ref = name，取首个可见行（已 created_at DESC，即最新版本）。
// 不可见或不存在时返回 ErrCustomSkillNotFound。
func (s *CustomSource) Detail(ctx context.Context, principal auth.Principal, ref string) (SkillDetailResult, error) {
	rows, err := s.store.ListVisibleCustomSkills(ctx, visibleParams(principal))
	if err != nil {
		return SkillDetailResult{}, err
	}
	for _, r := range rows {
		if r.Name == ref {
			return SkillDetailResult{
				Name:        r.Name,
				Source:      "custom",
				SourceRef:   r.Name,
				Description: r.Description,
				Version:     r.Version,
				AuthorName:  r.RequesterUsername,
			}, nil
		}
	}
	return SkillDetailResult{}, ErrCustomSkillNotFound
}

// Versions 返回某可见定制技能的版本列表：本来源市场只展示当前可见最新一版，故返回单条最新。
func (s *CustomSource) Versions(ctx context.Context, principal auth.Principal, ref string) ([]SkillVersionResult, error) {
	d, err := s.Detail(ctx, principal, ref)
	if err != nil {
		return nil, err
	}
	return []SkillVersionResult{{Version: d.Version}}, nil
}

// Download 取归档：custom 来源的安装走 AppSkillService 的 custom 分支（GetForInstall），
// 不经市场层 Download，故此处返回 ErrCustomSkillInvalid 以防误用。
func (s *CustomSource) Download(_ context.Context, _, _ string) ([]byte, string, error) {
	return nil, "", ErrCustomSkillInvalid
}

// 编译期断言：CustomSource 必须实现 SkillSource 接口。
var _ SkillSource = (*CustomSource)(nil)
