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
	// custom 是定制技能来源，可为 nil（未启用 custom 时）。
	// nil 时市场不展示定制技能，指定 source=custom 返回空列表（与 clawhub nil 同口径降级）。
	custom SkillSource
}

// NewSkillLibraryService 构造聚合 service。
// platform 必须非 nil；clawhub / custom 可为 nil（未接入对应来源时降级，仅影响该来源）。
func NewSkillLibraryService(platform, clawhub, custom SkillSource) *SkillLibraryService {
	return &SkillLibraryService{platform: platform, clawhub: clawhub, custom: custom}
}

// List 按 source 参数返回市场条目：
//   - "platform"：只查平台库，游标 cursor 透传（platform 无游标则忽略）。
//   - "custom"：只查定制技能库；custom 未配置（nil）时返回空列表。
//   - "clawhub"：只查公共库；clawhub 未配置（nil）时返回空列表。
//   - ""（空字符串）：聚合三者，platform 条目在前、custom 其次、clawhub 在后；
//     custom / ClawHub 调用失败时降级仅返回前序来源条目（不阻断请求，spec 要求）。
//   - 其他值：返回 ErrSkillMarketSourceUnknown（handler 层映射为 400）。
func (s *SkillLibraryService) List(ctx context.Context, principal auth.Principal, source, q, cursor string) (SkillPage, error) {
	switch source {
	case "platform":
		// 仅查平台库，cursor 透传（PlatformSource 会忽略）。
		return s.platform.Search(ctx, principal, q, cursor)

	case "custom":
		// 仅查定制技能库：未配置时直接返回空列表，不报错（与 clawhub nil 同口径）。
		if s.custom == nil {
			return SkillPage{Entries: []SkillEntry{}}, nil
		}
		return s.custom.Search(ctx, principal, q, cursor)

	case "clawhub":
		// 仅查公共库：未配置时直接返回空列表，不报错。
		if s.clawhub == nil {
			return SkillPage{Entries: []SkillEntry{}}, nil
		}
		return s.clawhub.Search(ctx, principal, q, cursor)

	case "":
		// 聚合模式：先查平台库，再追加定制技能与公共库（任一追加来源失败时降级，不阻断）。
		// platform 来源无游标，cursor 用空串（游标仅对 clawhub 有意义）。
		page, err := s.platform.Search(ctx, principal, q, "")
		if err != nil {
			// 平台库查询失败时直接上报，平台库是聚合的基础来源。
			return SkillPage{}, err
		}
		if s.custom != nil {
			// 定制技能失败不阻断：追加成功，忽略失败（降级展示）。custom 紧随 platform，无游标。
			if xp, xerr := s.custom.Search(ctx, principal, q, ""); xerr == nil {
				page.Entries = append(page.Entries, xp.Entries...)
			}
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

// Detail 返回指定 skill 的富详情 + 版本列表（详情页用）。
//   - source="platform"：查平台库该 name 的详情与版本。
//   - source="custom"：查定制技能库该 name 的详情与版本；custom 未配置（nil）时返回空。
//   - source="clawhub"：查公共库该 slug 的详情与版本；clawhub 未配置（nil）时返回空。
//   - 其他值：返回 ErrSkillMarketSourceUnknown（handler 层映射为 400）。
func (s *SkillLibraryService) Detail(ctx context.Context, principal auth.Principal, source, ref string) (SkillDetailResult, []SkillVersionResult, error) {
	var src SkillSource
	switch source {
	case "platform":
		src = s.platform
	case "custom":
		// 未配置定制技能库：返回空详情/空版本，不报错（与 clawhub 的降级口径一致）。
		if s.custom == nil {
			return SkillDetailResult{Source: "custom", SourceRef: ref}, []SkillVersionResult{}, nil
		}
		src = s.custom
	case "clawhub":
		// 未配置公共库：返回空详情/空版本，不报错（与 List 的降级口径一致）。
		if s.clawhub == nil {
			return SkillDetailResult{Source: "clawhub", SourceRef: ref}, []SkillVersionResult{}, nil
		}
		src = s.clawhub
	default:
		return SkillDetailResult{}, nil, ErrSkillMarketSourceUnknown
	}
	detail, err := src.Detail(ctx, principal, ref)
	if err != nil {
		return SkillDetailResult{}, nil, err
	}
	versions, err := src.Versions(ctx, principal, ref)
	if err != nil {
		return SkillDetailResult{}, nil, err
	}
	return detail, versions, nil
}

// Download 取指定 skill 某版本归档的原始字节与扩展名（platform=tar，clawhub=zip），供详情页下载。
// 仅平台管理员可调用（CanDownloadSkillArchive）；ref/version 缺一不可。
//   - source="platform"：复用平台库归档（GetForInstall）。
//   - source="clawhub"：回源 ClawHub 下载；clawhub 未配置（nil）时返回 ErrSkillMarketSourceUnknown。
//   - 其他值：ErrSkillMarketSourceUnknown。
func (s *SkillLibraryService) Download(ctx context.Context, principal auth.Principal, source, ref, version string) ([]byte, string, error) {
	// 下载会拿到完整归档原始字节，限平台管理员。
	if !auth.CanDownloadSkillArchive(principal) {
		return nil, "", ErrSkillMarketDenied
	}
	// ref（name/slug）与 version 都必填，否则无法定位归档。
	if ref == "" || version == "" {
		return nil, "", ErrSkillMarketInvalid
	}
	var src SkillSource
	switch source {
	case "platform":
		src = s.platform
	case "clawhub":
		// 未配置公共库时无法下载 clawhub 归档。
		if s.clawhub == nil {
			return nil, "", ErrSkillMarketSourceUnknown
		}
		src = s.clawhub
	default:
		return nil, "", ErrSkillMarketSourceUnknown
	}
	return src.Download(ctx, ref, version)
}
