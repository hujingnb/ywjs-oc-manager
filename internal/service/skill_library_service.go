// Package service 的市场聚合层实现。
// SkillLibraryService 按 source 参数聚合平台库与公共库两个来源，
// 提供统一的市场浏览/搜索入口。
package service

import (
	"context"

	"oc-manager/internal/auth"
)

// SkillLibraryService 聚合多个 SkillSource，提供市场浏览/搜索。
// 目前支持 "platform"（平台库）与 "clawhub"（ClawHub 公共库）两个来源。
// 公共库来源失败时降级不阻断平台库展示（spec 要求）。
type SkillLibraryService struct {
	// platform 是平台库来源，必须非 nil。
	platform SkillSource
	// clawhub 是 ClawHub 公共库来源，可为 nil（未配置 ClawHub BaseURL 时）。
	// nil 时市场仅展示平台库，指定 source=clawhub 返回空列表。
	clawhub SkillSource
}

// NewSkillLibraryService 构造聚合 service。
// platform 必须非 nil；clawhub 可为 nil（未接入公共库时降级为仅平台库）。
func NewSkillLibraryService(platform, clawhub SkillSource) *SkillLibraryService {
	return &SkillLibraryService{platform: platform, clawhub: clawhub}
}

// List 按 source 参数返回市场条目：
//   - "platform"：只查平台库，游标 cursor 透传（platform 无游标则忽略）。
//   - "clawhub"：只查公共库；clawhub 未配置（nil）时返回空列表。
//   - ""（空字符串）：聚合两者，platform 条目在前；ClawHub 调用失败时降级
//     仅返回平台库条目（不阻断请求，spec 要求）。
//   - 其他值：返回 ErrSkillMarketSourceUnknown（handler 层映射为 400）。
func (s *SkillLibraryService) List(ctx context.Context, principal auth.Principal, source, q, cursor string) (SkillPage, error) {
	switch source {
	case "platform":
		// 仅查平台库，cursor 透传（PlatformSource 会忽略）。
		return s.platform.Search(ctx, principal, q, cursor)

	case "clawhub":
		// 仅查公共库：未配置时直接返回空列表，不报错。
		if s.clawhub == nil {
			return SkillPage{Entries: []SkillEntry{}}, nil
		}
		return s.clawhub.Search(ctx, principal, q, cursor)

	case "":
		// 聚合模式：先查平台库，再追加公共库（公共库失败时降级，不阻断）。
		// platform 来源无游标，cursor 用空串（游标仅对 clawhub 有意义）。
		page, err := s.platform.Search(ctx, principal, q, "")
		if err != nil {
			// 平台库查询失败时直接上报，平台库是聚合的基础来源。
			return SkillPage{}, err
		}
		if s.clawhub != nil {
			// 公共库失败不阻断：追加成功，忽略失败（降级展示）。
			if cp, cerr := s.clawhub.Search(ctx, principal, q, cursor); cerr == nil {
				page.Entries = append(page.Entries, cp.Entries...)
				// 聚合模式下以公共库游标为下一页游标（platform 无游标）。
				page.NextCursor = cp.NextCursor
			}
		}
		return page, nil

	default:
		// 未知来源：返回哨兵错误，handler 层映射为 400 Bad Request。
		return SkillPage{}, ErrSkillMarketSourceUnknown
	}
}

// Versions 返回指定 skill 的全部历史版本号（详情页版本列表用）。
//   - source="platform"：查平台库该 name 的所有版本。
//   - source="clawhub"：查公共库该 slug 的所有版本；clawhub 未配置（nil）时返回空列表。
//   - 其他值：返回 ErrSkillMarketSourceUnknown（handler 层映射为 400）。
func (s *SkillLibraryService) Versions(ctx context.Context, principal auth.Principal, source, ref string) ([]string, error) {
	switch source {
	case "platform":
		return s.platform.Versions(ctx, principal, ref)
	case "clawhub":
		// 未配置公共库：返回空版本列表，不报错（与 List 的降级口径一致）。
		if s.clawhub == nil {
			return []string{}, nil
		}
		return s.clawhub.Versions(ctx, principal, ref)
	default:
		return nil, ErrSkillMarketSourceUnknown
	}
}
