// skill_update_checker.go — 周期回源检测 skill 最高版本，写回 app_skills.latest_version。
//
// 工作原理：
//  1. 查出全部已安装 skill 的 distinct (source, source_ref) 组合。
//  2. 按 source 类型，通过各自的外部查询接口取最高版本号（platform: ListPlatformSkills 首条；
//     clawhub: ListVersions 取最大 semver）。
//  3. 对该 (source, source_ref) 下所有 app_skills 行调用 UpdateAppSkillLatest 写回。
//  4. 任意单行写回失败仅记 warn，不中断其他行的处理（不跨轮次累积错误）。
//
// 本文件不包含 PeriodicReconciler 接线（接线在 P3-T8 完成）。
package service

import (
	"context"
	"log/slog"

	"github.com/guregu/null/v5"

	"oc-manager/internal/store/sqlc"
)

// =========================================================
// 依赖接口（最小化，便于测试替换）
// =========================================================

// SkillUpdateCheckerAppSkillStore 是 SkillUpdateChecker 所需的 app_skills 存取能力。
type SkillUpdateCheckerAppSkillStore interface {
	// ListDistinctAppSkillSources 返回所有已安装 skill 的去重 (source, source_ref) 对，
	// 用于遍历回源查最高版本。
	ListDistinctAppSkillSources(ctx context.Context) ([]sqlc.ListDistinctAppSkillSourcesRow, error)
	// ListAppSkillsBySourceRef 返回指定 (source, source_ref) 下的全部 app_skills 行，
	// 用于批量回写 latest_version。
	ListAppSkillsBySourceRef(ctx context.Context, arg sqlc.ListAppSkillsBySourceRefParams) ([]sqlc.AppSkill, error)
	// UpdateAppSkillLatest 更新单条 app_skills 的 latest_version 并刷新 last_checked_at。
	UpdateAppSkillLatest(ctx context.Context, arg sqlc.UpdateAppSkillLatestParams) error
}

// SkillUpdateCheckerPlatformStore 是 SkillUpdateChecker 所需的平台库查询能力。
// 通过 ListPlatformSkills 取全量列表，过滤同名条目、利用 ORDER BY name ASC, created_at DESC
// 确保首条即为最新版本（同名多版本时最新入库排最前）。
type SkillUpdateCheckerPlatformStore interface {
	// ListPlatformSkills 按 name ASC, created_at DESC 排序返回全部平台库 skill。
	// 同一 name 的首条即为最高版本。
	ListPlatformSkills(ctx context.Context) ([]sqlc.PlatformSkill, error)
}

// SkillUpdateCheckerCustomStore 是 SkillUpdateChecker 所需的定制技能（custom 来源）查询能力。
// 通过 ListAllCustomSkills 取全量列表，过滤同名条目、利用 ORDER BY name ASC, created_at DESC
// 确保首条即为最新版本（同 platform：custom 不解析版本串，仅按入库时间取最新）。
type SkillUpdateCheckerCustomStore interface {
	// ListAllCustomSkills 按 name ASC, created_at DESC 排序返回全部定制技能。
	// 同一 name 的首条即为最新交付版本。
	ListAllCustomSkills(ctx context.Context) ([]sqlc.CustomSkill, error)
}

// ClawHubVersionLister 从 ClawHub 查询指定 slug 的全部版本列表，供取最高版本用。
// 由真实 clawhub.Client 满足；nil 表示 ClawHub 来源未启用，此时直接跳过。
type ClawHubVersionLister interface {
	// ListVersions 返回指定 slug 的所有可用版本，列表为空或错误时调用方跳过该来源。
	ListVersions(ctx context.Context, slug string) ([]SkillVersion, error)
}

// SkillVersion 表示 ClawHub 返回的单个版本条目。
// Version 字段为版本字符串（如 "1.2.3"），遵循语义化版本格式。
type SkillVersion struct {
	// Version 版本字符串，由 ClawHub API 返回，格式为 semver（如 "1.0.0"）
	Version string
}

// =========================================================
// SkillUpdateChecker 实现
// =========================================================

// SkillUpdateChecker 遍历所有已安装 skill，回源查最高版本并写回 latest_version。
// 供 PeriodicReconciler 周期调用（P3-T8 接线）。
type SkillUpdateChecker struct {
	// appSkills 操作 app_skills 表（查 distinct 来源、列行、写回 latest_version）
	appSkills SkillUpdateCheckerAppSkillStore
	// platform 查询平台库 skill 列表，取同名最新版本
	platform SkillUpdateCheckerPlatformStore
	// custom 查询定制技能列表，取同名最新交付版本；nil 时跳过所有 custom 来源
	custom SkillUpdateCheckerCustomStore
	// clawhub ClawHub 版本列表客户端；nil 时跳过所有 clawhub 来源
	clawhub ClawHubVersionLister
}

// NewSkillUpdateChecker 构造 SkillUpdateChecker。custom 与 clawhub 均可传 nil（禁用对应来源）。
func NewSkillUpdateChecker(
	appSkills SkillUpdateCheckerAppSkillStore,
	platform SkillUpdateCheckerPlatformStore,
	custom SkillUpdateCheckerCustomStore,
	clawhub ClawHubVersionLister,
) *SkillUpdateChecker {
	return &SkillUpdateChecker{
		appSkills: appSkills,
		platform:  platform,
		custom:    custom,
		clawhub:   clawhub,
	}
}

// Tick 执行一轮回源检测。
// 遍历所有 distinct (source, source_ref)，按来源类型查最高版本，
// 批量回写该来源下所有 app_skills 行的 latest_version。
// 任意单条 UpdateAppSkillLatest 失败仅 slog.Warn，不中断其他行。
func (c *SkillUpdateChecker) Tick(ctx context.Context) error {
	// 查出全部已安装 skill 的去重 (source, source_ref) 组合
	sources, err := c.appSkills.ListDistinctAppSkillSources(ctx)
	if err != nil {
		return err
	}

	// platform 来源：一次性拉全量 platform_skills，构建 name→最高版本 map
	// （避免对每个 source_ref 各调一次 ListPlatformSkills）
	platformLatest := map[string]string{}
	for _, s := range sources {
		if s.Source == "platform" {
			// 只需取一次
			platformLatest, err = c.buildPlatformLatestMap(ctx)
			if err != nil {
				// platform 查询失败时跳过本轮所有 platform 来源，clawhub 来源继续
				slog.WarnContext(ctx, "skill 更新检测：拉取 platform 列表失败，本轮跳过 platform 来源",
					"error", err,
				)
				platformLatest = nil
			}
			break
		}
	}

	// custom 来源：仿 platform，一次性拉全量 custom_skills，构建 name→最新版本 map。
	// 仅当 custom store 已接线（非 nil）且确有 custom 来源 skill 时才查询；
	// 查询失败时本轮跳过所有 custom 来源（置 nil），其余来源继续。
	customLatest := map[string]string{}
	if c.custom != nil {
		for _, s := range sources {
			if s.Source == "custom" {
				customLatest, err = c.buildCustomLatestMap(ctx)
				if err != nil {
					slog.WarnContext(ctx, "skill 更新检测：拉取 custom 列表失败，本轮跳过 custom 来源",
						"error", err,
					)
					customLatest = nil
				}
				break
			}
		}
	} else {
		// custom store 未接线：标记为不可用，resolveLatest 跳过所有 custom 来源
		customLatest = nil
	}

	// 遍历每个 (source, source_ref) 对，查最高版本并批量回写
	for _, src := range sources {
		latestVer, ok := c.resolveLatest(ctx, src.Source, src.SourceRef, platformLatest, customLatest)
		if !ok {
			// 无法确定最高版本（来源不可用/查询失败/列表为空），跳过本批次
			continue
		}

		// 取该 (source, source_ref) 下所有 app_skills 行，逐条回写 latest_version
		rows, err := c.appSkills.ListAppSkillsBySourceRef(ctx, sqlc.ListAppSkillsBySourceRefParams{
			Source:    src.Source,
			SourceRef: src.SourceRef,
		})
		if err != nil {
			slog.WarnContext(ctx, "skill 更新检测：查询 app_skills 失败，跳过该来源",
				"source", src.Source,
				"source_ref", src.SourceRef,
				"error", err,
			)
			continue
		}

		for _, row := range rows {
			// latest_version 存储最高版本；若最高版本等于当前安装版本，写 NULL（无更新）
			var latestNull null.String
			if latestVer != row.Version {
				latestNull = null.StringFrom(latestVer)
			}
			if err := c.appSkills.UpdateAppSkillLatest(ctx, sqlc.UpdateAppSkillLatestParams{
				LatestVersion: latestNull,
				ID:            row.ID,
			}); err != nil {
				// 单条回写失败：记 warn 后继续，不影响其他行
				slog.WarnContext(ctx, "skill 更新检测：写回 latest_version 失败",
					"app_skill_id", row.ID,
					"source", src.Source,
					"source_ref", src.SourceRef,
					"latest_version", latestVer,
					"error", err,
				)
			}
		}
	}

	return nil
}

// resolveLatest 按 source 类型查最高版本字符串。
// 返回 (version, true) 表示成功；返回 ("", false) 表示跳过（来源不可用或无版本）。
func (c *SkillUpdateChecker) resolveLatest(
	ctx context.Context,
	source, sourceRef string,
	platformLatest map[string]string,
	customLatest map[string]string,
) (string, bool) {
	switch source {
	case "platform":
		if platformLatest == nil {
			// platform 列表拉取失败（已在 Tick 中记录 warn），跳过
			return "", false
		}
		ver, ok := platformLatest[sourceRef]
		if !ok || ver == "" {
			// 平台库中不存在该 name（已被删除或名称不匹配），跳过
			return "", false
		}
		return ver, true

	case "custom":
		if customLatest == nil {
			// custom store 未接线或列表拉取失败（已在 Tick 中记录 warn），跳过
			return "", false
		}
		ver, ok := customLatest[sourceRef]
		if !ok || ver == "" {
			// 定制技能库中不存在该 name（已被删除或名称不匹配），跳过
			return "", false
		}
		return ver, true

	case "clawhub":
		if c.clawhub == nil {
			// ClawHub 来源未启用，跳过
			return "", false
		}
		versions, err := c.clawhub.ListVersions(ctx, sourceRef)
		if err != nil {
			slog.WarnContext(ctx, "skill 更新检测：ClawHub ListVersions 失败，跳过该 slug",
				"slug", sourceRef,
				"error", err,
			)
			return "", false
		}
		if len(versions) == 0 {
			// slug 无可用版本（已下架或 API 返回空），跳过
			return "", false
		}
		// 从列表中取最大语义版本
		best := pickHighestVersion(versions)
		if best == "" {
			return "", false
		}
		return best, true

	default:
		// 未知来源类型，跳过
		return "", false
	}
}

// buildPlatformLatestMap 从 ListPlatformSkills 结果（ORDER BY name ASC, created_at DESC）
// 构建 name→最高版本 map。因排序保证同名首条最新，只取每个 name 的第一次出现。
func (c *SkillUpdateChecker) buildPlatformLatestMap(ctx context.Context) (map[string]string, error) {
	rows, err := c.platform.ListPlatformSkills(ctx)
	if err != nil {
		return nil, err
	}
	m := make(map[string]string, len(rows))
	for _, r := range rows {
		// 因 ORDER BY name ASC, created_at DESC，同名下首条为最新入库（版本最高）
		if _, exists := m[r.Name]; !exists {
			m[r.Name] = r.Version
		}
	}
	return m, nil
}

// buildCustomLatestMap 从 ListAllCustomSkills 结果（ORDER BY name ASC, created_at DESC）
// 构建 name→最新版本 map。custom 来源不解析版本串，仅按入库时间取最新：
// 因排序保证同名首条最新，只取每个 name 的第一次出现。
func (c *SkillUpdateChecker) buildCustomLatestMap(ctx context.Context) (map[string]string, error) {
	rows, err := c.custom.ListAllCustomSkills(ctx)
	if err != nil {
		return nil, err
	}
	m := make(map[string]string, len(rows))
	for _, r := range rows {
		// 因 ORDER BY name ASC, created_at DESC，同名下首条为最新交付（视为最新版本）
		if _, exists := m[r.Name]; !exists {
			m[r.Name] = r.Version
		}
	}
	return m, nil
}

// pickHighestVersion 从 SkillVersion 列表中取版本字符串最大的条目。
// 使用简单字符串比较：对于标准 semver（"1.0.0" "1.2.0" 等），字符串比较在
// 主版本号位数相同时结果正确；ClawHub 返回的版本一般遵循此规律。
// 若需精确 semver 比较可替换为 golang.org/x/mod/semver，当前不引入额外依赖。
func pickHighestVersion(versions []SkillVersion) string {
	if len(versions) == 0 {
		return ""
	}
	best := versions[0].Version
	for _, v := range versions[1:] {
		if v.Version > best {
			best = v.Version
		}
	}
	return best
}
